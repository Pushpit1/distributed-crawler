// api/submit.js - Submit new seed URLs to the crawler pool
const { getEngine, setCORS, BACKEND_URL } = require("./_engine");

module.exports = async function handler(req, res) {
    setCORS(res);

    if (req.method === "OPTIONS") {
        return res.status(200).end();
    }

    if (req.method !== "POST") {
        return res.status(405).json({ error: "Method not allowed" });
    }

    let urls = [];
    if (req.body) {
        if (Array.isArray(req.body.urls)) {
            urls = req.body.urls;
        } else if (req.body.url) {
            urls = [req.body.url];
        } else if (Array.isArray(req.body)) {
            urls = req.body;
        }
    }

    if (!urls || urls.length === 0) {
        return res.status(400).json({ error: "No URLs provided in request payload" });
    }

    // If external backend configured, attempt proxying first
    if (BACKEND_URL) {
        try {
            const controller = new AbortController();
            const timeoutId = setTimeout(() => controller.abort(), 4000);
            const backendRes = await fetch(`${BACKEND_URL}/api/submit`, {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ urls }),
                signal: controller.signal
            }).catch(() => fetch(`${BACKEND_URL}/submit`, {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ urls }),
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
    const enqueued = engine.submit(urls);

    // If auto_crawl flag set or default single URL submitted, execute immediate crawl on first URL
    let crawlResult = null;
    if (enqueued > 0) {
        try {
            crawlResult = await engine.crawlNext();
        } catch (err) {
            console.error("Auto crawl error:", err);
        }
    }

    return res.status(200).json({
        status: "success",
        enqueued: enqueued,
        crawled: crawlResult ? crawlResult.page : null
    });
};
