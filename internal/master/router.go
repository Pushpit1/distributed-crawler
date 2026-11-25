package master

import "net/http"

// Router builds the production HTTP ServeMux wrapped with security, CORS, rate limiting, and request ID middleware.
func (s *CoordinatorServer) Router(staticDir string) http.Handler {
	mux := http.NewServeMux()

	// Health & Readiness
	mux.HandleFunc("/health", s.HandleHealth)
	mux.HandleFunc("/api/health", s.HandleHealth)
	mux.HandleFunc("/ready", s.HandleReady)
	mux.HandleFunc("/api/ready", s.HandleReady)

	// Worker Coordination Protocol
	mux.HandleFunc("/register", s.HandleRegister)
	mux.HandleFunc("/api/register", s.HandleRegister)
	mux.HandleFunc("/heartbeat", s.HandleHeartbeat)
	mux.HandleFunc("/api/heartbeat", s.HandleHeartbeat)
	mux.HandleFunc("/poll", s.HandlePoll)
	mux.HandleFunc("/api/poll", s.HandlePoll)
	mux.HandleFunc("/report", s.HandleReport)
	mux.HandleFunc("/api/report", s.HandleReport)

	// Telemetry, Metrics & Analytics
	mux.HandleFunc("/status", s.HandleStatus)
	mux.HandleFunc("/api/status", s.HandleStatus)
	mux.HandleFunc("/metrics", s.HandleMetrics)
	mux.HandleFunc("/api/metrics", s.HandleMetrics)
	mux.HandleFunc("/api/analytics", s.HandleAnalytics)
	mux.HandleFunc("/api/workers", s.HandleWorkers)
	mux.HandleFunc("/api/workers/", s.HandleWorkerDetails)
	mux.HandleFunc("/api/activity", s.HandleActivity)
	mux.HandleFunc("/api/errors", s.HandleErrors)

	// Crawl Submission & Jobs
	mux.HandleFunc("/submit", s.HandleSubmitSeeds)
	mux.HandleFunc("/api/submit", s.HandleSubmitSeeds)
	mux.HandleFunc("/api/crawl", s.HandleCrawlStep)
	mux.HandleFunc("/api/jobs", s.HandleJobs)
	mux.HandleFunc("/api/jobs/", s.HandleJobOperation)

	// Search, Export & Domain Registry
	mux.HandleFunc("/search", s.HandleSearch)
	mux.HandleFunc("/api/search", s.HandleSearch)
	mux.HandleFunc("/api/export", s.HandleExport)
	mux.HandleFunc("/api/pages/", s.HandlePageDetails)
	mux.HandleFunc("/api/domains", s.HandleDomains)

	// Administration & Settings
	mux.HandleFunc("/reset", s.HandleReset)
	mux.HandleFunc("/api/reset", s.HandleReset)
	mux.HandleFunc("/api/config", s.HandleConfig)

	if staticDir != "" {
		mux.Handle("/", http.FileServer(http.Dir(staticDir)))
	}

	return s.WithMiddleware(mux)
}
