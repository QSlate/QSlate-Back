.PHONY: init up down logs test test-integration test-e2e build fmt vet new-service

init:
	go run ./cmd/dev-init

up: init
	docker compose up --build -d --wait --wait-timeout 120

down:
	docker compose down

logs:
	docker compose logs -f gateway auth workspace

test:
	go test -race ./...

test-integration:
	go test -race -tags integration -count=1 ./services/auth/internal/repository ./services/workspace/internal/repository

test-e2e:
	go test -tags e2e -count=1 ./tests/e2e

build:
	go build -o bin/gateway ./gateway/cmd/gateway
	go build -o bin/auth ./services/auth/cmd/auth
	go build -o bin/workspace ./services/workspace/cmd/service

fmt:
	gofmt -w cmd internal gateway services templates tests

vet:
	go vet ./...

new-service:
	go run ./cmd/new-service -name '$(NAME)'
