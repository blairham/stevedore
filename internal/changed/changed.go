// Package changed decides which images a change set touches, using per-image
// dependency globs plus shared globs — the granularity a "one Dockerfile, many
// images" monorepo needs so unchanged services are skipped.
package changed

import (
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
