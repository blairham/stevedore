---
description: Run make check (vet + race tests + build) and report failures concisely.
allowed-tools: Bash(make:*), Bash(go build:*), Bash(go test:*), Bash(go vet:*)
---

Run the local gate for stevedore and report the outcome.

Run `make check` — `go vet ./...`, `go test -race ./...` and `go build ./...`.
There is no lint step: golangci-lint runs as the pre-commit hook and in CI, and
is never run by hand.

If a specific target is the focus, the caller may pass `$ARGUMENTS` (e.g.
`vet` or `test`): if `$ARGUMENTS` is non-empty, run `make $ARGUMENTS` instead.

Then:
- If everything passes, say so in one line.
- If `vet` failed, report each finding with its `file:line` — do not fix it
  yourself unless asked.
- If `test` failed, report each failing test with its output.

Do not commit, push, or open a PR.
