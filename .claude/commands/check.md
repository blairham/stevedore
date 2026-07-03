---
description: Run the pre-PR gate (make check = lint + race tests) and report failures concisely.
allowed-tools: Bash(make:*), Bash(go build:*), Bash(go test:*), Bash(go vet:*), Bash(gofmt:*)
---

Run the pre-PR check gate for stevedore and report the outcome.

Run `make check` (which is `lint test` — see the `Makefile`). `lint` is a gofmt
check plus `go vet ./...`; `test` runs `go test -race ./...`.

If a specific target is the focus, the caller may pass `$ARGUMENTS` to scope it
(e.g. `lint` or `test`): if `$ARGUMENTS` is non-empty, run `make $ARGUMENTS`
instead of the full `check`.

Then:
- If everything passes, say so in one line.
- If `lint` failed, report the gofmt-needed files and/or each `go vet` finding
  with its `file:line` — do not fix it yourself unless asked.
- If `test` failed, report each failing test with its output.

Do not commit, push, or open a PR.
