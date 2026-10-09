// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package publish

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/run"
)

// glab release create takes the tag, then files to attach, then flags; --ref
// creates the tag at the built commit when it does not exist yet.
func TestGitLabReleaseArgv(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "argv")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> \"" + log + "\"; done\n"
	if err := os.WriteFile(
		filepath.Join(dir, "glab"),
		[]byte(script),
		0o755,
	); err != nil { //nolint:gosec // G306: a test fake must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	sink, err := os.CreateTemp(dir, "out")
	if err != nil {
		t.Fatal(err)
	}
	r := &run.Runner{Stdout: sink, Stderr: sink}
	if err = GitLabRelease(
		r,
		config.GitLabRelease{Enabled: true},
		"v1.2.0",
		"abc123",
		"demo 1.2.0",
		"dist/CHANGELOG.md",
		[]string{"dist/sbom.json"},
	); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{
		"release",
		"create",
		"v1.2.0",
		"dist/sbom.json",
		"--name",
		"demo 1.2.0",
		"--notes-file",
		"dist/CHANGELOG.md",
		"--ref",
		"abc123",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("glab argv = %q, want %q", got, want)
	}
}

func TestGitLabReleaseGuards(t *testing.T) {
	if err := GitLabRelease(&run.Runner{}, config.GitLabRelease{}, "", "", "", "", nil); err != nil {
		t.Errorf("disabled: %v", err)
	}
	if err := GitLabRelease(
		&run.Runner{DryRun: true},
		config.GitLabRelease{Enabled: true},
		"",
		"",
		"t",
		"n",
		nil,
	); err == nil {
		t.Error("no tag: want an error")
	}
	t.Setenv("PATH", t.TempDir())
	err := GitLabRelease(&run.Runner{}, config.GitLabRelease{Enabled: true}, "v1", "", "t", "n", nil)
	if err == nil || !strings.Contains(err.Error(), "glab not found") {
		t.Errorf("missing glab: err = %v", err)
	}
}
