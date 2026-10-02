# syntax=docker/dockerfile:1
# stevedore — the CLI in a distroless image, released by stevedore itself (see
# .stevedore.yaml).
#
# Distroless, static, non-root: no package manager, no libc and no userland,
# so there is nothing for a scanner to flag and nothing to exec but the one
# binary. Base images are pinned by digest; Dependabot moves the pins.

# Build on the native arch and cross-compile for the target platform, so
# multi-arch builds never run the Go toolchain under QEMU emulation.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine@sha256:8ac98ca534ac3f51e1f420a1dd2c15e74c75cfa0f23f3ad27eb5d7236c349a0c AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY . .
# Cache mounts rather than a `go mod download` layer: the module graph includes
# the lint tooling, and downloading all of it for one binary is wasted time.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/stevedore .

# The distroless `nonroot` variant runs as uid 65532 and carries CA
# certificates, /etc/passwd and tzdata — the few files a static Go binary may
# still reach for.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
LABEL org.opencontainers.image.title="stevedore" \
      org.opencontainers.image.description="Release Docker/OCI images the way GoReleaser releases binaries" \
      org.opencontainers.image.source="https://github.com/blairham/stevedore" \
      org.opencontainers.image.licenses="Apache-2.0"
COPY --from=build /out/stevedore /usr/bin/stevedore
ENTRYPOINT ["/usr/bin/stevedore"]
