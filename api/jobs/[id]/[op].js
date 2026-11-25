// api/jobs/[id]/[op].js - Pause, Resume, Cancel, Retry, or Export a crawl job
const { getEngine, setCORS, verifyAdminToken, proxyToBackend } = require("../../_engine");

module.exports = async function handler(req, res) {
    setCORS(res);
    if (req.method === "OPTIONS") return res.status(200).end();

    const id = req.query && req.query.id;
    const op = String((req.query && req.query.op) || "").toLowerCase();
    if (!id || !op) return res.status(400).json({ error: "Missing job id or operation" });

    if (await proxyToBackend(req, res, `/api/jobs/${encodeURIComponent(id)}/${encodeURIComponent(op)}`)) return;

    const engine = getEngine();
    const job = engine.jobs.find(j => j.id === id);
    if (!job) return res.status(404).json({ error: "Crawl job not found" });

    if (op === "export" && req.method === "GET") {
        const format = String((req.query && req.query.format) || "json").toLowerCase();
        const pages = engine.pages.filter(p => p.job_id === id);
        if (format === "csv") {
            const header = "id,job_id,url,final_url,domain,status_code,latency_ms,depth,title,crawled_at";
            const rows = pages.map(p => [p.id, p.job_id, p.url, p.final_url, p.domain, p.status_code, p.latency_ms, p.depth, `"${(p.title || "").replace(/"/g, '""')}"`, p.crawled_at].join(","));
            res.setHeader("Content-Type", "text/csv");
            res.setHeader("Content-Disposition", `attachment; filename="job-${id}.csv"`);
            return res.status(200).send([header, ...rows].join("\n"));
        }
        res.setHeader("Content-Type", "application/json");
        res.setHeader("Content-Disposition", `attachment; filename="job-${id}.json"`);
        return res.status(200).json(pages);
    }

    if (req.method !== "POST") return res.status(405).json({ error: "Method not allowed" });

    if (op === "cancel" && !verifyAdminToken(req)) {
        return res.status(401).json({ error: "Admin authorization required to cancel jobs" });
    }

    if (op === "pause") {
        job.status = "PAUSED";
        job.updated_at = new Date().toISOString();
        engine._recordLog(id, "JOB_PAUSED", "WARN", "", 0, 0, `Job ${id} paused`);
        return res.status(200).json({ job_id: id, status: "PAUSED" });
    }
    if (op === "resume") {
        job.status = "RUNNING";
        job.updated_at = new Date().toISOString();
        engine._recordLog(id, "JOB_RESUMED", "INFO", "", 0, 0, `Job ${id} resumed`);
        return res.status(200).json({ job_id: id, status: "RUNNING" });
    }
    if (op === "cancel") {
        job.status = "CANCELLED";
        job.completed_at = new Date().toISOString();
        job.updated_at = new Date().toISOString();
        engine.queue = engine.queue.filter(t => t.job_id !== id);
        engine._recordLog(id, "JOB_CANCELLED", "WARN", "", 0, 0, `Job ${id} cancelled`);
        return res.status(200).json({ job_id: id, status: "CANCELLED" });
    }
    if (op === "retry") {
        const failedURLs = [...new Set(engine.errors.filter(e => e.job_id === id).map(e => e.url))];
        let retried = 0;
        for (const u of failedURLs) {
            engine.queue.unshift({ url: u, job_id: id, depth: 0 });
            retried++;
        }
        if (retried > 0) job.status = "RUNNING";
        return res.status(200).json({ job_id: id, retried });
    }

    return res.status(400).json({ error: `Unsupported job operation: ${op}` });
};
