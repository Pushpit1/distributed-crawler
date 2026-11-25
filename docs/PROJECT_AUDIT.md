# PROJECT AUDIT — DISTRIBUTED CRAWLER PLATFORM
**Document:** `docs/PROJECT_AUDIT.md`  
**Audit Revision:** 2.0 (Deep Source-Level Code Verification)  
**Auditor:** Principal Software & Distributed Systems Architect  
**Repository:** [https://github.com/Pushpit1/distributed-crawler](https://github.com/Pushpit1/distributed-crawler)

---

## 1. Executive Summary

A deep line-by-line inspection of all Go packages, SQL migrations, Node.js serverless endpoints, frontend HTML/CSS/JS bundles, and test reports uncovered critical contract mismatches, security gaps, unimplemented job enforcement logic, and unverified/skipped integration tests beneath the existing repository layout.

This audit separates **verified working functionality** from **partial, broken, insecure, or unverified components** so all defects are systematically remediated.

---

## 2. Detailed Codebase Inspection Findings

### 2.1 Security Vulnerabilities Identified
1. **SSRF Bypass in `robots.txt` Fetcher (`pkg/crawler/robots.go`):**
   - `NewRobotsCache` instantiated `&http.Client{Timeout: 5 * time.Second}` using Go's default `http.DefaultTransport` instead of `NewSSRFSafeTransport` and `SafeRedirectPolicy`.
   - Because workers call `robotsCache.IsAllowed(ctx, taskURL)` *before* `parser.FetchAndParse`, submitting a malicious URL targeting internal infrastructure (e.g., `http://127.0.0.1:6379/` or `http://169.254.169.254/`) caused workers to issue an unguarded internal HTTP GET request for `/robots.txt`.
   - `robots.txt` fetch lacked `io.LimitReader` bounds and IPv4-mapped IPv6 address normalization (`::ffff:127.0.0.1`).
2. **Missing SSRF Validation at Coordinator Ingestion (`internal/master/server.go`):**
   - `HandleSubmitSeeds` pushed URLs directly into Redis without running `ValidateTargetURL`, allowing internal URLs into the frontier.
3. **Node Serverless SSRF Only Checked Literal Hostname Patterns (`api/_engine.js`):**
   - `isPrivateOrReservedHost` only matched hostname regexes (`127.`, `localhost`), failing to block custom domains resolving via DNS to private IPs or redirects pointing internally.
4. **Unprotected Destructive Operations & Missing Rate Limiting:**
   - `/api/reset` had a phrase check only for `"production"`, while `"dev"`, `"data"`, `"queue"`, and `/api/jobs/{id}/cancel` were completely unauthenticated.
   - `HandleCORS` hardcoded `Access-Control-Allow-Origin: *` without configurable `CORS_ALLOWED_ORIGINS`.
   - No request body size limit (`http.MaxBytesReader`), no API rate limiting, no request correlation IDs, and un-sanitized log strings susceptible to CR/LF log injection.

### 2.2 Broken Frontend ↔ Backend Contracts (`web/app.js` vs `server.go` / `api/*.js`)
1. **Response Wrapper Mismatch:**
   - Go Master (`/api/jobs`, `/api/search`, `/api/domains`, `/api/workers`, `/api/activity`) and Node serverless endpoints return top-level JSON arrays `[...]`.
   - `web/app.js` attempted to read nested keys (`data.jobs`, `res.results`, `res.domains`, `res.workers`, `res.events`), resolving to `undefined || []` and causing **empty tables/lists** even when real records existed.
2. **Field Name Mismatches:**
   - `GET /api/status`: Backend emitted `status: "online"`, `mode: "cluster"|"serverless_preview"`, `visited_count`, `domain_count`, `uptime_sec`. Frontend checked `data.status === 'ok'` (rendering `DEGRADED`), read `data.operating_mode` (always falling back to `[MODE A: CLUSTER]`), read `data.pages_crawled` (rendering `0`), and `data.domains_discovered` (rendering `0`).
   - `GET /api/jobs`: Backend emitted `pages_crawled`; `app.js` read `job.crawled_pages` (always `0`).
   - `GET /api/workers`: Backend emitted `id`; `app.js` read `w.worker_id` (rendering `worker-node`).
   - `GET /api/search`: Backend emitted `description`; `app.js` read `item.meta_description` (missing descriptions).
   - `POST /api/crawl`: Missing entirely on Go Master; Vercel returned `res.page` while `app.js` looked for `res.crawled`.
   - `POST /api/jobs/{id}/{op}`: Failed with 404 on Vercel because dynamic sub-routes under `api/jobs/` did not exist.
3. **CSS Class Mismatch & Unstyled Elements (`web/style.css` vs `web/app.js`):**
   - `web/app.js` generated `.recent-page-card`, `.search-result-card`, `.toast-container`, `.terminal-line`, `.domain-stat-row`, `.worker-snap-row`, `.feedback-banner`, `.btn-job-action`, and `.inspector-metrics-grid`, none of which existed in `web/style.css`.
4. **Hardcoded Analytics Metrics (`web/index.html`):**
   - `an-avg-latency` (`184 ms`), `an-success-rate` (`98.2%`), `an-robots-rate` (`100%`), and `an-lease-reclaims` (`0 tasks`) were hardcoded static HTML values never populated from backend telemetry.

### 2.3 Distributed Engine & Persistence Gaps
1. **Unenforced Job Constraints:**
   - Redis frontier pushed raw URL strings instead of structured tasks carrying `job_id` and `depth`.
   - `crawl_jobs.pages_crawled`, `pages_failed`, `completed_at`, `max_depth`, `max_pages`, `allowed_domains`, and `exclude_patterns` were ignored during crawling.
   - Pausing or cancelling a job updated only the PostgreSQL row without pausing or cancelling tasks in the Redis frontier/poll loop.
2. **Broken Domain Statistics Query:**
   - `SavePageFull` updated `domain_statistics` with `total_bytes`, `avg_latency_ms`, and `error_count`, but `GetDomainMetrics` queried `domains` with hardcoded `0, 0` (`SELECT host, total_pages, 0, 0, updated_at FROM domains`).
3. **Unused Database Tables & Incomplete Metadata:**
   - `discovered_urls`, `crawl_errors`, and `crawl_settings` existed in schema DDL but were never read or written by the application.
   - `final_url`, body `extracted_text`, and structured heading extraction were absent.
4. **Queue Reset Wiping Visited State:**
   - `Reset` level `"queue"` called `frontier.Clear()`, deleting `crawler:visited` even though PostgreSQL retained indexed pages, causing `visited_count` to diverge.
5. **Serverless Preview Simulated Workers:**
   - `api/_engine.js` fabricated 3 dummy workers (`worker-node-us-east-1`, etc.) with `Math.random()` heartbeat latencies. Persistent workers must never be faked in serverless mode.
6. **Non-Prometheus Metrics & Unexecuted Integration Tests:**
   - `GET /api/metrics` returned JSON instead of standard Prometheus exposition format (`crawl_requests_total`, etc.).
   - Prior PostgreSQL integration tests skipped silently due to port conflict on `5433`, leaving database migrations, full-text search, lease recovery, and E2E multi-worker flows unverified.

---

## 3. Preservation & Remediation Strategy

- **Preserve:** Working Redis frontier patterns, Go HTTP coordinator/worker separation, `golang.org/x/net/html` parser, `temoto/robotstxt`, PostgreSQL GIN `tsvector` foundation, obsidian theme visual identity.
- **Fix & Complete:** Eliminate every security bypass, unify Go/Node/UI API contracts, enforce job depth/page/pause/cancel state through structured Redis frontier tasks, remove fake metrics/workers, implement real analytics & Prometheus exposition, add CI/CD & rate limiting, and execute full race + PostgreSQL/Redis + Docker + E2E browser verification.
