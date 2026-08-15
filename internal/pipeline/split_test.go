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

	// Two legs, one image each in the group; every member gets the digest.
	if err := writeSplitDigest(dir, "dist", []string{"a", "b"}, []string{"linux/amd64"}, "sha256:aaa"); err != nil {
		t.Fatal(err)
	}
	if err := writeSplitDigest(dir, "dist", []string{"a", "b"}, []string{"linux/arm64"}, "sha256:bbb"); err != nil {
		t.Fatal(err)
	}

	digests, covered, err := readSplitDigests(dir, "dist", "a")
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
	if bd, _, err := readSplitDigests(dir, "dist", "b"); err != nil || len(bd) != 2 {
		t.Errorf("member b digests = %v (%v), want the same two", bd, err)
	}

	if _, _, err := readSplitDigests(dir, "dist", "missing"); err == nil {
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

	_, err = mergeGroup(r, Options{Dir: dir, DryRun: true}, rep, "dist", rep.Repos, []string{"ghcr.io/x/app:1.0.0"})
	if err == nil || !strings.Contains(err.Error(), "linux/arm64") {
		t.Fatalf("want missing-platform error naming linux/arm64, got %v", err)
	}
}

func TestMergeGroupBuildsImagetoolsCreatePerRepo(t *testing.T) {
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
	refs := []string{"ghcr.io/x/app:1.0.0", "ghcr.io/x/app:latest", "reg.io/x/app:1.0.0"}

	stderr, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	r := &run.Runner{DryRun: true, Stderr: stderr}

	digest, err := mergeGroup(r, Options{Dir: dir, DryRun: true}, rep, "dist", rep.Repos, refs)
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
		// One create per repo: its own tags, sources by digest.
		"buildx imagetools create --tag ghcr.io/x/app:1.0.0 --tag ghcr.io/x/app:latest ghcr.io/x/app@sha256:aaa ghcr.io/x/app@sha256:bbb",
		"buildx imagetools create --tag reg.io/x/app:1.0.0 reg.io/x/app@sha256:aaa reg.io/x/app@sha256:bbb",
	}
	for _, w := range want {
		if !strings.Contains(cmds, w) {
			t.Errorf("missing imagetools invocation %q in:\n%s", w, cmds)
		}
	}
	// A repo's tags must never reference another repo's manifest.
	if strings.Contains(cmds, "--tag reg.io/x/app:1.0.0 --tag ghcr.io") || strings.Contains(cmds, "--tag ghcr.io/x/app:1.0.0 --tag reg.io") {
		t.Errorf("tags leaked across repos:\n%s", cmds)
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
