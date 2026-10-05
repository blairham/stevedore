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

Expect an acknowledgement within 7 days. This is a personal project, not a
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

Every stevedore image is signed with cosign keyless signing (GitHub OIDC) and
carries an SBOM attestation and SLSA provenance. The signature is tied to the
workflow that built the release, not to a key someone could leak, so verify
against that workflow — not just "anything in this repository".

**Images.** stevedore verifies its own image with the same command it gives you
for yours:

```sh
stevedore verify ghcr.io/blairham/stevedore:1.0.0 \
  --certificate-identity '^https://github\.com/blairham/stevedore/\.github/workflows/release\.yml@refs/tags/v.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

`stevedore verify` matches the identity against the whole certificate
identity, hence the trailing `.*`; cosign's own regexp flag matches anywhere
in it, so anchor both ends yourself when you call cosign directly.

Or with cosign directly, if you would rather not use the tool you are checking:

```sh
cosign verify ghcr.io/blairham/stevedore:1.0.0 \
  --certificate-identity-regexp '^https://github\.com/blairham/stevedore/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

**Binaries.** Releases after `v1.0.1` sign `checksums.txt`, which lists the
digest of every archive. Verify the signature, then the archives against it:

```sh
VERSION=v1.0.2
cosign verify-blob \
  --certificate-identity "https://github.com/blairham/stevedore/.github/workflows/release.yml@refs/tags/$VERSION" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --bundle checksums.txt.sigstore.json checksums.txt
sha256sum --check --ignore-missing checksums.txt
```

A release re-run by hand (the workflow's `workflow_dispatch` input) is signed
by the ref it was dispatched from, usually `refs/heads/main`, rather than by
the tag, so use `@refs/heads/main` in `--certificate-identity` for that release.

The GitHub Action runs exactly these two checks on the archive it installs,
accepting either the tag or `main` as the signing ref, and fails rather than
running a binary it could not verify.

The macOS builds are additionally Developer ID signed and notarized.

**Build provenance.** From `v1.0.7`, every archive carries SLSA build
provenance recording that it was built by this repository's release workflow from the tagged
commit. It is stored in the repository's attestations and attached to the
release as `stevedore-<tag>.intoto.jsonl`. Check an archive with the
[GitHub CLI](https://cli.github.com/):

```sh
gh attestation verify stevedore_*_Linux_x86_64.tar.gz --repo blairham/stevedore
```

or offline, against the bundle attached to the release:

```sh
VERSION=v1.0.7
gh attestation verify stevedore_*_Linux_x86_64.tar.gz --repo blairham/stevedore \
  --bundle "stevedore-$VERSION.intoto.jsonl"
```

`v1.0.6` alone attaches `stevedore.intoto.jsonl` from slsa-github-generator
instead; check it with
[`slsa-verifier`](https://github.com/slsa-framework/slsa-verifier):

```sh
slsa-verifier verify-artifact stevedore_*_Linux_x86_64.tar.gz \
  --provenance-path stevedore.intoto.jsonl \
  --source-uri github.com/blairham/stevedore \
  --source-tag v1.0.6
```
