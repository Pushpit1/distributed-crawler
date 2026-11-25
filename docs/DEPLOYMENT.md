# GOPHER CRAWLER — PRODUCTION DEPLOYMENT GUIDE

Gopher Crawler supports two clearly distinct deployment topologies:
1. **Mode A: Full Distributed Cluster** (Go Coordinator Master + Independent Go Workers + Redis + PostgreSQL).
2. **Mode B: Serverless Preview / Proxy** (Vercel CDN + Node Serverless API).

---

## 1. Mode A: Full Distributed Cluster (Docker / Render / Fly.io / VMs)

### 1.1 Environment Configuration
Copy `.env.example` to `.env`:
```bash
PORT=8080
MASTER_PORT=8080
POSTGRES_PORT=5433
REDIS_PORT=6379
DATABASE_URL=postgres://crawler_user:crawler_password@postgres:5432/crawler_db?sslmode=disable
REDIS_ADDR=redis:6379
POLITENESS_MS=1000
WORKER_CONCURRENCY=4
TASK_LEASE_TIMEOUT_SEC=60

# Security & Admin Controls (set strong token in production)
ADMIN_API_TOKEN=
ENABLE_PRODUCTION_RESET=false
CORS_ALLOWED_ORIGINS=
RATE_LIMIT_RPM=180
```
*Note: If ports `8080`, `5433`, or `6379` are occupied on your host machine, override `MASTER_PORT`, `POSTGRES_PORT`, and `REDIS_PORT` in `.env`.*

### 1.2 Running Core Cluster with Docker Compose
```bash
docker compose up -d --build
# Scale to multiple independent worker daemons
docker compose up -d --scale worker=3
```

### 1.3 Running Cluster + Prometheus & Grafana Monitoring Stack
```bash
docker compose --profile monitoring up -d --build
# Prometheus: http://localhost:9091 (scrapes master:8080/api/metrics)
# Grafana:    http://localhost:3002 (import deploy/grafana/dashboard.json)
```

### 1.4 Native Windows / Linux Development Workflow
```powershell
# 1. Start Redis & PostgreSQL
docker compose up -d redis postgres

# 2. Run Coordinator Master
go run ./cmd/master

# 3. Start one or more Worker Daemons
go run ./cmd/worker -master http://localhost:8080 -concurrency 4
```

### 1.5 Persistent Coordinator Hosting (Fly.io / Render / Railway / AWS ECS)
Deploy `Dockerfile.master` and `Dockerfile.worker` against managed PostgreSQL (Neon / Supabase / RDS) and managed Redis (Upstash / Elasticache / Redis Cloud), passing `DATABASE_URL` and `REDIS_ADDR` via environment variables. Point Vercel's `CRAWLER_BACKEND_URL` environment variable to the Coordinator HTTPS URL.

---

## 2. Mode B: Vercel Frontend & Serverless Deployment

- **Frontend Assets:** Served statically from `/public` (kept synced with `/web` via `npm run build`).
- **API Proxy Mode (Recommended with Vercel):** Set `CRAWLER_BACKEND_URL=https://your-coordinator-domain.com` in Vercel project settings. All `/api/*` endpoints proxy directly to the live Go Coordinator with `X-Admin-Token` and `Authorization` forwarding.
- **Serverless Preview Mode (Standalone):** When `CRAWLER_BACKEND_URL` is unset, `/api/*` executes bounded in-memory crawls on demand and explicitly tags UI status as `MODE B: SERVERLESS PREVIEW` with `0` persistent workers (never fabricating cluster workers).

```bash
npm install -g vercel
vercel --prod
```
