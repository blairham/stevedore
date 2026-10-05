// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package preflight

import (
	"testing"

	"github.com/blairham/stevedore/internal/config"
)

// glab is required only when release.gitlab is enabled and the run publishes.
func TestRequirementsGitLabGating(t *testing.T) {
	if _, ok := labels(Requirements(&config.Config{}, Opts{GitLabRelease: true}))["glab"]; ok {
		t.Error("glab listed with release.gitlab disabled")
	}
	cfg := &config.Config{}
	cfg.Release.GitLab.Enabled = true
	if !labels(Requirements(cfg, Opts{GitLabRelease: true}))["glab"].Required {
		t.Error("glab should be required when release.gitlab is enabled and the run publishes")
	}
	if labels(Requirements(cfg, Opts{}))["glab"].Required {
		t.Error("glab should not be required when the run does not publish")
	}
}
