// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package changed decides which images a change set touches, using per-image
// dependency globs plus shared globs — the granularity a "one Dockerfile, many
// images" monorepo needs so unchanged services are skipped.
package changed

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// FilesSince returns the repo-relative paths that differ between ref and the
// current working tree. On a clean checkout this is the ref..HEAD change set.
func FilesSince(dir, ref string) ([]string, error) {
	if ref == "" {
		return nil, fmt.Errorf("changed-since requires a git ref")
	}
	cmd := exec.Command("git", "diff", "--name-only", ref)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff against %s: %w", ref, err)
	}
	var files []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if l := strings.TrimSpace(line); l != "" {
			files = append(files, l)
		}
	}
	return files, nil
}

// MarkerRef returns the marker ref name for an image id.
func MarkerRef(prefix, id string) string {
	return prefix + id
}

// RefExists reports whether a git ref resolves in the repository at dir.
func RefExists(dir, ref string) bool {
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", ref)
	cmd.Dir = dir
	return cmd.Run() == nil
}

// FetchMarkers best-effort fetches the marker ref namespace from origin so
// change detection sees the latest per-image baselines (important on a fresh CI
// checkout). Errors are non-fatal.
func FetchMarkers(dir, prefix string) {
	spec := prefix + "*:" + prefix + "*"
	cmd := exec.Command("git", "fetch", "--quiet", "origin", spec)
	cmd.Dir = dir
	_ = cmd.Run()
}

// ErrMarkerAhead means origin's marker already points at a descendant of HEAD,
// i.e. a newer commit has been released (typically a re-run of an older
// release). The marker is left alone; this is not a failure.
var ErrMarkerAhead = errors.New("origin marker is already ahead of HEAD")

// ErrMarkerDiverged means origin's marker points at a commit that is neither an
// ancestor nor a descendant of HEAD, e.g. one released from a branch that was
// never merged. Advancing it would discard that history, so it needs a human,
// and change detection keeps diffing from the stray commit until then.
var ErrMarkerDiverged = errors.New("origin marker has diverged from HEAD")

// AdvanceMarker points ref at HEAD and, when an origin remote exists, pushes it
// so the baseline persists across CI runs. The push only ever fast-forwards:
// origin's current marker is fetched and compared with HEAD first, so a marker
// that is ahead returns ErrMarkerAhead and one that has diverged returns
// ErrMarkerDiverged, both without touching origin.
func AdvanceMarker(dir, ref string) error {
	if hasOrigin(dir) {
		if err := checkFastForward(dir, ref); err != nil {
			return err
		}
	}
	up := exec.Command("git", "update-ref", ref, "HEAD")
	up.Dir = dir
	if out, err := up.CombinedOutput(); err != nil {
		return fmt.Errorf("update-ref %s: %v: %s", ref, err, strings.TrimSpace(string(out)))
	}
	if !hasOrigin(dir) {
		return nil
	}
	push := exec.Command("git", "push", "--quiet", "origin", ref)
	push.Dir = dir
	if out, err := push.CombinedOutput(); err != nil {
		return fmt.Errorf("push %s: %v: %s", ref, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// checkFastForward returns nil when origin has no ref or HEAD descends from
// it. A CI checkout fetches only heads and tags, so origin's marker object is
// fetched here first; without it git rejects the push as `(fetch first)`
// whatever the ancestry.
func checkFastForward(dir, ref string) error {
	ls := exec.Command("git", "ls-remote", "origin", ref)
	ls.Dir = dir
	out, err := ls.Output()
	if err != nil {
		return fmt.Errorf("ls-remote %s: %w", ref, err)
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return nil
	}
	remote := fields[0]
	fetch := exec.Command("git", "fetch", "--quiet", "origin", ref)
	fetch.Dir = dir
	if out, err := fetch.CombinedOutput(); err != nil {
		return fmt.Errorf("fetch %s: %v: %s", ref, err, strings.TrimSpace(string(out)))
	}
	head, err := revParse(dir, "HEAD")
	if err != nil {
		return err
	}
	if remote == head {
		return nil
	}
	if ok, err := isAncestor(dir, remote, head); err != nil || ok {
		return err
	}
	behind, err := isAncestor(dir, head, remote)
	if err != nil {
		return err
	}
	if behind {
		return fmt.Errorf("%s at %s: %w", ref, short(remote), ErrMarkerAhead)
	}
	return fmt.Errorf("%s at %s, HEAD %s: %w", ref, short(remote), short(head), ErrMarkerDiverged)
}

func revParse(dir, rev string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--verify", rev+"^{commit}")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("rev-parse %s: %w", rev, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// isAncestor reports whether a is an ancestor of b. git exits 1 for "no" and
// anything else for a real failure (a missing object in a shallow clone, say),
// which is returned rather than read as "no".
func isAncestor(dir, a, b string) (bool, error) {
	cmd := exec.Command("git", "merge-base", "--is-ancestor", a, b)
	cmd.Dir = dir
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("merge-base --is-ancestor %s %s: %w", short(a), short(b), err)
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

func hasOrigin(dir string) bool {
	cmd := exec.Command("git", "remote")
	cmd.Dir = dir
	out, err := cmd.Output()
	return err == nil && strings.Contains(string(out), "origin")
}

// Decision explains why an image is (or isn't) considered changed.
type Decision struct {
	Changed bool
	// Scoped is false when the image declares no Paths, so it can't be narrowed
	// and is treated as always-changed.
	Scoped bool
	// Reason is a short human explanation (a matching file, "unscoped", or
	// "no matching files").
	Reason string
}

// Evaluate decides whether an image is affected by the given changed files.
// scopedPaths are the image's resolved dependency globs; when empty the image is
// unscoped and always rebuilds (we can't prove it's safe to skip). Otherwise it
// changes when a file matches its scoped globs or the shared globs.
func Evaluate(scopedPaths, shared, files []string) Decision {
	if len(scopedPaths) == 0 {
		return Decision{Changed: true, Scoped: false, Reason: "no paths declared (unscoped)"}
	}
	patterns := make([]string, 0, len(scopedPaths)+len(shared))
	patterns = append(patterns, scopedPaths...)
	patterns = append(patterns, shared...)
	for _, f := range files {
		if p, ok := matchAny(patterns, f); ok {
			return Decision{Changed: true, Scoped: true, Reason: fmt.Sprintf("%s (matched %q)", f, p)}
		}
	}
	return Decision{Changed: false, Scoped: true, Reason: "no matching files"}
}

// Match reports whether path matches any of the glob patterns (doublestar, so
// ** spans path separators).
func Match(patterns []string, path string) bool {
	_, ok := matchAny(patterns, path)
	return ok
}

func matchAny(patterns []string, path string) (string, bool) {
	path = strings.TrimPrefix(path, "./")
	for _, pat := range patterns {
		if ok, err := doublestar.Match(pat, path); err == nil && ok {
			return pat, true
		}
	}
	return "", false
}
