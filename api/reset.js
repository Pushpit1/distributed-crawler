// api/reset.js - Wipe crawler queue, database index, and restore initial state
const { getEngine, setCORS, BACKEND_URL } = require("./_engine");

module.exports = async function handler(req, res) {
    setCORS(res);

    if (req.method === "OPTIONS") {
        return res.status(200).end();
    }

    if (req.method !== "POST") {
        return res.status(405).json({ error: "Method not allowed" });
    }

    // If external backend configured, attempt proxying first
    if (BACKEND_URL) {
        try {
            const controller = new AbortController();
            const timeoutId = setTimeout(() => controller.abort(), 4000);
            const backendRes = await fetch(`${BACKEND_URL}/api/reset`, {
                method: "POST",
                signal: controller.signal
            }).catch(() => fetch(`${BACKEND_URL}/reset`, {
                method: "POST",
                signal: controller.signal
            }));
            clearTimeout(timeoutId);

            if (backendRes && backendRes.ok) {
                const data = await backendRes.json();
                return res.status(200).json(data);
            }
        } catch {
            // Fall back to edge engine on connection error
        }
    }

    const engine = getEngine();
    engine.reset();

    return res.status(200).json({
        status: "reset_completed",
        message: "Frontier queue purged, visited deduplication set cleared, and seed URLs re-initialized."
    });
};
