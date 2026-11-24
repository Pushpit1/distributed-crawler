# GOPHER CRAWLER — PRODUCTION DEPLOYMENT GUIDE

Gopher Crawler supports two robust deployment topologies depending on operational requirements:
1. **Mode A: Full Distributed Cluster** (Production-grade Go Coordinator + Distributed Go Workers + Redis + PostgreSQL).
2. **Mode B: Serverless Preview** (Vercel Serverless Functions + Upstash Redis + Neon/Supabase PostgreSQL + Static CDN Console).

---

## 1. Mode A: Full Distributed Cluster (Docker / Kubernetes / VMs)

This is the recommended deployment architecture for large-scale, high-throughput crawling.

### 1.1 Prerequisites
- Docker & Docker Compose v2+
- 2+ CPU Cores, 4GB+ RAM

### 1.2 Environment Variables Configuration
Create a `.env` file from `.env.example`:
```bash
# Server Configuration
PORT=8080
MASTER_URL=http://master:8080

# Database Configuration (PostgreSQL)
DB_HOST=postgres
DB_PORT=5432
DB_USER=postgres
DB_PASSWORD=postgres
DB_NAME=crawler_db
DB_SSLMODE=disable

# Redis Frontier Queue
REDIS_ADDR=redis:6379
REDIS_PASSWORD=

# Crawler Politeness & Performance
DEFAULT_POLITENESS_MS=1000
MAX_CRAWL_DEPTH=3
MAX_PAGES_PER_JOB=500
TASK_LEASE_TIMEOUT_SEC=60
```

### 1.3 Launching with Docker Compose
The included `docker-compose.yml` launches:
- 1x Coordinator Master (`cmd/master`)
- 2x Autonomous Worker Daemons (`cmd/worker`)
- 1x Redis instance (Port 6379)
- 1x PostgreSQL instance (Port 5433 host / 5432 container)

Run:
```bash
docker compose up -d --build
```

Verify service health:
```bash
docker compose ps
curl http://localhost:8080/health
curl http://localhost:8080/ready
```

### 1.4 Horizontal Worker Scaling
Scale the worker fleet dynamically without restarting the coordinator:
```bash
docker compose up -d --scale worker=5
```
Each worker automatically self-registers with the coordinator and begins processing frontier leases.

---

## 2. Mode B: Vercel Serverless Preview Deployment

For serverless deployments and cloud preview testing without running dedicated container hosts.

### 2.1 Architecture
- **Frontend Console:** Static HTML/CSS/JS served directly via Vercel Edge CDN (`/public`).
- **Serverless API Routes:** Node.js serverless functions under `/api/*` handling dispatches, searches, and resets.
- **External Redis:** Upstash Redis (Serverless).
- **External PostgreSQL:** Neon, Supabase, or AWS RDS.

### 2.2 Vercel Configuration (`vercel.json`)
```json
{
  "version": 2,
  "rewrites": [
    { "source": "/api/(.*)", "destination": "/api/$1" },
    { "source": "/health", "destination": "/api/health" },
    { "source": "/ready", "destination": "/api/ready" },
    { "source": "/status", "destination": "/api/status" },
    { "source": "/search", "destination": "/api/search" },
    { "source": "/submit", "destination": "/api/submit" },
    { "source": "/reset", "destination": "/api/reset" },
    { "source": "/(.*)", "destination": "/$1" }
  ]
}
```

### 2.3 Deploying via Vercel CLI
```bash
npm install -g vercel
vercel login
vercel --prod
```

Configure Environment Variables in Vercel Project Settings:
- `OPERATING_MODE`: `preview`
- `DATABASE_URL`: `postgres://user:pass@ep-cool-db.us-east-2.aws.neon.tech/crawler_db?sslmode=require`
- `REDIS_URL`: `rediss://default:token@cool-redis.upstash.io:6379`

---

## 3. Health Checks & Monitoring

Both deployment modes expose standard health probes:
- `GET /health`: Liveness probe for load balancers.
- `GET /ready`: Readiness check verifying database and Redis ping before routing traffic.
- `GET /api/metrics`: Prometheus scraping endpoint.
