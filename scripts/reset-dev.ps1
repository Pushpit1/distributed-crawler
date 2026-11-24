# ==============================================================================
# GOPHER CRAWLER — DEV RESET SCRIPT (POWERSHELL)
# ==============================================================================

Write-Host ">>> Resetting Local Development Environment..." -ForegroundColor Yellow

if (Get-Command docker -ErrorAction SilentlyContinue) {
    Write-Host ">>> Stopping Docker containers and removing volumes..." -ForegroundColor Cyan
    docker compose down -v
    Write-Host "[✓] Docker containers and local storage volumes removed" -ForegroundColor Green
}

# Clear any compiled binaries
if (Test-Path bin) {
    Remove-Item -Recurse -Force bin
    Write-Host "[✓] Removed bin/ directory" -ForegroundColor Green
}

Write-Host "`n>>> Local environment clean. Re-launch with '.\scripts\dev.ps1'" -ForegroundColor Cyan
