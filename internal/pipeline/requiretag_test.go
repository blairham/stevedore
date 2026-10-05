// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const requireTagConfig = `version: 1
project_name: app
images:
  - id: app
    repositories: [ghcr.io/x/app]
versioning:
  require_tag: %v
change_detection:
  marker_refs: true
`

// requireTagRepo is a repository whose only image was released from HEAD
// (its marker points there), so change detection skips it.
func requireTagRepo(t *testing.T, requireTag bool) (string, func(...string)) {
	t.Helper()
	for k, v := range map[string]string{
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t.co",
		"GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t.co",
		"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1",
	} {
		t.Setenv(k, v)
	}
	root := t.TempDir()
	origin, dir := filepath.Join(root, "origin.git"), filepath.Join(root, "repo")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if len(args) > 0 && args[0] != "init" {
			cmd.Dir = dir
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "--bare", origin)
	git("init", "-q", "-b", "main", dir)
	cfg := strings.Replace(requireTagConfig, "%v", map[bool]string{true: "true", false: "false"}[requireTag], 1)
	for name, body := range map[string]string{".stevedore.yaml": cfg, "Dockerfile": "FROM scratch\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("add", ".")
	git("commit", "-q", "-m", "feat: first")
	git("remote", "add", "origin", origin)
	git("push", "-q", "origin", "main", "HEAD:refs/releases/image/app")
	return dir, git
}

// A tag on a commit whose sources did not change since the last release is
// still a release: under require_tag it builds every image instead of being
// skipped by change detection.
func TestRequireTagBuildsEveryImageOnATag(t *testing.T) {
	for _, requireTag := range []bool{false, true} {
		dir, git := requireTagRepo(t, requireTag)
		git("tag", "v1.0.0")
		o := Options{Dir: dir, ConfigPath: filepath.Join(dir, ".stevedore.yaml")}
		res, err := Plan(o)
		if err != nil {
			t.Fatal(err)
		}
		built := len(res.Include) == 1
		if built != requireTag {
			t.Fatalf("require_tag=%v: built=%v (include %+v, skipped %+v)", requireTag, built, res.Include, res.Skipped)
		}
		if requireTag && !strings.Contains(res.Include[0].Reason, "require_tag") {
			t.Errorf("reason = %q, want it to name require_tag", res.Include[0].Reason)
		}
	}
}

// Untagged, the same release job is a validate-only build rather than a
// refusal; a split leg or merge, which has no such form, is refused.
func TestRequireTagUntaggedValidatesOnly(t *testing.T) {
	dir, git := requireTagRepo(t, true)
	// A change since the marker, so the validation run builds.
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\nLABEL x=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("commit", "-q", "-am", "feat: second")
	base := Options{Dir: dir, ConfigPath: filepath.Join(dir, ".stevedore.yaml")}

	o, err := applyRequireTag(base)
	if err != nil {
		t.Fatal(err)
	}
	if !o.NoPush || o.Push || !o.Snapshot || o.BuildAll {
		t.Errorf("untagged: NoPush=%v Push=%v Snapshot=%v BuildAll=%v, want a validate-only snapshot", o.NoPush, o.Push, o.Snapshot, o.BuildAll)
	}

	for name, leg := range map[string]Options{
		"split": {SplitPlatforms: []string{"linux/amd64"}},
		"merge": {FromDigests: true},
	} {
		leg.Dir, leg.ConfigPath = base.Dir, base.ConfigPath
		if _, legErr := applyRequireTag(leg); legErr == nil || !strings.Contains(legErr.Error(), "no version tag") {
			t.Errorf("%s: err = %v, want a refusal", name, legErr)
		}
	}

	// A real release in the untagged repo would refuse (no tag on HEAD);
	// under require_tag it is a validation build and succeeds.
	stderr, err := captureStderr(t, func() error {
		saved := progress
		defer func() { progress = saved }()
		progress = os.Stderr
		o := base
		o.DryRun = true
		return Release(o)
	})
	if err != nil {
		t.Fatalf("release: %v\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "validate-only build") {
		t.Errorf("release did not say it ran validate-only:\n%s", stderr)
	}
	if !strings.Contains(stderr, "docker buildx build") {
		t.Fatalf("release built nothing, so the push check below would prove nothing:\n%s", stderr)
	}
	if strings.Contains(stderr, "push=true") || strings.Contains(stderr, "--push") {
		t.Errorf("validate-only release pushed:\n%s", stderr)
	}
}
