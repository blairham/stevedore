# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/stevedore .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/stevedore /usr/bin/stevedore
ENTRYPOINT ["/usr/bin/stevedore"]
