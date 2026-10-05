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
	quiesce(t, git, origin, seed, clone)
	return clone, seed, git
}

// quiesce turns off git's automatic housekeeping in each repo. A fetch or
// commit can leave a detached `gc --auto` / `maintenance run --auto` still
// writing into .git after the command returns, and t.TempDir's cleanup then
// fails with "directory not empty". The config lives in the repos, so it also
// covers the git processes the code under test spawns.
func quiesce(t *testing.T, git func(dir string, args ...string) string, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		git(d, "config", "gc.auto", "0")
		git(d, "config", "maintenance.auto", "false")
	}
}

func TestAdvanceMarkerOutcomes(t *testing.T) {
	const ref = "refs/releases/image/svc"

	t.Run("no marker on origin: created", func(t *testing.T) {
		clone, _, git := markerRepos(t)
		if err := AdvanceMarker(t.Context(), clone, ref); err != nil {
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
		if err := AdvanceMarker(t.Context(), clone, ref); err != nil {
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
		err := AdvanceMarker(t.Context(), clone, ref)
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
		err := AdvanceMarker(t.Context(), clone, ref)
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
	if RefExists(t.Context(), dir, ref) {
		t.Fatal("marker should not exist yet")
	}
	// No origin remote: AdvanceMarker just sets the ref locally.
	if err := AdvanceMarker(t.Context(), dir, ref); err != nil {
		t.Fatal(err)
	}
	if !RefExists(t.Context(), dir, ref) {
		t.Fatal("marker should exist after AdvanceMarker")
	}
	// The marker points at HEAD, so nothing changed since it.
	files, err := FilesSince(t.Context(), dir, ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Errorf("expected no changes since marker, got %v", files)
	}
}

// FilesSince must report both sides of a rename and non-ASCII names verbatim:
// a file moved out of an image's scope changes that image, and a name git
// would C-quote still matches its glob.
func TestFilesSinceRenamesAndNonASCII(t *testing.T) {
	clone, _, git := markerRepos(t)
	git(clone, "config", "user.email", "t@t.co")
	git(clone, "config", "user.name", "t")
	if err := os.MkdirAll(filepath.Join(clone, "svc-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"shared", "svc-b"} {
		if err := os.MkdirAll(filepath.Join(clone, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	commitFile(t, git, clone, "svc-a/util.go")
	base := git(clone, "rev-parse", "HEAD")
	git(clone, "mv", "svc-a/util.go", "shared/util.go")
	git(clone, "commit", "-q", "-m", "move util")
	commitFile(t, git, clone, "svc-b/café.txt")

	files, err := FilesSince(t.Context(), clone, base)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"svc-a/util.go": true, "shared/util.go": true, "svc-b/café.txt": true}
	if len(files) != len(want) {
		t.Errorf("FilesSince = %q, want the keys of %v", files, want)
	}
	for _, f := range files {
		if !want[f] {
			t.Errorf("unexpected path %q in %q", f, files)
		}
	}

	if d := Evaluate(Scope{Paths: []string{"svc-a/**"}}, nil, files); !d.Changed {
		t.Errorf("rename out of svc-a: Evaluate(svc-a/**) = %+v, want changed", d)
	}
	if d := Evaluate(Scope{Paths: []string{"svc-b/**"}}, nil, files); !d.Changed {
		t.Errorf("non-ASCII name: Evaluate(svc-b/**) = %+v, want changed", d)
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

// A pattern written with a leading ./ must match git's bare paths (#62);
// before the fix such an image was always reported unchanged.
func TestMatchDotSlashPattern(t *testing.T) {
	patterns := []string{"./svc-a/**"}
	for path, want := range map[string]bool{
		"svc-a/main.go":   true,
		"./svc-a/main.go": true,
		"svc-b/main.go":   false,
	} {
		if got := Match(patterns, path); got != want {
			t.Errorf("Match(%q, %q) = %v, want %v", patterns, path, got, want)
		}
	}
	d := Evaluate(Scope{Paths: patterns}, nil, []string{"svc-a/main.go"})
	if !d.Changed || !strings.Contains(d.Reason, `"./svc-a/**"`) {
		t.Errorf("Evaluate = %+v, want changed quoting the pattern as written", d)
	}
}

func TestEvaluateUnscoped(t *testing.T) {
	// No paths and no local context -> always changed (can't prove it's safe
	// to skip).
	d := Evaluate(Scope{}, []string{"Dockerfile"}, []string{"anything.txt"})
	if !d.Changed || d.Scoped {
		t.Errorf("unscoped image should be changed & unscoped: %+v", d)
	}
}

func TestEvaluateScoped(t *testing.T) {
	scoped := []string{"Acme.PaymentsGateway/**", "Acme.Fix/**", "Acme.Shared/**"}
	shared := []string{"Dockerfile", "*.sln"}

	// A file under one of its dependency dirs -> changed.
	d := Evaluate(Scope{Paths: scoped}, shared, []string{"Acme.Fix/FixEngine.cs"})
	if !d.Changed || !d.Scoped {
		t.Errorf("should be changed via Fix dep: %+v", d)
	}

	// A shared file -> changed.
	if d := Evaluate(Scope{Paths: scoped}, shared, []string{"Dockerfile"}); !d.Changed {
		t.Errorf("shared Dockerfile change should rebuild: %+v", d)
	}

	// An unrelated service -> not changed.
	if d := Evaluate(Scope{Paths: scoped}, shared, []string{"Acme.Billing/Client.cs"}); d.Changed {
		t.Errorf("unrelated Billing change should NOT rebuild PaymentsGateway: %+v", d)
	}
}

// commitFile writes name and commits it, so a diff has something to show.
func commitFile(t *testing.T, git func(dir string, args ...string) string, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
		t.Fatal(err)
	}
	git(dir, "add", name)
	git(dir, "commit", "-q", "-m", name)
}

func TestMarkerBase(t *testing.T) {
	const ref = "refs/releases/image/svc"

	t.Run("marker an ancestor of HEAD: the marker", func(t *testing.T) {
		clone, seed, git := markerRepos(t)
		git(seed, "push", "-q", "origin", "HEAD:"+ref)
		marker := git(seed, "rev-parse", "HEAD")
		commitFile(t, git, seed, "main.txt")
		git(seed, "push", "-q", "origin", "main")
		git(clone, "pull", "-q")
		if err := FetchMarkers(t.Context(), clone, "refs/releases/image/"); err != nil {
			t.Fatal(err)
		}
		base, diverged, err := MarkerBase(t.Context(), clone, ref)
		if err != nil {
			t.Fatal(err)
		}
		if base != marker || diverged {
			t.Errorf("MarkerBase = %s, diverged=%v; want %s, false", short(base), diverged, short(marker))
		}
	})

	// The 2026-09-11 trading state again, seen from the plan: diffing from the
	// stray commit itself reports the unmerged branch's own files as changed
	// (they are absent from HEAD), so every image they touch rebuilds on every
	// release. The base must be the fork point instead.
	t.Run("marker on an unmerged branch: the merge base", func(t *testing.T) {
		clone, seed, git := markerRepos(t)
		fork := git(seed, "rev-parse", "HEAD")
		git(seed, "checkout", "-q", "-b", "feature")
		commitFile(t, git, seed, "branch-only.txt")
		git(seed, "push", "-q", "origin", "HEAD:"+ref)
		git(seed, "checkout", "-q", "main")
		git(seed, "branch", "-q", "-D", "feature")
		commitFile(t, git, seed, "main.txt")
		git(seed, "push", "-q", "origin", "main")
		git(clone, "pull", "-q")
		if err := FetchMarkers(t.Context(), clone, "refs/releases/image/"); err != nil {
			t.Fatal(err)
		}
		base, diverged, err := MarkerBase(t.Context(), clone, ref)
		if err != nil {
			t.Fatal(err)
		}
		if base != fork || !diverged {
			t.Fatalf("MarkerBase = %s, diverged=%v; want fork %s, true", short(base), diverged, short(fork))
		}
		files, err := FilesSince(t.Context(), clone, base)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(files, ",") != "main.txt" {
			t.Errorf("FilesSince(base) = %v, want [main.txt] (not the unmerged branch's files)", files)
		}
	})

	t.Run("marker ahead of HEAD: the marker, not diverged", func(t *testing.T) {
		clone, seed, git := markerRepos(t)
		commitFile(t, git, seed, "newer.txt")
		git(seed, "push", "-q", "origin", "HEAD:"+ref)
		ahead := git(seed, "rev-parse", "HEAD")
		if err := FetchMarkers(t.Context(), clone, "refs/releases/image/"); err != nil {
			t.Fatal(err)
		}
		base, diverged, err := MarkerBase(t.Context(), clone, ref)
		if err != nil {
			t.Fatal(err)
		}
		if base != ahead || diverged {
			t.Errorf("MarkerBase = %s, diverged=%v; want %s, false", short(base), diverged, short(ahead))
		}
	})
}

func TestFetchMarkers(t *testing.T) {
	const prefix = "refs/releases/image/"
	const ref = prefix + "svc"

	// A marker reset by hand on origin is a non-fast-forward for any clone that
	// already holds the old one. Origin is the baseline, so it must win; an
	// unforced refspec rejects the update and the plan diffs from the old one.
	t.Run("origin marker reset by hand: local copy follows it", func(t *testing.T) {
		clone, seed, git := markerRepos(t)
		git(seed, "checkout", "-q", "-b", "feature")
		commitFile(t, git, seed, "branch-only.txt")
		git(seed, "push", "-q", "origin", "HEAD:"+ref)
		git(seed, "checkout", "-q", "main")
		if err := FetchMarkers(t.Context(), clone, prefix); err != nil {
			t.Fatal(err)
		}
		reset := git(seed, "rev-parse", "main")
		git(seed, "push", "-q", "--force", "origin", "main:"+ref)
		if err := FetchMarkers(t.Context(), clone, prefix); err != nil {
			t.Fatal(err)
		}
		if got := git(clone, "rev-parse", ref); got != reset {
			t.Errorf("local marker = %s, want origin's reset %s", short(got), short(reset))
		}
	})

	// A failed fetch used to be swallowed: with no markers present locally every
	// image reads "no release marker yet" and the release rebuilds everything.
	t.Run("origin unreachable: an error, not an empty baseline", func(t *testing.T) {
		clone, _, git := markerRepos(t)
		git(clone, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git"))
		if err := FetchMarkers(t.Context(), clone, prefix); err == nil {
			t.Fatal("FetchMarkers succeeded against a missing origin")
		}
	})

	t.Run("no origin remote: nothing to fetch, no error", func(t *testing.T) {
		clone, _, git := markerRepos(t)
		git(clone, "remote", "remove", "origin")
		if err := FetchMarkers(t.Context(), clone, prefix); err != nil {
			t.Fatal(err)
		}
	})
}
