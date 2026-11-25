// api/errors.js - Crawl error registry
const { getEngine, setCORS, proxyToBackend } = require("./_engine");

module.exports = async function handler(req, res) {
    setCORS(res);
    if (req.method === "OPTIONS") return res.status(200).end();
    if (await proxyToBackend(req, res, "/api/errors")) return;

    const engine = getEngine();
    return res.status(200).json({
        errors: engine.errors.slice(0, 50),
        total: engine.errors.length
    });
};
