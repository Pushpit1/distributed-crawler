document.addEventListener('DOMContentLoaded', () => {
    const API = "http://localhost:8080";

    const searchForm = document.getElementById('search-form');
    const searchInput = document.getElementById('search-input');
    const resultsCount = document.getElementById('results-count');
    const resultsList = document.getElementById('results-list');
    const emptyState = document.getElementById('empty-state');
    const shimmerLoader = document.getElementById('shimmer-loader');

    const seedForm = document.getElementById('seed-form');
    const seedInput = document.getElementById('seed-input');

    const resetBtn = document.getElementById('reset-btn');

    const metricPages = document.getElementById('metric-pages');
    const metricDomains = document.getElementById('metric-domains');
    const metricQueue = document.getElementById('metric-queue');
    const workersList = document.getElementById('workers-list');
    const activityFeed = document.getElementById('activity-feed');

    // ==========================================
    // Search
    // ==========================================
    searchForm.addEventListener('submit', async (e) => {
        e.preventDefault();

        const query = searchInput.value.trim();
        if (!query) return;

        emptyState.classList.add('hidden');
        resultsList.innerHTML = '';
        resultsCount.classList.add('hidden');
        shimmerLoader.classList.remove('hidden');

        try {
            const response = await fetch(`${API}/search?q=${encodeURIComponent(query)}`);

            if (!response.ok) {
                throw new Error(`Search request failed (${response.status})`);
            }

            const results = await response.json();
            renderResults(results);

        } catch (err) {
            console.error(err);
            renderError("Unable to connect to crawler backend.");
        } finally {
            shimmerLoader.classList.add('hidden');
        }
    });

    function renderResults(results) {
        resultsList.innerHTML = '';

        if (!results || results.length === 0) {
            resultsCount.textContent = "No matching pages found.";
            resultsCount.classList.remove("hidden");
            emptyState.classList.remove("hidden");
            return;
        }

        resultsCount.textContent = `Found ${results.length} matching pages`;
        resultsCount.classList.remove("hidden");

        results.forEach(item => {

            const card = document.createElement("div");
            card.className = "result-card glass-panel";

            let badgeHTML = "";

            if (item.status_code !== 200) {
                const badgeClass =
                    item.status_code === 403
                        ? "badge-forbidden"
                        : "badge-error";

                const label =
                    item.status_code === 403
                        ? "ROBOTS BLOCK"
                        : `HTTP ${item.status_code}`;

                badgeHTML = `<span class="status-badge ${badgeClass}">${label}</span>`;
            }

            card.innerHTML = `
                <a href="${item.URL}" target="_blank" class="result-title-link">
                    <h4 class="result-title">
                        ${escapeHTML(item.title || "Untitled Page")}
                        ${badgeHTML}
                    </h4>
                </a>

                <div class="result-url">
                    ${escapeHTML(item.URL)}
                </div>

                <p class="result-desc">
                    ${escapeHTML(item.description || "No description available")}
                </p>
            `;

            resultsList.appendChild(card);
        });
    }

    function renderError(message) {

        resultsList.innerHTML = "";

        const card = document.createElement("div");

        card.className = "result-card glass-panel";

        card.innerHTML = `
            <h4 style="color:red;">
                Backend Error
            </h4>

            <p>${escapeHTML(message)}</p>
        `;

        resultsList.appendChild(card);
    }

    // ==========================================
    // Submit Seed URL
    // ==========================================
    seedForm.addEventListener("submit", async (e) => {

        e.preventDefault();

        const url = seedInput.value.trim();

        if (!url) return;

        try {

            const response = await fetch(`${API}/submit`, {

                method: "POST",

                headers: {
                    "Content-Type": "application/json"
                },

                body: JSON.stringify({
                    urls: [url]
                })

            });

            if (!response.ok) {
                throw new Error("Failed");
            }

            await response.json();

            seedInput.value = "";

            seedInput.placeholder = "✅ Seed Added!";

            setTimeout(() => {

                seedInput.placeholder =
                    "Paste a new absolute seed URL...";

            }, 3000);

            updateDashboard();

        } catch (err) {

            console.error(err);

            alert("Failed to enqueue URL.");

        }

    });

    // ==========================================
    // Reset
    // ==========================================
    resetBtn.addEventListener("click", async () => {

        if (
            !confirm(
                "Reset crawler state?"
            )
        )
            return;

        try {

            const response = await fetch(`${API}/reset`, {

                method: "POST"

            });

            if (!response.ok)
                throw new Error();

            searchInput.value = "";

            resultsList.innerHTML = "";

            emptyState.classList.remove("hidden");

            metricPages.textContent = "0";
            metricDomains.textContent = "0";
            metricQueue.textContent = "0";

            activityFeed.innerHTML =
                "<div class='no-activity'>Reset Complete</div>";

            updateDashboard();

        } catch {

            alert("Reset failed.");

        }

    });

    // ==========================================
    // Dashboard
    // ==========================================
    async function updateDashboard() {

        try {

            const response =
                await fetch(`${API}/status`);

            if (!response.ok)
                throw new Error();

            const data =
                await response.json();

            metricPages.textContent =
                data.visited_count.toLocaleString();

            metricDomains.textContent =
                data.domain_count.toLocaleString();

            metricQueue.textContent =
                data.queue_size.toLocaleString();

            workersList.innerHTML = "";

            if (
                !data.workers ||
                data.workers.length === 0
            ) {

                workersList.innerHTML =
                    "<div class='no-workers'>No workers</div>";

            } else {

                data.workers.forEach(w => {

                    const div =
                        document.createElement("div");

                    div.className =
                        "worker-node-item";

                    div.innerHTML = `
                        <span>
                            🟢 ${escapeHTML(w.id)}
                        </span>

                        <span>
                            ${w.last_seen_sec_ago.toFixed(1)}s ago
                        </span>
                    `;

                    workersList.appendChild(div);

                });

            }

            activityFeed.innerHTML = "";

            if (
                !data.recent_pages ||
                data.recent_pages.length === 0
            ) {

                activityFeed.innerHTML =
                    "<div class='no-activity'>No Activity</div>";

            } else {

                data.recent_pages.forEach(p => {

                    const div =
                        document.createElement("div");

                    div.className =
                        "activity-item";

                    div.innerHTML = `
                        <div class="activity-header">

                            <span class="activity-title">
                                ${escapeHTML(p.title || "Untitled")}
                            </span>

                            <span class="status-badge">
                                ${p.status_code}
                            </span>

                        </div>

                        <div class="activity-url">
                            ${escapeHTML(p.url)}
                        </div>
                    `;

                    activityFeed.appendChild(div);

                });

            }

        } catch (err) {

            console.error(err);

        }

    }

    function escapeHTML(str) {

        if (!str) return "";

        return str
            .replace(/&/g, "&amp;")
            .replace(/</g, "&lt;")
            .replace(/>/g, "&gt;")
            .replace(/"/g, "&quot;")
            .replace(/'/g, "&#039;");
    }

    updateDashboard();

    setInterval(updateDashboard, 3000);

});