# Using stevedore in CI

Running stevedore in CI: the GitHub Action, pinning, and doing it by hand.

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
      - uses: actions/checkout@v6
        with:
          fetch-depth: 0            # tags + history for versioning/changelog
      - uses: docker/login-action@v4
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - uses: blairham/stevedore@v1  # installs stevedore + cosign/syft/grype
        with:
          command: release
```

## Pinning the action

`@v1` is a **moving major tag**, repointed at each release: you get bug fixes
without editing a workflow, and never a breaking change, since a breaking change
ships as `v2`. Pin harder if you'd rather review every bump:

| Pin | Resolves to |
|-----|-------------|
| `blairham/stevedore@v1` | newest `v1.x.y` — recommended |
| `blairham/stevedore@v1.2` | newest `v1.2.z` |
| `blairham/stevedore@v1.2.3` | exactly that release |
| `blairham/stevedore@<sha>` | exactly that commit — the strictest option |

The action's `version` input controls the **stevedore binary** it downloads,
which is a separate choice from the action code above. Left at its `latest`
default it resolves to the newest release *in the action's own major line*, so
`@v1` keeps running v1 binaries after v2 ships.

## Action inputs

| Input | Default | Description |
|-------|---------|-------------|
| `command` | `release` | Subcommand: `release`, `build`, `check`, `verify`, `doctor`. |
| `args` | `""` | Extra args, e.g. `--snapshot --only-changed`. |
| `config` | autodiscover | Path to the config file. |
| `version` | `latest` | stevedore version to install (`v1.0.2` or later; see below). |
| `working-directory` | `.` | Directory to run in. |
| `install-cosign` / `install-syft` / `install-grype` | `true` | Install that tool. cosign is installed regardless whenever the action installs stevedore, because it verifies the download. |
| `install-crane` | `false` | Install crane (enable for `versioning.strategy: registry`). |

## Action outputs

| Output | Set by | Value |
|--------|--------|-------|
| `refs` | `release`, `merge` | JSON: image id → `repository@sha256:…` (first repository) for every image with a published digest — pushed this run, or already released from this commit |
| `digests` | same | JSON: image id → `sha256:…` |
| `ref` / `digest` | same | the one pinned image's `repository@sha256:…` / digest — only when exactly one image was pinned |
| `summary` | `release`, `build` | the compact JSON release summary (each image also carries `digest_refs`) |
| `plan` / `only` / `pins` | `plan` | matrix mode (see [Monorepos](monorepo.md)) |

A dry run, a `--no-push` build and a split leg pin nothing: none of them has a digest a
consumer can pull. In matrix mode each `release --only` job reports the images it built,
and `publish` reports none, so collect the jobs' `refs` (or `outputs:` files) yourself.

## Feeding a GitOps repository

Pin deployments to the digest, not the tag — the digest is what was scanned, tested
and signed. For one image, `ref` is enough:

```yaml
- uses: blairham/stevedore@v1
  id: release
- run: yq -i '.image = "${{ steps.release.outputs.ref }}"' deploy/values.yaml
```

For several, have the release write the file itself with `outputs:` — a Go template
over the pinned images (`ID`, `Version`, `Digest`, `Repository`, `Ref`,
`Repositories`, `DigestRefs`, `Refs`), written only by a real run that pinned at
least one image. A kustomize overlay whose `images:` pins every released image (kustomize
matches `name` against the images in the base manifests and rewrites them to the
digest):

```yaml
outputs:
  file: deploy/overlays/prod/kustomization.yaml
  template: |
    apiVersion: kustomize.config.k8s.io/v1beta1
    kind: Kustomization
    resources: [../../base]
    images:
    {{- range .Images }}
      - name: {{ .Repository }}
        digest: {{ .Digest }}
    {{- end }}
```

The step after the release commits that file to the deployment repository (or opens
a PR with it). Only images this run pinned are in it — pushed, or already released
from this commit — and if none was, no file is written. Under `--only-changed` that
is a subset, and a template that renders a *whole* overlay would drop the pins of the
images that did not change. For partial releases, render edits instead and apply them
to the existing overlay:

```yaml
outputs:
  file: dist/pin-images.sh
  template: |
    {{- range .Images }}
    kustomize edit set image {{ .Ref }}
    {{- end }}
```

```sh
(cd deploy/overlays/prod && sh "$GITHUB_WORKSPACE/dist/pin-images.sh")
```

The action verifies the stevedore binary before running it: it checks the
release's `checksums.txt` against its keyless cosign signature, which must come
from this repository's `release.yml` for that tag (or for `main`, for a release
re-run by hand), and then checks the archive's sha256 against that file. Any
mismatch, or a release with no signed `checksums.txt` (`v1.0.0` and `v1.0.1`),
fails the step before the binary is extracted. To run a binary the action did
not verify, install it yourself and set `install-stevedore: false`.

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
