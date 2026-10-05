// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"github.com/spf13/cobra"

	"github.com/blairham/stevedore/internal/pipeline"
)

func newBuildCmd() *cobra.Command {
	var push bool
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build images locally (single platform, loaded into the docker daemon)",
		Long: "build is the inner-loop command: it builds each image for one platform and\n" +
			"loads it into the local docker daemon without pushing. Use --push to publish.",
		RunE: func(c *cobra.Command, _ []string) error {
			o, err := baseOptions(c.Context())
			if err != nil {
				return err
			}
			o.Snapshot = true // local builds are always snapshots
			if push {
				return pipeline.Release(pipeline.BuildPushOptions(o))
			}
			return pipeline.Build(o)
		},
	}
	cmd.Flags().BoolVar(&push, "push", false, "push instead of loading locally (multi-arch)")
	return cmd
}
