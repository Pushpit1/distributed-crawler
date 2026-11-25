# GOPHER CRAWLER — SECURITY ARCHITECTURE

## 1. Defense-in-Depth Threat Model

Because Gopher Crawler accepts arbitrary user-supplied seed URLs and traverses untrusted outbound hyperlinks, SSRF, resource exhaustion, and administrative access control are treated as first-order security boundaries.

---

## 2. Outbound SSRF & DNS Rebinding Protections

All outbound network fetches (`Parser.FetchAndParse`, `RobotsCache.IsAllowed`, `api/_engine.js`) enforce:

1. **Scheme, Credential & Length Enforcement:**
   - Only `http://` and `https://` permitted (`file://`, `ftp://`, `javascript:`, `data:` rejected).
   - Embedded URL credentials (`http://user:pass@host`) rejected.
   - Maximum URL length capped at `2048` characters.
2. **Forbidden Destination Ranges (`pkg/crawler/safe_dialer.go` & `api/_engine.js`):**
   - IPv4 Loopback (`127.0.0.0/8`), IPv6 Loopback (`::1`), Unspecified (`0.0.0.0/8`, `::`)
   - RFC 1918 Private (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`)
   - Link-Local & Cloud Provider Metadata (`169.254.0.0/16` including `169.254.169.254`, `fe80::/10`)
   - IPv4-mapped IPv6 addresses normalized via `ip.To4()` (blocking `::ffff:127.0.0.1`, `::ffff:169.254.169.254`)
   - IPv6 ULA (`fc00::/7`), CGNAT (`100.64.0.0/10`), Teredo (`2001::/32`), 6to4 (`2002::/16`), Multicast (`224.0.0.0/4`, `ff00::/8`)
   - Restricted hostnames: `localhost`, `*.localhost`, `*.local`, `*.internal`, `*.localdomain`, `*.arpa`
3. **DNS Rebinding Mitigation (`NewSSRFSafeTransport`):**
   - Coordinator validates all submitted seeds before queuing to Redis (`HandleSubmitSeeds`).
   - Both `Parser` and `RobotsCache` resolve hostnames inside `DialContext`, verify every resolved IP is public, and dial directly to the verified IP rather than re-resolving DNS cleanly preventing TOCTOU/DNS rebinding.
4. **Redirect Hop Re-Validation (`SafeRedirectPolicy`):**
   - Redirect chains are capped at 5 hops; every redirect target re-runs scheme, credential, and host/IP resolution checks.

---

## 3. Resource Exhaustion, XSS & Injection Defenses

- **Decompression Bomb & Body Caps:** Go response bodies wrapped in `io.LimitReader(..., 10MB)` (`robots.txt` capped at `512KB`; Vercel preview capped at `5MB`).
- **SQL Injection Prevention:** All PostgreSQL queries use parameterized statements (`$1..$N`) and `plainto_tsquery`.
- **Schema Migration Locking:** Concurrent cluster startups acquire PostgreSQL advisory lock `78493021` to prevent catalog races.
- **Log Injection Mitigation:** All dynamic URLs/hostnames logged pass through `SanitizeForLog()` stripping `\r`, `\n`, and control characters.
- **UI XSS Prevention:** Frontend escapes every field (`title`, `url`, `description`, `h1`, log payloads) via `escapeHtml()`.

---

## 4. API & Administrative Controls

- **Admin Bearer / Header Authorization:** When `ADMIN_API_TOKEN` is configured, destructive resets (`queue`, `job_data`, `data`, `dev`, `production`), job cancellation (`/cancel`), and runtime config updates require `X-Admin-Token` or `Authorization: Bearer <token>`.
- **Production Reset Lockout:** `level: "production"` is disabled by default (`ENABLE_PRODUCTION_RESET=false`) AND requires typing `"WIPE ALL DATA"`.
- **Request Hardening:** Security headers (`X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`), Request Correlation IDs (`X-Request-ID`), configurable `CORS_ALLOWED_ORIGINS`, `2MB` request body ceiling, and per-IP sliding-window API rate limiting (`RATE_LIMIT_RPM`).
- **Container Hardening:** `Dockerfile.master` and `Dockerfile.worker` compile statically (`CGO_ENABLED=0`) and drop privileges to non-root user `crawler` (`UID 10001`).
