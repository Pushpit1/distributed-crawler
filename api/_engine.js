// api/_engine.js - Hardened Serverless Preview Engine & Coordinator Proxy
// Explicitly identifies "serverless_preview" vs "proxy" (Mode A Go Cluster) — never fakes distributed workers.

const dns = require("dns");
const net = require("net");

const BACKEND_URL = (process.env.CRAWLER_BACKEND_URL || process.env.GO_MASTER_URL || "").replace(/\/+$/, "");
const ADMIN_TOKEN = process.env.ADMIN_API_TOKEN || "";
const ENABLE_PROD_RESET = String(process.env.ENABLE_PRODUCTION_RESET || "").toLowerCase() === "true";
const USER_AGENT = "DistributedCrawler/2.0 (+https://github.com/Pushpit1/distributed-crawler)";
const MAX_URL_LEN = 2048;
const MAX_BODY_BYTES = 5 * 1024 * 1024; // 5 MB serverless safety cap

// Check whether an IPv4 or IPv6 address belongs to loopback, RFC1918, link-local, metadata, or reserved space
function isPrivateOrReservedIP(ipRaw) {
    if (!ipRaw) return true;
    let ip = ipRaw.trim().toLowerCase();

    // Unwrap IPv4-mapped IPv6 (e.g. ::ffff:127.0.0.1 or ::ffff:7f00:1)
    if (ip.startsWith("::ffff:")) {
        const mapped = ip.slice(7);
        if (net.isIPv4(mapped)) {
            ip = mapped;
        }
    }

    if (net.isIPv4(ip)) {
        const parts = ip.split(".").map(Number);
        const [a, b] = parts;
        if (a === 0 || a === 10 || a === 127) return true;
        if (a === 169 && b === 254) return true; // Link-local / Cloud metadata
        if (a === 172 && b >= 16 && b <= 31) return true; // RFC1918
        if (a === 192 && b === 168) return true; // RFC1918
        if (a === 100 && b >= 64 && b <= 127) return true; // CGNAT
        if (a >= 224) return true; // Multicast & Reserved
        if (a === 192 && b === 0 && (parts[2] === 0 || parts[2] === 2)) return true;
        if (a === 198 && (b === 18 || b === 19)) return true;
        if (a === 198 && b === 51 && parts[2] === 100) return true;
        if (a === 203 && b === 0 && parts[2] === 113) return true;
        return false;
    }

    if (net.isIPv6(ip)) {
        if (ip === "::" || ip === "::1") return true;
        if (ip.startsWith("fe80:") || ip.startsWith("fc") || ip.startsWith("fd") || ip.startsWith("ff")) return true;
        if (ip.startsWith("2001:0:") || ip.startsWith("2002:")) return true;
        return false;
    }

    return true;
}

async function validateTargetURLAsync(rawUrl) {
    const trimmed = String(rawUrl || "").trim();
    if (!trimmed || trimmed.length > MAX_URL_LEN) {
        throw new Error(`Invalid URL length (must be 1-${MAX_URL_LEN} chars)`);
    }

    let parsed;
    try {
        parsed = new URL(trimmed);
    } catch (e) {
        throw new Error(`Invalid URL syntax: ${e.message}`);
    }

    if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
        throw new Error(`Unsupported protocol ${parsed.protocol}; only http/https allowed`);
    }
    if (parsed.username || parsed.password) {
        throw new Error("Embedded URL credentials are prohibited");
    }

    const hostname = parsed.hostname.toLowerCase();
    if (!hostname || hostname === "localhost" || hostname.endsWith(".localhost") || hostname.endsWith(".local") || hostname.endsWith(".internal")) {
        throw new Error(`SSRF Blocked: internal or reserved hostname "${hostname}"`);
    }

    if (net.isIP(hostname)) {
        if (isPrivateOrReservedIP(hostname)) {
            throw new Error(`SSRF Blocked: private/reserved IP "${hostname}"`);
        }
        return parsed.href;
    }

    // Resolve DNS and inspect every returned IP to guard against internal DNS records
    const records = await dns.promises.lookup(hostname, { all: true });
    if (!records || records.length === 0) {
        throw new Error(`DNS resolution returned no addresses for "${hostname}"`);
    }
    for (const rec of records) {
        if (isPrivateOrReservedIP(rec.address)) {
            throw new Error(`SSRF Blocked: "${hostname}" resolves to forbidden IP "${rec.address}"`);
        }
    }
    return parsed.href;
}

function matchesPolicy(rawUrl, allowedDomains = [], excludePatterns = []) {
    try {
        const host = new URL(rawUrl).hostname.toLowerCase();
        if (Array.isArray(allowedDomains) && allowedDomains.length > 0) {
            const ok = allowedDomains.some(d => {
                const clean = String(d || "").trim().toLowerCase();
                return clean && (host === clean || host.endsWith("." + clean));
            });
            if (!ok) return false;
        }
        if (Array.isArray(excludePatterns) && excludePatterns.length > 0) {
            for (const pat of excludePatterns) {
                const clean = String(pat || "").trim();
                if (!clean) continue;
                try {
                    if (new RegExp(clean).test(rawUrl)) return false;
                } catch {
                    if (rawUrl.toLowerCase().includes(clean.toLowerCase())) return false;
                }
            }
        }
        return true;
    } catch {
        return false;
    }
}

class CrawlerEngine {
    constructor() {
        this.config = {
            default_politeness_ms: "1000",
            default_max_depth: "3",
            default_max_pages: "50",
            default_concurrency: "2",
            lease_timeout_sec: "60"
        };
        this.reset("dev");
    }

    reset(level = "dev", confirmationPhrase = "", jobId = "") {
        const cleanLevel = String(level || "dev").toLowerCase();

        if (cleanLevel === "ui") {
            return { status: "success", level: "ui", message: "UI local view state cleared" };
        }

        if (cleanLevel === "production") {
            if (!ENABLE_PROD_RESET) {
                const err = new Error("Production reset disabled on server (set ENABLE_PRODUCTION_RESET=true to enable)");
                err.status = 403;
                throw err;
            }
            if (confirmationPhrase !== "WIPE ALL DATA") {
                const err = new Error("Forbidden: Production reset requires confirmation_phrase 'WIPE ALL DATA'");
                err.status = 403;
                throw err;
            }
        }

        if (cleanLevel === "job") {
            if (!jobId) {
                const err = new Error("job_id is required");
                err.status = 400;
                throw err;
            }
            const job = this.jobs.find(j => j.id === jobId);
            if (job) {
                job.status = "CANCELLED";
                job.completed_at = new Date().toISOString();
                this.queue = this.queue.filter(t => t.job_id !== jobId);
                this._recordLog(jobId, "JOB_CANCELLED", "WARN", "", 0, 0, `Job ${jobId} cancelled`);
            }
            return { status: "success", level: "job", message: `Job ${jobId} cancelled` };
        }

        if (cleanLevel === "queue") {
            this.queue = [];
            this._recordLog("", "QUEUE_FLUSHED", "WARN", "", 0, 0, "Pending serverless frontier queue purged; indexed pages preserved");
            return { status: "success", level: "queue", message: "Frontier queue flushed; indexed pages preserved" };
        }

        if (cleanLevel === "job_data") {
            if (!jobId) {
                const err = new Error("job_id is required");
                err.status = 400;
                throw err;
            }
            this.jobs = this.jobs.filter(j => j.id !== jobId);
            this.pages = this.pages.filter(p => p.job_id !== jobId);
            this.errors = this.errors.filter(e => e.job_id !== jobId);
            this.queue = this.queue.filter(t => t.job_id !== jobId);
            this._recomputeDomains();
            return { status: "success", level: "job_data", message: `Removed job ${jobId} and its indexed pages` };
        }

        if (cleanLevel === "data") {
            this.pages = [];
            this.errors = [];
            this.domains = new Set();
            this.discoveredCount = 0;
            this._recordLog("", "DATA_PURGED", "WARN", "", 0, 0, "Indexed pages purged cleanly");
            return { status: "success", level: "data", message: "Indexed pages and domain metrics purged" };
        }

        // Full dev or production clean reset (zero fake pages, zero fake workers)
        this.startTime = Date.now();
        this.visited = new Set();
        this.queue = [];
        this.domains = new Set();
        this.jobs = [];
        this.pages = [];
        this.errors = [];
        this.logs = [];
        this.discoveredCount = 0;
        this.robotsBlocked = 0;
        this.totalRetries = 0;

        this._recordLog("", "SYSTEM_INIT", "INFO", "", 0, 0,
            cleanLevel === "production"
                ? "Production reset executed cleanly (serverless preview storage cleared)"
                : "Serverless preview engine initialized — submit seeds or start a crawl job to fetch real pages"
        );

        return { status: "success", level: cleanLevel, message: "Cluster state reset cleanly" };
    }

    _recordLog(jobId, eventType, severity, url, statusCode, latencyMs, message) {
        this.logs.unshift({
            id: Date.now() + Math.floor(Math.random() * 1000),
            job_id: jobId || "",
            worker_id: "serverless-executor",
            event_type: eventType,
            severity: severity || "INFO",
            url: url || "",
            status_code: statusCode || 0,
            latency_ms: latencyMs || 0,
            message: message || "",
            created_at: new Date().toISOString()
        });
        if (this.logs.length > 200) this.logs.pop();
    }

    _recomputeDomains() {
        this.domains = new Set(this.pages.map(p => p.domain).filter(Boolean));
    }

    normalizeURL(href, base) {
        try {
            const raw = String(href || "").trim();
            const lower = raw.toLowerCase();
            if (!raw || lower.startsWith("javascript:") || lower.startsWith("mailto:") || lower.startsWith("tel:") || lower.startsWith("data:") || raw.startsWith("#")) {
                return null;
            }
            const resolved = new URL(raw, base);
            if (resolved.protocol !== "http:" && resolved.protocol !== "https:") return null;
            if (resolved.username || resolved.password) return null;
            resolved.hash = "";
            if (resolved.href.length > MAX_URL_LEN) return null;
            return resolved.href;
        } catch {
            return null;
        }
    }

    getHostname(targetUrl) {
        try {
            return new URL(targetUrl).hostname.toLowerCase();
        } catch {
            return "unknown";
        }
    }

    async fetchAndParse(targetUrl) {
        let currentUrl = await validateTargetURLAsync(targetUrl);
        const startTime = Date.now();
        let redirects = 0;
        let res;

        while (true) {
            const controller = new AbortController();
            const timeoutId = setTimeout(() => controller.abort(), 8000);
            try {
                res = await fetch(currentUrl, {
                    headers: {
                        "User-Agent": USER_AGENT,
                        "Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"
                    },
                    redirect: "manual",
                    signal: controller.signal
                });
                clearTimeout(timeoutId);
            } catch (err) {
                clearTimeout(timeoutId);
                throw err;
            }

            if (res.status >= 300 && res.status < 400) {
                const loc = res.headers.get("location");
                if (!loc) break;
                redirects++;
                if (redirects > 5) {
                    throw new Error("Exceeded maximum HTTP redirect limit (5)");
                }
                const nextUrl = new URL(loc, currentUrl).href;
                currentUrl = await validateTargetURLAsync(nextUrl); // Re-validate every redirect hop
                continue;
            }
            break;
        }

        const latency = Date.now() - startTime;
        const statusCode = res.status;

        if (!res.ok) {
            return {
                url: targetUrl,
                final_url: currentUrl,
                title: `HTTP Error (${statusCode})`,
                description: `Remote server returned status ${statusCode}`,
                h1: "",
                headings: [],
                extracted_text: "",
                canonical_url: currentUrl,
                links: [],
                internal_links: [],
                external_links: [],
                content_type: res.headers.get("content-type") || "",
                status_code: statusCode,
                latency_ms: latency,
                content_length: 0
            };
        }

        const contentType = res.headers.get("content-type") || "";
        if (!contentType.toLowerCase().includes("html")) {
            return {
                url: targetUrl,
                final_url: currentUrl,
                title: "Non-HTML Resource",
                description: `Content-Type: ${contentType}`,
                h1: "",
                headings: [],
                extracted_text: "",
                canonical_url: currentUrl,
                links: [],
                internal_links: [],
                external_links: [],
                content_type: contentType,
                status_code: statusCode,
                latency_ms: latency,
                content_length: 0
            };
        }

        // Enforce 5MB read limit in serverless execution
        const reader = res.body && res.body.getReader ? res.body.getReader() : null;
        let html = "";
        if (reader) {
            const decoder = new TextDecoder();
            let total = 0;
            while (true) {
                const { done, value } = await reader.read();
                if (done) break;
                total += value.byteLength;
                html += decoder.decode(value, { stream: true });
                if (total >= MAX_BODY_BYTES) {
                    await reader.cancel();
                    break;
                }
            }
            html += decoder.decode();
        } else {
            html = (await res.text()).slice(0, MAX_BODY_BYTES);
        }

        const contentLength = html.length;

        const titleMatch = html.match(/<title[^>]*>([\s\S]*?)<\/title>/i);
        const title = titleMatch ? titleMatch[1].replace(/<[^>]*>/g, "").replace(/\s+/g, " ").trim() : "";

        const descMatch = html.match(/<meta[^>]*name=["']description["'][^>]*content=["']([^"']*)["']/i) ||
                          html.match(/<meta[^>]*content=["']([^"']*)["'][^>]*name=["']description["']/i);
        const description = descMatch ? descMatch[1].replace(/\s+/g, " ").trim() : "";

        const h1Match = html.match(/<h1[^>]*>([\s\S]*?)<\/h1>/i);
        const h1 = h1Match ? h1Match[1].replace(/<[^>]*>/g, "").replace(/\s+/g, " ").trim() : "";

        const headings = [];
        for (const hm of html.matchAll(/<(h[1-3])[^>]*>([\s\S]*?)<\/\1>/gi)) {
            const cleanH = hm[2].replace(/<[^>]*>/g, "").replace(/\s+/g, " ").trim();
            if (cleanH && headings.length < 20) {
                headings.push(`${hm[1].toUpperCase()}: ${cleanH}`);
            }
        }

        const strippedBody = html
            .replace(/<script[\s\S]*?<\/script>/gi, " ")
            .replace(/<style[\s\S]*?<\/style>/gi, " ")
            .replace(/<[^>]+>/g, " ")
            .replace(/\s+/g, " ")
            .trim()
            .slice(0, 4096);

        let canonicalURL = currentUrl;
        const canonicalMatch = html.match(/<link[^>]*rel=["']canonical["'][^>]*href=["']([^"']*)["']/i);
        if (canonicalMatch) {
            const normCan = this.normalizeURL(canonicalMatch[1], currentUrl);
            if (normCan) canonicalURL = normCan;
        }

        const baseHost = this.getHostname(currentUrl);
        const links = [];
        const internal_links = [];
        const external_links = [];
        const linkSet = new Set();

        for (const match of html.matchAll(/<a[^>]*href=["']([^"']*)["']/gi)) {
            const norm = this.normalizeURL(match[1], currentUrl);
            if (norm && !linkSet.has(norm)) {
                linkSet.add(norm);
                links.push(norm);
                if (this.getHostname(norm) === baseHost) {
                    internal_links.push(norm);
                } else {
                    external_links.push(norm);
                }
                if (links.length >= 60) break;
            }
        }

        return {
            url: targetUrl,
            final_url: currentUrl,
            title: title || currentUrl,
            description: description || "No meta description extracted.",
            h1,
            headings,
            extracted_text: strippedBody,
            canonical_url: canonicalURL,
            content_type: contentType,
            links,
            internal_links,
            external_links,
            status_code: statusCode,
            latency_ms: latency,
            content_length: contentLength
        };
    }

    async submit(payload = {}) {
        const rawUrls = Array.isArray(payload.urls) ? payload.urls : (payload.url ? [payload.url] : (Array.isArray(payload) ? payload : []));
        if (rawUrls.length === 0) throw new Error("No seed URLs provided");

        const validated = [];
        for (const u of rawUrls) {
            const norm = await validateTargetURLAsync(u);
            validated.push(norm);
        }

        const maxDepth = Math.min(10, Math.max(1, parseInt(payload.max_depth || 3, 10) || 3));
        const maxPages = Math.min(500, Math.max(1, parseInt(payload.max_pages || 50, 10) || 50));
        const concurrency = Math.max(1, parseInt(payload.concurrency || 2, 10) || 2);
        const politenessMs = Math.max(100, parseInt(payload.politeness_delay_ms || 1000, 10) || 1000);
        const allowedDomains = Array.isArray(payload.allowed_domains) ? payload.allowed_domains : [];
        const excludePatterns = Array.isArray(payload.exclude_patterns) ? payload.exclude_patterns : [];

        const jobId = payload.job_id || `job-${Date.now()}`;
        const job = {
            id: jobId,
            name: (payload.name || "").trim() || `Crawl ${validated[0]}`,
            status: "RUNNING",
            seed_urls: validated,
            max_depth: maxDepth,
            max_pages: maxPages,
            concurrency,
            politeness_ms: politenessMs,
            allowed_domains: allowedDomains,
            exclude_patterns: excludePatterns,
            pages_crawled: 0,
            pages_failed: 0,
            discovered_count: 0,
            current_depth: 0,
            created_at: new Date().toISOString(),
            started_at: new Date().toISOString(),
            updated_at: new Date().toISOString()
        };

        let enqueued = 0;
        const jobVisited = new Set();
        for (const u of validated) {
            if (!jobVisited.has(u)) {
                jobVisited.add(u);
                this.visited.add(u);
                this.queue.unshift({ url: u, job_id: jobId, depth: 0 });
                enqueued++;
            }
        }

        this.jobs.unshift(job);
        if (this.jobs.length > 50) this.jobs.pop();

        this._recordLog(jobId, "JOB_CREATED", "INFO", validated[0], 0, 0,
            `Created job "${job.name}" (${enqueued} seeds, max_depth=${maxDepth}, max_pages=${maxPages})`
        );

        return { job, enqueued };
    }

    async crawlNext() {
        while (this.queue.length > 0) {
            const task = this.queue.pop();
            const job = this.jobs.find(j => j.id === task.job_id);
            if (job) {
                if (job.status === "CANCELLED") continue;
                if (job.status === "PAUSED") {
                    this.queue.unshift(task);
                    return { paused_only: true };
                }
                if (job.pages_crawled >= job.max_pages) {
                    job.status = "COMPLETED";
                    job.completed_at = new Date().toISOString();
                    continue;
                }
            }

            const domain = this.getHostname(task.url);
            let result;
            let errorMsg = "";

            try {
                result = await this.fetchAndParse(task.url);
            } catch (err) {
                errorMsg = err.message;
                result = {
                    url: task.url,
                    final_url: task.url,
                    title: "Crawl Failed",
                    description: err.message,
                    h1: "",
                    headings: [],
                    extracted_text: "",
                    canonical_url: task.url,
                    links: [],
                    internal_links: [],
                    external_links: [],
                    content_type: "",
                    status_code: err.message.includes("SSRF") ? 403 : 504,
                    latency_ms: 0,
                    content_length: 0
                };
            }

            this.domains.add(domain);

            const pageRecord = {
                id: Date.now() + Math.floor(Math.random() * 1000),
                job_id: task.job_id || "",
                url: task.url,
                final_url: result.final_url,
                canonical_url: result.canonical_url,
                title: result.title,
                description: result.description,
                h1: result.h1,
                headings: result.headings,
                extracted_text: result.extracted_text,
                content_type: result.content_type,
                content_length: result.content_length,
                status_code: result.status_code,
                latency_ms: result.latency_ms,
                depth: task.depth,
                domain,
                links_count: result.links.length,
                internal_links: result.internal_links,
                external_links: result.external_links,
                error_message: errorMsg,
                crawled_at: new Date().toISOString()
            };

            const existingIdx = this.pages.findIndex(p => p.url === task.url);
            if (existingIdx >= 0) {
                this.pages[existingIdx] = pageRecord;
            } else {
                this.pages.unshift(pageRecord);
            }
            if (this.pages.length > 500) this.pages.pop();

            const isError = pageRecord.status_code >= 400 || errorMsg !== "";
            if (isError) {
                this.errors.unshift({
                    id: Date.now(),
                    job_id: task.job_id || "",
                    url: task.url,
                    worker_id: "serverless-executor",
                    status_code: pageRecord.status_code,
                    error_type: errorMsg.includes("SSRF") ? "SSRF_BLOCKED" : `HTTP_${pageRecord.status_code}`,
                    error_message: errorMsg || `HTTP status ${pageRecord.status_code}`,
                    created_at: new Date().toISOString()
                });
            }

            let newEnqueued = 0;
            const maxDepth = job ? job.max_depth : 3;
            if (!isError && task.depth + 1 <= maxDepth) {
                const allowed = job ? job.allowed_domains : [];
                const excludes = job ? job.exclude_patterns : [];
                for (const link of result.links.slice(0, 15)) {
                    if (!matchesPolicy(link, allowed, excludes)) continue;
                    if (!this.visited.has(link)) {
                        this.visited.add(link);
                        this.queue.unshift({ url: link, job_id: task.job_id, depth: task.depth + 1 });
                        newEnqueued++;
                        this.discoveredCount++;
                    }
                }
            }

            if (job) {
                job.pages_crawled += 1;
                if (isError) job.pages_failed += 1;
                job.discovered_count += result.links.length;
                job.current_depth = Math.max(job.current_depth, task.depth);
                job.updated_at = new Date().toISOString();
                if (job.pages_crawled >= job.max_pages || !this.queue.some(t => t.job_id === job.id)) {
                    job.status = "COMPLETED";
                    job.completed_at = new Date().toISOString();
                }
            }

            this._recordLog(
                task.job_id,
                isError ? "CRAWL_ERROR" : "PAGE_CRAWLED",
                isError ? "ERROR" : "INFO",
                task.url,
                pageRecord.status_code,
                pageRecord.latency_ms,
                `${pageRecord.title} (${pageRecord.status_code}) — ${result.links.length} links (${pageRecord.latency_ms}ms)`
            );

            return { page: pageRecord, new_links_enqueued: newEnqueued };
        }
        return null;
    }

    search({ q = "", domain = "", status = "", from = "", to = "", sort = "relevance", limit = 20, offset = 0 } = {}) {
        let results = [...this.pages];

        if (domain) {
            results = results.filter(p => p.domain === domain.toLowerCase());
        }
        if (status && status !== "0" && status !== "all") {
            const st = String(status).toLowerCase();
            if (st === "2xx") results = results.filter(p => p.status_code >= 200 && p.status_code < 300);
            else if (st === "3xx") results = results.filter(p => p.status_code >= 300 && p.status_code < 400);
            else if (st === "4xx") results = results.filter(p => p.status_code >= 400 && p.status_code < 500);
            else if (st === "5xx") results = results.filter(p => p.status_code >= 500);
            else results = results.filter(p => String(p.status_code) === st);
        }
        if (from) results = results.filter(p => new Date(p.crawled_at) >= new Date(from));
        if (to) results = results.filter(p => new Date(p.crawled_at) <= new Date(to));

        if (q && q.trim()) {
            const kw = q.toLowerCase().trim();
            results = results.filter(p =>
                (p.title && p.title.toLowerCase().includes(kw)) ||
                (p.description && p.description.toLowerCase().includes(kw)) ||
                (p.extracted_text && p.extracted_text.toLowerCase().includes(kw)) ||
                (p.url && p.url.toLowerCase().includes(kw)) ||
                (p.domain && p.domain.toLowerCase().includes(kw))
            );
        }

        switch (sort) {
            case "oldest":
                results.sort((a, b) => new Date(a.crawled_at) - new Date(b.crawled_at));
                break;
            case "latency_asc":
                results.sort((a, b) => a.latency_ms - b.latency_ms);
                break;
            case "latency_desc":
                results.sort((a, b) => b.latency_ms - a.latency_ms);
                break;
            case "status":
                results.sort((a, b) => a.status_code - b.status_code);
                break;
            default:
                results.sort((a, b) => new Date(b.crawled_at) - new Date(a.crawled_at));
        }

        const total = results.length;
        return {
            results: results.slice(offset, offset + limit),
            total,
            limit,
            offset
        };
    }

    getDomains(limit = 50) {
        const stats = {};
        for (const p of this.pages) {
            const d = p.domain || "unknown";
            if (!stats[d]) {
                stats[d] = { domain: d, total_pages: 0, total_bytes: 0, sum_lat: 0, error_count: 0, last_crawled_at: p.crawled_at };
            }
            stats[d].total_pages += 1;
            stats[d].total_bytes += (p.content_length || 0);
            stats[d].sum_lat += (p.latency_ms || 0);
            if (p.status_code >= 400) stats[d].error_count += 1;
            if (new Date(p.crawled_at) > new Date(stats[d].last_crawled_at)) {
                stats[d].last_crawled_at = p.crawled_at;
            }
        }
        return Object.values(stats)
            .map(s => ({
                domain: s.domain,
                total_pages: s.total_pages,
                total_bytes: s.total_bytes,
                avg_latency_ms: s.total_pages > 0 ? Math.round(s.sum_lat / s.total_pages) : 0,
                error_count: s.error_count,
                last_crawled_at: s.last_crawled_at
            }))
            .sort((a, b) => b.total_pages - a.total_pages)
            .slice(0, limit);
    }

    getAnalytics(range = "24h") {
        const total = this.pages.length;
        const successCount = this.pages.filter(p => p.status_code >= 200 && p.status_code < 300).length;
        const failedCount = this.pages.filter(p => p.status_code >= 400 || p.status_code === 0).length;
        const latencies = this.pages.map(p => p.latency_ms || 0).sort((a, b) => a - b);
        const avgLatency = total > 0 ? Math.round(latencies.reduce((s, v) => s + v, 0) / total) : 0;
        const p50 = latencies.length > 0 ? latencies[Math.floor(latencies.length * 0.5)] : 0;
        const p95 = latencies.length > 0 ? latencies[Math.min(latencies.length - 1, Math.floor(latencies.length * 0.95))] : 0;

        const statusMap = { "2xx": 0, "3xx": 0, "4xx": 0, "5xx/Err": 0 };
        for (const p of this.pages) {
            if (p.status_code >= 200 && p.status_code < 300) statusMap["2xx"]++;
            else if (p.status_code >= 300 && p.status_code < 400) statusMap["3xx"]++;
            else if (p.status_code >= 400 && p.status_code < 500) statusMap["4xx"]++;
            else statusMap["5xx/Err"]++;
        }

        const errCatMap = {};
        for (const e of this.errors) {
            errCatMap[e.error_type] = (errCatMap[e.error_type] || 0) + 1;
        }

        const jobBreakdown = {};
        for (const j of this.jobs) {
            jobBreakdown[j.status] = (jobBreakdown[j.status] || 0) + 1;
        }

        return {
            range,
            total_requests: total,
            pages_indexed: total,
            success_count: successCount,
            failed_count: failedCount,
            success_rate: total > 0 ? Number(((successCount * 100) / total).toFixed(1)) : 0,
            robots_blocked: this.robotsBlocked,
            lease_reclaims: 0,
            retry_count: this.totalRetries,
            avg_latency_ms: avgLatency,
            p50_latency_ms: p50,
            p95_latency_ms: p95,
            status_breakdown: Object.entries(statusMap).filter(([, c]) => c > 0).map(([label, count]) => ({ label, count })),
            error_categories: Object.entries(errCatMap).map(([label, count]) => ({ label, count })),
            domain_distribution: this.getDomains(10),
            throughput_series: total > 0 ? [{ bucket: new Date().toISOString().slice(11, 16), pages: total, errors: failedCount }] : [],
            worker_utilization: [], // Honest empty list: serverless runs bounded on-demand requests, not persistent worker daemons
            job_breakdown: jobBreakdown
        };
    }

    getStatus() {
        const analytics = this.getAnalytics("24h");
        const uptimeSec = Math.floor((Date.now() - this.startTime) / 1000);
        const mins = Math.max(0.25, uptimeSec / 60);
        return {
            status: "online",
            mode: "serverless_preview",
            backend_url: BACKEND_URL || null,
            queue_size: this.queue.length,
            visited_count: this.visited.size,
            pages_indexed: this.pages.length,
            discovered_urls_count: this.discoveredCount,
            domain_count: this.domains.size,
            active_jobs: this.jobs.filter(j => j.status === "RUNNING").length,
            completed_jobs: this.jobs.filter(j => j.status === "COMPLETED").length,
            failed_jobs: this.jobs.filter(j => j.status === "FAILED").length,
            workers_active: 0, // Honest: zero persistent daemons in serverless preview
            leases_active: 0,
            leases_reclaimed: 0,
            throughput_rpm: this.pages.length > 0 ? Number((this.pages.length / mins).toFixed(1)) : 0,
            avg_latency_ms: analytics.avg_latency_ms,
            success_rate: analytics.success_rate,
            robots_blocked: this.robotsBlocked,
            uptime_sec: uptimeSec,
            workers: [], // Honest empty fleet in serverless preview
            recent_pages: this.pages.slice(0, 10),
            recent_jobs: this.jobs.slice(0, 5),
            domain_breakdown: this.getDomains(6),
            live_logs: this.logs.slice(0, 20)
        };
    }
}

let globalEngine = null;
function getEngine() {
    if (!globalEngine) globalEngine = new CrawlerEngine();
    return globalEngine;
}

function setCORS(res) {
    res.setHeader("Access-Control-Allow-Origin", "*");
    res.setHeader("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, HEAD");
    res.setHeader("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With, X-Admin-Token, X-Request-ID");
}

function verifyAdminToken(req) {
    if (!ADMIN_TOKEN) return true;
    const hdr = req.headers["x-admin-token"] || "";
    const auth = req.headers["authorization"] || "";
    const bearer = auth.startsWith("Bearer ") ? auth.slice(7) : "";
    return hdr === ADMIN_TOKEN || bearer === ADMIN_TOKEN;
}

async function proxyToBackend(req, res, targetPath, timeoutMs = 5000) {
    if (!BACKEND_URL) return false;
    const qs = req.url && req.url.includes("?") ? req.url.slice(req.url.indexOf("?")) : "";
    const url = `${BACKEND_URL}${targetPath}${qs}`;
    const controller = new AbortController();
    const tid = setTimeout(() => controller.abort(), timeoutMs);
    try {
        const headers = { "Accept": "application/json" };
        if (req.headers["authorization"]) headers["Authorization"] = req.headers["authorization"];
        if (req.headers["x-admin-token"]) headers["X-Admin-Token"] = req.headers["x-admin-token"];
        if (req.method !== "GET" && req.method !== "HEAD" && req.body) {
            headers["Content-Type"] = "application/json";
        }

        const backendRes = await fetch(url, {
            method: req.method,
            headers,
            body: req.method !== "GET" && req.method !== "HEAD" && req.body ? JSON.stringify(req.body) : undefined,
            signal: controller.signal
        });
        clearTimeout(tid);

        const contentType = backendRes.headers.get("content-type") || "application/json";
        const bodyText = await backendRes.text();
        res.status(backendRes.status).setHeader("Content-Type", contentType).send(bodyText);
        return true;
    } catch (err) {
        clearTimeout(tid);
        res.status(502).json({
            error: `Configured Go coordinator (${BACKEND_URL}) unreachable: ${err.message}`,
            mode: "proxy_error"
        });
        return true;
    }
}

module.exports = {
    getEngine,
    setCORS,
    verifyAdminToken,
    proxyToBackend,
    BACKEND_URL,
    ENABLE_PROD_RESET,
    validateTargetURLAsync,
    isPrivateOrReservedIP
};
