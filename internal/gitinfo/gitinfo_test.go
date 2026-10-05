// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package gitinfo

import "testing"

func TestDeriveVersion(t *testing.T) {
	cases := []struct {
		name string
		info Info
		want string
	}{
		{
			name: "clean tagged checkout strips v",
			info: Info{Tag: "v1.2.3", ShortCommit: "abc1234"},
			want: "1.2.3",
		},
		{
			name: "clean tag without v prefix",
			info: Info{Tag: "2.0.0", ShortCommit: "abc1234"},
			want: "2.0.0",
		},
		{
			name: "dirty tagged checkout is a snapshot",
			info: Info{Tag: "v1.2.3", LatestTag: "v1.2.3", ShortCommit: "abc1234", Dirty: true},
			want: "1.2.3-SNAPSHOT-abc1234-dirty",
		},
		{
			name: "untagged clean checkout past a tag is a snapshot of that tag",
			info: Info{LatestTag: "v1.2.3", ShortCommit: "abc1234"},
			want: "1.2.3-SNAPSHOT-abc1234",
		},
		{
			name: "untagged clean checkout",
			info: Info{ShortCommit: "abc1234"},
			want: "0.0.0-SNAPSHOT-abc1234",
		},
		{
			name: "untagged dirty checkout",
			info: Info{ShortCommit: "abc1234", Dirty: true},
			want: "0.0.0-SNAPSHOT-abc1234-dirty",
		},
		{
			name: "no short commit falls back to unknown",
			info: Info{Dirty: true},
			want: "0.0.0-SNAPSHOT-unknown-dirty",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := deriveVersion(&tc.info); got != tc.want {
				t.Errorf("deriveVersion() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReleaseTag(t *testing.T) {
	cases := []struct {
		name    string
		info    *Info
		version string
		want    string
	}{
		{"tag on HEAD names the version", &Info{Tag: "v1.4.0"}, "1.4.0", "v1.4.0"},
		{"tag without v kept verbatim", &Info{Tag: "1.4.0"}, "1.4.0", "1.4.0"},
		{"untagged HEAD uses the version", &Info{LatestTag: "v1.4.0"}, "1.5.0", "v1.5.0"},
		{"tag on HEAD naming another version is ignored", &Info{Tag: "v1.4.0"}, "2.0.0", "v2.0.0"},
		{"nil info", nil, "1.0.0", "v1.0.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.info.ReleaseTag(tc.version); got != tc.want {
				t.Errorf("ReleaseTag(%q) = %q, want %q", tc.version, got, tc.want)
			}
		})
	}
}
