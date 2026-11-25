# GOPHER CRAWLER — OPERATIONAL RUNBOOK

## 1. Health & Readiness Checks

```bash
curl -s http://localhost:8080/health | jq .
curl -s http://localhost:8080/ready | jq .
curl -s http://localhost:8080/api/metrics
```
`/ready` returns HTTP `200` when both PostgreSQL and Redis respond within 2s, or `503` if either store is degraded.

---

## 2. Task Lease Recovery & Worker Stale Detection

- **Leases:** `GetPoliteStructuredTask` writes an atomic JSON lease record to Redis hash `crawler:queue:leases`.
- **Reaper Sweep:** Coordinator runs a background reaper every 2s. Any lease exceeding `TASK_LEASE_TIMEOUT_SEC` (default `60s`) is pushed back onto `crawler:queue`, and a `LEASE_RECLAIMED` event is persisted to PostgreSQL.
- **Stale Workers:** Workers send heartbeats every 5s. Missing >15s transitions worker state to `UNHEALTHY` (`WORKER_STALE` event).
- **Graceful Shutdown:** Sending `SIGINT` or `SIGTERM` to `cmd/worker` drains active slots and emits a final `STOPPED` heartbeat.

---

## 3. Granular Safe Resets

| Tier | API Payload | Scope & Effect | Auth / Guard |
|------|-------------|----------------|--------------|
| 1. UI | `{"level":"ui"}` | Clears browser filters only | None |
| 2. Cancel Job | `{"level":"job","job_id":"..."}` | Marks job `CANCELLED` & discards tasks | Optional token |
| 3. Flush Queue | `{"level":"queue"}` | Flushes pending queue & leases; keeps indexed pages | `ADMIN_API_TOKEN` |
| 4. Purge Job | `{"level":"job_data","job_id":"..."}` | Deletes selected job + its pages/errors | `ADMIN_API_TOKEN` |
| 5. Purge Data | `{"level":"data"}` | Clears crawled pages/domains; keeps jobs/workers | `ADMIN_API_TOKEN` |
| 6. Dev Reset | `{"level":"dev"}` | Clears queue, jobs, pages, events | `ADMIN_API_TOKEN` |
| 7. Production | `{"level":"production","confirmation_phrase":"WIPE ALL DATA"}` | Full wipe | `ENABLE_PRODUCTION_RESET=true` + Token + Phrase |

---

## 4. Database Backup & Upgrade Strategy

Migrations live in `pkg/db/migrations/001_initial_schema.sql` using idempotent `CREATE TABLE IF NOT EXISTS`, `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`, and advisory locking (`pg_advisory_lock`). Existing data is never dropped during upgrades.

```bash
# Logical PostgreSQL backup
pg_dump "$DATABASE_URL" > crawler_backup_$(date +%F).sql
```
