# Development workflow for the SpaceCraft Discord bot.
#
# The dev stack (docker-compose.dev.yml) runs the bot under air with the source
# bind-mounted and the Go toolchain available inside the `bot` container. The
# fmt/mock targets exec into that already-running container and pull the pinned
# tools via `go run`, so no host Go toolchain (or extra image layers) is needed.

COMPOSE       := docker compose
COMPOSE_DEV   := docker compose -f docker-compose.dev.yml
DEV_SERVICE   := bot
# Full-stack profile for `dev.up`/`dev.stop`. `app` alone is an invalid project
# (bot's deps aren't in it); `all` is the complete one-shot stack.
DEV_PROFILE   := all
DEV_UP_DETACH := "-d"

# Load .env (if present) and export its vars to recipe shells, so values like
# POSTGRES_HOST_PORT / DEV_CMD reach docker compose. The leading '-' makes a
# missing .env non-fatal. Compose also reads .env on its own; this additionally
# exposes the same values to make itself.
-include .env
export

# Pinned tool versions. Keep golangci-lint in sync with .github/workflows/ci.yml,
# mockery with the header of the generated mocks / .mockery.yaml, and goose with
# the github.com/pressly/goose/v3 version in go.mod.
GOLANGCI_VERSION := v2.12.2
MOCKERY_VERSION  := v2.53.0
GOOSE_VERSION    := v3.27.1

# Where goose migrations live (embedded into the binary by the migrator module).
MIGRATIONS_DIR := internal/migrator/migrations

# Image build args. Override via env or .env (e.g. `make build APP_VERSION=1.2.3`).
# APP_VERSION is baked into the prod binary via ldflags; UID/GID make the dev
# image's user match the host so bind-mounted files aren't root-owned.
APP_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.0.0-unspecified)
UID     ?= $(shell id -u)
GID     ?= $(shell id -g)

.PHONY: *

help:
	@grep -oE "^[a-z\.-]+:" Makefile | uniq

# Both targets build the dedicated `build` service, which carries the shared
# image tag (reused by migrate + bot). Naming the service enables its `build`
# profile, so the build runs even though every service is profile-gated.

## build: build (or rebuild) the prod bot image, without starting any container
build:
	$(COMPOSE) build --build-arg APP_VERSION=$(APP_VERSION) build

## build-dev: build (or rebuild) the dev bot image, without starting any container
build-dev:
	$(COMPOSE_DEV) build \
		--build-arg UID=$(UID) --build-arg GID=$(GID) \
		--build-arg GOLANGCI_VERSION=$(GOLANGCI_VERSION) \
		--build-arg MOCKERY_VERSION=$(MOCKERY_VERSION) \
		--build-arg GOOSE_VERSION=$(GOOSE_VERSION) \
		build

## dev.up: build and start the dev stack (detached)
dev.up:
	$(COMPOSE_DEV) --profile $(DEV_PROFILE) \
       	up $(DEV_UP_DETACH) --no-log-prefix --remove-orphans

## dev.up-app: apply pending migrations, then run only the bot in the foreground
## (needs `dev.up-infra` first). The migrate one-shot runs fresh on every invocation
## (`run` always starts a new container) so a newly added migration is always
## applied; it only waits on the already-running postgres and never restarts the DB,
## and a failed migration aborts the target before the bot starts. --no-deps on the
## bot means compose doesn't pull in / attach / stop postgres, so Ctrl+C stops just
## the bot and the DB keeps running — re-run to restart the bot.
dev.up-app:
	$(COMPOSE_DEV) run --rm migrate
	$(COMPOSE_DEV) up --no-deps --no-log-prefix bot

## dev.up-infra: start the infrastructure (postgres + one-shot migrate), detached
dev.up-infra: DEV_PROFILE=infra
dev.up-infra: dev.up

## dev.down: stop and remove the dev stack, including volumes (wipes the dev DB)
dev.down:
	$(COMPOSE_DEV) --profile "*" down -v

## dev.stop: stop the dev stack, keeping containers and volumes
dev.stop:
	$(COMPOSE_DEV) --profile $(DEV_PROFILE) stop

## dev.fmt: format Go files (gofmt + gci import order) via the image-installed golangci-lint
dev.fmt:
	$(COMPOSE_DEV) exec -T $(DEV_SERVICE) golangci-lint fmt ./...

## dev.mock: regenerate mocks with the image-installed mockery (reads .mockery.yaml)
dev.mock:
	$(COMPOSE_DEV) exec -T $(DEV_SERVICE) mockery

## dev.migration: scaffold a timestamped goose SQL migration (make dev.migration name=add_foo)
dev.migration:
	@test -n "$(name)" || { echo "usage: make dev.migration name=<snake_case_name>"; exit 1; }
	$(COMPOSE_DEV) exec -T $(DEV_SERVICE) goose -dir $(MIGRATIONS_DIR) create $(name) sql