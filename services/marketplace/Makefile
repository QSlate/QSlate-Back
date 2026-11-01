.PHONY: build run test vet fmt test-integration docker-build
export GOWORK := off

build:
	go build -mod=readonly -o bin/marketplace ./cmd/service

run:
	go run -mod=readonly ./cmd/service

test:
	go test -mod=readonly -race ./...

vet:
	go vet -mod=readonly ./...

fmt:
	gofmt -w .

test-integration:
	go test -mod=readonly -race -tags integration -count=1 ./...

docker-build:
	docker build -t qslate-marketplace:local .
