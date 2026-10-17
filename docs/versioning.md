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
  # prerelease: rc                # cut 1.5.0-rc.1, rc.2, … instead of 1.5.0
  # pre_major: major              # major (default) | minor: breaking on 0.x is a minor
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
release tag (a prerelease tag does not count: see [prerelease
series](#prerelease-series-prerelease)). Under `registry`/`ecr` the version being bumped comes from the
registry, so the range starts at its git tag (`v<version>`, which
`release.github` creates, or `<version>`). With no such tag it falls back to the
latest reachable version tag, and with none of those to the whole history.

**In a monorepo, each image bumps by its own commits.** Under `registry`/`ecr`
every image is versioned from its own repository, and with `bump: auto` its
bump is decided only by the commits that touched what it is built from — the
same scope change detection uses: its `paths` (or the `.csproj` graph), else
its build context minus its dockerignore, plus `change_detection.shared_paths`.
A `feat` under `api/` makes `api` a minor release and leaves `worker` alone; a
`fix` under `worker/` in the same push makes `worker` a patch; a commit to a
file both are built from (a shared `go.mod`) bumps both. An image with a remote
build context cannot be narrowed and sees every commit.

- An image's range starts at **its own last release**: its release marker
  (`refs/releases/image/<id>`) when `change_detection.marker_refs` is on and the
  marker exists, otherwise the git tag of the version it bumps from, as above.
  Per-image versions rarely each get a git tag, so `marker_refs` is what makes
  the range exact.
- `on_no_changes` applies to each image by itself: with `skip`, an image none
  of whose commits calls for a bump has nothing to release and is skipped while
  the others release; with `patch`, it is released as a patch.
- A pinned `versioning.repo` is one **unified** version for every image, and it
  has to reflect every change, so its range stays repository-wide. So does the
  `git` strategy, which has one tag for the whole release.

**What it does per strategy.**

- `git`: a tag on HEAD still names the release (except a candidate that a
  release run promotes, below). An **untagged HEAD** releases
  as the latest release tag plus the bump (`v1.4.2` + a `feat` → `1.5.0`), and the
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
images:
  api:
    - ghcr.io/acme/api:1.5.0
  worker: 0.3.3 (patch: fix(worker): retry on 503, e4f5a6b)
    - ghcr.io/acme/worker:0.3.3
```

The release line is the first image's; under per-image versioning `check` adds
the version and deciding commit of every image that differs from it, and an
image with nothing to release says so, naming the scope it was narrowed to
(`nothing to release: no commit since 0.3.2 touching services/worker/**, …`).

`bump: auto` works with the `git`, `registry` and `ecr` strategies; the others
take the version as given. It cannot be combined with `require_tag`, which
makes an untagged HEAD validate-only where `auto` makes it a release.

### Prerelease series: `prerelease`

```yaml
versioning:
  bump: auto
  prerelease: rc      # or --prerelease rc on plan/release/merge/publish/check
```

With a prerelease identifier, a run cuts a **candidate** of the version
`bump: auto` computes instead of the version itself. Run by run, from the
release `1.4.0`:

| Run | Commits since `1.4.0` | Version |
|-----|-----------------------|---------|
| `--prerelease rc` | a `feat` | `1.5.0-rc.1` — the series starts |
| `--prerelease rc` | + a `fix` | `1.5.0-rc.2` — same target, next number |
| `--prerelease rc` | + a `feat!` | `2.0.0-rc.1` — a higher bump restarts the series at the new target |
| no prerelease | (same) | `2.0.0` — the series is promoted |

The target is always computed from the last **release**, never from a
candidate, so the commits of the whole series decide it, and a series only
moves up: a lower bump mid-series (a `fix` during `1.5.0-rc.N`) is the next
candidate of the same target, and promotion releases the series' own target
even when nothing since the release would call for that much. With a series in
flight there is always something to release, so `on_no_changes` does not apply.

- **Promoting the candidate's own commit.** Under `git`, a run without a
  prerelease on a commit tagged `v1.5.0-rc.2` releases `1.5.0` from that
  commit — the exact code that was tested. A run *with* `--prerelease rc` there
  keeps the tag, as any tag on HEAD does.
- **Promoting when the config sets `prerelease`.** Pass an empty flag,
  `--prerelease=`. Only a flag that is given overrides the config.
- **What counts as a series** is a prerelease shaped like the ones a run cuts,
  `<identifier>.<number>` (`1.5.0-rc.2`, `1.5.0-beta.3`), above the last
  release. Under `git` that is the latest reachable tag; under `registry`/`ecr`
  the highest such tag in the repository. Other prerelease tags
  (`1.6.0-alpine`, `2.0.0-beta`) are not a series and are left alone.
- **Switching identifiers.** `beta` → `rc` starts `1.5.0-rc.1` after
  `1.5.0-beta.3`. Switching to one that sorts *lower* (`alpha` after `beta`)
  would publish a version below one already out, and is an error.
- **The identifier** is one semver identifier — letters, digits and `-`, not
  all digits, no `.`: `rc`, `beta`, `pre-release`.
- **Tags and releases.** Nothing else changes: the floating-tag rule already
  keeps a prerelease off `latest`, `1` and `1.5` (unless
  `prerelease_floating_tags`), and `release.github.prerelease` already marks the
  GitHub release a prerelease when the version is one.
- **Matrix and split releases** resolve the version in every job, so pass the
  same `--prerelease` to `plan`, each `release` leg, `merge` and `publish` —
  as with `--snapshot`.
- **Changelog range.** The notes still cover the commits since the previous
  version tag, candidate or not: `1.5.0`'s notes start at `1.5.0-rc.2`.

`prerelease` also works with a fixed `bump` under `registry`/`ecr`: the target
is the fixed bump of the highest release, and the series rules are the same.
(Without `prerelease`, a fixed bump ignores prerelease tags as it always has.)
Under `git` without `bump: auto` it is an error — the tag on HEAD is the
version, and tagging `v1.5.0-rc.1` yourself is how that strategy cuts one —
and `static`/`env`/`command` take the version as given.

### Before 1.0: `pre_major`

```yaml
versioning:
  bump: auto
  pre_major: minor    # default: major
```

Semver gives `0.x` no stability promise, and many projects number it as if the
minor were the major. With `pre_major: minor`, while the last release's major
is `0` every level is one step lower: a breaking change bumps the **minor**
(`0.4.2` → `0.5.0`) and a `feat` bumps the **patch** (`0.4.2` → `0.4.3`); a
`fix` is still a patch. `check` says so: `version: 0.5.0 (minor: feat!: …,
a1b2c3d (major on 0.x: versioning.pre_major: minor))`. The default, `major`, is
strict semver: a breaking change on `0.x` releases `1.0.0`.

Under `pre_major: minor` nothing ever leaves `0.x` on its own: release `1.0.0`
deliberately — tag `v1.0.0` (git), or drop `pre_major` for the release that
should be `1.0.0`. It needs `bump: auto`; a fixed `bump: major` is already the
level you chose. It composes with `prerelease` (`0.5.0-rc.1`).

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
