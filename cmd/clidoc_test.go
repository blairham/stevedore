// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// docs/cli.md's flag reference has a section per command; every flag a
// command defines must appear in that command's own section, so a new flag
// cannot ship undocumented. Matching within the section, not anywhere in the
// file, keeps a flag that two commands share (--only) from hiding a gap.
func TestCLIDocListsEveryFlag(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "docs", "cli.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	ref := doc[strings.Index(doc, "## Flag reference"):]
	for _, c := range newRootCmd().Commands() {
		if c.Hidden || c.Name() == "help" || c.Name() == "completion" {
			continue
		}
		head := "### `stevedore " + c.Name() + "`"
		i := strings.Index(ref, head)
		if i < 0 {
			t.Errorf("docs/cli.md flag reference has no section for %s", c.Name())
			continue
		}
		sec := ref[i+len(head):]
		if j := strings.Index(sec, "\n### "); j >= 0 {
			sec = sec[:j]
		}
		c.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if f.Name == "help" {
				return
			}
			if !strings.Contains(sec, "`--"+f.Name+"`") && !strings.Contains(sec, "`--"+f.Name+" <") &&
				!strings.Contains(sec, ", --"+f.Name) {
				t.Errorf("%s --%s is not in its docs/cli.md section", c.Name(), f.Name)
			}
		})
	}
}
