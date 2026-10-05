// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package run

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stderrFile(t *testing.T) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func readAll(t *testing.T, f *os.File) string {
	t.Helper()
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Refresh runs under dry-run — planning needs what it fetches — and, unlike a
// read-only Capture, is echoed there because it changes local state. Run, the
// control, neither executes nor marks itself as executed under dry-run.
func TestRefreshRunsAndEchoesUnderDryRun(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		dryRun, verbose bool
		echoed          bool
	}{
		{dryRun: true, echoed: true},
		{verbose: true, echoed: true},
		{echoed: false},
	} {
		marker := filepath.Join(dir, "ran")
		_ = os.Remove(marker)
		errf := stderrFile(t)
		ctx := WithRunner(t.Context(), &Runner{DryRun: tc.dryRun, Verbose: tc.verbose, Stderr: errf})
		if err := Refresh(ctx, "touch", marker); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(marker); err != nil {
			t.Errorf("dryRun=%v verbose=%v: Refresh did not execute", tc.dryRun, tc.verbose)
		}
		got := readAll(t, errf)
		if echoed := strings.Contains(got, "+ touch "+marker); echoed != tc.echoed {
			t.Errorf("dryRun=%v verbose=%v: echoed=%v, want %v (stderr %q)", tc.dryRun, tc.verbose, echoed, tc.echoed, got)
		}
	}

	marker := filepath.Join(dir, "run")
	errf := stderrFile(t)
	if err := Exec(WithRunner(t.Context(), &Runner{DryRun: true, Stderr: errf}), "touch", marker); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("Run executed under dry-run")
	}
	if got := readAll(t, errf); !strings.HasPrefix(got, "[dry-run] touch ") {
		t.Errorf("Run under dry-run echoed %q", got)
	}
}

func TestRefreshErrorCarriesOutput(t *testing.T) {
	err := Refresh(t.Context(), "sh", "-c", "echo boom >&2; exit 3")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v, want the command's output in it", err)
	}
}

// A context carries its Runner's settings to a package that takes only a
// context; one without a Runner gets a quiet, executing one. Either way the
// command is bound to the context passed, not the Runner's own.
func TestContextRunner(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	if err := Exec(t.Context(), "touch", marker); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("a bare context's runner did not execute")
	}
	ctx, cancel := context.WithCancel(WithRunner(context.Background(), &Runner{}))
	cancel()
	if _, err := Query(ctx, "echo", "hi"); err == nil {
		t.Error("a command ran on a canceled context")
	}
}

// Capture executes under dry-run, so its verbose echo must not claim it was
// skipped.
func TestCaptureEchoesAsRanUnderDryRun(t *testing.T) {
	errf := stderrFile(t)
	out, err := (&Runner{DryRun: true, Verbose: true, Stderr: errf}).Capture("echo", "hi")
	if err != nil || out != "hi" {
		t.Fatalf("Capture = %q, %v", out, err)
	}
	if got := readAll(t, errf); got != "+ echo hi\n" {
		t.Errorf("echo = %q, want %q", got, "+ echo hi\n")
	}
}

// RunEnv shows the variables it adds in front of the command, as a shell
// would read them.
func TestRunEnvEchoesEnv(t *testing.T) {
	f := stderrFile(t)
	r := &Runner{DryRun: true, Stderr: f}
	if err := r.RunEnv([]string{"SOURCE_DATE_EPOCH=5"}, "docker", "buildx", "build"); err != nil {
		t.Fatal(err)
	}
	if got, want := readAll(t, f), "[dry-run] SOURCE_DATE_EPOCH=5 docker buildx build\n"; got != want {
		t.Errorf("echo = %q, want %q", got, want)
	}
}
