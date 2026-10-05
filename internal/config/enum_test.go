// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"reflect"
	"strings"
	"testing"
)

// The `enum` tags feed the JSON schema an editor validates against, so they
// must be exactly what Validate accepts: every listed value passes, and a value
// outside the list fails. A schema that is stricter than the code rejects
// working configs in the editor; a looser one is the drift the tags fix.
func TestEnumTagsMatchValidation(t *testing.T) {
	base := func() *Config {
		c := &Config{Version: 1, Images: []Image{{ID: "a", Repositories: []string{"r/a"}}}}
		c.Versioning = Versioning{Value: "1.0.0", Env: "V", Command: "echo 1"}
		return c
	}
	cases := []struct {
		owner any
		field string
		set   func(*Config, string)
	}{
		{Provenance{}, "Mode", func(c *Config, v string) { c.Provenance = Provenance{Enabled: true, Mode: v} }},
		{Versioning{}, "Strategy", func(c *Config, v string) { c.Versioning.Strategy = v }},
		{Versioning{}, "Bump", func(c *Config, v string) { c.Versioning.Strategy, c.Versioning.Bump = "registry", v }},
		{Versioning{}, "Lister", func(c *Config, v string) { c.Versioning.Strategy, c.Versioning.Lister = "registry", v }},
		{Test{}, "Platforms", func(c *Config, v string) { c.Test.Platforms = v }},
		{Scan{}, "Scanner", func(c *Config, v string) { c.Scan = Scan{Enabled: true, Scanner: v, FailOn: "high"} }},
		{Scan{}, "FailOn", func(c *Config, v string) { c.Scan = Scan{Enabled: true, Scanner: "grype", FailOn: v} }},
		{Cache{}, "Type", func(c *Config, v string) {
			c.Cache = Cache{Type: v}
			if v == CacheRegistry || v == CacheLocal {
				c.Cache.Ref = "x"
			}
		}},
		{Cache{}, "Mode", func(c *Config, v string) { c.Cache = Cache{Type: CacheGHA, Mode: v} }},
	}
	for _, tc := range cases {
		f, ok := reflect.TypeOf(tc.owner).FieldByName(tc.field)
		if !ok {
			t.Fatalf("%T has no field %s", tc.owner, tc.field)
		}
		enum := f.Tag.Get("enum")
		if enum == "" {
			t.Errorf("%T.%s has no enum tag", tc.owner, tc.field)
			continue
		}
		for v := range strings.SplitSeq(enum, ",") {
			c := base()
			tc.set(c, v)
			if err := c.Validate(); err != nil {
				t.Errorf("%T.%s: enum value %q fails validation: %v", tc.owner, tc.field, v, err)
			}
		}
		c := base()
		tc.set(c, "bogus")
		if err := c.Validate(); err == nil {
			t.Errorf("%T.%s: a value outside the enum validated", tc.owner, tc.field)
		}
	}
}
