package master

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"distributed-crawler/pkg/crawler"
	"distributed-crawler/pkg/db"
	"distributed-crawler/pkg/models"
)

// WorkerRegistry tracks live worker node heartbeats and health
type WorkerRegistry struct {
	mu      sync.RWMutex
	workers map[string]*models.WorkerInfo
}

// CoordinatorServer holds HTTP route handlers, frontier, and database repository dependencies
type CoordinatorServer struct {
	frontier        *crawler.Frontier
	pgRepo          *db.PostgresRepository
	registry        *WorkerRegistry
	politenessDelay time.Duration
	startTime       time.Time
}

// setCORSHeaders applies uniform CORS policy for web consoles and distributed nodes
func setCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, HEAD")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
}

// HandleCORS responds to HTTP OPTIONS preflight checks
func (s *CoordinatorServer) HandleCORS(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	w.WriteHeader(http.StatusOK)
}

// NewCoordinatorServer initializes the coordinator server instance and launches lease reclamation
func NewCoordinatorServer(frontier *crawler.Frontier, pgRepo *db.PostgresRepository, politenessDelay time.Duration) *CoordinatorServer {
	srv := &CoordinatorServer{
		frontier: frontier,
		pgRepo:   pgRepo,
		registry: &WorkerRegistry{
			workers: make(map[string]*models.WorkerInfo),
		},
		politenessDelay: politenessDelay,
		startTime:       time.Now(),
	}

	// Start background task recovery and health monitor if frontier is provided
	if frontier != nil {
		go srv.startBackgroundReaper()
	}

	return srv
}

func (s *CoordinatorServer) startBackgroundReaper() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		// 1. Reclaim abandoned task leases older than 60 seconds
		if s.frontier != nil {
			reclaimed, err := s.frontier.ReclaimExpiredLeases(ctx, 60*time.Second)
			if err == nil && reclaimed > 0 {
				log.Printf("♻️ Recovered %d expired crawl task leases back to queue.\n", reclaimed)
			}
		}

		// 2. Mark stale workers as UNHEALTHY
		s.registry.mu.Lock()
		now := time.Now()
		for _, w := range s.registry.workers {
			if now.Sub(w.LastHeartbeat) > 15*time.Second && w.Status != models.WorkerStateStopped {
				w.Status = models.WorkerStateUnhealthy
			}
		}
		s.registry.mu.Unlock()
		cancel()
	}
}

// HandleHealth handles GET /health and GET /api/health
func (s *CoordinatorServer) HandleHealth(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "healthy",
		"service":    "gopher-crawler-coordinator",
		"uptime_sec": time.Since(s.startTime).Seconds(),
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
	})
}

// HandleReady handles GET /ready (verifies storage and queue health)
func (s *CoordinatorServer) HandleReady(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

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
		if _, err := s.pgRepo.GetDomainCount(ctx); err != nil {
			dbReady = false
		}
	}

	if !redisReady || !dbReady {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":      "unready",
			"redis_ready": redisReady,
			"db_ready":    dbReady,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      "ready",
		"redis_ready": true,
		"db_ready":    true,
	})
}

// HandleRegister handles worker registration
func (s *CoordinatorServer) HandleRegister(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req models.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	workerID := fmt.Sprintf("worker-%s-%d", req.Hostname, time.Now().UnixNano()%100000)
	version := req.Version
	if version == "" {
		version = "1.0.0"
	}

	wInfo := models.WorkerInfo{
		ID:            workerID,
		Hostname:      req.Hostname,
		Version:       version,
		Status:        models.WorkerStateActive,
		LastHeartbeat: time.Now(),
	}

	s.registry.mu.Lock()
	s.registry.workers[workerID] = &wInfo
	s.registry.mu.Unlock()

	if s.pgRepo != nil {
		_ = s.pgRepo.RegisterWorker(r.Context(), wInfo)
		_ = s.pgRepo.RecordEvent(r.Context(), models.CrawlEvent{
			WorkerID:  workerID,
			EventType: "WORKER_REGISTERED",
			Message:   fmt.Sprintf("Worker node %s registered from %s", workerID, req.Hostname),
		})
	}

	log.Printf("📥 Registered new worker: %s (host: %s)\n", workerID, req.Hostname)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.RegisterResponse{
		WorkerID: workerID,
		Status:   "registered",
	})
}

// HandleHeartbeat updates worker telemetry
func (s *CoordinatorServer) HandleHeartbeat(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req models.HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	s.registry.mu.Lock()
	if existing, ok := s.registry.workers[req.WorkerID]; ok {
		existing.Status = req.Status
		existing.TasksActive = req.TasksActive
		existing.TasksCompleted = req.TasksCompleted
		existing.TasksFailed = req.TasksFailed
		existing.AvgLatencyMs = req.AvgLatencyMs
		existing.LastHeartbeat = time.Now()
	} else {
		s.registry.workers[req.WorkerID] = &models.WorkerInfo{
			ID:             req.WorkerID,
			Hostname:       "dynamic-worker",
			Status:         req.Status,
			TasksActive:    req.TasksActive,
			TasksCompleted: req.TasksCompleted,
			TasksFailed:    req.TasksFailed,
			AvgLatencyMs:   req.AvgLatencyMs,
			LastHeartbeat:  time.Now(),
		}
	}
	s.registry.mu.Unlock()

	if s.pgRepo != nil {
		_ = s.pgRepo.UpdateWorkerHeartbeat(r.Context(), req)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.HeartbeatResponse{
		Acknowledged: true,
		ServerTime:   time.Now().UTC().Format(time.RFC3339),
	})
}

// HandlePoll assigns the next polite task with visibility lease
func (s *CoordinatorServer) HandlePoll(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req models.PollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	s.registry.mu.Lock()
	if wInfo, exists := s.registry.workers[req.WorkerID]; exists {
		wInfo.LastHeartbeat = time.Now()
	}
	s.registry.mu.Unlock()

	ctx := r.Context()
	nextURL, err := s.frontier.GetPoliteTaskWithLease(ctx, s.politenessDelay, req.WorkerID)
	if err != nil {
		log.Printf("❌ Failed to get next polite URL: %v\n", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.PollResponse{
		URL: nextURL,
	})
}

// HandleReport processes crawl results and releases task leases
func (s *CoordinatorServer) HandleReport(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req models.ReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// 1. Release in-flight lease
	if s.frontier != nil {
		_ = s.frontier.AckTask(ctx, req.URL)
	}

	// 2. Persist page findings
	if s.pgRepo != nil {
		_, err := s.pgRepo.SavePageFull(ctx, req)
		if err != nil {
			log.Printf("❌ Failed to save page: %v\n", err)
		}

		_ = s.pgRepo.RecordEvent(ctx, models.CrawlEvent{
			WorkerID:   req.WorkerID,
			EventType:  "PAGE_CRAWLED",
			URL:        req.URL,
			StatusCode: req.StatusCode,
			LatencyMs:  req.LatencyMs,
			Message:    fmt.Sprintf("%s (%d OK) - %d links", req.Title, req.StatusCode, len(req.Links)),
		})
	}

	// 3. Enqueue discovered links
	newEnqueued := 0
	if len(req.Links) > 0 && s.frontier != nil {
		var err error
		newEnqueued, err = s.frontier.Submit(ctx, req.Links...)
		if err != nil {
			log.Printf("❌ Failed to enqueue discovered links: %v\n", err)
		}
	}

	visitedCount := int64(0)
	if s.frontier != nil {
		visitedCount, _ = s.frontier.VisitedCount(ctx)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.ReportResponse{
		Status:            "accepted",
		NewLinksEnqueued:  newEnqueued,
		TotalVisitedCount: visitedCount,
	})
}

// HandleStatus returns comprehensive real-time cluster telemetry
func (s *CoordinatorServer) HandleStatus(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	ctx := r.Context()
	qSize := int64(0)
	vCount := int64(0)
	dCount := int64(0)

	if s.frontier != nil {
		qSize, _ = s.frontier.QueueSize(ctx)
		vCount, _ = s.frontier.VisitedCount(ctx)
	}

	if s.pgRepo != nil {
		dCount, _ = s.pgRepo.GetDomainCount(ctx)
	}

	// Active workers
	var activeWorkers []models.WorkerInfo
	s.registry.mu.RLock()
	now := time.Now()
	for _, w := range s.registry.workers {
		secAgo := now.Sub(w.LastHeartbeat).Seconds()
		activeWorkers = append(activeWorkers, models.WorkerInfo{
			ID:             w.ID,
			Hostname:       w.Hostname,
			Version:        w.Version,
			Status:         w.Status,
			TasksActive:    w.TasksActive,
			TasksCompleted: w.TasksCompleted,
			TasksFailed:    w.TasksFailed,
			AvgLatencyMs:   w.AvgLatencyMs,
			LastSeenSecAgo: secAgo,
			LastHeartbeat:  w.LastHeartbeat,
		})
	}
	s.registry.mu.RUnlock()

	recentResults := []models.SearchResult{}
	liveLogs := []models.CrawlEvent{}

	if s.pgRepo != nil {
		recentResults, _ = s.pgRepo.GetRecentPages(ctx, 12)
		liveLogs, _ = s.pgRepo.GetRecentEvents(ctx, 15)
	}

	if recentResults == nil {
		recentResults = []models.SearchResult{}
	}
	if liveLogs == nil {
		liveLogs = []models.CrawlEvent{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.StatusResponse{
		Status:       "online",
		Mode:         "cluster",
		QueueSize:    qSize,
		VisitedCount: vCount,
		DomainCount:  dCount,
		Workers:      activeWorkers,
		RecentPages:  recentResults,
		LiveLogs:     liveLogs,
		UptimeSec:    time.Since(s.startTime).Seconds(),
	})
}

// HandleMetrics returns machine-readable Prometheus / JSON metrics
func (s *CoordinatorServer) HandleMetrics(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	ctx := r.Context()
	qSize, _ := s.frontier.QueueSize(ctx)
	vCount, _ := s.frontier.VisitedCount(ctx)
	dCount, _ := s.pgRepo.GetDomainCount(ctx)

	s.registry.mu.RLock()
	workerCount := len(s.registry.workers)
	s.registry.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"crawler_queue_size":     qSize,
		"crawler_visited_total":  vCount,
		"crawler_domains_total":  dCount,
		"crawler_workers_active": workerCount,
		"crawler_uptime_seconds": time.Since(s.startTime).Seconds(),
	})
}

// HandleWorkers lists registered worker nodes
func (s *CoordinatorServer) HandleWorkers(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	s.registry.mu.RLock()
	var list []models.WorkerInfo
	now := time.Now()
	for _, w := range s.registry.workers {
		list = append(list, models.WorkerInfo{
			ID:             w.ID,
			Hostname:       w.Hostname,
			Version:        w.Version,
			Status:         w.Status,
			TasksActive:    w.TasksActive,
			TasksCompleted: w.TasksCompleted,
			TasksFailed:    w.TasksFailed,
			AvgLatencyMs:   w.AvgLatencyMs,
			LastSeenSecAgo: now.Sub(w.LastHeartbeat).Seconds(),
			LastHeartbeat:  w.LastHeartbeat,
		})
	}
	s.registry.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

// HandleActivity returns recent activity events
func (s *CoordinatorServer) HandleActivity(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	limit := 30
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if val, err := strconv.Atoi(lStr); err == nil && val > 0 && val <= 100 {
			limit = val
		}
	}

	events, err := s.pgRepo.GetRecentEvents(r.Context(), limit)
	if err != nil {
		events = []models.CrawlEvent{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(events)
}

// HandleSubmitSeeds enqueues custom seed URLs or launches a crawl job
func (s *CoordinatorServer) HandleSubmitSeeds(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req models.SubmitSeedsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	if len(req.URLs) == 0 {
		http.Error(w, "No URLs provided", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	jobID := req.JobID
	if jobID == "" {
		jobID = fmt.Sprintf("job-%d", time.Now().Unix())
	}

	// Persist job if pgRepo available
	if s.pgRepo != nil {
		maxDepth := req.MaxDepth
		if maxDepth <= 0 {
			maxDepth = 3
		}
		maxPages := req.MaxPages
		if maxPages <= 0 {
			maxPages = 100
		}
		_ = s.pgRepo.CreateJob(ctx, models.CrawlJob{
			ID:       jobID,
			Name:     fmt.Sprintf("Crawl %s", req.URLs[0]),
			Status:   models.JobStateRunning,
			SeedURLs: req.URLs,
			MaxDepth: maxDepth,
			MaxPages: maxPages,
		})
		_ = s.pgRepo.RecordEvent(ctx, models.CrawlEvent{
			JobID:     jobID,
			EventType: "JOB_SUBMITTED",
			Message:   fmt.Sprintf("Submitted %d seed URLs for crawl job %s", len(req.URLs), jobID),
		})
	}

	enqueued := 0
	if s.frontier != nil {
		var err error
		enqueued, err = s.frontier.Submit(ctx, req.URLs...)
		if err != nil {
			log.Printf("❌ Failed to submit seeds to frontier: %v\n", err)
			http.Error(w, "Internal server error during URL submit", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.SubmitSeedsResponse{
		Status:   "success",
		JobID:    jobID,
		Enqueued: enqueued,
	})
}

// HandleJobs manages listing crawl jobs
func (s *CoordinatorServer) HandleJobs(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if s.pgRepo == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]models.CrawlJob{})
		return
	}

	jobs, err := s.pgRepo.ListJobs(r.Context(), 50, 0)
	if err != nil {
		jobs = []models.CrawlJob{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(jobs)
}

// HandleJobOperation handles pause, resume, cancel on jobs
func (s *CoordinatorServer) HandleJobOperation(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Path parsing: /api/jobs/{id}/{op}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		http.Error(w, "Invalid job endpoint path", http.StatusBadRequest)
		return
	}

	jobID := parts[2]
	op := parts[3]

	var targetStatus string
	switch op {
	case "pause":
		targetStatus = models.JobStatePaused
	case "resume":
		targetStatus = models.JobStateRunning
	case "cancel":
		targetStatus = models.JobStateCancelled
	default:
		http.Error(w, "Unsupported job operation", http.StatusBadRequest)
		return
	}

	if s.pgRepo != nil {
		_ = s.pgRepo.UpdateJobStatus(r.Context(), jobID, targetStatus, "")
		_ = s.pgRepo.RecordEvent(r.Context(), models.CrawlEvent{
			JobID:     jobID,
			EventType: fmt.Sprintf("JOB_%s", strings.ToUpper(op)),
			Message:   fmt.Sprintf("Job %s transitioned to %s", jobID, targetStatus),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"job_id": jobID,
		"status": targetStatus,
	})
}

// HandleSearch executes full-text search with filtering
func (s *CoordinatorServer) HandleSearch(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	query := r.URL.Query().Get("q")
	domain := r.URL.Query().Get("domain")
	statusCode, _ := strconv.Atoi(r.URL.Query().Get("status"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	if limit <= 0 {
		limit = 30
	}

	ctx := r.Context()
	var results []models.SearchResult
	var err error

	if s.pgRepo != nil {
		results, err = s.pgRepo.SearchPagesAdvanced(ctx, query, domain, statusCode, limit, offset)
	}

	if err != nil {
		log.Printf("❌ Search query failed: %v\n", err)
		http.Error(w, "Failed to retrieve search results", http.StatusInternalServerError)
		return
	}

	if results == nil {
		results = []models.SearchResult{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

// HandlePageDetails inspects a single crawled page by ID
func (s *CoordinatorServer) HandlePageDetails(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		http.Error(w, "Missing page ID", http.StatusBadRequest)
		return
	}

	id, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		http.Error(w, "Invalid page ID", http.StatusBadRequest)
		return
	}

	if s.pgRepo == nil {
		http.Error(w, "Database unavailable", http.StatusServiceUnavailable)
		return
	}

	page, err := s.pgRepo.GetPageByID(r.Context(), id)
	if err != nil {
		http.Error(w, "Page not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(page)
}

// HandleDomains returns domain aggregates
func (s *CoordinatorServer) HandleDomains(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	limit := 30
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if val, err := strconv.Atoi(lStr); err == nil {
			limit = val
		}
	}

	if s.pgRepo == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]models.DomainMetric{})
		return
	}

	metrics, err := s.pgRepo.GetDomainMetrics(r.Context(), limit)
	if err != nil {
		metrics = []models.DomainMetric{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metrics)
}

// HandleReset executes tiered reset operations with safety safeguards
func (s *CoordinatorServer) HandleReset(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req models.ResetRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	level := req.Level
	if level == "" {
		level = "dev"
	}

	ctx := r.Context()

	switch level {
	case "ui":
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(models.ResetResponse{
			Status:  "success",
			Level:   "ui",
			Message: "UI temporary state cleared",
		})
		return

	case "queue":
		if s.frontier != nil {
			_ = s.frontier.Clear(ctx)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(models.ResetResponse{
			Status:  "success",
			Level:   "queue",
			Message: "Frontier queue and task leases cleared",
		})
		return

	case "data":
		if s.pgRepo != nil {
			_ = s.pgRepo.TruncateData(ctx)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(models.ResetResponse{
			Status:  "success",
			Level:   "data",
			Message: "Crawled pages and domain metrics cleared",
		})
		return

	case "production":
		if req.ConfirmationPhrase != "WIPE ALL DATA" {
			http.Error(w, "Forbidden: Production reset requires confirmation_phrase 'WIPE ALL DATA'", http.StatusForbidden)
			return
		}
		if s.frontier != nil {
			_ = s.frontier.Clear(ctx)
		}
		if s.pgRepo != nil {
			_ = s.pgRepo.Truncate(ctx)
			_ = s.pgRepo.RecordEvent(ctx, models.CrawlEvent{
				EventType: "PRODUCTION_RESET",
				Message:   "Production database and queue wiped by authorized operator",
			})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(models.ResetResponse{
			Status:  "success",
			Level:   "production",
			Message: "Full production cluster reset completed",
		})
		return

	case "dev":
		fallthrough
	default:
		if s.frontier != nil {
			_ = s.frontier.Clear(ctx)
		}
		if s.pgRepo != nil {
			_ = s.pgRepo.Truncate(ctx)
		}
		if s.frontier != nil {
			_, _ = s.frontier.Submit(ctx, "https://go.dev")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(models.ResetResponse{
			Status:  "reset_completed",
			Level:   "dev",
			Message: "Database cleared, queue flushed, and re-seeded with https://go.dev",
		})
		return
	}
}

// HandleConfig returns runtime cluster configuration settings
func (s *CoordinatorServer) HandleConfig(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"politeness_delay_ms": s.politenessDelay.Milliseconds(),
		"max_depth_default":   3,
		"max_pages_default":   100,
		"lease_timeout_sec":   60,
		"version":             "1.0.0",
		"service":             "gopher-distributed-crawler",
	})
}
