#!/usr/bin/env bash
# ==============================================================================
# GOPHER CRAWLER — VERIFICATION SUITE (BASH)
# ==============================================================================
set -e

echo ">>> Running Gopher Crawler Full Verification Suite..."

echo -e "\n[1/5] Syncing web -> public and running JS lint..."
npm run build
npm run lint

echo -e "\n[2/5] Running Node Serverless Tests..."
npm test

echo -e "\n[3/5] Running Go Vet Static Analysis..."
go vet ./...

echo -e "\n[4/5] Running Go Tests with Race Detector..."
go test -p 1 -v -race ./...

if command -v docker >/dev/null 2>&1; then
    echo -e "\n[5/5] Validating Docker Compose Configuration..."
    docker compose config --quiet
fi

echo -e "\n[✓] All Verification Suites Passed!"
