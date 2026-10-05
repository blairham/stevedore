// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package tester runs a built image as a smoke test and gates the release on it.
package tester

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/run"
)

const (
	defaultTimeout = 60 * time.Second
	// removeTimeout bounds the `docker rm -f` that cleans up after a smoke
	// test was stopped early.
	removeTimeout = 30 * time.Second
)

// Run executes `docker run --rm --name stevedore-test-<rand> [--platform
// <platform>] <ref> <cmd...>` and returns an error unless the container exits
// with cfg.ExpectExit within the timeout. An empty platform lets docker pick.
// The run is bound to the Runner's context; when the timeout or a cancellation
// stops it early, the container is removed by name, since killing the docker
// CLI leaves the container itself running. In dry-run mode it echoes the
// command and returns nil.
func Run(r *run.Runner, cfg config.Test, ref, platform string) error {
	if !cfg.Enabled {
		return nil
	}
	name, err := containerName()
	if err != nil {
		return err
	}
	args := []string{"run", "--rm", "--name", name}
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
	parent := r.Context()
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	runErr := cmd.Run()

	if ctx.Err() != nil {
		removeContainer(parent, name)
		if parent.Err() != nil {
			return fmt.Errorf("smoke test of %s%s canceled: %w", ref, onPlatform(platform), parent.Err())
		}
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

// containerName returns a name unique to this smoke test, so a run stopped
// early can be removed without touching any other container.
func containerName() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("naming smoke-test container: %w", err)
	}
	return "stevedore-test-" + hex.EncodeToString(b), nil
}

// removeContainer force-removes the named container. It runs detached from
// ctx's cancellation, because cancellation is usually why it is needed, and
// is best-effort: a container docker already removed is the outcome wanted,
// and any other failure is a warning, since the smoke test has failed anyway.
func removeContainer(ctx context.Context, name string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), removeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "rm", "-f", name).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "No such container") {
		fmt.Fprintf(os.Stderr, "warning: could not remove smoke-test container %s: %v: %s\n",
			name, err, strings.TrimSpace(string(out)))
	}
}
