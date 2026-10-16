// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package versioner

import (
	"errors"
	"testing"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/conventional"
	"github.com/blairham/stevedore/internal/gitinfo"
	"github.com/blairham/stevedore/internal/semver"
)

// commit builds a commit whose SHA is padded so Reason shortens it.
func commit(sha, subject, body string) gitinfo.Commit {
	return gitinfo.Commit{SHA: sha + "0000000000", Subject: subject, Body: body}
}

// untagged is an untagged HEAD one release after v1.4.2.
func untagged() *gitinfo.Info {
	return &gitinfo.Info{
		Version:     "1.4.2-SNAPSHOT-abc1234",
		LatestTag:   "v1.4.2",
		PreviousTag: "v1.4.2",
		ShortCommit: "abc1234",
	}
}

// commitsSince serves commits for exactly one base, so a test also proves the
// range starts where it should.
func commitsSince(t *testing.T, wantBase string, commits ...gitinfo.Commit) func(string) ([]gitinfo.Commit, error) {
	t.Helper()
	return func(base string) ([]gitinfo.Commit, error) {
		if base != wantBase {
			t.Errorf("commits listed since %q, want since %q", base, wantBase)
		}
		return commits, nil
	}
}

func TestAutoGitUntagged(t *testing.T) {
	cases := []struct {
		name     string
		commits  []gitinfo.Commit
		cfg      config.Versioning
		want     string
		nothing  bool
		decision string
	}{
		{
			name:     "fix is a patch",
			commits:  []gitinfo.Commit{commit("a1b2c3d", "docs: readme", ""), commit("b2c3d4e", "fix: crash on start", "")},
			want:     "1.4.3",
			decision: "patch: fix: crash on start, b2c3d4e",
		},
		{
			name:     "perf is a patch",
			commits:  []gitinfo.Commit{commit("a1b2c3d", "perf: faster", "")},
			want:     "1.4.3",
			decision: "patch: perf: faster, a1b2c3d",
		},
		{
			name:     "feat is a minor",
			commits:  []gitinfo.Commit{commit("a1b2c3d", "fix: x", ""), commit("b2c3d4e", "feat(api): add pagination", "")},
			want:     "1.5.0",
			decision: "minor: feat(api): add pagination, b2c3d4e",
		},
		{
			name:     "type is case-insensitive",
			commits:  []gitinfo.Commit{commit("a1b2c3d", "Feat: shout", "")},
			want:     "1.5.0",
			decision: "minor: Feat: shout, a1b2c3d",
		},
		{
			name:     "breaking via bang",
			commits:  []gitinfo.Commit{commit("a1b2c3d", "feat: x", ""), commit("b2c3d4e", "refactor!: drop v0 config", "")},
			want:     "2.0.0",
			decision: "major: refactor!: drop v0 config, b2c3d4e",
		},
		{
			name:     "breaking via footer",
			commits:  []gitinfo.Commit{commit("a1b2c3d", "fix: y", "BREAKING CHANGE: flag removed")},
			want:     "2.0.0",
			decision: "major: fix: y, a1b2c3d",
		},
		{
			name: "nothing bump-worthy skips",
			commits: []gitinfo.Commit{
				commit("a1b2c3d", "docs: x", ""),
				commit("b2c3d4e", "chore: y", ""),
				commit("c3d4e5f", "plain", ""),
			},
			want:     "1.4.2",
			nothing:  true,
			decision: "nothing to release: no commit since 1.4.2 calls for a version bump",
		},
		{
			name:     "no commits skips",
			want:     "1.4.2",
			nothing:  true,
			decision: "nothing to release: no commit since 1.4.2 calls for a version bump",
		},
		{
			name:     "on_no_changes: patch",
			commits:  []gitinfo.Commit{commit("a1b2c3d", "docs: x", "")},
			cfg:      config.Versioning{OnNoChanges: "patch"},
			want:     "1.4.3",
			decision: "patch: no commit since 1.4.2 calls for a bump (versioning.on_no_changes: patch)",
		},
		{
			name:     "bump_types adds a type",
			commits:  []gitinfo.Commit{commit("a1b2c3d", "revert: undo x", "")},
			cfg:      config.Versioning{BumpTypes: config.BumpTypes{"revert": "patch"}},
			want:     "1.4.3",
			decision: "patch: revert: undo x, a1b2c3d",
		},
		{
			name:    "bump_types turns a type off",
			commits: []gitinfo.Commit{commit("a1b2c3d", "fix: x", "")},
			cfg:     config.Versioning{BumpTypes: config.BumpTypes{"fix": "none"}},
			want:    "1.4.2",
			nothing: true,
		},
		{
			name:     "bump_types cannot outvote breaking",
			commits:  []gitinfo.Commit{commit("a1b2c3d", "fix!: x", "")},
			cfg:      config.Versioning{BumpTypes: config.BumpTypes{"fix": "none"}},
			want:     "2.0.0",
			decision: "major: fix!: x, a1b2c3d",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			cfg.Strategy, cfg.Bump, cfg.Initial = "git", config.BumpAuto, "0.1.0"
			d, err := Decide(Input{Cfg: cfg, Git: untagged(), Commits: commitsSince(t, "1.4.2", tc.commits...)})
			if err != nil {
				t.Fatal(err)
			}
			if d.Version != tc.want {
				t.Errorf("version = %q, want %q", d.Version, tc.want)
			}
			if d.NothingToRelease != tc.nothing {
				t.Errorf("NothingToRelease = %v, want %v", d.NothingToRelease, tc.nothing)
			}
			if tc.decision != "" && d.Reason() != tc.decision {
				t.Errorf("Reason() = %q, want %q", d.Reason(), tc.decision)
			}
		})
	}
}

// No previous tag: nothing to bump, so the first release is versioning.initial
// and no commit is consulted.
func TestAutoGitNoPreviousTag(t *testing.T) {
	g := &gitinfo.Info{Version: "0.0.0-SNAPSHOT-abc1234", ShortCommit: "abc1234"}
	d, err := Decide(Input{
		Cfg: config.Versioning{Strategy: "git", Bump: config.BumpAuto, Initial: "v0.3.0"},
		Git: g,
		Commits: func(string) ([]gitinfo.Commit, error) {
			t.Error("commits listed with no previous release")
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.Version != "0.3.0" || !d.Initial || d.NothingToRelease {
		t.Errorf("decision = %+v, want initial 0.3.0", d)
	}
	if d.Reason() != "first release: versioning.initial" {
		t.Errorf("Reason() = %q", d.Reason())
	}
}

// A tag on HEAD is the release's name under auto too: nothing is derived.
func TestAutoGitTaggedHEADKeepsTag(t *testing.T) {
	d, err := Decide(Input{
		Cfg: config.Versioning{Strategy: "git", Bump: config.BumpAuto},
		Git: gi(),
		Commits: func(string) ([]gitinfo.Commit, error) {
			t.Error("commits listed for a tagged HEAD")
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.Version != "1.2.3" || d.Auto {
		t.Errorf("decision = %+v, want the tag's 1.2.3", d)
	}
}

// Without auto, an untagged HEAD stays a snapshot: auto is opt-in.
func TestGitWithoutAutoStaysSnapshot(t *testing.T) {
	got, err := Resolve(Input{Cfg: config.Versioning{Strategy: "git", Bump: "patch"}, Git: untagged()})
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.4.2-SNAPSHOT-abc1234" {
		t.Errorf("git without auto = %q, want the snapshot", got)
	}
}

// A snapshot under auto is the next version plus the snapshot suffix, and is
// never nothing to release.
func TestAutoSnapshot(t *testing.T) {
	cfg := config.Versioning{Strategy: "git", Bump: config.BumpAuto}
	d, err := Decide(
		Input{Cfg: cfg, Git: untagged(), Snapshot: true, Commits: commitsSince(t, "1.4.2", commit("a", "feat: x", ""))},
	)
	if err != nil {
		t.Fatal(err)
	}
	if d.Version != "1.5.0-SNAPSHOT-abc1234" {
		t.Errorf("snapshot = %q, want 1.5.0-SNAPSHOT-abc1234", d.Version)
	}
	d, err = Decide(Input{Cfg: cfg, Git: untagged(), Snapshot: true, Commits: commitsSince(t, "1.4.2")})
	if err != nil {
		t.Fatal(err)
	}
	if d.NothingToRelease || d.Version != "1.4.2-SNAPSHOT-abc1234" {
		t.Errorf("empty snapshot = %+v, want a 1.4.2 snapshot that still builds", d)
	}
}

func TestAutoRegistry(t *testing.T) {
	list := func(string) ([]string, error) { return []string{"1.3.9", "latest", "1.4.0-rc.1", "v1.2.0"}, nil }
	d, err := Decide(Input{
		Cfg:      config.Versioning{Strategy: "registry", Bump: config.BumpAuto, Initial: "0.1.0"},
		Git:      gi(),
		Repo:     "ghcr.io/x/app",
		ListTags: list,
		Commits:  commitsSince(t, "1.3.9", commit("a1b2c3d", "feat: x", "")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.Version != "1.4.0" {
		t.Errorf("registry auto = %q, want 1.4.0", d.Version)
	}

	// An empty registry starts at initial, auto or not.
	empty := func(string) ([]string, error) { return []string{"latest"}, nil }
	d, err = Decide(Input{
		Cfg:      config.Versioning{Strategy: "ecr", Bump: config.BumpAuto, Initial: "0.1.0"},
		Git:      gi(),
		Repo:     "r",
		ListTags: empty,
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.Version != "0.1.0" || !d.Initial {
		t.Errorf("empty registry under auto = %+v, want initial 0.1.0", d)
	}
}

func TestAutoNeedsCommits(t *testing.T) {
	_, err := Decide(Input{Cfg: config.Versioning{Strategy: "git", Bump: config.BumpAuto}, Git: untagged()})
	if !errors.Is(err, ErrNoCommits) {
		t.Errorf("err = %v, want ErrNoCommits", err)
	}
}

func TestNext(t *testing.T) {
	base := mustParse(t, "0.9.4")
	cases := []struct {
		level   conventional.Level
		onNo    string
		want    string
		nothing bool
	}{
		{conventional.Major, "", "1.0.0", false},
		{conventional.Minor, "", "0.10.0", false},
		{conventional.Patch, "", "0.9.5", false},
		{conventional.None, "", "0.9.4", true},
		{conventional.None, "skip", "0.9.4", true},
		{conventional.None, "patch", "0.9.5", false},
	}
	for _, tc := range cases {
		d := Next(base, conventional.Decision{Level: tc.level}, tc.onNo)
		if d.Version != tc.want || d.NothingToRelease != tc.nothing {
			t.Errorf("Next(0.9.4, %s, %q) = %s (nothing=%v), want %s (nothing=%v)",
				tc.level, tc.onNo, d.Version, d.NothingToRelease, tc.want, tc.nothing)
		}
	}
}

func mustParse(t *testing.T, s string) semver.Version {
	t.Helper()
	v, ok := parseRelease(s)
	if !ok {
		t.Fatalf("parseRelease(%q) rejected", s)
	}
	return v
}
