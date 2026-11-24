#!/usr/bin/env bash
# ==============================================================================
# GOPHER CRAWLER — TEST RUNNER (BASH)
# ==============================================================================
set -e

echo ">>> Running Gopher Crawler Verification Suite..."

echo -e "\n[1/4] Running Go Unit Tests..."
go test -v ./...

echo -e "\n[2/4] Running Go Vet Code Quality Check..."
go vet ./...

echo -e "\n[3/4] Running Serverless Engine Tests..."
npm test

if command -v docker >/dev/null 2>&1; then
    echo -e "\n[4/4] Validating Docker Compose Orchestration..."
    docker compose config --quiet
    echo "[✓] Docker Compose configuration valid"
fi

echo -e "\n[✓] All Test Suites Passed Cleanly!"
