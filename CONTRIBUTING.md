# Contributing to stevedore

Thanks for looking. Issues, bug reports and pull requests are all welcome.

## Before you open a PR

```sh
make check     # lint + test — the same thing CI runs
```

`make check` is `gofumpt` (as a check, not a rewrite), `go vet`, `golangci-lint`,
and `go test -race ./...`. If `gofumpt` complains, `make fmt` fixes it.

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

## Commits and PRs

- Conventional-commit prefixes (`fix:`, `feat:`, `ci:`, `docs:`, `chore:`). The
  changelog is generated from them, and `chore:`/`docs:`/`test:` are filtered out
  of release notes.
- Explain the **why** in the commit message. The diff already says what.
- One change per PR.
- Put `Closes #N` in the PR body so the issue actually closes.

## Compatibility

`.stevedore.yaml` carries `version: 1` and that contract is stable — see
[SECURITY.md](SECURITY.md) for the supported-version policy and
[docs/stability.md](docs/stability.md) for what "stable" covers. Adding an
optional field is fine. Renaming one, changing a default, or changing what an
existing field means is a v2 change, and needs a deprecation path first.

## Releasing

Maintainers only. Tag `vX.Y.Z` on `main`; the release workflow does the rest:
stevedore builds and publishes its own image (dogfooding), GoReleaser publishes
the CLI binary, the GitHub release and the Homebrew formula, and a final job
repoints the moving `vX` / `vX.Y` tags. Prereleases never move those pointers.
