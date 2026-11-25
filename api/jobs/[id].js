// api/jobs/[id].js - Inspect single crawl job details
const { getEngine, setCORS, proxyToBackend } = require("../_engine");

module.exports = async function handler(req, res) {
    setCORS(res);
    if (req.method === "OPTIONS") return res.status(200).end();
    const id = req.query && req.query.id;
    if (!id) return res.status(400).json({ error: "Missing job id" });

    if (await proxyToBackend(req, res, `/api/jobs/${encodeURIComponent(id)}`)) return;

    const engine = getEngine();
    const job = engine.jobs.find(j => j.id === id);
    if (!job) return res.status(404).json({ error: "Crawl job not found" });

    return res.status(200).json({
        job,
        seeds: job.seed_urls || [],
        errors: engine.errors.filter(e => e.job_id === id),
        events: engine.logs.filter(l => l.job_id === id),
        associated_workers: []
    });
};
