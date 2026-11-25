// api/submit.js - Submit crawl job & seed URLs
const { getEngine, setCORS, proxyToBackend } = require("./_engine");

module.exports = async function handler(req, res) {
    setCORS(res);
    if (req.method === "OPTIONS") return res.status(200).end();
    if (req.method !== "POST") return res.status(405).json({ error: "Method not allowed" });

    if (await proxyToBackend(req, res, "/api/submit", 8000)) return;

    const engine = getEngine();
    try {
        const { job, enqueued } = await engine.submit(req.body || {});
        // Execute 1 bounded immediate crawl step on the seed so serverless preview gives instant feedback
        let crawled = null;
        if (enqueued > 0) {
            const step = await engine.crawlNext();
            if (step && step.page) crawled = step.page;
        }
        return res.status(200).json({
            status: "success",
            job_id: job.id,
            enqueued,
            job,
            crawled
        });
    } catch (err) {
        return res.status(400).json({ error: err.message });
    }
};
