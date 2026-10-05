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
	return r.run(r.Context(), name, args)
}

// RunEnv is Run with env ("KEY=value" entries) added to the command's
// environment, on top of this process's. The entries are echoed in front of
// the command, as a shell would read them.
func (r *Runner) RunEnv(env []string, name string, args ...string) error {
	return r.runEnv(r.Context(), env, name, args)
}

func (r *Runner) run(ctx context.Context, name string, args []string) error {
	return r.runEnv(ctx, nil, name, args)
}

func (r *Runner) runEnv(ctx context.Context, env []string, name string, args []string) error {
	r.echo(strings.Join(append(quote(env), name), " "), args)
	if r.DryRun {
		return nil
	}
	cmd := exec.CommandContext(ctx, name, args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
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
// mode, since it is used for read-only queries (e.g. reading a digest file),
// and so is echoed under --verbose with the "+ " of a command that ran, never
// the "[dry-run] " of one that did not.
func (r *Runner) Capture(name string, args ...string) (string, error) {
	return r.capture(r.Context(), name, args)
}

func (r *Runner) capture(ctx context.Context, name string, args []string) (string, error) {
	if r.Verbose {
		r.echoRan(name, args)
	}
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *Runner) refresh(ctx context.Context, name string, args []string) error {
	if r.DryRun || r.Verbose {
		r.echoRan(name, args)
	}
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Packages whose API takes a context rather than a Runner (internal/changed,
// internal/gitinfo) reach the invocation's Runner through the context: the
// pipeline attaches it with WithRunner, and Exec, Query and
// Refresh start commands through it, bound to the context passed. A
// context without one gets a quiet Runner that executes everything.

type runnerKey struct{}

// WithRunner returns ctx carrying r.
func WithRunner(ctx context.Context, r *Runner) context.Context {
	return context.WithValue(ctx, runnerKey{}, r)
}

func fromContext(ctx context.Context) *Runner {
	if r, ok := ctx.Value(runnerKey{}).(*Runner); ok && r != nil {
		return r
	}
	return &Runner{}
}

// Exec is Run through ctx's Runner: skipped (and echoed) under dry-run.
func Exec(ctx context.Context, name string, args ...string) error {
	return fromContext(ctx).run(ctx, name, args)
}

// Query is Capture through ctx's Runner: a read-only query that runs
// under dry-run too and is echoed under verbose.
func Query(ctx context.Context, name string, args ...string) (string, error) {
	return fromContext(ctx).capture(ctx, name, args)
}

// Refresh runs a command that changes only local state the run itself
// has to read in order to plan — fetching git refs, say. Unlike Run it
// executes under dry-run too, because the plan would be wrong without it;
// unlike Capture it is echoed under dry-run as well as verbose, because it
// does change something. The command's own output is in the error when it
// fails.
func Refresh(ctx context.Context, name string, args ...string) error {
	return fromContext(ctx).refresh(ctx, name, args)
}

// Has reports whether an executable is available on PATH.
func Has(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// echoRan echoes a command that executes whatever the mode.
func (r *Runner) echoRan(name string, args []string) {
	fmt.Fprintln(r.err(), "+ "+name+" "+strings.Join(quote(args), " "))
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
