// Package fingerprint computes a content hash of an image's build inputs so a
// monorepo release can skip rebuilding images whose inputs are unchanged.
package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/blairham/stevedore/internal/changed"
	"github.com/blairham/stevedore/internal/config"
)

// skipDirs are directories never included in a fingerprint (VCS metadata and
// generated build output that don't affect the source-level inputs).
var skipDirs = map[string]bool{
	".git":         true,
	"obj":          true, // .NET build output
	"bin":          true, // .NET build output
	"node_modules": true, // JS deps
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
	fmt.Fprintf(h, "target=%s\n", img.Target)
	plats := append([]string(nil), img.Platforms...)
	sort.Strings(plats)
	fmt.Fprintf(h, "platforms=%s\n", strings.Join(plats, ","))
	buildArgs := append([]string(nil), img.BuildArgs...)
	sort.Strings(buildArgs)
	for _, a := range buildArgs {
		fmt.Fprintf(h, "arg=%s\n", a)
	}

	// The Dockerfile (it may live outside the context).
	dockerfile := absPath(dir, img.Dockerfile)
	if err := hashFile(h, "dockerfile", dockerfile); err != nil {
		return "", err
	}

	absDist := absPath(dir, distDir)
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
func hashMatching(h io.Writer, root string, patterns []string, distDir string) error {
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			if abs, _ := filepath.Abs(path); abs == distDir {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if changed.Match(patterns, filepath.ToSlash(rel)) {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, f := range files {
		rel, _ := filepath.Rel(root, f)
		if err := hashFile(h, "path:"+filepath.ToSlash(rel), f); err != nil {
			return err
		}
	}
	return nil
}

// hashTree folds every file under root (except skipped dirs and the dist dir)
// into h as (relpath, content) pairs, walked in a deterministic order.
func hashTree(h io.Writer, root, distDir string) error {
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("stat context %s: %w", root, err)
	}
	if !info.IsDir() {
		return hashFile(h, "ctx:"+filepath.Base(root), root)
	}

	var files []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			// Skip the dist dir when it lives inside the context.
			if abs, _ := filepath.Abs(path); abs == distDir {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil // don't follow symlinks
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, f := range files {
		rel, _ := filepath.Rel(root, f)
		if err := hashFile(h, "ctx:"+filepath.ToSlash(rel), f); err != nil {
			return err
		}
	}
	return nil
}

// hashFile mixes a labeled file's path and content into h. A missing file is
// recorded as absent rather than erroring, so an optional Dockerfile path is
// tolerated.
func hashFile(h io.Writer, label, path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(h, "%s=<absent>\n", label)
			return nil
		}
		return err
	}
	defer f.Close()
	fmt.Fprintf(h, "%s=", label)
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	fmt.Fprint(h, "\n")
	return nil
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
	data, err := os.ReadFile(path)
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
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
