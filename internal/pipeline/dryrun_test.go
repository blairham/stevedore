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

const dryRunConfig = `version: 1
project_name: app
images:
  - id: app
    dockerfile: Dockerfile
    context: .
    platforms: [linux/amd64, linux/arm64]
    repositories: [ghcr.io/x/app]
    tags: ["{{ .Version }}"]
scan:
  enabled: true
  scanner: grype
test:
  enabled: true
  cmd: ["--version"]
sign:
  cosign:
    enabled: true
sbom:
  enabled: true
  attest: true
changelog:
  enabled: true
change_detection:
  marker_refs: true
policy:
  require: [scan, test, sign, sbom]
`

// dryRunRepo is a tagged repository with an origin that carries a release
// marker one commit behind HEAD, so a release in it walks every stage that writes a file and the
// marker fetch that touches the network. Git is isolated from the host's
// config for stevedore's own git calls as well as the setup's.
func dryRunRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for k, v := range map[string]string{
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t.co",
		"GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t.co",
		"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1",
	} {
		t.Setenv(k, v)
	}
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	origin := filepath.Join(root, "origin.git")
	dir := filepath.Join(root, "repo")
	git(root, "init", "-q", "--bare", origin)
	git(root, "init", "-q", "-b", "main", dir)
	for name, body := range map[string]string{".stevedore.yaml": dryRunConfig, "Dockerfile": "FROM scratch\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(dir, "add", ".")
	git(dir, "commit", "-q", "-m", "feat: first")
	git(dir, "remote", "add", "origin", origin)
	git(dir, "push", "-q", "origin", "main", "HEAD:refs/releases/image/app")
	// A change since the marker, so the image builds rather than skips.
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\nLABEL x=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(dir, "commit", "-q", "-am", "feat: second")
	git(dir, "tag", "v0.1.0")
	// Merged: a real release is only cut from the default branch.
	git(dir, "push", "-q", "origin", "main")
	return dir
}

// captureStderr runs fn with os.Stderr pointed at a file and returns what was
// written there — the Runner echoes every command to stderr.
func captureStderr(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = f
	runErr := fn()
	os.Stderr = saved
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(data), runErr
}

// A dry run writes nothing: following it with a real release must not trip
// the dirty-tree guard on a dist/CHANGELOG.md it left behind. It still runs
// the marker fetch planning needs, and says so.
func TestDryRunWritesNothing(t *testing.T) {
	dir := dryRunRepo(t)
	actions := t.TempDir()
	stepSummary := filepath.Join(actions, "step-summary")
	output := filepath.Join(actions, "output")
	t.Setenv("GITHUB_STEP_SUMMARY", stepSummary)
	t.Setenv("GITHUB_OUTPUT", output)

	for _, snapshot := range []bool{true, false} {
		stderr, err := captureStderr(t, func() error {
			return Release(Options{Dir: dir, ConfigPath: filepath.Join(dir, ".stevedore.yaml"), DryRun: true, Snapshot: snapshot})
		})
		if err != nil {
			t.Fatalf("snapshot=%v: %v\n%s", snapshot, err, stderr)
		}
		cmd := exec.Command("git", "status", "--porcelain", "--untracked-files=all", "--ignored")
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		if s := strings.TrimSpace(string(out)); s != "" {
			t.Errorf("snapshot=%v: dry run left the tree dirty:\n%s", snapshot, s)
		}
		// git status cannot see an empty directory.
		for _, f := range []string{stepSummary, output, filepath.Join(dir, "dist")} {
			if _, err := os.Stat(f); err == nil {
				t.Errorf("snapshot=%v: dry run wrote %s", snapshot, f)
			}
		}
		if !strings.Contains(stderr, "+ git -C "+dir+" fetch --quiet origin +refs/releases/image/*:refs/releases/image/*") {
			t.Errorf("snapshot=%v: the marker fetch ran unannounced:\n%s", snapshot, stderr)
		}
		// The positive control for the echo assertion above: the dry run did
		// get as far as the stages that would have written files.
		if !strings.Contains(stderr, "[dry-run] syft ") {
			t.Errorf("snapshot=%v: dry run never reached the SBOM stage:\n%s", snapshot, stderr)
		}
	}
}
