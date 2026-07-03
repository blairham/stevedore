package cmd

import (
	"github.com/blairham/stevedore/internal/pipeline"
	"github.com/spf13/cobra"
)

func newReleaseCmd() *cobra.Command {
	var (
		snapshot      bool
		skipSign      bool
		skipSBOM      bool
		skipScan      bool
		skipTest      bool
		noPush        bool
		skipChangelog bool
		skipPublish   bool
		onlyChanged   bool
		changedSince  string
		output        string
	)
	cmd := &cobra.Command{
		Use:   "release",
		Short: "Build multi-arch images, push, sign, generate SBOM and changelog",
		Long: "release runs the full pipeline: build every image for all platforms,\n" +
			"push to all configured registries, sign with cosign, generate SBOMs, and\n" +
			"write a changelog. A clean, tagged checkout is required unless --snapshot.",
		RunE: func(_ *cobra.Command, _ []string) error {
			o, err := baseOptions()
			if err != nil {
				return err
			}
			o.Snapshot = snapshot
			o.SkipSign = skipSign
			o.SkipSBOM = skipSBOM
			o.SkipScan = skipScan
			o.SkipTest = skipTest
			o.NoPush = noPush
			o.SkipChangelog = skipChangelog
			o.SkipPublish = skipPublish
			o.OnlyChanged = onlyChanged
			o.ChangedSince = changedSince
			o.OutputJSON = output == "json"
			return pipeline.Release(o)
		},
	}
	cmd.Flags().BoolVar(&snapshot, "snapshot", false, "release without a tag/clean tree (skips floating tags)")
	cmd.Flags().BoolVar(&skipSign, "skip-sign", false, "skip cosign signing")
	cmd.Flags().BoolVar(&skipSBOM, "skip-sbom", false, "skip SBOM generation")
	cmd.Flags().BoolVar(&skipScan, "skip-scan", false, "skip vulnerability scanning")
	cmd.Flags().BoolVar(&skipTest, "skip-test", false, "skip the post-build smoke test")
	cmd.Flags().BoolVar(&noPush, "no-push", false, "build (and change-detect) without pushing; skips sign/sbom/scan/publish")
	cmd.Flags().BoolVar(&skipChangelog, "skip-changelog", false, "skip changelog generation")
	cmd.Flags().BoolVar(&onlyChanged, "only-changed", false, "skip images whose build inputs are unchanged since the last release (fingerprint state)")
	cmd.Flags().StringVar(&changedSince, "changed-since", "", "git ref: only build images whose paths changed since this ref (stateless, CI-native)")
	cmd.Flags().StringVar(&output, "output", "text", "output format: text or json (json emits a release summary to stdout)")
	cmd.Flags().BoolVar(&skipPublish, "skip-publish", false, "skip GitHub release creation and announcements")
	return cmd
}
