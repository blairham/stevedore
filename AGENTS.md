# AGENTS.md

Guidance for AI coding agents (Claude Code, Cursor, Copilot, Codex, OpenCode, …)
working in this repository. This is the **cross-tool single source of truth** —
`CLAUDE.md` imports it, so keep durable project context here, not there.

`README.md` is the front door — the pitch, install, quick start, and an index.
The user-facing reference lives in `docs/` (configuration, CLI, versioning,
supply chain, monorepos, CI, comparison, stability). This file covers what
neither does: the internal layout and the conventions you'd otherwise
reconstruct by reading a dozen files.

`examples/` is documentation people copy rather than read, so it is checked like
code — the `examples` CI job resolves every example config with the real binary.
Changing config behavior means updating the examples in the same PR.

## Project Overview

stevedore is a CLI that releases Docker/OCI images the way GoReleaser releases
binaries: from one declarative `.stevedore.yaml` it builds multi-arch images,
tags them from git or a registry, pushes to one or more repos, **gates on a
vulnerability scan and a smoke test**, signs with cosign, generates an SBOM,
emits SLSA provenance, writes a changelog, and cuts a GitHub release.

Design principle: **orchestrate, don't reimplement.** stevedore shells out to
`docker buildx`, `cosign`, `syft`, `grype`/`trivy`, `crane`, `aws`, and `gh`
rather than embedding them — which keeps the binary tiny and lets users pin tool
versions. Everything is verified up front by a preflight check (`stevedore
doctor`).

## Quick Reference

Prefer `make` — run `make help` to list targets.

```sh
make build     # go build ./...
make test      # go test -race ./...
make lint      # gofumpt (check) + go vet + golangci-lint
make check     # lint + test (mirror CI locally, run before a PR)
make fmt       # go tool gofumpt -w .
make tidy      # go mod tidy  (after changing deps)
```

External tools are only needed to actually *run* a release locally (not to build
or test stevedore itself): `docker`+`buildx`, `git`, `cosign`, `syft`,
`grype`/`trivy`, `crane`, `aws`, `gh`. `stevedore doctor` reports which are
missing for a given config.

## Project Structure

Standard Go layout: `main.go` → `cmd/` (cobra CLI) → `internal/` (the logic).

| Package | Purpose |
|---------|---------|
| `cmd` | cobra commands: release, merge, plan, build, check, verify, doctor, init, schema |
| `internal/config` | `.stevedore.yaml` schema, load, defaults, validation |
| `internal/pipeline` | orchestrates the whole release; the integration layer |
| `internal/builder` | `docker buildx` build/push (multi-arch, provenance, cache) |
| `internal/run` | external-command runner (dry-run + verbose aware) |
| `internal/gitinfo` | git-derived version/tag/commit/branch |
| `internal/tmpl` | Go-template rendering of tags/labels/build-args |
| `internal/versioner` | version strategies: git/registry/ecr/static/env/command |
| `internal/scanner` | grype/trivy vulnerability scan gate |
| `internal/tester` | post-build smoke-test gate (`docker run`) |
| `internal/signer` | cosign sign + SBOM attestation |
| `internal/sbom` | syft SBOM generation |
| `internal/sbomdiff` | dependency diff between two SBOMs |
| `internal/verifier` | verify a pushed image's signature/attestation/provenance |
| `internal/changelog` | conventional-commit changelog |
| `internal/changed` | git-diff change detection + glob matching |
| `internal/fingerprint` | content-hash change detection (`--only-changed`) |
| `internal/projgraph` | .NET `.csproj` dependency-graph resolver |
| `internal/publish` | GitHub release (`gh`) + Slack/Discord announce + notify webhook |
| `internal/summary` | JSON + GitHub-step-summary release report |
| `internal/preflight` | verify required external tools are installed |
| `internal/scaffold` | `stevedore init` — scan Dockerfiles |
| `internal/importer` | import from a goreleaser, docker-bake, or per-service manifest config |
| `internal/jsonschema` | JSON Schema of the config, from the Go structs |

## Code Conventions

- **Every external command goes through `internal/run`** so dry-run and verbose
  are honored uniformly. Don't call `exec.Command` directly in pipeline logic.
- **Pure logic is unit-tested; shell-out paths are thin.** Parsers, matchers,
  version bumps, and renderers have table tests; the `docker`/`cosign`/etc.
  invocations are kept small.
- **Config is validated strictly** — unknown YAML fields are rejected
  (`KnownFields(true)`), so typos fail fast.
- **Templates use `missingkey=error`** — referencing an undefined field is an
  error, never a silent empty string.
- Keep the binary dependency-light; think twice before adding a module.
- **Formatter is gofumpt**, pinned in `go.mod`'s `tool` block and run as
  `go tool gofumpt`. At commit time formatting is applied by the
  `golangci-lint-fmt` hook, which runs every formatter in `.golangci.yml`
  (gofumpt AND goimports) — a superset of what `make fmt` does.

## Architecture — the release pipeline

`internal/pipeline` runs the stages in this order, so a bad image is never signed
or shipped:

```
build → scan (gate) → smoke test (gate) → sign → SBOM + attest → provenance
      → changelog (+ dependency diff) → GitHub release → announce → summary
```

The version feeding the tags is resolved first (`internal/versioner`); under the
`registry`/`ecr` strategies each image is versioned independently from its own
repo. Change detection (`--only-changed` / `--changed-since`) can skip images
whose scoped paths didn't change, using per-image globs or the `.csproj` graph.

Split mode spreads the build across native-arch CI runners: `release --split
<platform>` legs push untagged by digest (recorded under `dist/digests/`), and
`stevedore merge` assembles the tagged manifest lists and runs the tail stages.
`plan --split-platforms` emits the per-platform matrix (with runner hints) that
drives the fan-out.

## Releasing stevedore itself

Two tools, one per artifact kind: **stevedore** releases its own container image
(dogfooding, `.stevedore.yaml`), and **GoReleaser** (`.goreleaser.yaml`)
publishes the CLI binary + Homebrew formula + GitHub release. See
`.github/workflows/release.yml`.

## Working agreements

- Run `make check` before proposing a change; keep the tree `gofumpt`-clean.
- `pre-commit install` once per clone. Hooks: hygiene (trailing whitespace,
  EOF, YAML, large files, merge conflicts, private keys), `go-mod-tidy-repo`,
  `go-fumpt-repo`, and gitleaks (allowlist in `.gitleaks.toml`).
- **Linter is golangci-lint v2**, `go tool`-pinned, config in `.golangci.yml`.
  It runs in CI and in `make lint` — not in pre-commit, where it's too slow
  for every commit. `go tool golangci-lint fmt` applies the formatter fixes.
- Add/extend table tests for pure logic you touch.
- Don't commit, push, tag, or create releases unless explicitly asked.
