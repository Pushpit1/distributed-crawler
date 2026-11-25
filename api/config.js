// api/config.js - Cluster & serverless runtime configuration
const { getEngine, setCORS, verifyAdminToken, BACKEND_URL, ENABLE_PROD_RESET, proxyToBackend } = require("./_engine");

module.exports = async function handler(req, res) {
    setCORS(res);
    if (req.method === "OPTIONS") return res.status(200).end();

    if (await proxyToBackend(req, res, "/api/config")) return;

    const engine = getEngine();
    if ((req.method === "POST" || req.method === "PUT") && req.body) {
        if (!verifyAdminToken(req)) {
            return res.status(401).json({ error: "Admin authorization required" });
        }
        for (const [k, v] of Object.entries(req.body)) {
            engine.config[k] = String(v);
        }
    }

    return res.status(200).json({
        service: "gopher-distributed-crawler",
        version: "2.0.0",
        mode: BACKEND_URL ? "proxy" : "serverless_preview",
        backend_url: BACKEND_URL || null,
        politeness_delay_ms: parseInt(engine.config.default_politeness_ms, 10) || 1000,
        max_depth_default: parseInt(engine.config.default_max_depth, 10) || 3,
        max_pages_default: parseInt(engine.config.default_max_pages, 10) || 50,
        lease_timeout_sec: parseInt(engine.config.lease_timeout_sec, 10) || 60,
        production_reset_enabled: ENABLE_PROD_RESET,
        admin_auth_required: Boolean(process.env.ADMIN_API_TOKEN),
        persisted_settings: engine.config
    });
};
