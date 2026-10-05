// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"github.com/spf13/cobra"

	"github.com/blairham/stevedore/internal/pipeline"
)

func newPublishCmd() *cobra.Command {
	var (
		only            []string
		pinVersions     []string
		allowNonDefault bool
	)
	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Create the GitHub release and announce, once, after a matrix release",
		Long: "publish is the last step of a matrix release. Each matrix job runs\n" +
			"`release --only <entry.only> <entry.pins>`, which builds, gates, signs,\n" +
			"tags, notifies and advances its markers but — like a split leg — creates\n" +
			"no GitHub release and posts no announcement, so N jobs do not publish N\n" +
			"times. Run publish once after the matrix, passing every built entry's\n" +
			"--only and --pin-version, to write the changelog, create the GitHub\n" +
			"release and announce. It builds and pushes nothing.",
		RunE: func(c *cobra.Command, _ []string) error {
			o, err := baseOptions(c.Context())
			if err != nil {
				return err
			}
			o.Only = only
			pins, err := parsePins(pinVersions)
			if err != nil {
				return err
			}
			o.PinVersions = pins
			o.AllowNonDefaultBranch = allowNonDefault
			return pipeline.Publish(o)
		},
	}
	cmd.Flags().StringSliceVar(&only, "only", nil, "image id(s) the matrix built (the plan step's flat only output); default every image, as does 'all'")
	cmd.Flags().StringArrayVar(&pinVersions, "pin-version", nil, "pin an image's version as id=version (repeatable; the plan entries' `pins`), so the release is named after what was pushed")
	cmd.Flags().BoolVar(&allowNonDefault, "allow-non-default-branch", false, "publish a real release from a commit that is not on default_branch")
	return cmd
}
