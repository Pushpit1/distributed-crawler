package master

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"distributed-crawler/pkg/crawler"
	"distributed-crawler/pkg/db"
	"distributed-crawler/pkg/models"
)

// WorkerRegistry keeps track of currently active workers
type WorkerRegistry struct {
	mu      sync.Mutex
	workers map[string]time.Time // Worker ID -> Last Seen Timestamp
}

// CoordinatorServer holds HTTP route handlers and dependencies
type CoordinatorServer struct {
	frontier        *crawler.Frontier
	pgRepo          *db.PostgresRepository
	registry        *WorkerRegistry
	politenessDelay time.Duration
}

// NewCoordinatorServer initializes the server instance with a politeness delay and DB repo configuration
func NewCoordinatorServer(frontier *crawler.Frontier, pgRepo *db.PostgresRepository, politenessDelay time.Duration) *CoordinatorServer {
	return &CoordinatorServer{
		frontier: frontier,
		pgRepo:   pgRepo,
		registry: &WorkerRegistry{
			workers: make(map[string]time.Time),
		},
		politenessDelay: politenessDelay,
	}
}

// HandleRegister handles POST /register
func (s *CoordinatorServer) HandleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req models.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	s.registry.mu.Lock()
	// Generate a simple worker ID based on timestamp and host
	workerID := fmt.Sprintf("worker-%s-%d", req.Hostname, time.Now().UnixNano())
	s.registry.workers[workerID] = time.Now()
	s.registry.mu.Unlock()

	log.Printf("📥 Registered new worker: %s (host: %s)\n", workerID, req.Hostname)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.RegisterResponse{
		WorkerID: workerID,
		Status:   "registered",
	})
}

// HandlePoll handles POST /poll
func (s *CoordinatorServer) HandlePoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req models.PollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// Update last-seen status of the worker
	s.registry.mu.Lock()
	if _, exists := s.registry.workers[req.WorkerID]; exists {
		s.registry.workers[req.WorkerID] = time.Now()
	} else {
		// Auto-register if not seen before (prevents crash on client restarts)
		s.registry.workers[req.WorkerID] = time.Now()
		log.Printf("⚠️ Unknown worker %s polled. Auto-registering.\n", req.WorkerID)
	}
	s.registry.mu.Unlock()

	// Get next URL task from Redis Frontier with politeness checks
	ctx := r.Context()
	nextURL, err := s.frontier.GetPoliteTask(ctx, s.politenessDelay)
	if err != nil {
		log.Printf("❌ Failed to get next polite URL from frontier: %v\n", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.PollResponse{
		URL: nextURL,
	})
}

// HandleReport handles POST /report
func (s *CoordinatorServer) HandleReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req models.ReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// Log the crawl stats (Simulating master tracking page crawls)
	log.Printf("📝 Report received from %s | URL: %s | Title: %q | Links Extracted: %d\n",
		req.WorkerID, req.URL, req.Title, len(req.Links))

	ctx := r.Context()

	// Save the crawled page findings to PostgreSQL!
	_, dbErr := s.pgRepo.SavePage(ctx, req.URL, req.Title, req.Description, req.StatusCode)
	if dbErr != nil {
		log.Printf("❌ Failed to save page to database: %v\n", dbErr)
	}

	newEnqueued := 0
	var err error

	// If crawl succeeded and found links, submit them to the Frontier for deduplication and queuing
	if len(req.Links) > 0 {
		newEnqueued, err = s.frontier.Submit(ctx, req.Links...)
		if err != nil {
			log.Printf("❌ Failed to submit reported links to frontier: %v\n", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	}

	visitedCount, _ := s.frontier.VisitedCount(ctx)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.ReportResponse{
		Status:            "accepted",
		NewLinksEnqueued:  newEnqueued,
		TotalVisitedCount: visitedCount,
	})
}

// HandleSearch handles GET /search
func (s *CoordinatorServer) HandleSearch(w http.ResponseWriter, r *http.Request) {
	// Enable CORS (Cross-Origin Resource Sharing) so that a frontend hosted elsewhere can connect
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "Missing query parameter 'q'", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	results, err := s.pgRepo.SearchPages(ctx, query)
	if err != nil {
		log.Printf("❌ Search query failed: %v\n", err)
		http.Error(w, "Failed to retrieve search results", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

// HandleStatus handles GET /status
func (s *CoordinatorServer) HandleStatus(w http.ResponseWriter, r *http.Request) {
	// Enable CORS
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()

	// 1. Get queue size from Redis
	qSize, err := s.frontier.QueueSize(ctx)
	if err != nil {
		log.Printf("❌ Failed to get queue size: %v\n", err)
		qSize = 0
	}

	// 2. Get visited count from Redis
	vCount, err := s.frontier.VisitedCount(ctx)
	if err != nil {
		log.Printf("❌ Failed to get visited count: %v\n", err)
		vCount = 0
	}

	// 3. Get unique domains count from Postgres
	dCount, err := s.pgRepo.GetDomainCount(ctx)
	if err != nil {
		log.Printf("❌ Failed to get domain count: %v\n", err)
		dCount = 0
	}

	// 4. Extract active workers from registry
	var activeWorkers []models.WorkerInfo
	s.registry.mu.Lock()
	now := time.Now()
	for id, lastSeen := range s.registry.workers {
		// Only display workers seen in the last 15 seconds as active
		secAgo := now.Sub(lastSeen).Seconds()
		if secAgo < 15.0 {
			activeWorkers = append(activeWorkers, models.WorkerInfo{
				ID:             id,
				LastSeenSecAgo: secAgo,
			})
		}
	}
	s.registry.mu.Unlock()

	// 5. Get latest 5 crawls from database for activity feed
	recentResults, err := s.pgRepo.GetRecentPages(ctx, 5)
	if err != nil {
		log.Printf("❌ Failed to get recent pages: %v\n", err)
		recentResults = []models.SearchResult{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.StatusResponse{
		QueueSize:    qSize,
		VisitedCount: vCount,
		DomainCount:  dCount,
		Workers:      activeWorkers,
		RecentPages:  recentResults,
	})
}

// HandleReset handles POST /reset
func (s *CoordinatorServer) HandleReset(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()

	// 1. Wipe Redis queue and visited keys
	if err := s.frontier.Clear(ctx); err != nil {
		log.Printf("❌ Failed to clear frontier: %v\n", err)
		http.Error(w, "Failed to clear Redis queue", http.StatusInternalServerError)
		return
	}

	// 2. Truncate Postgres tables (clean slate)
	if err := s.pgRepo.Truncate(ctx); err != nil {
		log.Printf("❌ Failed to truncate database: %v\n", err)
		http.Error(w, "Failed to clear database", http.StatusInternalServerError)
		return
	}

	// 3. Re-seed with default start page to kickstart the system
	_, err := s.frontier.Submit(ctx, "https://go.dev")
	if err != nil {
		log.Printf("❌ Failed to re-seed: %v\n", err)
	}

	log.Println("🧹 Engine reset triggered successfully: Database cleared, queue flushed, and seed enqueued.")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "reset_completed",
	})
}

// HandleSubmitSeeds handles POST /submit
func (s *CoordinatorServer) HandleSubmitSeeds(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

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
	enqueued, err := s.frontier.Submit(ctx, req.URLs...)
	if err != nil {
		log.Printf("❌ Failed to submit custom seeds: %v\n", err)
		http.Error(w, "Internal server error during URL submit", http.StatusInternalServerError)
		return
	}

	log.Printf("🌱 User submitted %d custom seeds. Enqueued new: %d\n", len(req.URLs), enqueued)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.SubmitSeedsResponse{
		Status:   "success",
		Enqueued: enqueued,
	})
}
