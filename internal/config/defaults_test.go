// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"maps"
	"slices"
	"strings"
	"testing"
)

func TestImageDefaultsMerge(t *testing.T) {
	p := writeTemp(t, ".stevedore.yaml", `
image_defaults:
  platforms: [linux/amd64, linux/arm64]
  repositories: ["ghcr.io/acme/{{ .ID }}"]
  tags: ["{{ .Version }}", latest]
  build_args: ["SERVICE={{ .ID }}"]
  labels:
    team: platform
    tier: backend
  cache_from: ["type=gha,scope={{ .ID }}"]
images:
  - id: api
  - id: worker
    tags: []                    # present: replaces, even when empty
    build_args: ["SERVICE=jobs"] # lists replace, never concatenate
    labels:
      tier: jobs                # maps merge, the image's key winning
  - id: web
    platforms: [linux/amd64]
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("merged config should validate: %v", err)
	}
	api, worker, web := c.Images[0], c.Images[1], c.Images[2]
	if !slices.Equal(api.Repositories, []string{"ghcr.io/acme/{{ .ID }}"}) ||
		!slices.Equal(api.Platforms, []string{"linux/amd64", "linux/arm64"}) {
		t.Errorf("api did not inherit: %+v", api)
	}
	if !slices.Equal(api.Tags, []string{"{{ .Version }}", "latest"}) ||
		!slices.Equal(api.CacheFrom, []string{"type=gha,scope={{ .ID }}"}) {
		t.Errorf("api tags/cache = %v / %v", api.Tags, api.CacheFrom)
	}
	if !maps.Equal(api.Labels, map[string]string{"team": "platform", "tier": "backend"}) {
		t.Errorf("api labels = %v", api.Labels)
	}
	if !slices.Equal(worker.BuildArgs, []string{"SERVICE=jobs"}) {
		t.Errorf("worker build_args = %v, want the image's list alone", worker.BuildArgs)
	}
	if !maps.Equal(worker.Labels, map[string]string{"team": "platform", "tier": "jobs"}) {
		t.Errorf("worker labels = %v", worker.Labels)
	}
	// tags: [] was the image's choice; applyDefaults then fills the usual
	// default rather than the image_defaults list.
	if !slices.Equal(worker.Tags, []string{"{{ .Version }}"}) {
		t.Errorf("worker tags = %v", worker.Tags)
	}
	if !slices.Equal(web.Platforms, []string{"linux/amd64"}) {
		t.Errorf("web platforms = %v", web.Platforms)
	}
}

// Defaults are checked as strictly as images, against the file as written.
func TestImageDefaultsStrict(t *testing.T) {
	p := writeTemp(
		t,
		".stevedore.yaml",
		"image_defaults:\n  base_platforms: [linux/amd64]\nimages:\n  - id: a\n    repositories: [r/a]\n",
	)
	if _, err := Load(
		p,
	); err == nil || !strings.Contains(err.Error(), "base_platforms") ||
		!strings.Contains(err.Error(), "line 2") {
		t.Errorf("unknown field in image_defaults: err = %v", err)
	}
	p = writeTemp(t, ".stevedore.yaml", "image_defaults:\n  id: shared\n  repositories: [r/x]\nimages:\n  - id: a\n")
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "image_defaults.id") {
		t.Errorf("id in image_defaults: Validate = %v", err)
	}
}

// A required field supplied only by the defaults satisfies validation, and
// one missing from both still fails it.
func TestImageDefaultsValidatedAfterMerge(t *testing.T) {
	p := writeTemp(t, ".stevedore.yaml", "image_defaults:\n  repositories: [r/x]\nimages:\n  - id: a\n")
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Validate(); err != nil {
		t.Errorf("repositories from image_defaults: %v", err)
	}
	p = writeTemp(t, ".stevedore.yaml", "image_defaults:\n  platforms: [linux/amd64]\nimages:\n  - id: a\n")
	if c, err = Load(p); err != nil {
		t.Fatal(err)
	}
	if err = c.Validate(); err == nil || !strings.Contains(err.Error(), "repository") {
		t.Errorf("no repositories anywhere: Validate = %v", err)
	}
}

// A key an image takes through a YAML merge key is the image's own.
func TestImageDefaultsYAMLMergeKey(t *testing.T) {
	p := writeTemp(t, ".stevedore.yaml", `
image_defaults:
  repositories: [r/x]
  tags: [from-defaults]
images:
  - &base
    id: a
    tags: [from-anchor]
  - <<: *base
    id: b
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Images[1].Tags; !slices.Equal(got, []string{"from-anchor"}) {
		t.Errorf("b tags = %v, want the merged anchor's", got)
	}
	if got := c.Images[1].Repositories; !slices.Equal(got, []string{"r/x"}) {
		t.Errorf("b repositories = %v, want the default", got)
	}
}
