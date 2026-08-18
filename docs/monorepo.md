# Monorepos

Change detection, build-once grouping, and fanning a release out across CI runners.

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

## Scoping each image

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

## Auto-deriving paths from a project graph

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

## Build once, tag many

Images whose build spec is identical (same Dockerfile, context, target, platforms,
build args, and labels) and differ only by destination repository/tag are **built
once** and pushed to every member's tags in a single `buildx` invocation — no
redundant rebuilds. Images that differ by a build arg (e.g. a `PROJECT=`) stay
separate. Grouping is automatic; nothing to configure.

## Matrix mode: one CI job per build

A single runner with `--parallel N` is fine for a handful of images, but a
change touching many heavy images wants one runner *each*. `stevedore plan`
splits deciding from building so CI can fan out:

```json
{
  "include": [
    {"group": "checkout", "ids": ["checkout"], "only": "checkout",
     "versions": {"checkout": "0.0.513"}, "pins": "--pin-version checkout=0.0.513",
     "reason": "src/Acme/… changed since its release marker"}
  ],
  "skipped": [{"id": "billing-gateway", "reason": "unchanged since its release marker"}]
}
```

`plan` resolves per-image versions, runs change detection (marker refs,
`--changed-since`, or `--only-changed`), and applies build-once grouping — a
group is **one** entry, so images sharing a build still ride one runner.
Each matrix job then runs `release --only <entry.only> <entry.pins>`: it builds
its entry unconditionally, tags exactly what the plan resolved, and advances
only its own release markers. Progress goes to stderr; stdout is only the JSON.

```yaml
jobs:
  plan:
    runs-on: ubuntu-latest
    outputs:
      matrix: ${{ steps.plan.outputs.plan }}
    steps:
      - uses: actions/checkout@v4
        with: {fetch-depth: 0}
      - uses: blairham/stevedore@v1
        id: plan
        with: {command: plan}

  build:
    needs: plan
    if: ${{ fromJson(needs.plan.outputs.matrix).include[0] != null }}
    strategy:
      matrix: ${{ fromJson(needs.plan.outputs.matrix) }}
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: {fetch-depth: 0}
      - uses: blairham/stevedore@v1
        with:
          command: release
          args: --only ${{ matrix.only }} ${{ matrix.pins }}
```

Entries carry the member `ids`, so a caller can also map per-entry metadata —
e.g. pick a per-service cloud credential/role for single-member entries.

## Native multi-arch: one runner per platform

A single-runner multi-arch build emulates every non-native platform with QEMU —
often 5–10× slower for compile-heavy stages. GitHub hosts native arm64 runners
(`ubuntu-24.04-arm`, free for public repos), so stevedore can split the build:
each matrix leg builds **one platform on its native runner** and pushes it
untagged, by digest; a final job merges the digests into one tagged manifest
list per image and runs the release tail (scan → smoke test → sign → SBOM →
changelog → publish) against the merged artifact — so nothing is ever signed or
published before every arch exists.

A composite action can't spawn jobs, so the fan-out lives in the workflow:
`plan --split-platforms` emits one matrix entry per build group per platform,
each with a `runner` hint (`linux/amd64` → `ubuntu-24.04`, `linux/arm64` →
`ubuntu-24.04-arm`; other platforms leave it empty for you to map):

```yaml
jobs:
  plan:
    runs-on: ubuntu-latest
    outputs:
      matrix: ${{ steps.plan.outputs.plan }}
    steps:
      - uses: actions/checkout@v4
        with: {fetch-depth: 0}
      - uses: blairham/stevedore@v1
        id: plan
        with: {command: plan, args: --split-platforms}

  build:
    needs: plan
    if: ${{ fromJson(needs.plan.outputs.matrix).include[0] != null }}
    strategy:
      matrix: ${{ fromJson(needs.plan.outputs.matrix) }}
    runs-on: ${{ matrix.runner }}
    steps:
      - uses: actions/checkout@v4
        with: {fetch-depth: 0}
      - uses: docker/login-action@v3
        with: {registry: ghcr.io, username: "${{ github.actor }}", password: "${{ secrets.GITHUB_TOKEN }}"}
      - uses: blairham/stevedore@v1
        with:
          command: release
          args: --only ${{ matrix.only }} ${{ matrix.pins }} --split ${{ matrix.platform }}
      # The legs and the merge job share dist/digests/ via artifacts.
      - uses: actions/upload-artifact@v4
        with:
          name: digests-${{ matrix.group }}-${{ strategy.job-index }}
          path: dist/digests/

  merge:
    needs: [plan, build]
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: {fetch-depth: 0}
      - uses: actions/download-artifact@v4
        with:
          pattern: digests-*
          path: dist/digests/
          merge-multiple: true
      - uses: docker/login-action@v3
        with: {registry: ghcr.io, username: "${{ github.actor }}", password: "${{ secrets.GITHUB_TOKEN }}"}
      - uses: blairham/stevedore@v1
        with:
          command: merge
```

The legs record each pushed digest as `dist/digests/<image-id>/<platform>`;
`merge` refuses to publish while any configured platform has no digest, so a
failed or missing leg can never ship a partial manifest list. Signing, SBOM
attestation, and the vulnerability/smoke-test gates all run once, against the
merged manifest-list digest — per-arch SLSA provenance from `--provenance` is
attached by the legs at build time and survives the merge.

For simple repos you can skip `plan` entirely and hardcode the matrix
(`matrix: {include: [{platform: linux/amd64, runner: ubuntu-24.04}, …]}`);
`release --split` and `merge` don't care where the fan-out came from.

> **Tip:** if your image is pure Go (`CGO_ENABLED=0`), cross-compiling inside
> the Dockerfile (`FROM --platform=$BUILDPLATFORM` + `GOOS`/`GOARCH` from
> `TARGETOS`/`TARGETARCH`) removes QEMU from the hot path with zero workflow
> changes — split builds earn their keep when build stages must *execute* on
> the target arch (CGO, native compilers, test suites in `RUN` steps).
