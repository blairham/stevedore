package cmd

import "testing"

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
