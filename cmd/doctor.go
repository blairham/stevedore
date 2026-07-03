package cmd

import (
	"fmt"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/preflight"
	"github.com/spf13/cobra"
)

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check that the external tools stevedore needs are installed",
		Long: "doctor probes for docker, buildx, git, cosign, and syft, reports the\n" +
			"version of each, and prints an install hint for anything missing that your\n" +
			"config requires. It reads the config to know which optional tools matter.",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := loadConfigForDoctor()
			if err != nil {
				return err
			}
			// Consider every optional feature the config enables.
			reqs := preflight.Requirements(cfg, preflight.Opts{Sign: true, SBOM: true, Scan: true, GitHubRelease: true})
			results := preflight.Check(reqs)

			missingRequired := false
			for _, r := range results {
				mark, status := "✓", r.Version
				if !r.Found {
					mark = "✗"
					status = "not found"
					if r.Required {
						missingRequired = true
					}
				}
				req := "optional"
				if r.Required {
					req = "required"
				}
				fmt.Printf("  %s  %-14s %-9s %s\n", mark, r.Label, req, status)
				if !r.Found {
					fmt.Printf("        └ %s — install: %s\n", r.Reason, r.Install)
				}
			}

			if missingRequired {
				return fmt.Errorf("one or more required tools are missing")
			}
			fmt.Println("\nall required tools present")
			return nil
		},
	}
}

// loadConfigForDoctor loads the config if one is discoverable, falling back to
// an empty config so doctor still reports tool presence outside a project.
func loadConfigForDoctor() (*config.Config, error) {
	path, err := resolveConfigPath()
	if err != nil {
		// No config found: check the always-required tools against defaults.
		return &config.Config{}, nil
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	fmt.Printf("config: %s\n\n", path)
	return cfg, nil
}
