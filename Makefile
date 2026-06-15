.PHONY: help infra-up infra-down server worker build tidy web-install web-dev fmt vet test

help:
	@echo "docvault dev targets:"
	@echo "  infra-up      start Postgres + MinIO via docker compose"
	@echo "  infra-down    stop infra (keep volumes)"
	@echo "  server        run the HTTP API server (go run ./cmd/server)"
	@echo "  worker        run the sync worker (go run ./cmd/worker)"
	@echo "  build         compile server + worker into ./bin"
	@echo "  web-install   install frontend deps (pnpm)"
	@echo "  web-dev       run the Vite dev server"
	@echo "  fmt/vet/test  standard Go checks"

infra-up:
	docker compose up -d

infra-down:
	docker compose down

server:
	go run ./cmd/server

worker:
	go run ./cmd/worker

build:
	mkdir -p bin
	go build -o bin/server ./cmd/server
	go build -o bin/worker ./cmd/worker

tidy:
	go mod tidy

fmt:
	go fmt ./...

vet:
	go vet ./...

test:
	go test ./...

web-install:
	cd web && pnpm install

web-dev:
	cd web && pnpm dev
