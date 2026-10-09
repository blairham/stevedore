// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package promote copies a released image, by digest, to other tags and
// repositories without rebuilding it, so the promoted image keeps the digest —
// and with it the signatures and attestations — of the one that was gated.
package promote

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/blairham/stevedore/internal/progress"
	"github.com/blairham/stevedore/internal/run"
	"github.com/blairham/stevedore/internal/verifier"
)

// Options describes one promotion.
type Options struct {
	// Source is the repository the released image lives in.
	Source string
	// From is a tag of Source, or a digest (sha256:…).
	From string
	// Tags are the tags to point at the digest in every destination.
	Tags []string
	// Repos are the destination repositories; Source may be among them.
	Repos []string
	// Verify authenticates the signature checked before anything is copied
	// and again in every destination before it is tagged.
	Verify verifier.Options
}

// legacySuffixes are the tag suffixes of cosign's tag-based storage
// (sha256-<hex>.sig and friends), which predates OCI referrers and which
// `oras copy -r` does not follow.
var legacySuffixes = []string{".sig", ".att", ".sbom"}

// dryRunDigest stands in for the digest a dry run could not resolve.
const dryRunDigest = "sha256:<digest-of-from>"

// Promote verifies the source's signature, copies the image with its
// signatures and attestations to every destination repository that is not the
// source, verifies the signature there, and only then applies the tags. It
// returns the promoted digest.
func Promote(r *run.Runner, o Options, out io.Writer) (string, error) {
	if len(o.Tags) == 0 {
		return "", errors.New("promote: at least one --to tag is required")
	}
	cross := false
	for _, repo := range o.Repos {
		if repo != o.Source {
			cross = true
		}
	}
	if err := requireTools(r, cross); err != nil {
		return "", err
	}
	digest, err := resolve(r, o.Source, o.From)
	if err != nil {
		return "", err
	}
	src := o.Source + "@" + digest
	progress.Printf(out, "==> promoting %s\n", src)
	if err := verifySignature(r, src, o.Verify, out); err != nil {
		return "", fmt.Errorf("source not promotable: %w", err)
	}
	for _, repo := range o.Repos {
		if repo == o.Source {
			continue
		}
		if err := copyTo(r, src, repo, digest, out); err != nil {
			return "", err
		}
		if err := verifySignature(r, repo+"@"+digest, o.Verify, out); err != nil {
			return "", fmt.Errorf("copy to %s did not carry the signature: %w", repo, err)
		}
	}
	for _, repo := range o.Repos {
		for _, tag := range o.Tags {
			progress.Printf(out, "    tag %s:%s\n", repo, tag)
			if err := r.Run("crane", "tag", repo+"@"+digest, tag); err != nil {
				return "", fmt.Errorf("tag %s:%s: %w", repo, tag, err)
			}
		}
	}
	return digest, nil
}

func requireTools(r *run.Runner, cross bool) error {
	if r.DryRun {
		return nil
	}
	tools := []string{"crane", "cosign"}
	if cross {
		tools = append(tools, "oras")
	}
	var missing []string
	for _, t := range tools {
		if !run.Has(t) {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("promote needs %s on PATH", strings.Join(missing, ", "))
	}
	return nil
}

// resolve returns the digest From names in source. A dry run that cannot
// reach the registry previews with a placeholder.
func resolve(r *run.Runner, source, from string) (string, error) {
	if strings.HasPrefix(from, "sha256:") {
		return from, nil
	}
	if from == "" {
		return "", errors.New("promote: --from (a tag or sha256 digest) is required")
	}
	d, err := r.Capture("crane", "digest", source+":"+from)
	if err != nil {
		if r.DryRun {
			return dryRunDigest, nil
		}
		return "", fmt.Errorf("resolve %s:%s: %w", source, from, err)
	}
	return strings.TrimSpace(d), nil
}

// copyTo copies src to repo by digest with its OCI referrers — cosign's
// bundle-format signatures and attestations — then any tag-based (legacy)
// cosign signature, attestation and SBOM tags. `cosign copy` is not used: on
// cosign v3 bundle signatures it copies no signature and overwrites the
// destination's referrers tag with the image index.
func copyTo(r *run.Runner, src, repo, digest string, out io.Writer) error {
	dst := repo + "@" + digest
	progress.Printf(out, "    copy %s\n", dst)
	if err := r.Run("oras", "copy", "-r", src, dst); err != nil {
		return fmt.Errorf("copy to %s: %w", repo, err)
	}
	source := strings.TrimSuffix(src, "@"+digest)
	base := strings.Replace(digest, ":", "-", 1)
	for _, suffix := range legacySuffixes {
		tag := base + suffix
		if r.DryRun {
			continue
		}
		if _, err := r.Capture("crane", "digest", source+":"+tag); err != nil {
			continue // no tag-based artifact of this kind
		}
		if err := r.Run("crane", "copy", source+":"+tag, repo+":"+tag); err != nil {
			return fmt.Errorf("copy %s to %s: %w", tag, repo, err)
		}
	}
	return nil
}

func verifySignature(r *run.Runner, ref string, o verifier.Options, out io.Writer) error {
	o.SBOM, o.Provenance = false, false
	checks, err := verifier.Verify(r, ref, o)
	if err != nil {
		return err
	}
	for _, c := range checks {
		if !c.OK {
			return fmt.Errorf("%s of %s: %s", c.Name, ref, c.Detail)
		}
		progress.Printf(out, "    ✓ %s %s\n", c.Name, ref)
	}
	return nil
}
