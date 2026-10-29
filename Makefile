.PHONY: init up down logs test test-integration test-e2e test-standalone build fmt vet new-service

# Explicit module iteration: ./... at the repository root excludes child modules.
SERVICE_MODULES := $(patsubst %/go.mod,%,$(wildcard services/*/go.mod))
MODULES := . gateway $(SERVICE_MODULES) templates/microservice

init:
	GOWORK=off go run ./cmd/dev-init

up: init
	docker compose up --build -d --wait --wait-timeout 120

down:
	docker compose down

logs:
	docker compose logs -f gateway auth workspace widgets strategy strategy-analyzer

test:
	@set -e; for qslate_module in $(MODULES); do \
		(cd "$$qslate_module" && GOWORK=off go test -mod=readonly -race ./...); \
	done
	python3 -B -m unittest discover -s services/strategy/analyzer -p 'test_*.py'

test-standalone:
	GOWORK=off go test -count=1 ./tests/architecture -run TestModulesBuildOutsideRepository

test-integration:
	@set -e; for qslate_module in $(SERVICE_MODULES); do \
		(cd "$$qslate_module" && GOWORK=off go test -mod=readonly -race -tags integration -count=1 ./internal/repository); \
	done

test-e2e:
	GOWORK=off go test -tags e2e -count=1 ./tests/e2e

build:
	$(MAKE) -C gateway build
	@set -e; for qslate_module in $(SERVICE_MODULES); do $(MAKE) -C "$$qslate_module" build; done

fmt:
	gofmt -w cmd gateway services templates tests

vet:
	@set -e; for qslate_module in $(MODULES); do \
		(cd "$$qslate_module" && GOWORK=off go vet -mod=readonly ./...); \
	done

new-service:
	GOWORK=off go run ./cmd/new-service -name '$(NAME)'
