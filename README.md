# stevedore

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

## Commands

| Command | What it does |
|---------|--------------|
| `stevedore release` | Full pipeline: build all platforms → push → sign → SBOM → changelog. Requires a clean, tagged checkout unless `--snapshot`. |
| `stevedore build` | Inner-loop build: one platform, loaded into the local docker daemon, no push. `--push` publishes multi-arch but skips the release extras. |
| `stevedore check` | Validate the config and print the fully-resolved release plan (the exact refs that would publish). |
| `stevedore verify <ref>` | Verify a pushed image's cosign signature, SBOM attestation, and SLSA provenance. |
| `stevedore doctor` | Probe for docker/buildx/git/cosign/syft/grype/crane/aws, report versions, and print install hints for anything your config requires. |
| `stevedore init` | Scaffold a `.stevedore.yaml` by scanning Dockerfiles, or `--from goreleaser` / `--from bake` to import an existing config. |
| `stevedore schema` | Print the JSON Schema for `.stevedore.yaml` (for editor autocomplete/validation). |

### Global flags

| Flag | Description |
|------|-------------|
| `-f, --config` | Path to config file (default: autodiscover `.stevedore.yaml`). |
| `--dir` | Project/repository root (default `.`). |
| `--dry-run` | Print every command without executing it. |
| `-v, --verbose` | Verbose output. |

### `release` flags

`--snapshot`, `--skip-sign`, `--skip-sbom`, `--skip-scan`, `--skip-test`,
`--skip-changelog`, `--skip-publish`, `--only-changed` / `--changed-since <ref>`
(skip unchanged images — see [Monorepos](#monorepos)), and `--output json`
(emit a machine-readable release summary to stdout).

Every release also writes `<dist>/release-summary.json` and, in GitHub Actions, a
job-summary table (images, digests, signed/sbom/provenance/test status, vuln
counts) to `$GITHUB_STEP_SUMMARY`.

### Editor support

```sh
stevedore schema > stevedore.schema.json
```

Then add to the top of `.stevedore.yaml` for autocomplete + validation:

```yaml
# yaml-language-server: $schema=./stevedore.schema.json
```

## How tags are resolved

The set of published references is the **cartesian product of `repositories` × `tags`**.
Tags are Go templates rendered against git state (see [Template context](#template-context)).

**Floating tags** — `latest` or anything ending in `-latest` — are treated specially:
they only publish on the **default branch** of a **non-snapshot** release. This means
`latest` never accidentally moves from a feature branch or a snapshot build, while
immutable tags like the version and commit SHA always publish.

Signing and SBOM generation happen **by digest** (`repo@sha256:…`), not by tag, so the
exact artifact is pinned regardless of how many mutable tags point at it.

## Versioning

The release version can be derived several ways via the `versioning:` block. The
default is `git`; the others let you avoid relying on git tags entirely.

| Strategy | Where the version comes from |
|----------|------------------------------|
| `git` (default) | Git tags. Clean, tagged checkout → the tag with any leading `v` stripped (`v1.4.0` → `1.4.0`). Otherwise a snapshot like `1.4.0-SNAPSHOT-9f8e7d6` (`-dirty` if the tree is dirty). |
| `registry` | Lists the existing tags in a registry repo (via `crane`), takes the highest semver, and bumps it by `patch`/`minor`/`major`. Non-semver tags (`latest`, commit SHAs) are ignored. |
| `ecr` | Like `registry`, but lists tags via `aws ecr describe-images` using your AWS credentials directly — no `crane` or docker credential helper. Region is inferred from the ECR host (override with `region:`). |
| `static` | An explicit `value:`. |
| `env` | Read from an environment variable. |
| `command` | The trimmed stdout of a command — an escape hatch for anything. |

In a multi-image config, `registry`/`ecr` version **each image independently from
its own repo**, preserving per-service versions. Set `repo:` to pin one repo for a
single unified version instead. A repo with no semver tags starts at
`versioning.initial` (default `0.1.0`). `stevedore check` never hard-fails on an
unreachable registry — it warns and shows a placeholder so the rest of the config
still validates offline.

`stevedore release` refuses to run on a dirty tree unless you pass `--snapshot`. A
git tag on HEAD is required **only** for the `git` strategy — the others source the
version elsewhere, which is handy when your tags drift out of sync with what's
actually published.

### Deriving the version from ECR

```yaml
versioning:
  strategy: ecr
  bump: minor            # patch (default) | minor | major
  # region: us-east-1    # optional; inferred from the ECR host otherwise
  # repo: "..."          # optional; defaults to each image's own repository
```

The `ecr` strategy shells out to `aws ecr describe-images` using your AWS
credentials directly (SSO profile / env / IRSA) — **no `crane` and no docker
credential helper**. In CI, run `aws-actions/configure-aws-credentials` first.

If you'd rather use `crane` (works across ghcr/Docker Hub/ECR via the Docker
credential chain), use `strategy: registry` with the
[`amazon-ecr-credential-helper`](https://github.com/awslabs/amazon-ecr-credential-helper)
configured. `stevedore check` prints the version it resolves, so you can preview the
next release without publishing anything.

## Supply chain

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

## Monorepos

For a repo that builds many images, stevedore can skip images whose code didn't
change. There are two modes:

| Mode | How it decides | Best for |
|------|----------------|----------|
| `--changed-since <ref>` | git diff since a ref; an image builds if a changed file matches its paths | CI (stateless — compare a PR to `main`) |
| `--only-changed` | content fingerprint vs. the last release, stored in `<dist>/fingerprints.json` | local iteration |
| `change_detection.marker_refs` | each image diffs against **its own** last-release git ref (`refs/releases/image/<id>`), advanced after each push | CI, per-image release cadence (stateless, no ref to pass) |

With `marker_refs: true`, an image rebuilds iff its sources changed since *its own*
last release — even across independent releases and fresh checkouts. After a
successful push, stevedore advances `refs/releases/image/<id>` to `HEAD` and pushes
it, so the baseline lives in git (no state file to persist).

```sh
stevedore release --changed-since origin/main
# ==> building api
# ==> skipping worker (no matching files since origin/main)
```

### Scoping each image

The hard case is **many images built from one Dockerfile and one context** (they
differ only by a build arg). Without scoping, any change rebuilds everything —
because every image's context is the whole repo. Declare what each image actually
depends on:

```yaml
change_detection:
  shared_paths:                 # a change here rebuilds every image
    - "Dockerfile"
    - "*.sln"
images:
  - id: reports
    build_args: ["PROJECT=Reports"]
    paths: ["Reports/**"]       # this image depends only on these (+ shared_paths)
```

### Auto-deriving paths from a project graph

Hand-maintaining `paths` gets ugly once shared libraries and transitive
dependencies are involved. For .NET, let stevedore read the `.csproj`
`<ProjectReference>` graph instead:

```yaml
change_detection:
  resolver: dotnet
  shared_paths: ["Dockerfile", "*.sln", "Directory.*"]
images:
  - id: payments-gateway
    build_args: ["PROJECT=Acme.PaymentsGateway"]
    project: Acme.PaymentsGateway/Acme.PaymentsGateway.csproj
    # paths auto-resolved to PaymentsGateway + Payments + Fix + Shared (transitively)
```

Now a change to a shared library rebuilds exactly the images that reference it —
transitively — and nothing else.

For `--only-changed`, the fingerprint file lives under `dist/` (git-ignored);
persist it between CI runs like a build cache (e.g. `actions/cache`). A fresh
checkout with no fingerprint safely rebuilds everything.

## Configuration

stevedore looks for `.stevedore.yaml`, `.stevedore.yml`, `stevedore.yaml`, or
`stevedore.yml` (in that order). Unknown fields are rejected, so typos fail fast.

```yaml
version: 1

project_name: myapp
default_branch: main      # branch on which floating tags may publish
dist: dist                # output dir for SBOMs and the changelog

images:
  - id: myapp             # stable identifier used in logs/artifact names
    dockerfile: Dockerfile
    context: .
    target: ""            # optional multi-stage target
    platforms:
      - linux/amd64
      - linux/arm64
    repositories:         # destinations without a tag
      - ghcr.io/acme/myapp
      - registry.acme.io/myapp
    tags:                 # Go templates; floating tags gated to default branch
      - "{{ .Version }}"
      - "{{ .ShortCommit }}"
      - "latest"
    build_args:
      - "VERSION={{ .Version }}"
    labels:
      org.opencontainers.image.source: "https://github.com/acme/myapp"
      org.opencontainers.image.version: "{{ .Version }}"
      org.opencontainers.image.revision: "{{ .Commit }}"
      org.opencontainers.image.created: "{{ .Date }}"
    secrets:              # BuildKit --secret entries
      - id: github_token
        env: GITHUB_TOKEN
      # - id: npmrc
      #   file: ./.npmrc
    cache_from:           # buildx --cache-from sources
      - "type=registry,ref=ghcr.io/acme/myapp:buildcache"
    cache_to:             # buildx --cache-to destinations
      - "type=registry,ref=ghcr.io/acme/myapp:buildcache,mode=max"
    paths:                # change-detection globs (see Monorepos); ** supported
      - "services/myapp/**"
    project: ""           # or a .csproj to auto-derive paths from its graph
    extra_flags: []       # passed verbatim to `docker buildx build`

sign:
  cosign:
    enabled: true
    key: ""               # omit for keyless (OIDC) signing
    args: []              # extra cosign flags

sbom:
  enabled: true
  generator: syft         # only syft is supported
  format: spdx-json       # or cyclonedx-json
  attest: true            # attach a signed SBOM attestation (needs cosign)

scan:
  enabled: true
  scanner: grype          # grype (default) | trivy
  fail_on: high           # block the release at this severity or above;
                          # negligible|low|medium|high|critical. Empty = report only.
  ignore:                 # vulnerability IDs to exclude from the gate
    - CVE-2024-0000
  args: []                # extra flags passed to the scanner

provenance:
  enabled: true           # emit a SLSA build-provenance attestation (push only)
  mode: max               # min | max (max records the full build definition)

test:
  enabled: true           # smoke-test the built image before signing/pushing
  cmd: ["/usr/bin/myapp", "--version"]   # run inside the container
  expect_exit: 0          # required exit code
  timeout: 30s            # Go duration; default 60s

versioning:
  strategy: git           # git (default) | registry | ecr | static | env | command
  # registry/ecr strategies:
  # bump: patch           # patch | minor | major
  # repo: ghcr.io/acme/myapp   # defaults to each image's own repository
  # region: us-east-1     # ecr only; inferred from the ECR host otherwise
  # initial: "0.1.0"      # when the repo has no semver tags yet

change_detection:         # scope --only-changed / --changed-since for monorepos
  resolver: ""            # "dotnet" auto-derives per-image paths from .csproj refs
  shared_paths:           # a change here rebuilds every image
    - "Dockerfile"
    - "*.sln"

changelog:
  enabled: true
  sort: asc               # asc | desc
  exclude:                # drop commits whose subject matches any regex
    - "^chore:"
    - "^docs:"
    - "^test:"
  dependency_diff: true   # append packages added/removed/upgraded since the
                          # previous release (diffs this SBOM vs the previous tag's)

release:
  github:
    enabled: true         # create a GitHub release (via gh) with the changelog
    draft: false
    prerelease: false

announce:
  slack:
    enabled: true
    webhook_env: SLACK_WEBHOOK   # env var holding the webhook URL
    template: "🚀 {{ .ProjectName }} {{ .Version }} shipped"   # optional
  discord:
    enabled: false
    webhook_env: DISCORD_WEBHOOK
```

Publishing (`release.github` + `announce`) runs only on real releases, never on
`--snapshot`, and can be turned off per-run with `--skip-publish`.

### Template context

Tags, labels, and build args are rendered with Go's `text/template`. Referencing an
undefined field is an error (no silent empty strings). Available fields:

| Field | Example |
|-------|---------|
| `.ProjectName` | `myapp` |
| `.Version` | `1.4.0` |
| `.Tag` | `v1.4.0` |
| `.Commit` | full SHA |
| `.ShortCommit` | `9f8e7d6` |
| `.Branch` | `main` |
| `.Date` | RFC 3339 build time (UTC) |
| `.Timestamp` | Unix seconds |
| `.IsSnapshot` | `true` in a snapshot build |
| `.IsDefault` | `true` when HEAD is on the default branch |
| `.Env.NAME` | environment variable `NAME` |

Helper functions: `lower`, `upper`, `trim`, `replace`, `trimPrefix`, `trimSuffix`.

### Changelog

When enabled, stevedore reads commits since the previous tag and groups them by
[Conventional Commit](https://www.conventionalcommits.org) type into **Features**,
**Bug Fixes**, **Performance**, **Refactors**, **Documentation**, and **Other**. A
`!` (e.g. `feat!:`) marks a breaking change. Non-conforming subjects land under
"Other". The result is written to `<dist>/CHANGELOG.md`.

## Using it in CI

stevedore does the same thing locally and in CI. The easiest way is the bundled
**GitHub Action**, which installs stevedore and every tool it needs (cosign, syft,
grype, and optionally crane) for you:

```yaml
name: release
on:
  push:
    tags: ["v*"]

jobs:
  release:
    runs-on: ubuntu-latest
    permissions:
      contents: write   # create the GitHub release
      packages: write   # push to ghcr.io
      id-token: write   # keyless cosign signing
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0            # tags + history for versioning/changelog
      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - uses: blairham/stevedore@v1  # installs stevedore + cosign/syft/grype
        with:
          command: release
```

### Action inputs

| Input | Default | Description |
|-------|---------|-------------|
| `command` | `release` | Subcommand: `release`, `build`, `check`, `verify`, `doctor`. |
| `args` | `""` | Extra args, e.g. `--snapshot --only-changed`. |
| `config` | autodiscover | Path to the config file. |
| `version` | `latest` | stevedore version to install. |
| `working-directory` | `.` | Directory to run in. |
| `install-cosign` / `install-syft` / `install-grype` | `true` | Install that tool. |
| `install-crane` | `false` | Install crane (enable for `versioning.strategy: registry`). |

A pull-request check that validates the plan without publishing:

```yaml
      - uses: blairham/stevedore@v1
        with:
          command: check
```

Prefer to wire the tools up yourself? stevedore is just a binary, so
`go run github.com/blairham/stevedore@latest release` after installing the tools
works too. Either way, **`fetch-depth: 0` matters**: shallow clones hide the tags and
history stevedore needs to derive the version and build the changelog.

## Development

```sh
go test ./...      # unit tests
go vet ./...
stevedore release --snapshot --dry-run   # dogfood: stevedore releases itself
```

Releases use two tools, one per artifact kind: **stevedore** builds and publishes
its own container image (dogfooding), and **GoReleaser** (`.goreleaser.yaml`)
publishes the CLI binary, the GitHub release, and the Homebrew cask. See
`.github/workflows/release.yml`.

## License

MIT
