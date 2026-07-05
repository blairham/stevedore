package pipeline

import (
	"testing"
	"time"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/gitinfo"
	"github.com/blairham/stevedore/internal/tmpl"
)

func TestIsFloating(t *testing.T) {
	cases := map[string]bool{
		"latest":      true,
		"LATEST":      true,
		"edge-latest": true,
		"0.1.0":       false,
		"abc1234":     false,
		"":            false,
	}
	for tag, want := range cases {
		if got := isFloating(tag); got != want {
			t.Errorf("isFloating(%q) = %v, want %v", tag, got, want)
		}
	}
}

func newCtx(branch, defaultBranch string, snapshot bool) *tmpl.Context {
	gi := &gitinfo.Info{
		Version:     "1.2.3",
		Tag:         "v1.2.3",
		Commit:      "deadbeefcafe",
		ShortCommit: "deadbee",
		Branch:      branch,
	}
	return tmpl.NewContext("demo", defaultBranch, gi, snapshot, time.Unix(0, 0).UTC(), nil)
}

func demoCfg() *config.Config {
	return &config.Config{
		Images: []config.Image{{
			ID:           "app",
			Repositories: []string{"ghcr.io/x/app", "reg.io/x/app"},
			Tags:         []string{"{{ .Version }}", "{{ .ShortCommit }}", "latest"},
		}},
	}
}

func TestResolvePlans_DefaultBranchKeepsLatest(t *testing.T) {
	ctx := newCtx("main", "main", false)
	plans, err := resolvePlans(demoCfg(), ctx, false, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 2 repos x 3 tags = 6 refs.
	if got := len(plans[0].Refs); got != 6 {
		t.Fatalf("want 6 refs, got %d: %v", got, plans[0].Refs)
	}
	if !contains(plans[0].Refs, "ghcr.io/x/app:latest") {
		t.Errorf("expected latest tag on default branch, got %v", plans[0].Refs)
	}
	if !contains(plans[0].Refs, "reg.io/x/app:1.2.3") {
		t.Errorf("expected version tag on second repo, got %v", plans[0].Refs)
	}
}

func TestResolvePlans_FeatureBranchDropsLatest(t *testing.T) {
	ctx := newCtx("feature/x", "main", false)
	plans, err := resolvePlans(demoCfg(), ctx, false, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// latest dropped off default branch: 2 repos x 2 tags = 4.
	if got := len(plans[0].Refs); got != 4 {
		t.Fatalf("want 4 refs, got %d: %v", got, plans[0].Refs)
	}
	if contains(plans[0].Refs, "ghcr.io/x/app:latest") {
		t.Errorf("latest must not publish off the default branch: %v", plans[0].Refs)
	}
}

func TestResolvePlans_PerImageVersion(t *testing.T) {
	ctx := newCtx("main", "main", false)
	cfg := &config.Config{
		Versioning: config.Versioning{Strategy: "registry"},
		Images: []config.Image{
			{ID: "checkout", Repositories: []string{"reg/checkout"}, Tags: []string{"{{ .Version }}"}},
			{ID: "billing", Repositories: []string{"reg/billing"}, Tags: []string{"{{ .Version }}"}},
		},
	}
	// Each repo resolves to its own version.
	versionFor := func(repo string) (string, error) {
		return map[string]string{"reg/checkout": "0.0.336", "reg/billing": "0.0.285"}[repo], nil
	}
	plans, err := resolvePlans(cfg, ctx, false, versionFor, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plans[0].Version != "0.0.336" || plans[0].Refs[0] != "reg/checkout:0.0.336" {
		t.Errorf("checkout plan = %+v", plans[0])
	}
	if plans[1].Version != "0.0.285" || plans[1].Refs[0] != "reg/billing:0.0.285" {
		t.Errorf("billing plan = %+v", plans[1])
	}
}

func TestResolvePlans_SnapshotDropsLatest(t *testing.T) {
	ctx := newCtx("main", "main", true)
	plans, err := resolvePlans(demoCfg(), ctx, true, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if contains(plans[0].Refs, "ghcr.io/x/app:latest") {
		t.Errorf("latest must not publish in a snapshot: %v", plans[0].Refs)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func TestResolvePlans_PinnedVersionWins(t *testing.T) {
	ctx := newCtx("main", "main", false)
	cfg := &config.Config{
		Versioning: config.Versioning{Strategy: "registry"},
		Images: []config.Image{
			{ID: "checkout", Repositories: []string{"reg/checkout"}, Tags: []string{"{{ .Version }}"}},
			{ID: "billing", Repositories: []string{"reg/billing"}, Tags: []string{"{{ .Version }}"}},
		},
	}
	// The resolver must not be consulted for a pinned image.
	versionFor := func(repo string) (string, error) {
		if repo == "reg/checkout" {
			t.Errorf("versionFor called for pinned image repo %s", repo)
		}
		return "0.0.285", nil
	}
	plans, err := resolvePlans(cfg, ctx, false, versionFor, map[string]string{"checkout": "0.0.400"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plans[0].Version != "0.0.400" || plans[0].Refs[0] != "reg/checkout:0.0.400" {
		t.Errorf("pinned checkout plan = %+v, want version 0.0.400", plans[0])
	}
	if plans[1].Version != "0.0.285" {
		t.Errorf("unpinned billing plan = %+v, want resolved 0.0.285", plans[1])
	}
}

func TestResolveVersion_OnlyPinnedAnchorSkipsRegistry(t *testing.T) {
	cfg := &config.Config{
		Versioning: config.Versioning{Strategy: "ecr"},
		Images: []config.Image{
			{ID: "a", Repositories: []string{"reg/a"}},
			{ID: "b", Repositories: []string{"reg/b"}},
		},
	}
	gi := &gitinfo.Info{Commit: "deadbeefcafe", ShortCommit: "deadbee", Branch: "main"}
	// The release version anchors on the first --only image; with that image
	// pinned there is nothing left to resolve, so no registry command runs
	// (a real read would fail here — no credentials in the test env).
	ver, err := resolveVersion(cfg, gi, Options{
		Only:        []string{"b"},
		PinVersions: map[string]string{"b": "2.0.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ver != "2.0.0" {
		t.Errorf("version = %q, want the pinned 2.0.0", ver)
	}
}
