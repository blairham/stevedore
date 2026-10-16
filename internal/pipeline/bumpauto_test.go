// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const bumpAutoConfig = `version: 1
project_name: app
images:
  - id: app
    repositories: [ghcr.io/x/app]
versioning:
  bump: auto
`

// bumpAutoRepo is a repository released as v1.4.2, with the given commits on
// top of the tag. Which commits git reports since the tag is the question, so
// the history is real.
func bumpAutoRepo(t *testing.T, subjects ...string) Options {
	t.Helper()
	for k, v := range map[string]string{
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t.co",
		"GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t.co",
		"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1",
	} {
		t.Setenv(k, v)
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	for name, body := range map[string]string{".stevedore.yaml": bumpAutoConfig, "Dockerfile": "FROM scratch\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("add", ".")
	// Before the tag, so a range that ignored the tag would see it.
	git("commit", "-q", "-m", "feat!: an old breaking change")
	git("tag", "v1.4.2")
	for _, s := range subjects {
		git("commit", "-q", "--allow-empty", "-m", s)
	}
	return Options{Dir: dir, ConfigPath: filepath.Join(dir, ".stevedore.yaml")}
}

// An untagged HEAD under bump: auto is a release of the next version, decided
// by the commits since the last tag, and check/plan say which commit decided.
func TestBumpAutoVersionsUntaggedHEAD(t *testing.T) {
	o := bumpAutoRepo(t, "fix: a", "feat(api): add pagination", "docs: b")
	p, err := Prepare(o)
	if err != nil {
		t.Fatal(err)
	}
	if p.Ctx.Version != "1.5.0" {
		t.Errorf("version = %q, want 1.5.0", p.Ctx.Version)
	}
	if r := p.Version.Reason(); !strings.HasPrefix(r, "minor: feat(api): add pagination, ") {
		t.Errorf("reason = %q, want the deciding feat commit", r)
	}
	if gerr := guardReleasable(p.Git, p.Config.Versioning); gerr != nil {
		t.Errorf("untagged HEAD under bump: auto is releasable: %v", gerr)
	}
	res, err := Plan(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Include) != 1 || res.Include[0].Versions["app"] != "1.5.0" {
		t.Fatalf("plan = %+v, want app at 1.5.0", res)
	}
	if r := res.Include[0].VersionReasons["app"]; !strings.HasPrefix(r, "minor: feat(api)") {
		t.Errorf("plan version reason = %q", r)
	}
}

// Nothing bump-worthy since the tag: nothing to release, so the plan builds
// nothing, and a snapshot still builds.
func TestBumpAutoNothingToRelease(t *testing.T) {
	o := bumpAutoRepo(t, "docs: a", "chore: b")
	res, err := Plan(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Include) != 0 || len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0].Reason, "nothing to release") {
		t.Fatalf("plan = %+v, want app skipped as nothing to release", res)
	}

	o.Snapshot = true
	res, err = Plan(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Include) != 1 || !strings.HasPrefix(res.Include[0].Versions["app"], "1.4.2-SNAPSHOT-") {
		t.Fatalf("snapshot plan = %+v, want app built as a 1.4.2 snapshot", res)
	}
}

// publish runs after the matrix jobs whatever they built; with nothing to
// release it must not cut a release of the last version again.
func TestBumpAutoPublishNothingToRelease(t *testing.T) {
	o := bumpAutoRepo(t, "docs: a")
	o.DryRun = true
	var out bytes.Buffer
	progressOut = &out
	t.Cleanup(func() { progressOut = os.Stdout })
	if err := Publish(o); err != nil {
		t.Fatalf("publish with nothing to release: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "nothing to release") || strings.Contains(got, "publish complete") {
		t.Errorf("publish output = %q, want nothing to release and no release", got)
	}
	if !nothingToRelease([]ImagePlan{{NoRelease: "x"}, {NoRelease: "y"}}) {
		t.Error("every plan has nothing to release: want true")
	}
	if nothingToRelease([]ImagePlan{{NoRelease: "x"}, {}}) || nothingToRelease(nil) {
		t.Error("a plan with something to release, or no plans: want false")
	}
}
