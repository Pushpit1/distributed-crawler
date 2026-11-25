// api/ready.js - Readiness probe
const { setCORS, BACKEND_URL, proxyToBackend } = require("./_engine");

module.exports = async function handler(req, res) {
    setCORS(res);
    if (req.method === "OPTIONS") return res.status(200).end();
    if (await proxyToBackend(req, res, "/api/ready")) return;

    return res.status(200).json({
        status: "ready",
        mode: BACKEND_URL ? "proxy" : "serverless_preview",
        redis_ready: false,
        db_ready: false,
        note: "Running in bounded in-memory serverless preview mode",
        timestamp: new Date().toISOString()
    });
};
