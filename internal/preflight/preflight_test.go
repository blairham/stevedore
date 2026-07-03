package preflight

import (
	"strings"
	"testing"

	"github.com/blairham/stevedore/internal/config"
)

func labels(reqs []Requirement) map[string]Requirement {
	m := map[string]Requirement{}
	for _, r := range reqs {
		m[r.Label] = r
	}
	return m
}

func TestRequirementsAlwaysNeedsBuildTools(t *testing.T) {
	reqs := Requirements(&config.Config{}, Opts{})
	m := labels(reqs)
	for _, want := range []string{"docker", "docker buildx", "git"} {
		if r, ok := m[want]; !ok || !r.Required {
			t.Errorf("%s should always be required, got %+v", want, r)
		}
	}
}

func TestRequirementsCosignGating(t *testing.T) {
	cfg := &config.Config{}
	cfg.Sign.Cosign.Enabled = true

	// Signing enabled in config AND requested for the run -> required.
	if !labels(Requirements(cfg, Opts{Sign: true}))["cosign"].Required {
		t.Error("cosign should be required when sign enabled and run wants signing")
	}
	// Config enables it but the run skips signing -> not required.
	if labels(Requirements(cfg, Opts{Sign: false}))["cosign"].Required {
		t.Error("cosign should not be required when run skips signing")
	}
	// Run wants signing but config disables it -> not required.
	if labels(Requirements(&config.Config{}, Opts{Sign: true}))["cosign"].Required {
		t.Error("cosign should not be required when sign disabled in config")
	}
}

func TestRequirementsSBOMGating(t *testing.T) {
	cfg := &config.Config{}
	cfg.SBOM.Enabled = true
	if !labels(Requirements(cfg, Opts{SBOM: true}))["syft"].Required {
		t.Error("syft should be required when sbom enabled and requested")
	}
	if labels(Requirements(cfg, Opts{SBOM: false}))["syft"].Required {
		t.Error("syft should not be required when run skips sbom")
	}
}

func TestVerify(t *testing.T) {
	results := []Result{
		{Requirement: Requirement{Label: "docker", Required: true}, Found: true},
		{Requirement: Requirement{Label: "cosign", Required: true, Install: "brew install cosign"}, Found: false},
		{Requirement: Requirement{Label: "syft", Required: false}, Found: false},
	}
	err := Verify(results)
	if err == nil {
		t.Fatal("expected error for missing required tool")
	}
	msg := err.Error()
	if !strings.Contains(msg, "cosign") {
		t.Errorf("error should name the missing required tool: %q", msg)
	}
	if !strings.Contains(msg, "brew install cosign") {
		t.Errorf("error should include the install hint: %q", msg)
	}
	if strings.Contains(msg, "syft") {
		t.Errorf("error should not list optional missing tools: %q", msg)
	}
}

func TestVerifyAllPresent(t *testing.T) {
	results := []Result{
		{Requirement: Requirement{Label: "docker", Required: true}, Found: true},
		{Requirement: Requirement{Label: "cosign", Required: false}, Found: false},
	}
	if err := Verify(results); err != nil {
		t.Errorf("expected nil when all required tools present, got %v", err)
	}
}

func TestCheckReportsPresence(t *testing.T) {
	// "go" is guaranteed present in the test environment; a bogus name is not.
	reqs := []Requirement{
		{Label: "go", Exe: "go", Probe: []string{"version"}, Required: true},
		{Label: "nope", Exe: "stevedore-nonexistent-xyz", Required: true},
	}
	results := Check(reqs)
	m := map[string]Result{}
	for _, r := range results {
		m[r.Label] = r
	}
	if !m["go"].Found || m["go"].Version == "" {
		t.Errorf("go should be found with a version: %+v", m["go"])
	}
	if m["nope"].Found {
		t.Errorf("bogus exe should not be found: %+v", m["nope"])
	}
}
