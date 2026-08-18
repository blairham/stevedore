# Stability contract

What v1 promises, and what it deliberately does not. The short version: **your
`.stevedore.yaml` keeps working, and so do the refs stevedore publishes.**

## Covered by semver

These are the surfaces you build a pipeline on. A breaking change to any of them
means a new major version.

| Surface | Promise |
|---------|---------|
| `.stevedore.yaml` `version: 1` | Every field keeps its name, type and meaning. New fields are optional and default to today's behavior. |
| Command names and flags | `release`, `merge`, `plan`, `build`, `check`, `verify`, `doctor`, `init`, `schema` and their flags keep working. Flags may be added; existing ones are not removed or repurposed. |
| Exit codes | `0` success, non-zero failure. A gate that blocks a release keeps failing the command. |
| Published refs | The tags stevedore computes from a given config and git state do not change. This is the one people forget: a "better" tag scheme is a breaking change, because it silently stops overwriting what a deployment pulls. |
| `plan --output json` and the release summary | Field names and types are stable; new fields may appear. Consume it by key, not by position. |
| The GitHub Action's inputs and outputs | Stable, and the moving `v1` tag never crosses a major. |
| The `latest`-style floating-tag rule | Floating tags publish on the default branch of a real release, and never from a snapshot. |

## Not covered

- **The Go packages.** Everything lives under `internal/`, which Go itself
  prevents you from importing. stevedore is a CLI, not a library. If you want a
  library API, open an issue and say what you would build with it — it is not a
  no, it is an unmade decision.
- **Human-readable stdout.** The progress output (`==> building …`) is for
  people. Parse `--output json` or the action's `summary` output instead; that is
  what they are for.
- **Which external tool is invoked, and how.** stevedore orchestrates rather than
  reimplements, so the exact `docker buildx` argv is an implementation detail and
  will change as those tools change. The *outcome* — what gets built, tagged,
  signed and pushed — is what is stable.
- **Exact wording of errors and warnings.** Do not grep them.
- **The tool versions stevedore works with.** New cosign/syft/grype releases can
  change behavior underneath us. `stevedore doctor` reports what it found.

## Deprecation

A field or flag being removed in the next major is announced by:

1. a warning on stderr naming the replacement, for at least one minor release, and
2. an entry in the release notes.

Nothing is removed in a patch release, and nothing is removed without that
warning having shipped first.

## Versioning of the artifacts themselves

- **Binary and image**: `X.Y.Z`, plus the moving `X` and `X.Y` git tags for the
  Action, plus `latest` on the image.
- **Config schema**: the top-level `version:` key, which is `1` and stays `1`
  until there is a `2`. It is deliberately not tied to the binary version — a
  stevedore 1.9 binary reads a `version: 1` config, and so will 1.0.
