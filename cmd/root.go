// Package cmd implements the stevedore CLI.
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/pipeline"
)

// Build-time version, overridden via -ldflags "-X .../cmd.version=...".
var version = "dev"

// SetVersion lets main inject the build version.
func SetVersion(v string) {
	if v != "" {
		version = v
	}
}

var (
	flagConfig  string
	flagDir     string
	flagVerbose bool
	flagDryRun  bool
)

// Execute runs the root command.
func Execute() error {
	return newRootCmd().Execute()
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "stevedore",
		Short: "Release Docker/OCI images the way goreleaser releases binaries",
		Long: "stevedore builds multi-arch container images, tags them from git state,\n" +
			"pushes to one or more registries, signs with cosign, generates SBOMs, and\n" +
			"writes a changelog — all from a single declarative config file.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}
	root.PersistentFlags().StringVarP(&flagConfig, "config", "f", "", "path to config file (default: autodiscover .stevedore.yaml)")
	root.PersistentFlags().StringVar(&flagDir, "dir", ".", "project/repository root")
	root.PersistentFlags().BoolVarP(&flagVerbose, "verbose", "v", false, "verbose output")
	root.PersistentFlags().BoolVar(&flagDryRun, "dry-run", false, "print commands without executing")

	root.AddCommand(
		newReleaseCmd(),
		newPlanCmd(),
		newBuildCmd(),
		newCheckCmd(),
		newVerifyCmd(),
		newDoctorCmd(),
		newInitCmd(),
		newSchemaCmd(),
	)
	return root
}

// resolveConfigPath returns the explicit --config or autodiscovers one in --dir.
func resolveConfigPath() (string, error) {
	if flagConfig != "" {
		return flagConfig, nil
	}
	p, err := config.Discover(flagDir)
	if err != nil {
		return "", err
	}
	return p, nil
}

func baseOptions() (pipeline.Options, error) {
	cfgPath, err := resolveConfigPath()
	if err != nil {
		return pipeline.Options{}, err
	}
	return pipeline.Options{
		ConfigPath: cfgPath,
		Dir:        flagDir,
		DryRun:     flagDryRun,
		Verbose:    flagVerbose,
	}, nil
}

func newCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Validate the config and print the resolved release plan",
		RunE: func(_ *cobra.Command, _ []string) error {
			o, err := baseOptions()
			if err != nil {
				return err
			}
			// check validates the config; it should not hard-fail just because a
			// registry version can't be resolved (offline / unauthenticated).
			o.SoftVersion = true
			p, err := pipeline.Prepare(o)
			if err != nil {
				return err
			}
			fmt.Printf("config:   %s\n", o.ConfigPath)
			fmt.Printf("project:  %s\n", p.Config.ProjectName)
			fmt.Printf("version:  %s\n", p.Ctx.Version)
			fmt.Printf("branch:   %s (default=%v)\n", p.Ctx.Branch, p.Ctx.IsDefault)
			fmt.Printf("commit:   %s\n", p.Ctx.ShortCommit)
			fmt.Println("images:")
			for _, plan := range p.Plans {
				fmt.Printf("  %s:\n", plan.Image.ID)
				for _, ref := range plan.Refs {
					fmt.Printf("    - %s\n", ref)
				}
			}
			fmt.Println("config OK")
			return nil
		},
	}
}
