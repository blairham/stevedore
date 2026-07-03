package cmd

import (
	"fmt"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/run"
	"github.com/blairham/stevedore/internal/verifier"
	"github.com/spf13/cobra"
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
			"--certificate-oidc-issuer; for keyed signatures, provide --key.",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			ref := args[0]

			// Default the SBOM predicate type and key from config when available.
			sbomType := "spdxjson"
			if cfg := tryLoadConfig(); cfg != nil {
				sbomType = cfg.SBOM.Format
				if key == "" {
					key = cfg.Sign.Cosign.Key
				}
			}

			o := verifier.Options{
				Key:        key,
				Identity:   identity,
				Issuer:     issuer,
				SBOM:       !noSBOM,
				SBOMType:   sbomType,
				Provenance: !noProv,
			}
			if !flagDryRun {
				if err := o.Valid(); err != nil {
					return err
				}
			}

			r := run.New(flagDryRun, flagVerbose)
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
	cmd.Flags().StringVar(&key, "key", "", "cosign public key (omit for keyless)")
	cmd.Flags().StringVar(&identity, "certificate-identity", "", "expected certificate identity regexp (keyless)")
	cmd.Flags().StringVar(&issuer, "certificate-oidc-issuer", "", "expected OIDC issuer regexp (keyless)")
	cmd.Flags().BoolVar(&noSBOM, "no-sbom", false, "skip SBOM attestation verification")
	cmd.Flags().BoolVar(&noProv, "no-provenance", false, "skip provenance verification")
	return cmd
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
