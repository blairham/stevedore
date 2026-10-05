// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

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

// Run executes `docker run --rm [--platform <platform>] <ref> <cmd...>` and
// returns an error unless the container exits with cfg.ExpectExit within the
// timeout. An empty platform lets docker pick. In dry-run mode it echoes the
// command and returns nil.
func Run(r *run.Runner, cfg config.Test, ref, platform string) error {
	if !cfg.Enabled {
		return nil
	}
	args := []string{"run", "--rm"}
	if platform != "" {
		args = append(args, "--platform", platform)
	}
	args = append(args, ref)
	args = append(args, cfg.Cmd...)

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
		return fmt.Errorf("smoke test timed out after %s running %s%s", timeout, ref, onPlatform(platform))
	}
	got := cmd.ProcessState.ExitCode()
	if got != cfg.ExpectExit {
		return fmt.Errorf("smoke test of %s%s exited %d, want %d (%w)", ref, onPlatform(platform), got, cfg.ExpectExit, runErr)
	}
	return nil
}

func onPlatform(p string) string {
	if p == "" {
		return ""
	}
	return " on " + p
}
