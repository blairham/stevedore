// Package tester runs a built image as a smoke test and gates the release on it.
package tester

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/run"
)

const defaultTimeout = 60 * time.Second

// Run executes `docker run --rm <ref> <cmd...>` and returns an error unless the
// container exits with cfg.ExpectExit within the timeout. In dry-run mode it
// echoes the command and returns nil.
func Run(r *run.Runner, cfg config.Test, ref string) error {
	if !cfg.Enabled {
		return nil
	}
	args := append([]string{"run", "--rm", ref}, cfg.Cmd...)

	if r.DryRun {
		return r.Run("docker", args...) // echoes only
	}

	timeout := defaultTimeout
	if cfg.Timeout != "" {
		d, err := time.ParseDuration(cfg.Timeout)
		if err != nil {
			return fmt.Errorf("invalid test.timeout %q: %w", cfg.Timeout, err)
		}
		timeout = d
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	runErr := cmd.Run()

	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("smoke test timed out after %s running %s", timeout, ref)
	}
	got := cmd.ProcessState.ExitCode()
	if got != cfg.ExpectExit {
		return fmt.Errorf("smoke test of %s exited %d, want %d (%v)", ref, got, cfg.ExpectExit, runErr)
	}
	return nil
}
