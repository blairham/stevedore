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
			{
				ID:         "checkout",
				Version:    "0.0.336",
				Digest:     "sha256:abcdef0123456789",
				Signed:     true,
				SBOM:       true,
				Provenance: true,
				Tested:     true,
				Vulns:      map[string]int{"high": 2, "low": 5},
			},
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
	if got := vulnCell(false, nil); got != "—" {
		t.Errorf("unscanned = %q, want —", got)
	}
	// A scan that found nothing is clean, not indistinguishable from no scan
	// (#66) — whether the count map is nil or empty.
	if got := vulnCell(true, nil); got != "clean" {
		t.Errorf("scanned, nil counts = %q, want clean", got)
	}
	if got := vulnCell(true, map[string]int{}); got != "clean" {
		t.Errorf("scanned, empty counts = %q, want clean", got)
	}
	if got := vulnCell(true, map[string]int{"critical": 1, "medium": 3}); got != "1 critical, 3 medium" {
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
	line, _, _ := strings.Cut(string(data), "\n")
	if !strings.HasPrefix(line, "summary={") {
		t.Fatalf("want a single-line summary=<json> output first, got %q", data)
	}
	var parsed Result
	if err := json.Unmarshal([]byte(line[len("summary="):]), &parsed); err != nil {
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

// refs/digests key each pinned image's first repository@digest by id; ref and
// digest are set only when there is exactly one, so a multi-image release
// cannot be mistaken for its first image.
func TestWriteGitHubOutputPins(t *testing.T) {
	out := filepath.Join(t.TempDir(), "output")
	t.Setenv("GITHUB_OUTPUT", out)
	api := Image{ID: "api", Digest: "sha256:aa", DigestRefs: []string{"reg/api@sha256:aa", "mirror/api@sha256:aa"}}
	skipped := Image{ID: "web", Skipped: true}
	if err := (Result{Images: []Image{api, skipped}}).WriteGitHubOutput(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	for _, want := range []string{
		`refs={"api":"reg/api@sha256:aa"}`, `digests={"api":"sha256:aa"}`,
		"ref=reg/api@sha256:aa", "digest=sha256:aa",
	} {
		if !strings.Contains(string(data), want+"\n") {
			t.Errorf("missing %q in:\n%s", want, data)
		}
	}

	os.Remove(out)
	worker := Image{ID: "worker", Digest: "sha256:bb", DigestRefs: []string{"reg/worker@sha256:bb"}}
	if err := (Result{Images: []Image{api, worker}}).WriteGitHubOutput(); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(out)
	if !strings.Contains(string(data), `refs={"api":"reg/api@sha256:aa","worker":"reg/worker@sha256:bb"}`) {
		t.Errorf("refs for two images missing:\n%s", data)
	}
	if strings.Contains(string(data), "\nref=") || strings.Contains(string(data), "\ndigest=") {
		t.Errorf("ref/digest set with two pinned images:\n%s", data)
	}
}

// A clean scanned image renders as clean and says scanned in the JSON; an
// unscanned one does neither (#66).
func TestCleanScanIsVisible(t *testing.T) {
	r := Result{Project: "p", Images: []Image{
		{ID: "clean", Digest: "sha256:aa", Scanned: true, Vulns: map[string]int{}},
		{ID: "unscanned", Digest: "sha256:bb"},
	}}
	md := r.Markdown()
	if !strings.Contains(md, "| clean |\n") || strings.Count(md, "| clean |") != 1 {
		t.Errorf("want exactly the scanned image rendered clean:\n%s", md)
	}
	data, err := r.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		Images []struct {
			ID      string `json:"id"`
			Scanned bool   `json:"scanned"`
		} `json:"images"`
	}
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if !back.Images[0].Scanned || back.Images[1].Scanned {
		t.Errorf("scanned flags = %+v, want [true false]", back.Images)
	}
}
