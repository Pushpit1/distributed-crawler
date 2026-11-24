// api/status.js - Real-time cluster status & telemetry metrics
const { getEngine, setCORS, BACKEND_URL } = require("./_engine");

module.exports = async function handler(req, res) {
    setCORS(res);

    if (req.method === "OPTIONS") {
        return res.status(200).end();
    }

    if (req.method !== "GET") {
        return res.status(405).json({ error: "Method not allowed" });
    }

    // If external backend configured, attempt proxying first
    if (BACKEND_URL) {
        try {
            const controller = new AbortController();
            const timeoutId = setTimeout(() => controller.abort(), 3000);
            const backendRes = await fetch(`${BACKEND_URL}/api/status`, {
                signal: controller.signal
            }).catch(() => fetch(`${BACKEND_URL}/status`, { signal: controller.signal }));
            clearTimeout(timeoutId);

            if (backendRes && backendRes.ok) {
                const data = await backendRes.json();
                return res.status(200).json({ ...data, mode: "proxy", backend_url: BACKEND_URL });
            }
        } catch {
            // Fall back to edge engine on connection error
        }
    }

    const engine = getEngine();
    return res.status(200).json(engine.getStatus());
};
