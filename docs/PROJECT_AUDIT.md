# PROJECT AUDIT — DISTRIBUTED CRAWLER PLATFORM
**Document:** `docs/PROJECT_AUDIT.md`  
**Date:** November 2026  
**Auditor:** Principal Software & Distributed Systems Architect  
**Repository:** [https://github.com/Pushpit1/distributed-crawler](https://github.com/Pushpit1/distributed-crawler)

---

## 1. Executive Summary

This repository implements a distributed web crawler and telemetry dashboard utilizing Go, Redis, PostgreSQL, and Vercel serverless functions. 

The audit identified a solid foundational architecture with working Redis FIFO queues, Robots.txt caching, PostgreSQL upserts, and basic Go worker/master coordination. However, to transform this from a capable prototype into a **production-grade enterprise crawling and search engine**, critical enhancements are required across task leases, SSRF security defenses, database schema normalization (introducing job management, event logs, and worker persistence), full-text search indexing, multi-level reset protection, and a multi-view operator dashboard.

---

## 2. Existing Architecture Analysis

### 2.1 Backend Components (Go)
- **Coordinator Master (`cmd/master/main.go`, `internal/master/server.go`)**:
  - Exposes HTTP REST routes on port `8080`.
  - Maintains an in-memory worker registry (`map[string]time.Time`).
  - Implements polite domain rate limiting via Redis TTL keys (`rate_limit:host:<domain>`).
  - Serves static dashboard assets from `web/`.
- **Worker Daemon (`cmd/worker/main.go`, `internal/worker/client.go`)**:
  - Registers with master via `POST /register`.
  - Continuously polls for tasks via `POST /poll`.
  - Checks compliance with `robots.txt` via `pkg/crawler/robots.go`.
  - Fetches and parses HTML using `pkg/crawler/parser.go`.
  - Reports crawl findings via `POST /report`.
- **URL Frontier (`pkg/crawler/frontier.go`)**:
  - Redis-backed deduplication set (`SADD` on `crawler:visited`) and FIFO queue (`LPUSH`/`RPOP` on `crawler:queue`).
  - Domain rate limiting check with up to 10 fallback queue inspections.
- **Persistence Layer (`pkg/db/postgres.go`)**:
  - PostgreSQL tables: `domains` (unique hosts) and `crawled_pages` (url, title, description, status_code, crawled_at).
  - Uses `ON CONFLICT (host) DO UPDATE` and `ON CONFLICT (url) DO UPDATE`.
  - Search implemented via basic SQL `ILIKE %query%`.

### 2.2 Vercel Serverless Layer (`api/`, `public/`)
- Node.js serverless functions in `api/`: `status.js`, `search.js`, `submit.js`, `crawl.js`, `reset.js`, `health.js`, and `_engine.js`.
- Capable of dual-mode execution:
  - **Mode A (Proxy)**: If `CRAWLER_BACKEND_URL` is set, proxies API calls to an external Go coordinator.
  - **Mode B (Serverless Demo)**: Standalone in-memory crawler executing real live HTTP fetches on demand.

---

## 3. Findings & Technical Deficits

### 3.1 Security Deficits (High Priority)
1. **SSRF (Server-Side Request Forgery)**:
   - `pkg/crawler/parser.go` fetches any target URL without verifying destination IP addresses.
   - Attackers could submit seeds pointing to `http://127.0.0.1:6379` (Redis), `http://localhost:5433` (Postgres), `http://169.254.169.254` (cloud metadata service), or internal RFC1918 private subnets (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`).
   - Fix: Implement an SSRF-safe `http.Transport` with custom `DialContext` that validates resolved IP addresses before connection and blocks private, link-local, loopback, and broadcast ranges.
2. **Decompression Bombs & Unbounded Body Reads**:
   - `FetchAndParse` uses `html.Parse(resp.Body)` without an explicit `io.LimitReader`.
   - Fix: Wrap response streams in `io.LimitReader(resp.Body, maxBodyBytes)` (e.g., 5MB–10MB limit).
3. **CORS Policy in Production**:
   - Headers currently set `Access-Control-Allow-Origin: *`.
   - Fix: Allow configurable allowed origins via environment variable `CORS_ALLOWED_ORIGINS` with a safe default for development.

### 3.2 Distributed Architecture & Task Reliability Deficits
1. **No Task Visibility Timeout / Lease Mechanism**:
   - When a worker pops a URL via `GetPoliteTask`, the URL is immediately removed from the Redis list.
   - If the worker crashes, loses power, or is killed during fetching, the URL is permanently lost and never crawled.
   - Fix: Implement task leasing/in-flight tracking with lease expiration and dead-letter/re-queue recovery.
2. **In-Memory Worker Registry**:
   - Active workers are stored in memory (`s.registry.workers`). If the master restarts, all worker records disappear until their next poll.
   - Fix: Persist worker heartbeat metadata to the database `worker_registry` table and Redis.
3. **Absence of Job Management**:
   - Crawls are currently unstructured single URLs submitted to a global queue without `job_id`, depth limits, page caps, or cancellation/pause flags.
   - Fix: Introduce a first-class `crawl_jobs` entity with statuses (`PENDING`, `RUNNING`, `PAUSED`, `COMPLETED`, `CANCELLED`, `FAILED`).

### 3.3 Database & Search Deficits
1. **Under-normalized Schema**:
   - Only `domains` and `crawled_pages` exist.
   - Missing tables: `crawl_jobs`, `crawl_seeds`, `discovered_urls`, `crawl_errors`, `worker_registry`, `crawl_events`, `domain_statistics`, `crawl_settings`.
2. **Suboptimal Search**:
   - SQL `ILIKE %query%` cannot utilize standard B-tree indexes and triggers full table scans on large crawl datasets.
   - Fix: Introduce PostgreSQL full-text search (`tsvector` column and GIN index) with ranking (`ts_rank`) alongside URL prefix/substring matching.

### 3.4 API & Observability Deficits
1. **Missing REST Endpoints**:
   - Missing `/ready`, `/api/metrics` (Prometheus/JSON), `/api/workers`, `/api/activity`, `/api/jobs`, `/api/jobs/{id}/pause`, `/api/jobs/{id}/resume`, `/api/jobs/{id}/cancel`, `/api/domains`, `/api/config`.
2. **Destructive Reset Operation**:
   - `POST /reset` truncates the entire database with a single click.
   - Fix: Implement tiered reset levels with typed confirmation phrases for production resets.

### 3.5 Frontend Architecture Deficits
1. **Single-Page Density**:
   - The UI combines search, seeds, and logs into a single compact view.
   - Fix: Implement a multi-view application structure with dedicated views:
     - Overview Dashboard
     - Crawl Explorer
     - Jobs Manager
     - Search Index Explorer
     - Domain Analytics
     - Worker Fleet Monitor
     - Live Activity Logs
     - System Analytics
     - Settings Console
     - API & Architecture Documentation

---

## 4. Remediation Plan & Priority Matrix

| Milestone | Area | Priority | Impact |
|---|---|---|---|
| **M0** | Repository Audit & Implementation Roadmap | P0 | Baseline documentation |
| **M1** | Database Schema Upgrade & Migrations | P0 | Multi-job & event persistence |
| **M2** | SSRF Security & Engine Hardening | P0 | Network & resource safety |
| **M3** | Distributed Worker State & Task Recovery | P0 | Reliability & fault tolerance |
| **M4** | PostgreSQL Full-Text Search Engine | P1 | Search relevancy & indexing |
| **M5** | Complete REST API & Observability | P1 | API completeness & metrics |
| **M6** | Multi-View Enterprise Frontend Console | P1 | Production operator UX |
| **M7** | Tiered Reset & Data Governance | P1 | Operational safety |
| **M8** | Dual Deployment (Vercel & Cluster) | P2 | Cloud readiness |
| **M9** | Automated Testing & QA Report | P0 | Reliability verification |
| **M10** | Comprehensive Documentation & Tooling | P1 | Developer ergonomics |
