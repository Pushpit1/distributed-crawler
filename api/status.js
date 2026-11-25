// api/status.js - Real-time telemetry & cluster status
const { getEngine, setCORS, proxyToBackend } = require("./_engine");

module.exports = async function handler(req, res) {
    setCORS(res);
    if (req.method === "OPTIONS") return res.status(200).end();
    if (req.method !== "GET") return res.status(405).json({ error: "Method not allowed" });

    if (await proxyToBackend(req, res, "/api/status")) return;

    const engine = getEngine();
    return res.status(200).json(engine.getStatus());
};
