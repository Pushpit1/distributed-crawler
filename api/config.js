// api/config.js - Cluster runtime configuration
const { setCORS, BACKEND_URL } = require("./_engine");

module.exports = async function handler(req, res) {
    setCORS(res);
    if (req.method === "OPTIONS") return res.status(200).end();

    return res.status(200).json({
        service: "gopher-distributed-crawler",
        version: "1.0.0",
        mode: BACKEND_URL ? "proxy" : "serverless_preview",
        backend_url: BACKEND_URL || null,
        politeness_delay_ms: 1000,
        max_depth_default: 3,
        max_pages_default: 100,
        lease_timeout_sec: 60
    });
};
