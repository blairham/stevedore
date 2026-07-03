// Package signer signs pushed images with cosign.
package signer

import (
	"fmt"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/run"
)

// Sign signs each repo at the given digest with cosign. Signing by digest
// (repo@sha256:...) is deliberate: it pins the exact artifact regardless of
// how many mutable tags point at it.
func Sign(r *run.Runner, cfg config.Cosign, repos []string, digest string) error {
	if !cfg.Enabled {
		return nil
	}
	if !r.DryRun && !run.Has("cosign") {
		return fmt.Errorf("sign.cosign.enabled but cosign not found on PATH")
	}
	if digest == "" && !r.DryRun {
		return fmt.Errorf("cannot sign: no image digest (was the image pushed?)")
	}
	for _, repo := range repos {
		ref := repo + "@" + digest
		args := []string{"sign", "--yes"}
		if cfg.Key != "" {
			args = append(args, "--key", cfg.Key)
		}
		args = append(args, cfg.Args...)
		args = append(args, ref)
		if err := r.Run("cosign", args...); err != nil {
			return fmt.Errorf("cosign sign %s: %w", ref, err)
		}
	}
	return nil
}

// Attest attaches a signed SBOM attestation to each repo at digest.
func Attest(r *run.Runner, cfg config.Cosign, repos []string, digest, predicatePath, predicateType string) error {
	if !cfg.Enabled {
		return nil
	}
	if !r.DryRun && !run.Has("cosign") {
		return fmt.Errorf("sbom.attest requires cosign, not found on PATH")
	}
	for _, repo := range repos {
		ref := repo + "@" + digest
		args := []string{"attest", "--yes", "--predicate", predicatePath, "--type", predicateType}
		if cfg.Key != "" {
			args = append(args, "--key", cfg.Key)
		}
		args = append(args, ref)
		if err := r.Run("cosign", args...); err != nil {
			return fmt.Errorf("cosign attest %s: %w", ref, err)
		}
	}
	return nil
}
