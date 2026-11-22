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
	docker compose logs -f gateway auth workspace widgets strategy strategy-analyzer marketplace lab market-data market-data-worker market-data-parser

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

# These opt-in Docker tests execute only fixed repository-owned Python fixtures.
.PHONY: simulator-build simulator-test test-engine up-lab-local
SIMULATOR_IMAGE ?= qslate-simulator:fb63ed3-lab-v1
simulator-build:
	docker build -t $(SIMULATOR_IMAGE) ./simulator

simulator-test: simulator-build
	docker run --rm --network none --read-only --cap-drop ALL --security-opt no-new-privileges --memory 512m --pids-limit 64 --tmpfs /work:rw,noexec,nosuid,nodev,size=64m,uid=10001,gid=10001,mode=0700 --entrypoint python $(SIMULATOR_IMAGE) -B -m unittest discover -s /app/simulator/tests -p 'test_*.py'

test-engine: simulator-build
	cd services/lab && SIMULATOR_TEST_IMAGE=$(SIMULATOR_IMAGE) GOWORK=off go test -race -tags engine -count=1 ./internal/execution

up-lab-local: init simulator-build
	LAB_CONFIG_FILE=/app/config/lab.local.json LAB_ALLOW_LOCAL_EXECUTION=true docker compose --profile lab-execution up --build -d --wait --wait-timeout 120

.PHONY: market-parser-test
market-parser-test:
	docker build --target test -t qslate-market-parser-tests:local ./services/market-data/parser
