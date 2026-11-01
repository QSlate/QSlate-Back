.PHONY: build run test vet fmt test-integration docker-build
export GOWORK := off

build:
	go build -mod=readonly -o bin/auth ./cmd/auth

run:
	go run -mod=readonly ./cmd/auth

test:
	go test -mod=readonly -race ./...

vet:
	go vet -mod=readonly ./...

fmt:
	gofmt -w .

test-integration:
	go test -mod=readonly -race -tags integration -count=1 ./internal/repository

docker-build:
	docker build -t qslate-auth:local .
