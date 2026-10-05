# Configuration

Every field of `.stevedore.yaml`, the template context, and the changelog.

stevedore looks for `.stevedore.yaml`, `.stevedore.yml`, `stevedore.yaml`, or
`stevedore.yml` (in that order). Unknown fields are rejected, so typos fail fast.

```yaml
version: 1

project_name: myapp
default_branch: main      # branch real releases must be cut from; floating tags publish only here
prerelease_floating_tags: false   # let a prerelease (1.3.0-rc.1) move floating
                          # tags too; by default it publishes only immutable ones
default_labels: true      # set org.opencontainers.image.{source,revision,
                          # version,created} on every image (see below)
source_date_epoch: true   # pass SOURCE_DATE_EPOCH=<commit time> to every build
                          # (build arg + buildx env); false to opt out
dist: dist                # output dir for SBOMs and the changelog

images:
  - id: myapp             # stable identifier used in logs/artifact names
    dockerfile: Dockerfile
    context: .
    target: ""            # optional multi-stage target
    platforms:
      - linux/amd64
      - linux/arm64
    repositories:         # destinations without a tag
      - ghcr.io/acme/myapp
      - registry.acme.io/myapp
    tags:                 # Go templates; floating tags gated to default branch
      - "{{ .Version }}"
      - "{{ .ShortCommit }}"
      - "{{ .Major }}.{{ .Minor }}"   # floating: 1.4 follows the newest 1.4.x
      - "{{ .Major }}"                # floating: 1 follows the newest 1.x.y
      - "latest"
    build_args:
      - "VERSION={{ .Version }}"
    labels:               # override the default OCI labels key by key
      org.opencontainers.image.licenses: "Apache-2.0"
      # a hand-written created label: use .CommitDate, not .Date (see below)
    annotations:          # OCI annotations: on the manifest, and on the index
                          # of a multi-platform image (a split build's merged
                          # list gets them at index level); pushed images only
      org.opencontainers.image.description: "myapp, the service"
    secrets:              # BuildKit --secret entries. An env-backed secret
      - id: github_token  # whose variable is unset or empty is SKIPPED in a
        env: GITHUB_TOKEN # --snapshot build, so local builds work without it;
        # optional: true  # a real release REFUSES to start without it, unless
      # - id: npmrc        # the secret is marked optional.
      #   file: ./.npmrc
    cache_from:           # buildx --cache-from sources; empty-rendering
      # entries are skipped, so an env-templated value enables caching only
      # where the environment provides it (e.g. CI) and stays inert locally
      - "type=registry,ref=ghcr.io/acme/myapp:buildcache"
      # - '{{ index .Env "STEVEDORE_CACHE_FROM" }}'
    cache_to:             # buildx --cache-to destinations (same skip rule)
      - "type=registry,ref=ghcr.io/acme/myapp:buildcache,mode=max"
      # - '{{ index .Env "STEVEDORE_CACHE_TO" }}'
    paths:                # change-detection globs (see Monorepos); ** supported.
      - "services/myapp/**"   # omitted: the build context minus .dockerignore,
                              # plus the Dockerfile and this config
    project: ""           # or a .csproj to auto-derive paths from its graph
    extra_flags: []       # passed verbatim to `docker buildx build`

sign:
  cosign:
    enabled: true
    key: ""               # private signing key; omit for keyless (OIDC) signing
    public_key: ""        # its public key: the default for `stevedore verify --key`
    args: []              # extra flags for both cosign sign and cosign attest

sbom:
  enabled: true
  generator: syft         # only syft is supported
  format: spdx-json       # or cyclonedx-json
  attest: true            # attach a signed SBOM attestation (needs cosign)

scan:
  enabled: true
  scanner: grype          # grype (default) | trivy
  fail_on: high           # block the release at this severity or above;
                          # negligible|low|medium|high|critical, or none to
                          # scan and report without gating (default: critical)
  ignore:                 # vulnerabilities to exclude from the gate
    - CVE-2024-0000       # a bare ID applies until removed
    - id: CVE-2024-1111   # or record why, and until when:
      reason: not reachable; no fixed release upstream
      expires: 2026-12-31 # YYYY-MM-DD, last day it applies (UTC); after
                          # that the finding counts again and the release
                          # log warns that the ignore expired
  vex: []                 # VEX documents (OpenVEX, CSAF, CycloneDX) passed
                          # to the scanner as --vex; must exist
  args: []                # extra flags passed to the scanner; output flags
                          # (trivy -f/--format/-o/--output/-t/--template,
                          # grype -o/--output/--file/-t/--template) are
                          # rejected: the gate must read the JSON report

provenance:
  enabled: true           # emit a SLSA build-provenance attestation (push only)
  mode: max               # min | max (max records the full build definition)

test:
  enabled: true           # smoke-test the built image before signing/tagging
  cmd: ["/usr/bin/myapp", "--version"]   # run inside the container
  expect_exit: 0          # required exit code
  timeout: 30s            # Go duration; default 60s

versioning:
  strategy: git           # git (default) | registry | ecr | static | env | command
  # registry/ecr strategies:
  # bump: patch           # patch | minor | major
  # repo: ghcr.io/acme/myapp   # defaults to each image's own repository
  # region: us-east-1     # ecr only; inferred from the ECR host otherwise
  # initial: "0.1.0"      # when the repo has no semver tags yet
  # require_tag: true    # tag on HEAD = release every image; untagged = validate-only build

change_detection:         # scope --only-changed / --changed-since for monorepos
  resolver: ""            # "dotnet" auto-derives per-image paths from .csproj refs
  shared_paths:           # a change here rebuilds every image
    - "Dockerfile"
    - "*.sln"

changelog:
  enabled: true
  sort: asc               # asc | desc
  exclude:                # drop commits whose subject matches any regex
    - "^chore:"
    - "^docs:"
    - "^test:"
  dependency_diff: true   # append packages added/removed/upgraded since the
                          # previous release (diffs this SBOM vs the previous tag's)

release:
  github:
    enabled: true         # create a GitHub release (via gh) with the changelog
    draft: false
    # prerelease: true    # unset (default): marked a prerelease exactly when the
                          # version is one (1.3.0-rc.1); true/false force it

announce:
  slack:
    enabled: true
    webhook_env: SLACK_WEBHOOK   # env var holding the webhook URL
    template: "🚀 {{ .ProjectName }} {{ .Version }} shipped"   # optional
  discord:
    enabled: false
    webhook_env: DISCORD_WEBHOOK

notify:                   # machine-readable post-push notification (CD trigger)
  webhook:
    enabled: true
    url_env: DEPLOY_WEBHOOK_URL    # env var holding the webhook URL
    # bearer_env: DEPLOY_WEBHOOK_TOKEN   # sent as "Authorization: Bearer <token>"
    # hmac_env: DEPLOY_WEBHOOK_SECRET    # body signed with HMAC-SHA256, sent as
    #                                    # "X-Stevedore-Signature: sha256=<hex>"
    # required: false                    # a failed delivery warns instead of failing
    # payload_template: '{"service": {{ json .Image }}, "version": {{ json .Version }}}'

policy:
  require: [scan, test, sign, sbom]  # stages a real release may not go without
```

`policy.require` holds real releases to their gates. A required stage that is
skipped with its flag (`--skip-scan`, `--skip-test`, `--skip-sign`,
`--skip-sbom`) or disabled in the config (`scan.enabled: false`, …) refuses a
real `release` or `merge` before anything is built. `--snapshot`, `--no-push`
and `--split` legs are not real releases and are exempt. Whether or not a
policy is set, a real release that ran without any of these stages is marked
**degraded** in the release summary (`"degraded": ["scan", …]` in the JSON, and a
banner over the job-summary table).

Before anything is pushed, a run that will create a GitHub release checks that
`gh` can authenticate — `GH_TOKEN` (or `GITHUB_TOKEN`) is set, or `gh auth status`
succeeds — so a missing token fails the run up front rather than after the images
are out. In GitHub Actions, pass `GH_TOKEN: ${{ github.token }}` to the release
step.

Publishing (`release.github` + `announce`) runs only on real releases, never on
`--snapshot`, and can be turned off per-run with `--skip-publish`. It also needs
something to publish: when change detection skips every image, the run pushes
nothing and creates no GitHub release and no announcement.

The GitHub release is named after the **release version**: the tag on HEAD when
it names that version (the `git` strategy always does), otherwise `v<version>` —
for example a `registry` strategy computing `1.5.0` creates release `v1.5.0`, tagged
at the commit that was built if the tag does not exist yet. A tag that is merely
reachable from HEAD is never reused: it names a release already cut from an
earlier commit.

## Post-push notifications

Where `announce` posts one human-readable message at the end of a release,
`notify.webhook` POSTs one structured JSON payload **per pushed image**, so a CD
system can react to the newly published digest (GitOps sync, rollout trigger)
without bespoke CI glue:

```json
{
  "project": "myapp",
  "snapshot": false,
  "image": "api",
  "version": "1.4.0",
  "digest": "sha256:9f8e…",
  "repositories": ["ghcr.io/acme/api"],
  "refs": ["ghcr.io/acme/api:1.4.0", "ghcr.io/acme/api:latest"]
}
```

Notifications fire only after the image passed every gate (scan, smoke test)
and its release stages completed. Unlike `announce`, they also fire on
`--snapshot` pushes — the payload carries the `snapshot` flag so the consumer
can route dev vs. prod — and they respect `--skip-publish`. `stevedore build
--push` never notifies: an inner-loop push is not a deploy. On a split release
they fire from the `merge` run, once the manifest lists are gated and tagged. The URL
and credentials come from environment variables; a missing variable or a
non-2xx response fails the release rather than silently skipping the trigger.

A webhook that is a convenience rather than the deploy trigger can be made
best-effort with `required: false`: a transport error or non-2xx response is
then printed as a warning, the remaining images are still notified, and the
release succeeds (its log says `notified webhook of 1 of 2 pushed image(s)`).
Configuration mistakes — an unset variable, a non-https URL, a template that
does not render — fail the release either way, because retrying will not fix
them. The default is `required: true`.

`payload_template` replaces the JSON body above with your own, so a receiver
that expects a different shape needs no glue step. It is a Go template over the
same fields (`.Project`, `.Snapshot`, `.Image`, `.Version`, `.Digest`,
`.Repositories`, `.Refs`), rendered once per image, and it must produce valid
JSON: use the `json` helper to encode a value rather than quoting it by hand.
Every payload is rendered before the first request, so a template error never
leaves the receiver with half the notifications. `hmac_env` signs the rendered
body.

```yaml
notify:
  webhook:
    enabled: true
    url_env: DEPLOY_WEBHOOK_URL
    payload_template: |
      {"service": {{ json .Image }}, "version": {{ json .Version }}, "digest": {{ json .Digest }}}
```

Webhook URLs — `notify.webhook` and both `announce` targets — must be `https://`.
A plain `http://` URL would send the URL, and for `notify` the bearer token, in
cleartext, so it fails the release. The one exception is a loopback host
(`localhost`, `127.0.0.0/8`, `[::1]`), so a local receiver can be tested
without a certificate; other hostnames are refused even if they happen to
resolve to loopback.

## Template context

Tags, labels, and build args are rendered with Go's `text/template`. Referencing an
undefined field is an error (no silent empty strings). Available fields:

| Field | Example |
|-------|---------|
| `.ProjectName` | `myapp` |
| `.Version` | `1.4.0` |
| `.Major` / `.Minor` / `.Patch` | `1` / `4` / `0` — the parts of `.Version`; an error when the version is not semver |
| `.Prerelease` | `rc.1` for `1.5.0-rc.1`; empty for a release. A snapshot version is read as its base, so `.Major`…`.Prerelease` of `1.4.0-SNAPSHOT-9f8e7d6` are those of `1.4.0` |
| `.IsPrerelease` | `true` when `.Version` is a semver prerelease. `false` for a snapshot (`1.5.0-SNAPSHOT-9f8e7d6` is a build after a release, not a candidate; see `.IsSnapshot`) and, never an error, for a non-semver version |
| `.Tag` | `v1.4.0` — the tag on HEAD; empty on an untagged commit |
| `.LatestTag` | `v1.4.0` — the most recent tag reachable from HEAD (`.Tag` when HEAD is tagged) |
| `.Commit` | full SHA |
| `.ShortCommit` | `9f8e7d6` |
| `.Branch` | `main` |
| `.Date` | RFC 3339 build time (UTC) — the wall clock, so it differs on every build |
| `.Timestamp` | Unix seconds of `.Date` |
| `.CommitDate` | RFC 3339 committer time of HEAD (UTC) — the same on every build of the commit; empty with no commits |
| `.CommitTimestamp` | Unix seconds of `.CommitDate` (`0` with no commits) |
| `.SourceURL` | `https://github.com/acme/myapp` — the origin remote as https, credentials dropped; empty without one |
| `.IsSnapshot` | `true` in a snapshot build |
| `.IsDefault` | `true` when HEAD is on the default branch — including a tag checkout cut from it |
| `.Detached` | `true` when HEAD points at a commit rather than a branch (any tag-triggered CI release) |
| `.Env.NAME` | environment variable `NAME` |

Helper functions: `lower`, `upper`, `trim`, `replace`, `trimPrefix`, `trimSuffix`, `json` (encodes a value as JSON).

## Default labels

Every image gets these OCI labels unless it sets the key itself in `labels:` (or the
config sets `default_labels: false`):

| Label | Value |
|-------|-------|
| `org.opencontainers.image.source` | the `origin` remote as https (`git@github.com:acme/app.git` → `https://github.com/acme/app`, any credentials dropped) |
| `org.opencontainers.image.revision` | the full commit SHA — also what the already-released check compares against |
| `org.opencontainers.image.version` | the image's version |
| `org.opencontainers.image.created` | the commit date (`.CommitDate`), not the build time, so a rebuild keeps its digest |

A default with nothing to say — no `origin` remote, no commits yet — is left out rather
than set empty. Labels live in the image config; for metadata a registry shows without
pulling the config, add the same keys under `annotations:`.

## Reproducible builds

Rebuilding a commit should produce the same image digest. Two things work against
that, and stevedore handles the one it controls:

- **Timestamps.** By default every build gets `SOURCE_DATE_EPOCH` set to HEAD's commit
  time, both as a `--build-arg` (for a Dockerfile that reads it) and in buildx's
  environment, where BuildKit uses it for the image config's `created` time and its
  history. A `SOURCE_DATE_EPOCH` already in the environment, or one set in an image's
  `build_args`, wins over the commit time. Set `source_date_epoch: false` to pass none.
- **Labels.** A label rendered from `.Date` bakes the wall clock into the image config
  and changes the digest on every build. Use `.CommitDate` for
  `org.opencontainers.image.created` instead.

Layer file timestamps are a separate matter: BuildKit rewrites them to
`SOURCE_DATE_EPOCH` only with the image exporter's `rewrite-timestamp=true` option
(BuildKit 0.13+), and what the Dockerfile itself does (package-manager caches,
generated files) is up to the Dockerfile.

## Changelog

When enabled, stevedore reads commits since the previous tag and groups them by
[Conventional Commit](https://www.conventionalcommits.org) type into **Features**,
**Bug Fixes**, **Performance**, **Refactors**, **Documentation**, and **Other**. A
`!` (e.g. `feat!:`) marks a breaking change. Non-conforming subjects land under
"Other". The result is written to `<dist>/CHANGELOG.md`.
