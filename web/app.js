/**
 * GOPHER CRAWLER — ENTERPRISE OPERATOR CONSOLE (v2.0.0)
 * Unified with Go Coordinator Master & Vercel Serverless Preview APIs.
 * Zero fake data, complete pagination/export/analytics/job control, accessible UX.
 */

(() => {
    'use strict';

    const DEFAULT_API = '/api';
    const PAGE_LIMIT = 15;

    const PROFILE_PRESETS = {
        small:    { max_depth: 2, max_pages: 25,  concurrency: 2, politeness_delay_ms: 1000 },
        docs:     { max_depth: 4, max_pages: 150, concurrency: 4, politeness_delay_ms: 500 },
        research: { max_depth: 3, max_pages: 250, concurrency: 4, politeness_delay_ms: 1500 },
        domain:   { max_depth: 5, max_pages: 500, concurrency: 6, politeness_delay_ms: 800 },
        custom:   { max_depth: 3, max_pages: 100, concurrency: 4, politeness_delay_ms: 1000 }
    };

    const state = {
        apiBase: localStorage.getItem('crawler_api_base') || DEFAULT_API,
        adminToken: sessionStorage.getItem('crawler_admin_token') || '',
        currentView: 'view-overview',
        autoCrawlActive: false,
        autoCrawlTimer: null,
        telemetryTimer: null,
        feedPaused: false,
        activeSearchFilter: '',
        searchOffset: 0,
        searchTotal: 0,
        activeJobFilter: 'ALL',
        analyticsRange: '24h',
        latestStatus: null,
        cachedPages: [],
        cachedJobs: [],
        cachedWorkers: [],
        cachedDomains: [],
        cachedEvents: []
    };

    const $ = id => document.getElementById(id);

    const DOM = {
        sidebar: $('sidebar'),
        mobileNavOpen: $('mobile-nav-open'),
        mobileNavClose: $('mobile-nav-close'),
        mobileBackdrop: $('mobile-nav-backdrop'),
        navItems: document.querySelectorAll('.nav-item[data-view]'),
        viewPanels: document.querySelectorAll('.view-panel'),
        breadcrumbTitle: $('breadcrumb-title'),
        globalPulseDot: $('global-pulse-dot'),
        globalStatusText: $('global-status-text'),
        globalModeTag: $('global-mode-tag'),
        serverlessPill: $('serverless-mode-pill'),
        sidebarJobCount: $('sidebar-job-count'),
        sidebarPagesCount: $('sidebar-pages-count'),
        sidebarWorkerCount: $('sidebar-worker-count'),
        autoCrawlToggle: $('auto-crawl-toggle'),
        crawlStepBtn: $('crawl-step-btn'),
        quickResetBtn: $('quick-reset-btn'),

        onboardingBanner: $('onboarding-banner'),
        dismissOnboardingBtn: $('dismiss-onboarding-btn'),

        ovPagesCount: $('ov-pages-count'),
        ovPagesSub: $('ov-pages-sub'),
        ovDomainsCount: $('ov-domains-count'),
        ovDomainsSub: $('ov-domains-sub'),
        ovQueueCount: $('ov-queue-count'),
        ovQueueSub: $('ov-queue-sub'),
        ovWorkersCount: $('ov-workers-count'),
        ovWorkersSub: $('ov-workers-sub'),
        ovThroughputVal: $('ov-throughput-val'),
        ovLatencyVal: $('ov-latency-val'),
        ovSuccessVal: $('ov-success-val'),
        ovJobsTally: $('ov-jobs-tally'),
        ovUptimeVal: $('ov-uptime-val'),
        ovRecentPages: $('ov-recent-pages'),
        ovWorkersList: $('ov-workers-list'),
        ovDomainBars: $('ov-domain-bars'),
        ovReclaimsVal: $('ov-reclaims-val'),
        ovRobotsVal: $('ov-robots-val'),
        ovRecentJobs: $('ov-recent-jobs'),
        ovLiveLogs: $('ov-live-logs'),

        profileCards: document.querySelectorAll('.profile-card[data-profile]'),
        crawlExplorerForm: $('crawl-explorer-form'),
        explorerNameInput: $('explorer-name-input'),
        explorerAllowedInput: $('explorer-allowed-input'),
        explorerUrlInput: $('explorer-url-input'),
        explorerDepthInput: $('explorer-depth-input'),
        explorerPagesInput: $('explorer-pages-input'),
        explorerConcurrencyInput: $('explorer-concurrency-input'),
        explorerPolitenessInput: $('explorer-politeness-input'),
        explorerExcludeInput: $('explorer-exclude-input'),
        explorerReviewSummary: $('explorer-review-summary'),
        presetPills: document.querySelectorAll('.preset-pill'),
        explorerFeedback: $('explorer-feedback'),
        explorerSubmitBtn: $('explorer-submit-btn'),

        jobStatusFilters: $('job-status-filters'),
        jobsTbody: $('jobs-tbody'),

        searchIndexForm: $('search-index-form'),
        searchMainInput: $('search-main-input'),
        searchClearBtn: $('search-clear-btn'),
        searchChips: document.querySelectorAll('.chip-item[data-filter]'),
        searchDomainSelect: $('search-domain-select'),
        searchSortSelect: $('search-sort-select'),
        searchDateFrom: $('search-date-from'),
        searchTallyText: $('search-tally-text'),
        searchResultsContainer: $('search-results-container'),
        searchPrevBtn: $('search-prev-btn'),
        searchNextBtn: $('search-next-btn'),
        searchPageIndicator: $('search-page-indicator'),
        searchExportCsvBtn: $('search-export-csv-btn'),
        searchExportJsonBtn: $('search-export-json-btn'),

        domainFilterInput: $('domain-filter-input'),
        domainsBarsView: $('domains-bars-view'),

        workersGridContainer: $('workers-grid-container'),

        activityTerminalFeed: $('activity-terminal-feed'),
        activitySearchInput: $('activity-search-input'),
        activitySeveritySelect: $('activity-severity-select'),
        feedCopyBtn: $('feed-copy-btn'),
        feedPauseBtn: $('feed-pause-btn'),
        feedClearBtn: $('feed-clear-btn'),

        analyticsRangeSelect: $('analytics-range-select'),
        anAvgLatency: $('an-avg-latency'),
        anLatencyDetail: $('an-latency-detail'),
        anSuccessRate: $('an-success-rate'),
        anSuccessDetail: $('an-success-detail'),
        anRobotsRate: $('an-robots-rate'),
        anLeaseReclaims: $('an-lease-reclaims'),
        anRetriesDetail: $('an-retries-detail'),
        anStatusChart: $('an-status-chart'),
        anErrorChart: $('an-error-chart'),
        anThroughputChart: $('an-throughput-chart'),
        anJobsChart: $('an-jobs-chart'),

        backendUrlInput: $('backend-url-input'),
        saveBackendUrlBtn: $('save-backend-url-btn'),
        adminTokenInput: $('admin-token-input'),
        saveAdminTokenBtn: $('save-admin-token-btn'),
        resetJobSelect: $('reset-job-select'),
        btnResetUi: $('btn-reset-ui'),
        btnResetCancelJob: $('btn-reset-cancel-job'),
        btnResetQueue: $('btn-reset-queue'),
        btnResetJobData: $('btn-reset-job-data'),
        btnResetData: $('btn-reset-data'),
        btnResetDev: $('btn-reset-dev'),
        btnResetProd: $('btn-reset-prod'),

        prodResetModal: $('prod-reset-modal'),
        prodResetPhraseInput: $('prod-reset-phrase-input'),
        closeProdModalBtn: $('close-prod-modal-btn'),
        cancelProdResetBtn: $('cancel-prod-reset-btn'),
        confirmProdResetBtn: $('confirm-prod-reset-btn'),

        confirmModal: $('confirm-action-modal'),
        confirmModalMessage: $('confirm-modal-message'),
        confirmModalClose: $('confirm-modal-close'),
        confirmModalCancel: $('confirm-modal-cancel'),
        confirmModalProceed: $('confirm-modal-proceed'),

        pageInspectorModal: $('page-inspector-modal'),
        inspectorModalTitle: $('inspector-modal-title'),
        inspectorModalBody: $('inspector-modal-body'),
        closeInspectorBtn: $('close-inspector-btn'),

        entityInspectorModal: $('entity-inspector-modal'),
        entityModalBadge: $('entity-modal-badge'),
        entityModalTitle: $('entity-modal-title'),
        entityModalBody: $('entity-modal-body'),
        closeEntityBtn: $('close-entity-btn')
    };

    // =========================================================================
    // UTILITIES
    // =========================================================================
    function getApiUrl(path) {
        const base = state.apiBase.replace(/\/+$/, '');
        const clean = path.startsWith('/') ? path : `/${path}`;
        return `${base}${clean}`;
    }

    async function apiRequest(endpoint, options = {}) {
        const headers = {
            'Accept': 'application/json',
            ...(options.body ? { 'Content-Type': 'application/json' } : {}),
            ...(state.adminToken ? { 'X-Admin-Token': state.adminToken } : {}),
            ...(options.headers || {})
        };
        const res = await fetch(getApiUrl(endpoint), { ...options, headers });
        if (!res.ok) {
            let errDetail = res.statusText;
            try {
                const errJson = await res.json();
                if (errJson.error) errDetail = errJson.error;
            } catch {
                // ignore
            }
            throw new Error(`${errDetail} (HTTP ${res.status})`);
        }
        const ct = res.headers.get('content-type') || '';
        return ct.includes('application/json') ? res.json() : res.text();
    }

    function escapeHtml(str) {
        if (str === null || str === undefined) return '';
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
        if (!bytes || bytes <= 0) return '0 B';
        const k = 1024;
        const sizes = ['B', 'KB', 'MB', 'GB'];
        const i = Math.min(sizes.length - 1, Math.floor(Math.log(bytes) / Math.log(k)));
        return `${parseFloat((bytes / Math.pow(k, i)).toFixed(1))} ${sizes[i]}`;
    }

    function formatDurationSec(sec) {
        const s = Math.max(0, Math.floor(Number(sec) || 0));
        if (s < 60) return `${s}s`;
        const m = Math.floor(s / 60);
        if (m < 60) return `${m}m ${s % 60}s`;
        const h = Math.floor(m / 60);
        return `${h}h ${m % 60}m`;
    }

    function formatRelativeTime(dateStr) {
        if (!dateStr) return 'unknown';
        const d = new Date(dateStr);
        if (isNaN(d.getTime())) return 'unknown';
        const diffSec = Math.floor((Date.now() - d.getTime()) / 1000);
        if (diffSec < 5) return 'just now';
        if (diffSec < 60) return `${diffSec}s ago`;
        const diffMin = Math.floor(diffSec / 60);
        if (diffMin < 60) return `${diffMin}m ago`;
        const diffHr = Math.floor(diffMin / 60);
        if (diffHr < 24) return `${diffHr}h ago`;
        return d.toLocaleDateString();
    }

    function statusBadgeClass(code) {
        if (code >= 200 && code < 300) return 'status-pill-ok';
        if (code >= 300 && code < 400) return 'status-pill-warn';
        return 'status-pill-err';
    }

    function jobBadgeClass(status) {
        const map = {
            RUNNING: 'badge-emerald',
            QUEUED: 'badge-cyan',
            PENDING: 'badge-cyan',
            COMPLETED: 'badge-purple',
            PAUSED: 'badge-amber',
            CANCELLED: 'badge-rose',
            FAILED: 'badge-rose'
        };
        return map[status] || 'badge-cyan';
    }

    function showToast(message, type = 'info') {
        let container = $('platform-toast-container');
        if (!container) {
            container = document.createElement('div');
            container.id = 'platform-toast-container';
            container.className = 'toast-container';
            document.body.appendChild(container);
        }
        const toast = document.createElement('div');
        toast.className = `toast-item toast-${type}`;
        toast.innerHTML = `<div class="toast-dot"></div><div>${escapeHtml(message)}</div>`;
        container.appendChild(toast);
        requestAnimationFrame(() => toast.classList.add('toast-show'));
        setTimeout(() => {
            toast.classList.remove('toast-show');
            setTimeout(() => toast.remove(), 260);
        }, 3800);
    }

    async function copyToClipboard(text, label = 'Copied') {
        try {
            await navigator.clipboard.writeText(text);
            showToast(`${label} copied to clipboard`, 'success');
        } catch {
            showToast('Clipboard write unavailable in browser', 'error');
        }
    }

    function openConfirmDialog(message, onConfirm) {
        if (!DOM.confirmModal) return;
        DOM.confirmModalMessage.textContent = message;
        DOM.confirmModal.classList.remove('hidden');

        const cleanup = () => {
            DOM.confirmModal.classList.add('hidden');
            DOM.confirmModalProceed.onclick = null;
        };
        DOM.confirmModalCancel.onclick = cleanup;
        DOM.confirmModalClose.onclick = cleanup;
        DOM.confirmModalProceed.onclick = async () => {
            cleanup();
            await onConfirm();
        };
    }

    // =========================================================================
    // NAVIGATION & RESPONSIVE SIDEBAR
    // =========================================================================
    function closeMobileNav() {
        if (DOM.sidebar) DOM.sidebar.classList.remove('open');
        if (DOM.mobileBackdrop) DOM.mobileBackdrop.classList.add('hidden');
    }

    function switchView(viewId) {
        if (!viewId || !$(viewId)) return;
        DOM.navItems.forEach(item => item.classList.toggle('active', item.dataset.view === viewId));
        DOM.viewPanels.forEach(panel => panel.classList.toggle('active', panel.id === viewId));

        state.currentView = viewId;
        window.location.hash = viewId.replace('view-', '');

        const titles = {
            'view-overview': 'OVERVIEW',
            'view-explorer': 'CRAWL EXPLORER',
            'view-jobs': 'JOBS MANAGER',
            'view-search': 'SEARCH INDEX',
            'view-domains': 'DOMAINS REGISTRY',
            'view-workers': 'WORKER FLEET',
            'view-activity': 'ACTIVITY LOGS',
            'view-analytics': 'SYSTEM ANALYTICS',
            'view-settings': 'SETTINGS & RESETS',
            'view-docs': 'DOCUMENTATION'
        };
        DOM.breadcrumbTitle.textContent = titles[viewId] || 'CONSOLE';
        closeMobileNav();
        refreshCurrentView();
    }

    function initNavigation() {
        DOM.navItems.forEach(btn => btn.addEventListener('click', () => switchView(btn.dataset.view)));
        document.querySelectorAll('[data-view-target]').forEach(el => {
            el.addEventListener('click', () => switchView(el.getAttribute('data-view-target')));
        });
        if (DOM.quickResetBtn) DOM.quickResetBtn.addEventListener('click', () => switchView('view-settings'));

        if (DOM.mobileNavOpen) {
            DOM.mobileNavOpen.addEventListener('click', () => {
                DOM.sidebar.classList.add('open');
                DOM.mobileBackdrop.classList.remove('hidden');
            });
        }
        if (DOM.mobileNavClose) DOM.mobileNavClose.addEventListener('click', closeMobileNav);
        if (DOM.mobileBackdrop) DOM.mobileBackdrop.addEventListener('click', closeMobileNav);

        if (DOM.onboardingBanner && localStorage.getItem('crawler_onboarding_dismissed') === '1') {
            DOM.onboardingBanner.classList.add('hidden');
        }
        if (DOM.dismissOnboardingBtn) {
            DOM.dismissOnboardingBtn.addEventListener('click', () => {
                DOM.onboardingBanner.classList.add('hidden');
                localStorage.setItem('crawler_onboarding_dismissed', '1');
            });
        }

        const hashView = window.location.hash ? `view-${window.location.hash.replace('#', '')}` : '';
        if (hashView && $(hashView)) {
            switchView(hashView);
        } else {
            switchView('view-overview');
        }
    }

    // =========================================================================
    // TELEMETRY & OVERVIEW DASHBOARD
    // =========================================================================
    async function fetchSystemStatus() {
        try {
            const data = await apiRequest('/status');
            state.latestStatus = data;

            const isOnline = data.status === 'online' || data.status === 'ok' || data.status === 'healthy';
            DOM.globalStatusText.textContent = isOnline ? 'SYSTEM ONLINE' : 'DEGRADED';
            DOM.globalPulseDot.className = `status-indicator-dot ${isOnline ? 'online' : 'offline'}`;

            const mode = data.mode || 'cluster';
            const modeTextMap = {
                cluster: '[MODE A: CLUSTER]',
                proxy: '[MODE A: PROXY CLUSTER]',
                serverless_preview: '[MODE B: SERVERLESS PREVIEW]'
            };
            DOM.globalModeTag.textContent = modeTextMap[mode] || `[${mode.toUpperCase()}]`;
            if (DOM.serverlessPill) {
                DOM.serverlessPill.classList.toggle('hidden', mode !== 'serverless_preview');
            }

            const pagesIndexed = data.pages_indexed ?? data.visited_count ?? 0;
            const domainCount = data.domain_count ?? 0;
            const discoveredCount = data.discovered_urls_count ?? 0;
            const queueSize = data.queue_size ?? 0;
            const activeLeases = data.leases_active ?? 0;
            const workersActive = data.workers_active ?? (data.workers ? data.workers.length : 0);
            const activeJobs = data.active_jobs ?? 0;

            DOM.ovPagesCount.textContent = formatNumber(pagesIndexed);
            DOM.ovPagesSub.textContent = mode === 'serverless_preview' ? 'in-memory preview index' : 'persisted in PostgreSQL';
            DOM.ovDomainsCount.textContent = formatNumber(domainCount);
            DOM.ovDomainsSub.textContent = `${formatNumber(discoveredCount)} URLs discovered`;
            DOM.ovQueueCount.textContent = formatNumber(queueSize);
            DOM.ovQueueSub.textContent = `${activeLeases} lease${activeLeases === 1 ? '' : 's'} active`;
            DOM.ovWorkersCount.textContent = formatNumber(workersActive);
            DOM.ovWorkersSub.textContent = `${activeJobs} job${activeJobs === 1 ? '' : 's'} running`;

            if (DOM.ovThroughputVal) DOM.ovThroughputVal.textContent = `${Number(data.throughput_rpm || 0).toFixed(1)} rpm`;
            if (DOM.ovLatencyVal) DOM.ovLatencyVal.textContent = `${data.avg_latency_ms || 0} ms`;
            if (DOM.ovSuccessVal) DOM.ovSuccessVal.textContent = `${Number(data.success_rate || 0).toFixed(1)}%`;
            if (DOM.ovJobsTally) DOM.ovJobsTally.textContent = `${data.completed_jobs || 0} / ${data.failed_jobs || 0}`;
            if (DOM.ovUptimeVal) DOM.ovUptimeVal.textContent = formatDurationSec(data.uptime_sec || 0);
            if (DOM.ovReclaimsVal) DOM.ovReclaimsVal.textContent = `${data.leases_reclaimed || 0} reclaimed (60s TTL)`;
            if (DOM.ovRobotsVal) DOM.ovRobotsVal.textContent = `${data.robots_blocked || 0} disallowed`;

            if (DOM.sidebarPagesCount) DOM.sidebarPagesCount.textContent = formatNumber(pagesIndexed);
            if (DOM.sidebarWorkerCount) DOM.sidebarWorkerCount.textContent = workersActive;

            renderRecentPages(data.recent_pages || []);
            renderWorkersSnapshot(data.workers || [], mode);
            renderMiniBars(DOM.ovDomainBars, (data.domain_breakdown || []).map(d => ({
                label: d.domain, count: d.total_pages, suffix: `${d.total_pages} pages`
            })));
            renderRecentJobsSnapshot(data.recent_jobs || []);
            renderMiniLogs(data.live_logs || []);

            return data;
        } catch (err) {
            DOM.globalStatusText.textContent = 'DISCONNECTED';
            DOM.globalPulseDot.className = 'status-indicator-dot offline';
            DOM.globalModeTag.textContent = '[BACKEND UNREACHABLE]';
            DOM.ovPagesCount.textContent = '--';
            DOM.ovDomainsCount.textContent = '--';
            DOM.ovQueueCount.textContent = '--';
            DOM.ovWorkersCount.textContent = '--';
            return null;
        }
    }

    function renderRecentPages(pages) {
        if (!DOM.ovRecentPages) return;
        if (!pages || pages.length === 0) {
            DOM.ovRecentPages.innerHTML = `<div class="empty-state-notice"><span>No crawled pages indexed yet. Launch a job in Crawl Explorer or click <strong>CRAWL STEP</strong>.</span></div>`;
            return;
        }
        DOM.ovRecentPages.innerHTML = pages.map(p => `
            <div class="recent-page-card" data-id="${escapeHtml(p.id)}" data-url="${escapeHtml(p.url)}">
                <div class="card-status-col">
                    <span class="status-code-badge ${statusBadgeClass(p.status_code)}">${p.status_code || 0}</span>
                </div>
                <div class="card-content-col">
                    <div class="card-title-row">
                        <h4 class="card-title">${escapeHtml(p.title || p.url)}</h4>
                        <span class="card-latency font-mono">${p.latency_ms || 0}ms</span>
                    </div>
                    <a href="${escapeHtml(p.url)}" target="_blank" rel="noopener noreferrer" class="card-url font-mono">${escapeHtml(p.url)}</a>
                </div>
            </div>
        `).join('');

        DOM.ovRecentPages.querySelectorAll('.recent-page-card').forEach(card => {
            card.addEventListener('click', e => {
                if (e.target.tagName === 'A') return;
                const page = pages.find(p => String(p.id) === card.dataset.id || p.url === card.dataset.url);
                if (page) openPageInspector(page);
            });
        });
    }

    function renderWorkersSnapshot(workers, mode) {
        if (!DOM.ovWorkersList) return;
        if (!workers || workers.length === 0) {
            DOM.ovWorkersList.innerHTML = mode === 'serverless_preview'
                ? `<div class="empty-state-notice"><span>Running in <strong>Serverless Preview</strong> mode (on-demand bounded HTTP execution, 0 persistent Go daemons).</span></div>`
                : `<div class="empty-state-notice"><span>No Go worker daemons online. Run <code>go run cmd/worker/main.go</code> or scale Docker workers.</span></div>`;
            return;
        }
        DOM.ovWorkersList.innerHTML = workers.slice(0, 5).map(w => {
            const isOnline = w.status === 'ACTIVE' || w.status === 'IDLE';
            const dotCls = isOnline ? 'dot-online' : (w.status === 'UNHEALTHY' ? 'dot-err' : 'dot-warn');
            return `
                <div class="worker-snap-row">
                    <div class="worker-snap-id font-mono">
                        <span class="status-mini-dot ${dotCls}"></span>
                        <span>${escapeHtml(w.id)}</span>
                    </div>
                    <div class="worker-snap-meta">
                        <span class="badge ${isOnline ? 'badge-emerald' : 'badge-amber'}">${escapeHtml(w.status)}</span>
                        <span class="worker-snap-tasks font-mono">${w.tasks_completed || 0} ok</span>
                    </div>
                </div>
            `;
        }).join('');
    }

    function renderMiniBars(container, items, fillColor = 'var(--color-cyan)') {
        if (!container) return;
        if (!items || items.length === 0) {
            container.innerHTML = `<span class="text-muted font-mono" style="font-size:11px;">No data recorded yet</span>`;
            return;
        }
        const max = Math.max(...items.map(i => i.count || 0), 1);
        container.innerHTML = items.map(item => {
            const pct = Math.max(4, Math.round(((item.count || 0) / max) * 100));
            return `
                <div class="chart-bar-item">
                    <div class="chart-bar-label-row">
                        <span>${escapeHtml(item.label)}</span>
                        <span>${escapeHtml(item.suffix || formatNumber(item.count))}</span>
                    </div>
                    <div class="chart-track">
                        <div class="chart-fill" style="width:${pct}%;background:${fillColor};"></div>
                    </div>
                </div>
            `;
        }).join('');
    }

    function renderRecentJobsSnapshot(jobs) {
        if (!DOM.ovRecentJobs) return;
        if (!jobs || jobs.length === 0) {
            DOM.ovRecentJobs.innerHTML = `<span class="text-muted font-mono">No jobs created yet</span>`;
            return;
        }
        DOM.ovRecentJobs.innerHTML = jobs.slice(0, 5).map(j => `
            <div class="mini-job-row">
                <span class="font-mono" style="overflow:hidden;text-overflow:ellipsis;white-space:nowrap;max-width:55%;">${escapeHtml(j.name || j.id)}</span>
                <span><span class="badge ${jobBadgeClass(j.status)}">${escapeHtml(j.status)}</span> <span class="font-mono text-muted">${j.pages_crawled || 0}/${j.max_pages || 0}</span></span>
            </div>
        `).join('');
    }

    function renderMiniLogs(logs) {
        if (!DOM.ovLiveLogs) return;
        if (!logs || logs.length === 0) {
            DOM.ovLiveLogs.innerHTML = `<span class="text-muted">Awaiting events...</span>`;
            return;
        }
        DOM.ovLiveLogs.innerHTML = logs.slice(0, 8).map(ev => {
            const cls = ev.severity === 'ERROR' ? 'text-rose' : (ev.severity === 'WARN' ? 'text-amber' : 'text-cyan');
            return `<div class="mini-log-row"><span class="${cls}">[${escapeHtml(ev.event_type)}]</span> ${escapeHtml(ev.message)}</div>`;
        }).join('');
    }

    // =========================================================================
    // AUTO-STEP & CRAWL STEP
    // =========================================================================
    function toggleAutoCrawl() {
        state.autoCrawlActive = !state.autoCrawlActive;
        DOM.autoCrawlToggle.setAttribute('aria-checked', state.autoCrawlActive ? 'true' : 'false');

        if (state.autoCrawlActive) {
            showToast('Auto-Step crawl enabled', 'success');
            runCrawlStep(true);
            state.autoCrawlTimer = setInterval(() => runCrawlStep(true), 2500);
        } else {
            showToast('Auto-Step crawl paused', 'info');
            if (state.autoCrawlTimer) {
                clearInterval(state.autoCrawlTimer);
                state.autoCrawlTimer = null;
            }
        }
    }

    async function runCrawlStep(quiet = false) {
        try {
            const res = await apiRequest('/crawl', { method: 'POST' });
            if (res.page && res.page.url) {
                showToast(`Crawled ${res.page.url} (${res.page.status_code})`, 'success');
            } else if (res.status === 'queue_empty') {
                if (state.autoCrawlActive) {
                    toggleAutoCrawl();
                    showToast('Frontier queue empty — auto-step paused', 'info');
                } else if (!quiet) {
                    showToast(res.message || 'Frontier queue is empty', 'info');
                }
            } else if (!quiet && res.message) {
                showToast(res.message, 'info');
            }
            await fetchSystemStatus();
            if (state.currentView === 'view-search') executeSearch();
            if (state.currentView === 'view-jobs') fetchJobs();
        } catch (err) {
            if (!quiet) showToast(`Crawl step error: ${err.message}`, 'error');
        }
    }

    // =========================================================================
    // CRAWL EXPLORER (VIEW 2)
    // =========================================================================
    function updateExplorerReview() {
        if (!DOM.explorerReviewSummary) return;
        const depth = DOM.explorerDepthInput.value || 2;
        const pages = DOM.explorerPagesInput.value || 25;
        const conc = DOM.explorerConcurrencyInput.value || 2;
        const delay = DOM.explorerPolitenessInput.value || 1000;
        const allowed = DOM.explorerAllowedInput.value.trim() || 'Any public domain';
        DOM.explorerReviewSummary.textContent =
            `Policy Preview → Max Depth: ${depth} | Max Pages: ${pages} | Concurrency: ${conc} | Politeness Delay: ${delay}ms | Allowed Domains: ${allowed}`;
    }

    function initCrawlExplorer() {
        if (!DOM.crawlExplorerForm) return;

        DOM.profileCards.forEach(card => {
            card.addEventListener('click', () => {
                DOM.profileCards.forEach(c => c.classList.remove('active'));
                card.classList.add('active');
                const p = PROFILE_PRESETS[card.dataset.profile];
                if (p) {
                    DOM.explorerDepthInput.value = p.max_depth;
                    DOM.explorerPagesInput.value = p.max_pages;
                    DOM.explorerConcurrencyInput.value = p.concurrency;
                    DOM.explorerPolitenessInput.value = p.politeness_delay_ms;
                    updateExplorerReview();
                }
            });
        });

        [DOM.explorerDepthInput, DOM.explorerPagesInput, DOM.explorerConcurrencyInput, DOM.explorerPolitenessInput, DOM.explorerAllowedInput].forEach(inp => {
            if (inp) inp.addEventListener('input', updateExplorerReview);
        });
        updateExplorerReview();

        DOM.presetPills.forEach(pill => {
            pill.addEventListener('click', () => {
                const seed = pill.dataset.seed;
                if (!seed) return;
                const cur = DOM.explorerUrlInput.value.trim();
                DOM.explorerUrlInput.value = !cur ? seed : (cur.includes(seed) ? cur : `${cur}\n${seed}`);
            });
        });

        DOM.crawlExplorerForm.addEventListener('submit', async e => {
            e.preventDefault();
            const urls = DOM.explorerUrlInput.value.split('\n').map(u => u.trim()).filter(Boolean);
            if (urls.length === 0) {
                showToast('Enter at least one seed URL', 'error');
                return;
            }

            const allowedDomains = DOM.explorerAllowedInput.value.split(',').map(s => s.trim()).filter(Boolean);
            const excludePatterns = DOM.explorerExcludeInput.value.split('\n').map(s => s.trim()).filter(Boolean);

            DOM.explorerSubmitBtn.disabled = true;
            DOM.explorerSubmitBtn.innerHTML = '<span>SUBMITTING & VALIDATING...</span>';

            try {
                const res = await apiRequest('/submit', {
                    method: 'POST',
                    body: JSON.stringify({
                        name: DOM.explorerNameInput.value.trim(),
                        urls,
                        max_depth: parseInt(DOM.explorerDepthInput.value, 10) || 3,
                        max_pages: parseInt(DOM.explorerPagesInput.value, 10) || 100,
                        concurrency: parseInt(DOM.explorerConcurrencyInput.value, 10) || 2,
                        politeness_delay_ms: parseInt(DOM.explorerPolitenessInput.value, 10) || 1000,
                        allowed_domains: allowedDomains,
                        exclude_patterns: excludePatterns
                    })
                });

                showToast(`Crawl job ${res.job_id || ''} launched (${res.enqueued || urls.length} seeds)`, 'success');
                DOM.explorerFeedback.className = 'feedback-banner feedback-success';
                DOM.explorerFeedback.innerHTML = `
                    <div class="feedback-icon">✓</div>
                    <div>
                        <strong>Crawl Job Registered</strong><br>
                        Job ID: <code class="font-mono">${escapeHtml(res.job_id)}</code> • Enqueued: ${res.enqueued} seed(s)<br>
                        SSRF validation passed. Workers are polling structured tasks.
                    </div>
                `;
                DOM.explorerFeedback.classList.remove('hidden');
                await fetchSystemStatus();
            } catch (err) {
                showToast(`Launch failed: ${err.message}`, 'error');
                DOM.explorerFeedback.className = 'feedback-banner feedback-error';
                DOM.explorerFeedback.innerHTML = `<div class="feedback-icon">✕</div><div><strong>Rejected:</strong> ${escapeHtml(err.message)}</div>`;
                DOM.explorerFeedback.classList.remove('hidden');
            } finally {
                DOM.explorerSubmitBtn.disabled = false;
                DOM.explorerSubmitBtn.innerHTML = '<span>LAUNCH CRAWL JOB</span>';
            }
        });
    }

    // =========================================================================
    // JOBS MANAGER (VIEW 3)
    // =========================================================================
    async function fetchJobs() {
        if (!DOM.jobsTbody) return;
        try {
            const data = await apiRequest('/jobs');
            const jobs = Array.isArray(data) ? data : (data.jobs || []);
            state.cachedJobs = jobs;
            if (DOM.sidebarJobCount) DOM.sidebarJobCount.textContent = jobs.length;
            populateJobDropdown(jobs);
            renderJobsTable(jobs);
        } catch (err) {
            DOM.jobsTbody.innerHTML = `<tr><td colspan="7" class="text-rose">Failed loading jobs: ${escapeHtml(err.message)}</td></tr>`;
        }
    }

    function populateJobDropdown(jobs) {
        if (!DOM.resetJobSelect) return;
        const prev = DOM.resetJobSelect.value;
        DOM.resetJobSelect.innerHTML = `<option value="">Select Job ID...</option>` +
            jobs.map(j => `<option value="${escapeHtml(j.id)}">${escapeHtml(j.id)} — ${escapeHtml(j.name)} (${escapeHtml(j.status)})</option>`).join('');
        if (prev) DOM.resetJobSelect.value = prev;
    }

    function renderJobsTable(jobs) {
        const filtered = state.activeJobFilter === 'ALL'
            ? jobs
            : jobs.filter(j => j.status === state.activeJobFilter);

        if (filtered.length === 0) {
            DOM.jobsTbody.innerHTML = `<tr><td colspan="7" style="text-align:center;padding:28px;" class="text-muted">No crawl jobs match "${escapeHtml(state.activeJobFilter)}". Launch one from Crawl Explorer.</td></tr>`;
            return;
        }

        DOM.jobsTbody.innerHTML = filtered.map(j => {
            const crawled = j.pages_crawled || 0;
            const maxPages = j.max_pages || 100;
            const pct = Math.min(100, Math.round((crawled / maxPages) * 100));
            const canPause = j.status === 'RUNNING';
            const canResume = j.status === 'PAUSED';
            const canCancel = j.status === 'RUNNING' || j.status === 'PAUSED' || j.status === 'QUEUED';
            const canRetry = (j.pages_failed || 0) > 0 || j.status === 'FAILED';

            return `
                <tr>
                    <td class="font-mono text-cyan">${escapeHtml(j.id)}</td>
                    <td>
                        <strong>${escapeHtml(j.name || 'Crawl Job')}</strong>
                        <div class="text-muted" style="font-size:10px;">${escapeHtml((j.seed_urls || []).slice(0, 2).join(', '))}</div>
                    </td>
                    <td><span class="badge ${jobBadgeClass(j.status)}">${escapeHtml(j.status)}</span></td>
                    <td>
                        <span>${formatNumber(crawled)} / ${formatNumber(maxPages)} (${j.pages_failed || 0} err)</span>
                        <div class="progress-track"><div class="progress-fill" style="width:${pct}%"></div></div>
                    </td>
                    <td>${j.current_depth || 0} / ${j.max_depth || 3}</td>
                    <td class="text-muted">${formatRelativeTime(j.created_at)}</td>
                    <td>
                        <div class="row-action-btns">
                            <button type="button" class="btn-job-action" data-op="inspect" data-id="${escapeHtml(j.id)}">Inspect</button>
                            ${canPause ? `<button type="button" class="btn-job-action" data-op="pause" data-id="${escapeHtml(j.id)}">Pause</button>` : ''}
                            ${canResume ? `<button type="button" class="btn-job-action" data-op="resume" data-id="${escapeHtml(j.id)}">Resume</button>` : ''}
                            ${canRetry ? `<button type="button" class="btn-job-action" data-op="retry" data-id="${escapeHtml(j.id)}">Retry Failed</button>` : ''}
                            ${canCancel ? `<button type="button" class="btn-job-action text-rose" data-op="cancel" data-id="${escapeHtml(j.id)}">Cancel</button>` : ''}
                            <button type="button" class="btn-job-action" data-op="export" data-id="${escapeHtml(j.id)}">Export</button>
                        </div>
                    </td>
                </tr>
            `;
        }).join('');

        DOM.jobsTbody.querySelectorAll('.btn-job-action').forEach(btn => {
            btn.addEventListener('click', async () => {
                const jobId = btn.dataset.id;
                const op = btn.dataset.op;
                if (op === 'inspect') return openJobInspector(jobId);
                if (op === 'export') {
                    window.open(getApiUrl(`/jobs/${encodeURIComponent(jobId)}/export?format=json`), '_blank');
                    return;
                }
                const execOp = async () => {
                    try {
                        await apiRequest(`/jobs/${encodeURIComponent(jobId)}/${op}`, { method: 'POST' });
                        showToast(`Job ${jobId} ${op} executed`, 'success');
                        fetchJobs();
                        fetchSystemStatus();
                    } catch (err) {
                        showToast(`Job action error: ${err.message}`, 'error');
                    }
                };
                if (op === 'cancel') {
                    openConfirmDialog(`Cancel crawl job "${jobId}"? Remaining queued URLs for this job will be discarded.`, execOp);
                } else {
                    await execOp();
                }
            });
        });
    }

    async function openJobInspector(jobId) {
        try {
            const detail = await apiRequest(`/jobs/${encodeURIComponent(jobId)}`);
            const job = detail.job || {};
            const errors = detail.errors || [];
            const events = detail.events || [];
            const workers = detail.associated_workers || [];

            DOM.entityModalBadge.textContent = `JOB // ${job.status || ''}`;
            DOM.entityModalTitle.textContent = `${job.name || job.id}`;
            DOM.entityModalBody.innerHTML = `
                <div class="inspector-metrics-grid">
                    <div class="insp-stat"><span class="insp-stat-label">CRAWLED</span><span class="insp-stat-val font-mono text-emerald">${job.pages_crawled || 0} / ${job.max_pages || 0}</span></div>
                    <div class="insp-stat"><span class="insp-stat-label">FAILED</span><span class="insp-stat-val font-mono text-rose">${job.pages_failed || 0}</span></div>
                    <div class="insp-stat"><span class="insp-stat-label">DISCOVERED URLS</span><span class="insp-stat-val font-mono text-cyan">${job.discovered_count || 0}</span></div>
                    <div class="insp-stat"><span class="insp-stat-label">DEPTH</span><span class="insp-stat-val font-mono">${job.current_depth || 0} / ${job.max_depth || 0}</span></div>
                </div>
                <div class="inspector-section">
                    <span class="inspector-label">SEED URLS</span>
                    <div class="inspector-link-list">${(detail.seeds || []).map(u => `<span class="insp-link font-mono">${escapeHtml(u)}</span>`).join('')}</div>
                </div>
                <div class="inspector-section">
                    <span class="inspector-label">ASSOCIATED WORKERS (${workers.length})</span>
                    <div class="font-mono">${workers.length ? escapeHtml(workers.join(', ')) : 'None recorded'}</div>
                </div>
                <div class="inspector-section">
                    <span class="inspector-label">RECENT ERRORS (${errors.length})</span>
                    <div class="inspector-link-list">
                        ${errors.length ? errors.map(er => `<div class="font-mono text-rose" style="font-size:11px;">[${escapeHtml(er.error_type)}] ${escapeHtml(er.url)} — ${escapeHtml(er.error_message)}</div>`).join('') : '<span class="text-muted">Zero errors recorded</span>'}
                    </div>
                </div>
                <div class="inspector-section">
                    <span class="inspector-label">EVENT HISTORY (${events.length})</span>
                    <div class="inspector-link-list">
                        ${events.map(ev => `<div class="font-mono" style="font-size:11px;">[${escapeHtml(ev.event_type)}] ${escapeHtml(ev.message)}</div>`).join('') || '<span class="text-muted">No events</span>'}
                    </div>
                </div>
                <div class="form-row">
                    <button type="button" class="telemetry-btn secondary-btn" onclick="window.open('${escapeHtml(getApiUrl(`/jobs/${encodeURIComponent(jobId)}/export?format=csv`))}','_blank')">EXPORT CSV</button>
                    <button type="button" class="telemetry-btn secondary-btn" onclick="window.open('${escapeHtml(getApiUrl(`/jobs/${encodeURIComponent(jobId)}/export?format=json`))}','_blank')">EXPORT JSON</button>
                </div>
            `;
            DOM.entityInspectorModal.classList.remove('hidden');
        } catch (err) {
            showToast(`Failed loading job details: ${err.message}`, 'error');
        }
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
    // SEARCH INDEX (VIEW 4)
    // =========================================================================
    function buildSearchParams(limit = PAGE_LIMIT, offset = state.searchOffset) {
        const params = new URLSearchParams();
        const q = DOM.searchMainInput ? DOM.searchMainInput.value.trim() : '';
        if (q) params.set('q', q);
        if (state.activeSearchFilter) params.set('status', state.activeSearchFilter);
        if (DOM.searchDomainSelect && DOM.searchDomainSelect.value) params.set('domain', DOM.searchDomainSelect.value);
        if (DOM.searchSortSelect && DOM.searchSortSelect.value) params.set('sort', DOM.searchSortSelect.value);
        if (DOM.searchDateFrom && DOM.searchDateFrom.value) params.set('from', DOM.searchDateFrom.value);
        params.set('limit', String(limit));
        params.set('offset', String(offset));
        return params;
    }

    async function populateDomainFilter() {
        if (!DOM.searchDomainSelect) return;
        try {
            const res = await apiRequest('/domains?limit=100');
            const domains = Array.isArray(res) ? res : (res.domains || []);
            const currentVal = DOM.searchDomainSelect.value;
            DOM.searchDomainSelect.innerHTML = `<option value="">All Domains (${domains.length})</option>` +
                domains.map(d => `<option value="${escapeHtml(d.domain)}">${escapeHtml(d.domain)} (${d.total_pages})</option>`).join('');
            DOM.searchDomainSelect.value = currentVal;
        } catch {
            // ignore
        }
    }

    async function executeSearch(resetOffset = false) {
        if (!DOM.searchResultsContainer) return;
        if (resetOffset) state.searchOffset = 0;

        DOM.searchResultsContainer.innerHTML = `<div class="loading-state-box"><span>Querying ranked PostgreSQL search index...</span></div>`;

        try {
            const params = buildSearchParams(PAGE_LIMIT, state.searchOffset);
            const res = await apiRequest(`/search?${params.toString()}`);
            const results = Array.isArray(res) ? res : (res.results || []);
            const total = Array.isArray(res) ? results.length : (res.total ?? results.length);
            state.cachedPages = results;
            state.searchTotal = total;

            if (DOM.searchTallyText) {
                const startIdx = total === 0 ? 0 : state.searchOffset + 1;
                const endIdx = state.searchOffset + results.length;
                DOM.searchTallyText.textContent = `Showing ${startIdx}–${endIdx} of ${total} page(s)`;
            }

            const currentPageNum = Math.floor(state.searchOffset / PAGE_LIMIT) + 1;
            const totalPages = Math.max(1, Math.ceil(total / PAGE_LIMIT));
            if (DOM.searchPageIndicator) DOM.searchPageIndicator.textContent = `Page ${currentPageNum} / ${totalPages}`;
            if (DOM.searchPrevBtn) DOM.searchPrevBtn.disabled = state.searchOffset <= 0;
            if (DOM.searchNextBtn) DOM.searchNextBtn.disabled = (state.searchOffset + results.length) >= total;

            renderSearchResults(results);
        } catch (err) {
            DOM.searchResultsContainer.innerHTML = `<div class="empty-state-notice"><span class="text-rose">Search error: ${escapeHtml(err.message)}</span></div>`;
        }
    }

    function renderSearchResults(results) {
        if (results.length === 0) {
            DOM.searchResultsContainer.innerHTML = `
                <div class="empty-state-notice">
                    <strong>No crawled pages found matching your filters</strong>
                    <span>Try clearing search keywords/filters or launch a new crawl in Crawl Explorer.</span>
                </div>
            `;
            return;
        }

        DOM.searchResultsContainer.innerHTML = results.map(item => `
            <div class="search-result-card" data-id="${escapeHtml(item.id)}" data-url="${escapeHtml(item.url)}">
                <div class="res-header-row">
                    <span class="status-code-badge ${statusBadgeClass(item.status_code)}">${item.status_code || 0}</span>
                    <h4 class="res-title">${escapeHtml(item.title || item.url)}</h4>
                    <span class="res-latency font-mono">${item.latency_ms || 0}ms</span>
                </div>
                <a href="${escapeHtml(item.url)}" target="_blank" rel="noopener noreferrer" class="res-url-link font-mono">${escapeHtml(item.url)}</a>
                ${item.description ? `<p class="res-snippet">${escapeHtml(item.description)}</p>` : ''}
                <div class="res-meta-row">
                    <span class="res-meta-tag">${escapeHtml(item.domain || '')}</span>
                    <span class="res-meta-tag">Depth: ${item.depth || 0}</span>
                    <span class="res-meta-tag">Size: ${formatBytes(item.content_length)}</span>
                    <span class="res-meta-tag">Links: ${item.links_count || 0}</span>
                    <span class="res-meta-tag">${formatRelativeTime(item.crawled_at)}</span>
                    <button type="button" class="btn-inspect-page" data-copy="${escapeHtml(item.url)}">COPY URL</button>
                    <button type="button" class="btn-inspect-page" data-inspect-id="${escapeHtml(item.id)}" data-url="${escapeHtml(item.url)}">INSPECT PAGE</button>
                </div>
            </div>
        `).join('');

        DOM.searchResultsContainer.querySelectorAll('[data-copy]').forEach(btn => {
            btn.addEventListener('click', e => {
                e.stopPropagation();
                copyToClipboard(btn.dataset.copy, 'URL');
            });
        });

        DOM.searchResultsContainer.querySelectorAll('.search-result-card').forEach(card => {
            card.addEventListener('click', e => {
                if (e.target.tagName === 'A' || e.target.hasAttribute('data-copy')) return;
                const p = results.find(r => String(r.id) === card.dataset.id || r.url === card.dataset.url);
                if (p) openPageInspector(p);
            });
        });
    }

    function initSearchView() {
        if (!DOM.searchIndexForm) return;
        DOM.searchIndexForm.addEventListener('submit', e => { e.preventDefault(); executeSearch(true); });
        if (DOM.searchClearBtn) {
            DOM.searchClearBtn.addEventListener('click', () => {
                DOM.searchMainInput.value = '';
                DOM.searchClearBtn.classList.add('hidden');
                executeSearch(true);
            });
        }
        DOM.searchMainInput.addEventListener('input', () => {
            DOM.searchClearBtn.classList.toggle('hidden', DOM.searchMainInput.value.length === 0);
        });
        DOM.searchChips.forEach(chip => {
            chip.addEventListener('click', () => {
                DOM.searchChips.forEach(c => c.classList.remove('active'));
                chip.classList.add('active');
                state.activeSearchFilter = chip.dataset.filter || '';
                executeSearch(true);
            });
        });
        [DOM.searchDomainSelect, DOM.searchSortSelect, DOM.searchDateFrom].forEach(sel => {
            if (sel) sel.addEventListener('change', () => executeSearch(true));
        });
        if (DOM.searchPrevBtn) {
            DOM.searchPrevBtn.addEventListener('click', () => {
                state.searchOffset = Math.max(0, state.searchOffset - PAGE_LIMIT);
                executeSearch(false);
            });
        }
        if (DOM.searchNextBtn) {
            DOM.searchNextBtn.addEventListener('click', () => {
                state.searchOffset += PAGE_LIMIT;
                executeSearch(false);
            });
        }
        if (DOM.searchExportCsvBtn) {
            DOM.searchExportCsvBtn.addEventListener('click', () => {
                const params = buildSearchParams(200, 0);
                params.set('format', 'csv');
                window.open(getApiUrl(`/export?${params.toString()}`), '_blank');
            });
        }
        if (DOM.searchExportJsonBtn) {
            DOM.searchExportJsonBtn.addEventListener('click', () => {
                const params = buildSearchParams(200, 0);
                params.set('format', 'json');
                window.open(getApiUrl(`/export?${params.toString()}`), '_blank');
            });
        }
    }

    // =========================================================================
    // PAGE DETAIL INSPECTOR MODAL
    // =========================================================================
    async function openPageInspector(summaryPage) {
        let page = summaryPage;
        if (summaryPage.id) {
            try {
                const full = await apiRequest(`/pages/${encodeURIComponent(summaryPage.id)}`);
                if (full && full.url) page = full;
            } catch {
                // use summary fields cleanly
            }
        }

        const internalLinks = page.internal_links || [];
        const externalLinks = page.external_links || [];
        const headings = page.headings || [];

        DOM.inspectorModalTitle.textContent = page.title || page.url;
        DOM.inspectorModalBody.innerHTML = `
            <div class="inspector-section">
                <span class="inspector-label">TARGET & FINAL URL</span>
                <div class="form-row">
                    <a href="${escapeHtml(page.url)}" target="_blank" rel="noopener noreferrer" class="inspector-url font-mono flex-1">${escapeHtml(page.url)}</a>
                    <button type="button" class="telemetry-btn secondary-btn" id="insp-copy-url-btn">COPY URL</button>
                </div>
                ${page.final_url && page.final_url !== page.url ? `<div class="text-muted font-mono" style="font-size:11px;">Final redirect: ${escapeHtml(page.final_url)}</div>` : ''}
                ${page.canonical_url ? `<div class="text-muted font-mono" style="font-size:11px;">Canonical: ${escapeHtml(page.canonical_url)}</div>` : ''}
            </div>

            <div class="inspector-metrics-grid">
                <div class="insp-stat"><span class="insp-stat-label">HTTP STATUS</span><span class="insp-stat-val font-mono">${page.status_code || 0}</span></div>
                <div class="insp-stat"><span class="insp-stat-label">LATENCY</span><span class="insp-stat-val font-mono text-cyan">${page.latency_ms || 0} ms</span></div>
                <div class="insp-stat"><span class="insp-stat-label">SIZE</span><span class="insp-stat-val font-mono">${formatBytes(page.content_length)}</span></div>
                <div class="insp-stat"><span class="insp-stat-label">DEPTH</span><span class="insp-stat-val font-mono">${page.depth || 0}</span></div>
            </div>

            ${page.error_message ? `<div class="inspector-section"><span class="inspector-label text-rose">ERROR DETAILS</span><div class="inspector-text text-rose font-mono">${escapeHtml(page.error_message)}</div></div>` : ''}
            ${page.description ? `<div class="inspector-section"><span class="inspector-label">META DESCRIPTION</span><div class="inspector-text">${escapeHtml(page.description)}</div></div>` : ''}
            ${headings.length ? `<div class="inspector-section"><span class="inspector-label">EXTRACTED HEADINGS (${headings.length})</span><div class="inspector-link-list">${headings.map(h => `<div class="font-mono" style="font-size:11px;">${escapeHtml(h)}</div>`).join('')}</div></div>` : (page.h1 ? `<div class="inspector-section"><span class="inspector-label">PRIMARY H1</span><div class="inspector-text font-mono">${escapeHtml(page.h1)}</div></div>` : '')}
            ${page.extracted_text ? `<div class="inspector-section"><span class="inspector-label">EXTRACTED TEXT EXCERPT</span><div class="inspector-text" style="max-height:140px;overflow-y:auto;">${escapeHtml(page.extracted_text.slice(0, 1200))}</div></div>` : ''}

            <div class="inspector-section">
                <span class="inspector-label">INTERNAL LINKS (${internalLinks.length})</span>
                <div class="inspector-link-list">${internalLinks.length ? internalLinks.map(l => `<a href="${escapeHtml(l)}" target="_blank" rel="noopener noreferrer" class="insp-link font-mono">${escapeHtml(l)}</a>`).join('') : '<span class="text-muted">None extracted</span>'}</div>
            </div>
            <div class="inspector-section">
                <span class="inspector-label">EXTERNAL LINKS (${externalLinks.length})</span>
                <div class="inspector-link-list">${externalLinks.length ? externalLinks.map(l => `<a href="${escapeHtml(l)}" target="_blank" rel="noopener noreferrer" class="insp-link font-mono">${escapeHtml(l)}</a>`).join('') : '<span class="text-muted">None extracted</span>'}</div>
            </div>
        `;
        const copyBtn = $('insp-copy-url-btn');
        if (copyBtn) copyBtn.addEventListener('click', () => copyToClipboard(page.url, 'Page URL'));
        DOM.pageInspectorModal.classList.remove('hidden');
    }

    // =========================================================================
    // DOMAINS REGISTRY (VIEW 5)
    // =========================================================================
    async function fetchDomains() {
        if (!DOM.domainsBarsView) return;
        try {
            const res = await apiRequest('/domains?limit=100');
            state.cachedDomains = Array.isArray(res) ? res : (res.domains || []);
            renderDomainsList();
        } catch (err) {
            DOM.domainsBarsView.innerHTML = `<div class="empty-state-notice"><span class="text-rose">Failed fetching domains: ${escapeHtml(err.message)}</span></div>`;
        }
    }

    function renderDomainsList() {
        const kw = DOM.domainFilterInput ? DOM.domainFilterInput.value.toLowerCase().trim() : '';
        const list = kw ? state.cachedDomains.filter(d => d.domain.toLowerCase().includes(kw)) : state.cachedDomains;
        if (list.length === 0) {
            DOM.domainsBarsView.innerHTML = `<div class="empty-state-notice"><span>No traversed host domains recorded yet.</span></div>`;
            return;
        }
        const maxPages = Math.max(...list.map(d => d.total_pages || 1), 1);
        DOM.domainsBarsView.innerHTML = list.map(d => {
            const pct = Math.max(4, Math.round(((d.total_pages || 0) / maxPages) * 100));
            return `
                <div class="domain-stat-row">
                    <div class="domain-info-col">
                        <span class="domain-host font-mono">${escapeHtml(d.domain)}</span>
                        <span class="domain-count font-mono">${formatNumber(d.total_pages)} pages • ${formatBytes(d.total_bytes || 0)}</span>
                    </div>
                    <div class="domain-bar-track"><div class="domain-bar-fill" style="width:${pct}%"></div></div>
                    <div class="domain-latency-col font-mono text-muted">${d.avg_latency_ms || 0}ms avg • ${d.error_count || 0} err</div>
                    <button type="button" class="btn-job-action" data-domain="${escapeHtml(d.domain)}">Search Host</button>
                </div>
            `;
        }).join('');

        DOM.domainsBarsView.querySelectorAll('[data-domain]').forEach(btn => {
            btn.addEventListener('click', () => {
                if (DOM.searchDomainSelect) {
                    populateDomainFilter().then(() => {
                        DOM.searchDomainSelect.value = btn.dataset.domain;
                        switchView('view-search');
                        executeSearch(true);
                    });
                }
            });
        });
    }

    // =========================================================================
    // WORKER FLEET MONITOR (VIEW 6)
    // =========================================================================
    async function fetchWorkers() {
        if (!DOM.workersGridContainer) return;
        try {
            const res = await apiRequest('/workers');
            const workers = Array.isArray(res) ? res : (res.workers || []);
            state.cachedWorkers = workers;
            if (DOM.sidebarWorkerCount) DOM.sidebarWorkerCount.textContent = workers.length;

            if (workers.length === 0) {
                DOM.workersGridContainer.innerHTML = `
                    <div class="empty-state-notice" style="grid-column:1/-1;">
                        <strong>No Persistent Go Worker Daemons Connected</strong>
                        <span>Distributed Crawler never fabricates dummy workers. Start real Go worker processes to populate the fleet:</span>
                        <pre class="empty-code-snippet"># Option A: Docker Compose horizontal scaling
docker compose up -d --scale worker=2

# Option B: Native Go worker daemon
go run cmd/worker/main.go -master http://localhost:8080 -concurrency 4</pre>
                    </div>
                `;
                return;
            }

            DOM.workersGridContainer.innerHTML = workers.map(w => {
                const isOnline = w.status === 'ACTIVE' || w.status === 'IDLE';
                const dotCls = isOnline ? 'dot-online' : (w.status === 'UNHEALTHY' ? 'dot-err' : 'dot-warn');
                const badgeCls = isOnline ? 'badge-emerald' : (w.status === 'UNHEALTHY' ? 'badge-rose' : 'badge-amber');
                return `
                    <div class="worker-card" data-worker-id="${escapeHtml(w.id)}">
                        <div class="worker-card-header">
                            <div class="worker-id-group">
                                <span class="status-mini-dot ${dotCls}"></span>
                                <h4 class="worker-id font-mono">${escapeHtml(w.id)}</h4>
                            </div>
                            <span class="badge ${badgeCls}">${escapeHtml(w.status)}</span>
                        </div>
                        <div class="worker-details-grid">
                            <div class="worker-stat-item"><span class="w-label">HOST / VER</span><span class="w-val font-mono">${escapeHtml(w.hostname || '-')} v${escapeHtml(w.version || '2.0')}</span></div>
                            <div class="worker-stat-item"><span class="w-label">HEARTBEAT AGE</span><span class="w-val font-mono">${Math.round(w.last_seen_sec_ago || 0)}s ago</span></div>
                            <div class="worker-stat-item"><span class="w-label">COMPLETED / FAIL</span><span class="w-val font-mono text-emerald">${w.tasks_completed || 0} / <span class="text-rose">${w.tasks_failed || 0}</span></span></div>
                            <div class="worker-stat-item"><span class="w-label">AVG LATENCY</span><span class="w-val font-mono text-cyan">${w.avg_latency_ms || 0} ms</span></div>
                        </div>
                        <div class="worker-current-task">
                            <span class="w-label">${w.current_task ? 'CURRENTLY CRAWLING' : 'ACTIVE SLOTS: ' + (w.tasks_active || 0)}</span>
                            <div class="task-url-truncate font-mono">${escapeHtml(w.current_task || 'Idle polling frontier queue')}</div>
                        </div>
                    </div>
                `;
            }).join('');

            DOM.workersGridContainer.querySelectorAll('.worker-card').forEach(card => {
                card.addEventListener('click', () => {
                    const wk = workers.find(w => w.id === card.dataset.workerId);
                    if (wk) openWorkerInspector(wk);
                });
            });
        } catch (err) {
            DOM.workersGridContainer.innerHTML = `<div class="empty-state-notice" style="grid-column:1/-1;"><span class="text-rose">Failed querying workers: ${escapeHtml(err.message)}</span></div>`;
        }
    }

    function openWorkerInspector(wk) {
        DOM.entityModalBadge.textContent = `WORKER // ${wk.status}`;
        DOM.entityModalTitle.textContent = wk.id;
        DOM.entityModalBody.innerHTML = `
            <div class="inspector-metrics-grid">
                <div class="insp-stat"><span class="insp-stat-label">HOSTNAME</span><span class="insp-stat-val font-mono">${escapeHtml(wk.hostname)}</span></div>
                <div class="insp-stat"><span class="insp-stat-label">VERSION</span><span class="insp-stat-val font-mono">${escapeHtml(wk.version || '2.0.0')}</span></div>
                <div class="insp-stat"><span class="insp-stat-label">UPTIME</span><span class="insp-stat-val font-mono">${formatDurationSec(wk.uptime_sec || 0)}</span></div>
                <div class="insp-stat"><span class="insp-stat-label">LAST HEARTBEAT</span><span class="insp-stat-val font-mono">${Math.round(wk.last_seen_sec_ago || 0)}s ago</span></div>
            </div>
            <div class="inspector-section">
                <span class="inspector-label">EXECUTION COUNTERS</span>
                <div class="inspector-text font-mono">Tasks Active: ${wk.tasks_active || 0} • Completed: ${wk.tasks_completed || 0} • Failed: ${wk.tasks_failed || 0} • Avg Latency: ${wk.avg_latency_ms || 0}ms</div>
            </div>
            ${wk.current_task ? `<div class="inspector-section"><span class="inspector-label">ACTIVE URL</span><div class="inspector-text font-mono">${escapeHtml(wk.current_task)}</div></div>` : ''}
        `;
        DOM.entityInspectorModal.classList.remove('hidden');
    }

    // =========================================================================
    // ACTIVITY LOGS (VIEW 7)
    // =========================================================================
    async function fetchActivityLogs() {
        if (!DOM.activityTerminalFeed || state.feedPaused) return;
        try {
            const params = new URLSearchParams({ limit: '100' });
            if (DOM.activitySeveritySelect && DOM.activitySeveritySelect.value) params.set('severity', DOM.activitySeveritySelect.value);
            if (DOM.activitySearchInput && DOM.activitySearchInput.value.trim()) params.set('q', DOM.activitySearchInput.value.trim());

            const res = await apiRequest(`/activity?${params.toString()}`);
            const events = Array.isArray(res) ? res : (res.events || []);
            state.cachedEvents = events;

            if (events.length === 0) {
                DOM.activityTerminalFeed.innerHTML = `<div class="terminal-line text-muted">[STREAM] No activity events match current filters.</div>`;
                return;
            }

            DOM.activityTerminalFeed.innerHTML = events.map(ev => {
                const t = ev.created_at ? new Date(ev.created_at).toLocaleTimeString() : '--:--:--';
                const sev = ev.severity || 'INFO';
                const sevCls = sev === 'ERROR' ? 'text-rose' : (sev === 'WARN' ? 'text-amber' : 'text-emerald');
                return `
                    <div class="terminal-line">
                        <span class="term-time">[${escapeHtml(t)}]</span>
                        <span class="term-type ${sevCls}">${escapeHtml(sev)}</span>
                        <span class="term-type text-cyan">${escapeHtml(ev.event_type)}</span>
                        ${ev.job_id ? `<span class="text-muted">[${escapeHtml(ev.job_id)}]</span>` : ''}
                        ${ev.worker_id ? `<span class="term-worker">[${escapeHtml(ev.worker_id)}]</span>` : ''}
                        <span class="term-msg">${escapeHtml(ev.message || ev.url || '')}</span>
                    </div>
                `;
            }).join('');
        } catch (err) {
            DOM.activityTerminalFeed.innerHTML = `<div class="terminal-line text-rose">[STREAM ERROR] ${escapeHtml(err.message)}</div>`;
        }
    }

    function initActivityView() {
        if (DOM.feedPauseBtn) {
            DOM.feedPauseBtn.addEventListener('click', () => {
                state.feedPaused = !state.feedPaused;
                DOM.feedPauseBtn.textContent = state.feedPaused ? 'RESUME' : 'PAUSE';
                DOM.feedPauseBtn.classList.toggle('primary-btn', state.feedPaused);
            });
        }
        if (DOM.feedClearBtn) {
            DOM.feedClearBtn.addEventListener('click', () => {
                DOM.activityTerminalFeed.innerHTML = `<div class="terminal-line text-muted">[LOCAL VIEW CLEARED] Backend persisted events remain intact. Click RESUME or switch tabs to reload.</div>`;
            });
        }
        if (DOM.feedCopyBtn) {
            DOM.feedCopyBtn.addEventListener('click', () => {
                const txt = state.cachedEvents.map(e => `[${e.created_at}] ${e.severity} ${e.event_type} ${e.message}`).join('\n');
                copyToClipboard(txt || 'No events', 'Activity logs');
            });
        }
        if (DOM.activitySeveritySelect) DOM.activitySeveritySelect.addEventListener('change', fetchActivityLogs);
        if (DOM.activitySearchInput) DOM.activitySearchInput.addEventListener('input', fetchActivityLogs);
    }

    // =========================================================================
    // ANALYTICS VIEW (VIEW 8)
    // =========================================================================
    async function fetchAnalytics() {
        try {
            const an = await apiRequest(`/analytics?range=${encodeURIComponent(state.analyticsRange)}`);
            DOM.anAvgLatency.textContent = an.total_requests > 0 ? `${an.avg_latency_ms} ms` : '0 ms';
            DOM.anLatencyDetail.textContent = `p50: ${an.p50_latency_ms || 0}ms • p95: ${an.p95_latency_ms || 0}ms`;
            DOM.anSuccessRate.textContent = `${Number(an.success_rate || 0).toFixed(1)}%`;
            DOM.anSuccessDetail.textContent = `${an.success_count || 0} ok / ${an.failed_count || 0} err`;
            DOM.anRobotsRate.textContent = `${an.robots_blocked || 0} blocked`;
            DOM.anLeaseReclaims.textContent = `${an.lease_reclaims || 0} reclaimed`;
            DOM.anRetriesDetail.textContent = `${an.retry_count || 0} fetch retries executed`;

            renderMiniBars(DOM.anStatusChart, an.status_breakdown || [], 'var(--color-emerald)');
            renderMiniBars(DOM.anErrorChart, an.error_categories || [], 'var(--color-rose)');
            renderMiniBars(DOM.anThroughputChart, (an.throughput_series || []).map(tp => ({
                label: tp.bucket, count: tp.pages, suffix: `${tp.pages} pages (${tp.errors} err)`
            })), 'var(--color-cyan)');

            const jobItems = Object.entries(an.job_breakdown || {}).map(([label, count]) => ({ label, count }));
            renderMiniBars(DOM.anJobsChart, jobItems, 'var(--color-purple)');
        } catch (err) {
            DOM.anLatencyDetail.textContent = `Analytics error: ${err.message}`;
        }
    }

    // =========================================================================
    // SETTINGS & 7-TIER SAFE RESETS (VIEW 9)
    // =========================================================================
    function initSettingsView() {
        if (DOM.backendUrlInput) DOM.backendUrlInput.value = state.apiBase;
        if (DOM.adminTokenInput) DOM.adminTokenInput.value = state.adminToken;

        if (DOM.saveBackendUrlBtn) {
            DOM.saveBackendUrlBtn.addEventListener('click', async () => {
                const newBase = DOM.backendUrlInput.value.trim() || DEFAULT_API;
                state.apiBase = newBase;
                localStorage.setItem('crawler_api_base', newBase);
                const status = await fetchSystemStatus();
                showToast(status ? `Connected to ${newBase}` : `Failed connecting to ${newBase}`, status ? 'success' : 'error');
            });
        }

        if (DOM.saveAdminTokenBtn) {
            DOM.saveAdminTokenBtn.addEventListener('click', () => {
                state.adminToken = DOM.adminTokenInput.value.trim();
                sessionStorage.setItem('crawler_admin_token', state.adminToken);
                showToast(state.adminToken ? 'Admin token saved to current browser session' : 'Admin token cleared', 'info');
            });
        }

        const runReset = async (payload, okMsg) => {
            try {
                const res = await apiRequest('/reset', { method: 'POST', body: JSON.stringify(payload) });
                showToast(res.message || okMsg, 'success');
                await fetchSystemStatus();
                refreshCurrentView();
            } catch (err) {
                showToast(`Reset blocked: ${err.message}`, 'error');
            }
        };

        DOM.btnResetUi.addEventListener('click', () => {
            if (DOM.searchMainInput) DOM.searchMainInput.value = '';
            state.activeSearchFilter = '';
            state.searchOffset = 0;
            state.activeJobFilter = 'ALL';
            showToast('Local UI view filters cleared', 'success');
        });

        DOM.btnResetCancelJob.addEventListener('click', () => {
            const jobId = DOM.resetJobSelect.value;
            if (!jobId) return showToast('Select a job ID first', 'error');
            openConfirmDialog(`Cancel crawl job "${jobId}" and discard its pending tasks?`, () => runReset({ level: 'job', job_id: jobId }, 'Job cancelled'));
        });

        DOM.btnResetQueue.addEventListener('click', () => {
            openConfirmDialog('Flush pending Redis queue and active leases? Existing indexed pages in PostgreSQL will be preserved.', () => runReset({ level: 'queue' }, 'Queue flushed'));
        });

        DOM.btnResetJobData.addEventListener('click', () => {
            const jobId = DOM.resetJobSelect.value;
            if (!jobId) return showToast('Select a job ID first', 'error');
            openConfirmDialog(`Delete job "${jobId}" and ALL indexed pages/errors belonging to that job?`, () => runReset({ level: 'job_data', job_id: jobId }, 'Job data purged'));
        });

        DOM.btnResetData.addEventListener('click', () => {
            openConfirmDialog('Purge ALL crawled pages and domain statistics? Job definitions and workers remain.', () => runReset({ level: 'data' }, 'Indexed data purged'));
        });

        DOM.btnResetDev.addEventListener('click', () => {
            openConfirmDialog('Execute full development reset (clears frontier queue, jobs, events, and crawled pages)?', () => runReset({ level: 'dev' }, 'Dev environment reset'));
        });

        DOM.btnResetProd.addEventListener('click', () => {
            DOM.prodResetPhraseInput.value = '';
            DOM.confirmProdResetBtn.disabled = true;
            DOM.prodResetModal.classList.remove('hidden');
        });

        const closeProdModal = () => DOM.prodResetModal.classList.add('hidden');
        DOM.cancelProdResetBtn.addEventListener('click', closeProdModal);
        DOM.closeProdModalBtn.addEventListener('click', closeProdModal);
        DOM.prodResetPhraseInput.addEventListener('input', () => {
            DOM.confirmProdResetBtn.disabled = DOM.prodResetPhraseInput.value.trim() !== 'WIPE ALL DATA';
        });
        DOM.confirmProdResetBtn.addEventListener('click', async () => {
            closeProdModal();
            await runReset({ level: 'production', confirmation_phrase: 'WIPE ALL DATA' }, 'Production cluster reset');
        });
    }

    // =========================================================================
    // MODALS & VIEW CONTROLLER
    // =========================================================================
    function initModals() {
        if (DOM.closeInspectorBtn) DOM.closeInspectorBtn.addEventListener('click', () => DOM.pageInspectorModal.classList.add('hidden'));
        if (DOM.closeEntityBtn) DOM.closeEntityBtn.addEventListener('click', () => DOM.entityInspectorModal.classList.add('hidden'));

        [DOM.pageInspectorModal, DOM.entityInspectorModal, DOM.prodResetModal, DOM.confirmModal].forEach(modal => {
            if (!modal) return;
            modal.addEventListener('click', e => {
                if (e.target === modal) modal.classList.add('hidden');
            });
        });

        window.addEventListener('keydown', e => {
            if (e.key === 'Escape') {
                [DOM.pageInspectorModal, DOM.entityInspectorModal, DOM.prodResetModal, DOM.confirmModal].forEach(m => {
                    if (m) m.classList.add('hidden');
                });
            }
        });
    }

    function refreshCurrentView() {
        switch (state.currentView) {
            case 'view-overview': fetchSystemStatus(); break;
            case 'view-jobs': fetchJobs(); break;
            case 'view-search': populateDomainFilter(); executeSearch(false); break;
            case 'view-domains': fetchDomains(); break;
            case 'view-workers': fetchWorkers(); break;
            case 'view-activity': fetchActivityLogs(); break;
            case 'view-analytics': fetchAnalytics(); break;
            case 'view-settings': fetchJobs(); break;
        }
    }

    function init() {
        initNavigation();
        initCrawlExplorer();
        initJobsView();
        initSearchView();
        initActivityView();
        initSettingsView();
        initModals();

        if (DOM.domainFilterInput) DOM.domainFilterInput.addEventListener('input', renderDomainsList);
        if (DOM.analyticsRangeSelect) {
            DOM.analyticsRangeSelect.addEventListener('change', () => {
                state.analyticsRange = DOM.analyticsRangeSelect.value;
                fetchAnalytics();
            });
        }
        if (DOM.autoCrawlToggle) DOM.autoCrawlToggle.addEventListener('click', toggleAutoCrawl);
        if (DOM.crawlStepBtn) DOM.crawlStepBtn.addEventListener('click', () => runCrawlStep(false));

        fetchSystemStatus();
        state.telemetryTimer = setInterval(() => {
            fetchSystemStatus();
            if (state.currentView === 'view-activity' && !state.feedPaused) fetchActivityLogs();
            if (state.currentView === 'view-workers') fetchWorkers();
            if (state.currentView === 'view-jobs') fetchJobs();
        }, 4000);
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }
})();
