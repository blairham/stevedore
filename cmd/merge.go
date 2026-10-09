// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"github.com/spf13/cobra"

	"github.com/blairham/stevedore/internal/pipeline"
)

func newMergeCmd() *cobra.Command {
	var (
		snapshot        bool
		skipSign        bool
		skipSBOM        bool
		skipScan        bool
		skipTest        bool
		skipChangelog   bool
		skipPublish     bool
		output          string
		only            []string
		pinVersions     []string
		keepGoing       bool
		allowNonDefault bool
	)
	cmd := &cobra.Command{
		Use:   "merge",
		Short: "Assemble split per-arch builds into manifest lists and finish the release",
		Long: "merge is the second half of a split release. Matrix jobs first run\n" +
			"`release --split <platform>` on native runners — each builds one platform\n" +
			"and pushes it untagged, by digest, recording the digest under dist/digests/.\n" +
			"merge then stitches those digests into one manifest list per image\n" +
			"(docker buildx imagetools create, pushed by digest) and runs the release\n" +
			"tail on the merged artifact: scan, smoke test, sign, SBOM — and only then\n" +
			"tags it — followed by changelog, GitHub release, announce, notify.\n" +
			"In CI, upload dist/digests/ from every leg and download it before merging.",
		RunE: func(c *cobra.Command, _ []string) error {
			o, err := baseOptions(c.Context())
			if err != nil {
				return err
			}
			o.Snapshot = snapshot
			o.SkipSign = skipSign
			o.SkipSBOM = skipSBOM
			o.SkipScan = skipScan
			o.SkipTest = skipTest
			o.SkipChangelog = skipChangelog
			o.SkipPublish = skipPublish
			o.OutputJSON = output == "json"
			o.Only = only
			pins, err := parsePins(pinVersions)
			if err != nil {
				return err
			}
			o.PinVersions = pins
			if o.PlanReasons, err = planReasons(); err != nil {
				return err
			}
			o.KeepGoing = keepGoing
			o.AllowNonDefaultBranch = allowNonDefault
			return pipeline.Merge(o)
		},
	}
	cmd.Flags().BoolVar(&snapshot, "snapshot", false, "merge a snapshot release (skips floating tags)")
	cmd.Flags().BoolVar(&skipSign, "skip-sign", false, "skip cosign signing")
	cmd.Flags().BoolVar(&skipSBOM, "skip-sbom", false, "skip SBOM generation")
	cmd.Flags().BoolVar(&skipScan, "skip-scan", false, "skip vulnerability scanning")
	cmd.Flags().BoolVar(&skipTest, "skip-test", false, "skip the post-build smoke test")
	cmd.Flags().BoolVar(&skipChangelog, "skip-changelog", false, "skip changelog generation")
	cmd.Flags().
		BoolVar(&skipPublish, "skip-publish", false, "skip the GitHub/GitLab release, announcements, and notify webhooks")
	cmd.Flags().
		StringSliceVar(&only, "only", nil, "image id(s) to merge (matrix mode: match the split legs' --only); 'all' selects every image")
	cmd.Flags().
		StringArrayVar(&pinVersions, "pin-version", nil, "pin an image's version as id=version (repeatable; match the split legs' pins)")
	cmd.Flags().
		BoolVar(&keepGoing, "keep-going", false, "merge every image whose digests are complete even after one fails, then fail at the end (the ones that merged are still tagged, recorded and notified)")
	cmd.Flags().
		StringVar(&output, "output", "text", "output format: text or json (json emits a release summary to stdout)")
	cmd.Flags().
		BoolVar(&allowNonDefault, "allow-non-default-branch", false, "publish a real release from a commit that is not on default_branch")
	return cmd
}
