# IMPLEMENTATION ROADMAP — DISTRIBUTED CRAWLER
**Document:** `docs/IMPLEMENTATION_ROADMAP.md`  
**Target:** Production-Grade Distributed Web Crawling & Telemetry Platform  
**Repository:** [https://github.com/Pushpit1/distributed-crawler](https://github.com/Pushpit1/distributed-crawler)

---

## Milestone 1: Database Architecture & Migration Engine
**Objective:** Expand relational schema from basic pages into a complete distributed crawl tracking model.
- [ ] Implement migration files under `pkg/db/migrations/` and safe embedded schema bootstrap.
- [ ] Create `crawl_jobs` table (id, status, seed_urls, max_depth, max_pages, pages_crawled, created_at, updated_at, completed_at, error_message).
- [ ] Create `crawl_seeds` table (id, job_id, url, status, created_at).
- [ ] Create `discovered_urls` table (id, job_id, source_page_id, url, domain, depth, status, created_at).
- [ ] Create `crawl_errors` table (id, job_id, url, worker_id, status_code, error_type, error_message, created_at).
- [ ] Create `worker_registry` table (id, hostname, status, tasks_active, tasks_completed, tasks_failed, avg_latency_ms, version, last_heartbeat, started_at).
- [ ] Create `crawl_events` table (id, job_id, worker_id, event_type, url, status_code, latency_ms, payload, created_at).
- [ ] Create `domain_statistics` table (domain, total_pages, total_bytes, avg_latency_ms, last_crawled_at).
- [ ] Add GIN index and `tsvector` column for PostgreSQL Full-Text Search on `crawled_pages`.
- [ ] **Acceptance Criteria:** Database migrations execute idempotently without dropping existing data; schema verifies with automated integration tests.

---

## Milestone 2: Crawling Engine Hardening & SSRF Security
**Objective:** Eliminate SSRF vulnerabilities and augment HTML metadata extraction.
- [ ] Implement `SSRFSafeTransport` in `pkg/crawler/safe_dialer.go`:
  - Resolves IP addresses before connecting.
  - Re-checks resolved IPs on HTTP redirects.
  - Blocks loopback (`127.0.0.0/8`, `::1`), private ranges (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`), link-local (`169.254.0.0/16`), and multicast.
- [ ] Enforce response size limits with `io.LimitReader(resp.Body, maxBytes)` (default 10MB).
- [ ] Extract enhanced HTML metadata:
  - Page title & meta description
  - Canonical URL (`<link rel="canonical">`)
  - Content type & page byte size
  - Headings (`<h1>`)
  - Outgoing links categorized as internal vs external
  - HTTP status, latency duration, crawl timestamp
- [ ] **Acceptance Criteria:** Unit tests verify that private IP URLs (`http://127.0.0.1`, `http://169.254.169.254`, `http://10.0.0.1`) are promptly rejected before TCP dial; valid public URLs fetch cleanly.

---

## Milestone 3: Distributed Worker Reliability & Lease Management
**Objective:** Implement fault-tolerant distributed worker protocol with leases and recovery.
- [ ] Worker lifecycle states: `STARTING`, `IDLE`, `ACTIVE`, `DRAINING`, `UNHEALTHY`, `STOPPED`.
- [ ] Heartbeat reporting mechanism: Workers send periodic telemetry to `POST /api/workers/{id}/heartbeat`.
- [ ] Task lease & in-flight tracking:
  - When a task is popped, it is recorded with a visibility lease in Redis.
  - Workers acknowledge task completion via `POST /api/report`.
  - Coordinator runs a background reaper that recovers expired leases and re-enqueues abandoned tasks.
- [ ] **Acceptance Criteria:** Worker crash during an active crawl results in the lease expiring and the task being safely reclaimed and crawled by another worker.

---

## Milestone 4: Full-Text Search & Indexing Engine
**Objective:** Build high-performance, ranked search over crawled web content.
- [ ] Add full-text search query using PostgreSQL `plainto_tsquery('english', $1)` with `ts_rank`.
- [ ] Support hybrid matching: full-text score + exact URL/domain substring fallback.
- [ ] Support pagination (`page`, `limit`), sorting (`date`, `relevance`, `latency`), domain filtering, and HTTP status filtering.
- [ ] **Acceptance Criteria:** Search queries return ranked results from persisted PostgreSQL records with sub-millisecond execution times.

---

## Milestone 5: Complete REST API & Observability
**Objective:** Expose comprehensive REST endpoints for control, monitoring, and administrative governance.
- [ ] **Health & Readiness:** `GET /health`, `GET /ready`, `GET /api/health`
- [ ] **Telemetry:** `GET /api/status`, `GET /api/metrics`, `GET /api/workers`, `GET /api/activity`
- [ ] **Job Operations:** `POST /api/submit`, `GET /api/jobs`, `GET /api/jobs/{id}`, `POST /api/jobs/{id}/pause`, `POST /api/jobs/{id}/resume`, `POST /api/jobs/{id}/cancel`
- [ ] **Search & Domains:** `GET /api/search`, `GET /api/pages/{id}`, `GET /api/domains`
- [ ] **Administration:** `POST /api/reset`, `GET /api/config`
- [ ] **Acceptance Criteria:** All endpoints return structured JSON, enforce CORS policy, handle errors gracefully, and are documented via OpenAPI schema.

---

## Milestone 6: Enterprise Operator Frontend (Bonanza/Descycle Aesthetic)
**Objective:** Provide an intuitive multi-view dashboard for operators and engineers.
- [ ] Navigation structure:
  1. **Overview Dashboard:** Cluster telemetry, throughput sparklines, recent crawl activity, system health.
  2. **Crawl Explorer:** Advanced URL submission, preset templates, depth and concurrency configuration.
  3. **Jobs Manager:** Active, queued, paused, completed, and cancelled jobs table with inspection modal.
  4. **Search Index:** Full-text search, multi-filter query bar, pagination, page preview drawer.
  5. **Domains:** Domain distribution metrics, host page counters, latency statistics.
  6. **Worker Fleet:** Node grid showing state, heartbeat timers, task throughput, and health chips.
  7. **Activity Logs:** High-density live telemetry stream with search, status filtering, and pause toggle.
  8. **System Analytics:** Distribution charts for HTTP response codes, latency bands, and throughput.
  9. **Settings & Operations:** Backend endpoint selector, crawl policies, and tiered reset console.
  10. **Architecture & Docs:** Embedded system diagrams and API reference.
- [ ] **Acceptance Criteria:** Frontend renders seamlessly in dark mode with responsive mobile support, zero console errors, and live backend synchronization.

---

## Milestone 7: Tiered Reset & Data Governance
**Objective:** Implement multi-level administrative resets with safe defaults.
- [ ] Level A: Clear UI state (local temporary filters).
- [ ] Level B: Reset/Cancel active crawl job.
- [ ] Level C: Flush pending Redis frontier queue.
- [ ] Level D: Purge indexed pages and domains.
- [ ] Level E: Full development reset.
- [ ] Level F: Production reset requiring confirmation phrase (`WIPE ALL DATA`).
- [ ] **Acceptance Criteria:** Irreversible resets cannot be triggered accidentally and require explicit modal verification.

---

## Milestone 8: Dual-Mode Deployment & Cloud Packaging
**Objective:** Support full distributed cluster deployment alongside lightweight Vercel serverless preview.
- [ ] **Mode A (Cluster):** Docker Compose setup with Master, Workers, Redis, and PostgreSQL.
- [ ] **Mode B (Serverless):** Vercel serverless execution with explicit "Serverless Preview" badge.
- [ ] **Acceptance Criteria:** `docker-compose up` spins up healthy services; Vercel deployment builds cleanly and executes without errors.

---

## Milestone 9: Automated Testing & Verification
**Objective:** Complete QA test suite across Go and frontend modules.
- [ ] Go unit tests (`go test -v ./...`).
- [ ] Go race detector validation (`go test -race ./...`).
- [ ] Node serverless test suite (`npm test`).
- [ ] Generate comprehensive `docs/TESTING_REPORT.md`.
- [ ] **Acceptance Criteria:** All tests pass with zero regressions.

---

## Milestone 10: Documentation & Developer Ergonomics
**Objective:** Professional documentation and automated setup scripts.
- [ ] Generate `README.md`, `docs/ARCHITECTURE.md`, `docs/API.md`, `docs/DEPLOYMENT.md`, `docs/OPERATIONS.md`, `docs/SECURITY.md`.
- [ ] Create automation scripts: `.env.example`, `Makefile`, `scripts/setup.ps1`, `scripts/dev.ps1`, `scripts/test.ps1`, `scripts/reset-dev.ps1` (and bash equivalents).
- [ ] **Acceptance Criteria:** New developers can clone and boot the entire stack in under 3 commands.
