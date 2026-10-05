// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package changelog

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/gitinfo"
)

// repoPastTag builds a repository with v1.4.0 two commits behind HEAD and
// returns its directory. The user's git config is kept out so a gpgSign
// setting cannot break the fixture.
func repoPastTag(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t.co", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t.co")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	commit := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", "-A")
		git("commit", "-qm", name)
	}
	git("init", "-q", "-b", "main")
	commit("feat: old")
	git("tag", "v1.4.0")
	commit("feat: new one")
	commit("fix: new two")
	return dir
}

// A release from an untagged HEAD (here a non-git strategy's computed 2.0.0)
// must not be headed by the last tag git can reach, which names a release
// that already happened.
func TestGenerate_UntaggedHEADUsesVersion(t *testing.T) {
	dir := repoPastTag(t)
	gi, err := gitinfo.Gather(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	gi.Version = "2.0.0"
	out, err := Generate(t.Context(), config.Changelog{Enabled: true}, gi, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "## 2.0.0\n") {
		t.Errorf("changelog heading: got %q", strings.SplitN(out, "\n", 2)[0])
	}
	if !strings.Contains(out, "_Changes since v1.4.0_") || strings.Contains(out, "old") {
		t.Errorf("changelog should cover exactly the commits since v1.4.0:\n%s", out)
	}
	if !strings.Contains(out, "new one") || !strings.Contains(out, "new two") {
		t.Errorf("changelog is missing commits since v1.4.0:\n%s", out)
	}
}
