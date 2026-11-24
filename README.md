# GOPHER CRAWLER ⚡
### Production-Grade Distributed Web Crawling, Indexing & Telemetry Platform

A high-performance distributed web crawler and full-text search engine written in **Go**, backed by **Redis** (atomic FIFO frontier queue, politeness buffers, and task leases) and **PostgreSQL** (relational page index with GIN-indexed `tsvector` full-text search), accompanied by an obsidian dark-mode observability console.

Inspired by the precision aesthetics of [Bonanza Studios](https://www.bonanza-studios.com) and [Descycle](https://www.descycle.com) — zero AI slop, high-density telemetry, real-time worker node heartbeats, and auditable event streams.

---

## 🏛️ System Architecture

```
                  ┌──────────────────────────────────────────────┐
                  │          OBSERVABILITY CONSOLE (UI)          │
                  │   10-View Telemetry Dashboard & Controller   │
                  └──────────────────────┬───────────────────────┘
                                         │ HTTP REST API
                                         ▼
                  ┌──────────────────────────────────────────────┐
                  │          Go Coordinator Master Node          │
                  │  - Task Scheduling & In-Flight Leases        │
                  │  - Per-Domain Politeness Enforcement         │
                  │  - PostgreSQL Full-Text Search (tsvector)    │
                  │  - Stale Worker Pruning & Lease Reaper       │
                  └──────────────┬────────────────┬──────────────┘
                                 │                │
                 Task Leases     │                │ Save Pages & Search
                 & Frontier FIFO │                │ GIN tsvector Index
                                 ▼                ▼
                     ┌────────────────┐     ┌────────────────┐
                     │  Redis Frontier│     │   PostgreSQL   │
                     │  - FIFO Queue  │     │ - Crawled Pages│
                     │  - Dedup Set   │     │ - Domain Stats │
                     │  - Task Leases │     │ - Job Registry │
                     │  - Politeness  │     │ - Event Logs   │
                     └────────────────┘     └────────────────┘
                                ▲
                                │ Claim Task Lease / Send Heartbeat
                                │
                     ┌──────────┴──────────┐
                     │                     │
             ┌───────────────┐     ┌───────────────┐
             │ Crawler Node  │     │ Crawler Node  │
             │   Worker #1   │     │   Worker #N   │
             │- SSRF Dialer  │     │- SSRF Dialer  │
             │- Robots Cache │     │- Robots Cache │
             │- 10MB Limit   │     │- 10MB Limit   │
             │- Heartbeat 5s │     │- Heartbeat 5s │
             └───────┬───────┘     └───────┬───────┘
                     │                     │
                     └──────────┬──────────┘
                                │ SSRF-Protected Outbound HTTP
                                ▼
                     ┌─────────────────────┐
                     │  External Internet  │
                     └─────────────────────┘
```

---

## 🚀 Key Highlights & Production Features

- **Distributed Task Scheduling with Atomic Leases:**
  - Tasks claimed by workers receive a 60-second in-flight lease stored in Redis (`crawl:leases`).
  - Master node's background reaper automatically reclaims tasks from abandoned or crashed workers back into `crawl:frontier`.
- **SSRF Hardened Outbound Network Dialer:**
  - Blocks loopback (`127.0.0.0/8`, `::1`), private RFC1918 subnets (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`), and cloud metadata (`169.254.169.254`).
  - Strict redirect policy (max 5 hops) verifying IP addresses at every successive hop against DNS rebinding.
  - Streaming payload limit enforced at 10 Megabytes via `io.LimitReader`.
- **Relational PostgreSQL Persistence & Ranked Search:**
  - Automatic idempotent database migrations on startup.
  - Documents indexed with native PostgreSQL `tsvector` and GIN index for millisecond keyword and phrase queries.
  - Tracks canonical URLs, title, meta description, H1 heading, HTTP status code, latency, content length, and internal/external hyperlinks.
- **Worker Node Fleet Management:**
  - Workers self-register on startup and maintain a 5-second background heartbeat loop.
  - Dynamic horizontal scaling (`docker compose up -d --scale worker=5`).
- **Tiered Data Governance & Reset Safeguards:**
  - **Level A (UI):** Clears search filters and browser cache.
  - **Level B (Queue):** Flushes Redis frontier queue and active leases.
  - **Level C (Data):** Purges crawled pages and domain statistics.
  - **Level D (Production):** Wipes database, Redis, and restarts with starter seed. Guarded by mandatory `confirmation_phrase: "WIPE ALL DATA"`.

---

## 🖥️ 10 Platform Views (UI Console)

1. **Overview:** High-level telemetry cards (Pages, Domains, Queue, Workers), live dispatches, worker fleet snapshot, and uptime monitor.
2. **Crawl Explorer:** Seed injector with preset pills (Go.dev, Hacker News, Wikipedia, GitHub), depth control, page ceiling, and SSRF feedback.
3. **Jobs Manager:** Real-time job registry tracking max depth, pages crawled, timestamps, and Pause/Resume/Cancel controls.
4. **Search Index:** Ranked full-text search with HTTP status filters (200 OK, 403 Forbidden, 5xx Errors) and interactive Page Inspector modal.
5. **Domains:** Discovered domain distribution bar charts with page counts and average latency.
6. **Worker Fleet:** Node status cards reporting heartbeats, completed/failed tasks, current active URL, and average latency.
7. **Activity Logs:** High-density terminal stream of crawl dispatches, lease expirations, and worker lifecycle events.
8. **Analytics:** Performance dashboard displaying average crawl latency, success ratios, and lease recovery statistics.
9. **Settings & Resets:** Backend API endpoint switcher (`/api` or `http://localhost:8080`) and tiered data reset triggers.
10. **Documentation:** Built-in system architecture and REST API endpoint reference.

---

## ⚡ Quick Start

### Option 1: Full Distributed Cluster (Docker Compose)

```bash
# 1. Clone repository
git clone https://github.com/Pushpit1/distributed-crawler.git
cd distributed-crawler

# 2. Launch master, workers, Redis, and PostgreSQL
docker compose up -d --build

# 3. Scale worker fleet
docker compose up -d --scale worker=3

# 4. Open dashboard in browser
open http://localhost:8080
```

### Option 2: Local Development (Native Go)

```bash
# 1. Start dependencies
docker compose up -d redis postgres

# 2. Run master coordinator
go run cmd/master/main.go

# 3. In another terminal, run worker
go run cmd/worker/main.go
```

### Option 3: Serverless Preview (Vercel)

```bash
# Run serverless test suite
npm test

# Deploy to Vercel
vercel --prod
```

---

## 🧪 Testing & Quality Assurance

Run the comprehensive unit and integration test suite across all subsystems:

```bash
# Test all Go packages
go test -v ./...

# Vet codebase
go vet ./...

# Test serverless preview engine
npm test
```

---

## 📚 Technical Documentation

- [System Architecture](docs/ARCHITECTURE.md)
- [REST API Specification](docs/API.md)
- [Deployment Guide](docs/DEPLOYMENT.md)
- [Operational Runbook](docs/OPERATIONS.md)
- [Security Specifications](docs/SECURITY.md)
- [Project Audit & Findings](docs/PROJECT_AUDIT.md)
- [Implementation Roadmap](docs/IMPLEMENTATION_ROADMAP.md)

---

## 📄 License
MIT License. Engineered for production-grade reliability and high-throughput web discovery.
