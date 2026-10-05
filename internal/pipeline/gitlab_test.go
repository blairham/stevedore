// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/gitinfo"
	"github.com/blairham/stevedore/internal/tmpl"
)

// release.gitlab cuts a GitLab release from the changelog at the built
// commit, named like the GitHub one.
func TestPublishReleaseGitLab(t *testing.T) {
	log := filepath.Join(t.TempDir(), "glab.log")
	fakeTool(t, "glab", `echo "glab $*" >> "`+log+`"`+"\n")
	p := &Prepared{
		Config: &config.Config{ProjectName: "demo", Release: config.Release{GitLab: config.GitLabRelease{Enabled: true}}},
		Git:    &gitinfo.Info{Commit: "abc123"},
		Ctx:    &tmpl.Context{Version: "1.2.0"},
	}
	if err := publishRelease(quietRunner(t), p, "dist/CHANGELOG.md", []string{"r/app:1.2.0"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	want := "glab release create v1.2.0 --name demo 1.2.0 --notes-file dist/CHANGELOG.md --ref abc123"
	if got := strings.TrimSpace(string(data)); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if err := publishRelease(quietRunner(t), p, "", nil); err == nil {
		t.Error("a GitLab release without changelog notes should fail")
	}
}
