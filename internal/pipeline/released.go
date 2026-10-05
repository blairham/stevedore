// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/blairham/stevedore/internal/run"
)

// A per-commit tag ({{ .ShortCommit }}) names exactly one build of one commit,
// and registries with tag immutability (ECR, Harbor, GHCR rules) refuse to
// move it. Re-running a release of an already-released commit therefore used
// to push a fresh version tag and then fail on the commit tag, leaving a
// partial tag set behind a red job. So before building, every commit tag of a
// group is looked up: if they all already exist and were built from this
// commit the image is already released, and if any names a different commit
// the run fails before anything is pushed.

// revisionLabel is the OCI label recording the commit an image was built from.
const revisionLabel = "org.opencontainers.image.revision"

// existingTag is what a registry reports for a tag that already exists.
type existingTag struct {
	Digest   string
	Revision string // empty when the image carries no revision label
}

// isCommitTag reports whether ref's tag names the commit: it contains the full
// or abbreviated HEAD SHA.
func isCommitTag(ref, commit, short string) bool {
	i := strings.LastIndex(ref, ":")
	if i < 0 || strings.Contains(ref[i:], "/") {
		return false
	}
	tag := ref[i+1:]
	return (commit != "" && strings.Contains(tag, commit)) || (short != "" && strings.Contains(tag, short))
}

// inspectTag looks ref up with `docker buildx imagetools inspect`; found is
// false when the tag does not exist.
func inspectTag(r *run.Runner, ref string) (tag existingTag, found bool, err error) {
	out, err := r.Capture("docker", "buildx", "imagetools", "inspect",
		"--format", `{"digest":{{json .Manifest.Digest}},"image":{{json .Image}}}`, ref)
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && strings.Contains(strings.ToLower(string(ee.Stderr)), "not found") {
			return existingTag{}, false, nil
		}
		return existingTag{}, false, err
	}
	var doc struct {
		Digest string          `json:"digest"`
		Image  json.RawMessage `json:"image"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		return existingTag{}, false, fmt.Errorf("parse imagetools inspect %s: %w", ref, err)
	}
	return existingTag{Digest: doc.Digest, Revision: imageRevision(doc.Image)}, true, nil
}

// imageRevision reads the revision label from imagetools' .Image, which is the
// image config for a single-platform manifest and a platform → config map for
// an index. Every platform of one build carries the same label; the first
// found (in platform order) wins.
func imageRevision(raw json.RawMessage) string {
	type cfg struct {
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"config"`
	}
	var single cfg
	if json.Unmarshal(raw, &single) == nil && single.Config.Labels[revisionLabel] != "" {
		return single.Config.Labels[revisionLabel]
	}
	var multi map[string]cfg
	if json.Unmarshal(raw, &multi) != nil {
		return ""
	}
	keys := make([]string, 0, len(multi))
	for k := range multi {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if v := multi[k].Config.Labels[revisionLabel]; v != "" {
			return v
		}
	}
	return ""
}

// releasedAt is the evidence that an image was already released: one of its
// commit tags and the digest that tag names.
type releasedAt struct {
	Ref, Digest string
}

// headCommit returns the full and abbreviated HEAD SHA, empty when unknown.
func headCommit(p *Prepared) (commit, short string) {
	if p.Ctx == nil {
		return "", ""
	}
	return p.Ctx.Commit, p.Ctx.ShortCommit
}

// releasedMembers looks up every commit tag of the group before anything is
// built and returns, for each member already released from this commit, the
// ref that proves it. A member counts as released only when ALL its commit
// tags exist; a commit tag that names a different build (its revision label
// says another commit), or a set where some commit tags exist and others do
// not, is an error — continuing would either fail against an immutable tag
// midway or leave the tag set split across two builds.
//
// A lookup that fails for any reason other than "not found" is reported and
// treated as unknown: the release proceeds, and applyTags pushes the commit
// tags first so a collision still fails before any other tag moves.
func releasedMembers(o Options, p *Prepared, r *run.Runner, grp []imageEval) (map[string]releasedAt, error) {
	released := map[string]releasedAt{}
	commit, short := headCommit(p)
	if o.DryRun || o.NoPush || len(o.SplitPlatforms) > 0 || (commit == "" && short == "") {
		return released, nil
	}
	for _, m := range grp {
		at, ok, err := memberReleased(r, m.plan, commit, short)
		if err != nil {
			return nil, err
		}
		if ok {
			released[m.plan.Image.ID] = at
		}
	}
	return released, nil
}

// memberReleased applies releasedMembers' rule to one image.
func memberReleased(r *run.Runner, plan ImagePlan, commit, short string) (releasedAt, bool, error) {
	var have, missing []string
	var digest string
	for _, ref := range plan.Refs {
		// A floating tag moves by design; it is never evidence that this
		// commit was released, however it is spelled.
		if plan.Floating[ref] || !isCommitTag(ref, commit, short) {
			continue
		}
		got, found, err := inspectTag(r, ref)
		switch {
		case err != nil:
			fmt.Fprintf(os.Stderr, "warning: could not look up %s before tagging: %v\n", ref, err)
			return releasedAt{}, false, nil
		case !found:
			missing = append(missing, ref)
		case got.Revision != "" && got.Revision != commit:
			return releasedAt{}, false, fmt.Errorf("image %s: %s already exists, built from commit %s, not %s — refusing to publish a tag set that disagrees with it",
				plan.Image.ID, ref, got.Revision, commit)
		default:
			have = append(have, ref)
			if digest == "" {
				digest = got.Digest
			}
		}
	}
	switch {
	case len(have) == 0:
		return releasedAt{}, false, nil
	case len(missing) > 0:
		return releasedAt{}, false, fmt.Errorf("image %s: commit tag(s) %s already exist but %s do not — an earlier release of this commit stopped partway; "+
			"tag the missing refs from the existing digest, or delete the existing ones, and re-run",
			plan.Image.ID, strings.Join(have, ", "), strings.Join(missing, ", "))
	}
	return releasedAt{Ref: have[0], Digest: digest}, true, nil
}

// tagRank orders a repository's tags for applyTags: commit tags first (the
// ones an immutable registry is most likely to refuse on a re-run), floating
// tags last. A refused tag then fails the call before the others move.
// floating is the plan's set of floating refs; a ref outside it still counts
// as floating when it is named like one ("latest").
func tagRank(ref string, floating map[string]bool, commit, short string) int {
	switch {
	case floating[ref] || isFloating(ref[strings.LastIndex(ref, ":")+1:]):
		return 2
	case isCommitTag(ref, commit, short):
		return 0
	default:
		return 1
	}
}

// orderRefsForTagging stable-sorts refs by tagRank.
func orderRefsForTagging(refs []string, floating map[string]bool, commit, short string) []string {
	out := append([]string(nil), refs...)
	sort.SliceStable(out, func(i, j int) bool {
		return tagRank(out[i], floating, commit, short) < tagRank(out[j], floating, commit, short)
	})
	return out
}
