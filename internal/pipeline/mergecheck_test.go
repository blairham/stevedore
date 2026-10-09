// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blairham/stevedore/internal/config"
)

// A leg records the version it resolved beside its digest, and the digest
// reader does not mistake that file for a platform.
func TestBuildSplitLeg_RecordsVersion(t *testing.T) {
	dir, _, o, p, grp := gateHarness(t)
	grp[0].plan.Version = "1.4.0"
	o.SplitPlatforms = []string{"linux/amd64"}

	if _, _, err := buildGroup(o, p, quietRunner(t), grp); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(splitDigestDir(dir, "dist", "app"), splitVersionFile))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(data)); got != "1.4.0" {
		t.Errorf("recorded version = %q, want 1.4.0", got)
	}
	digests, covered, err := readSplitDigests(dir, "dist", "app", []string{"linux/amd64"})
	if err != nil {
		t.Fatalf("the version file broke the digest reader: %v", err)
	}
	if len(digests) != 1 || !covered["linux-amd64"] {
		t.Errorf("digests = %v covered = %v, want the one amd64 digest", digests, covered)
	}
}

func TestCheckMergeInputs(t *testing.T) {
	eval := func(id, version, reason string) imageEval {
		return imageEval{plan: ImagePlan{Image: config.Image{ID: id}, Version: version}, reason: reason}
	}
	leg := func(t *testing.T, dir, id, version string) {
		t.Helper()
		if err := writeSplitDigest(dir, "dist", []string{id}, []string{"linux/amd64"}, "sha256:aaa"); err != nil {
			t.Fatal(err)
		}
		if version != "" {
			if err := writeSplitVersions(dir, "dist", []imageEval{eval(id, version, "")}); err != nil {
				t.Fatal(err)
			}
		}
	}
	cases := []struct {
		name    string
		legs    map[string]string // id → version the leg recorded ("" = none)
		only    []string
		toBuild []imageEval
		skipped []imageEval
		want    []string // substrings of the error; nil = no error
	}{
		{
			name:    "versions agree",
			legs:    map[string]string{"a": "1.0.0"},
			toBuild: []imageEval{eval("a", "1.0.0", "")},
		},
		{
			name:    "merge resolved another version",
			legs:    map[string]string{"a": "1.0.0"},
			toBuild: []imageEval{eval("a", "1.0.1", "")},
			want:    []string{"image a", "built version 1.0.0", "merge resolved 1.0.1", "--pin-version"},
		},
		{
			name:    "a leg that recorded no version is not checked",
			legs:    map[string]string{"a": ""},
			toBuild: []imageEval{eval("a", "1.0.1", "")},
		},
		{
			name:    "merge skipped an image the legs built",
			legs:    map[string]string{"a": "1.0.0", "b": "2.0.0"},
			toBuild: []imageEval{eval("a", "1.0.0", "")},
			skipped: []imageEval{eval("b", "2.0.0", "unchanged since its release marker")},
			want:    []string{"image b", "merge skipped it: unchanged since its release marker", "--only"},
		},
		{
			name:    "under --only the set is the caller's",
			legs:    map[string]string{"a": "1.0.0", "b": "2.0.0"},
			only:    []string{"a"},
			toBuild: []imageEval{eval("a", "1.0.0", "")},
			skipped: []imageEval{eval("b", "2.0.0", "not selected")},
		},
		{
			name:    "a skipped image no leg built",
			legs:    map[string]string{"a": "1.0.0"},
			toBuild: []imageEval{eval("a", "1.0.0", "")},
			skipped: []imageEval{eval("b", "2.0.0", "unchanged")},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for id, v := range tc.legs {
				leg(t, dir, id, v)
			}
			var toBuild [][]imageEval
			for _, m := range tc.toBuild {
				toBuild = append(toBuild, []imageEval{m})
			}
			err := checkMergeInputs(Options{Dir: dir, Only: tc.only}, "dist", toBuild, tc.skipped)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("want no error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want an error containing %q, got none", tc.want)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q lacks %q", err, w)
				}
			}
		})
	}
}

// End to end: the issue's scenario. The legs built app, then its release
// marker moved before merge ran. A merge without --only re-runs change
// detection, decides app is unchanged, and must refuse rather than leave the
// legs' digests unreleased; given the plan's --only and pins it proceeds, and
// a pin that disagrees with the legs' recorded version is refused.
func TestMergeRefusesWhatTheLegsDidNotBuild(t *testing.T) {
	dir := dryRunRepo(t)
	// dist/ is ignored, as in a real repository: the downloaded digests must
	// not read as a dirty tree.
	if err := os.WriteFile(filepath.Join(dir, ".git", "info", "exclude"), []byte("dist/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, platform := range []string{"linux/amd64", "linux/arm64"} {
		if err := writeSplitDigest(dir, "dist", []string{"app"}, []string{platform}, "sha256:"+platform[6:]); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeSplitVersions(
		dir,
		"dist",
		[]imageEval{{plan: ImagePlan{Image: config.Image{ID: "app"}, Version: "0.1.0"}}},
	); err != nil {
		t.Fatal(err)
	}
	// The marker moves to HEAD: app now reads as unchanged.
	cmd := exec.Command("git", "push", "-q", "-f", "origin", "HEAD:refs/releases/image/app")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("move marker: %v\n%s", err, out)
	}
	base := Options{Dir: dir, ConfigPath: filepath.Join(dir, ".stevedore.yaml"), DryRun: true}

	stderr, err := captureStderr(t, func() error { return Merge(base) })
	if err == nil || !strings.Contains(err.Error(), "merge skipped it") {
		t.Fatalf("merge without --only: want a refusal naming the skipped image, got %v\n%s", err, stderr)
	}

	withPlan := base
	withPlan.Only = []string{"app"}
	withPlan.PinVersions = map[string]string{"app": "0.1.0"}
	stderr, err = captureStderr(t, func() error { return Merge(withPlan) })
	if err != nil {
		t.Fatalf("merge with the plan's only/pins: %v\n%s", err, stderr)
	}

	wrongPin := withPlan
	wrongPin.PinVersions = map[string]string{"app": "0.2.0"}
	_, err = captureStderr(t, func() error { return Merge(wrongPin) })
	if err == nil || !strings.Contains(err.Error(), "built version 0.1.0, but merge resolved 0.2.0") {
		t.Fatalf("merge pinned to another version: want a version refusal, got %v", err)
	}
}
