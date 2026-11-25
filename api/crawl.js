// api/crawl.js - Execute one bounded crawl step
const { getEngine, setCORS, proxyToBackend } = require("./_engine");

module.exports = async function handler(req, res) {
    setCORS(res);
    if (req.method === "OPTIONS") return res.status(200).end();
    if (req.method !== "POST") return res.status(405).json({ error: "Method not allowed" });

    if (await proxyToBackend(req, res, "/api/crawl", 16000)) return;

    const engine = getEngine();
    if (req.body && req.body.url) {
        try {
            await engine.submit({ urls: [req.body.url] });
        } catch (err) {
            return res.status(400).json({ error: err.message });
        }
    }

    try {
        const result = await engine.crawlNext();
        if (!result) {
            return res.status(200).json({
                status: "queue_empty",
                message: "Frontier queue is empty. Submit new seeds to continue."
            });
        }
        if (result.paused_only) {
            return res.status(200).json({
                status: "paused",
                message: "Queued job is currently paused. Resume job to continue crawling."
            });
        }
        return res.status(200).json({
            status: "crawled",
            page: result.page,
            new_links_enqueued: result.new_links_enqueued
        });
    } catch (err) {
        return res.status(500).json({ error: `Crawl step failed: ${err.message}` });
    }
};
