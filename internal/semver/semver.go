// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package semver parses and orders semantic versions (semver.org 2.0.0). It is
// the one parser behind both the git tag selection (internal/gitinfo) and the
// .Major/.Minor/.Patch template fields (internal/tmpl), so a version that
// names a release and a version a tag is derived from can never disagree.
package semver

import (
	"cmp"
	"strconv"
	"strings"
)

// Version is a parsed semantic version: MAJOR.MINOR.PATCH with an optional
// prerelease. Build metadata is accepted and, per semver, ignored for
// precedence.
type Version struct {
	Major, Minor, Patch int
	// Pre holds the dot-separated prerelease identifiers ("rc", "1" for
	// "-rc.1"); empty for a release.
	Pre []string
}

// Parse accepts a semantic version with an optional leading "v" ("v1.2.3",
// "1.2.3-rc.1", "v1.2.3+build.5") and rejects everything else.
func Parse(s string) (Version, bool) {
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		if !validIdents(s[i+1:], false) {
			return Version{}, false
		}
		s = s[:i]
	}
	var v Version
	if i := strings.IndexByte(s, '-'); i >= 0 {
		if !validIdents(s[i+1:], true) {
			return Version{}, false
		}
		v.Pre = strings.Split(s[i+1:], ".")
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, false
	}
	core := [3]*int{&v.Major, &v.Minor, &v.Patch}
	for i, p := range parts {
		if !isNumeric(p) || (len(p) > 1 && p[0] == '0') {
			return Version{}, false
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return Version{}, false
		}
		*core[i] = n
	}
	return v, true
}

// IsPrerelease reports whether v carries a prerelease ("1.3.0-rc.1").
func (v Version) IsPrerelease() bool { return len(v.Pre) > 0 }

// Prerelease is the prerelease part without its leading "-" ("rc.1"), or "".
func (v Version) Prerelease() string { return strings.Join(v.Pre, ".") }

// validIdents checks dot-separated semver identifiers: non-empty, [0-9A-Za-z-],
// and (for prerelease) no leading zero on a numeric identifier.
func validIdents(s string, prerelease bool) bool {
	for _, id := range strings.Split(s, ".") {
		if id == "" {
			return false
		}
		for _, c := range id {
			if (c < '0' || c > '9') && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && c != '-' {
				return false
			}
		}
		if prerelease && isNumeric(id) && len(id) > 1 && id[0] == '0' {
			return false
		}
	}
	return true
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// Compare orders versions by semver precedence: -1, 0, or 1.
func (v Version) Compare(o Version) int {
	for _, c := range [...]int{cmp.Compare(v.Major, o.Major), cmp.Compare(v.Minor, o.Minor), cmp.Compare(v.Patch, o.Patch)} {
		if c != 0 {
			return c
		}
	}
	// A release outranks any prerelease of the same core version.
	switch {
	case len(v.Pre) == 0 && len(o.Pre) == 0:
		return 0
	case len(v.Pre) == 0:
		return 1
	case len(o.Pre) == 0:
		return -1
	}
	for i := 0; i < len(v.Pre) && i < len(o.Pre); i++ {
		a, b := v.Pre[i], o.Pre[i]
		an, bn := isNumeric(a), isNumeric(b)
		var c int
		switch {
		case an && bn:
			// Compare numerically without overflow: longer is larger, as
			// neither has leading zeros.
			c = cmp.Compare(len(a), len(b))
			if c == 0 {
				c = strings.Compare(a, b)
			}
		case an:
			c = -1
		case bn:
			c = 1
		default:
			c = strings.Compare(a, b)
		}
		if c != 0 {
			return c
		}
	}
	return cmp.Compare(len(v.Pre), len(o.Pre))
}
