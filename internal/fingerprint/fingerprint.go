// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package fingerprint computes a content hash of an image's build inputs so a
// monorepo release can skip rebuilding images whose inputs are unchanged.
package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/blairham/stevedore/internal/changed"
	"github.com/blairham/stevedore/internal/config"
)

// skipDirs are directories never included in a fingerprint (VCS metadata and
// dependencies that don't affect the source-level inputs).
var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true, // JS deps
}

// dotnetOutputDirs are .NET build output, skipped only beside a project file
// (see isDotnetOutput). Anywhere else bin/ is as likely to hold a COPY'd
// entrypoint script as build output, and hiding it let an edit there leave the
// fingerprint unchanged.
var dotnetOutputDirs = map[string]bool{"bin": true, "obj": true}

// dotnetProjectExts are the MSBuild project files whose directory gets the
// bin/ and obj/ output folders.
var dotnetProjectExts = map[string]bool{".csproj": true, ".fsproj": true, ".vbproj": true}

// isDotnetOutput reports whether dir, a bin/ or obj/ directory, sits beside a
// .NET project file — i.e. is that project's build output.
func isDotnetOutput(dir string) bool {
	if !dotnetOutputDirs[filepath.Base(dir)] {
		return false
	}
	entries, err := os.ReadDir(filepath.Dir(dir))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && dotnetProjectExts[strings.ToLower(filepath.Ext(e.Name()))] {
			return true
		}
	}
	return false
}

// Compute returns a hex digest of the build inputs for img: the Dockerfile, the
// target stage, the platforms, the (unrendered) build args, and the source
// files. Rendered values that change every release — the version, labels — are
// deliberately excluded so a version bump alone does not count as a change.
// scopedPaths, when non-empty, are the resolved dependency globs (per-image plus
// shared) that narrow the source hash to just the files this image is built
// from; otherwise the whole build context is used.
func Compute(dir string, img config.Image, distDir string, scopedPaths []string) (string, error) {
	h := sha256.New()

	// Stable header of non-file inputs.
	hashf(h, "target=%s\n", img.Target)
	plats := append([]string(nil), img.Platforms...)
	sort.Strings(plats)
	hashf(h, "platforms=%s\n", strings.Join(plats, ","))
	buildArgs := append([]string(nil), img.BuildArgs...)
	sort.Strings(buildArgs)
	for _, a := range buildArgs {
		hashf(h, "arg=%s\n", a)
	}
	// extra_flags reach buildx verbatim and can carry build inputs of their own
	// (--build-arg, --secret, --build-context). They are hashed in order, not
	// sorted: a flag and its value are separate elements, so sorting would mix
	// pairs up, and a reorder costing one rebuild is the safe direction.
	for _, f := range img.ExtraFlags {
		hashf(h, "flag=%s\n", f)
	}

	// The Dockerfile (it may live outside the context).
	dockerfile := absPath(dir, img.Dockerfile)
	if err := hashFile(h, "dockerfile", dockerfile); err != nil {
		return "", err
	}

	// The walk compares each directory's absolute path against this, so it must
	// be absolute and clean too: under the default --dir "." a joined "dist"
	// stays relative and never matches (issue #36).
	absDist, err := filepath.Abs(absPath(dir, distDir))
	if err != nil {
		return "", fmt.Errorf("resolve dist dir %s: %w", distDir, err)
	}
	if len(scopedPaths) > 0 {
		// Path-scoped: hash only files matching the resolved globs — so images
		// sharing one context/Dockerfile get distinct, narrowly-invalidated
		// fingerprints.
		if err := hashMatching(h, dir, scopedPaths, absDist); err != nil {
			return "", err
		}
	} else {
		// Whole build-context tree.
		ctxDir := absPath(dir, img.Context)
		if err := hashTree(h, ctxDir, absDist); err != nil {
			return "", err
		}
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashMatching folds every file under root whose repo-relative path matches one
// of patterns into h, in deterministic order.
func hashMatching(h hash.Hash, root string, patterns []string, distDir string) error {
	rels, err := walkFiles(root, distDir, func(rel string) bool {
		return changed.Match(patterns, filepath.ToSlash(rel))
	})
	if err != nil {
		return err
	}
	return hashFiles(h, "path:", root, rels)
}

// hashTree folds every file under root (except skipped dirs and the dist dir)
// into h as (relpath, content) pairs, walked in a deterministic order.
func hashTree(h hash.Hash, root, distDir string) error {
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("stat context %s: %w", root, err)
	}
	if !info.IsDir() {
		return hashFile(h, "ctx:"+filepath.Base(root), root)
	}
	rels, err := walkFiles(root, distDir, func(string) bool { return true })
	if err != nil {
		return err
	}
	return hashFiles(h, "ctx:", root, rels)
}

// walkFiles returns the root-relative paths of the files under root that keep
// accepts, sorted. Skipped dirs, .NET build output, and the dist dir (when it
// lives under root) are not descended into, though root itself always is, and
// symlinks are not followed. distDir must be absolute and clean, as
// filepath.Abs returns it.
func walkFiles(root, distDir string, keep func(rel string) bool) ([]string, error) {
	var rels []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (skipDirs[d.Name()] || isDotnetOutput(path)) {
				return filepath.SkipDir
			}
			if abs, absErr := filepath.Abs(path); absErr == nil && abs == distDir {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if keep(rel) {
			rels = append(rels, rel)
		}
		return nil
	})
	sort.Strings(rels)
	return rels, err
}

// hashFiles hashes each root-relative file under label+its slash path.
func hashFiles(h hash.Hash, label, root string, rels []string) error {
	for _, rel := range rels {
		if err := hashFile(h, label+filepath.ToSlash(rel), filepath.Join(root, rel)); err != nil {
			return err
		}
	}
	return nil
}

// hashFile mixes a labeled file's path, permission bits and content into h. The
// mode counts because COPY preserves it: a `chmod +x entrypoint.sh` changes the
// image without changing a byte of the file. A missing file is recorded as
// absent rather than erroring, so an optional Dockerfile path is tolerated.
func hashFile(h hash.Hash, label, path string) error {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		if os.IsNotExist(err) {
			hashf(h, "%s=<absent>\n", label)
			return nil
		}
		return err
	}
	return errors.Join(hashOpen(h, label, f), f.Close())
}

func hashOpen(h hash.Hash, label string, f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	hashf(h, "%s=%04o:", label, info.Mode().Perm())
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	hashf(h, "\n")
	return nil
}

// hashf mixes formatted text into h. hash.Hash's Write is documented never
// to return an error, so there is none to check.
func hashf(h hash.Hash, format string, a ...any) {
	h.Write(fmt.Appendf(nil, format, a...))
}

func absPath(dir, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dir, p)
}

// State maps an image ID to the fingerprint of its last successful build.
type State map[string]string

// Load reads the fingerprint state from path, returning an empty state when the
// file does not exist.
func Load(path string) (State, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		if os.IsNotExist(err) {
			return State{}, nil
		}
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse fingerprint state %s: %w", path, err)
	}
	if s == nil {
		s = State{}
	}
	return s, nil
}

// Save writes the state to path as indented JSON.
func (s State) Save(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile( //nolint:gosec // G306: it lives in dist/, read by non-owners (see pipeline.mkdirDist)
		path,
		append(data, '\n'),
		0o644,
	)
}
