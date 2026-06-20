# syntax=docker/dockerfile:1
ARG GO_VERSION=1.26

# --- build -------------------------------------------------------------------
# Pin the build stage to the BUILDPLATFORM (the native runner arch) and
# cross-compile to the requested TARGET* — the build is CGO-free, so Go
# cross-compiles natively instead of running the compiler under slow emulation.
# BUILDPLATFORM/TARGETOS/TARGETARCH are auto-provided by Buildx.
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG APP_VERSION=0.0.0-unspecified
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -ldflags "-X github.com/kweezl/spacecraft-corporation/internal/appconfig.version=${APP_VERSION}" \
    -o /bot ./cmd/bot

# --- prod (minimal runtime) --------------------------------------------------
FROM alpine:3.24 AS prod
# ca-certificates for outbound TLS (Discord, Postgres); a dedicated non-root user.
RUN apk add --no-cache ca-certificates \
 && adduser -D -H -u 65532 nonroot
COPY --from=build /bot /bot
USER nonroot:nonroot
EXPOSE 9464
ENTRYPOINT ["/bot"]

# --- dev (hot reload + debugger) --------------------------------------------
FROM golang:${GO_VERSION}-alpine AS dev
# Create a user matching the host UID/GID so files written into the bind-mounted
# source tree (go.mod updates, generated code) are owned by the host developer,
# not root. Override via build args: UID=$(id -u) GID=$(id -g).
ARG UID=1000
ARG GID=1000
RUN addgroup -g ${GID} dev \
 && adduser -D -u ${UID} -G dev dev
WORKDIR /src
# Keep the Go build cache in a stable dev-owned dir, deliberately OUTSIDE the
# bind-mounted /src (and air's tmp_dir under it) so air's cleanup never wipes it
# and incremental rebuilds stay fast.
ENV GOCACHE=/home/dev/.cache/go-build
# Dev tools are baked into the image (on PATH at /go/bin) so dev.fmt / dev.mock
# exec a pinned binary instead of `go run`-ing one on every call. Versions come
# in as build args (defaults kept in sync with the Makefile + .github/ci.yml).
ARG GOLANGCI_VERSION=v2.12.2
ARG MOCKERY_VERSION=v2.53.0
ARG GOOSE_VERSION=v3.27.1
RUN go install github.com/air-verse/air@latest \
 && go install github.com/go-delve/delve/cmd/dlv@latest \
 && go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_VERSION} \
 && go install github.com/vektra/mockery/v2@${MOCKERY_VERSION} \
 && go install github.com/pressly/goose/v3/cmd/goose@${GOOSE_VERSION}
COPY go.mod go.sum ./
RUN go mod download \
 && mkdir -p ${GOCACHE} \
 && chown -R dev:dev /go /src /home/dev
USER dev
EXPOSE 2345 9464
ENTRYPOINT ["air", "-c", ".air.toml"]
