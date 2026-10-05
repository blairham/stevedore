// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package changed

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"
)

// ContextScope is the default change-detection scope of an image that declares
// no paths: the files Docker would actually send as its build context. That is
// every file under the context directory that its dockerignore file does not
// exclude, plus the Dockerfile (which may live outside the context), the
// dockerignore file itself (editing it changes what the context holds), and the
// stevedore config (build args, target and platforms live there).
//
// All paths are repository-relative and slash-separated, the shape `git diff
// --name-only` reports.
type ContextScope struct {
	// Context is the build context directory; "" is the repository root.
	Context string
	// Dockerfile is the Dockerfile path.
	Dockerfile string
	// Config is the stevedore config file; "" when it is outside the repo.
	Config string
	// IgnoreFile is the dockerignore file in effect, "" when there is none.
	IgnoreFile string

	ignore *patternmatcher.PatternMatcher
}

// RepoRoot returns the top level of the git work tree containing dir.
func RepoRoot(ctx context.Context, dir string) (string, error) {
	out, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("git rev-parse --show-toplevel: %w", err)
	}
	return out, nil
}

// LoadContextScope builds the default scope for an image whose build context
// is contextDir and whose Dockerfile is dockerfile, in the repository rooted at
// repoRoot; configPath is the stevedore config. All three are absolute paths.
//
// The dockerignore lookup follows BuildKit: `<Dockerfile>.dockerignore` next to
// the Dockerfile wins over `.dockerignore` at the context root. Patterns use
// Docker's own matcher, so `!` re-inclusion, `**`, leading `/` and parent
// directory matches behave exactly as they do for `docker build`.
//
// ok is false when the context is not a directory inside the repository (a
// remote URL, say): git cannot say whether such a context changed, so the image
// stays unscoped and always builds.
func LoadContextScope(repoRoot, contextDir, dockerfile, configPath string) (scope *ContextScope, ok bool, err error) {
	root, err := realPath(repoRoot)
	if err != nil {
		return nil, false, err
	}
	info, statErr := os.Stat(contextDir)
	switch {
	case errors.Is(statErr, fs.ErrNotExist):
		return nil, false, nil
	case statErr != nil:
		return nil, false, fmt.Errorf("build context: %w", statErr)
	case !info.IsDir():
		return nil, false, nil
	}
	ctxRel, ok := repoRel(root, contextDir)
	if !ok {
		return nil, false, nil
	}
	s := &ContextScope{Context: ctxRel}
	if rel, ok := repoRel(root, dockerfile); ok {
		s.Dockerfile = rel
	}
	if rel, ok := repoRel(root, configPath); ok {
		s.Config = rel
	}
	for _, candidate := range []string{
		dockerfile + ".dockerignore",
		filepath.Join(contextDir, ".dockerignore"),
	} {
		data, err := os.ReadFile(filepath.Clean(candidate))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, false, fmt.Errorf("read %s: %w", candidate, err)
		}
		patterns, err := ignorefile.ReadAll(bytes.NewReader(data))
		if err != nil {
			return nil, false, fmt.Errorf("read %s: %w", candidate, err)
		}
		pm, err := patternmatcher.New(patterns)
		if err != nil {
			return nil, false, fmt.Errorf("parse %s: %w", candidate, err)
		}
		s.ignore = pm
		if rel, ok := repoRel(root, candidate); ok {
			s.IgnoreFile = rel
		}
		break
	}
	return s, true, nil
}

// Describe names the scope for a plan reason, e.g.
// `context services/api (.dockerignore)`.
func (s *ContextScope) Describe() string {
	ctx := s.Context
	if ctx == "" {
		ctx = "."
	}
	desc := "context " + ctx
	if s.IgnoreFile != "" {
		desc += " (" + path.Base(s.IgnoreFile) + ")"
	}
	return desc
}

// Contains reports whether a changed repository-relative file is one of the
// image's build inputs.
func (s *ContextScope) Contains(file string) bool {
	file = strings.TrimPrefix(file, "./")
	if file == "" {
		return false
	}
	if file == s.Dockerfile || file == s.Config || file == s.IgnoreFile {
		return true
	}
	rel := file
	if s.Context != "" {
		var ok bool
		rel, ok = strings.CutPrefix(file, s.Context+"/")
		if !ok {
			return false
		}
	}
	if s.ignore == nil {
		return true
	}
	excluded, err := s.ignore.MatchesOrParentMatches(filepath.FromSlash(rel))
	// A pattern that fails to match cleanly is no reason to skip a build.
	return err != nil || !excluded
}

// repoRel returns p relative to root as a slash path ("" for root itself), and
// false when p lies outside root.
func repoRel(root, p string) (string, bool) {
	rp, err := realPath(p)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(root, rp)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	if rel == "." {
		return "", true
	}
	return filepath.ToSlash(rel), true
}

// realPath makes p absolute and resolves symlinks in it, so a temp dir under
// /var compares equal to git's /private/var top level on macOS. A path that does
// not exist yet keeps its resolved parent.
func realPath(p string) (string, error) {
	a, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(a); err == nil {
		return r, nil
	}
	dir, base := filepath.Split(a)
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		return filepath.Join(r, base), nil
	}
	return a, nil
}
