/**
 * GOPHER CRAWLER — WEB APPLICATION CONTROLLER (v1.2.0)
 * Production-Inspired Distributed Web Crawling, Indexing & Telemetry Console
 * Strictly verified data, reactive telemetry polling, and tiered governance.
 */

(() => {
    'use strict';

    // =========================================================================
    // 1. STATE & CONFIGURATION
    // =========================================================================
    const DEFAULT_API = '/api';
    const state = {
        apiBase: localStorage.getItem('crawler_api_base') || DEFAULT_API,
        currentView: 'view-overview',
        autoCrawlActive: false,
        autoCrawlTimer: null,
        telemetryTimer: null,
        feedPaused: false,
        activeSearchFilter: '0',
        activeJobFilter: 'ALL',
        latestStatus: null,
        cachedPages: [],
        cachedJobs: [],
        cachedWorkers: [],
        cachedDomains: [],
        cachedEvents: []
    };

    // =========================================================================
    // 2. DOM ELEMENT REFERENCES
    // =========================================================================
    const DOM = {
        // Navigation & Header
        navItems: document.querySelectorAll('.nav-item[data-view]'),
        viewPanels: document.querySelectorAll('.view-panel'),
        breadcrumbTitle: document.getElementById('breadcrumb-title'),
        sidebarStatusBox: document.getElementById('sidebar-status-box'),
        globalPulseDot: document.getElementById('global-pulse-dot'),
        globalStatusText: document.getElementById('global-status-text'),
        globalModeTag: document.getElementById('global-mode-tag'),
        sidebarJobCount: document.getElementById('sidebar-job-count'),
        sidebarWorkerCount: document.getElementById('sidebar-worker-count'),
        autoCrawlToggle: document.getElementById('auto-crawl-toggle'),
        crawlStepBtn: document.getElementById('crawl-step-btn'),
        quickResetBtn: document.getElementById('quick-reset-btn'),

        // Overview View
        ovPagesCount: document.getElementById('ov-pages-count'),
        ovPagesSub: document.getElementById('ov-pages-sub'),
        ovDomainsCount: document.getElementById('ov-domains-count'),
        ovDomainsSub: document.getElementById('ov-domains-sub'),
        ovQueueCount: document.getElementById('ov-queue-count'),
        ovQueueSub: document.getElementById('ov-queue-sub'),
        ovWorkersCount: document.getElementById('ov-workers-count'),
        ovWorkersSub: document.getElementById('ov-workers-sub'),
        ovRecentPages: document.getElementById('ov-recent-pages'),
        ovWorkersList: document.getElementById('ov-workers-list'),
        ovUptimeVal: document.getElementById('ov-uptime-val'),

        // Crawl Explorer View
        crawlExplorerForm: document.getElementById('crawl-explorer-form'),
        explorerUrlInput: document.getElementById('explorer-url-input'),
        explorerDepthInput: document.getElementById('explorer-depth-input'),
        explorerPagesInput: document.getElementById('explorer-pages-input'),
        explorerPolitenessInput: document.getElementById('explorer-politeness-input'),
        presetPills: document.querySelectorAll('.preset-pill'),
        explorerFeedback: document.getElementById('explorer-feedback'),
        explorerSubmitBtn: document.getElementById('explorer-submit-btn'),

        // Jobs View
        jobStatusFilters: document.getElementById('job-status-filters'),
        jobsTbody: document.getElementById('jobs-tbody'),

        // Search Index View
        searchIndexForm: document.getElementById('search-index-form'),
        searchMainInput: document.getElementById('search-main-input'),
        searchClearBtn: document.getElementById('search-clear-btn'),
        searchChips: document.querySelectorAll('.chip-item[data-filter]'),
        searchTallyText: document.getElementById('search-tally-text'),
        searchResultsContainer: document.getElementById('search-results-container'),

        // Domains View
        domainsBarsView: document.getElementById('domains-bars-view'),

        // Workers View
        workersGridContainer: document.getElementById('workers-grid-container'),
        workersLivePulse: document.getElementById('workers-live-pulse'),

        // Activity Feed View
        activityTerminalFeed: document.getElementById('activity-terminal-feed'),
        feedPauseBtn: document.getElementById('feed-pause-btn'),
        feedClearBtn: document.getElementById('feed-clear-btn'),

        // Analytics View
        anAvgLatency: document.getElementById('an-avg-latency'),
        anSuccessRate: document.getElementById('an-success-rate'),
        anRobotsRate: document.getElementById('an-robots-rate'),
        anLeaseReclaims: document.getElementById('an-lease-reclaims'),

        // Settings View
        backendUrlInput: document.getElementById('backend-url-input'),
        saveBackendUrlBtn: document.getElementById('save-backend-url-btn'),
        btnResetUi: document.getElementById('btn-reset-ui'),
        btnResetQueue: document.getElementById('btn-reset-queue'),
        btnResetData: document.getElementById('btn-reset-data'),
        btnResetProd: document.getElementById('btn-reset-prod'),

        // Production Reset Modal
        prodResetModal: document.getElementById('prod-reset-modal'),
        prodResetPhraseInput: document.getElementById('prod-reset-phrase-input'),
        cancelProdResetBtn: document.getElementById('cancel-prod-reset-btn'),
        confirmProdResetBtn: document.getElementById('confirm-prod-reset-btn'),

        // Page Inspector Modal
        pageInspectorModal: document.getElementById('page-inspector-modal'),
        inspectorModalTitle: document.getElementById('inspector-modal-title'),
        inspectorModalBody: document.getElementById('inspector-modal-body'),
        closeInspectorBtn: document.getElementById('close-inspector-btn')
    };

    // =========================================================================
    // 3. UTILITY FUNCTIONS
    // =========================================================================
    function getApiUrl(path) {
        const base = state.apiBase.replace(/\/+$/, '');
        const cleanPath = path.startsWith('/') ? path : `/${path}`;
        return `${base}${cleanPath}`;
    }

    async function apiRequest(endpoint, options = {}) {
        const url = getApiUrl(endpoint);
        try {
            const res = await fetch(url, {
                ...options,
                headers: {
                    'Accept': 'application/json',
                    'Content-Type': 'application/json',
                    ...(options.headers || {})
                }
            });

            if (!res.ok) {
                let errDetail = res.statusText;
                try {
                    const errJson = await res.json();
                    if (errJson.error) errDetail = errJson.error;
                } catch {
                    // Fall back to statusText
                }
                throw new Error(`${errDetail} (${res.status})`);
            }

            return await res.json();
        } catch (err) {
            console.warn(`[API] Error calling ${endpoint}:`, err);
            throw err;
        }
    }

    function escapeHtml(str) {
        if (!str) return '';
        return String(str)
            .replace(/&/g, '&amp;')
            .replace(/</g, '&lt;')
            .replace(/>/g, '&gt;')
            .replace(/"/g, '&quot;')
            .replace(/'/g, '&#39;');
    }

    function formatNumber(num) {
        if (num === null || num === undefined || isNaN(num)) return '0';
        return Number(num).toLocaleString();
    }

    function formatBytes(bytes) {
        if (!bytes || bytes === 0) return '0 B';
        const k = 1024;
        const sizes = ['B', 'KB', 'MB', 'GB'];
        const i = Math.floor(Math.log(bytes) / Math.log(k));
        return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
    }

    function formatRelativeTime(dateStr) {
        if (!dateStr) return 'never';
        const date = new Date(dateStr);
        const now = new Date();
        const diffMs = now - date;
        const diffSec = Math.floor(diffMs / 1000);
        if (diffSec < 5) return 'just now';
        if (diffSec < 60) return `${diffSec}s ago`;
        const diffMin = Math.floor(diffSec / 60);
        if (diffMin < 60) return `${diffMin}m ago`;
        const diffHr = Math.floor(diffMin / 60);
        if (diffHr < 24) return `${diffHr}h ago`;
        return date.toLocaleDateString();
    }

    function showToast(message, type = 'info') {
        let toastContainer = document.getElementById('platform-toast-container');
        if (!toastContainer) {
            toastContainer = document.createElement('div');
            toastContainer.id = 'platform-toast-container';
            toastContainer.className = 'toast-container';
            document.body.appendChild(toastContainer);
        }

        const toast = document.createElement('div');
        toast.className = `toast-item toast-${type}`;
        toast.innerHTML = `
            <div class="toast-dot"></div>
            <div class="toast-text">${escapeHtml(message)}</div>
        `;
        toastContainer.appendChild(toast);

        setTimeout(() => {
            toast.classList.add('toast-show');
        }, 10);

        setTimeout(() => {
            toast.classList.remove('toast-show');
            setTimeout(() => toast.remove(), 300);
        }, 4000);
    }

    // =========================================================================
    // 4. NAVIGATION & VIEW SWITCHING
    // =========================================================================
    function switchView(viewId) {
        if (!viewId) return;

        // Update nav buttons
        DOM.navItems.forEach(item => {
            if (item.dataset.view === viewId) {
                item.classList.add('active');
            } else {
                item.classList.remove('active');
            }
        });

        // Update view panels
        DOM.viewPanels.forEach(panel => {
            if (panel.id === viewId) {
                panel.classList.add('active');
            } else {
                panel.classList.remove('active');
            }
        });

        state.currentView = viewId;
        window.location.hash = viewId.replace('view-', '');

        // Update header breadcrumb
        const titleMap = {
            'view-overview': 'OVERVIEW',
            'view-explorer': 'CRAWL EXPLORER',
            'view-jobs': 'JOBS MANAGER',
            'view-search': 'SEARCH INDEX',
            'view-domains': 'DOMAINS REGISTRY',
            'view-workers': 'WORKER FLEET',
            'view-activity': 'ACTIVITY LOGS',
            'view-analytics': 'PERFORMANCE & ANALYTICS',
            'view-settings': 'SETTINGS & RESETS',
            'view-docs': 'DOCUMENTATION'
        };
        DOM.breadcrumbTitle.textContent = titleMap[viewId] || 'TELEMETRY';

        // Refresh view data on switch
        refreshCurrentView();
    }

    function initNavigation() {
        DOM.navItems.forEach(btn => {
            btn.addEventListener('click', () => {
                switchView(btn.dataset.view);
            });
        });

        // Quick action link buttons (e.g. "VIEW ALL", "FLEET VIEW")
        document.querySelectorAll('[data-view-target]').forEach(link => {
            link.addEventListener('click', () => {
                switchView(link.getAttribute('data-view-target'));
            });
        });

        // Quick reset button in header jumps to settings view
        if (DOM.quickResetBtn) {
            DOM.quickResetBtn.addEventListener('click', () => {
                switchView('view-settings');
            });
        }

        // Handle initial hash in URL
        const hash = window.location.hash.replace('#', '');
        if (hash) {
            const target = `view-${hash}`;
            if (document.getElementById(target)) {
                switchView(target);
                return;
            }
        }
        switchView('view-overview');
    }

    // =========================================================================
    // 5. TELEMETRY POLLING & STATUS UPDATES
    // =========================================================================
    async function fetchSystemStatus() {
        try {
            const data = await apiRequest('/status');
            state.latestStatus = data;

            // Update top badges
            if (DOM.globalStatusText) {
                DOM.globalStatusText.textContent = data.status === 'ok' ? 'SYSTEM ONLINE' : 'DEGRADED';
            }
            if (DOM.globalModeTag) {
                const modeLabel = data.operating_mode ? `[${data.operating_mode.toUpperCase()}]` : '[MODE A: CLUSTER]';
                DOM.globalModeTag.textContent = modeLabel;
            }
            if (DOM.globalPulseDot) {
                DOM.globalPulseDot.className = 'status-indicator-dot online';
            }

            // Update Overview Metrics
            if (DOM.ovPagesCount) DOM.ovPagesCount.textContent = formatNumber(data.pages_crawled || 0);
            if (DOM.ovDomainsCount) DOM.ovDomainsCount.textContent = formatNumber(data.domains_discovered || 0);
            if (DOM.ovQueueCount) DOM.ovQueueCount.textContent = formatNumber(data.queue_size || 0);
            if (DOM.ovWorkersCount) DOM.ovWorkersCount.textContent = formatNumber(data.workers_active || (data.workers ? data.workers.length : 0));
            if (DOM.ovUptimeVal) DOM.ovUptimeVal.textContent = data.uptime || 'active';

            if (DOM.ovQueueSub) {
                const activeLeases = data.leases_active || 0;
                DOM.ovQueueSub.textContent = `${activeLeases} lease${activeLeases === 1 ? '' : 's'} active`;
            }

            // Sidebar counters
            if (DOM.sidebarWorkerCount) {
                DOM.sidebarWorkerCount.textContent = data.workers_active || (data.workers ? data.workers.length : 0);
            }

            // Overview recent pages stream
            if (data.recent_pages && data.recent_pages.length > 0) {
                renderRecentPages(data.recent_pages);
            }

            // Overview workers snapshot
            if (data.workers) {
                renderWorkersSnapshot(data.workers);
            }

            return data;
        } catch (err) {
            if (DOM.globalStatusText) DOM.globalStatusText.textContent = 'DISCONNECTED';
            if (DOM.globalPulseDot) DOM.globalPulseDot.className = 'status-indicator-dot offline';
            if (DOM.globalModeTag) DOM.globalModeTag.textContent = '[UNREACHABLE]';
            return null;
        }
    }

    function renderRecentPages(pages) {
        if (!DOM.ovRecentPages) return;
        DOM.ovRecentPages.innerHTML = pages.map(p => {
            const statusClass = p.status_code === 200 ? 'status-pill-ok' : 'status-pill-err';
            return `
                <div class="recent-page-card" data-url="${escapeHtml(p.url)}">
                    <div class="card-status-col">
                        <span class="status-code-badge ${statusClass}">${p.status_code || 200}</span>
                    </div>
                    <div class="card-content-col">
                        <div class="card-title-row">
                            <h4 class="card-title">${escapeHtml(p.title || p.url)}</h4>
                            <span class="card-latency font-mono">${p.latency_ms || 0}ms</span>
                        </div>
                        <a href="${escapeHtml(p.url)}" target="_blank" rel="noopener noreferrer" class="card-url font-mono">${escapeHtml(p.url)}</a>
                    </div>
                </div>
            `;
        }).join('');

        // Wire click to open page inspector
        DOM.ovRecentPages.querySelectorAll('.recent-page-card').forEach(card => {
            card.addEventListener('click', (e) => {
                if (e.target.tagName === 'A') return; // Allow direct link clicks
                const url = card.dataset.url;
                const page = pages.find(p => p.url === url);
                if (page) openInspectorModal(page);
            });
        });
    }

    function renderWorkersSnapshot(workers) {
        if (!DOM.ovWorkersList) return;
        if (!workers || workers.length === 0) {
            DOM.ovWorkersList.innerHTML = `
                <div class="empty-state-notice">
                    <span>No worker nodes currently reporting heartbeats</span>
                </div>
            `;
            return;
        }

        DOM.ovWorkersList.innerHTML = workers.slice(0, 5).map(w => {
            const isLive = w.status === 'ACTIVE' || w.status === 'IDLE';
            const badgeClass = isLive ? 'badge-emerald' : 'badge-amber';
            return `
                <div class="worker-snap-row">
                    <div class="worker-snap-id font-mono">
                        <span class="status-mini-dot ${isLive ? 'dot-online' : 'dot-warn'}"></span>
                        ${escapeHtml(w.worker_id || 'worker-node')}
                    </div>
                    <div class="worker-snap-meta">
                        <span class="worker-snap-status ${badgeClass}">${escapeHtml(w.status || 'UNKNOWN')}</span>
                        <span class="worker-snap-tasks font-mono">${w.tasks_completed || 0} tasks</span>
                    </div>
                </div>
            `;
        }).join('');
    }

    // =========================================================================
    // 6. AUTO-CRAWL & SINGLE STEP DISPATCHER
    // =========================================================================
    function toggleAutoCrawl() {
        state.autoCrawlActive = !state.autoCrawlActive;
        if (DOM.autoCrawlToggle) {
            DOM.autoCrawlToggle.classList.toggle('active', state.autoCrawlActive);
            DOM.autoCrawlToggle.setAttribute('aria-checked', state.autoCrawlActive ? 'true' : 'false');
        }

        if (state.autoCrawlActive) {
            showToast('Auto-Crawl loop activated', 'success');
            runAutoCrawlStep();
            state.autoCrawlTimer = setInterval(runAutoCrawlStep, 2500);
        } else {
            showToast('Auto-Crawl loop paused', 'info');
            if (state.autoCrawlTimer) {
                clearInterval(state.autoCrawlTimer);
                state.autoCrawlTimer = null;
            }
        }
    }

    async function runAutoCrawlStep() {
        try {
            const res = await apiRequest('/crawl', { method: 'POST' });
            if (res.crawled && res.crawled.url) {
                showToast(`Crawled: ${res.crawled.url} (${res.crawled.status_code || 200})`, 'info');
            }
            fetchSystemStatus();
            if (state.currentView === 'view-activity') fetchActivityLogs();
        } catch (err) {
            // Queue empty or quiet error
            if (err.message && err.message.includes('Queue is empty')) {
                // Pause auto crawl if frontier is exhausted
                if (state.autoCrawlActive) {
                    toggleAutoCrawl();
                    showToast('Frontier queue exhausted — auto-crawl stopped', 'info');
                }
            }
        }
    }

    async function handleManualCrawlStep() {
        if (!DOM.crawlStepBtn) return;
        DOM.crawlStepBtn.disabled = true;
        const origContent = DOM.crawlStepBtn.innerHTML;
        DOM.crawlStepBtn.innerHTML = `<span>DISPATCHING...</span>`;

        try {
            const res = await apiRequest('/crawl', { method: 'POST' });
            if (res.crawled && res.crawled.url) {
                showToast(`Step Dispatched: ${res.crawled.url} (${res.crawled.status_code || 200})`, 'success');
            } else if (res.message) {
                showToast(res.message, 'info');
            }
            await fetchSystemStatus();
            if (state.currentView === 'view-search') executeSearch();
        } catch (err) {
            showToast(`Crawl step failed: ${err.message}`, 'error');
        } finally {
            DOM.crawlStepBtn.disabled = false;
            DOM.crawlStepBtn.innerHTML = origContent;
        }
    }

    // =========================================================================
    // 7. CRAWL EXPLORER (VIEW 2)
    // =========================================================================
    function initCrawlExplorer() {
        if (!DOM.crawlExplorerForm) return;

        // Preset seed buttons
        DOM.presetPills.forEach(pill => {
            pill.addEventListener('click', () => {
                const seedUrl = pill.dataset.seed;
                if (!seedUrl) return;
                const current = DOM.explorerUrlInput.value.trim();
                if (!current) {
                    DOM.explorerUrlInput.value = seedUrl;
                } else if (!current.includes(seedUrl)) {
                    DOM.explorerUrlInput.value = `${current}\n${seedUrl}`;
                }
                DOM.explorerUrlInput.focus();
            });
        });

        // Submit form
        DOM.crawlExplorerForm.addEventListener('submit', async (e) => {
            e.preventDefault();
            const rawUrls = DOM.explorerUrlInput.value.trim();
            if (!rawUrls) {
                showToast('Please enter at least one seed URL', 'error');
                return;
            }

            const urls = rawUrls.split('\n')
                .map(u => u.trim())
                .filter(u => u.length > 0);

            const depth = parseInt(DOM.explorerDepthInput.value, 10) || 3;
            const pages = parseInt(DOM.explorerPagesInput.value, 10) || 100;
            const politeness = parseInt(DOM.explorerPolitenessInput.value, 10) || 1000;

            DOM.explorerSubmitBtn.disabled = true;
            DOM.explorerSubmitBtn.innerHTML = `<span>LAUNCHING DISPATCH...</span>`;

            try {
                const res = await apiRequest('/submit', {
                    method: 'POST',
                    body: JSON.stringify({
                        urls,
                        max_depth: depth,
                        max_pages: pages,
                        politeness_delay_ms: politeness
                    })
                });

                showToast(`Crawl job initiated! Added ${urls.length} seeds.`, 'success');

                // Render feedback banner
                if (DOM.explorerFeedback) {
                    DOM.explorerFeedback.className = 'feedback-banner feedback-success';
                    DOM.explorerFeedback.innerHTML = `
                        <div class="feedback-icon">✓</div>
                        <div class="feedback-text">
                            <strong>Job Successfully Registered!</strong><br>
                            Job ID: <code class="font-mono">${escapeHtml(res.job_id || 'job-auto')}</code> • Seeds Enqueued: ${urls.length}<br>
                            SSRF validation passed. Frontier queue updated.
                        </div>
                    `;
                    DOM.explorerFeedback.classList.remove('hidden');
                }

                DOM.explorerUrlInput.value = '';
                await fetchSystemStatus();
            } catch (err) {
                showToast(`Job launch failed: ${err.message}`, 'error');
                if (DOM.explorerFeedback) {
                    DOM.explorerFeedback.className = 'feedback-banner feedback-error';
                    DOM.explorerFeedback.innerHTML = `
                        <div class="feedback-icon">✕</div>
                        <div class="feedback-text">
                            <strong>Submission Error:</strong> ${escapeHtml(err.message)}
                        </div>
                    `;
                    DOM.explorerFeedback.classList.remove('hidden');
                }
            } finally {
                DOM.explorerSubmitBtn.disabled = false;
                DOM.explorerSubmitBtn.innerHTML = `<span>LAUNCH CRAWL JOB</span>`;
            }
        });
    }

    // =========================================================================
    // 8. JOBS MANAGER (VIEW 3)
    // =========================================================================
    async function fetchJobs() {
        if (!DOM.jobsTbody) return;
        try {
            const data = await apiRequest('/jobs');
            const jobs = data.jobs || [];
            state.cachedJobs = jobs;

            if (DOM.sidebarJobCount) {
                DOM.sidebarJobCount.textContent = jobs.length;
            }

            renderJobsTable(jobs);
        } catch (err) {
            DOM.jobsTbody.innerHTML = `
                <tr>
                    <td colspan="7" class="text-center text-muted">Failed to load crawl jobs: ${escapeHtml(err.message)}</td>
                </tr>
            `;
        }
    }

    function renderJobsTable(jobs) {
        if (!DOM.jobsTbody) return;

        // Apply filter
        const filtered = state.activeJobFilter === 'ALL'
            ? jobs
            : jobs.filter(j => j.status === state.activeJobFilter);

        if (filtered.length === 0) {
            DOM.jobsTbody.innerHTML = `
                <tr>
                    <td colspan="7" class="text-center text-muted" style="padding: 32px;">No crawl jobs match the selected filter.</td>
                </tr>
            `;
            return;
        }

        DOM.jobsTbody.innerHTML = filtered.map(job => {
            const statusClassMap = {
                'RUNNING': 'badge-emerald',
                'COMPLETED': 'badge-cyan',
                'PAUSED': 'badge-amber',
                'CANCELLED': 'badge-rose'
            };
            const badgeClass = statusClassMap[job.status] || 'badge-cyan';

            const canPause = job.status === 'RUNNING';
            const canResume = job.status === 'PAUSED';
            const canCancel = job.status === 'RUNNING' || job.status === 'PAUSED';

            return `
                <tr data-job-id="${escapeHtml(job.id)}">
                    <td class="font-mono text-cyan">${escapeHtml(job.id.substring(0, 8))}...</td>
                    <td><strong>${escapeHtml(job.name || 'Crawl Job')}</strong></td>
                    <td><span class="badge ${badgeClass}">${escapeHtml(job.status)}</span></td>
                    <td class="font-mono">${job.max_depth || 3}</td>
                    <td class="font-mono">${formatNumber(job.crawled_pages || 0)} / ${formatNumber(job.max_pages || 100)}</td>
                    <td class="font-mono text-muted">${formatRelativeTime(job.created_at)}</td>
                    <td>
                        <div class="row-action-btns">
                            ${canPause ? `<button type="button" class="btn-job-action btn-job-pause" data-action="pause" data-id="${escapeHtml(job.id)}">Pause</button>` : ''}
                            ${canResume ? `<button type="button" class="btn-job-action btn-job-resume" data-action="resume" data-id="${escapeHtml(job.id)}">Resume</button>` : ''}
                            ${canCancel ? `<button type="button" class="btn-job-action btn-job-cancel text-rose" data-action="cancel" data-id="${escapeHtml(job.id)}">Cancel</button>` : ''}
                        </div>
                    </td>
                </tr>
            `;
        }).join('');

        // Wire action buttons
        DOM.jobsTbody.querySelectorAll('.btn-job-action').forEach(btn => {
            btn.addEventListener('click', async () => {
                const jobId = btn.dataset.id;
                const action = btn.dataset.action;
                try {
                    await apiRequest(`/jobs/${jobId}/${action}`, { method: 'POST' });
                    showToast(`Job ${jobId.substring(0, 8)} ${action}d`, 'info');
                    fetchJobs();
                } catch (err) {
                    showToast(`Job action failed: ${err.message}`, 'error');
                }
            });
        });
    }

    function initJobsView() {
        if (!DOM.jobStatusFilters) return;
        DOM.jobStatusFilters.querySelectorAll('.filter-pill').forEach(btn => {
            btn.addEventListener('click', () => {
                DOM.jobStatusFilters.querySelectorAll('.filter-pill').forEach(b => b.classList.remove('active'));
                btn.classList.add('active');
                state.activeJobFilter = btn.dataset.filter || 'ALL';
                renderJobsTable(state.cachedJobs);
            });
        });
    }

    // =========================================================================
    // 9. SEARCH INDEX (VIEW 4)
    // =========================================================================
    async function executeSearch() {
        if (!DOM.searchResultsContainer) return;
        const query = DOM.searchMainInput.value.trim();
        const status = state.activeSearchFilter;

        DOM.searchResultsContainer.innerHTML = `
            <div class="loading-state-box">
                <span class="loading-spinner"></span>
                <span>Executing ranked PostgreSQL query...</span>
            </div>
        `;

        try {
            const params = new URLSearchParams();
            if (query) params.set('q', query);
            if (status && status !== '0') params.set('status', status);

            const res = await apiRequest(`/search?${params.toString()}`);
            const results = res.results || [];
            state.cachedPages = results;

            if (DOM.searchTallyText) {
                DOM.searchTallyText.textContent = `${results.length} page${results.length === 1 ? '' : 's'} indexed`;
            }

            renderSearchResults(results);
        } catch (err) {
            DOM.searchResultsContainer.innerHTML = `
                <div class="empty-state-notice">
                    <span>Search failed: ${escapeHtml(err.message)}</span>
                </div>
            `;
        }
    }

    function renderSearchResults(results) {
        if (!DOM.searchResultsContainer) return;
        if (results.length === 0) {
            DOM.searchResultsContainer.innerHTML = `
                <div class="empty-state-notice">
                    <span>No crawled pages match your query or filters</span>
                </div>
            `;
            return;
        }

        DOM.searchResultsContainer.innerHTML = results.map(item => {
            const isOk = item.status_code === 200;
            const badgeClass = isOk ? 'status-pill-ok' : 'status-pill-err';
            const linksCount = (item.internal_links ? item.internal_links.length : 0) + (item.external_links ? item.external_links.length : 0);

            return `
                <div class="search-result-card" data-url="${escapeHtml(item.url)}">
                    <div class="res-header-row">
                        <span class="status-code-badge ${badgeClass}">${item.status_code || 200}</span>
                        <h4 class="res-title">${escapeHtml(item.title || item.url)}</h4>
                        <span class="res-latency font-mono">${item.latency_ms || 0}ms</span>
                    </div>
                    <a href="${escapeHtml(item.url)}" target="_blank" rel="noopener noreferrer" class="res-url-link font-mono">${escapeHtml(item.url)}</a>
                    ${item.meta_description ? `<p class="res-snippet">${escapeHtml(item.meta_description)}</p>` : ''}
                    <div class="res-meta-row">
                        <span class="res-meta-tag">Depth: ${item.depth || 0}</span>
                        <span class="res-meta-tag">Size: ${formatBytes(item.content_length)}</span>
                        <span class="res-meta-tag">Links: ${linksCount || item.links_count || 0}</span>
                        <button type="button" class="btn-inspect-page" data-url="${escapeHtml(item.url)}">INSPECT PAGE</button>
                    </div>
                </div>
            `;
        }).join('');

        // Wire inspect buttons
        DOM.searchResultsContainer.querySelectorAll('.btn-inspect-page').forEach(btn => {
            btn.addEventListener('click', (e) => {
                e.stopPropagation();
                const url = btn.dataset.url;
                const page = results.find(p => p.url === url);
                if (page) openInspectorModal(page);
            });
        });

        // Wire entire card click
        DOM.searchResultsContainer.querySelectorAll('.search-result-card').forEach(card => {
            card.addEventListener('click', (e) => {
                if (e.target.tagName === 'A' || e.target.classList.contains('btn-inspect-page')) return;
                const url = card.dataset.url;
                const page = results.find(p => p.url === url);
                if (page) openInspectorModal(page);
            });
        });
    }

    function initSearchView() {
        if (!DOM.searchIndexForm) return;

        DOM.searchIndexForm.addEventListener('submit', (e) => {
            e.preventDefault();
            executeSearch();
        });

        if (DOM.searchClearBtn) {
            DOM.searchClearBtn.addEventListener('click', () => {
                DOM.searchMainInput.value = '';
                DOM.searchClearBtn.classList.add('hidden');
                executeSearch();
            });
        }

        if (DOM.searchMainInput) {
            DOM.searchMainInput.addEventListener('input', () => {
                if (DOM.searchMainInput.value.length > 0) {
                    DOM.searchClearBtn.classList.remove('hidden');
                } else {
                    DOM.searchClearBtn.classList.add('hidden');
                }
            });
        }

        // Status filter chips
        DOM.searchChips.forEach(chip => {
            chip.addEventListener('click', () => {
                DOM.searchChips.forEach(c => c.classList.remove('active'));
                chip.classList.add('active');
                state.activeSearchFilter = chip.dataset.filter || '0';
                executeSearch();
            });
        });
    }

    // =========================================================================
    // 10. DOMAINS REGISTRY (VIEW 5)
    // =========================================================================
    async function fetchDomains() {
        if (!DOM.domainsBarsView) return;
        try {
            const res = await apiRequest('/domains');
            const domains = res.domains || [];
            state.cachedDomains = domains;

            if (domains.length === 0) {
                DOM.domainsBarsView.innerHTML = `
                    <div class="empty-state-notice">
                        <span>No unique host domains registered yet</span>
                    </div>
                `;
                return;
            }

            const maxPages = Math.max(...domains.map(d => d.total_pages || 1), 1);

            DOM.domainsBarsView.innerHTML = domains.map(d => {
                const count = d.total_pages || 0;
                const pct = Math.min(100, Math.round((count / maxPages) * 100));
                return `
                    <div class="domain-stat-row">
                        <div class="domain-info-col">
                            <span class="domain-host font-mono">${escapeHtml(d.domain)}</span>
                            <span class="domain-count font-mono">${count} page${count === 1 ? '' : 's'}</span>
                        </div>
                        <div class="domain-bar-track">
                            <div class="domain-bar-fill" style="width: ${pct}%"></div>
                        </div>
                        <div class="domain-latency-col font-mono text-muted">
                            ${d.avg_latency_ms || 0}ms avg
                        </div>
                    </div>
                `;
            }).join('');
        } catch (err) {
            DOM.domainsBarsView.innerHTML = `
                <div class="empty-state-notice">
                    <span>Failed to retrieve domain distribution: ${escapeHtml(err.message)}</span>
                </div>
            `;
        }
    }

    // =========================================================================
    // 11. WORKER FLEET MONITOR (VIEW 6)
    // =========================================================================
    async function fetchWorkers() {
        if (!DOM.workersGridContainer) return;
        try {
            const res = await apiRequest('/workers');
            const workers = res.workers || [];
            state.cachedWorkers = workers;

            if (DOM.sidebarWorkerCount) {
                DOM.sidebarWorkerCount.textContent = workers.length;
            }

            if (workers.length === 0) {
                DOM.workersGridContainer.innerHTML = `
                    <div class="empty-state-notice" style="grid-column: 1 / -1;">
                        <span>No autonomous worker daemons registered. Mode B (Serverless) handles dispatches on demand.</span>
                    </div>
                `;
                return;
            }

            DOM.workersGridContainer.innerHTML = workers.map(w => {
                const isOnline = w.status === 'ACTIVE' || w.status === 'IDLE';
                const statusBadge = isOnline ? 'badge-emerald' : 'badge-amber';
                return `
                    <div class="worker-card">
                        <div class="worker-card-header">
                            <div class="worker-id-group">
                                <span class="status-mini-dot ${isOnline ? 'dot-online' : 'dot-warn'}"></span>
                                <h4 class="worker-id font-mono">${escapeHtml(w.worker_id)}</h4>
                            </div>
                            <span class="badge ${statusBadge}">${escapeHtml(w.status)}</span>
                        </div>
                        <div class="worker-details-grid">
                            <div class="worker-stat-item">
                                <span class="w-label">HEARTBEAT</span>
                                <span class="w-val font-mono">${formatRelativeTime(w.last_heartbeat)}</span>
                            </div>
                            <div class="worker-stat-item">
                                <span class="w-label">COMPLETED</span>
                                <span class="w-val font-mono text-emerald">${formatNumber(w.tasks_completed || 0)}</span>
                            </div>
                            <div class="worker-stat-item">
                                <span class="w-label">FAILED</span>
                                <span class="w-val font-mono text-rose">${formatNumber(w.tasks_failed || 0)}</span>
                            </div>
                            <div class="worker-stat-item">
                                <span class="w-label">AVG LATENCY</span>
                                <span class="w-val font-mono text-cyan">${w.avg_latency_ms || 0} ms</span>
                            </div>
                        </div>
                        ${w.current_task ? `
                            <div class="worker-current-task">
                                <span class="w-label">CURRENT TASK</span>
                                <div class="task-url-truncate font-mono">${escapeHtml(w.current_task)}</div>
                            </div>
                        ` : ''}
                    </div>
                `;
            }).join('');
        } catch (err) {
            DOM.workersGridContainer.innerHTML = `
                <div class="empty-state-notice" style="grid-column: 1 / -1;">
                    <span>Failed to inspect worker fleet: ${escapeHtml(err.message)}</span>
                </div>
            `;
        }
    }

    // =========================================================================
    // 12. AUDITABLE ACTIVITY STREAM (VIEW 7)
    // =========================================================================
    async function fetchActivityLogs() {
        if (!DOM.activityTerminalFeed || state.feedPaused) return;
        try {
            const res = await apiRequest('/activity');
            const events = res.events || [];
            state.cachedEvents = events;

            if (events.length === 0) {
                DOM.activityTerminalFeed.innerHTML = `
                    <div class="terminal-line text-muted">[SYSTEM] Activity stream initialized. Awaiting crawl dispatches...</div>
                `;
                return;
            }

            DOM.activityTerminalFeed.innerHTML = events.map(evt => {
                const time = evt.created_at ? new Date(evt.created_at).toLocaleTimeString() : '00:00:00';
                let typeColor = 'text-cyan';
                if (evt.event_type.includes('ERROR') || evt.event_type.includes('FAIL')) typeColor = 'text-rose';
                else if (evt.event_type.includes('COMPLET') || evt.event_type.includes('SUCCESS')) typeColor = 'text-emerald';
                else if (evt.event_type.includes('LEASE')) typeColor = 'text-amber';

                return `
                    <div class="terminal-line">
                        <span class="term-time font-mono">[${time}]</span>
                        <span class="term-type font-mono ${typeColor}">${escapeHtml(evt.event_type)}</span>
                        ${evt.worker_id ? `<span class="term-worker font-mono">[${escapeHtml(evt.worker_id)}]</span>` : ''}
                        <span class="term-msg">${escapeHtml(evt.message || '')}</span>
                    </div>
                `;
            }).join('');

            // Auto-scroll to bottom of terminal
            DOM.activityTerminalFeed.scrollTop = DOM.activityTerminalFeed.scrollHeight;
        } catch (err) {
            // Quiet fail for activity feed
        }
    }

    function initActivityView() {
        if (DOM.feedPauseBtn) {
            DOM.feedPauseBtn.addEventListener('click', () => {
                state.feedPaused = !state.feedPaused;
                DOM.feedPauseBtn.textContent = state.feedPaused ? 'RESUME STREAM' : 'PAUSE';
                DOM.feedPauseBtn.classList.toggle('primary-btn', state.feedPaused);
            });
        }

        if (DOM.feedClearBtn) {
            DOM.feedClearBtn.addEventListener('click', () => {
                if (DOM.activityTerminalFeed) {
                    DOM.activityTerminalFeed.innerHTML = `
                        <div class="terminal-line text-muted">[CONSOLE] Terminal feed cleared locally.</div>
                    `;
                }
            });
        }
    }

    // =========================================================================
    // 13. SETTINGS & TIERED RESETS (VIEW 9)
    // =========================================================================
    function initSettingsView() {
        // Populate current API input
        if (DOM.backendUrlInput) {
            DOM.backendUrlInput.value = state.apiBase;
        }

        if (DOM.saveBackendUrlBtn) {
            DOM.saveBackendUrlBtn.addEventListener('click', async () => {
                const newUrl = DOM.backendUrlInput.value.trim() || DEFAULT_API;
                state.apiBase = newUrl;
                localStorage.setItem('crawler_api_base', newUrl);
                showToast(`Backend connection set to: ${newUrl}`, 'info');
                await fetchSystemStatus();
            });
        }

        // LEVEL A: Reset UI state
        if (DOM.btnResetUi) {
            DOM.btnResetUi.addEventListener('click', () => {
                if (DOM.searchMainInput) DOM.searchMainInput.value = '';
                state.activeSearchFilter = '0';
                state.activeJobFilter = 'ALL';
                DOM.searchChips.forEach(c => c.classList.remove('active'));
                if (DOM.searchChips[0]) DOM.searchChips[0].classList.add('active');
                if (DOM.jobStatusFilters) {
                    DOM.jobStatusFilters.querySelectorAll('.filter-pill').forEach(b => b.classList.remove('active'));
                    if (DOM.jobStatusFilters.querySelector('[data-filter="ALL"]')) {
                        DOM.jobStatusFilters.querySelector('[data-filter="ALL"]').classList.add('active');
                    }
                }
                showToast('UI local state cleared', 'success');
            });
        }

        // LEVEL B: Flush Redis Queue
        if (DOM.btnResetQueue) {
            DOM.btnResetQueue.addEventListener('click', async () => {
                try {
                    await apiRequest('/reset', {
                        method: 'POST',
                        body: JSON.stringify({ level: 'queue' })
                    });
                    showToast('Frontier queue successfully purged', 'success');
                    await fetchSystemStatus();
                } catch (err) {
                    showToast(`Queue flush failed: ${err.message}`, 'error');
                }
            });
        }

        // LEVEL C: Purge Crawled Pages Data
        if (DOM.btnResetData) {
            DOM.btnResetData.addEventListener('click', async () => {
                if (!confirm('Are you sure you want to purge all crawled pages and domain statistics?')) return;
                try {
                    await apiRequest('/reset', {
                        method: 'POST',
                        body: JSON.stringify({ level: 'data' })
                    });
                    showToast('Crawled pages database truncated', 'success');
                    await fetchSystemStatus();
                    if (state.currentView === 'view-search') executeSearch();
                } catch (err) {
                    showToast(`Data purge failed: ${err.message}`, 'error');
                }
            });
        }

        // LEVEL D: Full Production Reset
        if (DOM.btnResetProd) {
            DOM.btnResetProd.addEventListener('click', () => {
                if (DOM.prodResetPhraseInput) DOM.prodResetPhraseInput.value = '';
                if (DOM.confirmProdResetBtn) DOM.confirmProdResetBtn.disabled = true;
                if (DOM.prodResetModal) DOM.prodResetModal.classList.remove('hidden');
            });
        }

        if (DOM.cancelProdResetBtn) {
            DOM.cancelProdResetBtn.addEventListener('click', () => {
                if (DOM.prodResetModal) DOM.prodResetModal.classList.add('hidden');
            });
        }

        if (DOM.prodResetPhraseInput) {
            DOM.prodResetPhraseInput.addEventListener('input', () => {
                const phrase = DOM.prodResetPhraseInput.value.trim();
                DOM.confirmProdResetBtn.disabled = (phrase !== 'WIPE ALL DATA');
            });
        }

        if (DOM.confirmProdResetBtn) {
            DOM.confirmProdResetBtn.addEventListener('click', async () => {
                try {
                    await apiRequest('/reset', {
                        method: 'POST',
                        body: JSON.stringify({
                            level: 'production',
                            confirmation_phrase: 'WIPE ALL DATA'
                        })
                    });
                    DOM.prodResetModal.classList.add('hidden');
                    showToast('Cluster fully reset to clean production state', 'success');
                    await fetchSystemStatus();
                    refreshCurrentView();
                } catch (err) {
                    showToast(`Production reset failed: ${err.message}`, 'error');
                }
            });
        }
    }

    // =========================================================================
    // 14. PAGE INSPECTOR MODAL
    // =========================================================================
    function openInspectorModal(page) {
        if (!DOM.pageInspectorModal || !page) return;

        if (DOM.inspectorModalTitle) {
            DOM.inspectorModalTitle.textContent = page.title || page.url;
        }

        const internalLinks = page.internal_links || [];
        const externalLinks = page.external_links || [];

        if (DOM.inspectorModalBody) {
            DOM.inspectorModalBody.innerHTML = `
                <div class="inspector-section">
                    <span class="inspector-label">CANONICAL / TARGET URL</span>
                    <a href="${escapeHtml(page.url)}" target="_blank" rel="noopener noreferrer" class="inspector-url font-mono">${escapeHtml(page.url)}</a>
                </div>

                <div class="inspector-metrics-grid">
                    <div class="insp-stat">
                        <span class="insp-stat-label">HTTP STATUS</span>
                        <span class="insp-stat-val font-mono ${page.status_code === 200 ? 'text-emerald' : 'text-rose'}">${page.status_code || 200}</span>
                    </div>
                    <div class="insp-stat">
                        <span class="insp-stat-label">LATENCY</span>
                        <span class="insp-stat-val font-mono text-cyan">${page.latency_ms || 0} ms</span>
                    </div>
                    <div class="insp-stat">
                        <span class="insp-stat-label">PAYLOAD SIZE</span>
                        <span class="insp-stat-val font-mono">${formatBytes(page.content_length)}</span>
                    </div>
                    <div class="insp-stat">
                        <span class="insp-stat-label">CRAWL DEPTH</span>
                        <span class="insp-stat-val font-mono">${page.depth || 0}</span>
                    </div>
                </div>

                ${page.meta_description ? `
                    <div class="inspector-section">
                        <span class="inspector-label">META DESCRIPTION</span>
                        <p class="inspector-text">${escapeHtml(page.meta_description)}</p>
                    </div>
                ` : ''}

                ${page.h1 ? `
                    <div class="inspector-section">
                        <span class="inspector-label">H1 HEADING</span>
                        <p class="inspector-text font-mono">${escapeHtml(page.h1)}</p>
                    </div>
                ` : ''}

                <div class="inspector-section">
                    <span class="inspector-label">INTERNAL HYPERLINKS (${internalLinks.length})</span>
                    <div class="inspector-link-list">
                        ${internalLinks.length > 0 ? internalLinks.map(l => `<a href="${escapeHtml(l)}" target="_blank" rel="noopener noreferrer" class="insp-link font-mono">${escapeHtml(l)}</a>`).join('') : '<span class="text-muted">None discovered</span>'}
                    </div>
                </div>

                <div class="inspector-section">
                    <span class="inspector-label">EXTERNAL OUTBOUND HYPERLINKS (${externalLinks.length})</span>
                    <div class="inspector-link-list">
                        ${externalLinks.length > 0 ? externalLinks.map(l => `<a href="${escapeHtml(l)}" target="_blank" rel="noopener noreferrer" class="insp-link font-mono">${escapeHtml(l)}</a>`).join('') : '<span class="text-muted">None discovered</span>'}
                    </div>
                </div>
            `;
        }

        DOM.pageInspectorModal.classList.remove('hidden');
    }

    function initInspectorModal() {
        if (DOM.closeInspectorBtn) {
            DOM.closeInspectorBtn.addEventListener('click', () => {
                DOM.pageInspectorModal.classList.add('hidden');
            });
        }

        if (DOM.pageInspectorModal) {
            DOM.pageInspectorModal.addEventListener('click', (e) => {
                if (e.target === DOM.pageInspectorModal) {
                    DOM.pageInspectorModal.classList.add('hidden');
                }
            });
        }

        window.addEventListener('keydown', (e) => {
            if (e.key === 'Escape') {
                if (DOM.pageInspectorModal && !DOM.pageInspectorModal.classList.contains('hidden')) {
                    DOM.pageInspectorModal.classList.add('hidden');
                }
                if (DOM.prodResetModal && !DOM.prodResetModal.classList.contains('hidden')) {
                    DOM.prodResetModal.classList.add('hidden');
                }
            }
        });
    }

    // =========================================================================
    // 15. VIEW REFRESH CONTROLLER
    // =========================================================================
    function refreshCurrentView() {
        switch (state.currentView) {
            case 'view-overview':
                fetchSystemStatus();
                break;
            case 'view-jobs':
                fetchJobs();
                break;
            case 'view-search':
                executeSearch();
                break;
            case 'view-domains':
                fetchDomains();
                break;
            case 'view-workers':
                fetchWorkers();
                break;
            case 'view-activity':
                fetchActivityLogs();
                break;
        }
    }

    // =========================================================================
    // 16. APPLICATION INITIALIZATION
    // =========================================================================
    function init() {
        initNavigation();
        initCrawlExplorer();
        initJobsView();
        initSearchView();
        initActivityView();
        initSettingsView();
        initInspectorModal();

        // Header controls
        if (DOM.autoCrawlToggle) {
            DOM.autoCrawlToggle.addEventListener('click', toggleAutoCrawl);
        }
        if (DOM.crawlStepBtn) {
            DOM.crawlStepBtn.addEventListener('click', handleManualCrawlStep);
        }

        // Initial fetch
        fetchSystemStatus();

        // Recurring background telemetry polling every 3 seconds
        state.telemetryTimer = setInterval(() => {
            fetchSystemStatus();
            if (state.currentView === 'view-activity' && !state.feedPaused) {
                fetchActivityLogs();
            }
        }, 3000);
    }

    // Launch on DOM ready
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }
})();
