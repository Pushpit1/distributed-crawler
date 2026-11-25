// api/export.js - Export indexed search results as JSON or CSV
const { getEngine, setCORS, proxyToBackend } = require("./_engine");

module.exports = async function handler(req, res) {
    setCORS(res);
    if (req.method === "OPTIONS") return res.status(200).end();
    if (await proxyToBackend(req, res, "/api/export")) return;

    const q = (req.query && req.query.q) || "";
    const domain = (req.query && req.query.domain) || "";
    const status = (req.query && req.query.status) || "";
    const format = String((req.query && req.query.format) || "json").toLowerCase();

    const engine = getEngine();
    const { results } = engine.search({ q, domain, status, limit: 200, offset: 0 });

    if (format === "csv") {
        const header = "id,job_id,url,final_url,domain,status_code,latency_ms,depth,title,crawled_at";
        const rows = results.map(p =>
            [p.id, p.job_id, p.url, p.final_url, p.domain, p.status_code, p.latency_ms, p.depth, `"${(p.title || "").replace(/"/g, '""')}"`, p.crawled_at].join(",")
        );
        res.setHeader("Content-Type", "text/csv");
        res.setHeader("Content-Disposition", 'attachment; filename="crawler-export.csv"');
        return res.status(200).send([header, ...rows].join("\n"));
    }

    res.setHeader("Content-Type", "application/json");
    res.setHeader("Content-Disposition", 'attachment; filename="crawler-export.json"');
    return res.status(200).json(results);
};
