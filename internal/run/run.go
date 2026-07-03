// Package run wraps external command execution with dry-run and verbose modes.
package run

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Runner executes external commands, honoring dry-run and verbose settings.
type Runner struct {
	DryRun  bool
	Verbose bool
	// Stdout/Stderr default to os.Stdout/os.Stderr when nil.
	Stdout *os.File
	Stderr *os.File
}

// New returns a Runner writing to the process stdio.
func New(dryRun, verbose bool) *Runner {
	return &Runner{DryRun: dryRun, Verbose: verbose, Stdout: os.Stdout, Stderr: os.Stderr}
}

// Run executes name with args, streaming output. In dry-run mode it prints the
// command and returns nil without executing.
func (r *Runner) Run(name string, args ...string) error {
	r.echo(name, args)
	if r.DryRun {
		return nil
	}
	cmd := exec.Command(name, args...)
	cmd.Stdout = r.out()
	cmd.Stderr = r.err()
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// Capture runs the command and returns its stdout. It executes even in dry-run
// mode, since it is used for read-only queries (e.g. reading a digest file).
func (r *Runner) Capture(name string, args ...string) (string, error) {
	if r.Verbose {
		r.echo(name, args)
	}
	out, err := exec.Command(name, args...).Output()
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
