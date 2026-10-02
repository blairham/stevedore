// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package jsonschema

import (
	"testing"

	"github.com/blairham/stevedore/internal/config"
)

func TestGenerateConfig(t *testing.T) {
	s := Generate(config.Config{}, "stevedore config")
	if s["$schema"] == nil || s["title"] != "stevedore config" {
		t.Errorf("missing schema header: %v", s["$schema"])
	}
	props, ok := s["properties"].(map[string]any)
	if !ok {
		t.Fatal("top-level properties missing")
	}
	// Keyed by yaml names, not Go field names.
	for _, want := range []string{"project_name", "default_branch", "images", "versioning", "change_detection"} {
		if _, ok := props[want]; !ok {
			t.Errorf("schema missing property %q", want)
		}
	}
	if _, ok := props["ProjectName"]; ok {
		t.Error("schema should use yaml names, not Go field names")
	}

	// images is an array of objects with an id property.
	images, _ := props["images"].(map[string]any)
	if images["type"] != "array" {
		t.Errorf("images should be array: %v", images)
	}
	item, _ := images["items"].(map[string]any)
	itemProps, _ := item["properties"].(map[string]any)
	if _, ok := itemProps["id"]; !ok {
		t.Errorf("image items should have an id property: %v", itemProps)
	}
	if _, ok := itemProps["build_args"]; !ok {
		t.Errorf("image items should have build_args: %v", itemProps)
	}
}

func TestTypeMapping(t *testing.T) {
	type inner struct {
		Name string `yaml:"name"`
	}
	type sample struct {
		Enabled bool              `yaml:"enabled"`
		Count   int               `yaml:"count"`
		Tags    []string          `yaml:"tags"`
		Labels  map[string]string `yaml:"labels"`
		Nested  inner             `yaml:"nested"`
	}
	props := object(t, Generate(sample{}, "x"), "properties")
	for field, want := range map[string]string{
		"enabled": "boolean",
		"count":   "integer",
		"tags":    "array",
		"labels":  "object",
		"nested":  "object",
	} {
		if got := object(t, props, field)["type"]; got != want {
			t.Errorf("%s: type %v, want %s", field, got, want)
		}
	}
}

// object returns m[key] as a JSON object, failing the test if it is not one.
func object(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	v, ok := m[key].(map[string]any)
	if !ok {
		t.Fatalf("%s is %T, want an object", key, m[key])
	}
	return v
}
