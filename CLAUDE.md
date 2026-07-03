# CLAUDE.md

@AGENTS.md

<!--
  AGENTS.md (imported above) is the cross-tool single source of truth, read by
  every AI coding assistant. Put all durable project context THERE, not here.
  This file holds only Claude Code-specific extras that other tools don't read.
-->

## Claude Code-specific notes

- **Building/testing stevedore needs no external tools** — `make build` /
  `make test` are pure Go. The `docker`/`cosign`/`syft`/`grype`/`crane`/`aws`/`gh`
  binaries are only needed to *run an actual release*; unit tests that touch them
  skip gracefully when the tool (or the docker socket) is absent.

- **Slash commands** (`.claude/commands/`):
  - `/check` — run the pre-PR gate (`make check` = lint + race tests) and report
    failures concisely. Optionally scope it: `/check lint` or `/check test`.

- **Demoing a release locally** is safe without a registry: `stevedore release
  --snapshot --dry-run` prints every command it would run, and `stevedore build`
  does a real local `--load` build. For change-detection demos, `--changed-since
  <ref>` reads real git history without needing a push.

- **Don't add the co-author trailer to commits** in this repo.
