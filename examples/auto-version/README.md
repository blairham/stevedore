# Versions from commits

`versioning.bump: auto`: every merge to `main` is a release candidate, and the
Conventional Commits since the last release choose its version. Nobody picks a
number or pushes a tag.

```sh
cp .stevedore.yaml /path/to/your/repo/
stevedore check
# version:  1.5.0 (minor: feat(api): add pagination, a1b2c3d)
```

| Since the last tag | Next version from `v1.4.2` |
|--------------------|----------------------------|
| `fix: crash on start` | `1.4.3` |
| `feat(api): add pagination` | `1.5.0` |
| `refactor!: drop the v1 config` | `2.0.0` |
| `docs: typo` only | nothing to release |

Release candidates come from the same commits. With `--prerelease rc` (or
`versioning.prerelease: rc`):

| Run | Version |
|-----|---------|
| `release --prerelease rc` after a `feat` | `1.5.0-rc.1` |
| `release --prerelease rc` after another `fix` | `1.5.0-rc.2` |
| `release --prerelease rc` after a `feat!` | `2.0.0-rc.1` |
| `release` | `2.0.0` |

Candidates never move `latest`, `1` or `1.5`, and the GitHub release is marked
a prerelease.

Things worth noticing:

- The release is published under `v<version>`, and `release.github` creates
  that tag. The next run starts its commit range there, so the loop closes
  without anyone tagging by hand.
- A tag you push yourself still wins: a tagged HEAD releases as its tag.
- `--snapshot` builds always run, as a snapshot of the version they would
  follow (`1.5.0-SNAPSHOT-a1b2c3d`), so PR builds are unaffected.
- `changelog.exclude` hides commits from the notes only; it never changes the
  bump.
- `pre_major: minor` only matters while the version is `0.x`: from `v0.4.2` a
  `refactor!:` releases `0.5.0` and a `feat:` `0.4.3`. Without it (strict
  semver) the breaking change would release `1.0.0`.

The full rules are in [`../../docs/versioning.md`](../../docs/versioning.md).
