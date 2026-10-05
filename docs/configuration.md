# Configuration

Every field of `.stevedore.yaml`, the template context, and the changelog.

stevedore looks for `.stevedore.yaml`, `.stevedore.yml`, `stevedore.yaml`, or
`stevedore.yml` (in that order). Unknown fields are rejected, so typos fail fast.

```yaml
version: 1

project_name: myapp
default_branch: main      # branch real releases must be cut from; floating tags publish only here
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
      - "latest"
    build_args:
      - "VERSION={{ .Version }}"
    labels:
      org.opencontainers.image.source: "https://github.com/acme/myapp"
      org.opencontainers.image.version: "{{ .Version }}"
      org.opencontainers.image.revision: "{{ .Commit }}"
      org.opencontainers.image.created: "{{ .Date }}"
    secrets:              # BuildKit --secret entries. An env-backed secret
      - id: github_token  # whose variable is unset or empty is SKIPPED (like an
        env: GITHUB_TOKEN # empty cache entry), so a config can declare a
      # - id: npmrc        # CI-minted token (e.g. a private-module fetch token)
      #   file: ./.npmrc   # that stays inert on local builds that don't export it.
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
    key: ""               # omit for keyless (OIDC) signing
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
  ignore:                 # vulnerability IDs to exclude from the gate
    - CVE-2024-0000
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
    prerelease: false

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
```

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
| `.Tag` | `v1.4.0` — the tag on HEAD; empty on an untagged commit |
| `.LatestTag` | `v1.4.0` — the most recent tag reachable from HEAD (`.Tag` when HEAD is tagged) |
| `.Commit` | full SHA |
| `.ShortCommit` | `9f8e7d6` |
| `.Branch` | `main` |
| `.Date` | RFC 3339 build time (UTC) |
| `.Timestamp` | Unix seconds |
| `.IsSnapshot` | `true` in a snapshot build |
| `.IsDefault` | `true` when HEAD is on the default branch — including a tag checkout cut from it |
| `.Detached` | `true` when HEAD points at a commit rather than a branch (any tag-triggered CI release) |
| `.Env.NAME` | environment variable `NAME` |

Helper functions: `lower`, `upper`, `trim`, `replace`, `trimPrefix`, `trimSuffix`.

## Changelog

When enabled, stevedore reads commits since the previous tag and groups them by
[Conventional Commit](https://www.conventionalcommits.org) type into **Features**,
**Bug Fixes**, **Performance**, **Refactors**, **Documentation**, and **Other**. A
`!` (e.g. `feat!:`) marks a breaking change. Non-conforming subjects land under
"Other". The result is written to `<dist>/CHANGELOG.md`.
