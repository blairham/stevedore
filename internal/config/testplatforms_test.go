// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package config

import "testing"

func TestValidateTestPlatforms(t *testing.T) {
	for mode, ok := range map[string]bool{"": true, TestPlatformsNative: true, TestPlatformsAll: true, "emulated": false} {
		cfg := Config{Version: 1, Images: []Image{{ID: "a", Repositories: []string{"r1"}}}}
		cfg.Test.Platforms = mode
		if err := cfg.Validate(); (err == nil) != ok {
			t.Errorf("test.platforms %q: err = %v", mode, err)
		}
	}
}
