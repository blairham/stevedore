# Using stevedore in CI

Running stevedore in CI: the GitHub Action, pinning, and doing it by hand.

stevedore does the same thing locally and in CI. The easiest way is the bundled
**GitHub Action**, which installs stevedore and every tool it needs (cosign, syft,
grype, and optionally crane) for you:

```yaml
name: release
on:
  push:
    tags: ["v*"]

jobs:
  release:
    runs-on: ubuntu-latest
    permissions:
      contents: write   # create the GitHub release
      packages: write   # push to ghcr.io
      id-token: write   # keyless cosign signing
    steps:
      - uses: actions/checkout@v6
        with:
          fetch-depth: 0            # tags + history for versioning/changelog
      - uses: docker/login-action@v4
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - uses: blairham/stevedore@v1  # installs stevedore + cosign/syft/grype
        with:
          command: release
```

## Pinning the action

`@v1` is a **moving major tag**, repointed at each release: you get bug fixes
without editing a workflow, and never a breaking change, since a breaking change
ships as `v2`. Pin harder if you'd rather review every bump:

| Pin | Resolves to |
|-----|-------------|
| `blairham/stevedore@v1` | newest `v1.x.y` — recommended |
| `blairham/stevedore@v1.2` | newest `v1.2.z` |
| `blairham/stevedore@v1.2.3` | exactly that release |
| `blairham/stevedore@<sha>` | exactly that commit — the strictest option |

The action's `version` input controls the **stevedore binary** it downloads,
which is a separate choice from the action code above. Left at its `latest`
default it resolves to the newest release *in the action's own major line*, so
`@v1` keeps running v1 binaries after v2 ships.

## Action inputs

| Input | Default | Description |
|-------|---------|-------------|
| `command` | `release` | Subcommand: `release`, `build`, `check`, `verify`, `doctor`. |
| `args` | `""` | Extra args, e.g. `--snapshot --only-changed`. |
| `config` | autodiscover | Path to the config file. |
| `version` | `latest` | stevedore version to install. |
| `working-directory` | `.` | Directory to run in. |
| `install-cosign` / `install-syft` / `install-grype` | `true` | Install that tool. |
| `install-crane` | `false` | Install crane (enable for `versioning.strategy: registry`). |

A pull-request check that validates the plan without publishing:

```yaml
      - uses: blairham/stevedore@v1
        with:
          command: check
```

Prefer to wire the tools up yourself? stevedore is just a binary, so
`go run github.com/blairham/stevedore@latest release` after installing the tools
works too. Either way, **`fetch-depth: 0` matters**: shallow clones hide the tags and
history stevedore needs to derive the version and build the changelog.
