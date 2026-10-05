// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/fingerprint"
	"github.com/blairham/stevedore/internal/gitinfo"
	"github.com/blairham/stevedore/internal/summary"
	"github.com/blairham/stevedore/internal/tmpl"
)

// finishHarness returns a release tail with nothing external configured, so
// finishRelease runs end to end without any tool on PATH.
func finishHarness(t *testing.T) (dir string, p *Prepared) {
	t.Helper()
	dir = t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Keep the job summary out of the CI run that executes this test.
	t.Setenv("GITHUB_STEP_SUMMARY", "")
	t.Setenv("GITHUB_OUTPUT", "")
	gi := &gitinfo.Info{Version: "1.0.0"}
	p = &Prepared{
		Config: &config.Config{ProjectName: "proj", Dist: "dist"},
		Git:    gi,
		Ctx:    &tmpl.Context{Version: "1.0.0"},
	}
	return dir, p
}

// A fingerprint hashes inputs only, so whatever run saves it becomes the
// baseline the next --only-changed release compares against. Only a run that
// really published may save it; otherwise the next real release reports
// "inputs unchanged" for images that were never released.
func TestFinishReleaseRecordsFingerprintsOnlyForARealPublish(t *testing.T) {
	cases := []struct {
		name string
		o    Options
		want bool
	}{
		{"release", Options{}, true},
		{"merge", Options{FromDigests: true}, true},
		{"no-push", Options{NoPush: true}, false},
		{"snapshot", Options{Snapshot: true}, false},
		{"split leg", Options{SplitPlatforms: []string{"linux/arm64"}}, false},
		{"dry run", Options{DryRun: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, p := finishHarness(t)
			o := tc.o
			o.Dir = dir
			fpPath := filepath.Join(dir, "dist", "fingerprints.json")
			state := fingerprint.State{"app": "abc123"}
			result := summary.Result{Project: "proj", Images: []summary.Image{{ID: "app", Version: "1.0.0"}}}
			if err := finishRelease(o, p, quietRunner(t), result, fpPath, state, nil); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(fpPath)
			if got := err == nil; got != tc.want {
				t.Errorf("fingerprints.json written = %v, want %v", got, tc.want)
			}
			if !o.DryRun {
				if _, err := os.Stat(filepath.Join(dir, "dist", "release-summary.json")); err != nil {
					t.Errorf("summary not written: %v", err)
				}
			}
		})
	}
}
