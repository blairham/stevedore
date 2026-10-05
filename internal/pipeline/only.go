// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/summary"
)

// OnlyAll is the --only value that selects every image, so a workflow need
// not enumerate the config's image IDs to build them all.
const OnlyAll = "all"

// expandOnly replaces --only all with every image ID, in config order. An
// image whose ID really is "all" keeps the literal meaning: that is what the
// flag selected before "all" meant anything else.
func expandOnly(o Options) (Options, error) {
	if !slices.Contains(o.Only, OnlyAll) {
		return o, nil
	}
	cfg, err := config.Load(o.ConfigPath)
	if err != nil {
		return o, err
	}
	ids := make([]string, 0, len(cfg.Images))
	for _, img := range cfg.Images {
		if img.ID == OnlyAll {
			return o, nil
		}
		ids = append(ids, img.ID)
	}
	o.Only = ids
	return o, nil
}

// ReasonsFromPlan reads a `stevedore plan` document and returns each planned
// image's reason for building, keyed by image ID. A matrix job or merge run
// selects its images with --only, which on its own can report nothing better
// than "selected via --only"; given the plan, the summary keeps the reason the
// plan actually decided on.
func ReasonsFromPlan(data []byte) (map[string]string, error) {
	var plan PlanResult
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, fmt.Errorf("parse plan: %w", err)
	}
	reasons := map[string]string{}
	for _, e := range plan.Include {
		for _, id := range e.IDs {
			if e.Reason != "" {
				reasons[id] = e.Reason
			}
		}
	}
	return reasons, nil
}

// FlatOnly is every planned image ID, comma-joined, in plan order: the --only
// of a final `publish` (or `merge`) step that covers the whole matrix.
func (r *PlanResult) FlatOnly() string {
	var ids []string
	for _, e := range r.Include {
		for _, id := range e.IDs {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	return strings.Join(ids, ",")
}

// FlatPins is every planned image's --pin-version flag, space-joined, to go
// with FlatOnly.
func (r *PlanResult) FlatPins() string {
	var pins, seen []string
	for _, e := range r.Include {
		for _, id := range e.IDs {
			if slices.Contains(seen, id) {
				continue
			}
			seen = append(seen, id)
			pins = append(pins, fmt.Sprintf("--pin-version %s=%s", id, e.Versions[id]))
		}
	}
	return strings.Join(pins, " ")
}

// WriteGitHubOutput appends the flat `only` and `pins` as step outputs to
// summary.OutputsPath() ($STEVEDORE_OUTPUTS_FILE or $GITHUB_OUTPUT), if set. They are outputs rather than keys of
// the plan document: a workflow that builds its matrix from the whole
// document (`matrix: ${{ fromJson(plan) }}`) turns every top-level key into a
// matrix dimension. The documented form is `matrix: {include: ${{
// fromJson(plan).include }}}`. No-op outside GitHub Actions.
func (r *PlanResult) WriteGitHubOutput() error {
	path := summary.OutputsPath()
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(filepath.Clean(path), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // G703: a file the Actions runner names
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "only=%s\npins=%s\n", r.FlatOnly(), r.FlatPins())
	return err
}
