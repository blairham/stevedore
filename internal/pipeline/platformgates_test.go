// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/summary"
)

// A multi-arch image is gated per platform: every variant is scanned, and the
// smoke test runs on the variants the docker host can run, with the others
// reported as skipped rather than counted as tested (#69).
func TestGatesCoverEveryPlatform(t *testing.T) {
	dir, log, o, p, grp := gateHarness(t)
	imgs, _, err := buildGroup(o, p, quietRunner(t), grp)
	if err != nil {
		t.Fatal(err)
	}
	calls := readCalls(t, log)
	for _, plat := range []string{"linux/amd64", "linux/arm64"} {
		if indexOf(calls, "grype ghcr.io/x/app@sha256:built", "--platform "+plat) < 0 {
			t.Errorf("%s was not scanned:\n%s", plat, strings.Join(calls, "\n"))
		}
		name := "scan-app-" + strings.ReplaceAll(plat, "/", "-") + ".json"
		if _, err := os.Stat(filepath.Join(dir, "dist", name)); err != nil {
			t.Errorf("per-platform report %s missing: %v", name, err)
		}
	}
	if indexOf(calls, "docker run --rm --name stevedore-test-", " --platform linux/amd64 ") < 0 {
		t.Errorf("native platform not smoke tested:\n%s", strings.Join(calls, "\n"))
	}
	if indexOf(calls, "docker run --rm --name stevedore-test-", " --platform linux/arm64 ") >= 0 {
		t.Errorf("non-native platform run without test.platforms: all:\n%s", strings.Join(calls, "\n"))
	}

	img := imgs[0]
	if img.Tested {
		t.Error("an image with an untested platform must not be reported as tested")
	}
	if !img.Scanned {
		t.Error("a scanned image must be reported as scanned, so a clean one reads clean (#66)")
	}
	want := map[string]summary.Platform{
		"linux/amd64": {Platform: "linux/amd64", Scanned: true, Tested: true},
		"linux/arm64": {Platform: "linux/arm64", Scanned: true},
	}
	if len(img.Platforms) != 2 {
		t.Fatalf("platforms = %+v", img.Platforms)
	}
	for _, got := range img.Platforms {
		w := want[got.Platform]
		if got.Scanned != w.Scanned || got.Tested != w.Tested {
			t.Errorf("%s = %+v, want scanned=%v tested=%v", got.Platform, got, w.Scanned, w.Tested)
		}
		if (got.TestSkipped != "") == w.Tested {
			t.Errorf("%s test_skipped = %q", got.Platform, got.TestSkipped)
		}
	}
}

// test.platforms: all runs a non-native platform when the host has an
// emulator for it, and every platform tested means the image is tested.
func TestSmokeTestAllUsesEmulation(t *testing.T) {
	_, log, o, p, grp := gateHarness(t)
	p.Config.Test.Platforms = config.TestPlatformsAll
	t.Setenv("FAKE_EMULATES", "linux/amd64*, linux/arm64, linux/386")
	imgs, _, err := buildGroup(o, p, quietRunner(t), grp)
	if err != nil {
		t.Fatal(err)
	}
	calls := readCalls(t, log)
	for _, plat := range []string{"linux/amd64", "linux/arm64"} {
		if indexOf(calls, "docker run --rm --name stevedore-test-", " --platform "+plat+" ") < 0 {
			t.Errorf("%s not smoke tested:\n%s", plat, strings.Join(calls, "\n"))
		}
	}
	if !imgs[0].Tested {
		t.Errorf("every platform tested, yet Tested=false: %+v", imgs[0].Platforms)
	}
}

// A finding in only the non-host variant still fails the gate, and nothing is
// signed or tagged.
func TestScanGateFailsOnAnyPlatform(t *testing.T) {
	_, log, o, p, grp := gateHarness(t)
	t.Setenv("FAKE_SCAN_CRITICAL_ON", "linux/arm64")
	_, _, err := buildGroup(o, p, quietRunner(t), grp)
	if err == nil || !strings.Contains(err.Error(), "linux/arm64") {
		t.Fatalf("want a gate failure naming linux/arm64, got %v", err)
	}
	calls := readCalls(t, log)
	if len(taggingCalls(calls)) > 0 || indexOf(calls, "cosign") >= 0 {
		t.Errorf("a failed arm64 scan still signed or tagged:\n%s", strings.Join(calls, "\n"))
	}
}
