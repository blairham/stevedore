// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package signer

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/run"
)

// fakeCosign puts a cosign on PATH that records each invocation's argv (one
// arg per line, invocations separated by "--") and exits with code, and
// returns a reader for the recorded invocations.
func fakeCosign(t *testing.T, code int) func() [][]string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake cosign is a shell script")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "argv.log")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> \"" + log + "\"; done\n" +
		"echo -- >> \"" + log + "\"\nexit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "cosign"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return func() [][]string {
		b, err := os.ReadFile(log)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			t.Fatal(err)
		}
		var calls [][]string
		var cur []string
		for _, l := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
			if l == "--" {
				calls = append(calls, cur)
				cur = nil
				continue
			}
			cur = append(cur, l)
		}
		return calls
	}
}

func quiet(t *testing.T) *run.Runner {
	t.Helper()
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = devnull.Close() })
	r := run.New(context.Background(), false, false)
	r.Stdout, r.Stderr = devnull, devnull
	return r
}

func TestSignArgv(t *testing.T) {
	calls := fakeCosign(t, 0)
	cfg := config.Cosign{Enabled: true, Key: "cosign.key", Args: []string{"--tlog-upload=false"}}
	if err := Sign(quiet(t), cfg, []string{"ghcr.io/x/a", "ghcr.io/x/b"}, "sha256:abc"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"sign", "--yes", "--key", "cosign.key", "--tlog-upload=false", "ghcr.io/x/a@sha256:abc"},
		{"sign", "--yes", "--key", "cosign.key", "--tlog-upload=false", "ghcr.io/x/b@sha256:abc"},
	}
	if got := calls(); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("cosign calls = %q, want %q", got, want)
	}
}

// sign.cosign.args must reach cosign attest too, or a --tlog-upload=false
// signature is paired with a publicly uploaded SBOM attestation (#51).
func TestAttestArgv(t *testing.T) {
	calls := fakeCosign(t, 0)
	cfg := config.Cosign{Enabled: true, Args: []string{"--tlog-upload=false", "--rekor-url", "https://rekor.example"}}
	if err := Attest(quiet(t), cfg, []string{"ghcr.io/x/a"}, "sha256:abc", "dist/sbom.json", "spdxjson"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{
		"attest", "--yes", "--predicate", "dist/sbom.json", "--type", "spdxjson",
		"--tlog-upload=false", "--rekor-url", "https://rekor.example", "ghcr.io/x/a@sha256:abc",
	}}
	if got := calls(); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("cosign calls = %q, want %q", got, want)
	}
}

func TestDisabledRunsNothing(t *testing.T) {
	calls := fakeCosign(t, 0)
	r := quiet(t)
	if err := Sign(r, config.Cosign{}, []string{"ghcr.io/x/a"}, "sha256:abc"); err != nil {
		t.Fatal(err)
	}
	if err := Attest(r, config.Cosign{}, []string{"ghcr.io/x/a"}, "sha256:abc", "p", "t"); err != nil {
		t.Fatal(err)
	}
	if got := calls(); len(got) != 0 {
		t.Errorf("disabled cosign ran %q", got)
	}
}

func TestCosignFailureStops(t *testing.T) {
	calls := fakeCosign(t, 1)
	cfg := config.Cosign{Enabled: true}
	repos := []string{"ghcr.io/x/a", "ghcr.io/x/b"}
	if err := Sign(quiet(t), cfg, repos, "sha256:abc"); err == nil || !strings.Contains(err.Error(), "ghcr.io/x/a@sha256:abc") {
		t.Errorf("Sign error = %v, want one naming the first ref", err)
	}
	if err := Attest(quiet(t), cfg, repos, "sha256:abc", "p", "t"); err == nil || !strings.Contains(err.Error(), "cosign attest") {
		t.Errorf("Attest error = %v, want a cosign attest error", err)
	}
	if got := calls(); len(got) != 2 {
		t.Errorf("got %d cosign calls, want 2 (each stops at the first failure)", len(got))
	}
}

func TestSignNeedsDigest(t *testing.T) {
	calls := fakeCosign(t, 0)
	if err := Sign(quiet(t), config.Cosign{Enabled: true}, []string{"ghcr.io/x/a"}, ""); err == nil {
		t.Error("Sign with no digest succeeded")
	}
	if got := calls(); len(got) != 0 {
		t.Errorf("cosign ran without a digest: %q", got)
	}
}

// Keyless signing passes no --key: cosign then takes the OIDC path.
func TestSignKeylessArgv(t *testing.T) {
	calls := fakeCosign(t, 0)
	if err := Sign(quiet(t), config.Cosign{Enabled: true}, []string{"ghcr.io/x/a"}, "sha256:abc"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"sign", "--yes", "ghcr.io/x/a@sha256:abc"}}
	if got := calls(); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("cosign calls = %q, want %q", got, want)
	}
}

// A dry run previews without cosign installed and without a digest, and runs
// nothing.
func TestDryRunNeedsNoCosign(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	r := quiet(t)
	r.DryRun = true
	cfg := config.Cosign{Enabled: true}
	if err := Sign(r, cfg, []string{"ghcr.io/x/a"}, ""); err != nil {
		t.Errorf("dry-run Sign: %v", err)
	}
	if err := Attest(r, cfg, []string{"ghcr.io/x/a"}, "", "p", "spdxjson"); err != nil {
		t.Errorf("dry-run Attest: %v", err)
	}
}

// A real run without cosign on PATH fails up front, naming the tool, rather
// than releasing an unsigned image.
func TestMissingCosignFails(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	cfg := config.Cosign{Enabled: true}
	if err := Sign(quiet(t), cfg, []string{"ghcr.io/x/a"}, "sha256:abc"); err == nil || !strings.Contains(err.Error(), "cosign not found") {
		t.Errorf("Sign err = %v", err)
	}
	if err := Attest(quiet(t), cfg, []string{"ghcr.io/x/a"}, "sha256:abc", "p", "spdxjson"); err == nil || !strings.Contains(err.Error(), "cosign") {
		t.Errorf("Attest err = %v", err)
	}
}
