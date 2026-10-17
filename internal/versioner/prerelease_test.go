// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package versioner

import (
	"strings"
	"testing"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/conventional"
	"github.com/blairham/stevedore/internal/gitinfo"
	"github.com/blairham/stevedore/internal/semver"
)

// version parses any semver, prereleases included.
func version(t *testing.T, s string) *semver.Version {
	t.Helper()
	v, ok := semver.Parse(s)
	if !ok {
		t.Fatalf("semver.Parse(%q) rejected", s)
	}
	return &v
}

// The rc sequence, run by run, from the release 1.4.0: start, increment,
// restart on a higher bump, never below the series, promote.
func TestNextPrereleaseSeries(t *testing.T) {
	cases := []struct {
		name   string
		series string // the prerelease in flight after 1.4.0; "" for none
		level  conventional.Level
		policy Policy
		want   string
		step   string
	}{
		{"start", "", conventional.Minor, Policy{Prerelease: "rc"}, "1.5.0-rc.1", ""},
		{"start on a patch", "", conventional.Patch, Policy{Prerelease: "rc"}, "1.4.1-rc.1", ""},
		{"increment", "1.5.0-rc.1", conventional.Minor, Policy{Prerelease: "rc"}, "1.5.0-rc.2", SeriesNext},
		{"increment is numeric", "1.5.0-rc.9", conventional.Minor, Policy{Prerelease: "rc"}, "1.5.0-rc.10", SeriesNext},
		{"increment from rc.0", "1.5.0-rc.0", conventional.Minor, Policy{Prerelease: "rc"}, "1.5.0-rc.1", SeriesNext},
		{"restart on a higher bump", "1.5.0-rc.2", conventional.Major, Policy{Prerelease: "rc"}, "2.0.0-rc.1", SeriesRestart},
		{
			"restart from a patch series",
			"1.4.1-rc.3",
			conventional.Minor,
			Policy{Prerelease: "rc"},
			"1.5.0-rc.1",
			SeriesRestart,
		},
		{
			"a lower bump stays on the series",
			"1.5.0-rc.2",
			conventional.Patch,
			Policy{Prerelease: "rc"},
			"1.5.0-rc.3",
			SeriesNext,
		},
		{
			"no new bump continues the series",
			"1.5.0-rc.2",
			conventional.None,
			Policy{Prerelease: "rc"},
			"1.5.0-rc.3",
			SeriesNext,
		},
		{"promote", "1.5.0-rc.2", conventional.Minor, Policy{}, "1.5.0", SeriesPromote},
		{"promote with no new bump", "1.5.0-rc.2", conventional.None, Policy{}, "1.5.0", SeriesPromote},
		{"promote under a lower bump", "1.5.0-rc.2", conventional.Patch, Policy{}, "1.5.0", SeriesPromote},
		{"supersede on a higher bump", "1.5.0-rc.2", conventional.Major, Policy{}, "2.0.0", SeriesSupersede},
		{
			"another identifier that sorts higher starts at 1",
			"1.5.0-beta.3",
			conventional.Minor,
			Policy{Prerelease: "rc"},
			"1.5.0-rc.1",
			SeriesRestart,
		},
		{"no series, no changes: nothing", "", conventional.None, Policy{Prerelease: "rc"}, "1.4.0", ""},
		{
			"no series, on_no_changes: patch",
			"",
			conventional.None,
			Policy{Prerelease: "rc", OnNoChanges: "patch"},
			"1.4.1-rc.1",
			"",
		},
	}
	base := mustParse(t, "1.4.0")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var series *semver.Version
			if tc.series != "" {
				series = version(t, tc.series)
			}
			d, err := Next(base, series, conventional.Decision{Level: tc.level}, tc.policy)
			if err != nil {
				t.Fatal(err)
			}
			if d.Version != tc.want || d.SeriesStep != tc.step || d.Series != tc.series {
				t.Errorf("Next = %s (%q %q), want %s (%q %q)", d.Version, d.SeriesStep, d.Series, tc.want, tc.step, tc.series)
			}
			if d.NothingToRelease != (tc.want == "1.4.0") {
				t.Errorf("NothingToRelease = %v", d.NothingToRelease)
			}
		})
	}
}

// Switching to an identifier that sorts below the series in flight would cut
// a version lower than one already published: refused, not reordered.
func TestNextPrereleaseNeverGoesBackwards(t *testing.T) {
	_, err := Next(
		mustParse(t, "1.4.0"),
		version(t, "1.5.0-rc.2"),
		conventional.Decision{Level: conventional.Minor},
		Policy{Prerelease: "alpha"},
	)
	if err == nil || !strings.Contains(err.Error(), "1.5.0-alpha.1 would not sort above 1.5.0-rc.2") {
		t.Errorf("err = %v, want alpha refused below rc", err)
	}
}

// pre_major: minor on 0.x takes every level one step lower; major (the
// default) is strict semver, and 1.x is never remapped.
func TestNextPreMajor(t *testing.T) {
	cases := []struct {
		base, preMajor string
		level          conventional.Level
		want           string
		remapped       bool
	}{
		{"0.4.2", "minor", conventional.Major, "0.5.0", true},
		{"0.4.2", "minor", conventional.Minor, "0.4.3", true},
		{"0.4.2", "minor", conventional.Patch, "0.4.3", false},
		{"0.0.3", "minor", conventional.Major, "0.1.0", true},
		{"0.4.2", "major", conventional.Major, "1.0.0", false},
		{"0.4.2", "", conventional.Major, "1.0.0", false},
		{"0.4.2", "", conventional.Minor, "0.5.0", false},
		{"1.4.2", "minor", conventional.Major, "2.0.0", false},
		{"1.4.2", "minor", conventional.Minor, "1.5.0", false},
	}
	for _, tc := range cases {
		d, err := Next(mustParse(t, tc.base), nil, conventional.Decision{Level: tc.level}, Policy{PreMajor: tc.preMajor})
		if err != nil {
			t.Fatal(err)
		}
		if d.Version != tc.want || d.PreMajor != tc.remapped {
			t.Errorf("Next(%s, %s, pre_major %q) = %s (remapped %v), want %s (remapped %v)",
				tc.base, tc.level, tc.preMajor, d.Version, d.PreMajor, tc.want, tc.remapped)
		}
	}

	// on_no_changes: patch on 0.x is still a patch, and the two rules
	// compose with a prerelease.
	d, err := Next(mustParse(t, "0.4.2"), nil, conventional.Decision{}, Policy{OnNoChanges: "patch", PreMajor: "minor"})
	if err != nil || d.Version != "0.4.3" {
		t.Errorf("on_no_changes: patch on 0.x = %s, %v; want 0.4.3", d.Version, err)
	}
	d, err = Next(
		mustParse(t, "0.4.2"),
		nil,
		conventional.Decision{Level: conventional.Major},
		Policy{PreMajor: "minor", Prerelease: "rc"},
	)
	if err != nil || d.Version != "0.5.0-rc.1" {
		t.Errorf("breaking 0.x candidate = %s, %v; want 0.5.0-rc.1", d.Version, err)
	}
}

func TestReasonPrereleaseAndPreMajor(t *testing.T) {
	sel := conventional.Decision{
		Level:  conventional.Major,
		Commit: commit("a1b2c3d", "feat!: drop v0 config", ""),
	}
	d, err := Next(mustParse(t, "0.4.2"), nil, sel, Policy{PreMajor: "minor"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "minor: feat!: drop v0 config, a1b2c3d (major on 0.x: versioning.pre_major: minor)"; d.Reason() != want {
		t.Errorf("Reason() = %q, want %q", d.Reason(), want)
	}
	d, err = Next(mustParse(t, "1.4.0"), version(t, "1.5.0-rc.2"), conventional.Decision{}, Policy{})
	if err != nil {
		t.Fatal(err)
	}
	if want := "no commit since 1.4.0 calls for more than the series in flight; promotes 1.5.0-rc.2"; d.Reason() != want {
		t.Errorf("Reason() = %q, want %q", d.Reason(), want)
	}
}

// seriesGit is an untagged HEAD after the candidates of 1.5.0, whose release
// before them is 1.4.0.
func seriesGit(tag string) *gitinfo.Info {
	return &gitinfo.Info{
		Tag:           tag,
		Version:       "1.5.0-rc.2-SNAPSHOT-abc1234",
		LatestTag:     "v1.5.0-rc.2",
		LatestRelease: "v1.4.0",
		ShortCommit:   "abc1234",
	}
}

// Under git, bump: auto bumps from the latest release, not the latest
// candidate: the commits since 1.4.0 decide the target, and the candidate
// decides the number.
func TestAutoGitPrereleaseSeries(t *testing.T) {
	feat := commit("a1b2c3d", "feat: x", "")
	cases := []struct {
		name       string
		prerelease string
		commits    []gitinfo.Commit
		want       string
	}{
		{"increment", "rc", []gitinfo.Commit{feat}, "1.5.0-rc.3"},
		{"restart on breaking", "rc", []gitinfo.Commit{feat, commit("b2c3d4e", "fix!: y", "")}, "2.0.0-rc.1"},
		{"promote", "", []gitinfo.Commit{feat}, "1.5.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, err := Decide(Input{
				Cfg:     config.Versioning{Strategy: "git", Bump: config.BumpAuto, Prerelease: tc.prerelease},
				Git:     seriesGit(""),
				Commits: commitsSince(t, "1.4.0", tc.commits...),
			})
			if err != nil {
				t.Fatal(err)
			}
			if d.Version != tc.want || d.Base != "1.4.0" || d.Series != "1.5.0-rc.2" {
				t.Errorf("decision = %+v, want %s from 1.4.0 after 1.5.0-rc.2", d, tc.want)
			}
		})
	}
}

// A candidate's tag on HEAD names the release for a run of its own series,
// and is promoted by a run without one: the commit that was 1.5.0-rc.2
// becomes 1.5.0 too.
func TestAutoGitCandidateOnHEAD(t *testing.T) {
	d, err := Decide(Input{
		Cfg: config.Versioning{Strategy: "git", Bump: config.BumpAuto, Prerelease: "rc"},
		Git: func() *gitinfo.Info { g := seriesGit("v1.5.0-rc.2"); g.Version = "1.5.0-rc.2"; return g }(),
		Commits: func(string) ([]gitinfo.Commit, error) {
			t.Error("commits listed for HEAD's own candidate")
			return nil, nil
		},
	})
	if err != nil || d.Version != "1.5.0-rc.2" {
		t.Errorf("rc run on the rc's commit = %+v, %v; want 1.5.0-rc.2", d, err)
	}

	d, err = Decide(Input{
		Cfg:     config.Versioning{Strategy: "git", Bump: config.BumpAuto},
		Git:     seriesGit("v1.5.0-rc.2"),
		Commits: commitsSince(t, "1.4.0", commit("a1b2c3d", "feat: x", "")),
	})
	if err != nil || d.Version != "1.5.0" || d.SeriesStep != SeriesPromote {
		t.Errorf("release run on the rc's commit = %+v, %v; want 1.5.0 promoting it", d, err)
	}

	// A prerelease outside the series shape on HEAD names the release, as
	// any tag does.
	g := &gitinfo.Info{Tag: "v2.0.0-beta", Version: "2.0.0-beta", LatestTag: "v2.0.0-beta", LatestRelease: "v1.4.0"}
	d, err = Decide(Input{Cfg: config.Versioning{Strategy: "git", Bump: config.BumpAuto}, Git: g})
	if err != nil || d.Version != "2.0.0-beta" {
		t.Errorf("unshaped prerelease on HEAD = %+v, %v; want 2.0.0-beta", d, err)
	}
}

// No release yet, only candidates: the series is toward versioning.initial
// (or above it), and no commit is consulted, as there is nothing to bump.
func TestAutoGitSeriesBeforeFirstRelease(t *testing.T) {
	g := &gitinfo.Info{LatestTag: "v0.1.0-rc.1", ShortCommit: "abc1234"}
	for prerelease, want := range map[string]string{"rc": "0.1.0-rc.2", "": "0.1.0"} {
		d, err := Decide(Input{
			Cfg: config.Versioning{Strategy: "git", Bump: config.BumpAuto, Initial: "0.1.0", Prerelease: prerelease},
			Git: g,
			Commits: func(string) ([]gitinfo.Commit, error) {
				t.Error("commits listed with no previous release")
				return nil, nil
			},
		})
		if err != nil || d.Version != want || !d.Initial {
			t.Errorf("prerelease %q = %+v, %v; want initial %s", prerelease, d, err, want)
		}
	}
	d, err := Decide(Input{
		Cfg: config.Versioning{Strategy: "git", Bump: config.BumpAuto, Initial: "0.1.0", Prerelease: "rc"},
		Git: &gitinfo.Info{ShortCommit: "abc1234"},
	})
	if err != nil || d.Version != "0.1.0-rc.1" {
		t.Errorf("first ever candidate = %+v, %v; want 0.1.0-rc.1", d, err)
	}
}

// Under registry/ecr the series is the highest candidate above the highest
// release; other prerelease-shaped tags (variants, stale candidates) are not
// a series.
func TestRegistryPrereleaseSeries(t *testing.T) {
	tags := []string{"1.4.0", "1.5.0-rc.1", "1.5.0-rc.2", "1.4.0-rc.7", "1.6.0-alpine", "v1.5.0-rc.2.amd64", "latest"}
	cases := []struct {
		name string
		cfg  config.Versioning
		want string
	}{
		{"auto increments", config.Versioning{Bump: config.BumpAuto, Prerelease: "rc"}, "1.5.0-rc.3"},
		{"auto promotes", config.Versioning{Bump: config.BumpAuto}, "1.5.0"},
		{"fixed bump increments", config.Versioning{Bump: "minor", Prerelease: "rc"}, "1.5.0-rc.3"},
		{"fixed bump promotes", config.Versioning{Bump: "minor"}, "1.5.0"},
		{"fixed bump below the series stays on it", config.Versioning{Bump: "patch", Prerelease: "rc"}, "1.5.0-rc.3"},
		// Without a prerelease a fixed bump ignores prereleases, as before.
		{"fixed bump without prerelease is unchanged", config.Versioning{Bump: "patch"}, "1.4.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			cfg.Strategy, cfg.Initial = "registry", "0.1.0"
			d, err := Decide(Input{
				Cfg:      cfg,
				Git:      gi(),
				Repo:     "ghcr.io/x/app",
				ListTags: func(string) ([]string, error) { return tags, nil },
				Commits:  commitsSince(t, "1.4.0", commit("a1b2c3d", "feat: x", "")),
			})
			if err != nil {
				t.Fatal(err)
			}
			if d.Version != tc.want {
				t.Errorf("version = %s, want %s", d.Version, tc.want)
			}
		})
	}

	// An empty registry starts a series at initial.
	d, err := Decide(Input{
		Cfg:      config.Versioning{Strategy: "ecr", Bump: config.BumpAuto, Initial: "1.0.0", Prerelease: "rc"},
		Git:      gi(),
		Repo:     "r",
		ListTags: func(string) ([]string, error) { return nil, nil },
	})
	if err != nil || d.Version != "1.0.0-rc.1" {
		t.Errorf("empty registry candidate = %+v, %v; want 1.0.0-rc.1", d, err)
	}
}

func TestSeriesOf(t *testing.T) {
	cases := map[string]struct {
		id string
		n  int
		ok bool
	}{
		"1.5.0-rc.2":       {"rc", 2, true},
		"1.5.0-beta.10":    {"beta", 10, true},
		"1.5.0-pre-x.1":    {"pre-x", 1, true},
		"1.5.0-rc":         {"", 0, false},
		"1.5.0-rc.x":       {"", 0, false},
		"1.5.0-1.2":        {"", 0, false},
		"1.5.0-rc.1.amd64": {"", 0, false},
		"1.5.0-rc.-1":      {"", 0, false},
		"1.5.0":            {"", 0, false},
	}
	for s, want := range cases {
		id, n, ok := seriesOf(*version(t, s))
		if id != want.id || n != want.n || ok != want.ok {
			t.Errorf("seriesOf(%s) = %q, %d, %v; want %q, %d, %v", s, id, n, ok, want.id, want.n, want.ok)
		}
	}
}

// versioning.pre_major reaches bump selection from the config.
func TestAutoPreMajorFromConfig(t *testing.T) {
	g := &gitinfo.Info{LatestTag: "v0.4.2", ShortCommit: "abc1234"}
	d, err := Decide(Input{
		Cfg:     config.Versioning{Strategy: "git", Bump: config.BumpAuto, PreMajor: config.PreMajorMinor},
		Git:     g,
		Commits: commitsSince(t, "0.4.2", commit("a1b2c3d", "feat!: x", "")),
	})
	if err != nil || d.Version != "0.5.0" {
		t.Errorf("breaking on 0.4.2 under pre_major: minor = %+v, %v; want 0.5.0", d, err)
	}
}
