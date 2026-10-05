// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package sbom

import (
	"os"
	"strings"
	"testing"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/run"
)

func TestPredicateType(t *testing.T) {
	cases := map[string]string{
		"cyclonedx-json": "cyclonedx",
		"cyclonedx":      "cyclonedx",
		"spdx-json":      "spdxjson",
		"":               "spdxjson",
		"anything-else":  "spdxjson",
	}
	for format, want := range cases {
		if got := PredicateType(format); got != want {
			t.Errorf("PredicateType(%q) = %q, want %q", format, got, want)
		}
	}
}

func TestGeneratePlatform(t *testing.T) {
	sink, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	r := &run.Runner{DryRun: true, Stdout: sink, Stderr: sink}
	cfg := config.SBOM{Enabled: true, Generator: "syft", Format: "spdx-json"}
	path, err := Generate(r, cfg, "dist", "app-linux-arm64", "img@sha256:d", "linux/arm64")
	if err != nil {
		t.Fatal(err)
	}
	if path != "dist/sbom-app-linux-arm64.spdx.json" {
		t.Errorf("path = %q", path)
	}
	out, _ := os.ReadFile(sink.Name())
	if !strings.Contains(string(out), "syft img@sha256:d --platform linux/arm64 -o spdx-json=") {
		t.Errorf("syft invocation = %q", out)
	}
}
