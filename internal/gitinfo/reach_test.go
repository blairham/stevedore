// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package gitinfo

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func cloneOf(t *testing.T, origin string, args ...string) *gitRepo {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "clone")
	args = append(append([]string{"clone", "-q"}, args...), origin, clone)
	if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v: %s", err, out)
	}
	r := &gitRepo{t: t, dir: clone}
	r.git("config", "user.email", "t@t.co")
	r.git("config", "user.name", "t")
	return r
}

// isolateGit keeps the host's git config away from stevedore's own git calls:
// a maintenance or auto-gc setting there detaches a writer into .git that
// outlives the test and its temp dir.
func isolateGit(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func TestCheckReachable(t *testing.T) {
	isolateGit(t)
	origin := originWithTag(t)

	t.Run("tag cut from main", func(t *testing.T) {
		ci := cloneOf(t, origin)
		ci.git("checkout", "-q", "v1.0.0")
		ref, err := CheckReachable(t.Context(), ci.dir, "main")
		if err != nil || ref != "origin/main" {
			t.Fatalf("CheckReachable = %q, %v; want origin/main, nil", ref, err)
		}
	})

	t.Run("feature branch commit", func(t *testing.T) {
		ci := cloneOf(t, origin)
		ci.git("checkout", "-q", "-b", "feat")
		ci.commit("b")
		if _, err := CheckReachable(t.Context(), ci.dir, "main"); !errors.Is(err, ErrNotOnBranch) {
			t.Fatalf("CheckReachable = %v, want ErrNotOnBranch", err)
		}
	})

	// A tag-only CI checkout can lack the remote-tracking branch entirely;
	// it is fetched rather than read as "not on main".
	t.Run("missing remote-tracking branch is fetched", func(t *testing.T) {
		ci := cloneOf(t, origin)
		ci.git("checkout", "-q", "v1.0.0")
		ci.git("update-ref", "-d", "refs/remotes/origin/main")
		if _, err := CheckReachable(t.Context(), ci.dir, "main"); err != nil {
			t.Fatalf("CheckReachable = %v, want nil after fetching origin/main", err)
		}
	})

	t.Run("unknown branch", func(t *testing.T) {
		ci := cloneOf(t, origin)
		_, err := CheckReachable(t.Context(), ci.dir, "trunk")
		if err == nil || errors.Is(err, ErrNotOnBranch) || errors.Is(err, ErrShallow) {
			t.Fatalf("CheckReachable(trunk) = %v, want a fetch error", err)
		}
	})

	t.Run("no origin uses the local branch", func(t *testing.T) {
		dir := mkdir(t, filepath.Join(t.TempDir(), "w"))
		r := newRepo(t, dir, "-b", "main")
		r.commit("a")
		if _, err := CheckReachable(t.Context(), dir, "main"); err != nil {
			t.Fatalf("on main: %v", err)
		}
		r.git("checkout", "-q", "-b", "feat")
		r.commit("b")
		if _, err := CheckReachable(t.Context(), dir, "main"); !errors.Is(err, ErrNotOnBranch) {
			t.Fatalf("on feat: %v, want ErrNotOnBranch", err)
		}
		if _, err := CheckReachable(t.Context(), dir, "trunk"); err == nil || errors.Is(err, ErrNotOnBranch) {
			t.Fatalf("missing branch: %v, want a not-found error", err)
		}
	})
}

// A shallow clone cannot prove a negative: the commits joining HEAD to main
// may just not be there, so the answer is "unknown", with its own advice.
func TestCheckReachableShallow(t *testing.T) {
	isolateGit(t)
	origin := originWithTag(t)
	// Give origin a feature branch off main, and main a commit past it.
	w := cloneOf(t, origin)
	w.git("checkout", "-q", "-b", "feat")
	w.commit("f")
	w.git("tag", "v1.1.0")
	w.git("checkout", "-q", "main")
	w.commit("m")
	w.git("push", "-q", "origin", "main", "feat", "--tags")

	ci := cloneOf(t, "file://"+origin, "--depth", "1", "--branch", "v1.1.0")
	if _, err := CheckReachable(t.Context(), ci.dir, "main"); !errors.Is(err, ErrShallow) {
		t.Fatalf("CheckReachable = %v, want ErrShallow", err)
	}
}
