// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blairham/stevedore/internal/config"
)

func TestAllPinned(t *testing.T) {
	plans := []ImagePlan{{Image: config.Image{ID: "a"}}, {Image: config.Image{ID: "b"}}}
	if allPinned(plans, map[string]string{"a": "1.0.0"}) {
		t.Error("one of two pinned counted as all pinned")
	}
	if !allPinned(plans, map[string]string{"a": "1.0.0", "b": "2.0.0"}) {
		t.Error("every image pinned, yet not all pinned")
	}
	if allPinned(nil, map[string]string{"a": "1.0.0"}) {
		t.Error("no plans counted as all pinned")
	}
}

func TestCheckSecrets(t *testing.T) {
	t.Setenv("SET_TOKEN", "x")
	t.Setenv("UNSET_TOKEN", "")
	t.Setenv("by_id", "")
	plan := func(s ...config.Secret) []ImagePlan {
		return []ImagePlan{{Image: config.Image{ID: "app", Secrets: s}}}
	}
	cases := []struct {
		name   string
		secret config.Secret
		want   string // "" = accepted
	}{
		{"set", config.Secret{ID: "tok", Env: "SET_TOKEN"}, ""},
		{"unset", config.Secret{ID: "tok", Env: "UNSET_TOKEN"}, `image app: secret "tok": $UNSET_TOKEN is unset`},
		{"unset, env from id", config.Secret{ID: "by_id"}, "$by_id is unset"},
		{"unset but optional", config.Secret{ID: "tok", Env: "UNSET_TOKEN", Optional: true}, ""},
		{"file-backed", config.Secret{ID: "tok", File: "/nonexistent"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSecrets(plan(tc.secret))
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// fakeTool installs an executable script as name on PATH.
func fakeTool(t *testing.T, name, body string) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(bin, name),
		[]byte("#!/bin/sh\n"+body),
		0o755,
	); err != nil { //nolint:gosec // G306: a test fake must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func clearGHTokens(t *testing.T) {
	t.Helper()
	for _, env := range ghTokenEnv {
		t.Setenv(env, "")
	}
}

func TestCheckGitHubAuth(t *testing.T) {
	clearGHTokens(t)
	fakeTool(t, "gh", "echo 'You are not logged into any GitHub hosts.' >&2\nexit 1\n")
	err := checkGitHubAuth(Options{})
	if err == nil || !strings.Contains(err.Error(), "not authenticated (You are not logged into any GitHub hosts.)") {
		t.Fatalf("err = %v, want the gh refusal with its own message", err)
	}
	// A token in the environment is how gh authenticates in CI; gh itself
	// (failing above) is not asked.
	t.Setenv("GH_TOKEN", "x")
	if err := checkGitHubAuth(Options{}); err != nil {
		t.Fatalf("with GH_TOKEN: %v", err)
	}
	clearGHTokens(t)
	fakeTool(t, "gh", "exit 0\n")
	if err := checkGitHubAuth(Options{}); err != nil {
		t.Fatalf("authenticated gh: %v", err)
	}
}

// preflightConfig is written outside the repository so the tree stays clean.
const preflightConfig = `version: 1
project_name: app
images:
  - id: app
    repositories: [ghcr.io/x/app]
    secrets:
      - {id: npm, env: PREFLIGHT_NPM_TOKEN}
release:
  github:
    enabled: true
`

// Both refusals happen before anything is built or pushed: an unset secret
// and an unauthenticated gh each stop a real release in preflight.
func TestReleasePreflightRefusesBeforePushing(t *testing.T) {
	dir := dryRunRepo(t)
	cfg := filepath.Join(t.TempDir(), "stevedore.yaml")
	if err := os.WriteFile(cfg, []byte(preflightConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	clearGHTokens(t)
	fakeTool(t, "docker", "echo \"docker $*\" >> \"$FAKE_LOG\"\n")
	fakeTool(t, "gh", "echo 'not logged in' >&2\nexit 1\n")
	log := filepath.Join(t.TempDir(), "calls.log")
	t.Setenv("FAKE_LOG", log)
	o := Options{Dir: dir, ConfigPath: cfg}

	t.Setenv("PREFLIGHT_NPM_TOKEN", "")
	if err := Release(o); err == nil || !strings.Contains(err.Error(), "$PREFLIGHT_NPM_TOKEN is unset") {
		t.Fatalf("unset secret: err = %v", err)
	}
	t.Setenv("PREFLIGHT_NPM_TOKEN", "x")
	if err := Release(o); err == nil || !strings.Contains(err.Error(), "gh is not authenticated") {
		t.Fatalf("unauthenticated gh: err = %v", err)
	}
	calls, _ := os.ReadFile(log)
	if strings.Contains(string(calls), "buildx build") {
		t.Errorf("built before preflight refused:\n%s", calls)
	}
	if !strings.Contains(string(calls), "docker version") {
		t.Errorf("the docker fake never ran, so the no-build check above proves nothing:\n%s", calls)
	}

	// A snapshot never publishes and may build without the secret.
	t.Setenv("PREFLIGHT_NPM_TOKEN", "")
	o.Snapshot, o.DryRun = true, true
	if _, err := captureStderr(
		t,
		func() error { return Release(o) },
	); err != nil &&
		strings.Contains(err.Error(), "is unset") {
		t.Errorf("snapshot refused an unset secret: %v", err)
	}
}
