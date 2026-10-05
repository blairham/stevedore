// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"fmt"
	"strings"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/tmpl"
)

// autoCache is the top-level cache (config.Cache) with its ref rendered for
// one image.
type autoCache struct {
	Type, Ref, Mode string
}

// applyCache gives plan the top-level cache, unless there is none or the
// image brings its own cache_from/cache_to — those override it whole, rather
// than mixing one direction of each.
func applyCache(c config.Cache, plan *ImagePlan, ctx *tmpl.Context) error {
	img := plan.Image
	if !c.Enabled() || len(img.CacheFrom) > 0 || len(img.CacheTo) > 0 {
		return nil
	}
	ref, err := tmpl.Render(c.Ref, ctx.WithImage(img.ID))
	if err != nil {
		return fmt.Errorf("image %s cache.ref: %w", img.ID, err)
	}
	plan.Cache = &autoCache{Type: c.Type, Ref: ref, Mode: c.Mode}
	plan.CacheFrom, plan.CacheTo = plan.Cache.entries(cacheScope(img.ID, nil))
	return nil
}

// entries renders the buildx --cache-from and --cache-to values for scope.
func (c *autoCache) entries(scope string) (from, to []string) {
	mode := c.Mode
	if mode == "" {
		mode = "max"
	}
	var src, dst string
	switch c.Type {
	case config.CacheGHA:
		src = "type=gha,scope=" + scope
		dst = src
	case config.CacheRegistry:
		src = "type=registry,ref=" + c.Ref + ":" + scope
		dst = src
	case config.CacheLocal:
		dir := strings.TrimSuffix(c.Ref, "/") + "/" + scope
		src, dst = "type=local,src="+dir, "type=local,dest="+dir
	default:
		return nil, nil
	}
	return []string{src}, []string{dst + ",mode=" + mode}
}

// cacheScope names an image's cache: its id, and on a split leg the leg's
// platforms too ("api-linux-arm64"). The result is safe as a registry tag, a
// path element and a gha scope: anything outside [A-Za-z0-9_.-] becomes "-",
// several platforms join with "_".
func cacheScope(id string, platforms []string) string {
	parts := []string{id}
	if len(platforms) > 0 {
		slugs := make([]string, len(platforms))
		for i, p := range platforms {
			slugs[i] = strings.ReplaceAll(p, "/", "-")
		}
		parts = append(parts, strings.Join(slugs, "_"))
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.', r == '-':
			return r
		}
		return '-'
	}, strings.Join(parts, "-"))
}
