.PHONY: run stop test lint build tidy

## run: build and start the full stack (postgres, redis, kafka, app, prometheus, grafana)
run:
	docker compose up --build

## stop: tear down all containers and remove anonymous volumes
stop:
	docker compose down -v

## test: run all tests with the race detector enabled
test:
	go test -race ./...

## lint: run golangci-lint (install from https://golangci-lint.run/usage/install/)
lint:
	golangci-lint run ./...

## build: compile a local binary to bin/server
build:
	mkdir -p bin
	go build -ldflags="-s -w" -o bin/server ./cmd/server

## tidy: remove unused dependencies and tidy go.sum
tidy:
	go mod tidy
