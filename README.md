<img src="assets/mascot.svg" alt="stevedore mascot: a dock worker in a red knit cap carrying a shipping container" width="132" align="right">

# stevedore

[![CI](https://github.com/blairham/stevedore/actions/workflows/ci.yml/badge.svg)](https://github.com/blairham/stevedore/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/blairham/stevedore?sort=semver)](https://github.com/blairham/stevedore/releases/latest)
[![Go Report Card](https://goreportcard.com/badge/github.com/blairham/stevedore)](https://goreportcard.com/report/github.com/blairham/stevedore)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**Release Docker/OCI images the way [goreleaser](https://goreleaser.com) releases binaries.**

stevedore builds multi-arch container images, tags them from git state, pushes to
one or more registries, signs them with [cosign](https://github.com/sigstore/cosign),
generates an [SBOM](https://github.com/anchore/syft), and writes a changelog — all
from a single declarative config file.

```console
$ stevedore release
==> building myapp
    - ghcr.io/acme/myapp:1.4.0
    - ghcr.io/acme/myapp:9f8e7d6
    - ghcr.io/acme/myapp:latest
+ docker buildx build --platform linux/amd64,linux/arm64 ... --push .
+ cosign sign --yes ghcr.io/acme/myapp@sha256:…
+ syft ghcr.io/acme/myapp@sha256:… -o spdx-json=dist/sbom-myapp.spdx.json
+ cosign attest --yes --predicate dist/sbom-myapp.spdx.json --type spdxjson ghcr.io/acme/myapp@sha256:…
==> changelog written to dist/CHANGELOG.md
==> release complete
```

## Why

CI pipelines for container images tend to be a pile of bespoke shell: compute a tag
from the git ref, `docker buildx` with the right flags, remember to sign, remember
the SBOM, hand-maintain a changelog. stevedore turns all of that into one config
file you check in — so `stevedore release` does the same thing on your laptop and in
CI, and every image ships signed and attested by default.

## Is this just `X`?

| | What it is | Where stevedore sits |
|---|---|---|
| `docker buildx` / `bake` | the builder; bake describes **build targets** | stevedore calls buildx and describes **releases**. `init --from bake` imports your targets |
| `docker/build-push-action` | buildx as a GitHub Action | same layer as bake. It builds and pushes one image; stevedore decides *which* images, *what version*, and everything after the push |
| `ko` | Dockerfile-free image builds for **Go** | a builder, and Go-only. stevedore's input is a Dockerfile and a context |
| GoReleaser `dockers:` | binary releases that can also build images | the closest overlap — see below |
| release-please | version + changelog from commits | no images. Already using it? `versioning.strategy: env` takes the version from it |

stevedore does not build anything — `docker buildx` does — and it does not
deploy anything. It owns the part in between: deciding what to build, what to
call it, whether it is allowed to ship, and what must be true before it does.
The tagline is exact: GoReleaser doesn't compile your code either.

**If GoReleaser's `dockers:` block already covers you, keep using it.** One tool
beats two. It differs where images are the *primary* artifact: stevedore's input
is a Dockerfile rather than a Go build, multi-arch is one `platforms:` field
instead of per-arch images plus a manifest block, and monorepos get change
detection and per-image versions. This repo uses both — stevedore publishes the
image, GoReleaser publishes the CLI binary.

[docs/comparison.md](docs/comparison.md) has the long version, including what
stevedore deliberately is **not**.

## Install

Homebrew:

```sh
brew install blairham/tap/stevedore
```

With Go:

```sh
go install github.com/blairham/stevedore@latest
```

Or run the released image:

```sh
docker run --rm -v "$PWD:/src" -w /src ghcr.io/blairham/stevedore release
```

### Requirements

stevedore orchestrates other tools rather than reimplementing them — the same
design goreleaser uses — which keeps the binary tiny (~4.5 MB) instead of bundling
their combined ~900 dependencies. Depending on which features you enable, you'll
need:

| Tool | Needed for | Required? |
|------|-----------|-----------|
| `docker` (with `buildx`) | building & pushing | always |
| `git` | version/tag/changelog | always |
| `cosign` | signing & attestation | when `sign.cosign.enabled` |
| `syft` | SBOM generation | when `sbom.enabled` |
| `grype` or `trivy` | vulnerability scanning | when `scan.enabled` |
| `crane` | registry-based versioning | when `versioning.strategy: registry` |
| `aws` | ECR-based versioning | when `versioning.strategy: ecr` |
| `gh` | GitHub releases | when `release.github.enabled` |

Run `stevedore doctor` to check what's installed and get an install hint for
anything missing. `release` and `build` run this check automatically and fail fast
(before building) if a tool they need is absent.

## Quick start

```sh
# 1. Drop a starter config in your repo (scans your Dockerfiles)
stevedore init
# ...or import an existing setup:
#   stevedore init --from goreleaser   # from a .goreleaser.yaml dockers: block
#   stevedore init --from bake         # from a docker-bake target set
#   stevedore init --from services --file .platform/services
#                                      # from a dir of per-service manifests

# 2. Edit .stevedore.yaml — set your repositories/owner

# 3. Validate config and preview the resolved plan
stevedore check

# 4. See exactly what would run, without doing it
stevedore release --snapshot --dry-run

# 5. Build locally for the inner loop (single platform, loaded into docker)
stevedore build

# 6. Cut a real release (clean, tagged checkout)
git tag v1.4.0
stevedore release
```

## Examples

Working configs, each validated in CI against the real binary:

| | |
|---|---|
| [`examples/single-image`](examples/single-image) | one service, signed and attested |
| [`examples/monorepo`](examples/monorepo) | many services, change detection, per-image versions |
| [`examples/split-multiarch`](examples/split-multiarch) | one native runner per platform, no QEMU |

## Commands

| Command | What it does |
|---------|--------------|
| `stevedore release` | Full pipeline: build → push → scan → test → sign → SBOM → changelog. Clean, tagged checkout unless `--snapshot`. |
| `stevedore merge` | Second half of a split release: stitch the per-arch digests into manifest lists and run the release tail. |
| `stevedore plan` | Resolve versions, change detection and build grouping, and print the plan as JSON. Builds nothing. |
| `stevedore build` | Inner loop: one platform, loaded into the local docker daemon, no push. |
| `stevedore check` | Validate the config and print the exact refs that would publish. |
| `stevedore verify <ref>` | Check a pushed image's signature, SBOM attestation and provenance. |
| `stevedore doctor` | Report which external tools are present, and how to install the missing ones. |
| `stevedore init` | Scaffold a config by scanning Dockerfiles, or import one with `--from`. |
| `stevedore schema` | Print the JSON Schema, for editor autocomplete and validation. |

Full flag reference: [docs/cli.md](docs/cli.md).

## Documentation

| | |
|---|---|
| [Configuration](docs/configuration.md) | every `.stevedore.yaml` field, the template context, the changelog |
| [CLI reference](docs/cli.md) | commands, flags, and the JSON surfaces meant for machines |
| [Versioning and tags](docs/versioning.md) | the six version strategies, and how floating tags are gated |
| [Supply chain](docs/supply-chain.md) | the gate ordering, signing, SBOMs, provenance, verification |
| [Monorepos](docs/monorepo.md) | change detection, build-once grouping, CI fan-out, native multi-arch |
| [Using it in CI](docs/ci.md) | the GitHub Action, its inputs, and how to pin it |
| [Importing a config](docs/importing.md) | `init --from goreleaser` / `bake` / `services` |
| [Comparison](docs/comparison.md) | ko, bake, build-push-action, GoReleaser — and what this is not |
| [Stability contract](docs/stability.md) | what v1 promises about your config and your tags |

## Development

```sh
go test ./...      # unit tests
go vet ./...
stevedore release --snapshot --dry-run   # dogfood: stevedore releases itself
```

Releases use two tools, one per artifact kind: **stevedore** builds and publishes
its own container image (dogfooding), and **GoReleaser** (`.goreleaser.yaml`)
publishes the CLI binary, the GitHub release, and the Homebrew formula. See
`.github/workflows/release.yml`.

## Contributing

Issues and PRs welcome — see [CONTRIBUTING.md](CONTRIBUTING.md) for the build,
the test conventions, and where the extension points are.
[docs/stability.md](docs/stability.md) is the compatibility contract: what v1
promises about your config and your published tags, and what it does not.

To report a vulnerability, see [SECURITY.md](SECURITY.md) — privately, please.

## License

MIT
