# How tags are resolved

Where the version comes from, and how it becomes a set of published tags.

The set of published references is the **cartesian product of `repositories` × `tags`**.
Tags are Go templates rendered against git state (see [Template context](configuration.md#template-context)).

**Floating tags** — `latest` or anything ending in `-latest` — are treated specially:
they only publish on the **default branch** of a **non-snapshot** release. This means
`latest` never accidentally moves from a feature branch or a snapshot build, while
immutable tags like the version and commit SHA always publish.

Signing and SBOM generation happen **by digest** (`repo@sha256:…`), not by tag, so the
exact artifact is pinned regardless of how many mutable tags point at it.

# Versioning

The release version can be derived several ways via the `versioning:` block. The
default is `git`; the others let you avoid relying on git tags entirely.

| Strategy | Where the version comes from |
|----------|------------------------------|
| `git` (default) | Git tags. Clean, tagged checkout → the tag with any leading `v` stripped (`v1.4.0` → `1.4.0`). Otherwise a snapshot like `1.4.0-SNAPSHOT-9f8e7d6` (`-dirty` if the tree is dirty). |
| `registry` | Lists the existing tags in a registry repo (via `crane`), takes the highest semver, and bumps it by `patch`/`minor`/`major`. Non-semver tags (`latest`, commit SHAs) are ignored. |
| `ecr` | Like `registry`, but lists tags via `aws ecr describe-images` using your AWS credentials directly — no `crane` or docker credential helper. Region is inferred from the ECR host (override with `region:`). |
| `static` | An explicit `value:`. |
| `env` | Read from an environment variable. |
| `command` | The trimmed stdout of a command — an escape hatch for anything. |

In a multi-image config, `registry`/`ecr` version **each image independently from
its own repo**, preserving per-service versions. Set `repo:` to pin one repo for a
single unified version instead. A repo with no semver tags starts at
`versioning.initial` (default `0.1.0`). `stevedore check` never hard-fails on an
unreachable registry — it warns and shows a placeholder so the rest of the config
still validates offline.

`stevedore release` refuses to run on a dirty tree unless you pass `--snapshot`. A
git tag on HEAD is required **only** for the `git` strategy — the others source the
version elsewhere, which is handy when your tags drift out of sync with what's
actually published.

## Deriving the version from ECR

```yaml
versioning:
  strategy: ecr
  bump: minor            # patch (default) | minor | major
  # region: us-east-1    # optional; inferred from the ECR host otherwise
  # repo: "..."          # optional; defaults to each image's own repository
```

The `ecr` strategy shells out to `aws ecr describe-images` using your AWS
credentials directly (SSO profile / env / IRSA) — **no `crane` and no docker
credential helper**. In CI, run `aws-actions/configure-aws-credentials` first.

If you'd rather use `crane` (works across ghcr/Docker Hub/ECR via the Docker
credential chain), use `strategy: registry` with the
[`amazon-ecr-credential-helper`](https://github.com/awslabs/amazon-ecr-credential-helper)
configured. `stevedore check` prints the version it resolves, so you can preview the
next release without publishing anything.
