package changelog

import (
	"regexp"
	"testing"

	"github.com/blairham/stevedore/internal/gitinfo"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		subject   string
		sha       string
		wantGroup string
		wantLine  string
	}{
		{"feat: add signing", "abc1234def", "Features", "add signing (abc1234)"},
		{"fix: correct digest parsing", "0000000", "Bug Fixes", "correct digest parsing (0000000)"},
		{"perf: faster builds", "1111111", "Performance", "faster builds (1111111)"},
		{"refactor: split pipeline", "2222222", "Refactors", "split pipeline (2222222)"},
		{"docs: update readme", "3333333", "Documentation", "update readme (3333333)"},
		{"feat(builder): multi-arch", "4444444", "Features", "builder: multi-arch (4444444)"},
		{"feat!: drop v0 config", "5555555", "Features", "**BREAKING** drop v0 config (5555555)"},
		{"feat(config)!: rename field", "6666666", "Features", "**BREAKING** config: rename field (6666666)"},
		{"just a plain message", "7777777", "Other", "just a plain message (7777777)"},
		{"chore: bump deps", "8888888", "Other", "bump deps (8888888)"},
	}
	for _, tc := range cases {
		t.Run(tc.subject, func(t *testing.T) {
			gotGroup, gotLine := classify(gitinfo.Commit{SHA: tc.sha, Subject: tc.subject})
			if gotGroup != tc.wantGroup {
				t.Errorf("group = %q, want %q", gotGroup, tc.wantGroup)
			}
			if gotLine != tc.wantLine {
				t.Errorf("line = %q, want %q", gotLine, tc.wantLine)
			}
		})
	}
}

func TestClassifyShortSHA(t *testing.T) {
	// A SHA shorter than 7 chars should not panic or be truncated.
	_, line := classify(gitinfo.Commit{SHA: "abc", Subject: "feat: x"})
	if line != "x (abc)" {
		t.Errorf("line = %q, want %q", line, "x (abc)")
	}
}

func TestMatchesAny(t *testing.T) {
	res := []*regexp.Regexp{
		regexp.MustCompile("^chore:"),
		regexp.MustCompile("^docs:"),
	}
	if !matchesAny(res, "chore: bump") {
		t.Error("should match chore:")
	}
	if matchesAny(res, "feat: new") {
		t.Error("should not match feat:")
	}
	if matchesAny(nil, "anything") {
		t.Error("empty rules should never match")
	}
}
