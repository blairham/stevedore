// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"slices"
	"testing"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/tmpl"
)

func TestFloatingTag(t *testing.T) {
	ctx := newCtx("main", false) // version 1.2.3
	cases := []struct {
		src  string
		want bool
	}{
		{"latest", true},
		{"edge-latest", true},
		{"{{ .Major }}", true},
		{"v{{ .Major }}.{{ .Minor }}", true},
		{"{{ .Major }}-alpine", true},
		{"{{ .Major }}.{{ .Minor }}.{{ .Patch }}", false},
		{"{{ .Major }}-{{ .ShortCommit }}", false},
		{"{{ .Version }}", false},
		{"{{ .ShortCommit }}", false},
		// Spelled by hand, but still the version's major.minor.
		{"1.2", true},
		{"v1", true},
		{"{{ trimSuffix .Version \".3\" }}", true},
		{"2", false},
		{"1.2.3", false},
	}
	for _, c := range cases {
		tag, err := renderTag(c.src, ctx)
		if err != nil {
			t.Fatal(err)
		}
		got, err := floatingTag(c.src, tag, ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("floatingTag(%q → %q) = %v, want %v", c.src, tag, got, c.want)
		}
	}
}

func semverCfg(allow bool) *config.Config {
	return &config.Config{
		PrereleaseFloatingTags: allow,
		Images: []config.Image{
			{
				ID:           "app",
				Repositories: []string{"reg/app"},
				Tags: []string{
					"{{ .Version }}",
					"{{ .ShortCommit }}",
					"{{ .Major }}",
					"{{ .Major }}.{{ .Minor }}",
					"latest",
				},
			},
		},
	}
}

func TestResolvePlans_MajorMinorTags(t *testing.T) {
	plans, err := resolvePlans(semverCfg(false), newCtx("main", false), false, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"reg/app:1.2.3", "reg/app:deadbee", "reg/app:1", "reg/app:1.2", "reg/app:latest"}
	if !slices.Equal(plans[0].Refs, want) {
		t.Errorf("refs = %v, want %v", plans[0].Refs, want)
	}
	for _, ref := range []string{"reg/app:1", "reg/app:1.2", "reg/app:latest"} {
		if !plans[0].Floating[ref] {
			t.Errorf("%s should be floating: %v", ref, plans[0].Floating)
		}
	}
	// Applied after the immutable tags, commit tag first.
	got := orderRefsForTagging(plans[0].Refs, plans[0].Floating, "deadbeefcafe", "deadbee")
	if want := []string{
		"reg/app:deadbee",
		"reg/app:1.2.3",
		"reg/app:1",
		"reg/app:1.2",
		"reg/app:latest",
	}; !slices.Equal(
		got,
		want,
	) {
		t.Errorf("tag order = %v, want %v", got, want)
	}
}

func TestResolvePlans_FeatureBranchDropsMajorMinor(t *testing.T) {
	plans, err := resolvePlans(semverCfg(false), newCtx("feature/x", false), false, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"reg/app:1.2.3", "reg/app:deadbee"}; !slices.Equal(plans[0].Refs, want) {
		t.Errorf("refs = %v, want %v", plans[0].Refs, want)
	}
}

// A release candidate cut on main must not move latest, 1 or 1.3.
func TestResolvePlans_PrereleaseWithholdsFloating(t *testing.T) {
	ctx := newCtx("main", false).WithVersion("1.3.0-rc.1")
	plans, err := resolvePlans(semverCfg(false), ctx, false, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"reg/app:1.3.0-rc.1", "reg/app:deadbee"}; !slices.Equal(plans[0].Refs, want) {
		t.Errorf("refs = %v, want %v", plans[0].Refs, want)
	}

	plans, err = resolvePlans(semverCfg(true), ctx, false, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{
		"reg/app:1.3.0-rc.1",
		"reg/app:deadbee",
		"reg/app:1",
		"reg/app:1.3",
		"reg/app:latest",
	}; !slices.Equal(
		plans[0].Refs,
		want,
	) {
		t.Errorf("prerelease_floating_tags: refs = %v, want %v", plans[0].Refs, want)
	}
}

// Under per-image versioning, the image's own version decides.
func TestResolvePlans_PrereleaseIsPerImage(t *testing.T) {
	cfg := &config.Config{Images: []config.Image{
		{ID: "a", Repositories: []string{"reg/a"}, Tags: []string{"{{ .Version }}", "latest"}},
		{ID: "b", Repositories: []string{"reg/b"}, Tags: []string{"{{ .Version }}", "latest"}},
	}}
	versionFor := func(repo string) (string, error) {
		return map[string]string{"reg/a": "2.0.0-beta.1", "reg/b": "1.4.0"}[repo], nil
	}
	plans, err := resolvePlans(cfg, newCtx("main", false), false, versionFor, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if contains(plans[0].Refs, "reg/a:latest") || !contains(plans[1].Refs, "reg/b:latest") {
		t.Errorf("a = %v, b = %v", plans[0].Refs, plans[1].Refs)
	}
}

// A floating tag is never taken as proof a commit was released.
func TestMemberReleasedSkipsFloating(t *testing.T) {
	plan := ImagePlan{Refs: []string{"reg/app:1-deadbee"}, Floating: map[string]bool{"reg/app:1-deadbee": true}}
	_, ok, err := memberReleased(nil, plan, "deadbeefcafe", "deadbee")
	if err != nil || ok {
		t.Errorf("memberReleased = %v, %v; want no lookup at all", ok, err)
	}
}

func renderTag(src string, ctx *tmpl.Context) (string, error) { return tmpl.Render(src, ctx) }

// An untagged commit on main carries a snapshot-form version; it is not a
// prerelease, so a release from it still moves latest and the major tags.
func TestResolvePlans_UntaggedMainKeepsFloating(t *testing.T) {
	ctx := newCtx("main", false).WithVersion("1.2.3-SNAPSHOT-deadbee")
	plans, err := resolvePlans(semverCfg(false), ctx, false, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"reg/app:1", "reg/app:1.2", "reg/app:latest"} {
		if !contains(plans[0].Refs, ref) {
			t.Errorf("%s missing: %v", ref, plans[0].Refs)
		}
	}
}

// check's placeholder for a version it could not read is not a prerelease.
func TestResolvePlans_UnresolvedPlaceholderKeepsFloating(t *testing.T) {
	ctx := newCtx("main", false).WithVersion(unresolvedVersion)
	plans, err := resolvePlans(semverCfg(false), ctx, false, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(plans[0].Refs, "reg/app:latest") {
		t.Errorf("latest hidden behind the placeholder: %v", plans[0].Refs)
	}
}
