// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package gitinfo

import (
	"os"
	"testing"
)

// gather runs Gather with the user's git config kept out, as the fixture is.
func gather(t *testing.T, dir string) *Info {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	info, err := Gather(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

// A non-version tag sharing HEAD with a release must not become the version
// (#47). "deploy-prod" sorts before "v1.0.0", which is the order git's own
// describe picks in.
func TestGatherIgnoresNonVersionTags(t *testing.T) {
	r := newRepo(t, t.TempDir(), "-b", "main")
	r.commit("a")
	r.git("tag", "v0.9.0")
	r.git("tag", "a-marker") // a non-version tag on the previous release too
	r.commit("b")
	r.git("tag", "v1.0.0")
	r.git("tag", "deploy-prod")
	r.git("tag", "-a", "-m", "annotated", "build-42")

	info := gather(t, r.dir)
	if info.Tag != "v1.0.0" || info.Version != "1.0.0" {
		t.Errorf("Tag, Version = %q, %q; want v1.0.0, 1.0.0", info.Tag, info.Version)
	}
	if info.LatestTag != "v1.0.0" {
		t.Errorf("LatestTag = %q, want v1.0.0", info.LatestTag)
	}
	if info.PreviousTag != "v0.9.0" {
		t.Errorf("PreviousTag = %q, want v0.9.0", info.PreviousTag)
	}
}

// With several version tags on HEAD the highest by semver wins, bare and
// v-prefixed alike, and a release outranks its own prereleases.
func TestGatherPicksHighestVersionOnHEAD(t *testing.T) {
	r := newRepo(t, t.TempDir(), "-b", "main")
	r.commit("a")
	for _, tag := range []string{"v1.10.0-rc.1", "1.9.0", "v1.10.0", "v1.2.0"} {
		r.git("tag", tag)
	}
	if info := gather(t, r.dir); info.Tag != "v1.10.0" || info.Version != "1.10.0" {
		t.Errorf("Tag, Version = %q, %q; want v1.10.0, 1.10.0", info.Tag, info.Version)
	}
}

// An untagged HEAD snapshots the nearest version tag, stepping past a
// version-looking tag that is not one ("v2-legacy" passes describe's
// "v[0-9]*" glob) and past non-version tags on nearer commits.
func TestGatherLatestTagSkipsNonVersions(t *testing.T) {
	r := newRepo(t, t.TempDir(), "-b", "main")
	r.commit("a")
	r.git("tag", "1.4.0")
	r.commit("b")
	r.git("tag", "v2-legacy")
	r.commit("c")
	r.git("tag", "deploy-prod")
	r.commit("d")

	info := gather(t, r.dir)
	if info.Tag != "" {
		t.Errorf("Tag = %q, want none", info.Tag)
	}
	if info.LatestTag != "1.4.0" || info.PreviousTag != "1.4.0" {
		t.Errorf("LatestTag, PreviousTag = %q, %q; want 1.4.0, 1.4.0", info.LatestTag, info.PreviousTag)
	}
	if want := "1.4.0-SNAPSHOT-" + info.ShortCommit; info.Version != want {
		t.Errorf("Version = %q, want %q", info.Version, want)
	}
}

// Only non-version tags anywhere: no tag, a 0.0.0 snapshot.
func TestGatherOnlyNonVersionTags(t *testing.T) {
	r := newRepo(t, t.TempDir(), "-b", "main")
	r.commit("a")
	r.git("tag", "deploy-prod")
	info := gather(t, r.dir)
	if info.Tag != "" || info.LatestTag != "" || info.PreviousTag != "" {
		t.Errorf("Tag, LatestTag, PreviousTag = %q, %q, %q; want all empty", info.Tag, info.LatestTag, info.PreviousTag)
	}
	if want := "0.0.0-SNAPSHOT-" + info.ShortCommit; info.Version != want {
		t.Errorf("Version = %q, want %q", info.Version, want)
	}
}

func TestParseVersion(t *testing.T) {
	good := []string{"v1.2.3", "1.2.3", "0.0.0", "v1.2.3-rc.1", "1.2.3-alpha-x.0", "v1.2.3+build.5", "1.2.3-rc.1+meta"}
	bad := []string{"", "v", "deploy-prod", "v1", "v1.2", "1.2.3.4", "v01.2.3", "1.2.3-", "1.2.3-rc..1", "1.2.3-01", "1.2.3+", "v2-legacy", "vv1.2.3", "1.2.3-rc_1"}
	for _, s := range good {
		if _, ok := parseVersion(s); !ok {
			t.Errorf("parseVersion(%q) rejected, want accepted", s)
		}
	}
	for _, s := range bad {
		if _, ok := parseVersion(s); ok {
			t.Errorf("parseVersion(%q) accepted, want rejected", s)
		}
	}
}

// The ordering is semver precedence (semver.org §11), lowest first.
func TestVersionCompare(t *testing.T) {
	order := []string{
		"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta",
		"1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.2.0", "1.10.0", "2.0.0",
	}
	for i := range order {
		for j := range order {
			a, _ := parseVersion(order[i])
			b, _ := parseVersion(order[j])
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got := a.compare(b); got != want {
				t.Errorf("compare(%s, %s) = %d, want %d", order[i], order[j], got, want)
			}
		}
	}
	a, _ := parseVersion("1.0.0+a")
	b, _ := parseVersion("v1.0.0+b")
	if a.compare(b) != 0 {
		t.Error("build metadata must not affect precedence")
	}
}
