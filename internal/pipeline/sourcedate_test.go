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
