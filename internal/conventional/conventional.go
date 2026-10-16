// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package conventional reads Conventional Commits (conventionalcommits.org
// 1.0.0): a commit's type, scope and whether it is breaking, and the semver
// bump a set of commits calls for. It is the one definition of "is this
// breaking" behind both the changelog and `versioning.bump: auto`, so the
// notes and the version a release carries can never disagree about it.
package conventional

import (
	"regexp"
	"strings"

	"github.com/blairham/stevedore/internal/gitinfo"
)

// Level is how far a commit moves the version. The order is the precedence:
// a higher level wins over a lower one when commits are combined.
type Level int

// The bump levels, lowest first.
const (
	None Level = iota
	Patch
	Minor
	Major
)

// String is the level's config spelling: none, patch, minor or major. It is
// also what semver.Version.Bump takes.
func (l Level) String() string {
	switch l {
	case Patch:
		return "patch"
	case Minor:
		return "minor"
	case Major:
		return "major"
	default:
		return "none"
	}
}

// ParseLevel reads a level's config spelling (case-insensitive).
func ParseLevel(s string) (Level, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "none":
		return None, true
	case "patch":
		return Patch, true
	case "minor":
		return Minor, true
	case "major":
		return Major, true
	}
	return None, false
}

// Message is a commit message read as a Conventional Commit.
type Message struct {
	// Type is the lowercased type ("feat" for "Feat(api): x"), or "" when
	// the subject is not a Conventional Commit.
	Type string
	// Scope is the scope without its parentheses ("api"), or "".
	Scope string
	// Description is the subject after the "type(scope)!:" prefix, or the
	// whole subject when there is no prefix.
	Description string
	// Breaking is set by a "!" before the colon or by a BREAKING CHANGE (or
	// BREAKING-CHANGE) footer in the body. The footer counts even on a
	// subject that is not a Conventional Commit.
	Breaking bool
}

var subjectRe = regexp.MustCompile(`^(\w+)(\([^)]*\))?(!)?:\s*(.+)$`)

// breakingFooter is the Conventional Commits footer that marks a breaking
// change from the body, as `!` does from the subject. The spec requires the
// token in capitals, and accepts BREAKING-CHANGE as a synonym.
var breakingFooter = regexp.MustCompile(`(?m)^BREAKING[ -]CHANGE:`)

// Parse reads a commit's subject and body.
func Parse(subject, body string) Message {
	msg := Message{Description: subject, Breaking: breakingFooter.MatchString(body)}
	m := subjectRe.FindStringSubmatch(subject)
	if m == nil {
		return msg
	}
	// Types are case-insensitive in Conventional Commits: "Feat:" is a feature.
	msg.Type = strings.ToLower(m[1])
	msg.Scope = strings.Trim(m[2], "()")
	msg.Breaking = msg.Breaking || m[3] != ""
	msg.Description = m[4]
	return msg
}

// Rules maps a commit type (lowercase) to the level it bumps. A type with no
// entry bumps nothing. A breaking commit is Major whatever its type.
type Rules map[string]Level

// DefaultRules is the Conventional Commits mapping: feat is a minor bump, fix
// and perf are patch bumps, everything else bumps nothing.
func DefaultRules() Rules {
	return Rules{"feat": Minor, "fix": Patch, "perf": Patch}
}

// With returns a copy of r with overrides applied over it, key by key: an
// override adds a type (revert: patch), changes one (perf: minor), or turns
// one off (fix: none). Keys are matched case-insensitively.
func (r Rules) With(overrides map[string]Level) Rules {
	out := make(Rules, len(r)+len(overrides))
	for k, v := range r {
		out[strings.ToLower(k)] = v
	}
	for k, v := range overrides {
		out[strings.ToLower(k)] = v
	}
	return out
}

// Level is the bump m calls for under r.
func (r Rules) Level(m Message) Level {
	if m.Breaking {
		return Major
	}
	return r[m.Type]
}

// Decision is the bump a set of commits calls for, and the commit that
// decided it.
type Decision struct {
	// Level is the highest level any commit calls for; None when no commit
	// calls for a bump (or there are no commits).
	Level Level
	// Commit is the commit that decided Level: the first one, in the order
	// given, at that level. Zero when Level is None.
	Commit gitinfo.Commit
	// Message is Commit's parsed message. Zero when Level is None.
	Message Message
}

// Select returns the highest bump the commits call for under r, and the
// first commit (in the order given; gitinfo.CommitsSince lists newest first)
// that calls for it. It is pure: which commits are in the range is the
// caller's decision.
func Select(commits []gitinfo.Commit, r Rules) Decision {
	var d Decision
	for _, c := range commits {
		msg := Parse(c.Subject, c.Body)
		if l := r.Level(msg); l > d.Level {
			d = Decision{Level: l, Commit: c, Message: msg}
			if l == Major {
				break
			}
		}
	}
	return d
}
