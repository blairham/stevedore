// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package run wraps external command execution with dry-run and verbose modes.
package run

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Runner executes external commands, honoring dry-run and verbose settings.
// It holds the context of the invocation it serves: every command it starts
// is bound to that context, so a Runner is built per call rather than shared.
type Runner struct {
	ctx     context.Context
	DryRun  bool
	Verbose bool
	// Stdout/Stderr default to os.Stdout/os.Stderr when nil.
	Stdout *os.File
	Stderr *os.File
}

// New returns a Runner writing to the process stdio whose commands are bound
// to ctx.
func New(ctx context.Context, dryRun, verbose bool) *Runner {
	return &Runner{ctx: ctx, DryRun: dryRun, Verbose: verbose, Stdout: os.Stdout, Stderr: os.Stderr}
}

// Run executes name with args, streaming output. In dry-run mode it prints the
// command and returns nil without executing.
func (r *Runner) Run(name string, args ...string) error {
	r.echo(name, args)
	if r.DryRun {
		return nil
	}
	cmd := exec.CommandContext(r.Context(), name, args...)
	cmd.Stdout = r.out()
	cmd.Stderr = r.err()
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// Preview prints the command the way Run does in dry-run mode, without running
// it. It is for a caller's dry-run branch that must not execute and so has no
// result to report.
func (r *Runner) Preview(name string, args ...string) {
	r.echo(name, args)
}

// Capture runs the command and returns its stdout. It executes even in dry-run
// mode, since it is used for read-only queries (e.g. reading a digest file).
func (r *Runner) Capture(name string, args ...string) (string, error) {
	if r.Verbose {
		r.echo(name, args)
	}
	out, err := exec.CommandContext(r.Context(), name, args...).Output()
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Has reports whether an executable is available on PATH.
func Has(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func (r *Runner) echo(name string, args []string) {
	prefix := "+ "
	if r.DryRun {
		prefix = "[dry-run] "
	}
	fmt.Fprintln(r.err(), prefix+name+" "+strings.Join(quote(args), " "))
}

// Context returns the context the Runner binds its work to, or Background for
// a zero Runner.
func (r *Runner) Context() context.Context {
	if r.ctx != nil {
		return r.ctx
	}
	return context.Background()
}

func (r *Runner) out() *os.File {
	if r.Stdout != nil {
		return r.Stdout
	}
	return os.Stdout
}

func (r *Runner) err() *os.File {
	if r.Stderr != nil {
		return r.Stderr
	}
	return os.Stderr
}

// quote wraps args containing spaces in single quotes for readable echo output.
func quote(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " \t\"'") {
			out[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		} else {
			out[i] = a
		}
	}
	return out
}
