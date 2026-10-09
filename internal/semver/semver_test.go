// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package semver

import "testing"

func TestParse(t *testing.T) {
	good := []string{"v1.2.3", "1.2.3", "0.0.0", "v1.2.3-rc.1", "1.2.3-alpha-x.0", "v1.2.3+build.5", "1.2.3-rc.1+meta"}
	bad := []string{
		"",
		"v",
		"deploy-prod",
		"v1",
		"v1.2",
		"1.2.3.4",
		"v01.2.3",
		"1.2.3-",
		"1.2.3-rc..1",
		"1.2.3-01",
		"1.2.3+",
		"v2-legacy",
		"vv1.2.3",
		"1.2.3-rc_1",
	}
	for _, s := range good {
		if _, ok := Parse(s); !ok {
			t.Errorf("Parse(%q) rejected, want accepted", s)
		}
	}
	for _, s := range bad {
		if _, ok := Parse(s); ok {
			t.Errorf("Parse(%q) accepted, want rejected", s)
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
			a, _ := Parse(order[i])
			b, _ := Parse(order[j])
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got := a.Compare(b); got != want {
				t.Errorf("compare(%s, %s) = %d, want %d", order[i], order[j], got, want)
			}
		}
	}
	a, _ := Parse("1.0.0+a")
	b, _ := Parse("v1.0.0+b")
	if a.Compare(b) != 0 {
		t.Error("build metadata must not affect precedence")
	}
}

func TestParseParts(t *testing.T) {
	v, ok := Parse("v1.12.3-rc.1+meta")
	if !ok || v.Major != 1 || v.Minor != 12 || v.Patch != 3 || v.Prerelease() != "rc.1" || !v.IsPrerelease() {
		t.Errorf("Parse(v1.12.3-rc.1+meta) = %+v, %v", v, ok)
	}
	if r, _ := Parse("2.0.0+meta"); r.IsPrerelease() || r.Prerelease() != "" {
		t.Errorf("build metadata is not a prerelease: %+v", r)
	}
}
