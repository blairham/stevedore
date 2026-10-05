// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"errors"
	"fmt"

	"github.com/blairham/stevedore/internal/config"
)

// stageOff is a release stage that will not run, and why.
type stageOff struct {
	stage string
	why   string // "--skip-scan was passed", "scan.enabled is false"
}

// stagesOff lists the gate and supply-chain stages this run will not perform,
// in pipeline order: skipped by flag, or disabled in the config.
func stagesOff(o Options, cfg *config.Config) []stageOff {
	stages := []struct {
		name    string
		skipped bool
		flag    string
		enabled bool
		key     string
	}{
		{config.StageScan, o.SkipScan, "--skip-scan", cfg.Scan.Enabled, "scan.enabled"},
		{config.StageTest, o.SkipTest, "--skip-test", cfg.Test.Enabled, "test.enabled"},
		{config.StageSign, o.SkipSign, "--skip-sign", cfg.Sign.Cosign.Enabled, "sign.cosign.enabled"},
		{config.StageSBOM, o.SkipSBOM, "--skip-sbom", cfg.SBOM.Enabled, "sbom.enabled"},
	}
	var off []stageOff
	for _, s := range stages {
		switch {
		case s.skipped:
			off = append(off, stageOff{s.name, s.flag + " was passed"})
		case !s.enabled:
			off = append(off, stageOff{s.name, s.key + " is false"})
		}
	}
	return off
}

// isRealRelease reports whether this run publishes a release under a real
// version: not a snapshot, not a --no-push validation, and not a split leg
// (which pushes untagged digests and leaves the stages to the merge run).
func isRealRelease(o Options) bool {
	return !o.Snapshot && !o.NoPush && len(o.SplitPlatforms) == 0
}

// enforcePolicy refuses a real release that would go without a stage
// policy.require names. Without it, adding --skip-scan to a red pipeline
// turns it green and ships an unscanned image under a real version.
func enforcePolicy(o Options, cfg *config.Config) error {
	if !isRealRelease(o) {
		return nil
	}
	var errs []error
	for _, s := range stagesOff(o, cfg) {
		if cfg.Policy.Requires(s.stage) {
			errs = append(errs, fmt.Errorf("policy.require: %s is required on a real release, but %s", s.stage, s.why))
		}
	}
	return errors.Join(errs...)
}

// degradedStages names the stages a real release ran without, for the
// summary. A snapshot or validation build is expected to skip them and is not
// marked.
func degradedStages(o Options, cfg *config.Config) []string {
	if !isRealRelease(o) {
		return nil
	}
	var names []string
	for _, s := range stagesOff(o, cfg) {
		names = append(names, s.stage)
	}
	return names
}
