package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/blairham/stevedore/internal/run"
)

// Split mode spreads one multi-arch build across native runners: each CI leg
// runs `release --split <platform>`, building only that platform and pushing
// it untagged, by digest. The digest is recorded as a file under
// dist/digests/<image-id>/, named after the platform(s) it covers. A final
// `merge` run reads those files, stitches the digests into one tagged manifest
// list per repository (`docker buildx imagetools create`), and finishes the
// release. In CI the legs and the merge job share dist/digests/ via artifacts.

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

func splitDigestDir(dir, dist, id string) string {
	return filepath.Join(dir, dist, "digests", id)
}

// writeSplitDigest records a leg's pushed digest for every group member, so
// the merge run can look it up by any member's image ID.
func writeSplitDigest(dir, dist string, ids, platforms []string, digest string) error {
	name := platformFile(platforms)
	for _, id := range ids {
		d := splitDigestDir(dir, dist, id)
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("create digest dir: %w", err)
		}
		path := filepath.Join(d, name)
		if err := os.WriteFile(path, []byte(digest+"\n"), 0o644); err != nil {
			return fmt.Errorf("write split digest: %w", err)
		}
		fmt.Fprintf(progress, "    digest recorded: %s\n", path)
	}
	return nil
}

// readSplitDigests loads an image's per-arch digests and the (sanitized)
// platforms they cover. Filenames are stable-sorted so merge output is
// deterministic.
func readSplitDigests(dir, dist, id string) (digests []string, covered map[string]bool, err error) {
	d := splitDigestDir(dir, dist, id)
	entries, err := os.ReadDir(d)
	if err != nil {
		return nil, nil, fmt.Errorf("image %s: no split digests under %s (run `stevedore release --split <platform>` legs first): %w", id, d, err)
	}
	covered = map[string]bool{}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(d, name))
		if err != nil {
			return nil, nil, fmt.Errorf("read split digest: %w", err)
		}
		digest := strings.TrimSpace(string(data))
		if digest == "" {
			return nil, nil, fmt.Errorf("image %s: empty split digest file %s", id, name)
		}
		digests = append(digests, digest)
		for p := range strings.SplitSeq(name, ",") {
			covered[p] = true
		}
	}
	if len(digests) == 0 {
		return nil, nil, fmt.Errorf("image %s: no split digests under %s (run `stevedore release --split <platform>` legs first)", id, d)
	}
	return digests, covered, nil
}

// mergeGroup assembles the split legs' digests into one tagged manifest list
// per repository and returns the merged manifest-list digest. It fails when a
// configured platform has no recorded digest, so a partial matrix (a leg that
// never ran or failed to upload its digests) can't publish an incomplete image.
func mergeGroup(r *run.Runner, o Options, rep ImagePlan, dist string, repos, refs []string) (string, error) {
	digests, covered, err := readSplitDigests(o.Dir, dist, rep.Image.ID)
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
		return "", fmt.Errorf("image %s: no split digest covers platform(s) %s — did every matrix leg run and share dist/digests?",
			rep.Image.ID, strings.Join(missing, ", "))
	}

	// One `imagetools create` per repository: its tags, its digest sources.
	// The per-arch digests are identical across repos (content-addressed; the
	// legs pushed the same blobs to every repo).
	for _, repo := range repos {
		args := []string{"buildx", "imagetools", "create"}
		for _, ref := range refs {
			if strings.HasPrefix(ref, repo+":") {
				args = append(args, "--tag", ref)
			}
		}
		for _, d := range digests {
			args = append(args, repo+"@"+d)
		}
		if err := r.Run("docker", args...); err != nil {
			return "", err
		}
	}
	if o.DryRun {
		return "", nil // Release substitutes the dry-run placeholder
	}
	return r.Capture("docker", "buildx", "imagetools", "inspect", refs[0], "--format", "{{.Manifest.Digest}}")
}
