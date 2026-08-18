package gitinfo

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitRepo drives a real git repository. The detached-HEAD behavior this file
// covers cannot be faked: it is entirely about what git reports for a checkout
// shaped the way CI shapes one.
type gitRepo struct {
	t   *testing.T
	dir string
}

func newRepo(t *testing.T, dir string, args ...string) *gitRepo {
	t.Helper()
	r := &gitRepo{t: t, dir: dir}
	r.git(append([]string{"init", "-q"}, args...)...)
	r.git("config", "user.email", "t@t.co")
	r.git("config", "user.name", "t")
	return r
}

func (r *gitRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

func (r *gitRepo) commit(name string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, name), []byte(name), 0o644); err != nil {
		r.t.Fatal(err)
	}
	r.git("add", "-A")
	r.git("commit", "-qm", name)
}

// originWithTag builds a bare origin holding one tagged commit on main, and
// returns its path.
func originWithTag(t *testing.T) string {
	t.Helper()
	origin := filepath.Join(t.TempDir(), "origin.git")
	newRepo(t, mkdir(t, origin), "--bare", "-b", "main")

	work := newRepo(t, mkdir(t, filepath.Join(t.TempDir(), "work")), "-b", "main")
	work.commit("a")
	work.git("tag", "v1.0.0")
	work.git("remote", "add", "origin", origin)
	work.git("push", "-q", "origin", "main", "--tags")
	return origin
}

func mkdir(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestOnBranchDetachedHEAD is the regression test for the bug that withheld
// every floating tag from every real release: a tag-triggered CI job checks out
// a detached HEAD, where `git rev-parse --abbrev-ref HEAD` answers "HEAD" and a
// string compare against the default branch can never succeed.
func TestOnBranchDetachedHEAD(t *testing.T) {
	origin := originWithTag(t)
	clone := filepath.Join(t.TempDir(), "ci")
	if out, err := exec.Command("git", "clone", "-q", origin, clone).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v: %s", err, out)
	}
	ci := &gitRepo{t: t, dir: clone}
	ci.git("checkout", "-q", "v1.0.0") // what a tag-triggered workflow does

	info, err := Gather(clone)
	if err != nil {
		t.Fatal(err)
	}
	if info.Branch != "HEAD" {
		t.Fatalf("Branch = %q, want the literal %q — the premise of this test", info.Branch, "HEAD")
	}
	if !info.Detached {
		t.Fatal("Detached = false on a tag checkout")
	}
	if !info.OnBranch("main") {
		t.Errorf("OnBranch(main) = false on a tag cut from main; Branches = %v", info.Branches)
	}
	if info.OnBranch("release") {
		t.Error("OnBranch(release) = true for a branch that does not exist")
	}
	if info.OnBranch("") {
		t.Error("OnBranch(\"\") = true; an unset default branch matches nothing")
	}
	if info.OnBranch("HEAD") {
		t.Error("OnBranch(HEAD) = true; origin/HEAD must not make the detached branch string match")
	}
}

func TestOnBranchAttached(t *testing.T) {
	dir := mkdir(t, filepath.Join(t.TempDir(), "w"))
	r := newRepo(t, dir, "-b", "main")
	r.commit("a")

	info, err := Gather(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Detached {
		t.Fatal("Detached = true on a branch checkout")
	}
	if !info.OnBranch("main") {
		t.Error("OnBranch(main) = false while on main")
	}

	// A side branch sitting at the same commit as main is still a side branch:
	// on an attached HEAD git's answer is the user's intent, and reachability
	// must not override it into moving a floating tag.
	r.git("checkout", "-q", "-b", "side")
	side, err := Gather(dir)
	if err != nil {
		t.Fatal(err)
	}
	if side.Branch != "side" {
		t.Fatalf("Branch = %q, want side", side.Branch)
	}
	if side.OnBranch("main") {
		t.Errorf("OnBranch(main) = true from a side branch at main's commit; Branches = %v", side.Branches)
	}
}

// TestOnBranchShallowClone documents the limit rather than pretending there
// isn't one: with no branch refs there is nothing for the commit to be
// reachable from, so the answer is "no" and the pipeline says why.
func TestOnBranchShallowClone(t *testing.T) {
	origin := originWithTag(t)
	clone := filepath.Join(t.TempDir(), "shallow")
	out, err := exec.Command("git", "clone", "-q", "--depth", "1", "--branch", "v1.0.0",
		"file://"+origin, clone).CombinedOutput()
	if err != nil {
		t.Skipf("shallow clone unsupported here: %v: %s", err, out)
	}
	info, err := Gather(clone)
	if err != nil {
		t.Fatal(err)
	}
	if info.OnBranch("main") {
		t.Skip("this git populates branch refs for a shallow tag clone; the limit does not apply")
	}
	if len(info.Branches) != 0 {
		t.Errorf("Branches = %v, want none in a shallow tag clone", info.Branches)
	}
}
