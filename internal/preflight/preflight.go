// Package preflight verifies the external tools stevedore shells out to
// (docker/buildx, git, cosign, syft) are present before a pipeline runs, so a
// missing dependency fails fast with an install hint instead of halfway through
// a release.
package preflight

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/blairham/stevedore/internal/config"
)

// Requirement describes one external tool stevedore may invoke.
type Requirement struct {
	// Label is the human-facing name (e.g. "docker buildx").
	Label string
	// Exe is the executable looked up on PATH.
	Exe string
	// Probe is the argument vector that prints a version (first output line is
	// reported).
	Probe []string
	// Reason explains why this run needs the tool.
	Reason string
	// Install is a short hint for obtaining the tool.
	Install string
	// Required reports whether the current invocation actually needs it.
	Required bool
}

// Opts selects which optional tools count as required for this invocation.
type Opts struct {
	// Sign is true when cosign signing will run.
	Sign bool
	// SBOM is true when SBOM generation will run.
	SBOM bool
	// Scan is true when vulnerability scanning will run.
	Scan bool
	// GitHubRelease is true when a GitHub release will be created.
	GitHubRelease bool
}

// Result is the outcome of probing a single requirement.
type Result struct {
	Requirement
	Found   bool
	Path    string
	Version string
}

// Requirements computes the tool list for cfg under the given options. docker
// and git are always required; cosign and syft are required only when their
// features are both enabled in config and requested for this run.
func Requirements(cfg *config.Config, o Opts) []Requirement {
	reqs := []Requirement{
		{
			Label:    "docker",
			Exe:      "docker",
			Probe:    []string{"version", "--format", "{{.Client.Version}}"},
			Reason:   "build and push images",
			Install:  "https://docs.docker.com/get-docker/",
			Required: true,
		},
		{
			Label:    "docker buildx",
			Exe:      "docker",
			Probe:    []string{"buildx", "version"},
			Reason:   "multi-arch builds via BuildKit",
			Install:  "https://github.com/docker/buildx#installing",
			Required: true,
		},
		{
			Label:    "git",
			Exe:      "git",
			Probe:    []string{"--version"},
			Reason:   "derive version, tags, and changelog",
			Install:  "https://git-scm.com/downloads",
			Required: true,
		},
		{
			Label:    "cosign",
			Exe:      "cosign",
			Probe:    []string{"version"},
			Reason:   "sign images and attach SBOM attestations",
			Install:  "brew install cosign  •  https://docs.sigstore.dev/cosign/system_config/installation/",
			Required: o.Sign && cfg.Sign.Cosign.Enabled,
		},
		{
			Label:    "syft",
			Exe:      "syft",
			Probe:    []string{"version"},
			Reason:   "generate SBOMs",
			Install:  "brew install syft  •  https://github.com/anchore/syft#installation",
			Required: o.SBOM && cfg.SBOM.Enabled,
		},
	}
	if cfg.Scan.Enabled {
		reqs = append(reqs, scannerRequirement(cfg.Scan.Scanner, o.Scan))
	}
	if cfg.Release.GitHub.Enabled {
		reqs = append(reqs, Requirement{
			Label:    "gh",
			Exe:      "gh",
			Probe:    []string{"--version"},
			Reason:   "create GitHub releases",
			Install:  "brew install gh  •  https://github.com/cli/cli#installation",
			Required: o.GitHubRelease,
		})
	}
	if cfg.Versioning.Strategy == "registry" && cfg.Versioning.Lister == "crane" {
		reqs = append(reqs, Requirement{
			Label:    "crane",
			Exe:      "crane",
			Probe:    []string{"version"},
			Reason:   "list registry tags to derive the next version",
			Install:  "brew install crane  •  https://github.com/google/go-containerregistry/tree/main/cmd/crane#installation",
			Required: true,
		})
	}
	if cfg.Versioning.Strategy == "ecr" {
		reqs = append(reqs, Requirement{
			Label:    "aws",
			Exe:      "aws",
			Probe:    []string{"--version"},
			Reason:   "list ECR tags to derive the next version",
			Install:  "brew install awscli  •  https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html",
			Required: true,
		})
	}
	return reqs
}

// scannerRequirement returns the requirement for the configured vulnerability
// scanner.
func scannerRequirement(scanner string, needed bool) Requirement {
	if scanner == "trivy" {
		return Requirement{
			Label:    "trivy",
			Exe:      "trivy",
			Probe:    []string{"--version"},
			Reason:   "scan images for vulnerabilities",
			Install:  "brew install trivy  •  https://aquasecurity.github.io/trivy/latest/getting-started/installation/",
			Required: needed,
		}
	}
	return Requirement{
		Label:    "grype",
		Exe:      "grype",
		Probe:    []string{"version"},
		Reason:   "scan images for vulnerabilities",
		Install:  "brew install grype  •  https://github.com/anchore/grype#installation",
		Required: needed,
	}
}

// Check probes each requirement on PATH and records its version.
func Check(reqs []Requirement) []Result {
	results := make([]Result, 0, len(reqs))
	for _, r := range reqs {
		res := Result{Requirement: r}
		if path, err := exec.LookPath(r.Exe); err == nil {
			res.Found = true
			res.Path = path
			res.Version = probeVersion(r.Exe, r.Probe)
		}
		results = append(results, res)
	}
	return results
}

// Verify returns an error listing every required tool that is missing, with
// install hints. It returns nil when all required tools are present.
func Verify(results []Result) error {
	var missing []Result
	for _, r := range results {
		if r.Required && !r.Found {
			missing = append(missing, r)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("missing required tools:\n")
	for _, m := range missing {
		fmt.Fprintf(&b, "  - %s (%s)\n      install: %s\n", m.Label, m.Reason, m.Install)
	}
	b.WriteString("install the tools above, or disable the feature / pass the matching --skip flag")
	return fmt.Errorf("%s", b.String())
}

// probeVersion runs the tool's version probe and returns a concise version
// string, or "" if the probe fails. Multi-line output (e.g. grype's) is reduced
// to the first line that actually carries a version number.
func probeVersion(exe string, args []string) string {
	out, err := exec.Command(exe, args...).Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	first := strings.TrimSpace(lines[0])
	if containsDigit(first) {
		return first
	}
	for _, l := range lines[1:] {
		if l = strings.TrimSpace(l); containsDigit(l) {
			return l
		}
	}
	return first
}

func containsDigit(s string) bool {
	return strings.ContainsAny(s, "0123456789")
}
