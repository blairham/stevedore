// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package verifier checks the supply-chain artifacts attached to a pushed
// image: the cosign signature, the SBOM attestation, and the SLSA provenance.
package verifier

import (
	"errors"
	"fmt"
	"strings"

	"github.com/blairham/stevedore/internal/run"
)

// Options controls how verification authenticates.
type Options struct {
	// Key is a cosign public key path. When empty, keyless verification is used
	// and Identity/Issuer are required.
	Key string
	// Identity is the expected certificate identity (regexp) for keyless
	// verification, e.g. the workflow URL or an email. It must match the whole
	// identity: authArgs anchors it, so "release@acme.com" does not also
	// accept "release@acme.com.evil.io".
	Identity string
	// Issuer is the expected OIDC issuer (regexp, anchored like Identity) for
	// keyless verification.
	Issuer string
	// SBOM, when true, verifies the SBOM attestation.
	SBOM bool
	// SBOMType is the attestation predicate type (spdxjson or cyclonedx).
	SBOMType string
	// Provenance, when true, checks for a SLSA provenance attestation.
	Provenance bool
}

// Check is the outcome of one verification step.
type Check struct {
	Name   string
	OK     bool
	Detail string
}

// Verify runs the requested checks against ref and returns one Check per step.
// The returned error is non-nil only on an internal failure; a failed check is
// reported via Check.OK, not the error.
func Verify(r *run.Runner, ref string, o Options) ([]Check, error) {
	if !run.Has("cosign") && !r.DryRun {
		return nil, fmt.Errorf("cosign not found on PATH")
	}
	var checks []Check

	// 1. Signature.
	sigArgs := append([]string{"verify"}, authArgs(o)...)
	sigArgs = append(sigArgs, ref)
	checks = append(checks, runCheck(r, "signature", "cosign", sigArgs))

	// 2. SBOM attestation.
	if o.SBOM {
		attArgs := append([]string{"verify-attestation", "--type", predicateType(o.SBOMType)}, authArgs(o)...)
		attArgs = append(attArgs, ref)
		checks = append(checks, runCheck(r, "sbom-attestation", "cosign", attArgs))
	}

	// 3. SLSA provenance (BuildKit-native, read via imagetools).
	if o.Provenance {
		checks = append(checks, provenanceCheck(r, ref))
	}

	return checks, nil
}

// authArgs renders the cosign auth flags for keyed or keyless verification.
func authArgs(o Options) []string {
	if o.Key != "" {
		return []string{"--key", o.Key}
	}
	var args []string
	if o.Identity != "" {
		args = append(args, "--certificate-identity-regexp", anchor(o.Identity))
	}
	if o.Issuer != "" {
		args = append(args, "--certificate-oidc-issuer-regexp", anchor(o.Issuer))
	}
	return args
}

// anchor makes a regexp match the whole string. cosign matches the identity
// and issuer regexps unanchored, so a bare identity would accept any
// certificate that merely contains it. The group keeps an alternation from
// escaping the anchors, and a pattern that is already anchored still means the
// same thing.
func anchor(re string) string {
	return "^(?:" + re + ")$"
}

func runCheck(r *run.Runner, name, exe string, args []string) Check {
	if r.DryRun {
		r.Preview(exe, args...)
		return Check{Name: name, OK: true, Detail: "dry-run"}
	}
	out, err := r.Capture(exe, args...)
	if err != nil {
		return Check{Name: name, OK: false, Detail: firstLine(err.Error())}
	}
	return Check{Name: name, OK: true, Detail: firstLine(out)}
}

// provenanceCheck reports whether ref carries a provenance attestation, read via
// `docker buildx imagetools inspect`.
func provenanceCheck(r *run.Runner, ref string) Check {
	const name = "provenance"
	if r.DryRun {
		r.Preview("docker", "buildx", "imagetools", "inspect", "--format", "{{ json .Provenance }}", ref)
		return Check{Name: name, OK: true, Detail: "dry-run"}
	}
	out, err := r.Capture("docker", "buildx", "imagetools", "inspect", "--format", "{{ json .Provenance }}", ref)
	if err != nil {
		return Check{Name: name, OK: false, Detail: firstLine(err.Error())}
	}
	trimmed := strings.TrimSpace(out)
	if trimmed == "" || trimmed == "null" || trimmed == "{}" {
		return Check{Name: name, OK: false, Detail: "no provenance attestation found"}
	}
	return Check{Name: name, OK: true, Detail: "provenance attestation present"}
}

// ErrKeyAndIdentity reports that both a key and keyless identity flags were
// given. They select different verification modes, and cosign would silently
// ignore one of them.
var ErrKeyAndIdentity = errors.New(
	"--key and --certificate-identity/--certificate-oidc-issuer are mutually exclusive: a keyed signature has no certificate identity to check",
)

// Valid reports whether the options are usable: keyed or keyless, not both,
// and keyless needs an identity.
func (o Options) Valid() error {
	if o.Key != "" && (o.Identity != "" || o.Issuer != "") {
		return ErrKeyAndIdentity
	}
	if o.Key == "" && o.Identity == "" {
		return fmt.Errorf("keyless verification needs --certificate-identity (or provide --key)")
	}
	return nil
}

// predicateType maps a syft/SBOM format to the cosign attestation predicate
// type name, matching what stevedore uses when attesting (see sbom.PredicateType).
func predicateType(t string) string {
	switch t {
	case "cyclonedx", "cyclonedx-json":
		return "cyclonedx"
	default:
		return "spdxjson"
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
