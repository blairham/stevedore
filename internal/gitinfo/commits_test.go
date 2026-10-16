// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package gitinfo

import "testing"

// CommitsSince must carry each commit's body, where a BREAKING CHANGE footer
// lives (#61); it used to read the subject alone.
func TestCommitsSinceBody(t *testing.T) {
	isolateGit(t)
	r := newRepo(t, t.TempDir())
	r.commit("first")
	r.git("commit", "-q", "--allow-empty", "-m", "feat: second", "-m", "BREAKING CHANGE: gone")
	commits, err := CommitsSince(t.Context(), r.dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 {
		t.Fatalf("got %d commits: %+v", len(commits), commits)
	}
	if c := commits[0]; c.Subject != "feat: second" || c.Body != "BREAKING CHANGE: gone" {
		t.Errorf("newest = %+v, want subject and body", c)
	}
	if c := commits[1]; c.Subject != "first" || c.Body != "" {
		t.Errorf("oldest = %+v, want empty body", c)
	}
}

// VersionTag finds a release's tag with or without the "v", and nothing for a
// version that was never tagged or a tag that only shares a prefix.
func TestVersionTag(t *testing.T) {
	isolateGit(t)
	r := newRepo(t, t.TempDir())
	r.commit("first")
	r.git("tag", "v1.2.0")
	r.git("tag", "1.3.0")
	r.git("tag", "v1.4.0-rc.1")
	for version, want := range map[string]string{
		"1.2.0":        "v1.2.0",
		"1.3.0":        "1.3.0",
		"1.4.0-rc.1":   "v1.4.0-rc.1",
		"1.4.0":        "",
		"1.2":          "",
		"9.9.9":        "",
		"1.2.0^{tree}": "",
	} {
		if got := VersionTag(t.Context(), r.dir, version); got != want {
			t.Errorf("VersionTag(%q) = %q, want %q", version, got, want)
		}
	}
}
