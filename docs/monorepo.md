# Monorepos

Change detection, build-once grouping, and fanning a release out across CI runners.

For a repo that builds many images, stevedore can skip images whose code didn't
change. There are two modes:

| Mode | How it decides | Best for |
|------|----------------|----------|
| `--changed-since <ref>` | git diff since a ref; an image builds if a changed file is in its [scope](#scoping-each-image) | CI (stateless — compare a PR to `main`) |
| `--only-changed` | content fingerprint vs. the last release, stored in `<dist>/fingerprints.json` | local iteration |
| `change_detection.marker_refs` | each image diffs against **its own** last-release git ref (`refs/releases/image/<id>`), advanced after each push | CI, per-image release cadence (stateless, no ref to pass) |

With `marker_refs: true`, an image rebuilds iff its sources changed since *its own*
last release — even across independent releases and fresh checkouts. After a
successful push, stevedore advances `refs/releases/image/<id>` to `HEAD` and pushes
it, so the baseline lives in git (no state file to persist).

The marker only ever fast-forwards. stevedore fetches origin's marker and compares it
with `HEAD` first:

- **Behind `HEAD`:** advanced.
- **Ahead of `HEAD`:** left alone, since a newer commit was already released (an older
  release re-run).
- **Diverged:** for example, released from a branch that was never merged. The release
  **fails**, after the images are pushed and notifications have fired. Until the marker
  is reset, change detection diffs from the merge base of the marker and `HEAD` rather
  than from the stray commit (whose own files would otherwise read as changed on every
  release), and `plan` says so in the image's reason. Each release still fails, so
  this needs a human. Reset it to the last commit actually released with a lease, e.g.
  `git push --force-with-lease=refs/releases/image/<id>:<stray> origin <sha>:refs/releases/image/<id>`.
  To prevent it, only release from your default branch.

Markers are fetched from origin with a forced refspec, so a marker reset there wins
over a stale local copy. A failed fetch fails the run: without markers every image
would read "never released" and rebuild.

**With `--changed-since` as well**, marker mode stays on and each image diffs from
the **older** of its marker and the ref. CI often passes
`--changed-since ${{ github.event.before }}`, and on its own that would skip a change
whose release failed or was canceled: the marker never advanced past it, but the
next push's ref is newer. When the marker is older, that change still rebuilds. When
the ref is older, the diff widens to cover it, as asked, and the reason says
`since <ref> (older than the release marker)`. When neither is an ancestor of the
other, or git cannot tell (for example, a shallow clone missing one of them), both
diffs are combined. An image with no marker yet still builds as never released.
`--only` still turns change detection off entirely.

```sh
stevedore release --changed-since origin/main
# ==> building api
# ==> skipping worker (no matching files since origin/main)
```

## Scoping each image

Every image has a **scope**: the files whose change rebuilds it. A diff since the
base (`--changed-since` or the image's release marker) that is empty leaves every
image unchanged, whatever its scope. Otherwise an image rebuilds when a changed
file falls in its scope or matches `change_detection.shared_paths`.

### The default: the build context

An image that declares no `paths` (and gets none from a resolver) is scoped to
what `docker build` would actually send it:

- every file under its **build context** directory,
- minus what the context's **dockerignore** excludes — `<Dockerfile>.dockerignore`
  next to the Dockerfile when it exists (BuildKit's rule), otherwise
  `<context>/.dockerignore`, parsed with Docker's own matcher, so `!` re-inclusion,
  `**`, a leading `/` and excluded parent directories behave exactly as in a build,
- plus the **Dockerfile** (wherever it lives), the dockerignore file itself, and
  `.stevedore.yaml` (build args, target, and platforms live there).

So a README edit outside `services/api/` — or a `*.md` inside it that its
`.dockerignore` drops — does not release an image built from `services/api`:

```sh
stevedore release --changed-since HEAD~1
# ==> skipping api (no matching files in context services/api (.dockerignore) since HEAD~1)
```

The plan reason names the scope it used, so a surprising rebuild says which file
reached the image and through which context. An image whose context is the repo
root (`.`) with no dockerignore is still rebuilt by any change; add a
`.dockerignore` (it shrinks the build context too) or declare `paths`. Only an
image whose context is not a local directory — a remote git or tarball URL — is
**unscoped**: git cannot see it, so it always builds.

This default applies to the diff-based modes (`--changed-since`, marker refs).
An `--only-changed` fingerprint of an image without `paths` still hashes its whole
context directory, dockerignored files included.

### Declaring paths

The hard case is **many images built from one Dockerfile and one context** (they
differ only by a build arg). Their default scope is the same shared context, so a
change to any service rebuilds every one of them. Declare what each image actually
depends on; `paths` replaces the default scope entirely:

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

Many near-identical images are also where copy-paste bites: a copied block that
drops one build arg ships the wrong binary. Put the shared part in
[`image_defaults:`](configuration.md#image-defaults) and let `{{ .ID }}` fill in the
per-image names, so each image states only what is its own (see
`examples/monorepo`).

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
checkout with no fingerprint safely rebuilds everything. Only a run that publishes
writes it: `--no-push`, snapshot builds (including `stevedore build`) and
`--split` legs read the file but leave it as it was, so validating a change never
makes the next real release skip it. The `merge` run records it for a split release.

## Build once, tag many

Images whose build spec is identical (same Dockerfile, context, target, platforms,
build args, and labels) and differ only by destination repository/tag are **built
once** and pushed by digest to every member's repository in a single `buildx`
invocation, then — once the gates pass — tagged with every member's tags. No
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
its entry unconditionally, tags exactly what the plan resolved, notifies, and
advances only its own release markers. Progress goes to stderr; stdout is only
the JSON.

A matrix job does **not** create the GitHub release or announce — with N jobs
that would be N `gh release create` calls for one tag and N Slack posts. As with
split legs and `merge`, one final job does it: `stevedore publish`, given every
entry's `only` and `pins` (GitHub's `join(….include.*.only, ',')` collects them),
writes the changelog, creates the release named after the pushed version, and
announces once. It runs only if every matrix job succeeded.

```yaml
jobs:
  plan:
    runs-on: ubuntu-latest
    outputs:
      matrix: ${{ steps.plan.outputs.plan }}
    steps:
      - uses: actions/checkout@v6
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
      - uses: actions/checkout@v6
        with: {fetch-depth: 0}
      - uses: blairham/stevedore@v1
        with:
          command: release
          args: --only ${{ matrix.only }} ${{ matrix.pins }}

  publish:
    needs: [plan, build]
    if: ${{ fromJson(needs.plan.outputs.matrix).include[0] != null }}
    runs-on: ubuntu-latest
    permissions:
      contents: write   # create the GitHub release
    steps:
      - uses: actions/checkout@v6
        with: {fetch-depth: 0}
      - uses: blairham/stevedore@v1
        env:
          GH_TOKEN: ${{ github.token }}   # for gh release create
        with:
          command: publish
          args: >-
            --only ${{ join(fromJson(needs.plan.outputs.matrix).include.*.only, ',') }}
            ${{ join(fromJson(needs.plan.outputs.matrix).include.*.pins, ' ') }}
```

Entries carry the member `ids`, so a caller can also map per-entry metadata —
e.g. pick a per-service cloud credential/role for single-member entries.

## Native multi-arch: one runner per platform

A single-runner multi-arch build emulates every non-native platform with QEMU —
often 5–10× slower for compile-heavy stages. GitHub hosts native arm64 runners
(`ubuntu-24.04-arm`, free for public repos), so stevedore can split the build:
each matrix leg builds **one platform on its native runner** and pushes it
untagged, by digest; a final job merges the digests into one manifest list per
image (still untagged) and runs the release tail (scan → smoke test → sign →
SBOM → tag → changelog → publish) against the merged artifact — so nothing is
ever signed or tagged before every arch exists and the gates have passed.

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
      - uses: actions/checkout@v6
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
      - uses: actions/checkout@v6
        with: {fetch-depth: 0}
      - uses: docker/login-action@v4
        with: {registry: ghcr.io, username: "${{ github.actor }}", password: "${{ secrets.GITHUB_TOKEN }}"}
      - uses: blairham/stevedore@v1
        with:
          command: release
          args: --only ${{ matrix.only }} ${{ matrix.pins }} --split ${{ matrix.platform }}
      # The legs and the merge job share dist/digests/ via artifacts.
      - uses: actions/upload-artifact@v7
        with:
          name: digests-${{ matrix.group }}-${{ strategy.job-index }}
          path: dist/digests/

  merge:
    needs: [plan, build]
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v6
        with: {fetch-depth: 0}
      - uses: actions/download-artifact@v8
        with:
          pattern: digests-*
          path: dist/digests/
          merge-multiple: true
      - uses: docker/login-action@v4
        with: {registry: ghcr.io, username: "${{ github.actor }}", password: "${{ secrets.GITHUB_TOKEN }}"}
      - uses: blairham/stevedore@v1
        with:
          command: merge
```

The legs record each pushed digest as `dist/digests/<image-id>/<platform>`;
`merge` refuses to publish while any configured platform has no digest, so a
failed or missing leg can never ship a partial manifest list — and it refuses
a digest for a platform the image does *not* configure (a stale file in a
persistent `dist/`), so it can't ship an extra one either. Signing, SBOM
attestation, and the vulnerability/smoke-test gates all run once, against the
merged manifest-list digest, and the tags are applied only after they pass — per-arch SLSA provenance from `--provenance` is
attached by the legs at build time and survives the merge.

For simple repos you can skip `plan` entirely and hardcode the matrix
(`matrix: {include: [{platform: linux/amd64, runner: ubuntu-24.04}, …]}`);
`release --split` and `merge` don't care where the fan-out came from. A
hardcoded leg skips every image whose `platforms` doesn't include the leg's
platform, so an amd64-only image is never built on the arm64 leg.

> **Tip:** if your image is pure Go (`CGO_ENABLED=0`), cross-compiling inside
> the Dockerfile (`FROM --platform=$BUILDPLATFORM` + `GOOS`/`GOARCH` from
> `TARGETOS`/`TARGETARCH`) removes QEMU from the hot path with zero workflow
> changes — split builds earn their keep when build stages must *execute* on
> the target arch (CGO, native compilers, test suites in `RUN` steps).
