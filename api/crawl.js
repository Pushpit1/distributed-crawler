// api/crawl.js - Manually dispatch a crawler worker step
const { getEngine, setCORS, BACKEND_URL } = require("./_engine");

module.exports = async function handler(req, res) {
    setCORS(res);

    if (req.method === "OPTIONS") {
        return res.status(200).end();
    }

    if (req.method !== "POST") {
        return res.status(405).json({ error: "Method not allowed" });
    }

    const engine = getEngine();

    // Check if a specific target URL was provided in request
    const targetUrl = req.body && req.body.url ? req.body.url.trim() : null;
    if (targetUrl) {
        engine.submit([targetUrl]);
    }

    try {
        const result = await engine.crawlNext();
        if (!result) {
            return res.status(200).json({
                status: "queue_empty",
                message: "No pending URLs in crawl frontier queue. Submit new seeds to continue."
            });
        }

        return res.status(200).json({
            status: "crawled",
            page: result.page,
            new_links_enqueued: result.new_links_enqueued
        });
    } catch (err) {
        return res.status(500).json({
            error: "Crawl step execution failed",
            details: err.message
        });
    }
};
