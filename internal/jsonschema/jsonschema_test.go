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
	s := Generate(sample{}, "x")
	props := s["properties"].(map[string]any)
	if props["enabled"].(map[string]any)["type"] != "boolean" {
		t.Error("bool mapping")
	}
	if props["count"].(map[string]any)["type"] != "integer" {
		t.Error("int mapping")
	}
	if props["tags"].(map[string]any)["type"] != "array" {
		t.Error("slice mapping")
	}
	if props["labels"].(map[string]any)["type"] != "object" {
		t.Error("map mapping")
	}
	nested := props["nested"].(map[string]any)
	if nested["type"] != "object" {
		t.Error("nested struct mapping")
	}
}
