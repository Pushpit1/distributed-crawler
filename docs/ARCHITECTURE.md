# GOPHER CRAWLER — DISTRIBUTED SYSTEM ARCHITECTURE

## 1. High-Level System Overview

Gopher Crawler is a production-inspired, concurrent, distributed web crawling and full-text search indexing platform engineered in Go. The system provides high-throughput discovery, respectful politeness enforcement, distributed task scheduling with atomic visibility leases, SSRF-hardened network fetching, and ranked relational indexing.

```
                                  +---------------------------------------+
                                  |     CLIENTS & OBSERVABILITY UI        |
                                  |  (Desktop / Mobile / Telemetry API)   |
                                  +-------------------+-------------------+
                                                      |
                                                      v  HTTP / REST
                                  +---------------------------------------+
                                  |       COORDINATOR MASTER NODE         |
                                  |  - Job Lifecycle Management           |
                                  |  - Domain Politeness Delays           |
                                  |  - Task Lease Reaper (60s Timeout)    |
                                  |  - Full-Text Search Query Engine      |
                                  |  - Tiered Data Governance Controller  |
                                  +---------+-------------------+---------+
                                            |                   |
                     Task Leases & Pop      |                   | Full-Text Search
                     Frontier Queues        v                   v
            +---------------------------------+       +---------------------------------+
            |     REDIS IN-MEMORY STORE       |       |     POSTGRESQL RELATIONAL DB    |
            | - FIFO Frontier (crawl:frontier)|       | - Crawled Pages (with GIN tsv)  |
            | - Visited Dedup (crawl:visited) |       | - Domain Registry & Aggregates  |
            | - Active Leases (crawl:leases)  |       | - Crawl Jobs & Seed Queues      |
            | - Politeness Keys (domain:last) |       | - Auditable Activity Event Log  |
            +----------------+----------------+       | - Worker Node Registry          |
                             ^                        +----------------+----------------+
                             |                                         ^
                             | Fetch Task & Ack                        | Save Page & Event
                             |                                         |
            +----------------+-----------------------------------------+----------------+
            |                          DISTRIBUTED WORKER FLEET                         |
            |                                                                           |
            |   +-----------------------+              +-----------------------+        |
            |   |     Worker Node 1     |              |     Worker Node N     |        |
            |   | - Autonomous Goroutines|              | - Autonomous Goroutines|        |
            |   | - 5s Heartbeat Loop   |              | - 5s Heartbeat Loop   |        |
            |   | - SSRF-Hardened Dialer|              | - SSRF-Hardened Dialer|        |
            |   | - Robots.txt Cache    |              | - Robots.txt Cache    |        |
            |   | - HTML Limiter (10MB) |              | - HTML Limiter (10MB) |        |
            |   +-----------+-----------+              +-----------+-----------+        |
            +---------------|--------------------------------------|--------------------+
                            |                                      |
                            +-------------------+------------------+
                                                |
                                                v  HTTP / HTTPS (SSRF Safe)
                                     +----------------------+
                                     |   EXTERNAL TARGETS   |
                                     |     (Internet)       |
                                     +----------------------+
```

---

## 2. Core Components

### 2.1 Coordinator (Master Node)
- **Role:** Central HTTP API endpoint and job supervisor.
- **Responsibilities:**
  - Ingests seed URLs via `/api/submit` and initiates crawl jobs with configurable depth and page quotas.
  - Exposes cluster health `/health`, readiness `/ready`, and real-time status `/api/status`.
  - Runs a background reaper routine every 10 seconds to detect expired worker leases (timeout: 60s) and reclaim abandoned tasks back to the frontier queue.
  - Monitors worker heartbeats and marks stale nodes inactive if no ping is received within 15 seconds.
  - Serves full-text search queries using PostgreSQL's native `tsvector` and `ts_rank`.

### 2.2 Redis Frontier Queue
- **Role:** High-throughput in-memory task broker and politeness buffer.
- **Key Schemas:**
  - `crawl:frontier` (List): FIFO queue of pending crawl task URLs.
  - `crawl:visited` (Set): Fast O(1) deduplication store preventing redundant fetches.
  - `crawl:leases` (Hash): In-flight task leases mapped to worker ID and timestamp.
  - `domain:last_crawl:<host>` (String): Timestamp of the last crawl dispatch to enforce polite delays.

### 2.3 Distributed Workers
- **Role:** Autonomous crawling daemons running concurrent worker goroutines.
- **Lifecycle:**
  1. On boot, registers with Master via `/api/workers/register` and begins a 5s background heartbeat loop.
  2. Claims polite task with an atomic visibility lease.
  3. Validates target host through `safe_dialer.go` to block SSRF, loopback, and private subnets.
  4. Parses target `robots.txt` and verifies crawl allowance.
  5. Performs bounded HTTP GET (10MB ceiling) with 10s socket timeout.
  6. Extracts title, meta description, H1 heading, canonical URL, and hyperlinks.
  7. Persists page to PostgreSQL and enqueues discovered links into Redis.
  8. Acknowledges task lease completion to Master.

### 2.4 PostgreSQL Relational Database
- **Role:** Persistent document store, analytics engine, and full-text search index.
- **Key Tables:**
  - `crawled_pages`: Complete document record with URL, canonical URL, status code, latency, headers, and GIN-indexed `tsv` column.
  - `domain_statistics`: Real-time rollup of traversed hosts, total pages, and average latency.
  - `crawl_jobs`: Execution lifecycle tracking for user-initiated crawls.
  - `worker_registry`: Registered worker fleet metadata and heartbeats.
  - `crawl_events`: Immutable audit trail for crawl dispatches, status codes, and lease reclaims.

---

## 3. Distributed Task Lease State Machine

To prevent task loss caused by worker crash or network timeout without introducing heavy distributed locking systems, Gopher Crawler implements an atomic lease mechanism:

```
           +---------------------------------------+
           |           Frontier Queue              |
           |          (crawl:frontier)             |
           +-------------------+-------------------+
                               |
                               | Worker claims task (RPOP)
                               v
           +---------------------------------------+
           |            In-Flight Lease            |
           |            (crawl:leases)             |
           +---------+-------------------+---------+
                     |                   |
    Worker succeeds  |                   | 60s Lease Expiry
    or safe failure  |                   | (Reaper triggers)
                     v                   v
           +-------------------+       +-------------------+
           |    Acknowledge    |       |  Reclaim Task &   |
           |  (Delete Lease)   |       | Re-enqueue to Q   |
           +-------------------+       +-------------------+
```

1. **Lease Allocation:** When a worker retrieves a URL from `crawl:frontier`, it atomically writes a lease record to `crawl:leases` storing the URL, claiming `worker_id`, and Unix timestamp.
2. **Execution Window:** The worker has a 60-second window to fetch, parse, and persist the document.
3. **Lease Acknowledgment:** Upon successful indexing, the worker issues an `AckTask` call, removing the entry from `crawl:leases`.
4. **Reclaim on Failure:** If a worker crashes or hangs, the coordinator's reaper scans `crawl:leases` during its 10-second tick. Any lease exceeding 60 seconds is automatically deleted from leases and prepended back into `crawl:frontier`.

---

## 4. SSRF Defense Architecture

Gopher Crawler enforces strict Server-Side Request Forgery defenses in `pkg/crawler/safe_dialer.go`:

1. **Host Resolution & IP Verification:**
   Before opening a TCP connection, DNS resolution resolves the target domain.
2. **Prohibited Subnets:**
   Any IP address matching the following ranges is rejected immediately with an HTTP 403 / network rejection:
   - `127.0.0.0/8` (IPv4 Loopback)
   - `::1` (IPv6 Loopback)
   - `10.0.0.0/8` (RFC 1918 Private)
   - `172.16.0.0/12` (RFC 1918 Private)
   - `192.168.0.0/16` (RFC 1918 Private)
   - `169.254.0.0/16` (Link-Local / Cloud Metadata e.g. AWS/GCP `169.254.169.254`)
   - `224.0.0.0/4` (Multicast)
3. **Safe Redirect Policy:**
   Redirects (HTTP 301, 302, 307, 308) are intercepted. Each successive hop is re-validated through the DNS resolver to prevent DNS rebinding or redirect chains pointing to internal infrastructure.
4. **Payload Limiting:**
   Responses are wrapped in an `io.LimitReader` with a 10 megabyte ceiling to prevent memory exhaustion from zip bombs or streaming payloads.
