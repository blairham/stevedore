// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package summary

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func demo() Result {
	return Result{
		Project: "acme",
		Images: []Image{
			{ID: "checkout", Version: "0.0.336", Digest: "sha256:abcdef0123456789", Signed: true, SBOM: true, Provenance: true, Tested: true, Vulns: map[string]int{"high": 2, "low": 5}},
			{ID: "reconciler", Skipped: true},
		},
	}
}

func TestJSON(t *testing.T) {
	data, err := demo().JSON()
	if err != nil {
		t.Fatal(err)
	}
	var back Result
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	if back.Project != "acme" || len(back.Images) != 2 {
		t.Errorf("round-trip mismatch: %+v", back)
	}
	if back.Images[0].Version != "0.0.336" || !back.Images[0].Signed {
		t.Errorf("image fields lost: %+v", back.Images[0])
	}
}

func TestMarkdown(t *testing.T) {
	md := demo().Markdown()
	for _, want := range []string{
		"## stevedore release — acme",
		"`checkout`",
		"0.0.336",
		"abcdef012345", // digest truncated to 12
		"2 high, 5 low",
		"_skipped (unchanged)_",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
}

func TestVulnCell(t *testing.T) {
	if got := vulnCell(nil); got != "—" {
		t.Errorf("empty vulns = %q", got)
	}
	if got := vulnCell(map[string]int{"critical": 1, "medium": 3}); got != "1 critical, 3 medium" {
		t.Errorf("vulnCell = %q (want most-severe first)", got)
	}
}

func TestShortDigest(t *testing.T) {
	if got := shortDigest("sha256:0123456789abcdef"); got != "0123456789ab" {
		t.Errorf("shortDigest = %q", got)
	}
	if got := shortDigest(""); got != "—" {
		t.Errorf("empty digest = %q", got)
	}
}

func TestMarkdownSkippedReason(t *testing.T) {
	r := Result{Images: []Image{{ID: "checkout", Skipped: true, Reason: "inputs unchanged"}, {ID: "bare", Skipped: true}}}
	md := r.Markdown()
	if !strings.Contains(md, "_skipped (inputs unchanged)_") {
		t.Errorf("skip reason missing from markdown:\n%s", md)
	}
	if !strings.Contains(md, "_skipped (unchanged)_") {
		t.Errorf("reasonless skip should fall back to 'unchanged':\n%s", md)
	}
}

func TestWriteGitHubOutput(t *testing.T) {
	out := filepath.Join(t.TempDir(), "output")
	t.Setenv("GITHUB_OUTPUT", out)
	r := Result{Project: "acme", Images: []Image{{
		ID: "checkout", Version: "0.0.532", Repositories: []string{"reg/acme/checkout"},
		Pushed: true, Reason: "Acme/… since its release marker",
	}}}
	if err := r.WriteGitHubOutput(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	line := string(data)
	if !strings.HasPrefix(line, "summary={") || strings.Count(line, "\n") != 1 {
		t.Fatalf("want single-line summary=<json> output, got %q", line)
	}
	var parsed Result
	if err := json.Unmarshal(data[len("summary="):len(data)-1], &parsed); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	img := parsed.Images[0]
	if !img.Pushed || img.Repositories[0] != "reg/acme/checkout" || img.Reason == "" {
		t.Errorf("round-trip lost fields: %+v", img)
	}

	t.Setenv("GITHUB_OUTPUT", "")
	if err := (Result{}).WriteGitHubOutput(); err != nil {
		t.Errorf("no-op without env, got %v", err)
	}
}

func TestMarkdownDegraded(t *testing.T) {
	r := demo()
	if md := r.Markdown(); strings.Contains(md, "Degraded") {
		t.Errorf("a release with every stage run is marked degraded:\n%s", md)
	}
	r.Degraded = []string{"scan", "test"}
	if md := r.Markdown(); !strings.Contains(md, "**Degraded release:** ran without scan, test") {
		t.Errorf("degraded stages missing from the summary:\n%s", md)
	}
}

// A multi-platform image gets a per-platform table, so a skipped smoke test on
// one variant is visible; a single-platform image does not.
func TestMarkdownPlatforms(t *testing.T) {
	r := Result{Images: []Image{
		{ID: "app", Platforms: []Platform{
			{Platform: "linux/amd64", Scanned: true, Tested: true},
			{Platform: "linux/arm64", Scanned: true, TestSkipped: "no emulator"},
		}},
		{ID: "solo", Platforms: []Platform{{Platform: "linux/amd64", Scanned: true}}},
	}}
	md := r.Markdown()
	for _, want := range []string{"| platform |", "| `app` | linux/arm64 | ✓ | _skipped_ |"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "| `solo` | linux/amd64") {
		t.Errorf("single-platform image should not get platform rows:\n%s", md)
	}
}
