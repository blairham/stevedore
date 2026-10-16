// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package conventional

import (
	"testing"

	"github.com/blairham/stevedore/internal/gitinfo"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name, subject, body string
		want                Message
	}{
		{"feat", "feat: add signing", "", Message{Type: "feat", Description: "add signing"}},
		{"scope", "fix(builder): multi-arch", "", Message{Type: "fix", Scope: "builder", Description: "multi-arch"}},
		{"bang", "feat!: drop v0 config", "", Message{Type: "feat", Description: "drop v0 config", Breaking: true}},
		{
			"scope bang",
			"feat(config)!: rename",
			"",
			Message{Type: "feat", Scope: "config", Description: "rename", Breaking: true},
		},
		{"type is case-insensitive", "Feat(API): x", "", Message{Type: "feat", Scope: "API", Description: "x"}},
		{"upper type", "FIX: y", "", Message{Type: "fix", Description: "y"}},
		{
			"footer",
			"feat: new api",
			"Details.\n\nBREAKING CHANGE: old api gone",
			Message{Type: "feat", Description: "new api", Breaking: true},
		},
		{"hyphen footer", "fix: x", "BREAKING-CHANGE: y", Message{Type: "fix", Description: "x", Breaking: true}},
		{"footer on plain subject", "rework", "BREAKING CHANGE: z", Message{Description: "rework", Breaking: true}},
		{"mention is not a footer", "fix: x", "not a BREAKING CHANGE: really", Message{Type: "fix", Description: "x"}},
		{"lowercase footer is not a footer", "fix: x", "breaking change: y", Message{Type: "fix", Description: "x"}},
		{"plain", "just a plain message", "", Message{Description: "just a plain message"}},
		{"no space after colon", "docs:typo", "", Message{Type: "docs", Description: "typo"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Parse(tc.subject, tc.body); got != tc.want {
				t.Errorf("Parse(%q, %q) = %+v, want %+v", tc.subject, tc.body, got, tc.want)
			}
		})
	}
}

func TestRulesLevel(t *testing.T) {
	r := DefaultRules()
	cases := []struct {
		subject, body string
		want          Level
	}{
		{"feat: x", "", Minor},
		{"Feat: x", "", Minor},
		{"FEAT(api): x", "", Minor},
		{"fix: x", "", Patch},
		{"Fix: x", "", Patch},
		{"perf: x", "", Patch},
		{"docs: x", "", None},
		{"chore: x", "", None},
		{"refactor: x", "", None},
		{"revert: x", "", None},
		{"plain subject", "", None},
		{"feat!: x", "", Major},
		{"chore!: drop node 18", "", Major},
		{"fix: x", "BREAKING CHANGE: y", Major},
		{"docs: x", "BREAKING-CHANGE: y", Major},
		{"plain subject", "BREAKING CHANGE: y", Major},
	}
	for _, tc := range cases {
		if got := r.Level(Parse(tc.subject, tc.body)); got != tc.want {
			t.Errorf("Level(%q, %q) = %s, want %s", tc.subject, tc.body, got, tc.want)
		}
	}
}

func TestRulesWith(t *testing.T) {
	r := DefaultRules().With(map[string]Level{"Revert": Patch, "perf": Minor, "fix": None})
	for typ, want := range map[string]Level{"feat": Minor, "fix": None, "perf": Minor, "revert": Patch, "docs": None} {
		if got := r.Level(Message{Type: typ}); got != want {
			t.Errorf("%s = %s, want %s", typ, got, want)
		}
	}
	// A breaking commit is major whatever the mapping says about its type.
	if got := r.Level(Message{Type: "fix", Breaking: true}); got != Major {
		t.Errorf("breaking fix under fix: none = %s, want major", got)
	}
	// With never mutates its receiver.
	if d := DefaultRules(); d["fix"] != Patch || len(d) != 3 {
		t.Errorf("DefaultRules mutated: %v", d)
	}
}

func TestSelect(t *testing.T) {
	c := func(sha, subject, body string) gitinfo.Commit {
		return gitinfo.Commit{SHA: sha, Subject: subject, Body: body}
	}
	cases := []struct {
		name    string
		commits []gitinfo.Commit
		level   Level
		sha     string
	}{
		{"no commits", nil, None, ""},
		{
			"nothing bump-worthy",
			[]gitinfo.Commit{c("a", "docs: x", ""), c("b", "chore: y", ""), c("c", "plain", "")},
			None,
			"",
		},
		{"fix only", []gitinfo.Commit{c("a", "docs: x", ""), c("b", "fix: y", "")}, Patch, "b"},
		{
			"feat beats fix",
			[]gitinfo.Commit{c("a", "fix: x", ""), c("b", "feat(api): y", ""), c("c", "fix: z", "")},
			Minor,
			"b",
		},
		{"first at the level decides", []gitinfo.Commit{c("a", "feat: x", ""), c("b", "feat: y", "")}, Minor, "a"},
		{"breaking via bang", []gitinfo.Commit{c("a", "feat: x", ""), c("b", "fix!: y", "")}, Major, "b"},
		{"breaking via footer", []gitinfo.Commit{c("a", "feat: x", ""), c("b", "fix: y", "BREAKING CHANGE: z")}, Major, "b"},
		{"case-insensitive", []gitinfo.Commit{c("a", "Fix: x", ""), c("b", "FEAT: y", "")}, Minor, "b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := Select(tc.commits, DefaultRules())
			if d.Level != tc.level || d.Commit.SHA != tc.sha {
				t.Errorf("Select = %s at %q, want %s at %q", d.Level, d.Commit.SHA, tc.level, tc.sha)
			}
		})
	}
}

func TestParseLevel(t *testing.T) {
	for s, want := range map[string]Level{"none": None, "patch": Patch, "Minor": Minor, " major ": Major} {
		if got, ok := ParseLevel(s); !ok || got != want {
			t.Errorf("ParseLevel(%q) = %s, %v; want %s", s, got, ok, want)
		}
		if want.String() != map[Level]string{None: "none", Patch: "patch", Minor: "minor", Major: "major"}[want] {
			t.Errorf("%d.String() = %q", want, want.String())
		}
	}
	if _, ok := ParseLevel("auto"); ok {
		t.Error("ParseLevel(auto) accepted")
	}
}
