// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"os"
	"strings"
	"testing"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/run"
)

func TestPlatformFile(t *testing.T) {
	cases := []struct {
		platforms []string
		want      string
	}{
		{[]string{"linux/arm64"}, "linux-arm64"},
		{[]string{"linux/arm/v7"}, "linux-arm-v7"},
		// Order-independent: a leg building both arches gets one stable name.
		{[]string{"linux/arm64", "linux/amd64"}, "linux-amd64,linux-arm64"},
	}
	for _, tc := range cases {
		if got := platformFile(tc.platforms); got != tc.want {
			t.Errorf("platformFile(%v) = %q, want %q", tc.platforms, got, tc.want)
		}
	}
}

func TestSplitDigestRoundtrip(t *testing.T) {
	dir := t.TempDir()
	both := []string{"linux/amd64", "linux/arm64"}

	// Two legs, one image each in the group; every member gets the digest.
	if err := writeSplitDigest(dir, "dist", []string{"a", "b"}, []string{"linux/amd64"}, "sha256:aaa"); err != nil {
		t.Fatal(err)
	}
	if err := writeSplitDigest(dir, "dist", []string{"a", "b"}, []string{"linux/arm64"}, "sha256:bbb"); err != nil {
		t.Fatal(err)
	}

	digests, covered, err := readSplitDigests(dir, "dist", "a", both)
	if err != nil {
		t.Fatal(err)
	}
	if len(digests) != 2 || digests[0] != "sha256:aaa" || digests[1] != "sha256:bbb" {
		t.Errorf("digests = %v, want sorted [sha256:aaa sha256:bbb]", digests)
	}
	if !covered["linux-amd64"] || !covered["linux-arm64"] {
		t.Errorf("covered = %v, want both platforms", covered)
	}
	// Group member b sees the same digests.
	if bd, _, err := readSplitDigests(dir, "dist", "b", both); err != nil || len(bd) != 2 {
		t.Errorf("member b digests = %v (%v), want the same two", bd, err)
	}

	if _, _, err := readSplitDigests(dir, "dist", "missing", both); err == nil {
		t.Error("expected error for an image with no recorded digests")
	}
}

func TestMergeGroupCoversAllPlatformsOrFails(t *testing.T) {
	dir := t.TempDir()
	if err := writeSplitDigest(dir, "dist", []string{"app"}, []string{"linux/amd64"}, "sha256:aaa"); err != nil {
		t.Fatal(err)
	}
	rep := ImagePlan{
		Image: config.Image{ID: "app", Platforms: []string{"linux/amd64", "linux/arm64"}},
		Repos: []string{"ghcr.io/x/app"},
	}
	stderr, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	r := &run.Runner{DryRun: true, Stderr: stderr}

	_, err = mergeGroup(r, Options{Dir: dir, DryRun: true}, rep, "dist", rep.Repos)
	if err == nil || !strings.Contains(err.Error(), "linux/arm64") {
		t.Fatalf("want missing-platform error naming linux/arm64, got %v", err)
	}
}

func TestMergeGroupBuildsUntaggedListPerRepo(t *testing.T) {
	dir := t.TempDir()
	for _, leg := range []struct{ platform, digest string }{
		{"linux/amd64", "sha256:aaa"},
		{"linux/arm64", "sha256:bbb"},
	} {
		if err := writeSplitDigest(dir, "dist", []string{"app"}, []string{leg.platform}, leg.digest); err != nil {
			t.Fatal(err)
		}
	}
	rep := ImagePlan{
		Image: config.Image{ID: "app", Platforms: []string{"linux/amd64", "linux/arm64"}},
		Repos: []string{"ghcr.io/x/app", "reg.io/x/app"},
	}

	stderr, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	r := &run.Runner{DryRun: true, Stderr: stderr}

	digest, err := mergeGroup(r, Options{Dir: dir, DryRun: true}, rep, "dist", rep.Repos)
	if err != nil {
		t.Fatal(err)
	}
	if digest != "" {
		t.Errorf("dry-run merge digest = %q, want empty (placeholder is Release's job)", digest)
	}

	out, err := os.ReadFile(stderr.Name())
	if err != nil {
		t.Fatal(err)
	}
	cmds := string(out)
	want := []string{
		// The list's digest is computed first, then pushed to each repo by
		// that digest — sources from the same repo, and no tag.
		"buildx imagetools create --dry-run ghcr.io/x/app@sha256:aaa ghcr.io/x/app@sha256:bbb",
		"buildx imagetools create --tag ghcr.io/x/app@sha256:<digest-resolved-at-build-time> ghcr.io/x/app@sha256:aaa ghcr.io/x/app@sha256:bbb",
		"buildx imagetools create --tag reg.io/x/app@sha256:<digest-resolved-at-build-time> reg.io/x/app@sha256:aaa reg.io/x/app@sha256:bbb",
	}
	for _, w := range want {
		if !strings.Contains(cmds, w) {
			t.Errorf("missing imagetools invocation %q in:\n%s", w, cmds)
		}
	}
	// Tags are the gates' to grant: merging must not name a single one.
	if strings.Contains(cmds, "app:") {
		t.Errorf("merge applied a tag before the gates:\n%s", cmds)
	}
}

func TestNewPlanResult_SplitPerPlatform(t *testing.T) {
	ev := evalFor("app", "Dockerfile", "1.2.3", true, "src changed")
	ev.plan.Image.Platforms = []string{"linux/amd64", "linux/arm64"}
	r := newPlanResult([][]imageEval{{ev}}, nil, true)

	if len(r.Include) != 2 {
		t.Fatalf("include entries = %d, want one per platform", len(r.Include))
	}
	amd, arm := r.Include[0], r.Include[1]
	if amd.Platform != "linux/amd64" || amd.Runner != "ubuntu-24.04" {
		t.Errorf("amd64 entry = %+v", amd)
	}
	if arm.Platform != "linux/arm64" || arm.Runner != "ubuntu-24.04-arm" {
		t.Errorf("arm64 entry = %+v", arm)
	}
	if amd.Only != "app" || amd.Pins != "--pin-version app=1.2.3" {
		t.Errorf("split entries must keep only/pins: %+v", amd)
	}

	// No platforms configured → the group stays a single, unsplit entry.
	plain := evalFor("app", "Dockerfile", "1.2.3", true, "src changed")
	r = newPlanResult([][]imageEval{{plain}}, nil, true)
	if len(r.Include) != 1 || r.Include[0].Platform != "" {
		t.Errorf("platformless group should not split: %+v", r.Include)
	}
}

func TestDefaultRunner(t *testing.T) {
	cases := map[string]string{
		"linux/amd64":  "ubuntu-24.04",
		"linux/arm64":  "ubuntu-24.04-arm",
		"linux/arm/v7": "",
	}
	for platform, want := range cases {
		if got := defaultRunner(platform); got != want {
			t.Errorf("defaultRunner(%q) = %q, want %q", platform, got, want)
		}
	}
}

// A leg is driven by a platform matrix, not by the image: `--split linux/arm64`
// reaches every selected image, including amd64-only ones, which must be
// skipped rather than built for a platform their config excludes.
func TestSplitLegGroups_SkipsGroupsWithoutTheLegPlatform(t *testing.T) {
	multi := evalFor("multi", "Dockerfile.multi", "1.0.0", true, "src changed")
	multi.plan.Image.Platforms = []string{"linux/amd64", "linux/arm64"}
	amdOnly := evalFor("amd", "Dockerfile.amd", "1.0.0", true, "src changed")
	amdOnly.plan.Image.Platforms = []string{"linux/amd64"}
	groups := [][]imageEval{{multi}, {amdOnly}}

	kept, skipped := splitLegGroups(groups, nil, []string{"linux/arm64"})
	if len(kept) != 1 || kept[0][0].plan.Image.ID != "multi" {
		t.Fatalf("arm64 leg kept %v, want only multi", kept)
	}
	if len(skipped) != 1 || skipped[0].plan.Image.ID != "amd" {
		t.Fatalf("arm64 leg skipped %v, want amd", skipped)
	}
	if r := skipped[0].reason; !strings.Contains(r, "linux/arm64") || !strings.Contains(r, "linux/amd64") {
		t.Errorf("skip reason %q should name the leg platform and the image's platforms", r)
	}

	kept, skipped = splitLegGroups(groups, nil, []string{"linux/amd64"})
	if len(kept) != 2 || len(skipped) != 0 {
		t.Errorf("amd64 leg kept %d / skipped %d, want both kept", len(kept), len(skipped))
	}
}

func TestLegPlatforms(t *testing.T) {
	got := legPlatforms([]string{"linux/arm64", "linux/amd64"}, []string{"linux/amd64"})
	if strings.Join(got, ",") != "linux/amd64" {
		t.Errorf("legPlatforms = %v, want [linux/amd64]", got)
	}
	if got := legPlatforms([]string{"linux/arm64"}, []string{"linux/amd64"}); len(got) != 0 {
		t.Errorf("legPlatforms = %v, want none", got)
	}
}

// Every entry `plan --split-platforms` emits must survive the leg it drives:
// the plan and the leg filter have to agree on which platforms an image has.
func TestPlanSplitEntriesAreKeptByTheirLeg(t *testing.T) {
	multi := evalFor("multi", "Dockerfile.multi", "1.0.0", true, "src changed")
	multi.plan.Image.Platforms = []string{"linux/amd64", "linux/arm64"}
	amdOnly := evalFor("amd", "Dockerfile.amd", "1.0.0", true, "src changed")
	amdOnly.plan.Image.Platforms = []string{"linux/amd64"}
	groups := [][]imageEval{{multi}, {amdOnly}}

	r := newPlanResult(groups, nil, true)
	if len(r.Include) != 3 {
		t.Fatalf("plan entries = %d, want 3 (multi×2, amd×1)", len(r.Include))
	}
	for _, e := range r.Include {
		var grp []imageEval
		for _, g := range groups {
			if g[0].plan.Image.ID == e.Group {
				grp = g
			}
		}
		kept, _ := splitLegGroups([][]imageEval{grp}, nil, []string{e.Platform})
		if len(kept) != 1 {
			t.Errorf("plan entry %s/%s would be skipped by its own leg", e.Group, e.Platform)
		}
	}
}

// End to end against a fake docker: a two-platform leg building an amd64-only
// image builds and records amd64 alone.
func TestBuildSplitLeg_BuildsOnlyConfiguredPlatforms(t *testing.T) {
	dir, log, o, p, grp := gateHarness(t)
	grp[0].plan.Image.Platforms = []string{"linux/amd64"}
	o.SplitPlatforms = []string{"linux/amd64", "linux/arm64"}

	if _, _, err := buildGroup(o, p, quietRunner(t), grp); err != nil {
		t.Fatal(err)
	}
	calls := readCalls(t, log)
	build := indexOf(calls, "buildx build")
	if build < 0 {
		t.Fatalf("no build in:\n%s", strings.Join(calls, "\n"))
	}
	if !strings.Contains(calls[build], "--platform linux/amd64 ") || strings.Contains(calls[build], "arm64") {
		t.Errorf("leg build should target linux/amd64 only: %s", calls[build])
	}
	entries, err := os.ReadDir(splitDigestDir(dir, "dist", "app"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "linux-amd64" {
		t.Errorf("digest files = %v, want only linux-amd64", entries)
	}
}

// A digest for a platform the image does not configure — a stale file in a
// persistent dist/, or a leg that built it anyway — must stop the merge, not
// ship an extra platform.
func TestMergeGroupRejectsUnconfiguredPlatform(t *testing.T) {
	dir := t.TempDir()
	for _, leg := range []struct{ platform, digest string }{
		{"linux/amd64", "sha256:aaa"},
		{"linux/arm64", "sha256:bbb"},
	} {
		if err := writeSplitDigest(dir, "dist", []string{"app"}, []string{leg.platform}, leg.digest); err != nil {
			t.Fatal(err)
		}
	}
	rep := ImagePlan{
		Image: config.Image{ID: "app", Platforms: []string{"linux/amd64"}},
		Repos: []string{"ghcr.io/x/app"},
	}
	stderr, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	r := &run.Runner{DryRun: true, Stderr: stderr}

	_, err = mergeGroup(r, Options{Dir: dir, DryRun: true}, rep, "dist", rep.Repos)
	if err == nil || !strings.Contains(err.Error(), "linux-arm64") {
		t.Fatalf("want an unexpected-platform error naming linux-arm64, got %v", err)
	}
	out, _ := os.ReadFile(stderr.Name())
	if strings.Contains(string(out), "imagetools") {
		t.Errorf("merge ran imagetools despite the unexpected digest:\n%s", out)
	}
}
