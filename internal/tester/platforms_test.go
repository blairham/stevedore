// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tester

import (
	"slices"
	"testing"

	"github.com/blairham/stevedore/internal/config"
)

func TestParseBuilderPlatforms(t *testing.T) {
	out := `Name:          multi
Driver:        docker-container
Nodes:
Name:      multi0
Platforms: linux/amd64*, linux/amd64/v2, linux/386
Name:      multi1
Platforms: linux/arm64, linux/arm/v7
`
	got := parseBuilderPlatforms(out)
	want := []string{"linux/amd64", "linux/amd64/v2", "linux/386", "linux/arm64", "linux/arm/v7"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestPlan(t *testing.T) {
	amd := Host{Native: "linux/amd64", Emulates: []string{"linux/amd64", "linux/386"}}
	amdQEMU := Host{Native: "linux/amd64", Emulates: []string{"linux/amd64", "linux/arm64", "linux/arm/v7"}}
	both := []string{"linux/amd64", "linux/arm64"}
	type want struct {
		run      bool
		emulated bool
	}
	cases := []struct {
		name  string
		host  Host
		mode  string
		plats []string
		want  []want
	}{
		{"native skips arm64", amd, "", both, []want{{run: true}, {}}},
		{"native ignores an emulator", amdQEMU, config.TestPlatformsNative, both, []want{{run: true}, {}}},
		{"all without emulator skips", amd, config.TestPlatformsAll, both, []want{{run: true}, {}}},
		{"all with emulator runs", amdQEMU, config.TestPlatformsAll, both, []want{{run: true}, {run: true, emulated: true}}},
		{"variant matches its arch", amdQEMU, config.TestPlatformsAll, []string{"linux/arm/v6"}, []want{{run: true, emulated: true}}},
		{"nothing native falls back to emulation", amdQEMU, "", []string{"linux/arm64"}, []want{{run: true, emulated: true}}},
		{"nothing native, no emulator", amd, "", []string{"linux/arm64"}, []want{{}}},
		{"native variant", Host{Native: "linux/arm64"}, "", []string{"linux/arm64/v8"}, []want{{run: true}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Plan(tc.host, tc.mode, tc.plats)
			if len(got) != len(tc.want) {
				t.Fatalf("got %+v", got)
			}
			for i, w := range tc.want {
				if (got[i].Skip == "") != w.run || got[i].Emulated != w.emulated {
					t.Errorf("%s: got %+v, want run=%v emulated=%v", got[i].Platform, got[i], w.run, w.emulated)
				}
			}
		})
	}
}
