// api/health.js - Service health check
const { setCORS, BACKEND_URL } = require("./_engine");

module.exports = async function handler(req, res) {
    setCORS(res);

    if (req.method === "OPTIONS") {
        return res.status(200).end();
    }

    return res.status(200).json({
        status: "healthy",
        platform: "vercel-serverless",
        service: "distributed-crawler-api",
        proxy_target: BACKEND_URL || null,
        timestamp: new Date().toISOString()
    });
};
