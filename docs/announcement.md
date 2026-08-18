# Announcement playbook

The launch plan for v1.0.0. Written before posting anywhere, because the
expensive mistakes in this category are all made in the first hour of the first
thread and cannot be edited out afterwards.

## The one idea

> **Release Docker images the way goreleaser releases binaries.**

That sentence is the whole pitch and it already works: it borrows a mental model
the audience has, so a tired reader evaluates it in two seconds without reading
a feature list. Do not improve it. Do not add a second clause to the title.

Everything else — signing, SBOMs, monorepo change detection, split multi-arch —
is body copy. A title that lists features reads as unfocused and dies.

## Rules

**Concede first, in the post itself.** The first comment in every thread will be
*"isn't this just ko / bake / build-push-action / goreleaser's `dockers:`
block?"* Answering it in the thread reads as defensive; answering it in the post
reads as having done the work. Link `docs/comparison.md`, and say the concession
out loud: **if GoReleaser's `dockers:` block already covers you, keep using it.**
Telling people not to switch is what makes the rest of the post credible.

**Never claim it makes builds faster.** stevedore does not build anything —
`docker buildx` does. The only speed claim available is the split multi-arch
one, and it belongs to native runners rather than to stevedore. Phrase it as
*"removes QEMU from the hot path"* and immediately add the caveat the docs
already carry: if your image is pure Go, cross-compile in the Dockerfile and you
do not need any of it. Volunteering the case where your feature is unnecessary
is worth more than the feature.

**Do not use "secure" as an adjective.** This is a supply-chain audience and
they have been marketed at. Say the mechanism instead: *the scan and smoke-test
gates run before the signature, so a failing image is never signed.* A mechanism
can be checked; an adjective invites someone to disprove it.

**State the counterparty risk before anyone asks.** MIT, no CLA, no account, no
telemetry, no hosted service anywhere in the path, single maintainer. Say
"single maintainer" yourself — someone else will, and it lands very differently
when it looks like a disclosure rather than a discovery. Then give the actual
mitigation, which is real: the config is a YAML file describing docker commands,
so the exit cost is reading it and writing the shell you would have written
anyway. Nothing is stored, nothing is proprietary, nothing is locked in.

**Lead the ~4.5 MB binary as a *dependency* story, not a size flex.** The
interesting part is not that it is small, it is *why*: it orchestrates cosign,
syft, grype and crane rather than vendoring them, which means you pin those
tools' versions yourself and a CVE in one of them is patched by upgrading that
tool, not by waiting for a stevedore release. That framing turns the obvious
objection — "so I have to install five other things" — into the answer.

**Answer the "another YAML file" objection with `--dry-run`.** It is the honest
response: yes, another config file, and here is the thing that makes it not a
black box — `stevedore release --snapshot --dry-run` prints every command it
would run, without running one. Put that in the post. It is the single most
disarming line available.

**Be in the thread.** Block the first six hours. Answer every comment, including
the dismissive ones, briefly and without defensiveness. A maintainer answering
in the thread is most of what separates a post that lands from one that does
not, and it matters more than anything in the post itself.

**Never argue with someone who says they will keep their shell script.** They
are right. Say so.

## Honest state, as of v1.0.0

Keep this section current; it is what stops the post from overclaiming.

| Claim | State |
|-------|-------|
| Dogfooded | Yes — stevedore publishes its own image with itself on every release. |
| Used in production | Yes, in one organisation, across many services, since v0.0.7. **Get explicit permission before naming that organisation**, and if permission is not given, say "a private monorepo of ~N services" rather than implying more. |
| External adopters | None yet. Do not imply otherwise, do not say "used by teams". |
| Multi-arch split | Shipped and used for real releases. |
| Windows | Not supported. `release` shells out to docker; nobody has tried it. Say so if asked. |
| Registries tested | ghcr.io and ECR for real. Docker Hub, GAR, Quay should work and have not been verified — say "should work, untested" rather than listing them. |
| Non-Docker builders | None. No ko, no buildah, no kaniko backend. |
| Version | v1.0.0, with a published stability contract (`docs/stability.md`). The contract is the answer to "0.x means unfinished". |

## What we will not claim

- That it is faster than anything.
- That it is "production-ready" as a phrase — show the stability contract instead.
- Any number that is not reproducible from the repo.
- That it replaces your CI. It runs *in* your CI.
- That it is a security product. It orchestrates security tools.
- Adoption we do not have.

## Pre-flight checklist

Nothing gets posted until every one of these is true. A broken copy-paste path
in the first hour costs the whole launch.

- [ ] `brew install blairham/tap/stevedore` works from a clean machine
- [ ] `go install github.com/blairham/stevedore@latest` works
- [ ] `docker run --rm ghcr.io/blairham/stevedore --version` works — i.e. the
      `:latest` tag actually exists (it did not, for ten releases; see #20)
- [ ] `uses: blairham/stevedore@v1` resolves (see #21)
- [ ] Every command in the README quick start, run in order, in a scratch repo
- [ ] `stevedore doctor` on a machine missing every optional tool gives useful
      install hints rather than a stack trace
- [ ] The Action is listed on the GitHub Marketplace
- [ ] Issue templates render, and Discussions is enabled and has a first post
- [ ] The repo has topics, a description and a social preview image

## Where, and in what order

Sequenced so the feedback from each round improves the next. Do not do them all
on one day — a thread you cannot sit in is a thread wasted.

1. **A writeup first**, on your own site or dev.to. The post is the artifact
   everything else links to, and writing it flushes out the claims that cannot
   survive a paragraph.
2. **r/devops** — the core audience, and forgiving of a first post. Read the
   self-promotion rules first. Lead with the problem, not the tool.
3. **Lobsters** (`devops`, `containers`) — small, high signal, and the comments
   are usually worth acting on before a bigger audience sees it.
4. **Show HN** — Tuesday to Thursday, roughly 9–11am US Eastern. One idea in the
   title, no adjectives, no exclamation mark. `Show HN: Stevedore – release
   Docker images the way goreleaser releases binaries`. Be at the keyboard.
5. **Newsletter submissions** — Golang Weekly, DevOps'ish, Last Week in AWS if
   the ECR versioning angle fits. These are slow and cost nothing.
6. **Awesome-list PRs** — awesome-go, awesome-docker, awesome-devops. Low
   traffic, but they are how people find things a year later.
7. **Sigstore and CNCF Slack** — only if there is a genuine question or
   contribution to make. Do not drop a link.

## The follow-through nobody does

The launch is not the point; the week after is.

- Answer every issue within a day for the first month, even if the answer is
  "not planning to do that". Silence in month one is what tells people a project
  is a weekend experiment.
- Ship a patch release in the first two weeks. It does not matter how small. A
  repo whose last commit is the launch commit reads as abandoned by week three.
- Write down every "isn't this just X" and every confusion from the threads.
  Those are the docs backlog, and they are more reliable than any guess about
  what is unclear.
