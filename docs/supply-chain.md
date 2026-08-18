# Supply chain

Signing, SBOMs, provenance, and the gates that run before any of it.

stevedore is secure-by-default: every release is signed, gets an SBOM, and (when
enabled) carries SLSA build provenance. The pipeline runs in this order so a bad
image never gets signed or shipped:

```
build → scan (gate) → smoke test (gate) → sign → SBOM + attest → provenance → changelog → publish
```

- **Scan gate** — `scan.fail_on` blocks the release if the built image has a
  vulnerability at or above the given severity (defaults to `critical`).
- **Smoke test gate** — `test.cmd` runs the image and blocks the release unless it
  exits `test.expect_exit`. Don't sign or ship an image that doesn't even start.
- **Signing** — cosign, keyed or keyless (OIDC), always by digest.
- **SBOM** — syft, optionally attached to the image as a signed attestation.
- **Provenance** — BuildKit SLSA provenance (`mode=max` records the full build).

Both gates run *before* signing, so a failing image is never signed or published.

Verify any published image round-trips:

```sh
stevedore verify ghcr.io/acme/myapp:1.4.0 \
  --certificate-identity "https://github.com/acme/myapp/.*" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com"
```
