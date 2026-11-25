// api/domains.js - Domain statistics and registry
const { getEngine, setCORS, proxyToBackend } = require("./_engine");

module.exports = async function handler(req, res) {
    setCORS(res);
    if (req.method === "OPTIONS") return res.status(200).end();
    if (await proxyToBackend(req, res, "/api/domains")) return;

    const engine = getEngine();
    const domains = engine.getDomains(50);
    return res.status(200).json({
        domains,
        total: domains.length
    });
};
