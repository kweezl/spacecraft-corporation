# syntax=docker/dockerfile:1
ARG GO_VERSION=1.26

# --- build -------------------------------------------------------------------
FROM golang:${GO_VERSION} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build \
    -ldflags "-X github.com/kweezl/spacecraft-cadet/internal/appconfig.version=${VERSION}" \
    -o /bot ./cmd/bot

# --- prod (minimal runtime) --------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot AS prod
COPY --from=build /bot /bot
USER nonroot:nonroot
ENTRYPOINT ["/bot"]

# --- dev (hot reload + debugger) --------------------------------------------
FROM golang:${GO_VERSION} AS dev
WORKDIR /src
RUN go install github.com/air-verse/air@latest \
 && go install github.com/go-delve/delve/cmd/dlv@latest
COPY go.mod go.sum ./
RUN go mod download
EXPOSE 2345
ENTRYPOINT ["air", "-c", ".air.toml"]
