// api/health.js - Health probe
const { setCORS, BACKEND_URL, proxyToBackend } = require("./_engine");

module.exports = async function handler(req, res) {
    setCORS(res);
    if (req.method === "OPTIONS") return res.status(200).end();
    if (await proxyToBackend(req, res, "/api/health")) return;

    return res.status(200).json({
        status: "healthy",
        platform: "vercel-serverless",
        service: "gopher-crawler-api",
        mode: BACKEND_URL ? "proxy" : "serverless_preview",
        proxy_target: BACKEND_URL || null,
        version: "2.0.0",
        timestamp: new Date().toISOString()
    });
};
