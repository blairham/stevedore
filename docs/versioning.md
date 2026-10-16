# How tags are resolved

Where the version comes from, and how it becomes a set of published tags.

The set of published references is the **cartesian product of `repositories` × `tags`**.
Tags are Go templates rendered against git state (see [Template context](configuration.md#template-context)).

**Floating tags** are pointers that move from release to release. A tag is floating
when it is:

- named like one — `latest`, or anything ending in `-latest`;
- built from `{{ .Major }}` or `{{ .Minor }}` without anything that pins one release —
  `{{ .Major }}`, `v{{ .Major }}.{{ .Minor }}`, `{{ .Major }}-alpine` are floating;
  `{{ .Major }}.{{ .Minor }}.{{ .Patch }}` or `{{ .Major }}-{{ .ShortCommit }}` are not
  (the pinning fields are `.Patch`, `.Prerelease`, `.Version`, `.Tag`, `.LatestTag`,
  `.Commit`, `.ShortCommit`, `.Date`, `.Timestamp`, `.CommitDate`, `.CommitTimestamp`);
- or spelled as the version's own major or major.minor however it was produced
  (`1`, `v1.4` for `1.4.2`).

Floating tags only publish on the **default branch** of a **non-snapshot**, **non-prerelease**
release, and they are applied last. So `latest` never accidentally moves from a feature
branch or a snapshot build, a release candidate (`v1.5.0-rc.1`) never moves `latest`,
`1` or `1.5`, while immutable tags like the version and commit SHA always publish. Set
`prerelease_floating_tags: true` to let a prerelease move them anyway. A prerelease
also marks the GitHub release as a prerelease unless `release.github.prerelease` says
otherwise.

The equivalent of docker/metadata-action's `type=semver` set:

```yaml
tags:
  - "{{ .Version }}"                 # 1.4.2       (pattern={{version}})
  - "{{ .Major }}.{{ .Minor }}"      # 1.4         (pattern={{major}}.{{minor}})
  - "{{ .Major }}"                   # 1           (pattern={{major}})
  - "latest"
```

**Re-releasing a commit.** A tag that contains the commit SHA (`{{ .ShortCommit }}`,
`{{ .Commit }}`, `main-{{ .ShortCommit }}`, …) names exactly one build of one
commit, so before building, stevedore looks each one up (`docker buildx imagetools
inspect`). If every commit tag of an image already exists and was built from this
commit — its `org.opencontainers.image.revision` label matches, or it has none — the
image is reported **already released**: nothing is rebuilt or re-tagged, and its
release marker still advances. This is what makes re-running a release job safe against
an immutable registry (ECR tag immutability and the like). A commit tag whose revision
label names a *different* commit, or a set where only some commit tags exist (an
earlier run that stopped partway), fails the run before anything is pushed. Images with
no commit tag are unaffected. Within each repository, tags are applied commit tags
first and floating tags last, so a collision the lookup could not see still fails before
the version tag moves.

Signing and SBOM generation happen **by digest** (`repo@sha256:…`), not by tag, so the
exact artifact is pinned regardless of how many mutable tags point at it.

# Versioning

The release version can be derived several ways via the `versioning:` block. The
default is `git`; the others let you avoid relying on git tags entirely.

| Strategy | Where the version comes from |
|----------|------------------------------|
| `git` (default) | Git tags. Clean checkout with a version tag **on HEAD** → the tag with any leading `v` stripped (`v1.4.0` → `1.4.0`; the highest wins when HEAD has several). Only semver tags count, with or without the `v` (`v1.4.0`, `1.4.0`, `v2.0.0-rc.1`); others such as `deploy-prod` are ignored here and for the latest/previous tag. Otherwise — HEAD untagged, dirty tree, or `--snapshot` — a snapshot of the latest reachable tag like `1.4.0-SNAPSHOT-9f8e7d6` (`-dirty` if the tree is dirty; `0.0.0-SNAPSHOT-…` with no tags at all). With [`bump: auto`](#versions-from-commits-bump-auto), an untagged HEAD is a release of the latest tag plus the bump its commits call for. |
| `registry` | Lists the existing tags in a registry repo (via `crane`), takes the highest release (semver without a prerelease), and bumps it by `patch`/`minor`/`major`, or by what the commits call for under [`bump: auto`](#versions-from-commits-bump-auto). Non-semver tags (`latest`, commit SHAs) are ignored. |
| `ecr` | Like `registry`, but lists tags via `aws ecr describe-images` using your AWS credentials directly — no `crane` or docker credential helper. Region is inferred from the ECR host (override with `region:`). |
| `static` | An explicit `value:`. |
| `env` | Read from an environment variable. |
| `command` | The trimmed stdout of a command — an escape hatch for anything. |

In a multi-image config, `registry`/`ecr` version **each image independently from
its own repo**, preserving per-service versions. Set `repo:` to pin one repo for a
single unified version instead. A repo with no semver tags starts at
`versioning.initial` (default `0.1.0`). A run in which every image's version is
pinned (`--pin-version`, as split legs and matrix jobs get from the plan) reads
no registry, and does not need `crane` or `aws`. `stevedore check` never hard-fails on an
unreachable registry — it warns and shows a placeholder so the rest of the config
still validates offline.

`stevedore release` refuses to run on a dirty tree unless you pass `--snapshot`. A
git tag on HEAD is required **only** for the `git` strategy without `bump: auto` (a tag on an earlier
commit does not count: releasing under it would overwrite that release's image
tags from a different commit). The others source the version elsewhere, which is handy when your tags drift out of sync with what's
actually published.

A real release (anything but `--snapshot`, `--no-push` or a `--split` leg) must
also be cut from a commit on `default_branch`: HEAD has to be reachable from
`origin/<default_branch>` (fetched when missing or stale; the local branch when
there is no `origin` remote). Otherwise a manual dispatch from a feature branch
would publish real versions of unmerged code. A shallow clone cannot prove it
either way and is refused with a hint — check out with `fetch-depth: 0`.
`--allow-non-default-branch` (on `release`, `merge` and `publish`) overrides it.
`merge` is the step that tags a split release, so it is the one held to the
branch; the legs only push untagged digests.

## Versions from commits: `bump: auto`

```yaml
versioning:
  bump: auto
  # bump_types: {revert: patch}   # merged over the defaults below
  # on_no_changes: skip           # skip (default) | patch
```

`bump: auto` reads the [Conventional Commits](https://www.conventionalcommits.org/)
since the last release and bumps by the highest level any of them calls for:

| Commit | Bump |
|--------|------|
| breaking — `feat!: …`, `fix(api)!: …`, or a `BREAKING CHANGE:` / `BREAKING-CHANGE:` footer in the body | major |
| `feat` | minor |
| `fix`, `perf` | patch |
| anything else (`docs`, `chore`, `refactor`, a subject with no type) | none |

Types are case-insensitive (`Feat:` is a feature). The footer is not: the spec
spells it in capitals. A breaking commit is a major bump whatever its type, and
that is the same rule the changelog uses to mark it **BREAKING**.

`bump_types` changes the mapping key by key over those defaults: `revert: patch`
adds a type, `perf: minor` changes one, `fix: none` turns one off. Levels are
`none`, `patch`, `minor` and `major`. `changelog.exclude` only hides commits from
the notes; it has no effect on the bump.

**Where the range starts.** Under `git`, the commits since the latest reachable
version tag. Under `registry`/`ecr` the version being bumped comes from the
registry, so the range starts at its git tag (`v<version>`, which
`release.github` creates, or `<version>`). With no such tag it falls back to the
latest reachable version tag, and with none of those to the whole history. The
range covers the whole repository: in a monorepo, every commit counts for every
image.

**What it does per strategy.**

- `git`: a tag on HEAD still names the release. An **untagged HEAD** releases
  as the latest tag plus the bump (`v1.4.2` + a `feat` → `1.5.0`), and the
  release publishes, and `release.github` creates, the tag `v1.5.0`. With no
  version tag at all the first release is `versioning.initial`.
- `registry` / `ecr`: the bump replaces `patch`/`minor`/`major`; a repository
  with no releases still starts at `versioning.initial`.

**Nothing to bump.** When no commit since the last release calls for a bump,
`on_no_changes: skip` (the default) means there is nothing to release: `plan`
and `release` skip every image with `nothing to release: …`, and nothing is
built, tagged or announced. `on_no_changes: patch` releases a patch bump
anyway. A `--snapshot` always builds, as a snapshot of the version it would
follow.

**Why that version.** `stevedore check`, `plan` (on stderr, and as the plan
entry's `version_reasons`) and `release` print the version with the commit that
decided it:

```
version:  1.5.0 (minor: feat(api): add pagination, a1b2c3d)
```

`bump: auto` works with the `git`, `registry` and `ecr` strategies; the others
take the version as given. It cannot be combined with `require_tag`, which
makes an untagged HEAD validate-only where `auto` makes it a release.

## Tag-driven releases: `require_tag`

```yaml
versioning:
  require_tag: true
```

With `require_tag`, a version tag on HEAD is what makes a run a release, so one
release job serves every push:

- **HEAD has a version tag** → a real release of **every image**. Change
  detection (`marker_refs`, `--changed-since`, `--only-changed`) is bypassed: a
  tag on a commit whose sources did not change since the last release is still a
  release, and would otherwise be skipped. `plan` lists every image too.
  `--only` still narrows the run.
- **HEAD is untagged** → a **validate-only build**: the run becomes `--snapshot
  --no-push`, builds what change detection selects, and pushes nothing. A
  `release --split` leg or a `merge` has no validate-only form and is refused.

`--snapshot` and `--no-push` runs are unaffected.

## Deriving the version from ECR

```yaml
versioning:
  strategy: ecr
  bump: minor            # patch (default) | minor | major | auto
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
