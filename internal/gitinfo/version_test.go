// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package gitinfo

import (
	"os"
	"path/filepath"
	"testing"
)

// gathered builds a real repository with the history laid down by setup and
// returns what Gather reports for it. Which tag `git describe` returns for a
// given history is the whole question here, so it is not faked.
func gathered(t *testing.T, setup func(r *gitRepo)) *Info {
	t.Helper()
	r := newRepo(t, t.TempDir(), "-b", "main")
	setup(r)
	info, err := Gather(t.Context(), r.dir)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func TestGather_TaggedHEAD(t *testing.T) {
	info := gathered(t, func(r *gitRepo) {
		r.commit("a")
		r.git("tag", "v1.3.0")
		r.commit("b")
		r.git("tag", "v1.4.0")
	})
	if info.Tag != "v1.4.0" || info.LatestTag != "v1.4.0" {
		t.Errorf("Tag=%q LatestTag=%q, want both v1.4.0", info.Tag, info.LatestTag)
	}
	if info.Version != "1.4.0" {
		t.Errorf("Version = %q, want 1.4.0", info.Version)
	}
	if info.PreviousTag != "v1.3.0" {
		t.Errorf("PreviousTag = %q, want v1.3.0", info.PreviousTag)
	}
}

// TestGather_TagTwoCommitsBack is the regression for releasing an untagged
// commit under the last release's version: two clean commits past v1.4.0 must
// not report Version 1.4.0, nor carry v1.4.0 as the tag on HEAD.
func TestGather_TagTwoCommitsBack(t *testing.T) {
	info := gathered(t, func(r *gitRepo) {
		r.commit("a")
		r.git("tag", "v1.4.0")
		r.commit("b")
		r.commit("c")
	})
	if info.Dirty {
		t.Fatal("fixture should be clean")
	}
	if info.Tag != "" {
		t.Errorf("Tag = %q, want empty (HEAD is not tagged)", info.Tag)
	}
	if info.LatestTag != "v1.4.0" {
		t.Errorf("LatestTag = %q, want v1.4.0", info.LatestTag)
	}
	if want := "1.4.0-SNAPSHOT-" + info.ShortCommit; info.Version != want {
		t.Errorf("Version = %q, want %q", info.Version, want)
	}
	// The commits being released are those since the last release.
	if info.PreviousTag != "v1.4.0" {
		t.Errorf("PreviousTag = %q, want v1.4.0", info.PreviousTag)
	}
}

func TestGather_DirtyTaggedHEAD(t *testing.T) {
	info := gathered(t, func(r *gitRepo) {
		r.commit("a")
		r.git("tag", "v1.4.0")
		if err := os.WriteFile(filepath.Join(r.dir, "a"), []byte("changed"), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	if info.Tag != "v1.4.0" || !info.Dirty {
		t.Fatalf("Tag=%q Dirty=%v, want v1.4.0 and dirty", info.Tag, info.Dirty)
	}
	if want := "1.4.0-SNAPSHOT-" + info.ShortCommit + "-dirty"; info.Version != want {
		t.Errorf("Version = %q, want %q", info.Version, want)
	}
}

func TestGather_NoTags(t *testing.T) {
	info := gathered(t, func(r *gitRepo) {
		r.commit("a")
		r.commit("b")
	})
	if info.Tag != "" || info.LatestTag != "" || info.PreviousTag != "" {
		t.Errorf("Tag=%q LatestTag=%q PreviousTag=%q, want all empty", info.Tag, info.LatestTag, info.PreviousTag)
	}
	if want := "0.0.0-SNAPSHOT-" + info.ShortCommit; info.Version != want {
		t.Errorf("Version = %q, want %q", info.Version, want)
	}
}

// A snapshot of a tagged commit is still a snapshot; SnapshotVersion is what
// the git strategy reports under --snapshot.
func TestGather_SnapshotOfTaggedHEAD(t *testing.T) {
	info := gathered(t, func(r *gitRepo) {
		r.commit("a")
		r.git("tag", "v1.4.0")
	})
	if want := "1.4.0-SNAPSHOT-" + info.ShortCommit; info.SnapshotVersion() != want {
		t.Errorf("SnapshotVersion() = %q, want %q", info.SnapshotVersion(), want)
	}
}
