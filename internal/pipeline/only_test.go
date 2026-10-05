// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/blairham/stevedore/internal/changed"
	"github.com/blairham/stevedore/internal/config"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".stevedore.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExpandOnly(t *testing.T) {
	two := writeConfig(t, "version: 1\nimages:\n  - {id: b, repositories: [r/b]}\n  - {id: a, repositories: [r/a]}\n")
	o, err := expandOnly(Options{ConfigPath: two, Only: []string{"all"}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(o.Only, []string{"b", "a"}) {
		t.Errorf("--only all = %v, want every image in config order", o.Only)
	}
	o, _ = expandOnly(Options{ConfigPath: two, Only: []string{"a"}})
	if !slices.Equal(o.Only, []string{"a"}) {
		t.Errorf("--only a = %v, want it untouched", o.Only)
	}
	// An image really named "all" keeps the literal meaning.
	named := writeConfig(t, "version: 1\nimages:\n  - {id: all, repositories: [r/all]}\n  - {id: x, repositories: [r/x]}\n")
	o, _ = expandOnly(Options{ConfigPath: named, Only: []string{"all"}})
	if !slices.Equal(o.Only, []string{"all"}) {
		t.Errorf("--only all with an image named all = %v, want [all]", o.Only)
	}
}

// --only all reaches the release: without the expansion it fails as an
// unknown image id.
func TestReleaseOnlyAll(t *testing.T) {
	dir := dryRunRepo(t)
	o := Options{Dir: dir, ConfigPath: filepath.Join(dir, ".stevedore.yaml"), DryRun: true, Only: []string{"all"}}
	stderr, err := captureStderr(t, func() error { return Release(o) })
	if err != nil {
		t.Fatalf("release --only all: %v\n%s", err, stderr)
	}
}

func TestReasonsFromPlanReachTheSummary(t *testing.T) {
	plan := `{"include":[{"group":"a","ids":["a","b"],"reason":"src/a changed since its release marker"},` +
		`{"group":"a","ids":["a","b"],"reason":"src/a changed since its release marker","platform":"linux/arm64"},` +
		`{"group":"c","ids":["c"],"reason":""}],"skipped":[]}`
	reasons, err := ReasonsFromPlan([]byte(plan))
	if err != nil {
		t.Fatal(err)
	}
	if reasons["b"] != "src/a changed since its release marker" || reasons["c"] != "" {
		t.Errorf("reasons = %v", reasons)
	}
	if _, err := ReasonsFromPlan([]byte("{not json")); err == nil {
		t.Error("a malformed plan parsed")
	}

	o := Options{Only: []string{"b", "c"}, PlanReasons: reasons}
	for id, want := range map[string]string{"b": "src/a changed since its release marker", "c": "selected via --only"} {
		_, got, err := changeDecision(o, config.ChangeDetection{}, ImagePlan{Image: config.Image{ID: id}}, changed.Scope{}, nil, false)
		if err != nil || got != want {
			t.Errorf("%s: reason = %q, %v; want %q", id, got, err, want)
		}
	}
}

func TestPlanFlatOnlyAndPins(t *testing.T) {
	r := &PlanResult{Include: []PlanEntry{
		{IDs: []string{"a", "b"}, Versions: map[string]string{"a": "1.0.0", "b": "1.0.0"}, Platform: "linux/amd64"},
		{IDs: []string{"a", "b"}, Versions: map[string]string{"a": "1.0.0", "b": "1.0.0"}, Platform: "linux/arm64"},
		{IDs: []string{"c"}, Versions: map[string]string{"c": "2.1.0"}},
	}}
	if got := r.FlatOnly(); got != "a,b,c" {
		t.Errorf("FlatOnly = %q", got)
	}
	if got, want := r.FlatPins(), "--pin-version a=1.0.0 --pin-version b=1.0.0 --pin-version c=2.1.0"; got != want {
		t.Errorf("FlatPins = %q, want %q", got, want)
	}
	out := filepath.Join(t.TempDir(), "output")
	t.Setenv("GITHUB_OUTPUT", out)
	if err := r.WriteGitHubOutput(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "only=a,b,c\npins=--pin-version a=1.0.0 --pin-version b=1.0.0 --pin-version c=2.1.0\n"; got != want {
		t.Errorf("GITHUB_OUTPUT = %q, want %q", got, want)
	}
	if strings.Contains(string(data), "plan=") {
		t.Error("wrote the plan itself; the action does that")
	}
}
