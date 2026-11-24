#!/usr/bin/env bash
# ==============================================================================
# GOPHER CRAWLER — DEV RESET SCRIPT (BASH)
# ==============================================================================
set -e

echo ">>> Resetting Local Development Environment..."

if command -v docker >/dev/null 2>&1; then
    echo ">>> Stopping Docker containers and removing volumes..."
    docker compose down -v
    echo "[✓] Docker containers and local storage volumes removed"
fi

rm -rf bin/
echo "[✓] Removed bin/ directory"

echo -e "\n>>> Local environment clean. Re-launch with './scripts/dev.sh'"
