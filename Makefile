# ==============================================================================
# GOPHER CRAWLER — MAKEFILE
# ==============================================================================

.PHONY: all build test test-race lint clean docker-up docker-down docker-scale dev setup help

SHELL := /bin/bash

help:
	@echo "GOPHER CRAWLER — Management Commands"
	@echo "  make setup        - Setup local environment and copy .env.example"
	@echo "  make build        - Compile Go binaries and sync frontend web -> public"
	@echo "  make test         - Run Go tests, JS lint, and serverless tests"
	@echo "  make test-race    - Run full Go suite with race detector"
	@echo "  make lint         - Run go vet, gofmt check, and JS syntax lint"
	@echo "  make dev          - Start Postgres & Redis in Docker and run coordinator"
	@echo "  make docker-up    - Build and launch entire cluster with Docker Compose"
	@echo "  make docker-down  - Stop all cluster containers"
	@echo "  make docker-scale - Scale distributed workers to 3 nodes"
	@echo "  make clean        - Remove compiled binaries"

setup:
	@cp -n .env.example .env || true
	@go mod download
	@npm install

build:
	@mkdir -p bin
	go build -o bin/master ./cmd/master
	go build -o bin/worker ./cmd/worker
	npm run build

test:
	go test -p 1 -v ./...
	npm test

test-race:
	CGO_ENABLED=1 go test -p 1 -v -race ./...

lint:
	go vet ./...
	npm run lint

dev:
	docker compose up -d redis postgres
	go run ./cmd/master

docker-up:
	docker compose up -d --build

docker-down:
	docker compose down

docker-scale:
	docker compose up -d --scale worker=3

clean:
	rm -rf bin/
