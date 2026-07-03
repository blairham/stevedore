package verifier

import (
	"os"
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
