# Development workflow for the SpaceCraft Discord bot.
#
# The dev stack (docker-compose.dev.yml) runs the bot under air with the source
# bind-mounted and the Go toolchain available inside the `bot` container. The
# fmt/mock targets exec into that already-running container and pull the pinned
# tools via `go run`, so no host Go toolchain (or extra image layers) is needed.

COMPOSE_DEV := docker compose -f docker-compose.dev.yml
DEV_SERVICE := bot

# Pinned tool versions. Keep golangci-lint in sync with .github/workflows/ci.yml
# and mockery in sync with the header of the generated mocks / .mockery.yaml.
GOLANGCI_VERSION := v2.12.2
MOCKERY_VERSION  := v2.53.0

.PHONY: dev.up dev.down dev.stop dev.fmt dev.mock

## dev.up: build and start the dev stack (detached)
dev.up:
	$(COMPOSE_DEV) up -d --build

## dev.down: stop and remove the dev stack, including volumes (wipes the dev DB)
dev.down:
	$(COMPOSE_DEV) down -v

## dev.stop: stop the dev stack, keeping containers and volumes
dev.stop:
	$(COMPOSE_DEV) stop

## dev.fmt: format Go files (gofmt + gci import order) via golangci-lint in the running container
dev.fmt:
	$(COMPOSE_DEV) exec -T $(DEV_SERVICE) \
		go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION) fmt ./...

## dev.mock: regenerate mocks with mockery (reads .mockery.yaml) in the running container
dev.mock:
	$(COMPOSE_DEV) exec -T $(DEV_SERVICE) \
		go run github.com/vektra/mockery/v2@$(MOCKERY_VERSION)