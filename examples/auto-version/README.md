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

Things worth noticing:

- The release is published under `v<version>`, and `release.github` creates
  that tag. The next run starts its commit range there, so the loop closes
  without anyone tagging by hand.
- A tag you push yourself still wins: a tagged HEAD releases as its tag.
- `--snapshot` builds always run, as a snapshot of the version they would
  follow (`1.5.0-SNAPSHOT-a1b2c3d`), so PR builds are unaffected.
- `changelog.exclude` hides commits from the notes only; it never changes the
  bump.

The full rules are in [`../../docs/versioning.md`](../../docs/versioning.md).
