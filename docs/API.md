# GOPHER CRAWLER — REST API REFERENCE

This document details the REST API implemented by the Gopher Crawler Coordinator Master Node (`cmd/master`) and compatible Vercel Serverless Functions (`/api/*`).

All endpoints return JSON responses. CORS headers (`Access-Control-Allow-Origin: *`) are pre-configured.

---

## 1. System Health & Probes

### 1.1 Coordinator Health Check
- **Endpoint:** `GET /health` or `GET /api/health`
- **Description:** Basic liveness probe returning system status and current ISO timestamp.
- **Example Response:**
```json
{
  "status": "ok",
  "time": "2026-11-24T15:30:00Z"
}
```

### 1.2 System Readiness Probe
- **Endpoint:** `GET /ready` or `GET /api/ready`
- **Description:** Verifies active database connectivity and Redis frontier availability.
- **Example Response:**
```json
{
  "status": "ready",
  "database": "connected",
  "redis": "connected",
  "time": "2026-11-24T15:30:00Z"
}
```

---

## 2. Cluster Telemetry & Observability

### 2.1 Cluster Status
- **Endpoint:** `GET /status` or `GET /api/status`
- **Description:** Returns aggregate metrics, worker count, recent crawled pages, and operating mode (`cluster` or `preview`).
- **Example Response:**
```json
{
  "status": "ok",
  "operating_mode": "cluster",
  "queue_size": 42,
  "pages_crawled": 1280,
  "domains_discovered": 18,
  "workers_active": 4,
  "leases_active": 2,
  "uptime": "3h 42m",
  "recent_pages": [
    {
      "url": "https://go.dev/doc/",
      "title": "Documentation - The Go Programming Language",
      "status_code": 200,
      "latency_ms": 142,
      "depth": 1,
      "crawled_at": "2026-11-24T15:29:45Z"
    }
  ],
  "workers": [
    {
      "worker_id": "worker-alpha-01",
      "status": "ACTIVE",
      "last_heartbeat": "2026-11-24T15:29:58Z",
      "tasks_completed": 320,
      "tasks_failed": 2,
      "avg_latency_ms": 165
    }
  ]
}
```

### 2.2 Prometheus Metrics
- **Endpoint:** `GET /api/metrics`
- **Description:** Prometheus-formatted text metrics for machine scraping.

---

## 3. Crawl Management & Seed Injection

### 3.1 Submit Crawl Job
- **Endpoint:** `POST /submit` or `POST /api/submit`
- **Request Body:**
```json
{
  "urls": [
    "https://go.dev",
    "https://news.ycombinator.com"
  ],
  "max_depth": 3,
  "max_pages": 250,
  "politeness_delay_ms": 1000
}
```
- **Response (200 OK):**
```json
{
  "status": "submitted",
  "job_id": "job-d41d8cd98f00b204e9800998ecf8427e",
  "message": "Successfully submitted 2 URLs for crawling",
  "added_count": 2
}
```
- **SSRF Validation Error (400 Bad Request):**
```json
{
  "error": "URL https://127.0.0.1/admin rejected by SSRF firewall: private or loopback IP"
}
```

### 3.2 List Crawl Jobs
- **Endpoint:** `GET /api/jobs`
- **Response:**
```json
{
  "jobs": [
    {
      "id": "job-d41d8cd98f00b204e9800998ecf8427e",
      "name": "Crawl https://go.dev",
      "status": "RUNNING",
      "max_depth": 3,
      "max_pages": 250,
      "crawled_pages": 48,
      "created_at": "2026-11-24T15:20:00Z"
    }
  ]
}
```

### 3.3 Manage Job Lifecycle
- **Pause Job:** `POST /api/jobs/{id}/pause`
- **Resume Job:** `POST /api/jobs/{id}/resume`
- **Cancel Job:** `POST /api/jobs/{id}/cancel`

### 3.4 Manual Crawl Step
- **Endpoint:** `POST /api/crawl`
- **Description:** Manually pops a single URL from the frontier queue, executes an SSRF-safe crawl step, and stores the results. Useful for debugging and serverless environments.

---

## 4. Search & Document Inspection

### 4.1 Full-Text Search
- **Endpoint:** `GET /search` or `GET /api/search`
- **Query Parameters:**
  - `q`: Search terms (e.g. `concurrent channels`)
  - `status`: Optional HTTP status code filter (`200`, `403`, `500`)
  - `domain`: Optional domain filter (`go.dev`)
  - `limit`: Number of results (default 50)
- **Response:**
```json
{
  "results": [
    {
      "url": "https://go.dev/doc/effective_go",
      "title": "Effective Go - The Go Programming Language",
      "meta_description": "Context and instructions for writing clear, idiomatic Go code.",
      "h1": "Effective Go",
      "status_code": 200,
      "latency_ms": 115,
      "content_length": 65536,
      "depth": 2,
      "internal_links": ["https://go.dev/doc/", "https://go.dev/ref/spec"],
      "external_links": ["https://github.com/golang/go"],
      "crawled_at": "2026-11-24T15:25:00Z"
    }
  ]
}
```

### 4.2 Domain Distribution
- **Endpoint:** `GET /api/domains`
- **Response:**
```json
{
  "domains": [
    {
      "domain": "go.dev",
      "total_pages": 124,
      "avg_latency_ms": 138,
      "last_crawled": "2026-11-24T15:28:10Z"
    }
  ]
}
```

---

## 5. Worker Fleet Management

### 5.1 Register Worker
- **Endpoint:** `POST /api/workers/register`
- **Request Body:**
```json
{
  "worker_id": "worker-node-01"
}
```

### 5.2 Worker Heartbeat
- **Endpoint:** `POST /api/heartbeat`
- **Request Body:**
```json
{
  "worker_id": "worker-node-01",
  "status": "ACTIVE",
  "current_task": "https://go.dev/ref/spec",
  "tasks_completed": 85,
  "tasks_failed": 0,
  "avg_latency_ms": 142
}
```

### 5.3 List Workers
- **Endpoint:** `GET /api/workers`
- **Response:**
```json
{
  "workers": [
    {
      "worker_id": "worker-node-01",
      "status": "ACTIVE",
      "last_heartbeat": "2026-11-24T15:29:55Z",
      "tasks_completed": 85,
      "tasks_failed": 0,
      "avg_latency_ms": 142,
      "current_task": "https://go.dev/ref/spec"
    }
  ]
}
```

---

## 6. Tiered Data Management & Reset

### 6.1 Cluster Reset
- **Endpoint:** `POST /reset` or `POST /api/reset`
- **Request Body Options:**
  - **Level B (Flush Queue):**
    ```json
    { "level": "queue" }
    ```
  - **Level C (Purge Data):**
    ```json
    { "level": "data" }
    ```
  - **Level D (Production Reset - Requires Phrase):**
    ```json
    {
      "level": "production",
      "confirmation_phrase": "WIPE ALL DATA"
    }
    ```
- **Response (200 OK):**
```json
{
  "status": "reset",
  "level": "production",
  "message": "Full production reset completed successfully. Re-seeded starter URL: https://go.dev"
}
```
- **Error (403 Forbidden - Missing Confirmation Phrase):**
```json
{
  "error": "Production reset rejected: confirmation_phrase must be 'WIPE ALL DATA'"
}
```
