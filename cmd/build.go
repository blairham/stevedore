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
		RunE: func(_ *cobra.Command, _ []string) error {
			o, err := baseOptions()
			if err != nil {
				return err
			}
			o.Snapshot = true // local builds are always snapshots
			if push {
				// build --push publishes multi-arch but skips the release
				// extras (sign/sbom/changelog) — that's what `release` is for.
				o.Push = true
				o.SkipSign = true
				o.SkipSBOM = true
				o.SkipChangelog = true
				return pipeline.Release(o)
			}
			return pipeline.Build(o)
		},
	}
	cmd.Flags().BoolVar(&push, "push", false, "push instead of loading locally (multi-arch)")
	return cmd
}
