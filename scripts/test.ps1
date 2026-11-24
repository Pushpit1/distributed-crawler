# ==============================================================================
# GOPHER CRAWLER — TEST RUNNER (POWERSHELL)
# ==============================================================================

Write-Host ">>> Running Gopher Crawler Verification Suite..." -ForegroundColor Cyan

$failed = $false

# 1. Go Unit Tests
Write-Host "`n[1/4] Running Go Unit Tests..." -ForegroundColor Cyan
go test -v ./...
if ($LASTEXITCODE -ne 0) { $failed = $true }

# 2. Go Vet
Write-Host "`n[2/4] Running Go Vet Code Quality Check..." -ForegroundColor Cyan
go vet ./...
if ($LASTEXITCODE -ne 0) { $failed = $true }

# 3. Node / Serverless Tests
Write-Host "`n[3/4] Running Serverless Engine Tests..." -ForegroundColor Cyan
npm test
if ($LASTEXITCODE -ne 0) { $failed = $true }

# 4. Docker Compose Config Validation
if (Get-Command docker -ErrorAction SilentlyContinue) {
    Write-Host "`n[4/4] Validating Docker Compose Orchestration..." -ForegroundColor Cyan
    docker compose config --quiet
    if ($LASTEXITCODE -eq 0) {
        Write-Host "[✓] Docker Compose configuration valid" -ForegroundColor Green
    } else {
        $failed = $true
    }
}

if ($failed) {
    Write-Host "`n[✕] Verification Failed! See above errors." -ForegroundColor Red
    exit 1
} else {
    Write-Host "`n[✓] All Test Suites Passed Cleanly!" -ForegroundColor Green
    exit 0
}
