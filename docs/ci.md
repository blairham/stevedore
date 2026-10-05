# Using stevedore in CI

Running stevedore in CI: the GitHub Action, pinning, doing it by hand, and
[GitLab CI](#gitlab-ci).

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

## GitLab CI

Nothing in stevedore needs GitHub. Outside Actions, three things change:

- **Step summary and outputs.** Set `STEVEDORE_SUMMARY_FILE` and
  `STEVEDORE_OUTPUTS_FILE` and stevedore appends the Markdown summary and the
  `key=value` outputs (`summary`, `refs`, `digests`, and `ref`/`digest` for a
  single image) to them. These are the files GitHub Actions gets through
  `$GITHUB_STEP_SUMMARY` / `$GITHUB_OUTPUT`, and the `STEVEDORE_*` variables
  take precedence. `plan` writes its `only` / `pins` outputs to the same file.
- **The release object.** `release.gitlab.enabled: true` creates a GitLab
  release with `glab release create <tag> --name … --notes-file <changelog>
  --ref <commit>`. `glab` is required, and preflighted, only when it is
  enabled. In a pipeline it authenticates with the job token when
  `GLAB_ENABLE_CI_AUTOLOGIN=true`, or with `GITLAB_TOKEN`.
- **Keyless signing.** cosign reads an OIDC token from `SIGSTORE_ID_TOKEN`,
  which GitLab issues through `id_tokens`.

```yaml
# .gitlab-ci.yml
release:
  stage: deploy
  image: docker:27
  services:
    - docker:27-dind
  rules:
    - if: $CI_COMMIT_TAG =~ /^v/
  id_tokens:
    SIGSTORE_ID_TOKEN:          # keyless cosign signing
      aud: sigstore
  variables:
    GIT_DEPTH: 0                # tags + history for versioning/changelog
    DOCKER_HOST: tcp://docker:2376
    DOCKER_TLS_CERTDIR: /certs
    DOCKER_TLS_VERIFY: 1
    DOCKER_CERT_PATH: /certs/client
    GLAB_ENABLE_CI_AUTOLOGIN: "true"   # glab uses the job token
    STEVEDORE_VERSION: "1.0.2"
    STEVEDORE_SUMMARY_FILE: $CI_PROJECT_DIR/stevedore-summary.md
    STEVEDORE_OUTPUTS_FILE: $CI_PROJECT_DIR/stevedore-outputs.env
  before_script:
    - apk add --no-cache bash curl git cosign glab
    - curl -sSfL https://raw.githubusercontent.com/anchore/syft/main/install.sh | sh -s -- -b /usr/local/bin
    - curl -sSfL https://raw.githubusercontent.com/anchore/grype/main/install.sh | sh -s -- -b /usr/local/bin
    - curl -sSfL "https://github.com/blairham/stevedore/releases/download/v${STEVEDORE_VERSION}/stevedore_${STEVEDORE_VERSION}_Linux_x86_64.tar.gz" | tar -xz -C /usr/local/bin stevedore
    - docker buildx create --use    # push-by-digest needs the docker-container driver
    - echo "$CI_REGISTRY_PASSWORD" | docker login -u "$CI_REGISTRY_USER" --password-stdin "$CI_REGISTRY"
  script:
    - stevedore doctor
    - stevedore release
  artifacts:
    when: always
    paths:
      - dist/
      - stevedore-summary.md
      - stevedore-outputs.env
```

with, in `.stevedore.yaml`:

```yaml
images:
  - repositories: [registry.gitlab.com/acme/myapp]
release:
  gitlab:
    enabled: true
```

Pin the install scripts' versions (`-s -- -b /usr/local/bin vX.Y.Z`) as you
would any other tool, and verify the stevedore archive against the release's
signed `checksums.txt` when supply-chain integrity matters (the GitHub Action
does both for you). The same pattern works in other CI systems: install the
tools, log in to the registry, point the two `STEVEDORE_*` variables at files
the system keeps, and run `stevedore release`.
