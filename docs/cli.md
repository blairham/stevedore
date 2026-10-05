# Commands

Every command, every flag, and the JSON surfaces meant for machines.

| Command | What it does |
|---------|--------------|
| `stevedore release` | Full pipeline: build all platforms → push → sign → SBOM → changelog. Requires a clean, tagged checkout unless `--snapshot`. `--split <platform>` builds one native-arch leg of a split release (see [Native multi-arch](monorepo.md#native-multi-arch-one-runner-per-platform)). |
| `stevedore merge` | Second half of a split release: stitch the legs' per-arch digests into manifest lists (`imagetools create`, by digest), run the gates on them, then sign, attest, tag, and finish the release (changelog, publish). |
| `stevedore publish` | Last step of a matrix release: write the changelog, create the GitHub release and announce — once, after every `release --only` job. Builds and pushes nothing. See [`publish` flags](#publish-flags). |
| `stevedore plan` | Resolve versions, change detection, and build-once grouping — print the plan as JSON without building. The `include` array is GitHub Actions matrix shape (see [Matrix mode](monorepo.md#matrix-mode-one-ci-job-per-build)); `--split-platforms` emits one entry per platform with native runner hints. |
| `stevedore build` | Inner-loop build: one platform, loaded into the local docker daemon, no push. `--push` publishes a multi-arch snapshot — see [`build --push`](#build---push). |
| `stevedore check` | Validate the config and print the fully-resolved release plan (the exact refs that would publish). |
| `stevedore verify <ref>` | Verify a pushed image's cosign signature, SBOM attestation, and SLSA provenance. |
| `stevedore promote <id>` | Copy a released image by digest to other tags or repositories without rebuilding, after verifying its signature. See [`promote`](#promote). |
| `stevedore doctor` | Probe for docker/buildx/git/cosign/syft/grype/crane/aws, report versions, and print install hints for anything your config requires. |
| `stevedore init` | Scaffold a `.stevedore.yaml` by scanning Dockerfiles, or `--from goreleaser` / `--from bake` / `--from services` to import an existing setup (see [Importing a config](importing.md)). |
| `stevedore schema` | Print the JSON Schema for `.stevedore.yaml` (for editor autocomplete/validation). |

## Global flags

| Flag | Description |
|------|-------------|
| `-f, --config` | Path to config file (default: autodiscover `.stevedore.yaml`). |
| `--dir` | Project/repository root (default `.`). |
| `--dry-run` | Print every command that would publish or change anything, without running it, and write no files (see [Dry run](#dry-run)). |
| `-v, --verbose` | Verbose output. |

## `build --push`

`build --push` is a snapshot release cut down to the image itself: it builds
every platform, pushes by digest, **runs the scan and smoke-test gates**, and
only then tags. The gates stay on deliberately — they are cheap next to the
build, and they are what keeps a broken image from ever carrying a tag. Turn
them off in the config (`scan.enabled`, `test.enabled`) if the inner loop has
no scanner installed.

It skips everything that belongs to `release`: no cosign signature, no SBOM,
no changelog, no GitHub release, no `announce`, and **no `notify` webhook** — an
inner-loop push must never be the thing that triggers a deploy. Use `release
--snapshot` when a snapshot should notify.

## Dry run

`--dry-run` prints each command that would publish or change something —
`docker buildx`, `cosign`, `syft`, `gh`, the notify webhook — prefixed
`[dry-run]`, and runs none of them. It writes **no files**: not `dist/` (no
changelog, fingerprints, digests, SBOMs or release summary) and not the GitHub
Actions `$GITHUB_STEP_SUMMARY` / `$GITHUB_OUTPUT` files. `--output json` still
prints the summary to stdout. A dry run therefore leaves the tree exactly as it
found it, so a real release can follow it.

Planning still has to read the world, so a few commands do run:

- **Read-only queries** — `git diff`, `rev-parse`, `merge-base`, `describe`,
  `log` for the changelog, registry tag listing for the `registry`/`ecr`
  versioning strategies. They are shown (prefixed `+`) under `--verbose`.
- **The release-marker fetch** under `change_detection.marker_refs` — `git fetch
  origin +refs/releases/image/*:…`. It changes nothing on the remote but does
  update local refs, and the plan is wrong without it, so it runs and is always
  echoed (prefixed `+`, meaning it ran). Markers are never advanced or pushed
  under `--dry-run`.

## `release` flags

`--snapshot`, `--skip-sign`, `--skip-sbom`, `--skip-scan`, `--skip-test`,
`--skip-changelog`, `--skip-publish`, `--only-changed` / `--changed-since <ref>`
(skip unchanged images — see [Monorepos](monorepo.md)), and `--output json`
(emit a machine-readable release summary to stdout).
`--allow-non-default-branch` lets a real release publish from a commit that is
not on `default_branch`, which is otherwise refused (see
[Versioning](versioning.md); `merge` and `publish` take it too). With
`change_detection.marker_refs` on, `--changed-since` does not replace the release
markers: each image diffs from whichever of its marker and the ref is older, so a
release that failed is still retried (see
[Monorepos](monorepo.md)).

`--only <id,…>` builds just those images, **unconditionally** — selection was the
planner's decision, so change detection is skipped. `--pin-version <id>=<ver>`
(repeatable) makes the run tag exactly what the plan resolved instead of
re-resolving. Both come straight out of a `stevedore plan` entry (`.only` /
`.pins`); see [Matrix mode](monorepo.md#matrix-mode-one-ci-job-per-build).
An `--only` run is one job of a matrix, so — like a `--split` leg — it creates no
GitHub release and posts no announcement; N jobs would otherwise publish N times.
It still notifies `notify.webhook` and advances its own release markers. Run
`stevedore publish` once after the matrix to publish. (`merge --only` behaves
the same way.) `--only all` selects every image, without listing their ids (unless an image
is itself named `all`). An `--only` run's summary gives each image's reason as
`selected via --only`; set `STEVEDORE_PLAN` to the plan document and it reports
the plan's reason instead (`merge` too).

When an image fails — its build, a gate, signing — the release stops building
further images and fails, but the images that already finished are not
forgotten: they are pushed, signed and tagged, so their release markers advance,
`notify.webhook` fires for them and the release summary records them, exactly as
in a release that succeeded. Otherwise the next run would see their new tags and
release them again as a new version. A release that lost an image writes no
changelog and creates no GitHub release or announcement; the re-run does.
`--keep-going` builds every image even after one fails, and fails at the end, so
one broken image does not hold back every healthy one. `merge --keep-going`
likewise merges and publishes every image whose split digests are all present.

`--split <platform>` builds only that platform, natively, and pushes it
**untagged, by digest** — no tags, no sign/scan/SBOM/publish. The digest lands
under `<dist>/digests/<image-id>/` for a later `stevedore merge`, which
assembles the manifest list and runs the whole release tail. A leg builds
only the platforms each image configures: an image whose `platforms` doesn't
include the leg's is skipped (reported with the reason), and writes no digest.
Each leg also records the version it built (`<dist>/digests/<image-id>/version`).
Give `merge` the plan's `--only` and `--pin-version` (its `only` and `pins`
outputs) so it releases exactly that; without them it re-runs change detection
and version resolution, and refuses when it resolves a different version or
skips an image the legs pushed digests for.
See
[Native multi-arch](monorepo.md#native-multi-arch-one-runner-per-platform).

## `publish` flags

`stevedore publish` runs only the publishing end of a release: the changelog, the
GitHub release (`release.github`) and the announcements (`announce`). It is the
single publishing step of a [matrix release](monorepo.md#matrix-mode-one-ci-job-per-build),
run after every `release --only` job has succeeded.

`--only <id,…>` and `--pin-version <id>=<ver>` (repeatable) take the joined
`only` and `pins` of every plan entry, so the announcement lists the images the
matrix built and the release is named after the version it pushed. Without the
pins, a `registry`/`ecr` strategy would re-resolve — and, the jobs having pushed,
resolve the *next* version. Like `release`, it needs a clean checkout, tagged
under the `git` strategy.

## `promote`

```sh
stevedore promote app --from 1.4.0 --to stable                 # retag in place
stevedore promote app --from 1.4.0 --to 1.4.0 --to prod \
  --to-repo registry.example.com/prod/app                     # to another repo
stevedore promote app --from sha256:… --to prod                # roll back by digest
```

`promote` moves an image between environments, or rolls one back, without
rebuilding it. The digest never changes, so the signatures, SBOM attestation and
provenance made at release time still apply.

- **Source**: the image's first configured repository, at `--from` (a tag,
  resolved with `crane digest`, or a `sha256:` digest).
- **Verified first**: the source's cosign signature is checked with the same
  flags as `verify` (`--key`, or `--certificate-identity` +
  `--certificate-oidc-issuer`, defaulting to `sign.cosign.public_key`). An
  unsigned or wrongly signed image is refused before anything is copied.
- **Destinations**: each `--to-repo` (repeatable), or every configured
  repository of the image. A destination other than the source gets the image
  copied by digest with its OCI referrers (`oras copy -r`: cosign v3 bundle
  signatures and attestations) and any tag-based cosign artifacts
  (`sha256-<hex>.sig`/`.att`/`.sbom`, copied with `crane copy`). The signature
  is then verified again in the destination.
- **Tags**: each `--to` (repeatable) is applied with `crane tag` in every
  destination, and only after every verification has passed.

`--dry-run` resolves the digest and prints the copy and tag commands without
running them. `cosign copy` is not used: on cosign v3's default bundle-format
signatures it copies no signature and overwrites the destination's referrers
tag.

## Release summary

Every release also writes `<dist>/release-summary.json` and, in GitHub Actions, a
job-summary table (images, digests, signed/sbom/provenance/test status, vuln
counts) to `$GITHUB_STEP_SUMMARY`. Each image entry carries `repositories`,
`pushed` (false under `--no-push`), and a `reason` — why it built ("src/…
since its release marker") or why it was skipped ("inputs unchanged"). Under
GitHub Actions the compact JSON is also written as a `summary` step output
(republished by the composite action), so workflows can drive per-image
follow-ups — e.g. deploy notifications — filtered on `pushed`.
A real release that ran without scan, smoke test, signing or SBOM — skipped by
flag or disabled in the config — lists those stages in a top-level `degraded`
array, and the job summary opens with a "Degraded release" banner.
`policy.require` turns them into a refusal instead (see
[Configuration](configuration.md)). Each gated image also carries
`platforms`: per platform, whether it was `scanned`, its `vulns`, whether it was `tested` (or `test_skipped` with the
reason), and its `sbom` path; its top-level `vulns` counts the distinct
findings across platforms, and `tested` is true only when every platform was.

## Editor support

```sh
stevedore schema > stevedore.schema.json
```

Then add to the top of `.stevedore.yaml` for autocomplete + validation:

```yaml
# yaml-language-server: $schema=./stevedore.schema.json
```
