/**
 * GOPHER CRAWLER — WEB APPLICATION SCRIPT
 * Production Grade Telemetry Console & Distributed Controller
 */

document.addEventListener('DOMContentLoaded', () => {
    // =========================================================================
    // 1. API Configuration & Auto-Detection
    // =========================================================================
    const DEFAULT_API = "/api";

    function getApiBase() {
        const stored = localStorage.getItem("crawler_api_base");
        if (stored) return stored.replace(/\/+$/, "");
        return DEFAULT_API;
    }

    function setApiBase(url) {
        if (!url || url.trim() === "" || url === "/api") {
            localStorage.removeItem("crawler_api_base");
        } else {
            localStorage.setItem("crawler_api_base", url.trim().replace(/\/+$/, ""));
        }
        updateConnectionStatus();
        updateDashboard();
    }

    // =========================================================================
    // 2. DOM Elements
    // =========================================================================
    // Nav & Status
    const statusPill = document.getElementById('system-status-pill');
    const statusLabelText = document.getElementById('status-label-text');
    const statusModeTag = document.getElementById('status-mode-tag');
    const autoCrawlToggle = document.getElementById('auto-crawl-toggle');
    const crawlStepBtn = document.getElementById('crawl-step-btn');
    const openResetBtn = document.getElementById('open-reset-btn');
    const openSettingsBtn = document.getElementById('open-settings-btn');
    const footerConnectionStatus = document.getElementById('footer-connection-status');

    // Metrics
    const metricPages = document.getElementById('metric-pages');
    const metricPagesTrend = document.getElementById('metric-pages-trend');
    const metricDomains = document.getElementById('metric-domains');
    const metricDomainsTrend = document.getElementById('metric-domains-trend');
    const metricQueue = document.getElementById('metric-queue');
    const metricQueueTrend = document.getElementById('metric-queue-trend');
    const metricUptime = document.getElementById('metric-uptime');
    const metricBackendTag = document.getElementById('metric-backend-tag');

    // Tabs
    const tabButtons = document.querySelectorAll('.tab-btn');
    const tabPanes = document.querySelectorAll('.tab-pane');

    // Search Tab
    const searchForm = document.getElementById('search-form');
    const searchInput = document.getElementById('search-input');
    const clearSearchBtn = document.getElementById('clear-search-btn');
    const filterChips = document.querySelectorAll('.chip-btn');
    const countAll = document.getElementById('count-all');
    const count200 = document.getElementById('count-200');
    const countErr = document.getElementById('count-err');
    const resultsMetaText = document.getElementById('results-meta-text');
    const resultsLoading = document.getElementById('results-loading');
    const resultsEmpty = document.getElementById('results-empty');
    const resultsList = document.getElementById('results-list');

    // Seed Tab
    const seedForm = document.getElementById('seed-form');
    const seedInput = document.getElementById('seed-input');
    const submitSeedBtn = document.getElementById('submit-seed-btn');
    const presetCards = document.querySelectorAll('.preset-card');
    const seedFeedbackBanner = document.getElementById('seed-feedback-banner');

    // Domain Tab
    const domainDistributionChart = document.getElementById('domain-distribution-chart');
    const domainTableContainer = document.getElementById('domain-table-container');

    // Infrastructure Panel
    const activeWorkersCount = document.getElementById('active-workers-count');
    const workersContainer = document.getElementById('workers-container');
    const terminalFeed = document.getElementById('terminal-feed');
    const toggleStreamBtn = document.getElementById('toggle-stream-btn');
    const clearStreamBtn = document.getElementById('clear-stream-btn');

    // Modals
    const resetModal = document.getElementById('reset-modal');
    const cancelResetBtn = document.getElementById('cancel-reset-btn');
    const confirmResetBtn = document.getElementById('confirm-reset-btn');

    const settingsModal = document.getElementById('settings-modal');
    const customApiInput = document.getElementById('custom-api-input');
    const closeSettingsBtn = document.getElementById('close-settings-btn');
    const saveSettingsBtn = document.getElementById('save-settings-btn');
    const urlPresetPills = document.querySelectorAll('.url-preset-pill');

    const pageDetailModal = document.getElementById('page-detail-modal');
    const detailPageTitle = document.getElementById('detail-page-title');
    const detailPageContent = document.getElementById('detail-page-content');
    const closeDetailBtn = document.getElementById('close-detail-btn');

    // =========================================================================
    // 3. Application State
    // =========================================================================
    let cachedPages = [];
    let currentFilter = 'all';
    let currentSearchQuery = '';
    let isStreamPaused = false;
    let autoCrawlTimer = null;
    let isReconnecting = false;
    let lastKnownUptime = 0;

    // =========================================================================
    // 4. API Request Wrapper with Graceful Fallbacks
    // =========================================================================
    async function apiFetch(endpoint, options = {}) {
        const base = getApiBase();
        const url = `${base}${endpoint}`;

        try {
            const res = await fetch(url, {
                ...options,
                headers: {
                    'Content-Type': 'application/json',
                    ...(options.headers || {})
                }
            });

            if (!res.ok) {
                // If relative /api returned 404 or failed and we haven't set an explicit custom URL,
                // try fallback to direct local Go master at port 8080
                if (base === "/api" && res.status === 404) {
                    const fallbackUrl = `http://localhost:8080${endpoint}`;
                    const fbRes = await fetch(fallbackUrl, options);
                    if (fbRes.ok) return await fbRes.json();
                }
                throw new Error(`HTTP ${res.status}: ${res.statusText}`);
            }

            return await res.json();
        } catch (err) {
            // Auto-fallback check if running locally
            if (base === "/api" && window.location.hostname === "localhost") {
                try {
                    const fallbackUrl = `http://localhost:8080${endpoint}`;
                    const fbRes = await fetch(fallbackUrl, options);
                    if (fbRes.ok) return await fbRes.json();
                } catch {
                    // Fall through to throw original error
                }
            }
            throw err;
        }
    }

    function updateConnectionStatus(isOnline = true, mode = null) {
        const base = getApiBase();
        if (isOnline) {
            statusPill.className = "system-status-indicator";
            statusLabelText.textContent = "SYS: NOMINAL";
            statusModeTag.textContent = mode ? `[${mode.toUpperCase()}]` : `[${base === "/api" ? "EDGE" : "CLUSTER"}]`;
            footerConnectionStatus.textContent = `Connected: ${base}`;
            footerConnectionStatus.className = "footer-text text-emerald";
            isReconnecting = false;
        } else {
            statusPill.className = "system-status-indicator status-offline";
            statusLabelText.textContent = "SYS: RECONNECTING";
            statusModeTag.textContent = "[OFFLINE]";
            footerConnectionStatus.textContent = `Disconnected (${base})`;
            footerConnectionStatus.className = "footer-text text-rose";
            isReconnecting = true;
        }
    }

    // =========================================================================
    // 5. Dashboard Telemetry Updates
    // =========================================================================
    async function updateDashboard() {
        try {
            const data = await apiFetch('/status');
            updateConnectionStatus(true, data.mode);

            // 1. Metric Counters
            metricPages.textContent = Number(data.visited_count || 0).toLocaleString();
            metricDomains.textContent = Number(data.domain_count || 0).toLocaleString();
            metricQueue.textContent = Number(data.queue_size || 0).toLocaleString();

            if (data.uptime_sec !== undefined) {
                lastKnownUptime = data.uptime_sec;
                metricUptime.textContent = formatUptime(data.uptime_sec);
            }

            if (data.mode === "proxy") {
                metricBackendTag.textContent = `Go Cluster Proxy`;
                metricBackendTag.className = "metric-trend text-cyan";
            } else {
                metricBackendTag.textContent = `Vercel Serverless Edge`;
                metricBackendTag.className = "metric-trend text-muted";
            }

            metricPagesTrend.textContent = `${data.queue_size || 0} in frontier`;
            metricDomainsTrend.textContent = `${data.domain_count || 0} unique hosts`;

            // 2. Worker Fleet
            renderWorkers(data.workers || []);

            // 3. Live Activity Feed (Terminal)
            if (!isStreamPaused && data.live_logs) {
                renderTerminalFeed(data.live_logs);
            } else if (!isStreamPaused && data.recent_pages) {
                renderTerminalFeedFromPages(data.recent_pages);
            }

            // 4. Domain Breakdown
            if (data.domain_breakdown) {
                renderDomainDistribution(data.domain_breakdown);
            }

            // 5. Cache recent pages for search and filters
            if (data.recent_pages && currentSearchQuery === '') {
                cachedPages = data.recent_pages;
                updateFilterCounts();
                renderResults(cachedPages);
            }

        } catch (err) {
            console.warn("Telemetry update warning:", err);
            updateConnectionStatus(false);
        }
    }

    function formatUptime(seconds) {
        if (!seconds) return "0s";
        const h = Math.floor(seconds / 3600);
        const m = Math.floor((seconds % 3600) / 60);
        const s = Math.floor(seconds % 60);
        if (h > 0) return `${h}h ${m}m`;
        if (m > 0) return `${m}m ${s}s`;
        return `${s}s`;
    }

    // =========================================================================
    // 6. Workers Rendering
    // =========================================================================
    function renderWorkers(workers) {
        activeWorkersCount.textContent = `${workers.length} ${workers.length === 1 ? 'NODE' : 'NODES'}`;

        if (!workers || workers.length === 0) {
            workersContainer.innerHTML = `
                <div class="worker-node-item">
                    <span class="worker-host">No workers actively reporting. Dispatched jobs run on serverless workers.</span>
                </div>
            `;
            return;
        }

        workersContainer.innerHTML = workers.map(w => {
            const isIdle = w.status === 'idle' || w.last_seen_sec_ago > 4.0;
            const dotClass = isIdle ? 'node-pulse-dot idle' : 'node-pulse-dot';
            const statusLabel = isIdle ? 'IDLE' : 'ACTIVE';
            const ping = (w.last_seen_sec_ago || 0).toFixed(1);

            return `
                <div class="worker-node-item">
                    <div class="worker-node-left">
                        <span class="${dotClass}"></span>
                        <div>
                            <div class="worker-id">${escapeHTML(w.id || 'worker-node')}</div>
                            <div class="worker-host">${escapeHTML(w.hostname || 'cluster-node')}</div>
                        </div>
                    </div>
                    <div class="worker-meta">
                        <span class="badge ${isIdle ? 'badge-403' : 'badge-200'}">${statusLabel}</span>
                        <span>${ping}s ago</span>
                    </div>
                </div>
            `;
        }).join('');
    }

    // =========================================================================
    // 7. Live Terminal Feed Rendering
    // =========================================================================
    function renderTerminalFeed(logs) {
        if (!logs || logs.length === 0) {
            terminalFeed.innerHTML = `<div class="event-url text-muted">Awaiting crawl dispatch...</div>`;
            return;
        }

        terminalFeed.innerHTML = logs.map(log => {
            const statusClass = log.status === 200 ? 'status-200' : (log.status === 403 ? 'status-403' : 'status-500');
            return `
                <div class="terminal-event-row">
                    <span class="event-time">${escapeHTML(log.timestamp || '')}</span>
                    <span class="event-status ${statusClass}">${log.status}</span>
                    <span class="event-url" title="${escapeHTML(log.url)}">${escapeHTML(log.url)}</span>
                    <span class="event-latency">${escapeHTML(log.latency || '')}</span>
                </div>
            `;
        }).join('');
    }

    function renderTerminalFeedFromPages(pages) {
        if (!pages || pages.length === 0) return;
        terminalFeed.innerHTML = pages.map(p => {
            const status = p.status_code || 200;
            const statusClass = status === 200 ? 'status-200' : (status === 403 ? 'status-403' : 'status-500');
            return `
                <div class="terminal-event-row">
                    <span class="event-status ${statusClass}">${status}</span>
                    <span class="event-url" title="${escapeHTML(p.url)}">${escapeHTML(p.url)}</span>
                    <span class="event-latency">${escapeHTML(p.domain || '')}</span>
                </div>
            `;
        }).join('');
    }

    // =========================================================================
    // 8. Domain Distribution Rendering
    // =========================================================================
    function renderDomainDistribution(distribution) {
        if (!distribution || distribution.length === 0) {
            domainDistributionChart.innerHTML = `<span class="text-muted">No domain data registered yet.</span>`;
            domainTableContainer.innerHTML = '';
            return;
        }

        const maxCount = Math.max(...distribution.map(d => d.count), 1);

        domainDistributionChart.innerHTML = distribution.map(d => {
            const pct = Math.round((d.count / maxCount) * 100);
            return `
                <div class="domain-bar-row">
                    <div class="domain-bar-meta">
                        <span class="text-cyan font-mono">${escapeHTML(d.domain)}</span>
                        <span class="text-muted">${d.count} pages (${pct}%)</span>
                    </div>
                    <div class="domain-bar-track">
                        <div class="domain-bar-fill" style="width: ${pct}%;"></div>
                    </div>
                </div>
            `;
        }).join('');

        domainTableContainer.innerHTML = `
            <table class="domain-table">
                <thead>
                    <tr>
                        <th>DOMAIN / HOSTNAME</th>
                        <th>CRAWLED PAGES</th>
                        <th>SHARE</th>
                    </tr>
                </thead>
                <tbody>
                    ${distribution.map(d => {
                        const pct = Math.round((d.count / maxCount) * 100);
                        return `
                            <tr>
                                <td class="text-cyan">${escapeHTML(d.domain)}</td>
                                <td>${d.count}</td>
                                <td>${pct}%</td>
                            </tr>
                        `;
                    }).join('')}
                </tbody>
            </table>
        `;
    }

    // =========================================================================
    // 9. Search Execution & Filter Logic
    // =========================================================================
    searchForm.addEventListener('submit', (e) => {
        e.preventDefault();
        performSearch(searchInput.value.trim());
    });

    let searchDebounce = null;
    searchInput.addEventListener('input', () => {
        const q = searchInput.value.trim();
        clearSearchBtn.classList.toggle('hidden', q === '');
        clearTimeout(searchDebounce);
        searchDebounce = setTimeout(() => {
            performSearch(q);
        }, 320);
    });

    clearSearchBtn.addEventListener('click', () => {
        searchInput.value = '';
        clearSearchBtn.classList.add('hidden');
        performSearch('');
    });

    async function performSearch(query) {
        currentSearchQuery = query;
        resultsLoading.classList.remove('hidden');
        resultsEmpty.classList.add('hidden');
        resultsList.innerHTML = '';

        try {
            const results = await apiFetch(`/search?q=${encodeURIComponent(query)}`);
            cachedPages = results || [];
            updateFilterCounts();
            renderResults(cachedPages);
        } catch (err) {
            console.error("Search error:", err);
            resultsList.innerHTML = `
                <div class="feedback-banner feedback-error">
                    <span>Search error: ${escapeHTML(err.message)}</span>
                </div>
            `;
        } finally {
            resultsLoading.classList.add('hidden');
        }
    }

    function updateFilterCounts() {
        const total = cachedPages.length;
        const ok200 = cachedPages.filter(p => p.status_code === 200).length;
        const errs = cachedPages.filter(p => p.status_code !== 200).length;

        countAll.textContent = total;
        count200.textContent = ok200;
        countErr.textContent = errs;
    }

    filterChips.forEach(chip => {
        chip.addEventListener('click', () => {
            filterChips.forEach(c => c.classList.remove('active'));
            chip.classList.add('active');
            currentFilter = chip.getAttribute('data-filter');
            renderResults(cachedPages);
        });
    });

    function renderResults(pages) {
        let filtered = pages || [];
        if (currentFilter === '200') {
            filtered = filtered.filter(p => p.status_code === 200);
        } else if (currentFilter === 'error') {
            filtered = filtered.filter(p => p.status_code !== 200);
        }

        resultsMetaText.textContent = currentSearchQuery 
            ? `Found ${filtered.length} matches for "${currentSearchQuery}"` 
            : `Showing ${filtered.length} indexed pages`;

        if (filtered.length === 0) {
            resultsEmpty.classList.remove('hidden');
            resultsList.innerHTML = '';
            return;
        }

        resultsEmpty.classList.add('hidden');
        resultsList.innerHTML = filtered.map(item => {
            const statusCode = item.status_code || 200;
            const badgeClass = statusCode === 200 ? 'badge-200' : (statusCode === 403 ? 'badge-403' : 'badge-err');
            const badgeText = statusCode === 200 ? '200 OK' : (statusCode === 403 ? '403 ROBOTS' : `HTTP ${statusCode}`);

            const domain = item.domain || getDomainFromUrl(item.url || item.URL);
            const pageUrl = item.url || item.URL || '#';
            const title = item.title || item.Title || 'Untitled Document';
            const desc = item.description || item.Description || 'No description provided by meta tags.';

            return `
                <div class="result-card">
                    <div class="result-card-header">
                        <a href="${escapeHTML(pageUrl)}" target="_blank" rel="noopener" class="result-title">
                            ${escapeHTML(title)}
                        </a>
                        <span class="badge ${badgeClass}">${badgeText}</span>
                    </div>

                    <div class="result-url-row">
                        <span class="result-domain-pill">${escapeHTML(domain)}</span>
                        <a href="${escapeHTML(pageUrl)}" target="_blank" rel="noopener" class="result-url-text">${escapeHTML(pageUrl)}</a>
                    </div>

                    <p class="result-description">${escapeHTML(desc)}</p>

                    <div class="result-card-footer">
                        <span>Crawled ${item.latency_ms ? `${item.latency_ms}ms` : 'instant'}</span>
                        <div class="footer-actions">
                            <button type="button" class="action-link copy-btn" data-url="${escapeHTML(pageUrl)}">COPY URL</button>
                            <button type="button" class="action-link inspect-btn" data-json='${escapeJSONAttr(item)}'>INSPECT</button>
                        </div>
                    </div>
                </div>
            `;
        }).join('');

        // Attach action handlers for copy and inspect
        resultsList.querySelectorAll('.copy-btn').forEach(btn => {
            btn.addEventListener('click', () => {
                const url = btn.getAttribute('data-url');
                navigator.clipboard.writeText(url).then(() => {
                    const original = btn.textContent;
                    btn.textContent = 'COPIED!';
                    setTimeout(() => btn.textContent = original, 1500);
                });
            });
        });

        resultsList.querySelectorAll('.inspect-btn').forEach(btn => {
            btn.addEventListener('click', () => {
                try {
                    const raw = JSON.parse(btn.getAttribute('data-json'));
                    showPageDetails(raw);
                } catch (e) {
                    console.error("Inspect error", e);
                }
            });
        });
    }

    function getDomainFromUrl(urlStr) {
        try {
            return new URL(urlStr).hostname;
        } catch {
            return "host";
        }
    }

    // =========================================================================
    // 10. Seed Injection & Crawl Dispatch
    // =========================================================================
    seedForm.addEventListener('submit', async (e) => {
        e.preventDefault();
        const url = seedInput.value.trim();
        if (!url) return;

        submitSeedBtn.disabled = true;
        submitSeedBtn.innerHTML = `<span>ENQUEUING...</span>`;

        try {
            const data = await apiFetch('/submit', {
                method: 'POST',
                body: JSON.stringify({ urls: [url] })
            });

            showSeedFeedback(
                `Successfully enqueued seed URL (${data.enqueued || 1} new). Crawl scheduled in frontier.`,
                'success'
            );
            seedInput.value = '';
            updateDashboard();

        } catch (err) {
            showSeedFeedback(`Failed to enqueue seed: ${err.message}`, 'error');
        } finally {
            submitSeedBtn.disabled = false;
            submitSeedBtn.innerHTML = `<span>ENQUEUE & CRAWL</span>`;
        }
    });

    // Preset cards
    presetCards.forEach(card => {
        card.addEventListener('click', () => {
            const seed = card.getAttribute('data-seed');
            seedInput.value = seed;
            seedInput.focus();
        });
    });

    // Quick tag presets on empty search
    document.querySelectorAll('.preset-tag').forEach(tag => {
        tag.addEventListener('click', () => {
            const q = tag.getAttribute('data-query');
            searchInput.value = q;
            clearSearchBtn.classList.remove('hidden');
            performSearch(q);
        });
    });

    function showSeedFeedback(message, type = 'success') {
        seedFeedbackBanner.className = `feedback-banner feedback-${type}`;
        seedFeedbackBanner.textContent = message;
        seedFeedbackBanner.classList.remove('hidden');
        setTimeout(() => {
            seedFeedbackBanner.classList.add('hidden');
        }, 5000);
    }

    // Manual Crawl Step Trigger
    crawlStepBtn.addEventListener('click', async () => {
        crawlStepBtn.disabled = true;
        crawlStepBtn.innerHTML = `<span>CRAWLING...</span>`;

        try {
            const res = await apiFetch('/crawl', { method: 'POST' });
            if (res.status === 'queue_empty') {
                showSeedFeedback(res.message, 'error');
            } else if (res.page) {
                showSeedFeedback(`Crawl step executed on ${res.page.url}. Extracted ${res.new_links_enqueued || 0} links.`, 'success');
            }
            updateDashboard();
        } catch (err) {
            showSeedFeedback(`Crawl step failed: ${err.message}`, 'error');
        } finally {
            crawlStepBtn.disabled = false;
            crawlStepBtn.innerHTML = `
                <svg class="btn-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><polygon points="5 3 19 12 5 21 5 3"></polygon></svg>
                <span>CRAWL STEP</span>
            `;
        }
    });

    // Auto-Crawl Switch
    autoCrawlToggle.addEventListener('click', () => {
        const isChecked = autoCrawlToggle.getAttribute('aria-checked') === 'true';
        const nextState = !isChecked;
        autoCrawlToggle.setAttribute('aria-checked', String(nextState));

        if (nextState) {
            // Start automatic periodic crawling
            autoCrawlTimer = setInterval(async () => {
                try {
                    await apiFetch('/crawl', { method: 'POST' });
                    updateDashboard();
                } catch (e) {
                    console.warn("Auto crawl loop error:", e);
                }
            }, 3500);
            showSeedFeedback("Auto-Crawl daemon loop activated.", "success");
        } else {
            clearInterval(autoCrawlTimer);
            autoCrawlTimer = null;
            showSeedFeedback("Auto-Crawl paused.", "error");
        }
    });

    // =========================================================================
    // 11. Tab Switching
    // =========================================================================
    tabButtons.forEach(btn => {
        btn.addEventListener('click', () => {
            tabButtons.forEach(b => b.classList.remove('active'));
            tabPanes.forEach(p => p.classList.remove('active'));

            btn.classList.add('active');
            const target = btn.getAttribute('data-tab');
            const pane = document.getElementById(target);
            if (pane) pane.classList.add('active');
        });
    });

    // =========================================================================
    // 12. Stream Control (Pause / Clear)
    // =========================================================================
    toggleStreamBtn.addEventListener('click', () => {
        isStreamPaused = !isStreamPaused;
        toggleStreamBtn.textContent = isStreamPaused ? 'RESUME' : 'PAUSE';
        toggleStreamBtn.classList.toggle('active', isStreamPaused);
    });

    clearStreamBtn.addEventListener('click', () => {
        terminalFeed.innerHTML = `<div class="event-url text-muted">Stream cleared by operator.</div>`;
    });

    // =========================================================================
    // 13. Wipe & Reset Modal
    // =========================================================================
    openResetBtn.addEventListener('click', () => {
        resetModal.classList.remove('hidden');
    });

    cancelResetBtn.addEventListener('click', () => {
        resetModal.classList.add('hidden');
    });

    confirmResetBtn.addEventListener('click', async () => {
        confirmResetBtn.disabled = true;
        confirmResetBtn.textContent = 'PURGING...';

        try {
            await apiFetch('/reset', { method: 'POST' });
            resetModal.classList.add('hidden');
            searchInput.value = '';
            cachedPages = [];
            currentSearchQuery = '';
            showSeedFeedback("Cluster wiped and reset. Re-seeded with https://go.dev.", "success");
            updateDashboard();
        } catch (err) {
            alert(`Reset failed: ${err.message}`);
        } finally {
            confirmResetBtn.disabled = false;
            confirmResetBtn.textContent = 'CONFIRM WIPE & RESET';
        }
    });

    // =========================================================================
    // 14. Settings / Cluster Config Modal
    // =========================================================================
    openSettingsBtn.addEventListener('click', () => {
        customApiInput.value = localStorage.getItem("crawler_api_base") || "";
        settingsModal.classList.remove('hidden');
    });

    closeSettingsBtn.addEventListener('click', () => {
        settingsModal.classList.add('hidden');
    });

    saveSettingsBtn.addEventListener('click', () => {
        const val = customApiInput.value.trim();
        setApiBase(val);
        settingsModal.classList.add('hidden');
        showSeedFeedback(`Backend endpoint changed to: ${getApiBase()}`, "success");
    });

    urlPresetPills.forEach(pill => {
        pill.addEventListener('click', () => {
            customApiInput.value = pill.getAttribute('data-url');
        });
    });

    // Close modals on backdrop click or ESC
    [resetModal, settingsModal, pageDetailModal].forEach(m => {
        m.addEventListener('click', (e) => {
            if (e.target === m) m.classList.add('hidden');
        });
    });

    window.addEventListener('keydown', (e) => {
        if (e.key === 'Escape') {
            resetModal.classList.add('hidden');
            settingsModal.classList.add('hidden');
            pageDetailModal.classList.add('hidden');
        }
        // Quick focus search on '/'
        if (e.key === '/' && document.activeElement !== searchInput && document.activeElement !== seedInput) {
            e.preventDefault();
            searchInput.focus();
        }
    });

    // =========================================================================
    // 15. Page Detail Inspector Modal
    // =========================================================================
    closeDetailBtn.addEventListener('click', () => {
        pageDetailModal.classList.add('hidden');
    });

    function showPageDetails(item) {
        detailPageTitle.textContent = item.title || item.Title || 'PAGE INSPECTOR';
        const pageUrl = item.url || item.URL || '';
        const status = item.status_code || item.StatusCode || 200;

        detailPageContent.innerHTML = `
            <div class="detail-meta-list">
                <div class="detail-row">
                    <span class="detail-key">TARGET URL</span>
                    <a href="${escapeHTML(pageUrl)}" target="_blank" rel="noopener" class="detail-val text-cyan">${escapeHTML(pageUrl)}</a>
                </div>
                <div class="detail-row">
                    <span class="detail-key">HTTP STATUS</span>
                    <span class="detail-val ${status === 200 ? 'text-emerald' : 'text-rose'}">${status}</span>
                </div>
                <div class="detail-row">
                    <span class="detail-key">HOST / DOMAIN</span>
                    <span class="detail-val font-mono">${escapeHTML(item.domain || getDomainFromUrl(pageUrl))}</span>
                </div>
                <div class="detail-row">
                    <span class="detail-key">LATENCY</span>
                    <span class="detail-val font-mono">${item.latency_ms ? `${item.latency_ms}ms` : 'N/A'}</span>
                </div>
                <div class="detail-row">
                    <span class="detail-key">TITLE</span>
                    <span class="detail-val">${escapeHTML(item.title || item.Title || 'None')}</span>
                </div>
                <div class="detail-row">
                    <span class="detail-key">META DESCRIPTION</span>
                    <span class="detail-val text-muted">${escapeHTML(item.description || item.Description || 'None')}</span>
                </div>
                <div class="detail-row">
                    <span class="detail-key">DISCOVERED LINKS</span>
                    <span class="detail-val font-mono">${item.links_count || (item.links ? item.links.length : 'N/A')} links</span>
                </div>
            </div>
        `;
        pageDetailModal.classList.remove('hidden');
    }

    // =========================================================================
    // 16. Utility Helpers
    // =========================================================================
    function escapeHTML(str) {
        if (!str) return "";
        return String(str)
            .replace(/&/g, "&amp;")
            .replace(/</g, "&lt;")
            .replace(/>/g, "&gt;")
            .replace(/"/g, "&quot;")
            .replace(/'/g, "&#039;");
    }

    function escapeJSONAttr(obj) {
        try {
            return JSON.stringify(obj)
                .replace(/'/g, "&apos;")
                .replace(/"/g, "&quot;");
        } catch {
            return "{}";
        }
    }

    // =========================================================================
    // 17. Initialization
    // =========================================================================
    updateDashboard();
    setInterval(updateDashboard, 3000);
});
