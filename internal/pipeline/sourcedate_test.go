// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"testing"

	"github.com/blairham/stevedore/internal/config"
)

func TestSourceDateEpochPlan(t *testing.T) {
	ctx := newCtx("main", false)
	ctx.CommitTimestamp = 1714979289
	off := false
	cases := []struct {
		name string
		env  string
		args []string
		cfg  *bool
		want string
	}{
		{"commit time by default", "", nil, nil, "1714979289"},
		{"environment wins", "42", nil, nil, "42"},
		{"build arg wins", "42", []string{"SOURCE_DATE_EPOCH=7"}, nil, "7"},
		{"opted out", "42", nil, &off, ""},
	}
	for _, c := range cases {
		t.Setenv("SOURCE_DATE_EPOCH", c.env)
		cfg := &config.Config{SourceDateEpoch: c.cfg, Images: []config.Image{{
			ID: "app", Repositories: []string{"reg/app"}, Tags: []string{"{{ .Version }}"}, BuildArgs: c.args,
		}}}
		plans, err := resolvePlans(cfg, ctx, false, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := plans[0].SourceDateEpoch; got != c.want {
			t.Errorf("%s: SourceDateEpoch = %q, want %q", c.name, got, c.want)
		}
		if got := toSpec(plans[0], ".", true, false, config.Provenance{}).SourceDateEpoch; got != c.want {
			t.Errorf("%s: spec SourceDateEpoch = %q, want %q", c.name, got, c.want)
		}
	}
}

// {{ .ID }} names the image in its repositories, build args and tags — what
// makes one image_defaults block serve many images.
func TestResolvePlans_ImageID(t *testing.T) {
	img := func(id string) config.Image {
		return config.Image{
			ID: id, Repositories: []string{"reg/{{ .ID }}"},
			Tags: []string{"{{ .ID }}-{{ .Version }}"}, BuildArgs: []string{"SERVICE={{ .ID }}"},
		}
	}
	plans, err := resolvePlans(&config.Config{Images: []config.Image{img("api"), img("worker")}}, newCtx("main", false), false, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"api", "worker"} {
		p := plans[i]
		if p.Repos[0] != "reg/"+id || p.Refs[0] != "reg/"+id+":"+id+"-1.2.3" || p.BuildArgs[0] != "SERVICE="+id {
			t.Errorf("%s: repos %v refs %v args %v", id, p.Repos, p.Refs, p.BuildArgs)
		}
	}
}
