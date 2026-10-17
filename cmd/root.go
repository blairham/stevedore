// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package cmd implements the stevedore CLI.
package cmd

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/pipeline"
)

// Build-time version, overridden via -ldflags "-X .../cmd.version=...".
var version = "dev"

// SetVersion lets main inject the build version. A binary from `go install`
// has no -ldflags, so main passes its "dev" default; the module version the go
// command stamped into the binary is used instead when there is one.
func SetVersion(v string) {
	version = resolveVersion(v, debug.ReadBuildInfo)
}

func resolveVersion(v string, buildInfo func() (*debug.BuildInfo, bool)) string {
	if v != "" && v != "dev" {
		return v
	}
	if info, ok := buildInfo(); ok {
		// "(devel)" is what a build from a local checkout reports.
		if mv := info.Main.Version; mv != "" && mv != "(devel)" {
			return strings.TrimPrefix(mv, "v")
		}
	}
	return "dev"
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
	root.PersistentFlags().
		StringVarP(&flagConfig, "config", "f", "", "path to config file (default: autodiscover .stevedore.yaml)")
	root.PersistentFlags().StringVar(&flagDir, "dir", ".", "project/repository root")
	root.PersistentFlags().BoolVarP(&flagVerbose, "verbose", "v", false, "verbose output")
	root.PersistentFlags().BoolVar(&flagDryRun, "dry-run", false, "print commands without executing")

	root.AddCommand(
		newReleaseCmd(),
		newMergeCmd(),
		newPublishCmd(),
		newPlanCmd(),
		newBuildCmd(),
		newCheckCmd(),
		newVerifyCmd(),
		newPromoteCmd(),
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

func baseOptions(ctx context.Context) (pipeline.Options, error) {
	cfgPath, err := resolveConfigPath()
	if err != nil {
		return pipeline.Options{}, err
	}
	return pipeline.Options{
		Context:    ctx,
		ConfigPath: cfgPath,
		Dir:        flagDir,
		DryRun:     flagDryRun,
		Verbose:    flagVerbose,
	}, nil
}

// prereleaseUsage is --prerelease's help, shared by every command that
// resolves a version.
const prereleaseUsage = "cut a prerelease of this series (rc: 1.5.0-rc.1, then rc.2), overriding " +
	"versioning.prerelease; empty cuts a release, promoting the series. Same value on every command of one release"

// prereleaseFlag registers --prerelease on cmd and returns the function that
// copies it into the options. Only a flag that was given overrides the
// config, so --prerelease= is how a run promotes when the config sets one.
func prereleaseFlag(cmd *cobra.Command) func(*pipeline.Options) {
	var value string
	cmd.Flags().StringVar(&value, "prerelease", "", prereleaseUsage)
	return func(o *pipeline.Options) {
		if cmd.Flags().Changed("prerelease") {
			v := value
			o.Prerelease = &v
		}
	}
}

func newCheckCmd() *cobra.Command {
	var applyPrerelease func(*pipeline.Options)
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Validate the config and print the resolved release plan",
		RunE: func(c *cobra.Command, _ []string) error {
			o, err := baseOptions(c.Context())
			if err != nil {
				return err
			}
			// check validates the config; it should not hard-fail just because a
			// registry version can't be resolved (offline / unauthenticated).
			o.SoftVersion = true
			applyPrerelease(&o)
			p, err := pipeline.Prepare(o)
			if err != nil {
				return err
			}
			fmt.Printf("config:   %s\n", o.ConfigPath)
			fmt.Printf("project:  %s\n", p.Config.ProjectName)
			if r := p.Version.Reason(); r != "" {
				fmt.Printf("version:  %s (%s)\n", p.Ctx.Version, r)
			} else {
				fmt.Printf("version:  %s\n", p.Ctx.Version)
			}
			fmt.Printf("branch:   %s (default=%v)\n", p.Ctx.Branch, p.Ctx.IsDefault)
			fmt.Printf("commit:   %s\n", p.Ctx.ShortCommit)
			fmt.Println("images:")
			for _, plan := range p.Plans {
				// Under per-image registry versioning an image can be
				// decided differently from the release — under bump: auto
				// by its own commits — so say how. The same reason over a
				// different base is still a different version.
				switch {
				case plan.VersionReason == "" ||
					(plan.VersionReason == p.Version.Reason() && plan.Version == p.Ctx.Version):
					fmt.Printf("  %s:\n", plan.Image.ID)
				case plan.NoRelease != "":
					fmt.Printf("  %s: (%s)\n", plan.Image.ID, plan.NoRelease)
				default:
					fmt.Printf("  %s: %s (%s)\n", plan.Image.ID, plan.Version, plan.VersionReason)
				}
				for _, ref := range plan.Refs {
					fmt.Printf("    - %s\n", ref)
				}
			}
			fmt.Println("config OK")
			return nil
		},
	}
	applyPrerelease = prereleaseFlag(cmd)
	return cmd
}
