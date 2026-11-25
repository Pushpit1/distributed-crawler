# DISTRIBUTED CRAWLER — TESTING & VERIFICATION REPORT

**Date of Verification:** 2026-11-25  
**Operating Environment:** Go 1.26.5 (`windows/amd64`, `CGO_ENABLED=0`) / Node.js v24.18.0 / Docker 29.6.1 (ephemeral PostgreSQL 16 & Redis 7) / Windows 11  
**Overall Result:** **PASS (0 Failed, 0 Skipped in Automated Go & Node Suites)**

---

## 1. Test Execution Summary

| Test Suite | Command / Target | Status | Observed Result / Duration | Notes & Environment Details |
|------------|------------------|--------|----------------------------|-----------------------------|
| Go Formatting | `gofmt -l .` | **PASS** | Empty diff (all `.go` files formatted) | Executed after final code edit |
| Go Static Analysis | `go vet ./...` | **PASS** | Exit code 0 | Zero warnings across all packages |
| Go Binary Compilation | `go build ./cmd/master` & `./cmd/worker` | **PASS** | Exit code 0 | Binaries built cleanly and cleaned up |
| Go Distributed Master + E2E | `go test -count=1 -v ./...` -> `internal/master` | **PASS** | `18.506s` (2 tests, 0 skips) | 2 concurrent workers, robots.txt block, transient 500 retries, graceful drain, crashed lease recovery, SSRF `/api/submit`, Prometheus metrics, admin reset guards |
| Go Crawler Engine & Frontier | `go test -count=1 -v ./...` -> `pkg/crawler` | **PASS** | `3.013s` (11 tests, 0 skips) | Safe dialer IPv4/IPv6/SSRF, safe redirect hop check, robots transport & size cap, URL normalization (10 subtests), HTML extraction, Redis FIFO/dedup/politeness/clear |
| Go PostgreSQL Repository | `go test -count=1 -v ./...` -> `pkg/db` | **PASS** | `6.304s` (1 full lifecycle test, `5.18s`, 0 skips) | Advisory-locked migrations, jobs/seeds/pages/errors/discovered URLs, FTS search + filters + sorting + pagination, SQL analytics & percentiles |
| Frontend Asset Sync | `npm run build` | **PASS** | Exit code 0 | Synced `web/{index.html,style.css,app.js}` -> `public/*` |
| JavaScript Syntax Lint | `npm run lint` | **PASS** | Exit code 0 | `node --check` on 19 files (`api/*.js`, `web/app.js`, `public/app.js`) |
| Serverless Engine Tests | `npm test` (`node --test api/engine.test.js`) | **PASS** | `444.49ms` (4/4 passed, 0 failed, 0 skipped) | IPv4/IPv6/SSRF validator, zero fake workers/pages on boot, job lifecycle, search filters/pagination, analytics, tiered resets |
| Docker Compose Config | `docker compose config --quiet` & `--profile monitoring config --quiet` | **PASS** | Exit code 0 | Validated default stack and Prometheus/Grafana monitoring profile |

---

## 2. Detailed Verification Scope & Honest Limitations

### 2.1 Go Race Detector (`-race`) on Windows Host
- On this Windows 11 host (`go1.26.5 windows/amd64`), the local MinGW toolchain (`gcc` / `cc1.exe`) is 32-bit, so `CGO_ENABLED=1 go test -race` cannot compile `windows/amd64` binaries locally (`64-bit mode not compiled in`).
- All local runs therefore execute with `CGO_ENABLED=0` (matching `scripts/test.ps1` auto-detection).
- Full race detection (`go test -p 1 -v -race ./...`) is configured in `.github/workflows/ci.yml` on `ubuntu-latest` against live Redis 7 and PostgreSQL 16 service containers.

### 2.2 Parallel Test Serialization Against PostgreSQL
- `pkg/db` (`TestPostgresRepository_FullLifecycle`) and `internal/master` (`TestEndToEndDistributedWorkflow_MultiWorkerAndLeaseRecovery`) share a PostgreSQL instance when running `go test -count=1 -v ./...` across packages in parallel.
- Both `initSchema` (lock ID `78493021`) and the integration test fixture (`AcquireAdvisoryLock(..., 99887766)`) pin a dedicated `*sql.Conn` connection for PostgreSQL advisory locks, preventing `TRUNCATE TABLE ... CASCADE` deadlocks (`40P01`) even without `-p 1`, while `Makefile`, `scripts/test.ps1`, `scripts/test.sh`, and `.github/workflows/ci.yml` also pass `-p 1` as defense in depth.

### 2.3 Browser UI Verification
- Earlier in the verification cycle against the live Docker cluster (`http://localhost:8088`), headless browser subagent `c5cde501-fe27-4f52-b316-db5b4363901b` verified HTTP 200 load, zero console/network errors, populated metrics, Search Index (`7,374` indexed pages from the existing DB volume), SSRF blocking on `http://169.254.169.254/latest/meta-data/` (`400 Bad Request`), and Overview/Jobs/Workers/Logs views.
- After the final CSS refinement (moving `#mobile-nav-backdrop` inside `.platform-layout` and applying `minmax(0, 1fr)` grid overflow fixes in `web/index.html` and `web/style.css`), subsequent browser subagent follow-ups timed out in the IDE webview runtime (`WEBVIEW_TIMEOUT` / `WEBVIEW_UNRESPONSIVE` after 1m0s), so post-CSS-tweak UI layout is verified by `npm run build` asset synchronization and source inspection rather than a fresh browser screenshot.

---

## 3. Executed Case Breakdown

### 3.1 Security & SSRF Defenses (`pkg/crawler/safe_dialer_test.go`, `api/engine.test.js`)
- Blocks IPv4 loopback (`127.0.0.1`), RFC1918 private networks (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`), CGNAT (`100.64.0.0/10`), link-local / cloud metadata (`169.254.169.254`), IPv6 loopback (`::1`), IPv6 ULA (`fc00::/7`), and IPv4-mapped IPv6 (`::ffff:127.0.0.1`, `::ffff:169.254.169.254`).
- Rejects non-HTTP(S) schemes (`file://`, `javascript:`, `mailto:`), embedded URL credentials, `localhost`, `.local`, `.internal`, and redirect hops targeting blocked subnets (`TestSafeRedirectPolicy_BlocksInternalHop`).
- Enforces `RobotsCache` SSRF-safe HTTP transport and `512KB` body size limit.

### 3.2 Distributed Execution & Lease Recovery (`internal/master/server_test.go`)
- Spins up Coordinator HTTP server + PostgreSQL 16 + Redis 7 + 2 real worker daemons + deterministic `httptest.Server` target site.
- Verifies root crawl, depth-limited link discovery, `robots.txt` `Disallow: /secret` compliance (`403` skipped), automatic exponential retry on transient HTTP `500` followed by `502` error persistence, clean worker draining (`SIGINT`/cancel shutdown), and coordinator reclamation + re-execution of an expired 60s task lease from a simulated crashed worker (`crashed-worker-99`).
- Validates `/health`, `/api/metrics` Prometheus exposition, SSRF rejection on `/api/submit`, and `ADMIN_API_TOKEN` + `"WIPE ALL DATA"` confirmation guards on `/api/reset`.

### 3.3 Storage & Full-Text Search (`pkg/db/postgres_test.go`)
- Exercises `CreateJob`, `SavePageFull` (`pq.Array` across `TEXT[]` columns + transactional `discovered_urls`, `crawl_errors`, `domain_statistics`), `GetJobDetails`, `SearchPagesWithTotal` (FTS ranking, status/domain filters, sorting, pagination), `GetAnalytics` (`AVG`, `PERCENTILE_CONT(0.50)` / `(0.95)`, status & error breakdown, throughput buckets), and `DeleteJobData`.
