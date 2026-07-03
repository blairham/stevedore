package tester

import (
	"os"
	"testing"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/run"
)

func TestDisabledIsNoop(t *testing.T) {
	if err := Run(&run.Runner{}, config.Test{Enabled: false}, "img:tag"); err != nil {
		t.Errorf("disabled test should be a no-op, got %v", err)
	}
}

func TestDryRunEchoesAndPasses(t *testing.T) {
	devnull, _ := os.Open(os.DevNull)
	r := &run.Runner{DryRun: true, Stdout: devnull, Stderr: devnull}
	cfg := config.Test{Enabled: true, Cmd: []string{"/bin/true"}}
	if err := Run(r, cfg, "img:tag"); err != nil {
		t.Errorf("dry-run should not execute or error, got %v", err)
	}
}

func TestRealRunChecksExitCode(t *testing.T) {
	if _, err := os.Stat("/var/run/docker.sock"); err != nil {
		t.Skip("docker not available")
	}
	// A passing container: alpine `true` exits 0.
	if err := Run(&run.Runner{}, config.Test{Enabled: true, Cmd: []string{"true"}}, "alpine"); err != nil {
		t.Errorf("expected pass, got %v", err)
	}
	// A failing container: `false` exits 1, expect_exit defaults to 0 -> error.
	if err := Run(&run.Runner{}, config.Test{Enabled: true, Cmd: []string{"false"}}, "alpine"); err == nil {
		t.Error("expected smoke test to fail on non-zero exit")
	}
}
