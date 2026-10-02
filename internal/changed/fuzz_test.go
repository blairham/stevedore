// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package changed

import "testing"

// FuzzMatch drives the change-detection globs with arbitrary patterns and
// paths. Both come from user config and git output, so a malformed pattern
// has to be a non-match, never a panic.
func FuzzMatch(f *testing.F) {
	for _, seed := range [][2]string{
		{"services/api/**", "services/api/main.go"},
		{"**/*.go", "./cmd/root.go"},
		{"[", "x"},
		{"{a,b}/*", "a/c"},
		{"", ""},
	} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, pattern, path string) {
		_ = Match([]string{pattern}, path)
		_ = Evaluate([]string{pattern}, nil, []string{path})
	})
}
