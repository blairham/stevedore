// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/promote"
	"github.com/blairham/stevedore/internal/run"
	"github.com/blairham/stevedore/internal/verifier"
)

func newPromoteCmd() *cobra.Command {
	var (
		from     string
		to       []string
		toRepo   []string
		key      string
		identity string
		issuer   string
	)
	cmd := &cobra.Command{
		Use:   "promote <image-id>",
		Short: "Copy a released image by digest to other tags or repositories, without rebuilding",
		Long: "promote points new tags — in the image's own repositories or in others —\n" +
			"at the exact digest an earlier release built, gated and signed, so\n" +
			"promotion (dev → staging → prod) and rollback never rebuild and never\n" +
			"change the digest.\n\n" +
			"The source is the image's first configured repository, at --from (a tag\n" +
			"or sha256 digest). Its cosign signature is verified first. Destinations\n" +
			"are --to-repo (repeatable), or every configured repository of the image.\n" +
			"A destination that is not the source receives the image with its\n" +
			"signatures and attestations (oras copy -r, plus any tag-based cosign\n" +
			"artifacts via crane), and its signature is verified again before any\n" +
			"tag is applied there. Tags are applied with crane tag.\n\n" +
			"Verification takes the same flags as `stevedore verify`.",
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			path, err := resolveConfigPath()
			if err != nil {
				return err
			}
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			img, err := findImage(cfg, args[0])
			if err != nil {
				return err
			}
			pubKey, err := verifyKey(key, identity != "" || issuer != "", cfg)
			if err != nil {
				return err
			}
			vo := verifier.Options{Key: pubKey, Identity: identity, Issuer: issuer}
			if verr := vo.Valid(); verr != nil && (!flagDryRun || errors.Is(verr, verifier.ErrKeyAndIdentity)) {
				return verr
			}
			repos := toRepo
			if len(repos) == 0 {
				repos = img.Repositories
			}
			r := run.New(c.Context(), flagDryRun, flagVerbose)
			digest, err := promote.Promote(r, promote.Options{
				Source: img.Repositories[0],
				From:   from,
				Tags:   to,
				Repos:  repos,
				Verify: vo,
			}, os.Stdout)
			if err != nil {
				return err
			}
			fmt.Printf("\npromoted %s\n", digest)
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "source tag or sha256 digest in the image's first repository (required)")
	cmd.Flags().StringArrayVar(&to, "to", nil, "tag to point at the promoted digest (repeatable, required)")
	cmd.Flags().StringArrayVar(&toRepo, "to-repo", nil, "destination repository (repeatable; default: the image's configured repositories)")
	cmd.Flags().StringVar(&key, "key", "", "cosign public key (default sign.cosign.public_key; omit for keyless)")
	cmd.Flags().StringVar(&identity, "certificate-identity", "", "expected certificate identity regexp, matched against the whole identity (keyless)")
	cmd.Flags().StringVar(&issuer, "certificate-oidc-issuer", "", "expected OIDC issuer regexp, matched against the whole issuer (keyless)")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}

// findImage returns the configured image with the given ID.
func findImage(cfg *config.Config, id string) (config.Image, error) {
	for _, img := range cfg.Images {
		if img.ID == id {
			return img, nil
		}
	}
	ids := make([]string, 0, len(cfg.Images))
	for _, img := range cfg.Images {
		ids = append(ids, img.ID)
	}
	return config.Image{}, fmt.Errorf("no image %q in the config (have: %v)", id, ids)
}
