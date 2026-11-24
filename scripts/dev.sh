#!/usr/bin/env bash
# ==============================================================================
# GOPHER CRAWLER — DEV LAUNCHER (BASH)
# ==============================================================================
set -e

echo ">>> Launching Gopher Crawler Development Environment..."

if command -v docker >/dev/null 2>&1; then
    echo ">>> Starting Redis & PostgreSQL containers..."
    docker compose up -d redis postgres
fi

echo ">>> Starting Go Coordinator Master Node (Port 8080)..."
go run cmd/master/main.go
