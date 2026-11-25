// api/jobs.js - List crawl jobs
const { getEngine, setCORS, proxyToBackend } = require("./_engine");

module.exports = async function handler(req, res) {
    setCORS(res);
    if (req.method === "OPTIONS") return res.status(200).end();
    if (await proxyToBackend(req, res, "/api/jobs")) return;

    const engine = getEngine();
    return res.status(200).json({
        jobs: engine.jobs,
        total: engine.jobs.length
    });
};
