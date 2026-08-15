# syntax=docker/dockerfile:1
# Build on the native arch and cross-compile for the target platform, so
# multi-arch builds never run the Go toolchain under QEMU emulation.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/stevedore .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/stevedore /usr/bin/stevedore
ENTRYPOINT ["/usr/bin/stevedore"]
