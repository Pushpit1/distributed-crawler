# ==============================================================================
# GOPHER CRAWLER — MAKEFILE
# ==============================================================================

.PHONY: all build test lint clean docker-up docker-down docker-scale dev setup help

SHELL := /bin/bash

help:
	@echo "GOPHER CRAWLER — Management Commands"
	@echo "  make setup        - Setup local environment and copy .env.example"
	@echo "  make build        - Compile Go binaries (master & worker)"
	@echo "  make test         - Run Go and Node test suites"
	@echo "  make lint         - Run go vet and static checks"
	@echo "  make dev          - Start dependencies in Docker and run master"
	@echo "  make docker-up    - Build and launch entire cluster with Docker Compose"
	@echo "  make docker-down  - Stop all running containers"
	@echo "  make docker-scale - Scale workers to 4 nodes"
	@echo "  make clean        - Remove build artifacts and temporary binaries"

setup:
	@cp -n .env.example .env || true
	@go mod download
	@npm install

build:
	@mkdir -p bin
	go build -o bin/master cmd/master/main.go
	go build -o bin/worker cmd/worker/main.go

test:
	go test -v ./...
	npm test

lint:
	go vet ./...

dev:
	docker compose up -d redis postgres
	go run cmd/master/main.go

docker-up:
	docker compose up -d --build

docker-down:
	docker compose down

docker-scale:
	docker compose up -d --scale worker=4

clean:
	rm -rf bin/
