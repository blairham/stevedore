// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tester

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/run"
)

func TestDisabledIsNoop(t *testing.T) {
	if err := Run(&run.Runner{}, config.Test{Enabled: false}, "img:tag", ""); err != nil {
		t.Errorf("disabled test should be a no-op, got %v", err)
	}
}

func TestDryRunEchoesAndPasses(t *testing.T) {
	devnull, _ := os.Open(os.DevNull)
	r := &run.Runner{DryRun: true, Stdout: devnull, Stderr: devnull}
	cfg := config.Test{Enabled: true, Cmd: []string{"/bin/true"}}
	if err := Run(r, cfg, "img:tag", ""); err != nil {
		t.Errorf("dry-run should not execute or error, got %v", err)
	}
}

func TestRealRunChecksExitCode(t *testing.T) {
	if _, err := os.Stat("/var/run/docker.sock"); err != nil {
		t.Skip("docker not available")
	}
	// A passing container: alpine `true` exits 0.
	if err := Run(&run.Runner{}, config.Test{Enabled: true, Cmd: []string{"true"}}, "alpine", ""); err != nil {
		t.Errorf("expected pass, got %v", err)
	}
	// A failing container: `false` exits 1, expect_exit defaults to 0 -> error.
	if err := Run(&run.Runner{}, config.Test{Enabled: true, Cmd: []string{"false"}}, "alpine", ""); err == nil {
		t.Error("expected smoke test to fail on non-zero exit")
	}
}

// fakeDocker puts a docker script on PATH whose `run` hangs like a container
// that never exits and whose `rm` records the container it was asked to
// remove. It returns the files holding the run and rm arguments.
func fakeDocker(t *testing.T) (runLog, rmLog string) {
	t.Helper()
	dir := t.TempDir()
	runLog = filepath.Join(dir, "run.log")
	rmLog = filepath.Join(dir, "rm.log")
	script := "#!/bin/sh\ncase \"$1\" in\n" +
		"  run) echo \"$@\" > " + runLog + "; exec /bin/sleep 30 ;;\n" +
		"  rm) echo \"$@\" > " + rmLog + " ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return runLog, rmLog
}

// The container a smoke test started must not outlive it: killing the docker
// CLI leaves the container running, so a timeout or a cancellation removes it
// by the name the run gave it.
func TestStoppedSmokeTestRemovesContainer(t *testing.T) {
	devnull, _ := os.Open(os.DevNull)
	defer devnull.Close()

	for _, tc := range []struct {
		name    string
		timeout string
		cancel  bool
		want    string
	}{
		// Long enough for the fake to record its arguments on a loaded
		// machine; the cancel case cancels as soon as it has.
		{name: "timeout", timeout: "2s", want: "timed out"},
		{name: "cancel", timeout: "30s", cancel: true, want: "canceled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runLog, rmLog := fakeDocker(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancel {
				go func() {
					for ctx.Err() == nil {
						if b, _ := os.ReadFile(runLog); len(b) > 0 {
							cancel()
							return
						}
						time.Sleep(20 * time.Millisecond)
					}
				}()
			}
			r := run.New(ctx, false, false)
			r.Stdout, r.Stderr = devnull, devnull

			start := time.Now()
			err := Run(r, config.Test{Enabled: true, Timeout: tc.timeout, Cmd: []string{"serve"}}, "img:tag", "")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to say %q", err, tc.want)
			}
			if d := time.Since(start); d > 10*time.Second {
				t.Errorf("smoke test took %s; it should stop at the timeout or cancellation", d)
			}

			ran, _ := os.ReadFile(runLog)
			fields := strings.Fields(string(ran))
			name := ""
			for i, f := range fields {
				if f == "--name" && i+1 < len(fields) {
					name = fields[i+1]
				}
			}
			if !strings.HasPrefix(name, "stevedore-test-") {
				t.Fatalf("docker run args %q carry no stevedore-test- name", ran)
			}
			removed, _ := os.ReadFile(rmLog)
			if got, want := strings.TrimSpace(string(removed)), "rm -f "+name; got != want {
				t.Errorf("docker rm args = %q, want %q", got, want)
			}
		})
	}
}
