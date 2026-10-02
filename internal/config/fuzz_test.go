// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzLoad feeds arbitrary bytes through the same decode, default and
// validate path a user's .stevedore.yaml takes. The config is input stevedore
// does not control, so the property is that a bad one is an error, never a
// panic.
func FuzzLoad(f *testing.F) {
	for _, seed := range []string{
		"",
		"version: 1\n",
		"version: 1\nproject_name: x\nimages:\n  - id: a\n    repositories: [ghcr.io/x/a]\n",
		"version: 1\nversioning:\n  strategy: registry\n",
		"images: {}\n",
		"version: [\n",
	} {
		f.Add([]byte(seed))
	}
	dir := f.TempDir()
	f.Fuzz(func(t *testing.T, data []byte) {
		path := filepath.Join(dir, ".stevedore.yaml")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		c, err := Load(path)
		if err != nil {
			return
		}
		_ = c.Validate()
	})
}
