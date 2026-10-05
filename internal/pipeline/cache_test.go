// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"slices"
	"strings"
	"testing"

	"github.com/blairham/stevedore/internal/config"
)

func TestAutoCacheEntries(t *testing.T) {
	cases := []struct {
		c        autoCache
		from, to string
	}{
		{autoCache{Type: "gha"}, "type=gha,scope=api", "type=gha,scope=api,mode=max"},
		{autoCache{Type: "registry", Ref: "ghcr.io/acme/cache", Mode: "min"}, "type=registry,ref=ghcr.io/acme/cache:api", "type=registry,ref=ghcr.io/acme/cache:api,mode=min"},
		{autoCache{Type: "local", Ref: "/tmp/cache/"}, "type=local,src=/tmp/cache/api", "type=local,dest=/tmp/cache/api,mode=max"},
	}
	for _, c := range cases {
		from, to := c.c.entries("api")
		if !slices.Equal(from, []string{c.from}) || !slices.Equal(to, []string{c.to}) {
			t.Errorf("%s: from %v to %v, want %s / %s", c.c.Type, from, to, c.from, c.to)
		}
	}
}

func TestCacheScope(t *testing.T) {
	for _, c := range []struct {
		id        string
		platforms []string
		want      string
	}{
		{"api", nil, "api"},
		{"api", []string{"linux/arm64"}, "api-linux-arm64"},
		{"api", []string{"linux/arm/v7", "linux/amd64"}, "api-linux-arm-v7_linux-amd64"},
		{"svc@eu", nil, "svc-eu"},
	} {
		if got := cacheScope(c.id, c.platforms); got != c.want {
			t.Errorf("cacheScope(%s, %v) = %q, want %q", c.id, c.platforms, got, c.want)
		}
	}
}

func TestResolvePlans_TopLevelCache(t *testing.T) {
	cfg := &config.Config{
		Cache: config.Cache{Type: "registry", Ref: `{{ env "CACHE_REGISTRY" }}/{{ .ID }}-cache`},
		Images: []config.Image{
			{ID: "api", Repositories: []string{"reg/api"}, Tags: []string{"{{ .Version }}"}},
			// Its own cache_to takes it out of the top-level cache entirely.
			{ID: "web", Repositories: []string{"reg/web"}, Tags: []string{"{{ .Version }}"}, CacheTo: []string{"type=inline"}},
		},
	}
	ctx := newCtx("main", false)
	ctx.Env = map[string]string{"CACHE_REGISTRY": "ghcr.io/acme"}
	plans, err := resolvePlans(cfg, ctx, false, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	api, web := plans[0], plans[1]
	if !slices.Equal(api.CacheFrom, []string{"type=registry,ref=ghcr.io/acme/api-cache:api"}) ||
		!slices.Equal(api.CacheTo, []string{"type=registry,ref=ghcr.io/acme/api-cache:api,mode=max"}) {
		t.Errorf("api cache = %v / %v", api.CacheFrom, api.CacheTo)
	}
	if web.Cache != nil || len(web.CacheFrom) != 0 || !slices.Equal(web.CacheTo, []string{"type=inline"}) {
		t.Errorf("web cache = %+v %v / %v, want only its own", web.Cache, web.CacheFrom, web.CacheTo)
	}

	cfg.Cache = config.Cache{Type: "none"}
	if plans, err = resolvePlans(cfg, ctx, false, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(plans[0].CacheFrom)+len(plans[0].CacheTo) != 0 {
		t.Errorf("cache.type none still cached: %v / %v", plans[0].CacheFrom, plans[0].CacheTo)
	}
}

// A split leg caches under its own platform's scope, so parallel legs of one
// image do not overwrite each other's cache.
func TestBuildSplitLeg_ScopesCacheByPlatform(t *testing.T) {
	_, log, o, p, grp := gateHarness(t)
	grp[0].plan.Image.Platforms = []string{"linux/amd64", "linux/arm64"}
	grp[0].plan.Cache = &autoCache{Type: "gha"}
	grp[0].plan.CacheFrom, grp[0].plan.CacheTo = grp[0].plan.Cache.entries("app")
	o.SplitPlatforms = []string{"linux/arm64"}

	if _, _, err := buildGroup(o, p, quietRunner(t), grp); err != nil {
		t.Fatal(err)
	}
	calls := readCalls(t, log)
	build := indexOf(calls, "buildx build")
	if build < 0 {
		t.Fatalf("no build in:\n%s", strings.Join(calls, "\n"))
	}
	if !strings.Contains(calls[build], "--cache-from type=gha,scope=app-linux-arm64 ") ||
		!strings.Contains(calls[build], "--cache-to type=gha,scope=app-linux-arm64,mode=max") {
		t.Errorf("leg cache not scoped to its platform: %s", calls[build])
	}
}
