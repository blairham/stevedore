// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package jsonschema generates a JSON Schema (draft-07) from a Go struct,
// keyed by its yaml tags, so editors can validate and autocomplete the config.
package jsonschema

import (
	"reflect"
	"strings"
)

// Generate returns the JSON Schema for the type of v.
func Generate(v any, title string) map[string]any {
	s := schemaFor(reflect.TypeOf(v))
	s["$schema"] = "http://json-schema.org/draft-07/schema#"
	s["title"] = title
	return s
}

// typeKey is JSON Schema's "type" keyword, which every node carries.
const typeKey = "type"

// Schemer is implemented by types whose YAML form is not their Go shape (a
// custom unmarshaler accepting several spellings); its schema is used as is.
type Schemer interface {
	JSONSchema() map[string]any
}

var schemerType = reflect.TypeFor[Schemer]()

func schemaFor(t reflect.Type) map[string]any {
	if t.Implements(schemerType) {
		if s, ok := reflect.Zero(t).Interface().(Schemer); ok {
			return s.JSONSchema()
		}
	}
	switch t.Kind() {
	case reflect.Pointer:
		return schemaFor(t.Elem())
	case reflect.String:
		return map[string]any{typeKey: "string"}
	case reflect.Bool:
		return map[string]any{typeKey: "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{typeKey: "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{typeKey: "number"}
	case reflect.Slice, reflect.Array:
		return map[string]any{typeKey: "array", "items": schemaFor(t.Elem())}
	case reflect.Map:
		return map[string]any{typeKey: "object", "additionalProperties": schemaFor(t.Elem())}
	case reflect.Struct:
		return structSchema(t)
	default:
		return map[string]any{}
	}
}

func structSchema(t reflect.Type) map[string]any {
	props := map[string]any{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := yamlName(f)
		if name == "" || name == "-" {
			continue
		}
		props[name] = without(schemaFor(f.Type), f.Tag.Get("jsonschema"))
	}
	return map[string]any{
		typeKey:                "object",
		"properties":           props,
		"additionalProperties": false,
	}
}

// without applies a field's `jsonschema:"without=a,b"` tag: the named
// properties are dropped from the field's object schema. It is how a field
// reusing a struct (image_defaults, an Image) leaves out what it cannot set.
func without(s map[string]any, tag string) map[string]any {
	names, ok := strings.CutPrefix(tag, "without=")
	if !ok {
		return s
	}
	props, ok := s["properties"].(map[string]any)
	if !ok {
		return s
	}
	trimmed := make(map[string]any, len(props))
	for k, v := range props {
		trimmed[k] = v
	}
	for _, n := range strings.Split(names, ",") {
		delete(trimmed, n)
	}
	out := make(map[string]any, len(s))
	for k, v := range s {
		out[k] = v
	}
	out["properties"] = trimmed
	return out
}

func yamlName(f reflect.StructField) string {
	tag := f.Tag.Get("yaml")
	if tag == "" {
		return strings.ToLower(f.Name)
	}
	return strings.SplitN(tag, ",", 2)[0]
}
