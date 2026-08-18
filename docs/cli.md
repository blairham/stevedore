# Commands

Every command, every flag, and the JSON surfaces meant for machines.

| Command | What it does |
|---------|--------------|
| `stevedore release` | Full pipeline: build all platforms → push → sign → SBOM → changelog. Requires a clean, tagged checkout unless `--snapshot`. `--split <platform>` builds one native-arch leg of a split release (see [Native multi-arch](monorepo.md#native-multi-arch-one-runner-per-platform)). |
| `stevedore merge` | Second half of a split release: stitch the legs' per-arch digests into tagged manifest lists (`imagetools create`) and run the release tail (scan, test, sign, SBOM, changelog, publish). |
| `stevedore plan` | Resolve versions, change detection, and build-once grouping — print the plan as JSON without building. The `include` array is GitHub Actions matrix shape (see [Matrix mode](monorepo.md#matrix-mode-one-ci-job-per-build)); `--split-platforms` emits one entry per platform with native runner hints. |
| `stevedore build` | Inner-loop build: one platform, loaded into the local docker daemon, no push. `--push` publishes multi-arch but skips the release extras. |
| `stevedore check` | Validate the config and print the fully-resolved release plan (the exact refs that would publish). |
| `stevedore verify <ref>` | Verify a pushed image's cosign signature, SBOM attestation, and SLSA provenance. |
| `stevedore doctor` | Probe for docker/buildx/git/cosign/syft/grype/crane/aws, report versions, and print install hints for anything your config requires. |
| `stevedore init` | Scaffold a `.stevedore.yaml` by scanning Dockerfiles, or `--from goreleaser` / `--from bake` / `--from services` to import an existing setup (see [Importing a config](importing.md)). |
| `stevedore schema` | Print the JSON Schema for `.stevedore.yaml` (for editor autocomplete/validation). |

## Global flags

| Flag | Description |
|------|-------------|
| `-f, --config` | Path to config file (default: autodiscover `.stevedore.yaml`). |
| `--dir` | Project/repository root (default `.`). |
| `--dry-run` | Print every command without executing it. |
| `-v, --verbose` | Verbose output. |

## `release` flags

`--snapshot`, `--skip-sign`, `--skip-sbom`, `--skip-scan`, `--skip-test`,
`--skip-changelog`, `--skip-publish`, `--only-changed` / `--changed-since <ref>`
(skip unchanged images — see [Monorepos](monorepo.md)), and `--output json`
(emit a machine-readable release summary to stdout).

`--only <id,…>` builds just those images, **unconditionally** — selection was the
planner's decision, so change detection is skipped. `--pin-version <id>=<ver>`
(repeatable) makes the run tag exactly what the plan resolved instead of
re-resolving. Both come straight out of a `stevedore plan` entry (`.only` /
`.pins`); see [Matrix mode](monorepo.md#matrix-mode-one-ci-job-per-build).

`--split <platform>` builds only that platform, natively, and pushes it
**untagged, by digest** — no tags, no sign/scan/SBOM/publish. The digest lands
under `<dist>/digests/<image-id>/` for a later `stevedore merge`, which
assembles the manifest list and runs the whole release tail. See
[Native multi-arch](monorepo.md#native-multi-arch-one-runner-per-platform).

Every release also writes `<dist>/release-summary.json` and, in GitHub Actions, a
job-summary table (images, digests, signed/sbom/provenance/test status, vuln
counts) to `$GITHUB_STEP_SUMMARY`. Each image entry carries `repositories`,
`pushed` (false under `--no-push`), and a `reason` — why it built ("src/…
since its release marker") or why it was skipped ("inputs unchanged"). Under
GitHub Actions the compact JSON is also written as a `summary` step output
(republished by the composite action), so workflows can drive per-image
follow-ups — e.g. deploy notifications — filtered on `pushed`.

## Editor support

```sh
stevedore schema > stevedore.schema.json
```

Then add to the top of `.stevedore.yaml` for autocomplete + validation:

```yaml
# yaml-language-server: $schema=./stevedore.schema.json
```
