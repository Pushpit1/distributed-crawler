// api/analytics.js - Real aggregated analytics metrics
const { getEngine, setCORS, proxyToBackend } = require("./_engine");

module.exports = async function handler(req, res) {
    setCORS(res);
    if (req.method === "OPTIONS") return res.status(200).end();
    if (req.method !== "GET") return res.status(405).json({ error: "Method not allowed" });

    if (await proxyToBackend(req, res, "/api/analytics")) return;

    const range = (req.query && req.query.range) || "24h";
    const engine = getEngine();
    return res.status(200).json(engine.getAnalytics(range));
};
