// api/reset.js - Tiered cluster and data reset
const { getEngine, setCORS, BACKEND_URL } = require("./_engine");

module.exports = async function handler(req, res) {
    setCORS(res);

    if (req.method === "OPTIONS") return res.status(200).end();

    if (req.method !== "POST") {
        return res.status(405).json({ error: "Method not allowed" });
    }

    const level = (req.body && req.body.level) || "dev";
    const phrase = (req.body && req.body.confirmation_phrase) || "";

    if (level === "production" && phrase !== "WIPE ALL DATA") {
        return res.status(403).json({
            error: "Forbidden: Production reset requires confirmation_phrase 'WIPE ALL DATA'"
        });
    }

    if (BACKEND_URL) {
        try {
            const controller = new AbortController();
            const timeoutId = setTimeout(() => controller.abort(), 5000);
            const backendRes = await fetch(`${BACKEND_URL}/api/reset`, {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ level, confirmation_phrase: phrase }),
                signal: controller.signal
            });
            clearTimeout(timeoutId);

            if (backendRes.ok) return res.status(200).json(await backendRes.json());
        } catch {
            // fallback
        }
    }

    const engine = getEngine();
    try {
        const result = engine.reset(level, phrase);
        return res.status(200).json(result);
    } catch (err) {
        return res.status(err.status || 500).json({ error: err.message });
    }
};
