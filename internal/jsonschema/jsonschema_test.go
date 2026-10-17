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

	// image_defaults is an image without an id — and leaves images' id alone.
	defaults, _ := props["image_defaults"].(map[string]any)
	defProps, _ := defaults["properties"].(map[string]any)
	if _, ok := defProps["build_args"]; !ok {
		t.Errorf("image_defaults should have build_args: %v", defProps)
	}
	if _, ok := defProps["id"]; ok {
		t.Error("image_defaults must not offer id")
	}
	if _, ok := itemProps["id"]; !ok {
		t.Error("dropping id from image_defaults dropped it from images too")
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

// scan.ignore entries accept a bare ID or a mapping, so the schema must offer
// both; the struct-derived object schema alone would flag the original form.
func TestScanIgnoreSchemaAcceptsBothForms(t *testing.T) {
	props := object(t, Generate(config.Config{}, "x"), "properties")
	scan, _ := props["scan"].(map[string]any)
	item := object(t, object(t, object(t, scan, "properties"), "ignore"), "items")
	alts, _ := item["oneOf"].([]any)
	if len(alts) != 2 {
		t.Fatalf("ignore items = %v, want oneOf string|object", item)
	}
	first, _ := alts[0].(map[string]any)
	second, _ := alts[1].(map[string]any)
	if first["type"] != "string" || second["type"] != "object" {
		t.Errorf("oneOf = %v", alts)
	}
}

// Fields with a fixed set of values carry it as an enum, so an editor flags
// `scanner: foo` instead of accepting any string.
func TestEnumsReachTheSchema(t *testing.T) {
	scan := object(t, object(t, Generate(config.Config{}, "x"), "properties"), "scan")
	scanner := object(t, object(t, scan, "properties"), "scanner")
	enum, _ := scanner["enum"].([]any)
	if len(enum) != 2 || enum[0] != "grype" || enum[1] != "trivy" {
		t.Errorf("scan.scanner enum = %v, want [grype trivy]", scanner["enum"])
	}
}

// versioning.bump offers auto, and bump_types' values are the levels a type
// may map to.
func TestVersioningBumpSchema(t *testing.T) {
	v := object(t, object(t, object(t, Generate(config.Config{}, "x"), "properties"), "versioning"), "properties")
	enum, _ := object(t, v, "bump")["enum"].([]any)
	if len(enum) != 4 || enum[3] != "auto" {
		t.Errorf("versioning.bump enum = %v, want patch, minor, major, auto", enum)
	}
	levels, _ := object(t, object(t, v, "bump_types"), "additionalProperties")["enum"].([]any)
	if len(levels) != 4 || levels[0] != "none" {
		t.Errorf("versioning.bump_types levels = %v, want none, patch, minor, major", levels)
	}
	if enum, _ := object(t, v, "on_no_changes")["enum"].([]any); len(enum) != 2 {
		t.Errorf("versioning.on_no_changes enum = %v, want skip, patch", enum)
	}
	if enum, _ := object(t, v, "pre_major")["enum"].([]any); len(enum) != 2 || enum[0] != "major" {
		t.Errorf("versioning.pre_major enum = %v, want major, minor", enum)
	}
	if typ := object(t, v, "prerelease")["type"]; typ != "string" {
		t.Errorf("versioning.prerelease type = %v, want string", typ)
	}
}
