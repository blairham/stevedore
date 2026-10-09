// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"fmt"

	"github.com/blairham/stevedore/internal/gitinfo"
	"github.com/blairham/stevedore/internal/progress"
)

// applyRequireTag turns versioning.require_tag into run options, so a
// tag-driven repository needs one release job rather than two. A version tag
// on HEAD is the release: every image builds, since a tag on a commit whose
// sources did not change since the last release would otherwise be skipped by
// change detection and release nothing. Without a tag the same job is a
// validate-only build — a snapshot that pushes nothing.
//
// A snapshot or --no-push run asked for that already and is left alone. A
// split leg or a merge has no validate-only form, so an untagged one is
// refused rather than quietly pushing nothing.
func applyRequireTag(o Options) (Options, error) {
	if o.Snapshot || o.NoPush {
		return o, nil
	}
	cfg, err := loadConfig(o)
	if err != nil {
		return o, err
	}
	if !cfg.Versioning.RequireTag {
		return o, nil
	}
	gi, err := gitinfo.Gather(o.context(), o.Dir)
	if err != nil {
		return o, err
	}
	if gi.Tag != "" {
		o.BuildAll = true
		return o, nil
	}
	if len(o.SplitPlatforms) > 0 || o.FromDigests {
		return o, fmt.Errorf(
			"versioning.require_tag: HEAD has no version tag, and a split leg or merge only runs for a tagged release; tag HEAD, or validate with a plain `release`",
		)
	}
	progress.Println(
		progressOut,
		"==> versioning.require_tag: HEAD has no version tag; validate-only build (snapshot, nothing pushed)",
	)
	o.Snapshot = true
	o.NoPush = true
	o.Push = false
	o.SoftVersion = true
	return o, nil
}
