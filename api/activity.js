// api/activity.js - Auditable activity event logs with severity & keyword filters
const { getEngine, setCORS, proxyToBackend } = require("./_engine");

module.exports = async function handler(req, res) {
    setCORS(res);
    if (req.method === "OPTIONS") return res.status(200).end();
    if (await proxyToBackend(req, res, "/api/activity")) return;

    const severity = String((req.query && req.query.severity) || "").toUpperCase();
    const q = String((req.query && req.query.q) || "").toLowerCase().trim();
    const limit = Math.min(200, Math.max(1, parseInt((req.query && req.query.limit) || "50", 10) || 50));

    const engine = getEngine();
    let events = engine.logs;
    if (severity) {
        events = events.filter(e => String(e.severity || "INFO").toUpperCase() === severity);
    }
    if (q) {
        events = events.filter(e =>
            (e.message && e.message.toLowerCase().includes(q)) ||
            (e.url && e.url.toLowerCase().includes(q)) ||
            (e.event_type && e.event_type.toLowerCase().includes(q))
        );
    }

    return res.status(200).json({
        events: events.slice(0, limit),
        total: events.length
    });
};
