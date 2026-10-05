# Supply chain

Signing, SBOMs, provenance, and the gates that run before any of it.

stevedore is secure-by-default: every release is signed, gets an SBOM, and (when
enabled) carries SLSA build provenance. The pipeline runs in this order so a bad
image never gets signed or tagged:

```
build + push by digest → scan (gate) → smoke test (gate) → sign → SBOM + attest
      → tag → changelog → publish
```

The build is pushed **untagged, by digest** (provenance is attached by BuildKit
at this point), and every later stage addresses it as `repo@sha256:…`. Only
when the gates have passed and the digest is signed does stevedore apply the
tags — every tag on every repository, `latest` included — with
`docker buildx imagetools create --prefer-index=false`, which copies the
manifest rather than wrapping it, so each tag names exactly the digest that was
scanned, tested, and signed.

- **Scan gate** — `scan.fail_on` blocks the release if the built image has a
  vulnerability at or above the given severity (defaults to `critical`).
- **Smoke test gate** — `test.cmd` runs the image and blocks the release unless it
  exits `test.expect_exit`. Don't sign or ship an image that doesn't even start.
- **Signing** — cosign, keyed or keyless (OIDC), always by digest.
- **SBOM** — syft, optionally attached to the image as a signed attestation.
- **Provenance** — BuildKit SLSA provenance (`mode=max` records the full build).

Both gates run *before* signing and tagging, so a failing image is never signed
or tagged: it exists in the registry only as an untagged digest that nothing
points at, and the tags from the previous release stay where they were.
Signing comes before tagging too, so no tag ever names an unsigned image, even
briefly.

Pushing by digest needs a builder that supports it — the `docker-container`
driver (what `docker/setup-buildx-action` and the stevedore action create), or
`kubernetes`/`remote`. The plain `docker` driver cannot push by digest; locally,
`docker buildx create --use` once is enough.

Verify any published image round-trips:

```sh
stevedore verify ghcr.io/acme/myapp:1.4.0 \
  --certificate-identity "https://github.com/acme/myapp/.*" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com"
```
