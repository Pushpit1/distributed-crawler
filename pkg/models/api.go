package models

// RegisterRequest represents the request payload from workers registering.
type RegisterRequest struct {
	Hostname string `json:"hostname"`
}

// RegisterResponse represents the response containing the unique worker ID.
type RegisterResponse struct {
	WorkerID string `json:"worker_id"`
	Status   string `json:"status"`
}

// PollRequest represents the poll payload from a worker.
type PollRequest struct {
	WorkerID string `json:"worker_id"`
}

// PollResponse contains the next URL assigned to the worker.
type PollResponse struct {
	URL string `json:"url"`
}

// ReportRequest contains the crawl findings from a worker.
type ReportRequest struct {
	WorkerID    string   `json:"worker_id"`
	URL         string   `json:"url"`
	StatusCode  int      `json:"status_code"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Links       []string `json:"links"`
}

// ReportResponse confirms receipt and lists enqueued URLs.
type ReportResponse struct {
	Status             string `json:"status"`
	NewLinksEnqueued   int    `json:"new_links_enqueued"`
	TotalVisitedCount  int64  `json:"total_visited_count"`
}

// SearchResult represents a matching crawled page search result.
type SearchResult struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Description string `json:"description"`
	StatusCode  int    `json:"status_code"`
}

// WorkerInfo represents dashboard-friendly metrics about a worker node.
type WorkerInfo struct {
	ID             string  `json:"id"`
	LastSeenSecAgo float64 `json:"last_seen_sec_ago"`
}

// StatusResponse contains the full cluster status, queues, active workers, and recent crawls.
type StatusResponse struct {
	Status       string         `json:"status,omitempty"`
	QueueSize    int64          `json:"queue_size"`
	VisitedCount int64          `json:"visited_count"`
	DomainCount  int64          `json:"domain_count"`
	Workers      []WorkerInfo   `json:"workers"`
	RecentPages  []SearchResult `json:"recent_pages"`
	UptimeSec    float64        `json:"uptime_sec,omitempty"`
}

// SubmitSeedsRequest represents custom URLs submitted by the user to start crawling.
type SubmitSeedsRequest struct {
	URLs []string `json:"urls"`
}

// SubmitSeedsResponse confirms enqueuing of custom seed URLs.
type SubmitSeedsResponse struct {
	Status   string `json:"status"`
	Enqueued int    `json:"enqueued"`
}
