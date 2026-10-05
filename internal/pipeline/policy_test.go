// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/blairham/stevedore/internal/config"
)

// The skip flags turned a red pipeline green and shipped an unscanned image
// under a real version. Under policy.require a real release refuses them, and
// refuses the same stage turned off in the config; a snapshot, a --no-push
// validation and a split leg are not real releases and pass.
func TestReleaseEnforcesPolicyRequire(t *testing.T) {
	dir := dryRunRepo(t)
	cfg := filepath.Join(dir, ".stevedore.yaml")
	cases := []struct {
		name string
		o    Options
		want string // "" = not refused by policy
	}{
		{"skip-scan", Options{SkipScan: true}, "scan is required on a real release, but --skip-scan was passed"},
		{"skip-test", Options{SkipTest: true}, "test is required"},
		{"skip-sign", Options{SkipSign: true}, "sign is required"},
		{"skip-sbom", Options{SkipSBOM: true}, "sbom is required"},
		{"merge", Options{FromDigests: true, SkipScan: true}, "scan is required"},
		{"snapshot", Options{Snapshot: true, SkipScan: true}, ""},
		{"no-push", Options{NoPush: true, SkipScan: true}, ""},
		{"split leg", Options{SplitPlatforms: []string{"linux/amd64"}, SkipScan: true}, ""},
		{"nothing skipped", Options{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := tc.o
			o.Dir, o.ConfigPath, o.DryRun = dir, cfg, true
			_, err := captureStderr(t, func() error { return Release(o) })
			got := ""
			if err != nil && strings.Contains(err.Error(), "policy.require") {
				got = err.Error()
			}
			if tc.want == "" && got != "" {
				t.Fatalf("refused by policy: %v", got)
			}
			if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Fatalf("err = %v, want a policy refusal containing %q", err, tc.want)
			}
		})
	}
}

func TestEnforcePolicyDisabledStage(t *testing.T) {
	cfg := &config.Config{Policy: config.Policy{Require: []string{"scan", "sign"}}}
	cfg.Sign.Cosign.Enabled = true
	err := enforcePolicy(Options{}, cfg)
	if err == nil || !strings.Contains(err.Error(), "scan is required on a real release, but scan.enabled is false") {
		t.Fatalf("err = %v, want a refusal for the disabled scan stage", err)
	}
	if strings.Contains(err.Error(), "sign is required") {
		t.Errorf("sign is enabled and not skipped, yet refused: %v", err)
	}
	// Not required: a disabled stage is allowed, and only reported.
	cfg.Policy.Require = nil
	if err := enforcePolicy(Options{}, cfg); err != nil {
		t.Errorf("no policy: %v", err)
	}
	if got := degradedStages(Options{SkipSign: true}, cfg); !slices.Equal(got, []string{"scan", "test", "sign", "sbom"}) {
		t.Errorf("degradedStages = %v, want every stage off", got)
	}
	if got := degradedStages(Options{Snapshot: true}, cfg); got != nil {
		t.Errorf("snapshot degradedStages = %v, want none", got)
	}
}
