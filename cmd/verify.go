// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/run"
	"github.com/blairham/stevedore/internal/verifier"
)

func newVerifyCmd() *cobra.Command {
	var (
		key      string
		identity string
		issuer   string
		noSBOM   bool
		noProv   bool
	)
	cmd := &cobra.Command{
		Use:   "verify <image-ref>",
		Short: "Verify the signature, SBOM attestation, and provenance of a pushed image",
		Long: "verify checks the supply-chain artifacts stevedore attaches during a\n" +
			"release: the cosign signature, the SBOM attestation, and the SLSA build\n" +
			"provenance. Pass a full reference (repo:tag or repo@sha256:...).\n\n" +
			"For keyless (OIDC) signatures, provide --certificate-identity and\n" +
			"--certificate-oidc-issuer; both are regexps that must match the whole\n" +
			"value. For keyed signatures, provide --key (the public key), or set\n" +
			"sign.cosign.public_key in the config. The two modes are exclusive.",
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			ref := args[0]

			// Default the SBOM predicate type and key from config when available.
			sbomType := "spdxjson"
			cfg := tryLoadConfig()
			if cfg != nil {
				sbomType = cfg.SBOM.Format
			}
			pubKey, err := verifyKey(key, identity != "" || issuer != "", cfg)
			if err != nil {
				return err
			}

			o := verifier.Options{
				Key:        pubKey,
				Identity:   identity,
				Issuer:     issuer,
				SBOM:       !noSBOM,
				SBOMType:   sbomType,
				Provenance: !noProv,
			}
			// A dry run previews without an identity, but a contradictory
			// pair of flags is a usage error either way.
			if verr := o.Valid(); verr != nil && (!flagDryRun || errors.Is(verr, verifier.ErrKeyAndIdentity)) {
				return verr
			}

			r := run.New(c.Context(), flagDryRun, flagVerbose)
			checks, err := verifier.Verify(r, ref, o)
			if err != nil {
				return err
			}

			allOK := true
			fmt.Printf("verifying %s\n", ref)
			for _, c := range checks {
				mark := "✓"
				if !c.OK {
					mark = "✗"
					allOK = false
				}
				fmt.Printf("  %s  %-16s %s\n", mark, c.Name, c.Detail)
			}
			if !allOK {
				return fmt.Errorf("verification failed")
			}
			fmt.Println("\nall checks passed")
			return nil
		},
	}
	cmd.Flags().StringVar(&key, "key", "", "cosign public key (default sign.cosign.public_key; omit for keyless)")
	cmd.Flags().
		StringVar(&identity, "certificate-identity", "", "expected certificate identity regexp, matched against the whole identity (keyless)")
	cmd.Flags().
		StringVar(&issuer, "certificate-oidc-issuer", "", "expected OIDC issuer regexp, matched against the whole issuer (keyless)")
	cmd.Flags().BoolVar(&noSBOM, "no-sbom", false, "skip SBOM attestation verification")
	cmd.Flags().BoolVar(&noProv, "no-provenance", false, "skip provenance verification")
	return cmd
}

// verifyKey picks the public key verify passes to cosign. An explicit --key
// wins; otherwise sign.cosign.public_key, unless identity flags ask for
// keyless verification. sign.cosign.key is never used: it is the private
// signing key, which cosign cannot verify with, so a keyed config that names
// no public key is an error that says what to set instead.
func verifyKey(flagKey string, keyless bool, cfg *config.Config) (string, error) {
	if flagKey != "" || keyless || cfg == nil {
		return flagKey, nil
	}
	if cfg.Sign.Cosign.PublicKey != "" {
		return cfg.Sign.Cosign.PublicKey, nil
	}
	if cfg.Sign.Cosign.Key != "" {
		return "", fmt.Errorf(
			"sign.cosign.key is the private signing key and cannot verify; set sign.cosign.public_key or pass --key <public key>",
		)
	}
	return "", nil
}

// tryLoadConfig loads the config if one is discoverable, returning nil
// otherwise. verify works without a config (all inputs can come from flags).
func tryLoadConfig() *config.Config {
	path, err := resolveConfigPath()
	if err != nil {
		return nil
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil
	}
	return cfg
}
