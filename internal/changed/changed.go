// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package changed decides which images a change set touches, using per-image
// dependency globs plus shared globs — the granularity a "one Dockerfile, many
// images" monorepo needs so unchanged services are skipped.
package changed

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// FilesSince returns the repo-relative paths that differ between ref and the
// current working tree. On a clean checkout this is the ref..HEAD change set.
func FilesSince(ctx context.Context, dir, ref string) ([]string, error) {
	if ref == "" {
		return nil, fmt.Errorf("changed-since requires a git ref")
	}
	cmd := exec.CommandContext(ctx, "git", "diff", "--name-only", ref)
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
func RefExists(ctx context.Context, dir, ref string) bool {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "--quiet", ref)
	cmd.Dir = dir
	return cmd.Run() == nil
}

// FetchMarkers fetches the marker ref namespace from origin so change
// detection sees the latest per-image baselines (important on a fresh CI
// checkout). The refspec is forced: origin is the baseline, and a marker reset
// there by hand is a non-fast-forward that an unforced fetch rejects, leaving a
// stale local marker in charge. A failed fetch is an error — swallowing it left
// no markers locally, so every image read "never released" and rebuilt. With
// no origin remote there is nothing to fetch.
func FetchMarkers(ctx context.Context, dir, prefix string) error {
	if !hasOrigin(ctx, dir) {
		return nil
	}
	spec := "+" + prefix + "*:" + prefix + "*"
	cmd := exec.CommandContext(ctx, "git", "fetch", "--quiet", "origin", spec)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("fetch release markers %s*: %w: %s", prefix, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// MarkerBase returns the commit change detection should diff from for ref.
// That is the marker itself, unless it has diverged from HEAD (a release from a
// branch that was never merged): diffing from the stray commit reports that
// branch's own files as changed on every later release, so the base is then
// the merge base and diverged is true. The marker still has to be reset by
// hand — AdvanceMarker refuses to move it — but until then the plan stays
// scoped to what main actually changed.
func MarkerBase(ctx context.Context, dir, ref string) (base string, diverged bool, err error) {
	marker, err := revParse(ctx, dir, ref)
	if err != nil {
		return "", false, err
	}
	head, err := revParse(ctx, dir, "HEAD")
	if err != nil {
		return "", false, err
	}
	cmd := exec.CommandContext(ctx, "git", "merge-base", marker, head)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", false, fmt.Errorf("merge-base %s HEAD: %w", ref, err)
	}
	mb := strings.TrimSpace(string(out))
	if mb == marker || mb == head {
		return marker, false, nil
	}
	return mb, true, nil
}

// ErrMarkerAhead means origin's marker already points at a descendant of HEAD,
// i.e. a newer commit has been released (typically a re-run of an older
// release). The marker is left alone; this is not a failure.
var ErrMarkerAhead = errors.New("origin marker is already ahead of HEAD")

// ErrMarkerDiverged means origin's marker points at a commit that is neither an
// ancestor nor a descendant of HEAD, e.g. one released from a branch that was
// never merged. Advancing it would discard that history, so it needs a human;
// until then change detection diffs from the merge base (see MarkerBase).
var ErrMarkerDiverged = errors.New("origin marker has diverged from HEAD")

// AdvanceMarker points ref at HEAD and, when an origin remote exists, pushes it
// so the baseline persists across CI runs. The push only ever fast-forwards:
// origin's current marker is fetched and compared with HEAD first, so a marker
// that is ahead returns ErrMarkerAhead and one that has diverged returns
// ErrMarkerDiverged, both without touching origin.
func AdvanceMarker(ctx context.Context, dir, ref string) error {
	if hasOrigin(ctx, dir) {
		if err := checkFastForward(ctx, dir, ref); err != nil {
			return err
		}
	}
	up := exec.CommandContext(ctx, "git", "update-ref", ref, "HEAD")
	up.Dir = dir
	if out, err := up.CombinedOutput(); err != nil {
		return fmt.Errorf("update-ref %s: %w: %s", ref, err, strings.TrimSpace(string(out)))
	}
	if !hasOrigin(ctx, dir) {
		return nil
	}
	push := exec.CommandContext(ctx, "git", "push", "--quiet", "origin", ref)
	push.Dir = dir
	if out, err := push.CombinedOutput(); err != nil {
		return fmt.Errorf("push %s: %w: %s", ref, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// checkFastForward returns nil when origin has no ref or HEAD descends from
// it. A CI checkout fetches only heads and tags, so origin's marker object is
// fetched here first; without it git rejects the push as `(fetch first)`
// whatever the ancestry.
func checkFastForward(ctx context.Context, dir, ref string) error {
	ls := exec.CommandContext(ctx, "git", "ls-remote", "origin", ref)
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
	fetch := exec.CommandContext(ctx, "git", "fetch", "--quiet", "origin", ref)
	fetch.Dir = dir
	if fout, ferr := fetch.CombinedOutput(); ferr != nil {
		return fmt.Errorf("fetch %s: %w: %s", ref, ferr, strings.TrimSpace(string(fout)))
	}
	head, err := revParse(ctx, dir, "HEAD")
	if err != nil {
		return err
	}
	if remote == head {
		return nil
	}
	fastForward, err := isAncestor(ctx, dir, remote, head)
	if err != nil || fastForward {
		return err
	}
	behind, err := isAncestor(ctx, dir, head, remote)
	if err != nil {
		return err
	}
	if behind {
		return fmt.Errorf("%s at %s: %w", ref, short(remote), ErrMarkerAhead)
	}
	return fmt.Errorf("%s at %s, HEAD %s: %w", ref, short(remote), short(head), ErrMarkerDiverged)
}

func revParse(ctx context.Context, dir, rev string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--verify", rev+"^{commit}")
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
func isAncestor(ctx context.Context, dir, a, b string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "merge-base", "--is-ancestor", a, b)
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

func hasOrigin(ctx context.Context, dir string) bool {
	cmd := exec.CommandContext(ctx, "git", "remote")
	cmd.Dir = dir
	out, err := cmd.Output()
	return err == nil && strings.Contains(string(out), "origin")
}

// Decision explains why an image is (or isn't) considered changed.
type Decision struct {
	Changed bool
	// Scoped is false when the image's inputs could not be narrowed at all (no
	// paths and a context git cannot see), so it is treated as always-changed.
	Scoped bool
	// Reason is a short human explanation (a matching file and the scope it
	// matched, "unscoped", or "no matching files in <scope>").
	Reason string
}

// Scope is what an image is built from, for change detection. Paths, when
// set, are the image's resolved dependency globs and are the whole scope.
// Otherwise Context is its default scope (see ContextScope). With neither the
// image is unscoped.
type Scope struct {
	Paths   []string
	Context *ContextScope
}

// Evaluate decides whether an image is affected by the given changed files. An
// empty change set means nothing changed, whatever the scope. Otherwise the
// image changes when a file matches the shared globs or falls in its scope: its
// Paths globs when it declares them, else its build context. Only an image with
// neither (a remote context) is unscoped and always rebuilds, since we cannot
// prove it is safe to skip.
func Evaluate(scope Scope, shared, files []string) Decision {
	if len(scope.Paths) == 0 && scope.Context == nil {
		return Decision{Changed: true, Scoped: false, Reason: "no paths declared and no local build context (unscoped)"}
	}
	if len(files) == 0 {
		return Decision{Changed: false, Scoped: true, Reason: "no files changed"}
	}
	if len(scope.Paths) == 0 {
		desc := scope.Context.Describe()
		for _, f := range files {
			if p, ok := matchAny(shared, f); ok {
				return Decision{Changed: true, Scoped: true, Reason: fmt.Sprintf("%s (matched %q)", f, p)}
			}
			if scope.Context.Contains(f) {
				return Decision{Changed: true, Scoped: true, Reason: fmt.Sprintf("%s (in %s)", f, desc)}
			}
		}
		return Decision{Changed: false, Scoped: true, Reason: "no matching files in " + desc}
	}
	patterns := make([]string, 0, len(scope.Paths)+len(shared))
	patterns = append(patterns, scope.Paths...)
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
