// api/search.js - Search indexed pages with filters, sorting & pagination
const { getEngine, setCORS, proxyToBackend } = require("./_engine");

module.exports = async function handler(req, res) {
    setCORS(res);
    if (req.method === "OPTIONS") return res.status(200).end();
    if (req.method !== "GET") return res.status(405).json({ error: "Method not allowed" });

    if (await proxyToBackend(req, res, "/api/search")) return;

    const q = (req.query && req.query.q) || "";
    const domain = (req.query && req.query.domain) || "";
    const status = (req.query && req.query.status) || "";
    const from = (req.query && req.query.from) || "";
    const to = (req.query && req.query.to) || "";
    const sort = (req.query && req.query.sort) || "relevance";
    const limit = Math.min(100, Math.max(1, parseInt((req.query && req.query.limit) || "20", 10) || 20));
    const offset = Math.max(0, parseInt((req.query && req.query.offset) || "0", 10) || 0);

    const engine = getEngine();
    return res.status(200).json(engine.search({ q, domain, status, from, to, sort, limit, offset }));
};
