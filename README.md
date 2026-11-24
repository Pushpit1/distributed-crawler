# GOPHER CRAWLER ⚡

A high-performance distributed web crawler and live telemetry console written in **Go**, powered by **Redis** (FIFO Frontier queue & deduplication) and **PostgreSQL** (relational page index & unique host registry), with a production-grade **Vercel Serverless Edge console**.

Inspired by the precision brutalist aesthetics of [Bonanza Studios](https://www.bonanza-studios.com) and [Descycle](https://www.descycle.com) — zero AI slop, high-density telemetry, real-time worker node heartbeats, and live search.

---

## 🏛️ System Architecture

```
                  ┌──────────────────────────────────────────────┐
                  │      Vercel Serverless Edge Console         │
                  │  (Index Explorer • Seed Injector • Telemetry)│
                  └──────────────────────┬───────────────────────┘
                                         │ /api/*
                                         ▼
                  ┌──────────────────────────────────────────────┐
                  │          Go Coordinator Master Node          │
                  │   (Task Scheduling • Politeness • Routing)   │
                  └──────────────┬────────────────┬──────────────┘
                                 │                │
                 Atomic Push/Pop │                │ Persist Pages
                                 ▼                ▼
                     ┌────────────────┐     ┌────────────────┐
                     │  Redis Frontier│     │   PostgreSQL   │
                     │  (FIFO Queue & │     │ (Page Index &  │
                     │  Visited Set)  │     │ Domain Registry│
                     └────────────────┘     └────────────────┘
                               ▲
                               │ Poll / Report Tasks
                     ┌─────────┴─────────┐
                     │                   │
             ┌───────────────┐   ┌───────────────┐
             │ Crawler Node  │   │ Crawler Node  │
             │   Worker #1   │   │   Worker #2   │
             │(Robots.txt &  │   │(Robots.txt &  │
             │ HTML Parsing) │   │ HTML Parsing) │
             └───────────────┘   └───────────────┘
```

### Core Components

1. **Coordinator Master (`cmd/master`)**:
   - Manages distributed task distribution, polite per-domain rate limiting, and worker heartbeats.
   - REST API exposes `/api/status`, `/api/search`, `/api/submit`, `/api/reset`, and `/api/health`.
2. **Worker Fleet Daemon (`cmd/worker`)**:
   - Autonomous workers that poll the coordinator for tasks, check `robots.txt` compliance, fetch web pages with exponential backoff, extract links and metadata, and report structured findings back to the master.
3. **URL Frontier (`pkg/crawler/frontier.go`)**:
   - Backed by Redis `SADD` (atomic deduplication visited set) and `LPUSH`/`RPOP` (FIFO queue).
   - Domain politeness delay prevents hammering hosts.
4. **Relational Index (`pkg/db/postgres.go`)**:
   - Normalizes domains and crawled pages with atomic upserts (`ON CONFLICT DO UPDATE`).
   - Keyword search across page titles, meta descriptions, and URLs with SQL `ILIKE`.
5. **Vercel Serverless Edge Console (`api/` + `public/`)**:
   - Deploys natively to Vercel with zero infrastructure required.
   - Features built-in serverless crawl engine (Node.js 18+) that actively fetches target websites, parses HTML, and registers pages in real-time, or proxies to a live Go cluster when configured with `CRAWLER_BACKEND_URL`.

---

## 🚀 Live Vercel Deployment

Deploy the dashboard and serverless crawler directly to **Vercel**:

### Option 1: Via Vercel CLI
```bash
# 1. Install Vercel CLI (if not already installed)
npm install -g vercel

# 2. Deploy directly from repository root
vercel --prod
```

### Option 2: Connect via GitHub
1. Push this repository to GitHub: `https://github.com/Pushpit1/distributed-crawler`.
2. Go to [Vercel Dashboard](https://vercel.com/new).
3. Import your `distributed-crawler` repository.
4. Click **Deploy**. Vercel will automatically recognize the static assets in `public/` and serverless functions in `api/`.

*(Optional: Set the `CRAWLER_BACKEND_URL` environment variable in Vercel to point to your hosted Go Coordinator master node to bridge the Vercel dashboard to your distributed Docker fleet!)*

---

## 🛠️ Local Development & Docker Compose

Run the entire distributed cluster (Master, 2 Workers, Redis, and PostgreSQL) with one command:

```bash
docker-compose up --build
```

### Accessing the Local Services
- **Web Dashboard**: [http://localhost:8080](http://localhost:8080)
- **Coordinator API**: [http://localhost:8080/api/status](http://localhost:8080/api/status)
- **PostgreSQL**: `localhost:5433` (user: `crawler_user`, db: `crawler_db`)
- **Redis**: `localhost:6379`

---

## 🧪 Testing

### Go Integration & Unit Tests
Run the Go test suite:
```bash
go test -v ./...
```

### Vercel Serverless Engine Tests
Test the Vercel serverless crawler engine:
```bash
npm test
```

---

## 📡 API Reference

All endpoints support uniform CORS and can be consumed by external applications or frontend dashboards:

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/api/status` | Returns queue size, visited count, unique domains, active workers, uptime, and recent crawl feed. |
| `GET` | `/api/search?q={query}` | Searches indexed pages by title, description, or URL. Empty query returns latest indexed pages. |
| `POST` | `/api/submit` | Enqueues seed URLs into the frontier queue. Payload: `{"urls": ["https://go.dev"]}`. |
| `POST` | `/api/crawl` | Dispatches an immediate crawl step on the next queued URL. |
| `POST` | `/api/reset` | Flushes Redis queue and visited keys, truncates database tables, and re-seeds default start page. |
| `GET` | `/api/health` | Health check endpoint returning cluster uptime and status. |

---

## 📄 License
MIT License. Built for high performance, reliability, and precision telemetry.
