// api/reset.js - Granular safe reset with admin token & confirmation phrase checks
const { getEngine, setCORS, verifyAdminToken, proxyToBackend } = require("./_engine");

module.exports = async function handler(req, res) {
    setCORS(res);
    if (req.method === "OPTIONS") return res.status(200).end();
    if (req.method !== "POST") return res.status(405).json({ error: "Method not allowed" });

    if (await proxyToBackend(req, res, "/api/reset")) return;

    const body = req.body || {};
    const level = String(body.level || "ui").toLowerCase();
    const phrase = String(body.confirmation_phrase || "");
    const jobId = String(body.job_id || "");

    if (level !== "ui" && !verifyAdminToken(req)) {
        return res.status(401).json({ error: "Admin authorization required for destructive reset operation" });
    }

    const engine = getEngine();
    try {
        const result = engine.reset(level, phrase, jobId);
        return res.status(200).json(result);
    } catch (err) {
        return res.status(err.status || 500).json({ error: err.message });
    }
};
