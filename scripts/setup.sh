#!/usr/bin/env bash
# ==============================================================================
# GOPHER CRAWLER — SETUP SCRIPT (BASH)
# ==============================================================================
set -e

echo ">>> Initializing Gopher Crawler Environment..."

# 1. Check for Go
if command -v go >/dev/null 2>&1; then
    echo "[✓] Go Detected: $(go version)"
else
    echo "[✕] Go is not installed or not on PATH."
fi

# 2. Check for Node
if command -v node >/dev/null 2>&1; then
    echo "[✓] Node.js Detected: $(node --version)"
else
    echo "[✕] Node.js is not installed or not on PATH."
fi

# 3. Check for Docker
if command -v docker >/dev/null 2>&1; then
    echo "[✓] Docker is available"
else
    echo "[!] Docker not detected."
fi

# 4. Copy .env if not exists
if [ ! -f .env ]; then
    cp .env.example .env
    echo "[✓] Created .env configuration from .env.example"
else
    echo "[i] .env file already exists"
fi

# 5. Download Go dependencies
echo ">>> Downloading Go modules..."
go mod download
echo "[✓] Go modules ready"

# 6. Install Node dependencies
if [ -f package.json ]; then
    echo ">>> Installing Node packages..."
    npm install --silent
    echo "[✓] Node packages ready"
fi

echo -e "\n>>> Setup Complete! Run './scripts/dev.sh' or 'docker compose up' to start."
