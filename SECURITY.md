# Security policy

stevedore signs, attests and gates other people's releases, so a compromise here
reaches further than the tool itself. Reports are taken seriously and answered.

## Reporting a vulnerability

Use GitHub's [private vulnerability
reporting](https://github.com/blairham/stevedore/security/advisories/new). It
opens a channel visible only to the maintainers.

**Please do not open a public issue for a vulnerability.**

What helps: the stevedore version (`stevedore --version`), the relevant part of
your `.stevedore.yaml`, and what an attacker gets. A proof of concept is welcome
but not required to start the conversation.

Expect an acknowledgement within a few days. This is a personal project, not a
funded security team — if it is quiet, it is a calendar problem rather than
disinterest, and a nudge on the same thread is fine.

## Supported versions

| Version | Supported |
|---------|-----------|
| `v1.x` | ✅ |
| `v0.x` | ❌ — pre-v1, superseded |

Fixes land on the newest minor of the current major. The moving `v1` tag picks
them up with no workflow edit.

## Scope

**In scope** — anything where stevedore can be made to do something its config
did not ask for:

- publishing to a registry, tag or repository the config does not name
- signing or attesting an artifact that did not pass the scan and test gates,
  which run *before* signing precisely so this cannot happen
- leaking registry credentials, cloud credentials or signing material into logs,
  build args, image layers, the SBOM, or the JSON summary
- template/config injection reaching a shell — the `versioning.strategy: command`
  path runs a configured command by design, but nothing else should
- the GitHub Action escalating beyond the permissions the calling workflow granted

**Out of scope:**

- Vulnerabilities in the tools stevedore drives (`docker`, `cosign`, `syft`,
  `grype`, `trivy`, `crane`, `aws`, `gh`). Report those upstream. If stevedore
  *invokes* one of them unsafely, that is in scope and is our bug.
- A `.stevedore.yaml` you do not trust. The config is executable input in the
  same sense a `Makefile` is: it names commands to run. Running an untrusted
  config is equivalent to running an untrusted script.
- CVEs in the released image's base layer that the scan gate is configured to
  allow. `scan.fail_on` is yours to set.

## Verifying what you downloaded

Every stevedore release is signed with cosign (keyless, GitHub OIDC) and carries
an SBOM attestation and SLSA provenance. stevedore verifies its own artifacts
with the same command it gives you for yours:

```sh
stevedore verify ghcr.io/blairham/stevedore:1.0.0 \
  --certificate-identity "https://github.com/blairham/stevedore/.*" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com"
```

Or with cosign directly, if you would rather not use the tool you are checking:

```sh
cosign verify ghcr.io/blairham/stevedore:1.0.0 \
  --certificate-identity-regexp "https://github.com/blairham/stevedore/.*" \
  --certificate-oidc-issuer-regexp "https://token.actions.githubusercontent.com"
```

Binaries from the GitHub release have a `checksums.txt` alongside them; the macOS
builds are Developer ID signed and notarized.
