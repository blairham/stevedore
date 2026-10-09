// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/blairham/stevedore/internal/progress"
	"github.com/blairham/stevedore/internal/run"
)

// Split mode spreads one multi-arch build across native runners: each CI leg
// runs `release --split <platform>`, building only that platform and pushing
// it untagged, by digest. The digest is recorded as a file under
// dist/digests/<image-id>/, named after the platform(s) it covers. A final
// `merge` run reads those files, stitches the digests into one manifest list
// per repository (`docker buildx imagetools create`, pushed by digest), gates
// it, and only then tags it and finishes the release. In CI the legs and the merge job share dist/digests/ via artifacts.

// platformFile renders the digest filename for a leg's platforms:
// linux/arm64 → linux-arm64; a multi-platform leg joins with commas.
func platformFile(platforms []string) string {
	parts := make([]string, len(platforms))
	for i, p := range platforms {
		parts[i] = strings.ReplaceAll(p, "/", "-")
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// legPlatforms returns the leg's platforms that an image is configured for,
// in the leg's order. A leg is driven by a platform matrix, not by the image,
// so `--split linux/arm64` reaches amd64-only images too; building those for
// arm64 would publish a platform their config excludes.
func legPlatforms(split, configured []string) []string {
	want := make(map[string]bool, len(configured))
	for _, p := range configured {
		want[p] = true
	}
	var out []string
	for _, p := range split {
		if want[p] {
			out = append(out, p)
		}
	}
	return out
}

// splitLegGroups moves every build group none of whose configured platforms
// is in the leg onto the skipped list, with a reason that names both sides.
// Group members share a build key, which includes the platforms, so the
// first member speaks for the group.
func splitLegGroups(toBuild [][]imageEval, skipped []imageEval, split []string) ([][]imageEval, []imageEval) {
	kept := toBuild[:0:0]
	for _, grp := range toBuild {
		configured := grp[0].plan.Image.Platforms
		if len(legPlatforms(split, configured)) > 0 {
			kept = append(kept, grp)
			continue
		}
		reason := fmt.Sprintf("not configured for split platform(s) %s; image platforms: %s",
			strings.Join(split, ","), strings.Join(configured, ","))
		for _, m := range grp {
			m.reason = reason
			skipped = append(skipped, m)
		}
	}
	return kept, skipped
}

func splitDigestDir(dir, dist, id string) string {
	return filepath.Join(dir, dist, "digests", id)
}

// writeSplitDigest records a leg's pushed digest for every group member, so
// the merge run can look it up by any member's image ID.
func writeSplitDigest(dir, dist string, ids, platforms []string, digest string) error {
	name := platformFile(platforms)
	for _, id := range ids {
		d := splitDigestDir(dir, dist, id)
		if err := mkdirDist(d); err != nil {
			return fmt.Errorf("create digest dir: %w", err)
		}
		path := filepath.Join(d, name)
		if err := writeDistFile(path, []byte(digest+"\n")); err != nil {
			return fmt.Errorf("write split digest: %w", err)
		}
		progress.Printf(progressOut, "    digest recorded: %s\n", path)
	}
	return nil
}

// splitVersionFile sits beside an image's digest files and holds the version
// the leg resolved for it. The merge run compares it with the version it
// resolves itself: the legs' builds already carry that version (labels, build
// args), so tagging them with another one would publish a mislabelled image.
const splitVersionFile = "version"

// writeSplitVersions records each group member's resolved version for the
// merge run's cross-check (see checkMergeInputs). Every leg of a plan writes
// the same content, so legs sharing one dist/digests overwrite it harmlessly.
func writeSplitVersions(dir, dist string, grp []imageEval) error {
	for _, m := range grp {
		if m.plan.Version == "" {
			continue
		}
		path := filepath.Join(splitDigestDir(dir, dist, m.plan.Image.ID), splitVersionFile)
		if err := writeDistFile(path, []byte(m.plan.Version+"\n")); err != nil {
			return fmt.Errorf("write split version: %w", err)
		}
	}
	return nil
}

// hasSplitDigests reports whether a leg recorded any digest for id.
func hasSplitDigests(dir, dist, id string) bool {
	entries, err := os.ReadDir(splitDigestDir(dir, dist, id))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && e.Name() != splitVersionFile {
			return true
		}
	}
	return false
}

// checkMergeInputs refuses a merge whose own decisions disagree with what the
// split legs built. Without --only, merge re-runs change detection and version
// resolution rather than reading the plan, and either can come out differently
// by the time it runs (a marker moved, a registry gained a tag, a `command`
// strategy is not deterministic). Two disagreements are caught:
//
//   - an image the legs built and recorded a version for, which merge resolves
//     to a different version: the per-arch images carry the legs' version, so
//     tagging them with merge's would mislabel them;
//   - an image the legs pushed digests for that merge's change detection skips
//     (only checked without --only, where merge chose the set itself): it would
//     be left pushed but never tagged, signed or released.
//
// Either way the fix is the same: give merge the plan's `only` and `pins`.
func checkMergeInputs(o Options, dist string, toBuild [][]imageEval, skipped []imageEval) error {
	var problems []string
	for _, grp := range toBuild {
		for _, m := range grp {
			data, err := os.ReadFile(
				filepath.Clean(filepath.Join(splitDigestDir(o.Dir, dist, m.plan.Image.ID), splitVersionFile)),
			)
			if err != nil {
				continue // an older leg, or no digests at all (mergeGroup reports that)
			}
			if built := strings.TrimSpace(string(data)); built != "" && built != m.plan.Version {
				problems = append(problems, fmt.Sprintf("image %s: the split legs built version %s, but merge resolved %s",
					m.plan.Image.ID, built, m.plan.Version))
			}
		}
	}
	if len(o.Only) == 0 {
		for _, m := range skipped {
			if hasSplitDigests(o.Dir, dist, m.plan.Image.ID) {
				problems = append(
					problems,
					fmt.Sprintf("image %s: the split legs pushed digests for it (%s), but merge skipped it: %s",
						m.plan.Image.ID, splitDigestDir(o.Dir, dist, m.plan.Image.ID), m.reason),
				)
			}
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf(
		"merge disagrees with what the split legs built:\n  %s\npass merge the plan's only and pins outputs (--only <ids> --pin-version <id>=<version>) so it releases exactly what the legs built",
		strings.Join(problems, "\n  "),
	)
}

// readSplitDigests loads an image's per-arch digests and the (sanitized)
// platforms they cover. Filenames are stable-sorted so merge output is
// deterministic. A file covering a platform outside configured is an error,
// not something to skip: it is either a leftover from an earlier run in a
// persistent dist/ or a leg that built a platform the image excludes, and in
// both cases merging it would publish a platform the config does not ask for.
func readSplitDigests(
	dir, dist, id string,
	configured []string,
) (digests []string, covered map[string]bool, err error) {
	allowed := make(map[string]bool, len(configured))
	for _, p := range configured {
		allowed[strings.ReplaceAll(p, "/", "-")] = true
	}
	d := splitDigestDir(dir, dist, id)
	entries, err := os.ReadDir(d)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"image %s: no split digests under %s (run `stevedore release --split <platform>` legs first): %w",
			id,
			d,
			err,
		)
	}
	covered = map[string]bool{}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && e.Name() != splitVersionFile {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		digest, platforms, err := readSplitDigest(d, name, id, allowed, configured)
		if err != nil {
			return nil, nil, err
		}
		digests = append(digests, digest)
		for _, p := range platforms {
			covered[p] = true
		}
	}
	if len(digests) == 0 {
		return nil, nil, fmt.Errorf(
			"image %s: no split digests under %s (run `stevedore release --split <platform>` legs first)",
			id,
			d,
		)
	}
	return digests, covered, nil
}

// readSplitDigest reads one leg's digest file, named for the (sanitized)
// platforms it covers, and refuses one covering a platform outside allowed.
func readSplitDigest(
	d, name, id string,
	allowed map[string]bool,
	configured []string,
) (digest string, platforms []string, err error) {
	data, err := os.ReadFile(filepath.Clean(filepath.Join(d, name)))
	if err != nil {
		return "", nil, fmt.Errorf("read split digest: %w", err)
	}
	digest = strings.TrimSpace(string(data))
	if digest == "" {
		return "", nil, fmt.Errorf("image %s: empty split digest file %s", id, name)
	}
	platforms = strings.Split(name, ",")
	var unexpected []string
	for _, p := range platforms {
		if !allowed[p] {
			unexpected = append(unexpected, p)
		}
	}
	if len(unexpected) > 0 {
		return "", nil, fmt.Errorf(
			"image %s: split digest %s covers %s, which the image does not configure (platforms: %s) — remove the stale file, or the leg that wrote it",
			id,
			filepath.Join(d, name),
			strings.Join(unexpected, ","),
			strings.Join(configured, ","),
		)
	}
	return digest, platforms, nil
}

// mergeGroup assembles the split legs' digests into one manifest list per
// repository and returns its digest. The list is pushed untagged, by digest:
// tags are applied only after the gates pass. It fails when a configured
// platform has no recorded digest, so a partial matrix (a leg that never ran
// or failed to upload its digests) can't publish an incomplete image, and when
// a digest covers a platform the image does not configure, so it can't
// publish an unwanted one either.
func mergeGroup(r *run.Runner, o Options, rep ImagePlan, dist string, repos []string) (string, error) {
	digests, covered, err := readSplitDigests(o.Dir, dist, rep.Image.ID, rep.Image.Platforms)
	if err != nil {
		return "", err
	}
	var missing []string
	for _, p := range rep.Image.Platforms {
		if !covered[strings.ReplaceAll(p, "/", "-")] {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		return "", fmt.Errorf(
			"image %s: no split digest covers platform(s) %s — did every matrix leg run and share dist/digests?",
			rep.Image.ID,
			strings.Join(missing, ", "),
		)
	}

	// imagetools create has no "push untagged" switch, but it accepts a digest
	// reference as its target. So compute the list's digest first: --dry-run
	// prints exactly the bytes a real create pushes (Capture trims only the
	// trailing newline), and the per-arch digests are identical across repos
	// (content-addressed; the legs pushed the same blobs to every repo), so one
	// list serves them all.
	// The annotations go on both, so the computed digest is the pushed one.
	annotations := indexAnnotations(rep.Annotations)
	dryArgs := imagetoolsCreate(append(append([]string{"--dry-run"}, annotations...), sources(repos[0], digests)...)...)
	var listDigest string
	if o.DryRun {
		r.Preview("docker", dryArgs...)
		listDigest = dryRunDigest("", true)
	} else {
		list, err := r.Capture("docker", dryArgs...)
		if err != nil {
			return "", err
		}
		listDigest = fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(list)))
	}

	// One untagged create per repository, addressed by the list's digest.
	for _, repo := range repos {
		args := imagetoolsCreate(
			append(append([]string{"--tag", repo + "@" + listDigest}, annotations...), sources(repo, digests)...)...,
		)
		if err := r.Run("docker", args...); err != nil {
			return "", err
		}
	}
	if o.DryRun {
		return "", nil // Release substitutes the dry-run placeholder
	}
	return listDigest, nil
}

// indexAnnotations renders the --annotation flags for a merged manifest list:
// index level, since the per-platform manifests were annotated (and pushed,
// content-addressed) by their split legs and cannot be changed here.
func indexAnnotations(annotations map[string]string) []string {
	keys := make([]string, 0, len(annotations))
	for k := range annotations {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	args := make([]string, 0, 2*len(keys))
	for _, k := range keys {
		args = append(args, "--annotation", "index:"+k+"="+annotations[k])
	}
	return args
}

// imagetoolsCreate renders a `docker buildx imagetools create` argument list.
func imagetoolsCreate(args ...string) []string {
	return append([]string{"buildx", "imagetools", "create"}, args...)
}

// sources renders the per-arch digests as repo@digest references.
func sources(repo string, digests []string) []string {
	out := make([]string, len(digests))
	for i, d := range digests {
		out[i] = repo + "@" + d
	}
	return out
}
