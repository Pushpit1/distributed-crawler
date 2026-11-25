// api/pages/[id].js - Inspect individual crawled page record
const { getEngine, setCORS, proxyToBackend } = require("../_engine");

module.exports = async function handler(req, res) {
    setCORS(res);
    if (req.method === "OPTIONS") return res.status(200).end();
    const id = req.query && req.query.id;
    if (!id) return res.status(400).json({ error: "Missing page id" });

    if (await proxyToBackend(req, res, `/api/pages/${encodeURIComponent(id)}`)) return;

    const engine = getEngine();
    const page = engine.pages.find(p => String(p.id) === String(id) || p.url === id);
    if (!page) return res.status(404).json({ error: "Page not found" });
    return res.status(200).json(page);
};
