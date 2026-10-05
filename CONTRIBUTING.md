# Contributing to stevedore

Thanks for looking. Issues, bug reports and pull requests are all welcome.

## Before you open a PR

```sh
pre-commit install   # once per clone: formatting, golangci-lint, secrets, YAML
make check           # go vet + go test -race + build
```

golangci-lint runs as the commit hook and in CI, not as a make target; a
failing hook fails the commit, and you fix it and commit again. `make fmt`
applies gofumpt.

You do **not** need docker, cosign, syft or any other external tool to build or
test stevedore. They are only needed to run an actual release. `stevedore doctor`
reports which ones a given config requires and which are missing.

## The shape of a change

stevedore's one design principle is **orchestrate, don't reimplement**. It shells
out to `docker buildx`, `cosign`, `syft`, `grype`/`trivy`, `crane`, `aws` and `gh`
rather than embedding them. That is what keeps the binary around 4.5 MB instead of
carrying their combined ~900 dependencies, and it lets users pin tool versions
independently of stevedore.

So a change that adds a Go library to do something an existing tool already does
is the one kind of PR likely to be turned down. A change that adds a *stage*, an
*importer*, a *version strategy* or a *registry* is squarely in scope.

Common extension points:

| You want to | Look at |
|-------------|---------|
| add a pipeline stage | `internal/pipeline` — the integration layer |
| import an existing setup (`init --from X`) | `internal/importer` |
| add a version strategy | `internal/versioner` |
| add a scanner | `internal/scanner` |
| change the config schema | `internal/config`, then `internal/jsonschema` |

Adding a config field means three edits, not one: the struct in
`internal/config`, its validation, and the JSON Schema that `stevedore schema`
prints for editor autocomplete. A field that validates but has no schema entry
will be flagged as an error inside a user's editor.

## Tests

**New functionality and bug fixes come with tests in the same pull request.**
A bug fix comes with a test that fails without the fix; a new config field,
flag or command comes with a test that exercises it. A PR without one is not
ready to merge.

- `go test -race ./...`, and tests must never touch real user state — use
  `t.TempDir()`.
- Anything that depends on git's behavior should drive **a real git repository**
  (see `internal/gitinfo/branch_test.go` for the pattern). We shipped ten
  releases with every floating tag silently withheld because the tests
  constructed the git state by hand, in a shape a real release never has. If the
  behavior is "what does git report here", a hand-built fixture will agree with
  whatever you already believed.
- External tools are stubbed through `internal/run`; tests do not shell out to
  docker.

## The Contributor License Agreement

Contributions require a signed CLA; the text is in [`CLA.md`](CLA.md).

**Why.** The project may need to offer different licensing terms in future.
That is only possible if one party can license the whole work, and copyright
in a contribution stays with its author unless licensed onward.

The CLA does **not** take your copyright. You keep it; you grant a license
broad enough to include sublicensing, and you affirm the work is your own —
including that no employer holds rights to it.

## Commits and PRs

- Conventional-commit prefixes (`fix:`, `feat:`, `ci:`, `docs:`, `chore:`).
- A user-visible change gets a line under `[Unreleased]` in
  [`CHANGELOG.md`](CHANGELOG.md), written for someone upgrading. That section
  becomes the release notes; commit subjects do not.
- Explain the **why** in the commit message. The diff already says what.
- One change per PR.
- Put `Closes #N` in the PR body so the issue actually closes.
- Commits must be signed.
- Every `.go` file carries the two-line SPDX header (`Apache-2.0`); the
  pre-commit hook fails without it.

## Compatibility

`.stevedore.yaml` carries `version: 1` and that contract is stable — see
[SECURITY.md](SECURITY.md) for the supported-version policy and
[docs/stability.md](docs/stability.md) for what "stable" covers. Adding an
optional field is fine. Renaming one, changing a default, or changing what an
existing field means is a v2 change, and needs a deprecation path first.

## Releasing

Maintainers only. First move the CHANGELOG's `[Unreleased]` entries under
`## [X.Y.Z] - <date>` in a pull request. Then tag `vX.Y.Z` on `main`, on that
commit or a later one; the release workflow does the rest, and refuses a tag
whose CHANGELOG section is missing or empty:
stevedore builds and publishes its own image (dogfooding), GoReleaser publishes
the CLI binary, the GitHub release and the Homebrew formula, and a final job
repoints the moving `vX` / `vX.Y` tags. Prereleases never move those pointers.
