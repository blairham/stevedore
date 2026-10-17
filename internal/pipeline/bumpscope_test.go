// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blairham/stevedore/internal/changed"
	"github.com/blairham/stevedore/internal/gitinfo"
)

// monoConfig is a two-image monorepo versioned per image from the registry:
// api declares its paths, worker has none and falls back to its build context.
// go.mod is in api's paths and is the shared root of worker's, so it is in
// both images' sources.
const monoConfig = `version: 1
project_name: mono
images:
  - id: api
    repositories: [reg/api]
    dockerfile: api/Dockerfile
    context: api
    paths: ["api/**", "go.mod"]
  - id: worker
    repositories: [reg/worker]
    dockerfile: worker/Dockerfile
    context: worker
change_detection:
  shared_paths: ["go.mod"]
%[2]sversioning:
  strategy: registry
  lister: crane
  bump: auto
%[1]s`

// monoRepo is a monorepo whose images were both last released as 1.4.2 (the
// fake crane lists that tag for every repository, and v1.4.2 is the git tag),
// with extra versioning and change_detection keys. Each change is a commit subject
// and the file it writes; a "" file is an empty commit.
type change struct{ subject, file string }

func monoRepo(
	t *testing.T,
	versioning, changeDetection string,
	changes ...change,
) (Options, func(args ...string) string) {
	t.Helper()
	for k, v := range map[string]string{
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t.co",
		"GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t.co",
		"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1",
	} {
		t.Setenv(k, v)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "crane"), []byte("#!/bin/sh\necho 1.4.2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, body string) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "gc.auto", "0")
	git("config", "maintenance.auto", "false")
	write(".stevedore.yaml", fmt.Sprintf(monoConfig, versioning, changeDetection))
	for _, f := range []string{"api/Dockerfile", "worker/Dockerfile"} {
		write(f, "FROM scratch\n")
	}
	write("go.mod", "module x\n")
	write("api/main.go", "package main\n")
	write("worker/main.go", "package main\n")
	git("add", ".")
	// Before the tag, so a range that ignored the tag would see it.
	git("commit", "-q", "-m", "feat!: an old breaking change")
	git("tag", "v1.4.2")
	for _, c := range changes {
		if c.file == "" {
			git("commit", "-q", "--allow-empty", "-m", c.subject)
			continue
		}
		write(c.file, c.subject+"\n")
		git("add", ".")
		git("commit", "-q", "-m", c.subject)
	}
	return Options{Dir: dir, ConfigPath: filepath.Join(dir, ".stevedore.yaml")}, git
}

// planned returns each prepared image's version and version reason.
func planned(t *testing.T, o Options) (map[string]string, map[string]string, *Prepared) {
	t.Helper()
	p, err := Prepare(o)
	if err != nil {
		t.Fatal(err)
	}
	versions, reasons := map[string]string{}, map[string]string{}
	for _, pl := range p.Plans {
		versions[pl.Image.ID], reasons[pl.Image.ID] = pl.Version, pl.VersionReason
	}
	return versions, reasons, p
}

// A feat in one image's path and a fix in the other's: each image takes the
// bump its own commits call for, and says which commit decided it.
func TestBumpAutoScopedPerImage(t *testing.T) {
	o, _ := monoRepo(t, "", "",
		change{"feat(api): add pagination", "api/page.go"},
		change{"fix(worker): retry", "worker/retry.go"},
		change{"docs: readme", "README.md"},
	)
	versions, reasons, p := planned(t, o)
	if versions["api"] != "1.5.0" || versions["worker"] != "1.4.3" {
		t.Errorf("versions = %v, want api 1.5.0 (its feat) and worker 1.4.3 (its fix)", versions)
	}
	if !strings.HasPrefix(reasons["api"], "minor: feat(api): add pagination, ") {
		t.Errorf("api reason = %q, want its feat", reasons["api"])
	}
	if !strings.HasPrefix(reasons["worker"], "patch: fix(worker): retry, ") {
		t.Errorf("worker reason = %q, want its fix", reasons["worker"])
	}
	// The release version anchors on the first image, scoped the same way.
	if p.Ctx.Version != "1.5.0" || p.Version.Reason() != reasons["api"] {
		t.Errorf("release = %s (%s), want api's 1.5.0 and reason", p.Ctx.Version, p.Version.Reason())
	}

	res, err := Plan(o)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range res.Include {
		for id, v := range e.Versions {
			got[id] = v + " " + e.VersionReasons[id]
		}
	}
	if !strings.HasPrefix(got["api"], "1.5.0 minor: feat(api)") ||
		!strings.HasPrefix(got["worker"], "1.4.3 patch: fix(worker)") {
		t.Errorf("plan = %v, want each image's own version and deciding commit", got)
	}
}

// A commit to a file both images are built from bumps both.
func TestBumpAutoScopedSharedPath(t *testing.T) {
	o, _ := monoRepo(t, "", "", change{"feat: go 1.27", "go.mod"})
	versions, reasons, _ := planned(t, o)
	for _, id := range []string{"api", "worker"} {
		if versions[id] != "1.5.0" || !strings.HasPrefix(reasons[id], "minor: feat: go 1.27, ") {
			t.Errorf("%s = %s (%s), want 1.5.0 from the shared go.mod commit", id, versions[id], reasons[id])
		}
	}
}

// An image none of whose commits call for a bump follows on_no_changes by
// itself: skip leaves it with nothing to release while the other releases.
func TestBumpAutoScopedNothingToRelease(t *testing.T) {
	o, _ := monoRepo(t, "", "", change{"feat(api): add pagination", "api/page.go"})
	versions, _, p := planned(t, o)
	for _, pl := range p.Plans {
		switch pl.Image.ID {
		case "api":
			if pl.NoRelease != "" || versions["api"] != "1.5.0" {
				t.Errorf("api = %s (no release %q), want 1.5.0", versions["api"], pl.NoRelease)
			}
		case "worker":
			if !strings.Contains(pl.NoRelease, "nothing to release: no commit since 1.4.2 touching context worker") {
				t.Errorf("worker no release = %q, want nothing to release, naming its scope", pl.NoRelease)
			}
		}
	}
	res, err := Plan(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Include) != 1 || res.Include[0].Versions["api"] != "1.5.0" ||
		len(res.Skipped) != 1 || res.Skipped[0].ID != "worker" {
		t.Errorf("plan = %+v, want api built and worker skipped", res)
	}

	// on_no_changes: patch releases worker as a patch on its own.
	o, _ = monoRepo(t, "  on_no_changes: patch\n", "", change{"feat(api): add pagination", "api/page.go"})
	versions, reasons, _ := planned(t, o)
	if versions["api"] != "1.5.0" || versions["worker"] != "1.4.3" ||
		!strings.Contains(reasons["worker"], "touching context worker") {
		t.Errorf("versions = %v, worker reason %q; want api 1.5.0 and worker a 1.4.3 patch", versions, reasons["worker"])
	}
}

// A unified version (versioning.repo) reflects every change in the
// repository, so its range is not scoped to any image.
func TestBumpAutoUnifiedIgnoresScope(t *testing.T) {
	o, _ := monoRepo(t, "  repo: reg/api\n", "",
		change{"feat(worker): new queue", "worker/queue.go"},
		change{"fix(api): typo", "api/typo.go"},
	)
	versions, reasons, _ := planned(t, o)
	for _, id := range []string{"api", "worker"} {
		if versions[id] != "1.5.0" || !strings.HasPrefix(reasons[id], "minor: feat(worker): new queue, ") {
			t.Errorf("%s = %s (%s), want the unified 1.5.0 from worker's feat", id, versions[id], reasons[id])
		}
	}
}

// With marker_refs, an image's last release is its marker: commits before it
// were released already, even though the version tag is older.
func TestBumpAutoScopedSinceMarker(t *testing.T) {
	o, git := monoRepo(t, "", "  marker_refs: true\n",
		change{"feat(api): released already", "api/old.go"},
	)
	git("update-ref", "refs/releases/image/api", "HEAD")
	git("commit", "-q", "--allow-empty", "-m", "chore: between")
	if err := os.WriteFile(filepath.Join(o.Dir, "api", "new.go"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "fix(api): after the release")

	versions, reasons, _ := planned(t, o)
	if versions["api"] != "1.4.3" || !strings.HasPrefix(reasons["api"], "patch: fix(api): after the release, ") {
		t.Errorf("api = %s (%s), want 1.4.3 from the fix since its marker", versions["api"], reasons["api"])
	}
	// worker has no marker: its range is the version tag's, scoped.
	if versions["worker"] != "1.4.2" {
		t.Errorf("worker = %s, want nothing to release (1.4.2) — nothing since v1.4.2 touched it", versions["worker"])
	}
}

// touching keeps a commit exactly when change detection would have marked the
// image changed by that commit's files alone.
func TestTouching(t *testing.T) {
	commits := []gitinfo.Commit{{SHA: "a"}, {SHA: "b"}, {SHA: "c"}, {SHA: "d"}}
	files := map[string][]string{
		"a": {"api/x.go"},
		"b": {"worker/y.go"},
		"c": {"go.mod"},
		"d": nil, // an empty commit
	}
	shas := func(cs []gitinfo.Commit) string {
		s := make([]string, 0, len(cs))
		for _, c := range cs {
			s = append(s, c.SHA)
		}
		return strings.Join(s, "")
	}
	if got := shas(touching(commits, files, changed.Scope{Paths: []string{"api/**"}}, []string{"go.mod"})); got != "ac" {
		t.Errorf("api scope = %q, want a and the shared c", got)
	}
	if got := shas(touching(commits, files, changed.Scope{}, nil)); got != "abcd" {
		t.Errorf("unscoped = %q, want every commit", got)
	}
}
