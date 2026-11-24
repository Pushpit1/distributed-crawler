# ==============================================================================
# GOPHER CRAWLER — DEV LAUNCHER (POWERSHELL)
# ==============================================================================

Write-Host ">>> Launching Gopher Crawler Development Environment..." -ForegroundColor Cyan

# 1. Start Docker dependencies (Redis & Postgres)
if (Get-Command docker -ErrorAction SilentlyContinue) {
    Write-Host ">>> Starting Redis & PostgreSQL containers..." -ForegroundColor Cyan
    docker compose up -d redis postgres
}

# 2. Start Master Coordinator
Write-Host ">>> Starting Go Coordinator Master Node (Port 8080)..." -ForegroundColor Green
go run cmd/master/main.go
