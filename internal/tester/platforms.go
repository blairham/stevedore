// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tester

import (
	"fmt"
	"strings"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/run"
)

// Target is one platform of the image and whether the smoke test runs on it.
type Target struct {
	Platform string
	// Emulated marks a platform the docker host runs only under emulation.
	Emulated bool
	// Skip, when non-empty, is why the platform is not tested.
	Skip string
}

// Host is what the docker host can run: its native os/arch and the platforms
// its builder reports, which include those it has an emulator for.
type Host struct {
	Native   string
	Emulates []string
}

// DetectHost asks docker for the daemon's os/arch and the current builder's
// platform list. buildx lists a platform only when the node can run it —
// natively or through a registered binfmt emulator — which is the question
// a non-native `docker run --platform` turns on.
func DetectHost(r *run.Runner) (Host, error) {
	native, err := r.Capture("docker", "version", "--format", "{{.Server.Os}}/{{.Server.Arch}}")
	if err != nil {
		return Host{}, fmt.Errorf("detect docker host platform: %w", err)
	}
	h := Host{Native: strings.TrimSpace(native)}
	// A builder that cannot be inspected only means no emulation is known.
	if out, err := r.Capture("docker", "buildx", "inspect"); err == nil {
		h.Emulates = parseBuilderPlatforms(out)
	}
	return h, nil
}

// parseBuilderPlatforms reads every "Platforms:" line of `docker buildx
// inspect` (one per node), dropping the "*" that marks configured platforms.
func parseBuilderPlatforms(out string) []string {
	var plats []string
	for line := range strings.SplitSeq(out, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "Platforms:")
		if !ok {
			continue
		}
		for p := range strings.SplitSeq(rest, ",") {
			if p = strings.TrimSuffix(strings.TrimSpace(p), "*"); p != "" {
				plats = append(plats, p)
			}
		}
	}
	return plats
}

// osArch strips a platform's variant: linux/arm64/v8 -> linux/arm64. One
// emulator serves every variant of an architecture.
func osArch(p string) string {
	parts := strings.SplitN(p, "/", 3)
	if len(parts) < 2 {
		return p
	}
	return parts[0] + "/" + parts[1]
}

func (h Host) native(p string) bool { return osArch(p) == osArch(h.Native) }

func (h Host) emulates(p string) bool {
	for _, e := range h.Emulates {
		if osArch(e) == osArch(p) {
			return true
		}
	}
	return false
}

// Plan decides, for each configured platform, whether the smoke test runs on
// it under mode (config.TestPlatformsNative or config.TestPlatformsAll).
func Plan(h Host, mode string, platforms []string) []Target {
	anyNative := false
	for _, p := range platforms {
		if h.native(p) {
			anyNative = true
		}
	}
	emulate := mode == config.TestPlatformsAll || !anyNative
	out := make([]Target, 0, len(platforms))
	for _, p := range platforms {
		t := Target{Platform: p}
		switch {
		case h.native(p):
		case !emulate:
			t.Skip = fmt.Sprintf("not native to the docker host (%s); set test.platforms: all to run it under emulation", h.Native)
		case h.emulates(p):
			t.Emulated = true
		default:
			t.Skip = fmt.Sprintf("the docker host (%s) has no emulator for it; register one (e.g. docker/setup-qemu-action or tonistiigi/binfmt) to test it", h.Native)
		}
		out = append(out, t)
	}
	return out
}
