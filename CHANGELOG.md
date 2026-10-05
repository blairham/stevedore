# Changelog

All notable changes to stevedore are recorded here. The release workflow
publishes a tag's section as that GitHub release's notes, and fails a release
whose section is missing.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html);
[docs/stability.md](docs/stability.md) says what the v1 contract covers.
Releases up to and including v1.0.6 predate this file; their notes are on the
[GitHub releases page](https://github.com/blairham/stevedore/releases).

## [Unreleased]

### Security

- Tags are applied only after the scan and smoke-test gates pass. Previously
  `buildx --push` (and merge mode's `imagetools create`) published every tag,
  `latest` included, before the gates ran, so a failing gate only withheld
  the signature. (#91)
- New `policy.require: [scan, test, sign, sbom]` refuses a real `release` or
  `merge` in which a required stage is skipped by flag or disabled in config.
  (#116)
- A real `release`, `merge` or `publish` refuses a commit that is not on the
  default branch. (#112)
- Webhook URLs are refused over plain `http://` except to loopback, and are
  redacted to scheme and host in logs. (#110, #117)
- `verify` never uses the private signing key; a new
  `sign.cosign.public_key` is the default verification key, and the keyless
  identity is anchored. (#118)
- The GitHub Action verifies the stevedore archive it downloads against the
  cosign-signed `checksums.txt` before running it. (#124)
- The scanner gate fails closed when the scan output is not the scanner's
  JSON report, and `scan.args` may not redirect that output. (#89)
- Release binaries carry SLSA build provenance from
  `actions/attest-build-provenance`, attached to each release as
  `stevedore-<tag>.intoto.jsonl`. (#28)

### Added

- `stevedore promote` points new tags, in the image's own repositories or
  others, at a released digest without rebuilding it, verifying the cosign
  signature before and after the copy, so signatures and attestations still
  apply. (#137)
- Digest-pinned refs for GitOps: `digest_refs` in the summary, `refs`,
  `digests`, `ref` and `digest` action outputs, and an `outputs:` block that
  renders a template to a file. (#136)
- `stevedore publish`: writes the changelog, creates the GitHub release and
  announces once for a matrix release, after the `--only` legs. (#103)
- `--only all`, and flat `only`/`pins` outputs from `plan` for the final
  publish step; `--only` runs keep the plan's reason in their summary. (#130)
- `--keep-going`, and a failed release still records the images that did
  build. (#108)
- Top-level `image_defaults:` merged under every image, and `{{ .ID }}` in
  templates. (#129)
- Top-level `cache:` (`gha`, `registry`, `local`, `none`), scoped per image
  and platform. (#131)
- Default OCI labels (`source`, `revision`, `version`, `created`; off with
  `default_labels: false`), `.SourceURL` in templates, and an `annotations:`
  block. Images gain these labels with no config change, so their digests
  differ from builds made before. (#125)
- Floating `major` / `major.minor` tags with `.Major`, `.Minor`, `.Patch`,
  `.Prerelease` and `.IsPrerelease` in templates; prereleases never move a
  floating tag. (#114)
- `versioning.require_tag` for tag-driven releases: a version tag on HEAD
  releases every image, an untagged HEAD is a validate-only build. (#120)
- `scan.ignore` entries take a reason and an expiry date, and VEX documents
  pass through to the scanner. (#111)
- `scan.fail_on: none` scans and reports without gating. (#94)
- `notify.webhook.required: false` for best-effort delivery, and a payload
  template. (#126)
- Scan, smoke test and SBOM run for every platform of a multi-arch image;
  `test.platforms` picks native-only or emulated too. (#119)
- `.CommitDate` and `.CommitTimestamp` in templates, and `SOURCE_DATE_EPOCH`
  from the commit time for reproducible builds. (#122)
- Non-GitHub CI: `STEVEDORE_SUMMARY_FILE` / `STEVEDORE_OUTPUTS_FILE` receive
  the step summary and key=value outputs (winning over `$GITHUB_*`), a
  `release.gitlab` target creates a GitLab release via `glab`, and
  docs/ci.md shows a GitLab CI pipeline. (#141)

### Changed

- A commit whose tags already exist, built from that commit, is treated as
  released rather than rebuilt. (#105)
- The release version comes only from semver tags; other tags are ignored.
  (#104)
- An untagged commit is never released under the last tag's version; it gets
  a `-SNAPSHOT-<sha>` version. (#88)
- `plan` combines `--changed-since` with release markers instead of dropping
  them. (#99)
- An image with no `paths` is scoped to its build context. (#90)
- `--dry-run` writes no files and shows the git commands planning runs. (#107)
- Preflight checks `gh` authentication before a run that creates a GitHub
  release, skips registry-listing tools when every version is pinned, and
  refuses unset secrets. (#123)
- A failed command's error carries the tail of its stderr, with hints for
  common registry failures. (#127)
- `fingerprint` skips `bin/` and `obj/` only beside a .NET project file.
  (#106)
- The image's `latest` tag moves forward only. (#121)
- stevedore's own release notes come from this file's section for the tag,
  and a release without one fails. (#138)
- `stevedore schema` lists the allowed values of `scan.scanner`,
  `scan.fail_on`, `versioning.strategy`/`bump`/`lister`, `provenance.mode`,
  `cache.type`/`mode`, `sbom.generator` and `test.platforms`, so an editor
  flags a typo before a release does. (#68)

### Fixed

- Config validation refuses an unknown `changelog.sort` or
  `change_detection.resolver` (values are case-sensitive) and a
  `versioning.initial` that is not `MAJOR.MINOR.PATCH`, instead of silently
  picking a branch or releasing it as the version. (#64)
- `--help` no longer shows a stray word as a flag's value name
  (`--split stevedore merge`, `--pin-version pins`); docs/cli.md lists every
  command's flags, and the docs no longer claim signing, SBOMs and the scan
  are on by default (they are on in the config `stevedore init` writes).
  (#143)
- `merge` refuses when it resolves a different version than the split legs
  built, or (without `--only`) skips an image the legs pushed digests for;
  the documented split workflow passes the plan's `only`/`pins` to `merge`.
  Legs now record the built version under `dist/digests/<id>/version`. (#134)
- The documented matrix workflows build the matrix from the plan's `include`
  list: the whole plan document made `skipped` a matrix dimension, which
  failed the workflow when empty and otherwise built only the last planned
  image, once per skipped one. (#133)
- Smoke-test containers are named and removed when a run times out or is
  canceled, instead of being left running. (#132)
- `doctor` and preflight report `docker buildx` missing when the plugin is
  absent, instead of passing whenever docker is on PATH. (#128)
- `build --push` no longer fires the notify webhook. (#97)
- A run that built nothing creates no GitHub release or announcement, and
  announcements list only what was built. (#101)
- A split release builds and merges only the platforms each image
  configures. (#98)
- `sign.cosign.args` reach `cosign attest` too. (#100)
- Fingerprint state is saved only after a real publish, and `dist/` is
  excluded under a relative `--dir`. (#96, #87)
- The `--only-changed` fingerprint covers file permission bits and
  `extra_flags`, so `chmod +x` or a flag-passed build arg is a change. The
  first run after upgrading rebuilds every image once. (#115)
- Change detection sees both sides of a rename and non-ASCII paths, tolerates
  a leading `./` on path globs, and matches the `origin` remote by name.
  (#95, #109, #113)
- Bake imports resolve a dockerfile relative to its context. (#102)
- The release workflow points the moving `v1`/`v1.0` tags at the release
  commit, not HEAD. (#92)
