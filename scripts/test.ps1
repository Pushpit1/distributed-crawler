# ==============================================================================
# GOPHER CRAWLER — VERIFICATION SUITE (POWERSHELL)
# ==============================================================================

Write-Host ">>> Running Gopher Crawler Full Verification Suite..." -ForegroundColor Cyan

$failed = $false

Write-Host "`n[1/5] Syncing web assets -> public and running JS syntax lint..." -ForegroundColor Cyan
npm run build
if ($LASTEXITCODE -ne 0) { $failed = $true }
npm run lint
if ($LASTEXITCODE -ne 0) { $failed = $true }

Write-Host "`n[2/5] Running Node Serverless Engine Tests..." -ForegroundColor Cyan
npm test
if ($LASTEXITCODE -ne 0) { $failed = $true }

Write-Host "`n[3/5] Running Go Vet Static Analysis..." -ForegroundColor Cyan
go vet ./...
if ($LASTEXITCODE -ne 0) { $failed = $true }

Write-Host "`n[4/5] Running Go Unit & Integration Tests..." -ForegroundColor Cyan
$useRace = $false
if (Get-Command gcc -ErrorAction SilentlyContinue) {
    $gccTarget = (gcc -dumpmachine 2>$null)
    if ($gccTarget -match "64") { $useRace = $true }
}
if ($useRace) {
    Write-Host "[i] 64-bit GCC detected ($gccTarget) — enabling race detector" -ForegroundColor Gray
    $env:CGO_ENABLED = "1"
    go test -p 1 -v -race ./...
} else {
    $env:CGO_ENABLED = "0"
    go test -p 1 -v ./...
}
if ($LASTEXITCODE -ne 0) { $failed = $true }

if (Get-Command docker -ErrorAction SilentlyContinue) {
    Write-Host "`n[5/5] Validating Docker Compose Configuration..." -ForegroundColor Cyan
    docker compose config --quiet
    if ($LASTEXITCODE -ne 0) { $failed = $true }
}

if ($failed) {
    Write-Host "`n[✕] Verification Failed! See above output." -ForegroundColor Red
    exit 1
} else {
    Write-Host "`n[✓] All Verification Suites Passed!" -ForegroundColor Green
    exit 0
}
