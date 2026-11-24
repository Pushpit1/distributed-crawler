# GOPHER CRAWLER — SECURITY ARCHITECTURE

## 1. Threat Model & Design Principles

Web crawlers are uniquely exposed to remote network risks because they ingest untrusted URLs from external user input and follow untrusted hyperlinks found in crawled HTML.

Gopher Crawler is engineered around strict defense-in-depth principles:
1. **Never trust remote URLs:** Every seed and extracted hyperlink is sanitized and validated prior to network resolution.
2. **Never access internal network infrastructure:** Loopback, private enterprise subnets, link-local addresses, and cloud provider metadata services are strictly blocked.
3. **Never allow unbounded resource consumption:** HTTP request timeouts, response byte limits, and maximum crawl depths are strictly bounded.

---

## 2. Server-Side Request Forgery (SSRF) Defenses

All network requests dispatched by Gopher Crawler pass through `pkg/crawler/safe_dialer.go`:

### 2.1 Blocked IP Ranges
The custom net dialer resolves the hostname and validates the target IP against non-routable, private, and reserved subnets:

| Subnet | Specification | Threat Mitigated |
|--------|---------------|------------------|
| `127.0.0.0/8` | RFC 1122 Loopback | Localhost services (e.g. Postgres, Redis, Master API) |
| `::1` | RFC 4291 IPv6 Loopback | Localhost IPv6 services |
| `10.0.0.0/8` | RFC 1918 Private | Internal enterprise / VPC network traversal |
| `172.16.0.0/12` | RFC 1918 Private | Docker bridge networks and container interconnects |
| `192.168.0.0/16`| RFC 1918 Private | Local LAN services |
| `169.254.0.0/16`| RFC 3927 Link-Local | Cloud metadata services (`http://169.254.169.254/latest/meta-data/`) |
| `224.0.0.0/4` | RFC 5771 Multicast | Multicast amplification attacks |

### 2.2 DNS Rebinding & Safe Redirect Policy
Attackers frequently attempt to bypass initial hostname checks by responding with a public IP on the first DNS lookup, followed by a 302 redirect pointing to an internal IP.

Gopher Crawler neutralizes this by enforcing `SafeRedirectPolicy`:
- Every redirect hop (HTTP 301, 302, 303, 307, 308) re-evaluates the destination URL through `ValidateTargetURL`.
- Maximum redirect hops are capped at 5.
- The underlying TCP connection is re-dialed using the safe dialer, ensuring DNS rebinding cannot redirect an established connection to internal addresses.

---

## 3. Resource Exhaustion & Payload Limiting

### 3.1 Bounded Stream Reader
Remote servers could return infinite streams or multi-gigabyte files (decompression bombs) to exhaust memory.
- All HTTP response bodies are wrapped in `io.LimitReader(resp.Body, 10 * 1024 * 1024)` (10 Megabyte limit).
- HTML parsing is terminated immediately if the response exceeds this threshold.

### 3.2 Timeouts
- Network socket connect timeout: 5 seconds.
- Overall HTTP client timeout: 10 seconds.
- Idle connection timeout: 15 seconds.

---

## 4. Database Security & Injection Mitigation

- **Parameterized Queries:** All PostgreSQL queries in `pkg/db/postgres.go` utilize parameterized placeholders (`$1`, `$2`, etc.). Zero raw string concatenations are permitted.
- **Full-Text Search Sanitization:** Full-text queries pass through `plainto_tsquery('english', $1)` ensuring arbitrary boolean injection cannot corrupt search indexes.

---

## 5. Administrative & Reset Safeguards

Accidental or unauthorized data wipes in production environments are prevented via multi-tier confirmation barriers:
- Queue and page purges require explicit request scopes.
- Full production wipes (`level: "production"`) reject any payload that does not provide `confirmation_phrase: "WIPE ALL DATA"` with an HTTP 403 Forbidden response.
