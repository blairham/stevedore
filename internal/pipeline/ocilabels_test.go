// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"maps"
	"os"
	"strings"
	"testing"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/run"
)

func TestDefaultLabels(t *testing.T) {
	ctx := newCtx("main", false)
	ctx.SourceURL = "https://github.com/acme/app"
	ctx.CommitDate = "2024-05-06T07:08:09Z"
	img := config.Image{ID: "app", Repositories: []string{"reg/app"}, Tags: []string{"{{ .Version }}"}}

	plans, err := resolvePlans(&config.Config{Images: []config.Image{img}}, ctx, false, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"org.opencontainers.image.source":   "https://github.com/acme/app",
		"org.opencontainers.image.revision": "deadbeefcafe",
		"org.opencontainers.image.version":  "1.2.3",
		"org.opencontainers.image.created":  "2024-05-06T07:08:09Z",
	}
	if !maps.Equal(plans[0].Labels, want) {
		t.Errorf("labels = %v, want %v", plans[0].Labels, want)
	}

	// The image's own label wins, key by key.
	img.Labels = map[string]string{"org.opencontainers.image.source": "https://example.org/fork"}
	plans, err = resolvePlans(&config.Config{Images: []config.Image{img}}, ctx, false, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := plans[0].Labels["org.opencontainers.image.source"]; got != "https://example.org/fork" {
		t.Errorf("user source label overridden: %q", got)
	}
	if got := plans[0].Labels["org.opencontainers.image.revision"]; got != "deadbeefcafe" {
		t.Errorf("revision label = %q", got)
	}

	// Opted out: only the image's own.
	off := false
	plans, err = resolvePlans(&config.Config{DefaultLabels: &off, Images: []config.Image{img}}, ctx, false, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans[0].Labels) != 1 {
		t.Errorf("default_labels: false still set %v", plans[0].Labels)
	}

	// Nothing to say means no label, not an empty one.
	ctx.SourceURL, ctx.CommitDate = "", ""
	img.Labels = nil
	plans, err = resolvePlans(&config.Config{Images: []config.Image{img}}, ctx, false, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"org.opencontainers.image.source", "org.opencontainers.image.created"} {
		if _, ok := plans[0].Labels[k]; ok {
			t.Errorf("%s set with nothing to say: %v", k, plans[0].Labels)
		}
	}
}

func TestAnnotationsRendered(t *testing.T) {
	img := config.Image{
		ID: "app", Repositories: []string{"reg/app"}, Tags: []string{"{{ .Version }}"},
		Annotations: map[string]string{"org.opencontainers.image.version": "{{ .Version }}"},
	}
	plans, err := resolvePlans(&config.Config{Images: []config.Image{img}}, newCtx("main", false), false, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := toSpec(plans[0], ".", true, false, config.Provenance{}).Annotations["org.opencontainers.image.version"]; got != "1.2.3" {
		t.Errorf("annotation = %q", got)
	}
}

// The merged list carries the annotations at index level, on the dry-run that
// computes its digest as well as on the push, so the two digests agree.
func TestMergeGroupAnnotatesIndex(t *testing.T) {
	dir := t.TempDir()
	for _, leg := range []struct{ platform, digest string }{{"linux/amd64", "sha256:aaa"}, {"linux/arm64", "sha256:bbb"}} {
		if err := writeSplitDigest(dir, "dist", []string{"app"}, []string{leg.platform}, leg.digest); err != nil {
			t.Fatal(err)
		}
	}
	rep := ImagePlan{
		Image:       config.Image{ID: "app", Platforms: []string{"linux/amd64", "linux/arm64"}},
		Repos:       []string{"reg/app"},
		Annotations: map[string]string{"a.b": "c"},
	}
	stderr, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	r := &run.Runner{DryRun: true, Stderr: stderr}
	if _, err = mergeGroup(r, Options{Dir: dir, DryRun: true}, rep, "dist", rep.Repos); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(stderr.Name())
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{
		"imagetools create --dry-run --annotation index:a.b=c reg/app@sha256:aaa",
		"imagetools create --tag reg/app@sha256:<digest-resolved-at-build-time> --annotation index:a.b=c reg/app@sha256:aaa",
	} {
		if !strings.Contains(string(out), w) {
			t.Errorf("missing %q in:\n%s", w, out)
		}
	}
}
