// api/metrics.js - Prometheus & JSON telemetry metrics
const { getEngine, setCORS, proxyToBackend } = require("./_engine");

module.exports = async function handler(req, res) {
    setCORS(res);
    if (req.method === "OPTIONS") return res.status(200).end();

    if (await proxyToBackend(req, res, "/api/metrics")) return;

    const engine = getEngine();
    const st = engine.getStatus();
    const an = engine.getAnalytics("24h");

    if (req.query && req.query.format === "json") {
        return res.status(200).json({
            crawl_requests_total: an.total_requests,
            crawl_requests_success_total: an.success_count,
            crawl_requests_failed_total: an.failed_count,
            crawl_request_duration_ms: an.avg_latency_ms,
            crawl_pages_indexed_total: st.pages_indexed,
            crawl_queue_depth: st.queue_size,
            crawl_jobs_active: st.active_jobs,
            crawl_jobs_completed: st.completed_jobs,
            crawl_workers_active: 0
        });
    }

    const lines = [
        "# HELP crawl_requests_total Total crawl requests processed",
        "# TYPE crawl_requests_total counter",
        `crawl_requests_total ${an.total_requests}`,
        "# HELP crawl_requests_success_total Successful HTTP 2xx crawl requests",
        "# TYPE crawl_requests_success_total counter",
        `crawl_requests_success_total ${an.success_count}`,
        "# HELP crawl_requests_failed_total Failed or error crawl requests",
        "# TYPE crawl_requests_failed_total counter",
        `crawl_requests_failed_total ${an.failed_count}`,
        "# HELP crawl_request_duration Average HTTP crawl duration in milliseconds",
        "# TYPE crawl_request_duration gauge",
        `crawl_request_duration ${an.avg_latency_ms}`,
        "# HELP crawl_pages_indexed_total Pages currently indexed",
        "# TYPE crawl_pages_indexed_total gauge",
        `crawl_pages_indexed_total ${st.pages_indexed}`,
        "# HELP crawl_queue_depth Pending URLs waiting in frontier",
        "# TYPE crawl_queue_depth gauge",
        `crawl_queue_depth ${st.queue_size}`,
        "# HELP crawl_jobs_active Active crawl jobs",
        "# TYPE crawl_jobs_active gauge",
        `crawl_jobs_active ${st.active_jobs}`,
        "# HELP crawl_jobs_completed Completed crawl jobs",
        "# TYPE crawl_jobs_completed gauge",
        `crawl_jobs_completed ${st.completed_jobs}`,
        "# HELP crawl_workers_active Persistent worker daemons (0 in serverless preview)",
        "# TYPE crawl_workers_active gauge",
        "crawl_workers_active 0"
    ];

    return res.status(200).setHeader("Content-Type", "text/plain; version=0.0.4").send(lines.join("\n") + "\n");
};
