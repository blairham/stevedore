// Package changelog builds release notes from conventional-commit history.
package changelog

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/gitinfo"
)

// group maps a conventional-commit type to a human-readable changelog heading.
type group struct {
	title string
	types []string
}

var groups = []group{
	{"Features", []string{"feat"}},
	{"Bug Fixes", []string{"fix"}},
	{"Performance", []string{"perf"}},
	{"Refactors", []string{"refactor"}},
	{"Documentation", []string{"docs"}},
	{"Other", nil}, // catch-all
}

var conventional = regexp.MustCompile(`^(\w+)(\([^)]*\))?(!)?:\s*(.+)$`)

// Generate renders a Markdown changelog for commits since the previous tag.
func Generate(cfg config.Changelog, gi *gitinfo.Info, dir string) (string, error) {
	if !cfg.Enabled {
		return "", nil
	}
	commits, err := gitinfo.CommitsSince(dir, gi.PreviousTag)
	if err != nil {
		return "", fmt.Errorf("read commit history: %w", err)
	}

	excludes := make([]*regexp.Regexp, 0, len(cfg.Exclude))
	for _, e := range cfg.Exclude {
		re, err := regexp.Compile(e)
		if err != nil {
			return "", fmt.Errorf("invalid changelog exclude %q: %w", e, err)
		}
		excludes = append(excludes, re)
	}

	buckets := map[string][]string{}
	for _, c := range commits {
		if matchesAny(excludes, c.Subject) {
			continue
		}
		title, line := classify(c)
		buckets[title] = append(buckets[title], line)
	}

	var b strings.Builder
	header := gi.Tag
	if header == "" {
		header = gi.Version
	}
	fmt.Fprintf(&b, "## %s\n\n", header)
	if gi.PreviousTag != "" {
		fmt.Fprintf(&b, "_Changes since %s_\n\n", gi.PreviousTag)
	}

	empty := true
	for _, g := range groups {
		lines := buckets[g.title]
		if len(lines) == 0 {
			continue
		}
		empty = false
		if cfg.Sort == "asc" {
			sort.Strings(lines)
		} else {
			sort.Sort(sort.Reverse(sort.StringSlice(lines)))
		}
		fmt.Fprintf(&b, "### %s\n\n", g.title)
		for _, l := range lines {
			fmt.Fprintf(&b, "- %s\n", l)
		}
		b.WriteString("\n")
	}
	if empty {
		b.WriteString("_No notable changes._\n")
	}
	return b.String(), nil
}

// classify returns the changelog group title and formatted line for a commit.
func classify(c gitinfo.Commit) (string, string) {
	m := conventional.FindStringSubmatch(c.Subject)
	short := c.SHA
	if len(short) > 7 {
		short = short[:7]
	}
	if m == nil {
		return "Other", fmt.Sprintf("%s (%s)", c.Subject, short)
	}
	typ, scope, bang, desc := m[1], m[2], m[3], m[4]
	line := desc
	if scope != "" {
		line = strings.Trim(scope, "()") + ": " + desc
	}
	if bang != "" {
		line = "**BREAKING** " + line
	}
	line = fmt.Sprintf("%s (%s)", line, short)
	for _, g := range groups {
		for _, t := range g.types {
			if t == typ {
				return g.title, line
			}
		}
	}
	return "Other", line
}

func matchesAny(res []*regexp.Regexp, s string) bool {
	for _, re := range res {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}
