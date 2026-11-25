// api/workers.js - Registered worker node fleet (honest empty list in serverless preview)
const { getEngine, setCORS, proxyToBackend } = require("./_engine");

module.exports = async function handler(req, res) {
    setCORS(res);
    if (req.method === "OPTIONS") return res.status(200).end();
    if (await proxyToBackend(req, res, "/api/workers")) return;

    return res.status(200).json({
        workers: [],
        total: 0,
        mode: "serverless_preview",
        note: "Serverless preview executes bounded on-demand requests without persistent Go worker daemons."
    });
};
