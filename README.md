# GOPHER CRAWLER ⚡
### Production-Grade Distributed Web Crawling, Indexing & Telemetry Platform

A concurrent, distributed web crawling and full-text search platform engineered in **Go**, backed by **Redis** (structured FIFO frontier, politeness throttling, job pause/cancel sets, and atomic task leases) and **PostgreSQL** (relational persistence, SQL analytics aggregations, and GIN `tsvector` full-text search), paired with an obsidian telemetry console (10 views) deployable locally via Docker Compose or to Vercel.

---

## 🏛️ System Architecture

- **Coordinator Master (`cmd/master`, `internal/master`)**: REST API with request correlation IDs, per-IP rate limiting, CORS controls, optional admin authorization, Prometheus `/api/metrics`, SQL analytics engine, and background task lease reaper.
- **Distributed Workers (`cmd/worker`, `internal/worker`)**: Autonomous processes running bounded concurrency pools, 5s heartbeats, `robots.txt` SSRF-safe compliance checks, exponential retries, and graceful `SIGINT`/`SIGTERM` draining.
- **Security Layer (`pkg/crawler/safe_dialer.go`, `pkg/crawler/robots.go`)**: Blocks loopback (`127.0.0.0/8`, `::1`, `::ffff:127.0.0.1`), RFC 1918 private networks, cloud metadata (`169.254.169.254`), DNS rebinding in `DialContext`, redirect hopping (>5 hops / internal IPs), and 10MB body decompression bombs.
- **PostgreSQL Storage (`pkg/db`)**: Advisory-locked idempotent migrations (`pkg/db/migrations/001_initial_schema.sql`), weighted full-text search, pagination, CSV/JSON exports, and 10 normalized tables.
- **Dual-Mode Vercel Support (`api/`, `public/`)**: Operates either as a proxy to your Go Coordinator (`CRAWLER_BACKEND_URL`) or as an explicit `Serverless Preview` executor without fake workers.

---

## 🖥️ 10 Platform Views

1. **Overview**: 10+ live metrics, top domains, recent crawls, recent jobs, worker fleet snapshot, health checks, and quick-start onboarding.
2. **Crawl Explorer**: 5 functional presets (*Small Website*, *Documentation*, *Research*, *Domain Exploration*, *Custom*), domain allowlist, regex excludes, review panel.
3. **Jobs Manager**: Filterable statuses (`QUEUED`, `RUNNING`, `PAUSED`, `COMPLETED`, `FAILED`, `CANCELLED`), progress bars, inspect modal, Pause/Resume/Retry/Cancel/Export.
4. **Search Index**: Ranked full-text search with status class chips, domain selector, sorting, date filters, pagination, copy URL, Page Inspector modal, and CSV/JSON export.
5. **Domains**: Real PostgreSQL host metrics (`total_pages`, `total_bytes`, `avg_latency_ms`, `error_count`).
6. **Worker Fleet**: Real registered Go nodes, heartbeats, active task URLs, and clear empty-state onboarding CLI instructions.
7. **Activity Logs**: Terminal stream with severity filter, keyword search, copy, pause/resume, and non-destructive clear view.
8. **Analytics**: Backend SQL aggregations (`p50`/`p95` latency, 2xx success ratio, robots blocks, lease reclaims, HTTP code/error/throughput charts).
9. **Settings & Resets**: Coordinator URL configuration, session `X-Admin-Token`, and 7 granular safe reset tiers.
10. **Documentation**: Embedded architecture guide, worker startup snippet, and API reference.

---

## ⚡ Quick Start

### Windows / Linux Local Development
```powershell
# 1. Start PostgreSQL & Redis containers
docker compose up -d redis postgres

# 2. Run Coordinator Master (in terminal 1)
go run ./cmd/master

# 3. Start Distributed Workers (in terminal 2+)
go run ./cmd/worker -master http://localhost:8080 -concurrency 4

# 4. Open Dashboard
start http://localhost:8080
```

### Full Docker Cluster (+ Optional Prometheus & Grafana)
```bash
cp .env.example .env
docker compose up -d --build
docker compose up -d --scale worker=3

# With Prometheus + Grafana observability stack:
docker compose --profile monitoring up -d --build
```

---

## 🧪 Verification & Testing

```bash
go vet ./...
go test -v ./...
npm run build && npm run lint && npm test
docker compose config --quiet
```

See detailed guides in [`docs/`](docs/):
- [`docs/PROJECT_AUDIT.md`](docs/PROJECT_AUDIT.md)
- [`docs/IMPLEMENTATION_ROADMAP.md`](docs/IMPLEMENTATION_ROADMAP.md)
- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)
- [`docs/API.md`](docs/API.md)
- [`docs/DEPLOYMENT.md`](docs/DEPLOYMENT.md)
- [`docs/OPERATIONS.md`](docs/OPERATIONS.md)
- [`docs/SECURITY.md`](docs/SECURITY.md)
- [`docs/TESTING_REPORT.md`](docs/TESTING_REPORT.md)
