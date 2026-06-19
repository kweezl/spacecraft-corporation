# syntax=docker/dockerfile:1
ARG GO_VERSION=1.26

# --- build -------------------------------------------------------------------
FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=0.0.0-unspecified
RUN CGO_ENABLED=0 go build \
    -ldflags "-X github.com/kweezl/spacecraft-corporation/internal/appconfig.version=${VERSION}" \
    -o /bot ./cmd/bot

# --- prod (minimal runtime) --------------------------------------------------
FROM alpine:3.24 AS prod
# ca-certificates for outbound TLS (Discord, Postgres); a dedicated non-root user.
RUN apk add --no-cache ca-certificates \
 && adduser -D -H -u 65532 nonroot
COPY --from=build /bot /bot
USER nonroot:nonroot
EXPOSE 8080
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
RUN go install github.com/air-verse/air@latest \
 && go install github.com/go-delve/delve/cmd/dlv@latest
COPY go.mod go.sum ./
RUN go mod download \
 && chown -R dev:dev /go /src
USER dev
EXPOSE 2345 8080
ENTRYPOINT ["air", "-c", ".air.toml"]
