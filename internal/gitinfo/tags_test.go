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

// CommitTime is HEAD's committer date, not the author date and not now.
func TestGatherCommitTime(t *testing.T) {
	r := newRepo(t, t.TempDir(), "-b", "main")
	t.Setenv("GIT_AUTHOR_DATE", "2001-01-01T00:00:00Z")
	t.Setenv("GIT_COMMITTER_DATE", "2024-05-06T07:08:09Z")
	r.commit("a")
	info := gather(t, r.dir)
	if got := info.CommitTime.Unix(); got != 1714979289 {
		t.Errorf("CommitTime = %v (%d), want 2024-05-06T07:08:09Z", info.CommitTime, got)
	}
	if empty := gather(t, newRepo(t, t.TempDir(), "-b", "main").dir); !empty.CommitTime.IsZero() {
		t.Errorf("CommitTime with no commits = %v, want zero", empty.CommitTime)
	}
}

func TestHTTPSURL(t *testing.T) {
	cases := map[string]string{
		"git@github.com:acme/app.git":                       "https://github.com/acme/app",
		"github.com:acme/app":                               "https://github.com/acme/app",
		"https://github.com/acme/app.git":                   "https://github.com/acme/app",
		"https://x-access-token:s3cr3t@github.com/acme/app": "https://github.com/acme/app",
		"http://gitlab.example.com/group/sub/app.git/":      "https://gitlab.example.com/group/sub/app",
		"ssh://git@github.com:22/acme/app.git":              "https://github.com/acme/app",
		"git://example.org/app":                             "https://example.org/app",
		"file:///srv/git/app.git":                           "",
		"/srv/git/app.git":                                  "",
		"./a:b":                                             "",
		`C:\repos\app`:                                      "",
		"":                                                  "",
	}
	for in, want := range cases {
		if got := HTTPSURL(in); got != want {
			t.Errorf("HTTPSURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGatherSourceURL(t *testing.T) {
	r := newRepo(t, t.TempDir(), "-b", "main")
	r.commit("a")
	if got := gather(t, r.dir).SourceURL; got != "" {
		t.Errorf("SourceURL without origin = %q, want empty", got)
	}
	r.git("remote", "add", "origin", "git@github.com:acme/app.git")
	if got := gather(t, r.dir).SourceURL; got != "https://github.com/acme/app" {
		t.Errorf("SourceURL = %q", got)
	}
}
