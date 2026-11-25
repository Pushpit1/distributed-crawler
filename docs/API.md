# GOPHER CRAWLER — REST API REFERENCE (v2.0)

All routes return structured JSON (`error`, `status`, `request_id` on failures) with `X-Request-ID` correlation headers (except Prometheus text format on `/api/metrics` and CSV downloads on `/export`).

---

## 1. Health & Readiness

- `GET /health` | `GET /api/health` → `{ status: "healthy", service, mode, version, uptime_sec, timestamp }`
- `GET /ready` | `GET /api/ready` → `{ status: "ready"|"unready", mode, redis_ready, db_ready, timestamp }` (HTTP 503 if dependency is down)

---

## 2. Telemetry, Prometheus Metrics & Analytics

- `GET /status` | `GET /api/status` → Complete live cluster telemetry (`mode`, `queue_size`, `pages_indexed`, `discovered_urls_count`, `domain_count`, `active_jobs`, `completed_jobs`, `failed_jobs`, `workers_active`, `leases_active`, `leases_reclaimed`, `throughput_rpm`, `avg_latency_ms`, `success_rate`, `robots_blocked`, `uptime_sec`, `workers[]`, `recent_pages[]`, `recent_jobs[]`, `domain_breakdown[]`, `live_logs[]`).
- `GET /metrics` | `GET /api/metrics` → Prometheus v0.0.4 exposition (`crawl_requests_total`, `crawl_requests_success_total`, `crawl_requests_failed_total`, `crawl_request_duration`, `crawl_pages_indexed_total`, `crawl_queue_depth`, `crawl_jobs_active`, `crawl_jobs_completed`, `crawl_workers_active`, `crawl_worker_heartbeat_age`, `crawl_task_retries_total`, `crawl_database_errors_total`). Pass `?format=json` for JSON.
- `GET /api/analytics?range=24h|1h|all` → SQL/backend aggregated latency percentiles (`p50_latency_ms`, `p95_latency_ms`), `status_breakdown[]`, `error_categories[]`, `domain_distribution[]`, `throughput_series[]`, `job_breakdown`.
- `GET /api/workers` → `{ workers: [...], total }`
- `GET /api/workers/{id}` → Single worker node details.
- `GET /api/activity?severity=INFO|WARN|ERROR&q=&limit=50` → `{ events: [...], total }`
- `GET /api/errors` → `{ errors: [...], total }`

---

## 3. Crawl Submission & Job Management

- `POST /api/submit` | `POST /submit`
  ```json
  {
    "name": "Go Docs Crawl",
    "urls": ["https://go.dev"],
    "max_depth": 3,
    "max_pages": 100,
    "concurrency": 4,
    "politeness_delay_ms": 1000,
    "allowed_domains": ["go.dev", "pkg.go.dev"],
    "exclude_patterns": ["/login", "\\.pdf$"]
  }
  ```
- `POST /api/crawl` → Synchronously executes one bounded frontier crawl step.
- `GET /api/jobs` → `{ jobs: [...], total }`
- `GET /api/jobs/{id}` → `{ job, seeds[], errors[], events[], associated_workers[] }`
- `POST /api/jobs/{id}/pause` → Parks job frontier tasks & sets status `PAUSED`.
- `POST /api/jobs/{id}/resume` → Re-enqueues parked tasks & sets status `RUNNING`.
- `POST /api/jobs/{id}/cancel` → *(Admin token if configured)* Discards queued tasks & sets `CANCELLED`.
- `POST /api/jobs/{id}/retry` → Re-enqueues failed URLs recorded in `crawl_errors`.
- `GET /api/jobs/{id}/export?format=csv|json` → Download results for a specific job.

---

## 4. Full-Text Search, Inspection & Export

- `GET /api/search?q=&domain=&status=2xx|3xx|4xx|5xx|200&from=&to=&sort=relevance|newest|oldest|latency_asc|latency_desc|status&limit=20&offset=0`
  Returns `{ results: [...], total, limit, offset }`.
- `GET /api/pages/{id}` → Full page metadata (`final_url`, `canonical_url`, `headings`, `extracted_text`, `internal_links`, `external_links`).
- `GET /api/domains?limit=50` → `{ domains: [...], total }`.
- `GET /api/export?format=csv|json&q=&domain=&status=` → Export filtered index dataset.

---

## 5. Worker Protocol (Internal Cluster RPC)

- `POST /api/register` (`{ hostname, version }`)
- `POST /api/heartbeat` (`{ worker_id, hostname, version, status, current_task, tasks_active, tasks_completed, tasks_failed, avg_latency_ms, uptime_sec }`)
- `POST /api/poll` (`{ worker_id }`) → `{ url, job_id, depth, max_depth, allowed_domains, exclude_patterns, lease_key }`
- `POST /api/report` (`ReportRequest`) → Persists document, updates stats/job progress, enqueues bounded child links, releases task lease.

---

## 6. Safe Reset & Configuration

- `POST /api/reset` (`{ level, job_id, confirmation_phrase }`)
  - Levels: `"ui"`, `"job"`, `"queue"`, `"job_data"`, `"data"`, `"dev"`, `"production"`.
- `GET /api/config` & `POST /api/config` → Read/update runtime crawler configuration.
