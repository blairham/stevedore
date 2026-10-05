// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package summary

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The STEVEDORE_* sinks win over the GitHub Actions ones, which still work
// alone; with neither set nothing is written.
func TestSinkPaths(t *testing.T) {
	dir := t.TempDir()
	gh, generic := filepath.Join(dir, "gh"), filepath.Join(dir, "generic")
	t.Setenv("GITHUB_OUTPUT", gh)
	t.Setenv("GITHUB_STEP_SUMMARY", gh+"-summary")
	t.Setenv(OutputsFileEnv, "")
	t.Setenv(SummaryFileEnv, "")
	if OutputsPath() != gh || MarkdownPath() != gh+"-summary" {
		t.Errorf("GitHub fallback: %q, %q", OutputsPath(), MarkdownPath())
	}
	t.Setenv(OutputsFileEnv, generic)
	t.Setenv(SummaryFileEnv, generic+"-summary")
	if OutputsPath() != generic || MarkdownPath() != generic+"-summary" {
		t.Errorf("STEVEDORE_* should win: %q, %q", OutputsPath(), MarkdownPath())
	}

	r := Result{Project: "demo", Images: []Image{{ID: "api", Digest: "sha256:abc", Repositories: []string{"r/api"}, Pushed: true}}}
	if err := r.WriteGitHubOutput(); err != nil {
		t.Fatal(err)
	}
	if err := r.WriteGitHubStepSummary(); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(generic)
	if err != nil || !strings.Contains(string(out), "summary=") {
		t.Errorf("outputs file = %q, %v", out, err)
	}
	md, err := os.ReadFile(generic + "-summary")
	if err != nil || !strings.Contains(string(md), "stevedore release") {
		t.Errorf("summary file = %q, %v", md, err)
	}
	if _, err := os.Stat(gh); err == nil {
		t.Error("the GitHub sink was written although STEVEDORE_OUTPUTS_FILE was set")
	}
}
