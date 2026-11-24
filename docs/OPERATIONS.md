# GOPHER CRAWLER — OPERATIONAL RUNBOOK

## 1. Routine Operations & Health Verification

### 1.1 Cluster Health Check
Execute the readiness probe to inspect database and Redis connectivity:
```bash
curl -s http://localhost:8080/ready | jq .
```
Expected output:
```json
{
  "status": "ready",
  "database": "connected",
  "redis": "connected",
  "time": "2026-11-24T15:30:00Z"
}
```

### 1.2 Inspect Active Leases
Check how many task leases are currently in flight:
```bash
curl -s http://localhost:8080/api/status | jq '.leases_active'
```

---

## 2. Distributed Task Lease Reclamation

### 2.1 How Task Leases Work
When a worker claims a URL from the frontier queue, it acquires a 60-second lease stored in Redis under the `crawl:leases` hash. If the worker encounters a panic, network outage, or hardware failure before calling `AckTask`:
- The Coordinator's background reaper detects leases older than 60 seconds during its 10-second inspection sweep.
- Expired tasks are deleted from `crawl:leases` and pushed back to the head of `crawl:frontier`.
- An event of type `LEASE_EXPIRED` is logged to `crawl_events`.

### 2.2 Manual Lease Inspection
To inspect in-flight leases in Redis directly:
```bash
docker exec -it distributed-crawler-redis redis-cli HGETALL crawl:leases
```

To manually purge stale leases without restarting:
```bash
docker exec -it distributed-crawler-redis redis-cli DEL crawl:leases
```

---

## 3. Worker Node Health & Stale Worker Pruning

### 3.1 Heartbeat Protocol
Workers send a JSON heartbeat every 5 seconds to `/api/heartbeat`. The coordinator updates `worker_registry.last_heartbeat`.

### 3.2 Automated Pruning
The Coordinator background reaper checks for stale workers every 10 seconds:
- If `now - last_heartbeat > 15s`: The worker status is transitioned from `ACTIVE` to `DEAD`.
- An event of type `WORKER_STALE` is logged.

To list currently registered workers:
```bash
curl -s http://localhost:8080/api/workers | jq .
```

---

## 4. Tiered Reset Procedures

Gopher Crawler provides four operational reset tiers:

| Tier | Scope | Command / API Payload | Confirmation Required |
|------|-------|-----------------------|-----------------------|
| **Level A** | UI Search Filters & Cache | UI Button: "CLEAR UI" | No |
| **Level B** | Redis Frontier Queue | `POST /api/reset` `{"level":"queue"}` | No |
| **Level C** | Crawled Pages & Domain Stats | `POST /api/reset` `{"level":"data"}` | Confirmation Dialog |
| **Level D** | Full Cluster Production Wipe | `POST /api/reset` `{"level":"production","confirmation_phrase":"WIPE ALL DATA"}` | Explicit Phrase Required |

### Production Wipe via cURL
```bash
curl -X POST http://localhost:8080/api/reset \
  -H "Content-Type: application/json" \
  -d '{"level":"production","confirmation_phrase":"WIPE ALL DATA"}'
```

---

## 5. Troubleshooting Common Incidents

### Incident 1: Frontier Queue Grows Unbounded
- **Cause:** Discovered links exceed worker crawl capacity.
- **Remedy:** Scale up workers:
  ```bash
  docker compose up -d --scale worker=5
  ```
- **Alternative:** Lower `max_depth` in job submissions.

### Incident 2: High Error Ratio / HTTP 429 (Too Many Requests)
- **Cause:** Politeness delay is too aggressive for target servers.
- **Remedy:** Increase `politeness_delay_ms` (e.g. from 1000ms to 3000ms).

### Incident 3: Target Server Blocked by SSRF Firewall
- **Symptoms:** Error logs reporting `SSRF safety validation failed: target IP is private or loopback`.
- **Explanation:** The crawler is engineered to protect against accessing private RFC 1918 networks (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`) and cloud metadata (`169.254.169.254`).
- **Remedy:** Verify target domain resolves to a valid public IP. For internal development and mock tests, use the unit testing suite where `SetAllowPrivateSubnets(true)` is explicitly authorized.
