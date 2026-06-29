# Development workflow for the SpaceCraft Discord bot.
#
# The dev stack (docker-compose.dev.yml) runs the bot under air with the source
# bind-mounted and the Go toolchain available inside the `bot` container. The
# fmt/mock targets exec into that already-running container and pull the pinned
# tools via `go run`, so no host Go toolchain (or extra image layers) is needed.

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

# Dev image build args: UID/GID make the dev image's user match the host so
# bind-mounted files aren't root-owned. (The prod image is built and pushed by CI
# — .github/workflows/release.yml — not locally.)
UID     ?= $(shell id -u)
GID     ?= $(shell id -g)

.PHONY: *

help:
	@grep -oE "^[a-z\.-]+:" Makefile | uniq

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

## gamedata.gen: regenerate the gamedata layer + item icons from GAMEDATA_SOURCE
## (the public spacecraft-resources generated/ dir). Runs on the HOST (needs the
## Go toolchain + the resources clone — the dev container has neither mounted).
## Default version v1; cut a new layer with: make gamedata.gen version=v2 parent=v1
gamedata.gen:
	@test -n "$(GAMEDATA_SOURCE)" || { echo "set GAMEDATA_SOURCE to the spacecraft-resources generated/ dir (see .env.example)"; exit 1; }
	go run ./internal/gamedata/gen -version $(or $(version),v1) $(if $(parent),-parent $(parent),) -root ./internal/gamedata