// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package verifier

import (
	"errors"
	"os"
	"regexp"
	"slices"
	"testing"

	"github.com/blairham/stevedore/internal/run"
)

func TestAuthArgs(t *testing.T) {
	if got := authArgs(Options{Key: "cosign.pub"}); got[0] != "--key" || got[1] != "cosign.pub" {
		t.Errorf("keyed authArgs = %v", got)
	}
	got := authArgs(Options{Identity: "https://github.com/x/.+", Issuer: "https://token.actions.githubusercontent.com"})
	if !slices.Contains(got, "--certificate-identity-regexp") || !slices.Contains(got, "--certificate-oidc-issuer-regexp") {
		t.Errorf("keyless authArgs missing flags: %v", got)
	}
}

// The identity and issuer reach cosign anchored at both ends, because cosign
// matches its regexp flags anywhere in the value. Compile the regexps cosign
// would receive and check what they accept.
func TestAuthArgsAnchorsIdentityAndIssuer(t *testing.T) {
	args := authArgs(Options{Identity: "release@acme.com", Issuer: "https://token.actions.githubusercontent.com|https://accounts.google.com"})
	value := func(flag string) *regexp.Regexp {
		i := slices.Index(args, flag)
		if i < 0 || i+1 >= len(args) {
			t.Fatalf("%s missing from %v", flag, args)
		}
		return regexp.MustCompile(args[i+1])
	}
	id := value("--certificate-identity-regexp")
	if !id.MatchString("release@acme.com") {
		t.Errorf("identity %q should accept the exact identity", id)
	}
	for _, bad := range []string{"release@acme.com.evil.io", "evil-release@acme.com"} {
		if id.MatchString(bad) {
			t.Errorf("identity %q accepts %q", id, bad)
		}
	}
	iss := value("--certificate-oidc-issuer-regexp")
	for _, ok := range []string{"https://token.actions.githubusercontent.com", "https://accounts.google.com"} {
		if !iss.MatchString(ok) {
			t.Errorf("issuer %q should accept %q", iss, ok)
		}
	}
	// The alternation must not escape the anchors.
	if iss.MatchString("https://token.actions.githubusercontent.com.evil.io") {
		t.Errorf("issuer %q accepts a suffixed issuer", iss)
	}
	// A pattern the user already anchored keeps its meaning.
	pre := regexp.MustCompile(anchor("^https://github\\.com/acme/.*$"))
	if !pre.MatchString("https://github.com/acme/app/.github/workflows/r.yml@refs/tags/v1") {
		t.Errorf("pre-anchored pattern %q stopped matching", pre)
	}
}

func TestPredicateType(t *testing.T) {
	cases := map[string]string{
		"":               "spdxjson",
		"spdx-json":      "spdxjson", // stevedore's default format -> cosign predicate name
		"cyclonedx":      "cyclonedx",
		"cyclonedx-json": "cyclonedx",
	}
	for in, want := range cases {
		if got := predicateType(in); got != want {
			t.Errorf("predicateType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOptionsValid(t *testing.T) {
	if err := (Options{}).Valid(); err == nil {
		t.Error("empty options (no key, no identity) should be invalid")
	}
	if err := (Options{Key: "k"}).Valid(); err != nil {
		t.Errorf("keyed should be valid: %v", err)
	}
	if err := (Options{Identity: "id"}).Valid(); err != nil {
		t.Errorf("keyless with identity should be valid: %v", err)
	}
	for _, o := range []Options{{Key: "k", Identity: "id"}, {Key: "k", Issuer: "iss"}} {
		if err := o.Valid(); !errors.Is(err, ErrKeyAndIdentity) {
			t.Errorf("%+v: key with identity flags should be refused, got %v", o, err)
		}
	}
}

func TestVerifyDryRun(t *testing.T) {
	// Dry-run skips the cosign presence check and marks each step OK.
	devnull, _ := os.Open(os.DevNull)
	r := &run.Runner{DryRun: true, Stdout: devnull, Stderr: devnull}
	checks, err := Verify(r, "repo:tag", Options{Key: "k", SBOM: true, Provenance: true})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, c := range checks {
		names[c.Name] = c.OK
	}
	for _, want := range []string{"signature", "sbom-attestation", "provenance"} {
		if !names[want] {
			t.Errorf("expected dry-run check %q to be present and OK: %+v", want, checks)
		}
	}
}
