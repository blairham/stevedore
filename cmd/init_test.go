// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestServiceMappingDefaults(t *testing.T) {
	m, err := serviceMapping(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "name" || m.Repositories != "image" || m.Paths != "sourcePaths" {
		t.Errorf("defaults = %+v", m)
	}
	if m.BuildArgs["PROJECT"] != "project" {
		t.Errorf("default build args = %v", m.BuildArgs)
	}
}

func TestServiceMappingOverrides(t *testing.T) {
	m, err := serviceMapping(
		[]string{"id=service", "paths=source_paths", "dockerfile=build.dockerfile"},
		[]string{"BUILD_PROJECT=build.project"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "service" || m.Paths != "source_paths" || m.Dockerfile != "build.dockerfile" {
		t.Errorf("mapping = %+v", m)
	}
	// A --map-build-arg replaces the default PROJECT mapping outright.
	if len(m.BuildArgs) != 1 || m.BuildArgs["BUILD_PROJECT"] != "build.project" {
		t.Errorf("build args = %v", m.BuildArgs)
	}
	// Unmapped fields keep their defaults.
	if m.Repositories != "image" {
		t.Errorf("repositories = %q", m.Repositories)
	}
}

func TestServiceMappingErrors(t *testing.T) {
	if _, err := serviceMapping([]string{"nonsense"}, nil); err == nil {
		t.Error("malformed --map should error")
	}
	if _, err := serviceMapping([]string{"bogus=field"}, nil); err == nil {
		t.Error("unknown --map field should error")
	}
	if _, err := serviceMapping(nil, []string{"NOVALUE"}); err == nil {
		t.Error("malformed --map-build-arg should error")
	}
}

func TestIgnoreDist(t *testing.T) {
	for _, tc := range []struct {
		name, existing string // existing "-" means no .gitignore
		wantAdded      bool
		want           string
	}{
		{"no gitignore", "-", true, "dist/\n"},
		{"unrelated entries", "node_modules/\n", true, "node_modules/\ndist/\n"},
		{"no trailing newline", "*.log", true, "*.log\ndist/\n"},
		{"already ignored", "/dist\n", false, "/dist\n"},
		{"already ignored, spaced", "  dist/  \n", false, "  dist/  \n"},
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, ".gitignore")
		if tc.existing != "-" {
			if err := os.WriteFile(path, []byte(tc.existing), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		added, err := ignoreDist(dir)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if added != tc.wantAdded || string(got) != tc.want {
			t.Errorf("%s: added=%v, .gitignore=%q; want added=%v, %q", tc.name, added, got, tc.wantAdded, tc.want)
		}
	}
}
