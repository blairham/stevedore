// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package changed

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// markerRepos builds a bare origin plus a CI-shaped clone of it: main only,
// with no refs outside heads/tags fetched. It returns the clone dir and a git
// runner that takes the dir to run in.
func markerRepos(t *testing.T) (string, string, func(dir string, args ...string) string) {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	seed := filepath.Join(root, "seed")
	clone := filepath.Join(root, "clone")
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t.co",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t.co")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git(root, "init", "-q", "--bare", "-b", "main", origin)
	git(root, "init", "-q", "-b", "main", seed)
	git(seed, "commit", "-q", "--allow-empty", "-m", "c1")
	git(seed, "remote", "add", "origin", origin)
	git(seed, "push", "-q", "origin", "main")
	git(root, "clone", "-q", origin, clone)
	return clone, seed, git
}

func TestAdvanceMarkerOutcomes(t *testing.T) {
	const ref = "refs/releases/image/svc"

	t.Run("no marker on origin: created", func(t *testing.T) {
		clone, _, git := markerRepos(t)
		if err := AdvanceMarker(clone, ref); err != nil {
			t.Fatal(err)
		}
		if got, want := git(clone, "ls-remote", "origin", ref), git(clone, "rev-parse", "HEAD"); !strings.HasPrefix(got, want) {
			t.Errorf("origin marker = %q, want %s", got, want)
		}
	})

	t.Run("marker behind HEAD: fast-forwarded", func(t *testing.T) {
		clone, seed, git := markerRepos(t)
		git(seed, "push", "-q", "origin", "HEAD:"+ref)
		git(seed, "commit", "-q", "--allow-empty", "-m", "c2")
		git(seed, "push", "-q", "origin", "main")
		git(clone, "pull", "-q")
		if err := AdvanceMarker(clone, ref); err != nil {
			t.Fatal(err)
		}
		if got, want := git(clone, "ls-remote", "origin", ref), git(clone, "rev-parse", "HEAD"); !strings.HasPrefix(got, want) {
			t.Errorf("origin marker = %q, want %s", got, want)
		}
	})

	t.Run("marker ahead of HEAD: left alone", func(t *testing.T) {
		clone, seed, git := markerRepos(t)
		git(seed, "commit", "-q", "--allow-empty", "-m", "c2")
		git(seed, "push", "-q", "origin", "main", "HEAD:"+ref)
		ahead := git(seed, "rev-parse", "HEAD")
		err := AdvanceMarker(clone, ref)
		if !errors.Is(err, ErrMarkerAhead) {
			t.Fatalf("err = %v, want ErrMarkerAhead", err)
		}
		if got := git(clone, "ls-remote", "origin", ref); !strings.HasPrefix(got, ahead) {
			t.Errorf("origin marker moved: %q, want %s", got, ahead)
		}
	})

	// The 2026-09-11 trading state: a release from a branch that was never
	// merged and was then deleted, so origin's marker is reachable from no
	// branch and the CI clone has never seen the object.
	t.Run("marker on an unmerged, deleted branch: diverged", func(t *testing.T) {
		clone, seed, git := markerRepos(t)
		git(seed, "checkout", "-q", "-b", "feature")
		git(seed, "commit", "-q", "--allow-empty", "-m", "unmerged")
		orphan := git(seed, "rev-parse", "HEAD")
		git(seed, "push", "-q", "origin", "HEAD:"+ref)
		// main moves on past the fork point; the branch is never merged.
		git(seed, "checkout", "-q", "main")
		git(seed, "commit", "-q", "--allow-empty", "-m", "c2")
		git(seed, "push", "-q", "origin", "main")
		git(clone, "pull", "-q")
		err := AdvanceMarker(clone, ref)
		if !errors.Is(err, ErrMarkerDiverged) {
			t.Fatalf("err = %v, want ErrMarkerDiverged", err)
		}
		if got := git(clone, "ls-remote", "origin", ref); !strings.HasPrefix(got, orphan) {
			t.Errorf("origin marker moved: %q, want %s", got, orphan)
		}
	})
}

func TestMarkerRef(t *testing.T) {
	if got := MarkerRef("refs/releases/image/", "checkout"); got != "refs/releases/image/checkout" {
		t.Errorf("MarkerRef = %q", got)
	}
}

func TestRefExistsAndAdvance(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.email", "t@t.co")
	git("config", "user.name", "t")
	os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644)
	git("add", "-A")
	git("commit", "-qm", "init")

	ref := MarkerRef("refs/releases/image/", "svc")
	if RefExists(dir, ref) {
		t.Fatal("marker should not exist yet")
	}
	// No origin remote: AdvanceMarker just sets the ref locally.
	if err := AdvanceMarker(dir, ref); err != nil {
		t.Fatal(err)
	}
	if !RefExists(dir, ref) {
		t.Fatal("marker should exist after AdvanceMarker")
	}
	// The marker points at HEAD, so nothing changed since it.
	files, err := FilesSince(dir, ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Errorf("expected no changes since marker, got %v", files)
	}
}

func TestMatch(t *testing.T) {
	patterns := []string{"Acme.Reports/**", "Directory.*", "*.sln"}
	cases := map[string]bool{
		"Acme.Reports/Foo.cs":         true,
		"Acme.Reports/sub/dir/Bar.cs": true,
		"Acme.Payments/Foo.cs":        false,
		"Directory.Build.props":       true,
		"Acme.sln":                    true,
		"README.md":                   false,
		"./Acme.Reports/Foo.cs":       true, // leading ./ tolerated
	}
	for path, want := range cases {
		if got := Match(patterns, path); got != want {
			t.Errorf("Match(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestEvaluateUnscoped(t *testing.T) {
	// No paths -> always changed (can't prove it's safe to skip).
	d := Evaluate(nil, []string{"Dockerfile"}, []string{"anything.txt"})
	if !d.Changed || d.Scoped {
		t.Errorf("unscoped image should be changed & unscoped: %+v", d)
	}
}

func TestEvaluateScoped(t *testing.T) {
	scoped := []string{"Acme.PaymentsGateway/**", "Acme.Fix/**", "Acme.Shared/**"}
	shared := []string{"Dockerfile", "*.sln"}

	// A file under one of its dependency dirs -> changed.
	d := Evaluate(scoped, shared, []string{"Acme.Fix/FixEngine.cs"})
	if !d.Changed || !d.Scoped {
		t.Errorf("should be changed via Fix dep: %+v", d)
	}

	// A shared file -> changed.
	if d := Evaluate(scoped, shared, []string{"Dockerfile"}); !d.Changed {
		t.Errorf("shared Dockerfile change should rebuild: %+v", d)
	}

	// An unrelated service -> not changed.
	if d := Evaluate(scoped, shared, []string{"Acme.Billing/Client.cs"}); d.Changed {
		t.Errorf("unrelated Billing change should NOT rebuild PaymentsGateway: %+v", d)
	}
}
