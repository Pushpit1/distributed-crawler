const test = require("node:test");
const assert = require("node:assert/strict");
const { getEngine, isPrivateOrReservedIP, validateTargetURLAsync } = require("./_engine.js");

test("SSRF IP blocking covers loopback, RFC1918, metadata, ULA, and IPv4-mapped IPv6", () => {
    const blocked = [
        "127.0.0.1",
        "10.0.0.1",
        "172.16.0.5",
        "172.31.255.255",
        "192.168.1.1",
        "169.254.169.254",
        "::1",
        "::ffff:127.0.0.1",
        "::ffff:169.254.169.254",
        "fe80::1",
        "fc00::1"
    ];
    for (const ip of blocked) {
        assert.equal(isPrivateOrReservedIP(ip), true, `Expected ${ip} to be blocked`);
    }

    const allowed = ["8.8.8.8", "1.1.1.1", "142.250.190.46"];
    for (const ip of allowed) {
        assert.equal(isPrivateOrReservedIP(ip), false, `Expected ${ip} to be allowed`);
    }
});

test("SSRF URL validator rejects internal schemes, credentials, and localhost", async () => {
    await assert.rejects(() => validateTargetURLAsync("http://127.0.0.1:8080/admin"));
    await assert.rejects(() => validateTargetURLAsync("http://localhost:6379"));
    await assert.rejects(() => validateTargetURLAsync("http://user:pass@example.com"));
    await assert.rejects(() => validateTargetURLAsync("file:///etc/passwd"));
    await assert.rejects(() => validateTargetURLAsync("http://169.254.169.254/latest/meta-data/"));
});

test("Serverless engine starts cleanly with zero fake workers or fake pages", () => {
    const engine = getEngine();
    engine.reset("dev");
    const st = engine.getStatus();

    assert.equal(st.mode, "serverless_preview");
    assert.equal(st.workers.length, 0);
    assert.equal(st.workers_active, 0);
    assert.equal(st.pages_indexed, 0);
    assert.equal(st.recent_pages.length, 0);
});

test("Job lifecycle, search filters/pagination, analytics, and tiered resets", async () => {
    const engine = getEngine();
    engine.reset("dev");

    // Seed submission
    const { job, enqueued } = await engine.submit({
        name: "Test Crawl Job",
        urls: ["https://8.8.8.8/docs"],
        max_depth: 2,
        max_pages: 10
    });
    assert.equal(enqueued, 1);
    assert.equal(job.status, "RUNNING");

    // Inject synthetic test records strictly in unit test to test search/filter/sort/analytics logic
    engine.pages.push(
        {
            id: 10, job_id: job.id, url: "https://example.org/a", domain: "example.org",
            title: "Alpha Distributed Systems", description: "Go concurrency guide",
            extracted_text: "Channels and goroutines", status_code: 200, latency_ms: 120,
            depth: 0, content_length: 1024, crawled_at: "2026-11-25T10:00:00Z"
        },
        {
            id: 11, job_id: job.id, url: "https://example.org/err", domain: "example.org",
            title: "Not Found", description: "Missing", extracted_text: "",
            status_code: 404, latency_ms: 45, depth: 1, content_length: 120,
            crawled_at: "2026-11-25T11:00:00Z"
        }
    );

    const fts = engine.search({ q: "concurrency", status: "2xx", sort: "newest" });
    assert.equal(fts.total, 1);
    assert.equal(fts.results[0].url, "https://example.org/a");

    const analytics = engine.getAnalytics("24h");
    assert.equal(analytics.pages_indexed, 2);
    assert.equal(analytics.success_count, 1);
    assert.equal(analytics.failed_count, 1);
    assert.equal(analytics.success_rate, 50);

    // Queue reset preserves pages
    engine.reset("queue");
    assert.equal(engine.queue.length, 0);
    assert.equal(engine.pages.length, 2);

    // Production reset blocked unless confirmed
    assert.throws(() => engine.reset("production", "wrong"), /disabled|Forbidden/);
});
