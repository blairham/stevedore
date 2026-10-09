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

// Run executes outside dry-run: the child's stdout reaches the Runner's
// Stdout, and the command is echoed with "+ " on its Stderr — always, since a
// release log should show every command that changed something.
func TestRunExecutesStreamsAndEchoes(t *testing.T) {
	outf, errf := stderrFile(t), stderrFile(t)
	marker := filepath.Join(t.TempDir(), "ran")
	r := &Runner{Stdout: outf, Stderr: errf}
	if err := r.Run("sh", "-c", "echo hello; touch "+marker); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("Run did not execute")
	}
	if got := readAll(t, outf); got != "hello\n" {
		t.Errorf("stdout = %q, want the child's output", got)
	}
	if got := readAll(t, errf); !strings.HasPrefix(got, "+ sh -c ") {
		t.Errorf("echo = %q, want a \"+ \" line", got)
	}
}

// A failing command's error names it and keeps the exit status.
func TestRunErrorNamesCommand(t *testing.T) {
	r := &Runner{Stdout: stderrFile(t), Stderr: stderrFile(t)}
	err := r.Run("sh", "-c", "exit 7")
	if err == nil || !strings.HasPrefix(err.Error(), "sh: ") || !strings.Contains(err.Error(), "exit status 7") {
		t.Errorf("err = %v", err)
	}
}

// RunEnv adds its entries to the child's environment on top of this
// process's, rather than replacing it.
func TestRunEnvReachesChild(t *testing.T) {
	outf := stderrFile(t)
	t.Setenv("STEVEDORE_INHERITED", "kept")
	r := &Runner{Stdout: outf, Stderr: stderrFile(t)}
	if err := r.RunEnv(
		[]string{"STEVEDORE_ADDED=yes"},
		"sh",
		"-c",
		`echo "$STEVEDORE_ADDED $STEVEDORE_INHERITED"`,
	); err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, outf); got != "yes kept\n" {
		t.Errorf("child saw %q, want %q", got, "yes kept\n")
	}
}

// Capture returns trimmed stdout and, outside verbose, echoes nothing: it is
// a query, not an action.
func TestCaptureQuietAndTrimmed(t *testing.T) {
	errf := stderrFile(t)
	out, err := (&Runner{Stderr: errf}).Capture("printf", "  v1.2.3\n\n")
	if err != nil || out != "v1.2.3" {
		t.Fatalf("Capture = %q, %v", out, err)
	}
	if got := readAll(t, errf); got != "" {
		t.Errorf("non-verbose Capture echoed %q", got)
	}
}

// Preview echoes in the same format Run uses for the mode, and runs nothing.
func TestPreview(t *testing.T) {
	for _, tc := range []struct {
		dryRun bool
		want   string
	}{
		{true, "[dry-run] cosign sign 'a b'\n"},
		{false, "+ cosign sign 'a b'\n"},
	} {
		errf := stderrFile(t)
		(&Runner{DryRun: tc.dryRun, Stderr: errf}).Preview("cosign", "sign", "a b")
		if got := readAll(t, errf); got != tc.want {
			t.Errorf("dryRun=%v: %q, want %q", tc.dryRun, got, tc.want)
		}
	}
}

// Echoed arguments are shell-quoted when they would otherwise read as
// several words, so a logged command can be pasted back.
func TestQuote(t *testing.T) {
	got := quote([]string{"plain", "two words", "it's", `say "hi"`, "tab\tsep", ""})
	want := []string{"plain", "'two words'", `'it'\''s'`, `'say "hi"'`, "'tab\tsep'", ""}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("quote(%d) = %q, want %q", i, got[i], want[i])
		}
	}
}

// A zero Runner works: Background context, process stdio.
func TestZeroRunner(t *testing.T) {
	var r Runner
	if r.Context() != context.Background() {
		t.Error("zero Runner context is not Background")
	}
	if r.out() != os.Stdout || r.err() != os.Stderr {
		t.Error("zero Runner does not default to the process stdio")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if New(ctx, true, true).Context() != ctx {
		t.Error("New does not bind the context it was given")
	}
}

func TestHas(t *testing.T) {
	if !Has("sh") {
		t.Error("Has(sh) = false")
	}
	if Has("stevedore-no-such-tool") {
		t.Error("Has reported a missing tool")
	}
}
