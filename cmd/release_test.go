// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"runtime/debug"
	"testing"
)

func TestParsePins(t *testing.T) {
	pins, err := parsePins([]string{"checkout=0.0.400", "billing=1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	if pins["checkout"] != "0.0.400" || pins["billing"] != "1.2.3" {
		t.Errorf("pins = %v", pins)
	}
	if got, err := parsePins(nil); err != nil || len(got) != 0 {
		t.Errorf("no flags should yield no pins, got %v, %v", got, err)
	}
	for _, bad := range []string{"checkout", "=1.0.0", "checkout="} {
		if _, err := parsePins([]string{bad}); err == nil {
			t.Errorf("parsePins(%q) should error", bad)
		}
	}
}

func TestResolveVersion(t *testing.T) {
	stamped := func(v string) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) {
			return &debug.BuildInfo{Main: debug.Module{Version: v}}, true
		}
	}
	none := func() (*debug.BuildInfo, bool) { return nil, false }
	for _, tc := range []struct {
		name, ldflags string
		info          func() (*debug.BuildInfo, bool)
		want          string
	}{
		{"ldflags win", "1.2.3", stamped("v9.9.9"), "1.2.3"},
		{"go install", "dev", stamped("v1.0.4"), "1.0.4"},
		{"local checkout", "dev", stamped("(devel)"), "dev"},
		{"no build info", "dev", none, "dev"},
		{"empty ldflags", "", stamped("v1.0.4"), "1.0.4"},
	} {
		if got := resolveVersion(tc.ldflags, tc.info); got != tc.want {
			t.Errorf("%s: resolveVersion(%q) = %q, want %q", tc.name, tc.ldflags, got, tc.want)
		}
	}
}
