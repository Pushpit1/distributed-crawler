# ==============================================================================
# GOPHER CRAWLER — SETUP SCRIPT (POWERSHELL)
# ==============================================================================

Write-Host ">>> Initializing Gopher Crawler Environment..." -ForegroundColor Cyan

# 1. Check for Go
if (Get-Command go -ErrorAction SilentlyContinue) {
    $goVer = go version
    Write-Host "[✓] Go Detected: $goVer" -ForegroundColor Green
} else {
    Write-Host "[✕] Go is not installed or not on PATH." -ForegroundColor Red
}

# 2. Check for Node
if (Get-Command node -ErrorAction SilentlyContinue) {
    $nodeVer = node --version
    Write-Host "[✓] Node.js Detected: $nodeVer" -ForegroundColor Green
} else {
    Write-Host "[✕] Node.js is not installed or not on PATH." -ForegroundColor Red
}

# 3. Check for Docker
if (Get-Command docker -ErrorAction SilentlyContinue) {
    Write-Host "[✓] Docker is available" -ForegroundColor Green
} else {
    Write-Host "[!] Docker not detected. Native Go dev mode will be required." -ForegroundColor Yellow
}

# 4. Copy .env if not exists
if (-not (Test-Path .env)) {
    Copy-Item .env.example .env
    Write-Host "[✓] Created .env configuration from .env.example" -ForegroundColor Green
} else {
    Write-Host "[i] .env file already exists" -ForegroundColor Gray
}

# 5. Download Go dependencies
Write-Host ">>> Downloading Go modules..." -ForegroundColor Cyan
go mod download
if ($LASTEXITCODE -eq 0) {
    Write-Host "[✓] Go modules ready" -ForegroundColor Green
}

# 6. Install Node dependencies
if (Test-Path package.json) {
    Write-Host ">>> Installing Node packages..." -ForegroundColor Cyan
    npm install --silent
    Write-Host "[✓] Node packages ready" -ForegroundColor Green
}

Write-Host "`n>>> Setup Complete! Run '.\scripts\dev.ps1' or 'docker compose up' to start." -ForegroundColor Cyan
