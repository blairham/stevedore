# Native multi-arch, one runner per platform

A single-runner multi-arch build emulates every non-native platform under QEMU,
which is often 5–10× slower for compile-heavy stages. This splits the build so
each platform runs on its own native runner, then merges.

The workflow is [`release.yml`](release.yml) — copy it to
`.github/workflows/`.

```
plan --split-platforms  →  release --split linux/amd64   (ubuntu-24.04)
                        →  release --split linux/arm64   (ubuntu-24.04-arm)
                        →  merge
```

**The config is unchanged.** `platforms:` lists both arches exactly as it would
for a single-runner build; splitting is a workflow decision. The same file
builds single-runner on your laptop and split in CI.

## What each leg does

A leg builds **one** platform natively and pushes it **untagged, by digest**.
No tags, no signing, no SBOM, no publish — it records the digest under
`dist/digests/<image-id>/<platform>` and stops.

`merge` assembles the digests into one tagged manifest list per image, then runs
the entire release tail once against it: scan → smoke test → sign → SBOM →
changelog → publish. So nothing is signed or published until every architecture
exists, and `merge` refuses to publish while any configured platform has no
digest — a failed leg cannot ship a partial manifest list.

## Read this before you adopt it

**If your image is pure Go (`CGO_ENABLED=0`), you probably do not need this.**
Cross-compiling inside the Dockerfile removes QEMU from the hot path with zero
workflow changes and no fan-out:

```dockerfile
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /out/app .
```

Split builds earn their keep when build stages must genuinely *execute* on the
target architecture: CGO, native compilers, or test suites inside `RUN` steps.

`ubuntu-24.04-arm` is free for public repositories. Private repos need either a
paid arm64 runner or a self-hosted one.
