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
