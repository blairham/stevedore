// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/blairham/stevedore/internal/pipeline"
)

// planEnv names the variable a matrix job or merge step can carry the plan
// document in, so the release summary keeps the plan's reason for each image
// instead of "selected via --only". An environment variable rather than a
// flag: the document holds spaces, and the action word-splits its args.
const planEnv = "STEVEDORE_PLAN"

// planReasons reads the plan's per-image reasons from $STEVEDORE_PLAN, if set.
func planReasons() (map[string]string, error) {
	data := os.Getenv(planEnv)
	if strings.TrimSpace(data) == "" {
		return map[string]string{}, nil
	}
	reasons, err := pipeline.ReasonsFromPlan([]byte(data))
	if err != nil {
		return nil, fmt.Errorf("$%s: %w", planEnv, err)
	}
	return reasons, nil
}

// parsePins parses repeated --pin-version id=version flags into a map.
func parsePins(pins []string) (map[string]string, error) {
	out := make(map[string]string, len(pins))
	for _, p := range pins {
		id, ver, ok := strings.Cut(p, "=")
		if !ok || id == "" || ver == "" {
			return nil, fmt.Errorf("--pin-version %q: want id=version", p)
		}
		out[id] = ver
	}
	return out, nil
}

func newReleaseCmd() *cobra.Command {
	var (
		snapshot        bool
		skipSign        bool
		skipSBOM        bool
		skipScan        bool
		skipTest        bool
		noPush          bool
		parallel        int
		skipChangelog   bool
		skipPublish     bool
		onlyChanged     bool
		changedSince    string
		output          string
		only            []string
		pinVersions     []string
		split           []string
		keepGoing       bool
		allowNonDefault bool
	)
	cmd := &cobra.Command{
		Use:   "release",
		Short: "Build multi-arch images, push, sign, generate SBOM and changelog",
		Long: "release runs the full pipeline: build every image for all platforms,\n" +
			"push to all configured registries, sign with cosign, generate SBOMs, and\n" +
			"write a changelog. A clean, tagged checkout is required unless --snapshot.",
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
			o.NoPush = noPush
			o.Parallel = parallel
			o.SkipChangelog = skipChangelog
			o.SkipPublish = skipPublish
			o.OnlyChanged = onlyChanged
			o.ChangedSince = changedSince
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
			o.SplitPlatforms = split
			o.KeepGoing = keepGoing
			o.AllowNonDefaultBranch = allowNonDefault
			return pipeline.Release(o)
		},
	}
	cmd.Flags().BoolVar(&snapshot, "snapshot", false, "release without a tag/clean tree (skips floating tags)")
	cmd.Flags().BoolVar(&skipSign, "skip-sign", false, "skip cosign signing")
	cmd.Flags().BoolVar(&skipSBOM, "skip-sbom", false, "skip SBOM generation")
	cmd.Flags().BoolVar(&skipScan, "skip-scan", false, "skip vulnerability scanning")
	cmd.Flags().BoolVar(&skipTest, "skip-test", false, "skip the post-build smoke test")
	cmd.Flags().
		BoolVar(&noPush, "no-push", false, "build (and change-detect) without pushing; skips the scan, smoke test, signing, SBOM and publish")
	cmd.Flags().IntVar(&parallel, "parallel", 1, "build up to N images concurrently")
	cmd.Flags().BoolVar(&skipChangelog, "skip-changelog", false, "skip changelog generation")
	cmd.Flags().
		BoolVar(&onlyChanged, "only-changed", false, "skip images whose build inputs are unchanged since the last release (fingerprint state)")
	cmd.Flags().
		StringVar(&changedSince, "changed-since", "", "git ref: only build images whose paths changed since this ref (stateless, CI-native; under marker_refs, since the older of this and the image's marker)")
	cmd.Flags().
		StringSliceVar(&only, "only", nil, "image id(s) to build unconditionally, skipping change detection (matrix mode: one plan entry per job); 'all' selects every image")
	cmd.Flags().
		StringArrayVar(&pinVersions, "pin-version", nil, "pin an image's version as id=version (repeatable; from the plan's pins)")
	cmd.Flags().
		StringVar(&output, "output", "text", "output format: text or json (json emits a release summary to stdout)")
	cmd.Flags().
		BoolVar(&skipPublish, "skip-publish", false, "skip the GitHub/GitLab release, announcements, and notify webhooks")
	cmd.Flags().
		BoolVar(&keepGoing, "keep-going", false, "build every image even after one fails, then fail at the end (the ones that built are still tagged, recorded and notified)")
	cmd.Flags().
		StringSliceVar(&split, "split", nil, "platform(s) to build natively on this runner, pushed untagged by digest for a later stevedore merge (native multi-arch CI: one matrix leg per arch)")
	cmd.Flags().
		BoolVar(&allowNonDefault, "allow-non-default-branch", false, "publish a real release from a commit that is not on default_branch")
	return cmd
}
