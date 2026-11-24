// api/jobs.js - Crawl job management
const { getEngine, setCORS, BACKEND_URL } = require("./_engine");

module.exports = async function handler(req, res) {
    setCORS(res);

    if (req.method === "OPTIONS") return res.status(200).end();

    if (BACKEND_URL) {
        try {
            const controller = new AbortController();
            const timeoutId = setTimeout(() => controller.abort(), 4000);
            const backendRes = await fetch(`${BACKEND_URL}/api/jobs`, { signal: controller.signal });
            clearTimeout(timeoutId);
            if (backendRes.ok) return res.status(200).json(await backendRes.json());
        } catch {
            // fallback to engine
        }
    }

    const engine = getEngine();

    // Check if handling pause/resume/cancel via query/body
    if (req.method === "POST" && req.query.id && req.query.op) {
        const updated = engine.updateJob(req.query.id, req.query.op.toUpperCase());
        if (updated) return res.status(200).json(updated);
        return res.status(404).json({ error: "Job not found" });
    }

    return res.status(200).json(engine.listJobs());
};
