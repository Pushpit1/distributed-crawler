// api/ready.js - Service readiness probe
const { setCORS } = require("./_engine");

module.exports = async function handler(req, res) {
    setCORS(res);
    if (req.method === "OPTIONS") return res.status(200).end();
    return res.status(200).json({ status: "ready", timestamp: new Date().toISOString() });
};
