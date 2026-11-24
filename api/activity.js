// api/activity.js - Auditable crawl events and telemetry feed
const { getEngine, setCORS, BACKEND_URL } = require("./_engine");

module.exports = async function handler(req, res) {
    setCORS(res);

    if (req.method === "OPTIONS") return res.status(200).end();

    if (BACKEND_URL) {
        try {
            const controller = new AbortController();
            const timeoutId = setTimeout(() => controller.abort(), 4000);
            const backendRes = await fetch(`${BACKEND_URL}/api/activity`, { signal: controller.signal });
            clearTimeout(timeoutId);
            if (backendRes.ok) return res.status(200).json(await backendRes.json());
        } catch {
            // fallback
        }
    }

    const engine = getEngine();
    return res.status(200).json(engine.logs);
};
