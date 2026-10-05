// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A real release from a commit nobody merged publishes real versions of every
// image; the tag and clean-tree guards cannot see it. Only --snapshot and
// --allow-non-default-branch get it through.
func TestReleaseRefusesCommitOffDefaultBranch(t *testing.T) {
	dir := dryRunRepo(t)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("checkout", "-q", "-b", "feat")
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\nLABEL x=2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("commit", "-q", "-am", "feat: unmerged")
	git("tag", "v0.2.0")

	cfg := filepath.Join(dir, ".stevedore.yaml")
	cases := []struct {
		name    string
		o       Options
		refused bool
	}{
		{"release", Options{}, true},
		{"merge", Options{FromDigests: true}, true},
		{"snapshot", Options{Snapshot: true}, false},
		{"allowed", Options{AllowNonDefaultBranch: true}, false},
		{"no-push", Options{NoPush: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := tc.o
			o.Dir, o.ConfigPath, o.DryRun = dir, cfg, true
			_, err := captureStderr(t, func() error { return Release(o) })
			refused := err != nil && strings.Contains(err.Error(), "not on the default branch")
			if refused != tc.refused {
				t.Fatalf("refused = %v, want %v (err: %v)", refused, tc.refused, err)
			}
		})
	}

	_, err := captureStderr(t, func() error { return Publish(Options{Dir: dir, ConfigPath: cfg, DryRun: true}) })
	if err == nil || !strings.Contains(err.Error(), "not on the default branch") {
		t.Errorf("publish: err = %v, want a default-branch refusal", err)
	}
}
