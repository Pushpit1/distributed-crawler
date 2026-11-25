package master

import (
	"context"
	"crypto/subtle"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"distributed-crawler/pkg/crawler"
	"distributed-crawler/pkg/db"
	"distributed-crawler/pkg/models"
)

// WorkerRegistry tracks live worker node heartbeats and health in memory alongside Postgres.
type WorkerRegistry struct {
	mu      sync.RWMutex
	workers map[string]*models.WorkerInfo
}

// ipRateLimiter provides lightweight sliding-window rate limiting per client IP.
type ipRateLimiter struct {
	mu       sync.Mutex
	requests map[string][]time.Time
	limit    int
	window   time.Duration
}

func newIPRateLimiter(limit int, window time.Duration) *ipRateLimiter {
	return &ipRateLimiter{
		requests: make(map[string][]time.Time),
		limit:    limit,
		window:   window,
	}
}

func (rl *ipRateLimiter) allow(ip string) bool {
	if rl.limit <= 0 {
		return true
	}
	now := time.Now()
	cutoff := now.Add(-rl.window)

	rl.mu.Lock()
	defer rl.mu.Unlock()

	timestamps := rl.requests[ip]
	validIdx := 0
	for _, ts := range timestamps {
		if ts.After(cutoff) {
			timestamps[validIdx] = ts
			validIdx++
		}
	}
	timestamps = timestamps[:validIdx]

	if len(timestamps) >= rl.limit {
		rl.requests[ip] = timestamps
		return false
	}

	rl.requests[ip] = append(timestamps, now)
	return true
}

// CoordinatorServer holds HTTP handlers, Redis frontier, PostgreSQL repository, and telemetry counters.
type CoordinatorServer struct {
	frontier            *crawler.Frontier
	pgRepo              *db.PostgresRepository
	registry            *WorkerRegistry
	politenessDelay     time.Duration
	leaseTimeout        time.Duration
	startTime           time.Time
	reaperCancel        context.CancelFunc
	allowPrivateSubnets bool
	adminToken          string
	enableProdReset     bool
	corsAllowedOrigins  string
	limiter             *ipRateLimiter
	stepParser          *crawler.Parser
	stepRobots          *crawler.RobotsCache

	// Atomic Prometheus telemetry counters
	crawlRequestsTotal        atomic.Int64
	crawlRequestsSuccessTotal atomic.Int64
	crawlRequestsFailedTotal  atomic.Int64
	crawlTaskRetriesTotal     atomic.Int64
	crawlDBErrorsTotal        atomic.Int64
	crawlLeasesReclaimedTotal atomic.Int64
	crawlRobotsBlockedTotal   atomic.Int64
	crawlDurationSumMs        atomic.Int64
	crawlDurationCount        atomic.Int64
}

// NewCoordinatorServer initializes the coordinator server instance and launches lease reclamation.
func NewCoordinatorServer(frontier *crawler.Frontier, pgRepo *db.PostgresRepository, politenessDelay time.Duration) *CoordinatorServer {
	leaseSec := 60
	if envLease := os.Getenv("TASK_LEASE_TIMEOUT_SEC"); envLease != "" {
		if v, err := strconv.Atoi(envLease); err == nil && v >= 5 {
			leaseSec = v
		}
	}

	rateLimit := 180
	if envRL := os.Getenv("RATE_LIMIT_RPM"); envRL != "" {
		if v, err := strconv.Atoi(envRL); err == nil && v > 0 {
			rateLimit = v
		}
	}

	enableProd := strings.EqualFold(os.Getenv("ENABLE_PRODUCTION_RESET"), "true")

	parser := crawler.NewParser(10*time.Second, "")
	robots := crawler.NewRobotsCache(parser.UserAgent())

	srv := &CoordinatorServer{
		frontier:           frontier,
		pgRepo:             pgRepo,
		registry:           &WorkerRegistry{workers: make(map[string]*models.WorkerInfo)},
		politenessDelay:    politenessDelay,
		leaseTimeout:       time.Duration(leaseSec) * time.Second,
		startTime:          time.Now(),
		adminToken:         os.Getenv("ADMIN_API_TOKEN"),
		enableProdReset:    enableProd,
		corsAllowedOrigins: os.Getenv("CORS_ALLOWED_ORIGINS"),
		limiter:            newIPRateLimiter(rateLimit, time.Minute),
		stepParser:         parser,
		stepRobots:         robots,
	}

	if frontier != nil {
		reaperCtx, cancel := context.WithCancel(context.Background())
		srv.reaperCancel = cancel
		go srv.startBackgroundReaper(reaperCtx)
	}

	return srv
}

// Close stops background coordinator goroutines gracefully.
func (s *CoordinatorServer) Close() {
	if s.reaperCancel != nil {
		s.reaperCancel()
	}
}

// SetAllowPrivateSubnets allows loopback targets strictly for deterministic integration testing.
func (s *CoordinatorServer) SetAllowPrivateSubnets(allow bool) {
	s.allowPrivateSubnets = allow
	s.stepParser.SetAllowPrivateSubnets(allow)
	s.stepRobots.SetAllowPrivateSubnets(allow)
}

// SetLeaseTimeout overrides the lease timeout duration (useful in tests).
func (s *CoordinatorServer) SetLeaseTimeout(d time.Duration) {
	s.leaseTimeout = d
}

// SetCORSHeaders applies origin policy and security headers cleanly.
func (s *CoordinatorServer) setCORSHeaders(w http.ResponseWriter, r *http.Request) {
	origin := "*"
	if s.corsAllowedOrigins != "" {
		reqOrigin := r.Header.Get("Origin")
		allowedList := strings.Split(s.corsAllowedOrigins, ",")
		origin = strings.TrimSpace(allowedList[0])
		for _, a := range allowedList {
			if strings.TrimSpace(a) == reqOrigin {
				origin = reqOrigin
				break
			}
		}
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, HEAD")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With, X-Admin-Token, X-Request-ID")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

// WithMiddleware wraps an HTTP handler with Request IDs, security headers, CORS, body size limits, and rate limiting.
func (s *CoordinatorServer) WithMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-ID")
		if reqID == "" {
			reqID = fmt.Sprintf("req-%d", time.Now().UnixNano())
		}
		w.Header().Set("X-Request-ID", reqID)
		s.setCORSHeaders(w, r)

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Exempt health/readiness/status telemetry from strict rate limiting
		if r.URL.Path != "/health" && r.URL.Path != "/api/health" && r.URL.Path != "/ready" && r.URL.Path != "/api/ready" && strings.HasPrefix(r.URL.Path, "/api/") {
			clientIP := clientIPFromRequest(r)
			if !s.limiter.allow(clientIP) {
				s.writeError(w, http.StatusTooManyRequests, "Rate limit exceeded", reqID)
				return
			}
		}

		if r.Body != nil && (r.Method == http.MethodPost || r.Method == http.MethodPut) {
			r.Body = http.MaxBytesReader(w, r.Body, 2*1024*1024) // 2 MB request limit
		}

		next.ServeHTTP(w, r)
	})
}

func clientIPFromRequest(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *CoordinatorServer) writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *CoordinatorServer) writeError(w http.ResponseWriter, status int, msg, reqID string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error":      msg,
		"status":     status,
		"request_id": reqID,
	})
}

func (s *CoordinatorServer) verifyAdminAuth(r *http.Request) bool {
	if s.adminToken == "" {
		return true // Admin auth disabled unless ADMIN_API_TOKEN is set
	}
	token := r.Header.Get("X-Admin-Token")
	if token == "" {
		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") {
			token = strings.TrimPrefix(auth, "Bearer ")
		}
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(s.adminToken)) == 1
}

// HandleCORS responds to HTTP OPTIONS preflight checks.
func (s *CoordinatorServer) HandleCORS(w http.ResponseWriter, r *http.Request) {
	s.setCORSHeaders(w, r)
	w.WriteHeader(http.StatusOK)
}

func (s *CoordinatorServer) startBackgroundReaper(reaperCtx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-reaperCtx.Done():
			return
		case <-ticker.C:
		}
		ctx, cancel := context.WithTimeout(reaperCtx, 4*time.Second)

		// 1. Reclaim abandoned task leases
		if s.frontier != nil {
			reclaimed, err := s.frontier.ReclaimExpiredLeases(ctx, s.leaseTimeout)
			if err == nil && reclaimed > 0 {
				s.crawlLeasesReclaimedTotal.Add(int64(reclaimed))
				log.Printf("♻️ Recovered %d expired crawl task leases back to queue.\n", reclaimed)
				if s.pgRepo != nil {
					_ = s.pgRepo.RecordEvent(ctx, models.CrawlEvent{
						EventType: "LEASE_RECLAIMED",
						Severity:  "WARN",
						Message:   fmt.Sprintf("Recovered %d expired task lease(s) back into frontier queue", reclaimed),
					})
				}
			}
		}

		// 2. Flag stale workers (>15s without heartbeat)
		s.registry.mu.Lock()
		now := time.Now()
		for _, w := range s.registry.workers {
			if now.Sub(w.LastHeartbeat) > 15*time.Second && w.Status != models.WorkerStateStopped && w.Status != models.WorkerStateUnhealthy {
				w.Status = models.WorkerStateUnhealthy
				if s.pgRepo != nil {
					_ = s.pgRepo.RecordEvent(ctx, models.CrawlEvent{
						WorkerID:  w.ID,
						EventType: "WORKER_STALE",
						Severity:  "WARN",
						Message:   fmt.Sprintf("Worker %s marked UNHEALTHY after missing heartbeat >15s", w.ID),
					})
				}
			}
		}
		s.registry.mu.Unlock()

		cancel()
	}
}

// HandleHealth handles GET /health and GET /api/health.
func (s *CoordinatorServer) HandleHealth(w http.ResponseWriter, r *http.Request) {
	s.setCORSHeaders(w, r)
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":     "healthy",
		"service":    "gopher-crawler-coordinator",
		"mode":       "cluster",
		"version":    "2.0.0",
		"uptime_sec": time.Since(s.startTime).Seconds(),
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
	})
}

// HandleReady handles GET /ready and GET /api/ready (verifies Postgres & Redis connectivity).
func (s *CoordinatorServer) HandleReady(w http.ResponseWriter, r *http.Request) {
	s.setCORSHeaders(w, r)
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	redisReady := true
	if s.frontier != nil {
		if _, err := s.frontier.QueueSize(ctx); err != nil {
			redisReady = false
		}
	}

	dbReady := true
	if s.pgRepo != nil {
		if err := s.pgRepo.Ping(ctx); err != nil {
			dbReady = false
		}
	}

	status := http.StatusOK
	readyText := "ready"
	if !redisReady || !dbReady {
		status = http.StatusServiceUnavailable
		readyText = "unready"
	}

	s.writeJSON(w, status, map[string]interface{}{
		"status":      readyText,
		"mode":        "cluster",
		"redis_ready": redisReady,
		"db_ready":    dbReady,
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
	})
}

// HandleRegister handles POST /api/register.
func (s *CoordinatorServer) HandleRegister(w http.ResponseWriter, r *http.Request) {
	s.setCORSHeaders(w, r)
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed", r.Header.Get("X-Request-ID"))
		return
	}

	var req models.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON payload", r.Header.Get("X-Request-ID"))
		return
	}

	hostname := crawler.SanitizeForLog(req.Hostname)
	if hostname == "" {
		hostname = "worker-node"
	}
	workerID := fmt.Sprintf("worker-%s-%d", hostname, time.Now().UnixNano()%100000)
	version := req.Version
	if version == "" {
		version = "2.0.0"
	}

	wInfo := models.WorkerInfo{
		ID:            workerID,
		Hostname:      hostname,
		Version:       version,
		Status:        models.WorkerStateIdle,
		StartedAt:     time.Now(),
		LastHeartbeat: time.Now(),
	}

	s.registry.mu.Lock()
	s.registry.workers[workerID] = &wInfo
	s.registry.mu.Unlock()

	if s.pgRepo != nil {
		if err := s.pgRepo.RegisterWorker(r.Context(), wInfo); err != nil {
			s.crawlDBErrorsTotal.Add(1)
		}
		_ = s.pgRepo.RecordEvent(r.Context(), models.CrawlEvent{
			WorkerID:  workerID,
			EventType: "WORKER_REGISTERED",
			Severity:  "INFO",
			Message:   fmt.Sprintf("Worker node %s registered from %s (v%s)", workerID, hostname, version),
		})
	}

	log.Printf("📥 Registered worker node: id=%s host=%s\n", workerID, hostname)

	s.writeJSON(w, http.StatusOK, models.RegisterResponse{
		WorkerID: workerID,
		Status:   "registered",
	})
}

// HandleHeartbeat handles POST /api/heartbeat.
func (s *CoordinatorServer) HandleHeartbeat(w http.ResponseWriter, r *http.Request) {
	s.setCORSHeaders(w, r)
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed", r.Header.Get("X-Request-ID"))
		return
	}

	var req models.HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON payload", r.Header.Get("X-Request-ID"))
		return
	}

	s.registry.mu.Lock()
	if existing, ok := s.registry.workers[req.WorkerID]; ok {
		existing.Status = req.Status
		existing.CurrentTask = req.CurrentTask
		existing.TasksActive = req.TasksActive
		existing.TasksCompleted = req.TasksCompleted
		existing.TasksFailed = req.TasksFailed
		existing.AvgLatencyMs = req.AvgLatencyMs
		existing.UptimeSec = req.UptimeSec
		existing.LastHeartbeat = time.Now()
	} else {
		host := req.Hostname
		if host == "" {
			host = "dynamic-worker"
		}
		s.registry.workers[req.WorkerID] = &models.WorkerInfo{
			ID:             req.WorkerID,
			Hostname:       host,
			Version:        req.Version,
			Status:         req.Status,
			CurrentTask:    req.CurrentTask,
			TasksActive:    req.TasksActive,
			TasksCompleted: req.TasksCompleted,
			TasksFailed:    req.TasksFailed,
			AvgLatencyMs:   req.AvgLatencyMs,
			UptimeSec:      req.UptimeSec,
			StartedAt:      time.Now(),
			LastHeartbeat:  time.Now(),
		}
	}
	s.registry.mu.Unlock()

	if s.pgRepo != nil {
		if err := s.pgRepo.UpdateWorkerHeartbeat(r.Context(), req); err != nil {
			s.crawlDBErrorsTotal.Add(1)
		}
	}

	s.writeJSON(w, http.StatusOK, models.HeartbeatResponse{
		Acknowledged: true,
		ServerTime:   time.Now().UTC().Format(time.RFC3339),
	})
}

// HandlePoll assigns the next polite structured task with lease and job constraints.
func (s *CoordinatorServer) HandlePoll(w http.ResponseWriter, r *http.Request) {
	s.setCORSHeaders(w, r)
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed", r.Header.Get("X-Request-ID"))
		return
	}

	var req models.PollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid poll payload", r.Header.Get("X-Request-ID"))
		return
	}

	s.registry.mu.Lock()
	if wInfo, exists := s.registry.workers[req.WorkerID]; exists {
		wInfo.LastHeartbeat = time.Now()
	}
	s.registry.mu.Unlock()

	if s.frontier == nil {
		s.writeJSON(w, http.StatusOK, models.PollResponse{})
		return
	}

	ctx := r.Context()
	task, err := s.frontier.GetPoliteStructuredTask(ctx, s.politenessDelay, req.WorkerID)
	if err != nil {
		log.Printf("❌ Failed polling next task: %v\n", err)
		s.writeError(w, http.StatusInternalServerError, "Failed to poll task frontier", r.Header.Get("X-Request-ID"))
		return
	}

	if task == nil {
		// Check if any RUNNING jobs finished their queue and should transition cleanly to COMPLETED
		s.checkCompletedJobs(ctx)
		s.writeJSON(w, http.StatusOK, models.PollResponse{})
		return
	}

	leaseKey := task.URL
	if task.JobID != "" {
		leaseKey = task.JobID + "::" + task.URL
	}

	s.writeJSON(w, http.StatusOK, models.PollResponse{
		URL:             task.URL,
		JobID:           task.JobID,
		Depth:           task.Depth,
		MaxDepth:        task.MaxDepth,
		Retries:         task.Retries,
		AllowedDomains:  task.AllowedDomains,
		ExcludePatterns: task.ExcludePatterns,
		LeaseKey:        leaseKey,
	})
}

func (s *CoordinatorServer) checkCompletedJobs(ctx context.Context) {
	if s.pgRepo == nil || s.frontier == nil {
		return
	}
	qSize, _ := s.frontier.QueueSize(ctx)
	leases, _ := s.frontier.ActiveLeaseCount(ctx)
	if qSize > 0 || leases > 0 {
		return
	}
	jobs, err := s.pgRepo.ListJobs(ctx, 20, 0)
	if err != nil {
		return
	}
	for _, job := range jobs {
		if job.Status == models.JobStateRunning && job.PagesCrawled > 0 {
			_ = s.pgRepo.UpdateJobStatus(ctx, job.ID, models.JobStateCompleted, "")
			_ = s.pgRepo.RecordEvent(ctx, models.CrawlEvent{
				JobID:     job.ID,
				EventType: "JOB_COMPLETED",
				Severity:  "INFO",
				Message:   fmt.Sprintf("Job %s completed (%d pages crawled)", job.ID, job.PagesCrawled),
			})
		}
	}
}

// HandleReport processes worker crawl reports, enqueues child links BEFORE releasing lease, and persists data.
func (s *CoordinatorServer) HandleReport(w http.ResponseWriter, r *http.Request) {
	s.setCORSHeaders(w, r)
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed", r.Header.Get("X-Request-ID"))
		return
	}

	var req models.ReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid report payload", r.Header.Get("X-Request-ID"))
		return
	}

	ctx := r.Context()

	// 1. Update metrics & persist page to Postgres FIRST (while in-flight lease STILL held in Redis!)
	s.recordAndPersistReport(ctx, req)

	// 2. Enqueue discovered child links WHILE lease is still active (prevents race where idle worker sees queue=0 & leases=0)
	newEnqueued := s.enqueueDiscoveredLinks(ctx, req)

	// 3. Release in-flight task lease AFTER children are safely queued
	if s.frontier != nil {
		if req.LeaseKey != "" {
			_ = s.frontier.AckTask(ctx, req.LeaseKey)
		}
		_ = s.frontier.AckTask(ctx, req.URL, req.JobID)
	}

	visitedCount := int64(0)
	if s.frontier != nil {
		visitedCount, _ = s.frontier.VisitedCount(ctx)
	}

	if s.pgRepo != nil {
		s.checkCompletedJobs(ctx)
	}

	s.writeJSON(w, http.StatusOK, models.ReportResponse{
		Status:            "accepted",
		NewLinksEnqueued:  newEnqueued,
		TotalVisitedCount: visitedCount,
	})
}

func (s *CoordinatorServer) recordAndPersistReport(ctx context.Context, req models.ReportRequest) {
	// Update metrics counters
	s.crawlRequestsTotal.Add(1)
	if req.Retries > 0 {
		s.crawlTaskRetriesTotal.Add(int64(req.Retries))
	}
	if req.RobotsBlocked {
		s.crawlRobotsBlockedTotal.Add(1)
	}
	if req.StatusCode >= 200 && req.StatusCode < 400 && req.ErrorMessage == "" {
		s.crawlRequestsSuccessTotal.Add(1)
	} else {
		s.crawlRequestsFailedTotal.Add(1)
	}
	if req.LatencyMs > 0 {
		s.crawlDurationSumMs.Add(int64(req.LatencyMs))
		s.crawlDurationCount.Add(1)
	}

	// Persist page, stats, errors, discovered URLs, and event logs
	if s.pgRepo != nil {
		if _, err := s.pgRepo.SavePageFull(ctx, req); err != nil {
			s.crawlDBErrorsTotal.Add(1)
			log.Printf("❌ Failed persisting page %s: %v\n", crawler.SanitizeForLog(req.URL), err)
		}

		evType := "PAGE_CRAWLED"
		severity := "INFO"
		msg := fmt.Sprintf("%s (%d) — %d links (%dms)", crawler.SanitizeForLog(req.Title), req.StatusCode, len(req.Links), req.LatencyMs)
		if req.RobotsBlocked {
			evType = "ROBOTS_BLOCKED"
			severity = "WARN"
			msg = fmt.Sprintf("Blocked by robots/security policy: %s", crawler.SanitizeForLog(req.ErrorMessage))
		} else if req.ErrorMessage != "" || req.StatusCode >= 400 || req.StatusCode == 0 {
			evType = "CRAWL_ERROR"
			severity = "ERROR"
			msg = fmt.Sprintf("Error %d on %s: %s", req.StatusCode, crawler.SanitizeForLog(req.URL), crawler.SanitizeForLog(req.ErrorMessage))
		}

		_ = s.pgRepo.RecordEvent(ctx, models.CrawlEvent{
			JobID:      req.JobID,
			WorkerID:   req.WorkerID,
			EventType:  evType,
			Severity:   severity,
			URL:        req.URL,
			StatusCode: req.StatusCode,
			LatencyMs:  req.LatencyMs,
			Message:    msg,
		})
	}
}

func (s *CoordinatorServer) enqueueDiscoveredLinks(ctx context.Context, req models.ReportRequest) int {
	if s.frontier == nil || len(req.Links) == 0 {
		return 0
	}

	// Unstructured crawl fallback when JobID is empty
	if req.JobID == "" {
		validLinks := make([]string, 0, len(req.Links))
		for _, link := range req.Links {
			if _, err := crawler.ValidateTargetURL(link, s.allowPrivateSubnets); err == nil {
				validLinks = append(validLinks, link)
			}
		}
		if len(validLinks) == 0 {
			return 0
		}
		n, err := s.frontier.Submit(ctx, validLinks...)
		if err != nil {
			log.Printf("❌ Failed to enqueue discovered links: %v\n", err)
		}
		return n
	}

	// Job-scoped enforcement: check job state, depth ceiling, page ceiling, domain & exclude filters
	if s.pgRepo == nil {
		return 0
	}
	job, err := s.pgRepo.GetJob(ctx, req.JobID)
	if err != nil || job == nil {
		return 0
	}
	if job.Status != models.JobStateRunning || job.PagesCrawled >= job.MaxPages {
		return 0
	}

	nextDepth := req.Depth + 1
	if nextDepth > job.MaxDepth {
		return 0
	}

	// Cap discovered links per page to avoid frontier explosion
	remainingQuota := job.MaxPages - job.PagesCrawled
	var tasks []models.CrawlTask
	for _, link := range req.Links {
		if _, err := crawler.ValidateTargetURL(link, s.allowPrivateSubnets); err != nil {
			continue
		}
		if ok, _ := crawler.MatchesURLPolicy(link, job.AllowedDomains, job.ExcludePatterns); !ok {
			continue
		}
		tasks = append(tasks, models.CrawlTask{
			URL:             link,
			JobID:           job.ID,
			Depth:           nextDepth,
			MaxDepth:        job.MaxDepth,
			AllowedDomains:  job.AllowedDomains,
			ExcludePatterns: job.ExcludePatterns,
			PolitenessMs:    job.PolitenessMs,
		})
		if len(tasks) >= remainingQuota*2 && len(tasks) >= 50 {
			break
		}
	}

	if len(tasks) == 0 {
		return 0
	}

	n, err := s.frontier.SubmitJobTasks(ctx, job.ID, tasks...)
	if err != nil {
		log.Printf("❌ Failed enqueueing job tasks: %v\n", err)
		return 0
	}
	return n
}

// HandleCrawlStep executes one synchronous bounded crawl step via coordinator when requested by operators.
func (s *CoordinatorServer) HandleCrawlStep(w http.ResponseWriter, r *http.Request) {
	s.setCORSHeaders(w, r)
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed", r.Header.Get("X-Request-ID"))
		return
	}

	if s.frontier == nil {
		s.writeError(w, http.StatusServiceUnavailable, "Redis frontier unavailable", r.Header.Get("X-Request-ID"))
		return
	}

	ctx := r.Context()
	task, err := s.frontier.GetPoliteStructuredTask(ctx, 0, "coordinator-step")
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error(), r.Header.Get("X-Request-ID"))
		return
	}

	if task == nil || task.URL == "" {
		s.writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":  "queue_empty",
			"message": "Frontier queue is empty. Submit new seeds to continue.",
		})
		return
	}

	crawlCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	report := models.ReportRequest{
		WorkerID: "coordinator-step",
		JobID:    task.JobID,
		URL:      task.URL,
		Depth:    task.Depth,
	}

	allowed, robotsErr := s.stepRobots.IsAllowed(crawlCtx, task.URL)
	if robotsErr != nil || !allowed {
		report.StatusCode = http.StatusForbidden
		report.Title = "Blocked by robots/security policy"
		report.RobotsBlocked = true
		report.ErrorType = "ROBOTS_DISALLOWED"
		if robotsErr != nil {
			report.ErrorMessage = robotsErr.Error()
		} else {
			report.ErrorMessage = "Disallowed by robots.txt"
		}
	} else {
		data, fetchErr := s.stepParser.FetchAndParse(crawlCtx, task.URL)
		if data != nil {
			report.FinalURL = data.FinalURL
			report.CanonicalURL = data.CanonicalURL
			report.StatusCode = data.StatusCode
			report.Title = data.Title
			report.Description = data.Description
			report.H1 = data.H1
			report.Headings = data.Headings
			report.ExtractedText = data.ExtractedText
			report.ContentType = data.ContentType
			report.ContentLength = data.ContentLength
			report.LatencyMs = data.LatencyMs
			report.Retries = data.Retries
			report.Links = data.Links
			report.InternalLinks = data.InternalLinks
			report.ExternalLinks = data.ExternalLinks
		}
		if fetchErr != nil {
			report.ErrorMessage = fetchErr.Error()
			if report.StatusCode == 0 {
				report.StatusCode = http.StatusBadGateway
			}
		}
	}

	s.recordAndPersistReport(ctx, report)
	newEnqueued := s.enqueueDiscoveredLinks(ctx, report)
	if s.frontier != nil {
		_ = s.frontier.AckTask(ctx, report.URL, report.JobID)
	}
	s.checkCompletedJobs(ctx)

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":             "crawled",
		"new_links_enqueued": newEnqueued,
		"page": map[string]interface{}{
			"url":         report.URL,
			"final_url":   report.FinalURL,
			"title":       report.Title,
			"status_code": report.StatusCode,
			"latency_ms":  report.LatencyMs,
			"depth":       report.Depth,
			"links_count": len(report.Links),
		},
	})
}

// HandleStatus returns comprehensive real-time cluster telemetry.
func (s *CoordinatorServer) HandleStatus(w http.ResponseWriter, r *http.Request) {
	s.setCORSHeaders(w, r)
	ctx := r.Context()

	var qSize, vCount, pagesIndexed, discoveredCount, dCount, leasesActive int64
	if s.frontier != nil {
		qSize, _ = s.frontier.QueueSize(ctx)
		vCount, _ = s.frontier.VisitedCount(ctx)
		leasesActive, _ = s.frontier.ActiveLeaseCount(ctx)
	}

	var recentResults []models.SearchResult
	var recentJobs []models.CrawlJob
	var domainBreakdown []models.DomainMetric
	var liveLogs []models.CrawlEvent
	var analytics *models.AnalyticsResponse

	if s.pgRepo != nil {
		dCount, _ = s.pgRepo.GetDomainCount(ctx)
		pagesIndexed, _ = s.pgRepo.GetIndexedPagesCount(ctx)
		discoveredCount, _ = s.pgRepo.GetDiscoveredURLsCount(ctx)
		recentResults, _ = s.pgRepo.GetRecentPages(ctx, 10)
		recentJobs, _ = s.pgRepo.ListJobs(ctx, 10, 0)
		domainBreakdown, _ = s.pgRepo.GetDomainMetrics(ctx, 8)
		liveLogs, _ = s.pgRepo.GetRecentEvents(ctx, 20)
		analytics, _ = s.pgRepo.GetAnalytics(ctx, "24h")
	}

	if vCount == 0 && pagesIndexed > 0 {
		vCount = pagesIndexed
	}

	workers := s.mergedWorkers(ctx)
	workersActive := 0
	for _, wk := range workers {
		if wk.Status == models.WorkerStateActive || wk.Status == models.WorkerStateIdle {
			workersActive++
		}
	}

	activeJobs, completedJobs, failedJobs := 0, 0, 0
	for _, jb := range recentJobs {
		switch jb.Status {
		case models.JobStateRunning:
			activeJobs++
		case models.JobStateCompleted:
			completedJobs++
		case models.JobStateFailed:
			failedJobs++
		}
	}

	uptimeSec := time.Since(s.startTime).Seconds()
	avgLatency := 0
	successRate := 0.0
	robotsBlocked := s.crawlRobotsBlockedTotal.Load()
	leasesReclaimed := s.crawlLeasesReclaimedTotal.Load()
	if analytics != nil {
		avgLatency = analytics.AvgLatencyMs
		successRate = analytics.SuccessRate
		if analytics.RobotsBlocked > robotsBlocked {
			robotsBlocked = analytics.RobotsBlocked
		}
		if analytics.LeaseReclaims > leasesReclaimed {
			leasesReclaimed = analytics.LeaseReclaims
		}
		if analytics.JobBreakdown[models.JobStateRunning] > activeJobs {
			activeJobs = analytics.JobBreakdown[models.JobStateRunning]
		}
		if analytics.JobBreakdown[models.JobStateCompleted] > completedJobs {
			completedJobs = analytics.JobBreakdown[models.JobStateCompleted]
		}
		if analytics.JobBreakdown[models.JobStateFailed] > failedJobs {
			failedJobs = analytics.JobBreakdown[models.JobStateFailed]
		}
	}

	throughputRPM := 0.0
	if uptimeSec > 1 && pagesIndexed > 0 {
		mins := uptimeSec / 60.0
		if mins < 0.25 {
			mins = 0.25
		}
		throughputRPM = float64(pagesIndexed) / mins
	}

	if recentResults == nil {
		recentResults = []models.SearchResult{}
	}
	if recentJobs == nil {
		recentJobs = []models.CrawlJob{}
	}
	if domainBreakdown == nil {
		domainBreakdown = []models.DomainMetric{}
	}
	if liveLogs == nil {
		liveLogs = []models.CrawlEvent{}
	}

	s.writeJSON(w, http.StatusOK, models.StatusResponse{
		Status:              "online",
		Mode:                "cluster",
		QueueSize:           qSize,
		VisitedCount:        vCount,
		PagesIndexed:        pagesIndexed,
		DiscoveredURLsCount: discoveredCount,
		DomainCount:         dCount,
		ActiveJobs:          activeJobs,
		CompletedJobs:       completedJobs,
		FailedJobs:          failedJobs,
		WorkersActive:       workersActive,
		LeasesActive:        leasesActive,
		LeasesReclaimed:     leasesReclaimed,
		ThroughputRPM:       throughputRPM,
		AvgLatencyMs:        avgLatency,
		SuccessRate:         successRate,
		RobotsBlocked:       robotsBlocked,
		UptimeSec:           uptimeSec,
		Workers:             workers,
		RecentPages:         recentResults,
		RecentJobs:          recentJobs,
		DomainBreakdown:     domainBreakdown,
		LiveLogs:            liveLogs,
	})
}

func (s *CoordinatorServer) mergedWorkers(ctx context.Context) []models.WorkerInfo {
	byID := map[string]models.WorkerInfo{}
	if s.pgRepo != nil {
		if dbWorkers, err := s.pgRepo.ListWorkers(ctx); err == nil {
			for _, w := range dbWorkers {
				byID[w.ID] = w
			}
		}
	}

	s.registry.mu.RLock()
	now := time.Now()
	for id, w := range s.registry.workers {
		cp := *w
		cp.LastSeenSecAgo = now.Sub(w.LastHeartbeat).Seconds()
		byID[id] = cp
	}
	s.registry.mu.RUnlock()

	list := make([]models.WorkerInfo, 0, len(byID))
	for _, w := range byID {
		list = append(list, w)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].LastHeartbeat.After(list[j].LastHeartbeat)
	})
	return list
}

// HandleMetrics serves Prometheus text exposition format (or JSON when ?format=json).
func (s *CoordinatorServer) HandleMetrics(w http.ResponseWriter, r *http.Request) {
	s.setCORSHeaders(w, r)
	ctx := r.Context()

	var qSize, pagesIndexed, dCount int64
	if s.frontier != nil {
		qSize, _ = s.frontier.QueueSize(ctx)
	}
	if s.pgRepo != nil {
		dCount, _ = s.pgRepo.GetDomainCount(ctx)
		pagesIndexed, _ = s.pgRepo.GetIndexedPagesCount(ctx)
	}

	workers := s.mergedWorkers(ctx)
	workersActive := 0
	var maxHeartbeatAge float64
	for _, wk := range workers {
		if wk.Status == models.WorkerStateActive || wk.Status == models.WorkerStateIdle {
			workersActive++
			if wk.LastSeenSecAgo > maxHeartbeatAge {
				maxHeartbeatAge = wk.LastSeenSecAgo
			}
		}
	}

	activeJobs, completedJobs := 0, 0
	if s.pgRepo != nil {
		if jobs, err := s.pgRepo.ListJobs(ctx, 100, 0); err == nil {
			for _, j := range jobs {
				if j.Status == models.JobStateRunning {
					activeJobs++
				} else if j.Status == models.JobStateCompleted {
					completedJobs++
				}
			}
		}
	}

	reqCount := s.crawlDurationCount.Load()
	avgDurationMs := 0.0
	if reqCount > 0 {
		avgDurationMs = float64(s.crawlDurationSumMs.Load()) / float64(reqCount)
	}

	if r.URL.Query().Get("format") == "json" {
		s.writeJSON(w, http.StatusOK, map[string]interface{}{
			"crawl_requests_total":           s.crawlRequestsTotal.Load(),
			"crawl_requests_success_total":   s.crawlRequestsSuccessTotal.Load(),
			"crawl_requests_failed_total":    s.crawlRequestsFailedTotal.Load(),
			"crawl_request_duration_ms":      avgDurationMs,
			"crawl_pages_indexed_total":      pagesIndexed,
			"crawl_domains_total":            dCount,
			"crawl_queue_depth":              qSize,
			"crawl_jobs_active":              activeJobs,
			"crawl_jobs_completed":           completedJobs,
			"crawl_workers_active":           workersActive,
			"crawl_worker_heartbeat_age_sec": maxHeartbeatAge,
			"crawl_task_retries_total":       s.crawlTaskRetriesTotal.Load(),
			"crawl_database_errors_total":    s.crawlDBErrorsTotal.Load(),
			"crawl_leases_reclaimed_total":   s.crawlLeasesReclaimedTotal.Load(),
			"crawl_robots_blocked_total":     s.crawlRobotsBlockedTotal.Load(),
		})
		return
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	var sb strings.Builder
	sb.WriteString("# HELP crawl_requests_total Total crawl requests processed\n# TYPE crawl_requests_total counter\n")
	sb.WriteString(fmt.Sprintf("crawl_requests_total %d\n", s.crawlRequestsTotal.Load()))

	sb.WriteString("# HELP crawl_requests_success_total Successful HTTP 2xx/3xx crawl requests\n# TYPE crawl_requests_success_total counter\n")
	sb.WriteString(fmt.Sprintf("crawl_requests_success_total %d\n", s.crawlRequestsSuccessTotal.Load()))

	sb.WriteString("# HELP crawl_requests_failed_total Failed or error crawl requests\n# TYPE crawl_requests_failed_total counter\n")
	sb.WriteString(fmt.Sprintf("crawl_requests_failed_total %d\n", s.crawlRequestsFailedTotal.Load()))

	sb.WriteString("# HELP crawl_request_duration Average HTTP crawl duration in milliseconds\n# TYPE crawl_request_duration gauge\n")
	sb.WriteString(fmt.Sprintf("crawl_request_duration %.2f\n", avgDurationMs))

	sb.WriteString("# HELP crawl_pages_indexed_total Total persisted pages in PostgreSQL\n# TYPE crawl_pages_indexed_total gauge\n")
	sb.WriteString(fmt.Sprintf("crawl_pages_indexed_total %d\n", pagesIndexed))

	sb.WriteString("# HELP crawl_queue_depth Pending URLs waiting in Redis frontier\n# TYPE crawl_queue_depth gauge\n")
	sb.WriteString(fmt.Sprintf("crawl_queue_depth %d\n", qSize))

	sb.WriteString("# HELP crawl_jobs_active Active running crawl jobs\n# TYPE crawl_jobs_active gauge\n")
	sb.WriteString(fmt.Sprintf("crawl_jobs_active %d\n", activeJobs))

	sb.WriteString("# HELP crawl_jobs_completed Completed crawl jobs\n# TYPE crawl_jobs_completed gauge\n")
	sb.WriteString(fmt.Sprintf("crawl_jobs_completed %d\n", completedJobs))

	sb.WriteString("# HELP crawl_workers_active Active registered workers\n# TYPE crawl_workers_active gauge\n")
	sb.WriteString(fmt.Sprintf("crawl_workers_active %d\n", workersActive))

	sb.WriteString("# HELP crawl_worker_heartbeat_age Maximum active worker heartbeat age in seconds\n# TYPE crawl_worker_heartbeat_age gauge\n")
	sb.WriteString(fmt.Sprintf("crawl_worker_heartbeat_age %.2f\n", maxHeartbeatAge))

	sb.WriteString("# HELP crawl_task_retries_total Total fetch retries executed across workers\n# TYPE crawl_task_retries_total counter\n")
	sb.WriteString(fmt.Sprintf("crawl_task_retries_total %d\n", s.crawlTaskRetriesTotal.Load()))

	sb.WriteString("# HELP crawl_database_errors_total Total PostgreSQL write/query errors\n# TYPE crawl_database_errors_total counter\n")
	sb.WriteString(fmt.Sprintf("crawl_database_errors_total %d\n", s.crawlDBErrorsTotal.Load()))

	_, _ = w.Write([]byte(sb.String()))
}

// HandleAnalytics returns backend-aggregated analytics for the Analytics dashboard.
func (s *CoordinatorServer) HandleAnalytics(w http.ResponseWriter, r *http.Request) {
	s.setCORSHeaders(w, r)
	rangeParam := r.URL.Query().Get("range")
	if rangeParam == "" {
		rangeParam = "24h"
	}

	if s.pgRepo == nil {
		s.writeJSON(w, http.StatusOK, models.AnalyticsResponse{
			Range:              rangeParam,
			WorkerUtilization:  s.mergedWorkers(r.Context()),
			StatusBreakdown:    []models.BucketCount{},
			ErrorCategories:    []models.BucketCount{},
			DomainDistribution: []models.DomainMetric{},
			ThroughputSeries:   []models.ThroughputPoint{},
			JobBreakdown:       map[string]int{},
		})
		return
	}

	res, err := s.pgRepo.GetAnalytics(r.Context(), rangeParam)
	if err != nil {
		s.crawlDBErrorsTotal.Add(1)
		s.writeError(w, http.StatusInternalServerError, "Failed to compute analytics aggregations", r.Header.Get("X-Request-ID"))
		return
	}
	res.RetryCount = s.crawlTaskRetriesTotal.Load()
	if s.crawlLeasesReclaimedTotal.Load() > res.LeaseReclaims {
		res.LeaseReclaims = s.crawlLeasesReclaimedTotal.Load()
	}
	res.WorkerUtilization = s.mergedWorkers(r.Context())

	s.writeJSON(w, http.StatusOK, res)
}

// HandleWorkers lists registered worker nodes in unified `{ workers: [...] }` shape.
func (s *CoordinatorServer) HandleWorkers(w http.ResponseWriter, r *http.Request) {
	s.setCORSHeaders(w, r)
	workers := s.mergedWorkers(r.Context())
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"workers": workers,
		"total":   len(workers),
	})
}

// HandleWorkerDetails inspects a single worker by ID.
func (s *CoordinatorServer) HandleWorkerDetails(w http.ResponseWriter, r *http.Request) {
	s.setCORSHeaders(w, r)
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		s.writeError(w, http.StatusBadRequest, "Missing worker ID", r.Header.Get("X-Request-ID"))
		return
	}
	targetID := parts[2]
	for _, wk := range s.mergedWorkers(r.Context()) {
		if wk.ID == targetID {
			s.writeJSON(w, http.StatusOK, wk)
			return
		}
	}
	s.writeError(w, http.StatusNotFound, "Worker not found", r.Header.Get("X-Request-ID"))
}

// HandleActivity returns filtered activity events in unified `{ events: [...] }` shape.
func (s *CoordinatorServer) HandleActivity(w http.ResponseWriter, r *http.Request) {
	s.setCORSHeaders(w, r)
	limit := 50
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if val, err := strconv.Atoi(lStr); err == nil && val > 0 && val <= 200 {
			limit = val
		}
	}
	severity := r.URL.Query().Get("severity")
	search := r.URL.Query().Get("q")

	var events []models.CrawlEvent
	if s.pgRepo != nil {
		events, _ = s.pgRepo.QueryEvents(r.Context(), severity, search, limit)
	}
	if events == nil {
		events = []models.CrawlEvent{}
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"events": events,
		"total":  len(events),
	})
}

// HandleErrors returns recent crawl error entries.
func (s *CoordinatorServer) HandleErrors(w http.ResponseWriter, r *http.Request) {
	s.setCORSHeaders(w, r)
	if s.pgRepo == nil {
		s.writeJSON(w, http.StatusOK, map[string]interface{}{"errors": []models.CrawlError{}})
		return
	}
	errs, err := s.pgRepo.ListCrawlErrors(r.Context(), 50)
	if err != nil {
		errs = []models.CrawlError{}
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"errors": errs,
		"total":  len(errs),
	})
}

// HandleSubmitSeeds validates URLs against SSRF, persists a CrawlJob, and enqueues structured seed tasks.
func (s *CoordinatorServer) HandleSubmitSeeds(w http.ResponseWriter, r *http.Request) {
	s.setCORSHeaders(w, r)
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed", r.Header.Get("X-Request-ID"))
		return
	}

	var req models.SubmitSeedsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON request body", r.Header.Get("X-Request-ID"))
		return
	}

	if req.URL != "" && len(req.URLs) == 0 {
		req.URLs = []string{req.URL}
	}
	if len(req.URLs) == 0 {
		s.writeError(w, http.StatusBadRequest, "At least one seed URL is required", r.Header.Get("X-Request-ID"))
		return
	}

	// Enforce SSRF & scheme validation on EVERY submitted seed URL before accepting!
	validatedURLs := make([]string, 0, len(req.URLs))
	for _, raw := range req.URLs {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		parsed, err := crawler.ValidateTargetURL(trimmed, s.allowPrivateSubnets)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, fmt.Sprintf("URL %q rejected by SSRF firewall: %v", crawler.SanitizeForLog(trimmed), err), r.Header.Get("X-Request-ID"))
			return
		}
		validatedURLs = append(validatedURLs, parsed.String())
	}

	if len(validatedURLs) == 0 {
		s.writeError(w, http.StatusBadRequest, "No valid seed URLs provided", r.Header.Get("X-Request-ID"))
		return
	}

	maxDepth := req.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 3
	}
	if maxDepth > 10 {
		maxDepth = 10
	}
	maxPages := req.MaxPages
	if maxPages <= 0 {
		maxPages = 100
	}
	if maxPages > 10000 {
		maxPages = 10000
	}
	concurrency := req.Concurrency
	if concurrency <= 0 {
		concurrency = 4
	}
	politenessMs := req.PolitenessMs
	if politenessMs <= 0 {
		politenessMs = int(s.politenessDelay.Milliseconds())
		if politenessMs <= 0 {
			politenessMs = 1000
		}
	}

	jobID := req.JobID
	if jobID == "" {
		jobID = fmt.Sprintf("job-%d", time.Now().UnixNano()/1000000)
	}
	jobName := strings.TrimSpace(req.Name)
	if jobName == "" {
		jobName = fmt.Sprintf("Crawl %s", validatedURLs[0])
	}

	ctx := r.Context()
	job := models.CrawlJob{
		ID:              jobID,
		Name:            jobName,
		Status:          models.JobStateRunning,
		SeedURLs:        validatedURLs,
		MaxDepth:        maxDepth,
		MaxPages:        maxPages,
		Concurrency:     concurrency,
		PolitenessMs:    politenessMs,
		AllowedDomains:  req.AllowedDomains,
		ExcludePatterns: req.ExcludePatterns,
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}

	if s.pgRepo != nil {
		if err := s.pgRepo.CreateJob(ctx, job); err != nil {
			s.crawlDBErrorsTotal.Add(1)
			s.writeError(w, http.StatusInternalServerError, "Failed to persist crawl job", r.Header.Get("X-Request-ID"))
			return
		}
		_ = s.pgRepo.RecordEvent(ctx, models.CrawlEvent{
			JobID:     jobID,
			EventType: "JOB_CREATED",
			Severity:  "INFO",
			Message:   fmt.Sprintf("Created crawl job %q (%d seeds, max_depth=%d, max_pages=%d)", jobName, len(validatedURLs), maxDepth, maxPages),
		})
	}

	enqueued := 0
	if s.frontier != nil {
		tasks := make([]models.CrawlTask, 0, len(validatedURLs))
		for _, u := range validatedURLs {
			tasks = append(tasks, models.CrawlTask{
				URL:             u,
				JobID:           jobID,
				Depth:           0,
				MaxDepth:        maxDepth,
				AllowedDomains:  req.AllowedDomains,
				ExcludePatterns: req.ExcludePatterns,
				PolitenessMs:    politenessMs,
			})
		}
		var err error
		enqueued, err = s.frontier.SubmitJobTasks(ctx, jobID, tasks...)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "Failed enqueueing seed tasks to Redis", r.Header.Get("X-Request-ID"))
			return
		}
	}

	s.writeJSON(w, http.StatusOK, models.SubmitSeedsResponse{
		Status:   "success",
		JobID:    jobID,
		Enqueued: enqueued,
		Job:      &job,
	})
}

// HandleJobs lists crawl jobs in unified `{ jobs: [...] }` shape.
func (s *CoordinatorServer) HandleJobs(w http.ResponseWriter, r *http.Request) {
	s.setCORSHeaders(w, r)
	if s.pgRepo == nil {
		s.writeJSON(w, http.StatusOK, map[string]interface{}{"jobs": []models.CrawlJob{}, "total": 0})
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 {
		limit = 50
	}

	jobs, err := s.pgRepo.ListJobs(r.Context(), limit, offset)
	if err != nil {
		s.crawlDBErrorsTotal.Add(1)
		jobs = []models.CrawlJob{}
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"jobs":  jobs,
		"total": len(jobs),
	})
}

// HandleJobOperation handles GET /api/jobs/{id} and POST /api/jobs/{id}/{pause|resume|cancel|retry} and GET /api/jobs/{id}/export.
func (s *CoordinatorServer) HandleJobOperation(w http.ResponseWriter, r *http.Request) {
	s.setCORSHeaders(w, r)

	// Normalize segments regardless of /api prefix
	trimmed := strings.Trim(r.URL.Path, "/")
	parts := strings.Split(trimmed, "/")
	// Expected: api/jobs/{id} or api/jobs/{id}/{op}
	if len(parts) < 3 {
		s.writeError(w, http.StatusBadRequest, "Invalid job endpoint path", r.Header.Get("X-Request-ID"))
		return
	}

	jobID := parts[2]
	if len(parts) == 3 && r.Method == http.MethodGet {
		if s.pgRepo == nil {
			s.writeError(w, http.StatusServiceUnavailable, "Database unavailable", r.Header.Get("X-Request-ID"))
			return
		}
		detail, err := s.pgRepo.GetJobDetails(r.Context(), jobID)
		if err != nil {
			s.writeError(w, http.StatusNotFound, "Crawl job not found", r.Header.Get("X-Request-ID"))
			return
		}
		s.writeJSON(w, http.StatusOK, detail)
		return
	}

	if len(parts) < 4 {
		s.writeError(w, http.StatusBadRequest, "Missing job operation", r.Header.Get("X-Request-ID"))
		return
	}

	op := strings.ToLower(parts[3])
	ctx := r.Context()

	if op == "export" && r.Method == http.MethodGet {
		s.handleExportPages(w, r, "", jobID)
		return
	}

	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required for job state transition", r.Header.Get("X-Request-ID"))
		return
	}

	if op == "cancel" && !s.verifyAdminAuth(r) {
		s.writeError(w, http.StatusUnauthorized, "Valid admin authorization required to cancel jobs", r.Header.Get("X-Request-ID"))
		return
	}

	switch op {
	case "pause":
		if s.frontier != nil {
			_ = s.frontier.PauseJob(ctx, jobID)
		}
		if s.pgRepo != nil {
			_ = s.pgRepo.UpdateJobStatus(ctx, jobID, models.JobStatePaused, "")
			_ = s.pgRepo.RecordEvent(ctx, models.CrawlEvent{
				JobID: jobID, EventType: "JOB_PAUSED", Severity: "WARN",
				Message: fmt.Sprintf("Job %s paused; remaining tasks parked", jobID),
			})
		}
		s.writeJSON(w, http.StatusOK, map[string]interface{}{"job_id": jobID, "status": models.JobStatePaused})

	case "resume":
		restored := 0
		if s.frontier != nil {
			restored, _ = s.frontier.ResumeJob(ctx, jobID)
		}
		if s.pgRepo != nil {
			_ = s.pgRepo.UpdateJobStatus(ctx, jobID, models.JobStateRunning, "")
			_ = s.pgRepo.RecordEvent(ctx, models.CrawlEvent{
				JobID: jobID, EventType: "JOB_RESUMED", Severity: "INFO",
				Message: fmt.Sprintf("Job %s resumed (%d parked tasks re-enqueued)", jobID, restored),
			})
		}
		s.writeJSON(w, http.StatusOK, map[string]interface{}{"job_id": jobID, "status": models.JobStateRunning, "restored_tasks": restored})

	case "cancel":
		if s.frontier != nil {
			_ = s.frontier.CancelJob(ctx, jobID)
		}
		if s.pgRepo != nil {
			_ = s.pgRepo.UpdateJobStatus(ctx, jobID, models.JobStateCancelled, "Cancelled by operator")
			_ = s.pgRepo.RecordEvent(ctx, models.CrawlEvent{
				JobID: jobID, EventType: "JOB_CANCELLED", Severity: "WARN",
				Message: fmt.Sprintf("Job %s cancelled; queued tasks discarded", jobID),
			})
		}
		s.writeJSON(w, http.StatusOK, map[string]interface{}{"job_id": jobID, "status": models.JobStateCancelled})

	case "retry":
		retried := 0
		if s.pgRepo != nil && s.frontier != nil {
			job, err := s.pgRepo.GetJob(ctx, jobID)
			if err != nil {
				s.writeError(w, http.StatusNotFound, "Job not found", r.Header.Get("X-Request-ID"))
				return
			}
			failedURLs, _ := s.pgRepo.GetFailedURLsForJob(ctx, jobID)
			var retryTasks []models.CrawlTask
			for _, u := range failedURLs {
				if _, err := crawler.ValidateTargetURL(u, s.allowPrivateSubnets); err == nil {
					retryTasks = append(retryTasks, models.CrawlTask{
						URL: u, JobID: jobID, Depth: 0, MaxDepth: job.MaxDepth,
						AllowedDomains: job.AllowedDomains, ExcludePatterns: job.ExcludePatterns,
						PolitenessMs: job.PolitenessMs, Retries: 1,
					})
				}
			}
			for _, t := range retryTasks {
				if s.frontier.RequeueTask(ctx, t) == nil {
					retried++
				}
			}
			if retried > 0 {
				_ = s.pgRepo.UpdateJobStatus(ctx, jobID, models.JobStateRunning, "")
				_ = s.pgRepo.RecordEvent(ctx, models.CrawlEvent{
					JobID: jobID, EventType: "JOB_RETRY_FAILED", Severity: "INFO",
					Message: fmt.Sprintf("Re-enqueued %d failed URLs for job %s", retried, jobID),
				})
			}
		}
		s.writeJSON(w, http.StatusOK, map[string]interface{}{"job_id": jobID, "retried": retried})

	default:
		s.writeError(w, http.StatusBadRequest, "Unsupported job operation", r.Header.Get("X-Request-ID"))
	}
}

// HandleSearch executes ranked PostgreSQL full-text search with pagination, domain/status/date filters, and sort.
func (s *CoordinatorServer) HandleSearch(w http.ResponseWriter, r *http.Request) {
	s.setCORSHeaders(w, r)

	q := r.URL.Query().Get("q")
	domain := r.URL.Query().Get("domain")
	statusFilter := r.URL.Query().Get("status")
	dateFrom := r.URL.Query().Get("from")
	dateTo := r.URL.Query().Get("to")
	sortBy := r.URL.Query().Get("sort")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	if s.pgRepo == nil {
		s.writeJSON(w, http.StatusOK, models.SearchResponse{
			Results: []models.SearchResult{},
			Total:   0,
			Limit:   limit,
			Offset:  offset,
		})
		return
	}

	results, total, err := s.pgRepo.SearchPagesWithTotal(r.Context(), q, domain, statusFilter, dateFrom, dateTo, sortBy, limit, offset)
	if err != nil {
		s.crawlDBErrorsTotal.Add(1)
		s.writeError(w, http.StatusInternalServerError, "Failed querying search index", r.Header.Get("X-Request-ID"))
		return
	}

	s.writeJSON(w, http.StatusOK, models.SearchResponse{
		Results: results,
		Total:   total,
		Limit:   limit,
		Offset:  offset,
	})
}

// HandleExport exports crawled pages matching filters (or single job) as CSV or JSON.
func (s *CoordinatorServer) HandleExport(w http.ResponseWriter, r *http.Request) {
	s.setCORSHeaders(w, r)
	s.handleExportPages(w, r, r.URL.Query().Get("q"), r.URL.Query().Get("job_id"))
}

func (s *CoordinatorServer) handleExportPages(w http.ResponseWriter, r *http.Request, query, jobID string) {
	format := strings.ToLower(r.URL.Query().Get("format"))
	if format == "" {
		format = "json"
	}

	if s.pgRepo == nil {
		s.writeError(w, http.StatusServiceUnavailable, "Database unavailable", r.Header.Get("X-Request-ID"))
		return
	}

	domain := r.URL.Query().Get("domain")
	statusFilter := r.URL.Query().Get("status")
	results, _, err := s.pgRepo.SearchPagesWithTotal(r.Context(), query, domain, statusFilter, "", "", "newest", 200, 0)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Export failed", r.Header.Get("X-Request-ID"))
		return
	}

	if jobID != "" {
		filtered := make([]models.SearchResult, 0)
		for _, r := range results {
			if r.JobID == jobID {
				filtered = append(filtered, r)
			}
		}
		results = filtered
	}

	if format == "csv" {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", `attachment; filename="crawler-export.csv"`)
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"id", "job_id", "url", "final_url", "domain", "status_code", "latency_ms", "depth", "title", "description", "crawled_at"})
		for _, p := range results {
			_ = cw.Write([]string{
				strconv.FormatInt(p.ID, 10), p.JobID, p.URL, p.FinalURL, p.Domain,
				strconv.Itoa(p.StatusCode), strconv.Itoa(p.LatencyMs), strconv.Itoa(p.Depth),
				p.Title, p.Description, p.CrawledAt.Format(time.RFC3339),
			})
		}
		cw.Flush()
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="crawler-export.json"`)
	_ = json.NewEncoder(w).Encode(results)
}

// HandlePageDetails inspects a single crawled page by ID.
func (s *CoordinatorServer) HandlePageDetails(w http.ResponseWriter, r *http.Request) {
	s.setCORSHeaders(w, r)

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		s.writeError(w, http.StatusBadRequest, "Missing page ID", r.Header.Get("X-Request-ID"))
		return
	}

	id, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid numeric page ID", r.Header.Get("X-Request-ID"))
		return
	}

	if s.pgRepo == nil {
		s.writeError(w, http.StatusServiceUnavailable, "Database unavailable", r.Header.Get("X-Request-ID"))
		return
	}

	page, err := s.pgRepo.GetPageByID(r.Context(), id)
	if err != nil {
		s.writeError(w, http.StatusNotFound, "Page not found", r.Header.Get("X-Request-ID"))
		return
	}

	s.writeJSON(w, http.StatusOK, page)
}

// HandleDomains returns real aggregated domain metrics in unified `{ domains: [...] }` shape.
func (s *CoordinatorServer) HandleDomains(w http.ResponseWriter, r *http.Request) {
	s.setCORSHeaders(w, r)

	limit := 50
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if val, err := strconv.Atoi(lStr); err == nil && val > 0 && val <= 200 {
			limit = val
		}
	}

	if s.pgRepo == nil {
		s.writeJSON(w, http.StatusOK, map[string]interface{}{"domains": []models.DomainMetric{}, "total": 0})
		return
	}

	metrics, err := s.pgRepo.GetDomainMetrics(r.Context(), limit)
	if err != nil {
		s.crawlDBErrorsTotal.Add(1)
		metrics = []models.DomainMetric{}
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"domains": metrics,
		"total":   len(metrics),
	})
}

// HandleReset executes granular tiered resets with strict authorization and audit logging.
func (s *CoordinatorServer) HandleReset(w http.ResponseWriter, r *http.Request) {
	s.setCORSHeaders(w, r)
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed", r.Header.Get("X-Request-ID"))
		return
	}

	var req models.ResetRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	level := strings.ToLower(strings.TrimSpace(req.Level))
	if level == "" {
		level = "ui"
	}

	ctx := r.Context()

	switch level {
	case "ui":
		s.writeJSON(w, http.StatusOK, models.ResetResponse{
			Status:  "success",
			Level:   "ui",
			Message: "Local UI view filters cleared cleanly",
		})
		return

	case "job":
		if req.JobID == "" {
			s.writeError(w, http.StatusBadRequest, "job_id is required for single job reset", r.Header.Get("X-Request-ID"))
			return
		}
		if s.frontier != nil {
			_ = s.frontier.CancelJob(ctx, req.JobID)
		}
		if s.pgRepo != nil {
			_ = s.pgRepo.UpdateJobStatus(ctx, req.JobID, models.JobStateCancelled, "Cancelled via reset console")
			_ = s.pgRepo.RecordEvent(ctx, models.CrawlEvent{
				JobID: req.JobID, EventType: "JOB_CANCELLED", Severity: "WARN",
				Message: fmt.Sprintf("Job %s cancelled via reset console", req.JobID),
			})
		}
		s.writeJSON(w, http.StatusOK, models.ResetResponse{
			Status:  "success",
			Level:   "job",
			Message: fmt.Sprintf("Crawl job %s cancelled and removed from dispatch", req.JobID),
		})
		return

	case "queue":
		if !s.verifyAdminAuth(r) {
			s.writeError(w, http.StatusUnauthorized, "Admin token required to flush queue", r.Header.Get("X-Request-ID"))
			return
		}
		if s.frontier != nil {
			_ = s.frontier.ClearQueueOnly(ctx)
		}
		if s.pgRepo != nil {
			_ = s.pgRepo.RecordEvent(ctx, models.CrawlEvent{
				EventType: "QUEUE_FLUSHED", Severity: "WARN",
				Message: "Pending Redis frontier queue and active leases flushed; database index preserved",
			})
		}
		s.writeJSON(w, http.StatusOK, models.ResetResponse{
			Status:  "success",
			Level:   "queue",
			Message: "Pending frontier queue and leases cleared; indexed pages preserved",
		})
		return

	case "job_data":
		if !s.verifyAdminAuth(r) {
			s.writeError(w, http.StatusUnauthorized, "Admin token required", r.Header.Get("X-Request-ID"))
			return
		}
		if req.JobID == "" {
			s.writeError(w, http.StatusBadRequest, "job_id is required to purge job data", r.Header.Get("X-Request-ID"))
			return
		}
		if s.frontier != nil {
			_ = s.frontier.CancelJob(ctx, req.JobID)
		}
		if s.pgRepo != nil {
			if err := s.pgRepo.DeleteJobData(ctx, req.JobID); err != nil {
				s.writeError(w, http.StatusNotFound, "Job not found or already deleted", r.Header.Get("X-Request-ID"))
				return
			}
			_ = s.pgRepo.RecordEvent(ctx, models.CrawlEvent{
				EventType: "JOB_DATA_PURGED", Severity: "WARN",
				Message: fmt.Sprintf("All crawled data deleted for job %s", req.JobID),
			})
		}
		s.writeJSON(w, http.StatusOK, models.ResetResponse{
			Status:  "success",
			Level:   "job_data",
			Message: fmt.Sprintf("Job %s and all associated records removed", req.JobID),
		})
		return

	case "data":
		if !s.verifyAdminAuth(r) {
			s.writeError(w, http.StatusUnauthorized, "Admin token required to purge indexed data", r.Header.Get("X-Request-ID"))
			return
		}
		if s.pgRepo != nil {
			_ = s.pgRepo.TruncateData(ctx)
			_ = s.pgRepo.RecordEvent(ctx, models.CrawlEvent{
				EventType: "DATA_PURGED", Severity: "WARN",
				Message: "Crawled pages index and domain statistics cleared",
			})
		}
		s.writeJSON(w, http.StatusOK, models.ResetResponse{
			Status:  "success",
			Level:   "data",
			Message: "Indexed pages and domain metrics cleared; job metadata preserved",
		})
		return

	case "dev":
		if !s.verifyAdminAuth(r) {
			s.writeError(w, http.StatusUnauthorized, "Admin token required for dev reset", r.Header.Get("X-Request-ID"))
			return
		}
		if s.frontier != nil {
			_ = s.frontier.Clear(ctx)
		}
		if s.pgRepo != nil {
			_ = s.pgRepo.Truncate(ctx)
			_ = s.pgRepo.RecordEvent(ctx, models.CrawlEvent{
				EventType: "DEV_RESET", Severity: "WARN",
				Message: "Development cluster data, frontier queue, and jobs reset cleanly",
			})
		}
		s.writeJSON(w, http.StatusOK, models.ResetResponse{
			Status:  "success",
			Level:   "dev",
			Message: "Development environment reset cleanly (frontier and database cleared)",
		})
		return

	case "production":
		if !s.enableProdReset {
			s.writeError(w, http.StatusForbidden, "Production reset is disabled on this cluster (set ENABLE_PRODUCTION_RESET=true to enable)", r.Header.Get("X-Request-ID"))
			return
		}
		if !s.verifyAdminAuth(r) {
			s.writeError(w, http.StatusUnauthorized, "Valid admin authorization required for production reset", r.Header.Get("X-Request-ID"))
			return
		}
		if req.ConfirmationPhrase != "WIPE ALL DATA" {
			s.writeError(w, http.StatusForbidden, "Forbidden: Production reset requires confirmation_phrase 'WIPE ALL DATA'", r.Header.Get("X-Request-ID"))
			return
		}
		if s.frontier != nil {
			_ = s.frontier.Clear(ctx)
		}
		if s.pgRepo != nil {
			_ = s.pgRepo.Truncate(ctx)
			_ = s.pgRepo.RecordEvent(ctx, models.CrawlEvent{
				EventType: "PRODUCTION_RESET", Severity: "WARN",
				Message: "Production database and Redis frontier wiped by authorized operator",
			})
		}
		s.writeJSON(w, http.StatusOK, models.ResetResponse{
			Status:  "success",
			Level:   "production",
			Message: "Full administrative cluster reset completed",
		})
		return

	default:
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("Unsupported reset level: %s", level), r.Header.Get("X-Request-ID"))
	}
}

// HandleConfig reads or updates runtime cluster configuration.
func (s *CoordinatorServer) HandleConfig(w http.ResponseWriter, r *http.Request) {
	s.setCORSHeaders(w, r)

	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		if !s.verifyAdminAuth(r) {
			s.writeError(w, http.StatusUnauthorized, "Admin token required to modify settings", r.Header.Get("X-Request-ID"))
			return
		}
		var updates map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			s.writeError(w, http.StatusBadRequest, "Invalid settings payload", r.Header.Get("X-Request-ID"))
			return
		}
		if s.pgRepo != nil {
			for k, v := range updates {
				_ = s.pgRepo.UpdateSetting(r.Context(), k, fmt.Sprintf("%v", v))
			}
			_ = s.pgRepo.RecordEvent(r.Context(), models.CrawlEvent{
				EventType: "CONFIG_UPDATED", Severity: "INFO",
				Message: "Cluster default crawl settings updated",
			})
		}
	}

	persisted := map[string]string{}
	if s.pgRepo != nil {
		if settings, err := s.pgRepo.GetSettings(r.Context()); err == nil {
			persisted = settings
		}
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"service":                  "gopher-distributed-crawler",
		"version":                  "2.0.0",
		"mode":                     "cluster",
		"politeness_delay_ms":      s.politenessDelay.Milliseconds(),
		"lease_timeout_sec":        int(s.leaseTimeout.Seconds()),
		"production_reset_enabled": s.enableProdReset,
		"admin_auth_required":      s.adminToken != "",
		"max_depth_default":        3,
		"max_pages_default":        100,
		"persisted_settings":       persisted,
	})
}
