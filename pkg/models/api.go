package models

import "time"

// WorkerState constants
const (
	WorkerStateStarting  = "STARTING"
	WorkerStateIdle      = "IDLE"
	WorkerStateActive    = "ACTIVE"
	WorkerStateDraining  = "DRAINING"
	WorkerStateUnhealthy = "UNHEALTHY"
	WorkerStateStopped   = "STOPPED"
)

// JobState constants
const (
	JobStatePending   = "PENDING"
	JobStateRunning   = "RUNNING"
	JobStatePaused    = "PAUSED"
	JobStateCompleted = "COMPLETED"
	JobStateCancelled = "CANCELLED"
	JobStateFailed    = "FAILED"
)

// RegisterRequest represents the request payload from workers registering.
type RegisterRequest struct {
	Hostname string `json:"hostname"`
	Version  string `json:"version,omitempty"`
}

// RegisterResponse represents the response containing the unique worker ID.
type RegisterResponse struct {
	WorkerID string `json:"worker_id"`
	Status   string `json:"status"`
}

// HeartbeatRequest payload sent periodically by worker daemons
type HeartbeatRequest struct {
	WorkerID       string  `json:"worker_id"`
	Status         string  `json:"status"`
	TasksActive    int     `json:"tasks_active"`
	TasksCompleted int     `json:"tasks_completed"`
	TasksFailed    int     `json:"tasks_failed"`
	AvgLatencyMs   int     `json:"avg_latency_ms"`
}

// HeartbeatResponse returned by master
type HeartbeatResponse struct {
	Acknowledged bool   `json:"acknowledged"`
	ServerTime   string `json:"server_time"`
}

// PollRequest represents the poll payload from a worker.
type PollRequest struct {
	WorkerID string `json:"worker_id"`
}

// PollResponse contains the next URL assigned to the worker.
type PollResponse struct {
	URL        string `json:"url"`
	JobID      string `json:"job_id,omitempty"`
	Depth      int    `json:"depth,omitempty"`
	MaxDepth   int    `json:"max_depth,omitempty"`
	TaskLeaseID string `json:"task_lease_id,omitempty"`
}

// ReportRequest contains the crawl findings from a worker.
type ReportRequest struct {
	WorkerID      string   `json:"worker_id"`
	JobID         string   `json:"job_id,omitempty"`
	URL           string   `json:"url"`
	CanonicalURL  string   `json:"canonical_url,omitempty"`
	StatusCode    int      `json:"status_code"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	H1            string   `json:"h1,omitempty"`
	ContentType   string   `json:"content_type,omitempty"`
	ContentLength int      `json:"content_length,omitempty"`
	LatencyMs     int      `json:"latency_ms,omitempty"`
	Depth         int      `json:"depth,omitempty"`
	Links         []string `json:"links"`
	ErrorMessage  string   `json:"error_message,omitempty"`
}

// ReportResponse confirms receipt and lists enqueued URLs.
type ReportResponse struct {
	Status            string `json:"status"`
	NewLinksEnqueued  int    `json:"new_links_enqueued"`
	TotalVisitedCount int64  `json:"total_visited_count"`
}

// CrawlJob represents a structured crawl execution job
type CrawlJob struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Status       string     `json:"status"`
	SeedURLs     []string   `json:"seed_urls"`
	MaxDepth     int        `json:"max_depth"`
	MaxPages     int        `json:"max_pages"`
	PagesCrawled int        `json:"pages_crawled"`
	PagesFailed  int        `json:"pages_failed"`
	ErrorMessage string     `json:"error_message,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
}

// CreateJobRequest payload to launch a crawl job
type CreateJobRequest struct {
	Name     string   `json:"name,omitempty"`
	URLs     []string `json:"urls"`
	MaxDepth int      `json:"max_depth,omitempty"`
	MaxPages int      `json:"max_pages,omitempty"`
}

// SearchResult represents a matching crawled page search result.
type SearchResult struct {
	ID            int64     `json:"id,omitempty"`
	JobID         string    `json:"job_id,omitempty"`
	URL           string    `json:"url"`
	CanonicalURL  string    `json:"canonical_url,omitempty"`
	Domain        string    `json:"domain,omitempty"`
	Title         string    `json:"title"`
	Description   string    `json:"description"`
	H1            string    `json:"h1,omitempty"`
	ContentType   string    `json:"content_type,omitempty"`
	ContentLength int       `json:"content_length,omitempty"`
	StatusCode    int       `json:"status_code"`
	LatencyMs     int       `json:"latency_ms,omitempty"`
	Depth         int       `json:"depth,omitempty"`
	LinksCount    int       `json:"links_count,omitempty"`
	CrawledAt     time.Time `json:"crawled_at"`
	Relevance     float64   `json:"relevance,omitempty"`
}

// WorkerInfo represents dashboard-friendly metrics about a worker node.
type WorkerInfo struct {
	ID             string    `json:"id"`
	Hostname       string    `json:"hostname"`
	Version        string    `json:"version,omitempty"`
	Status         string    `json:"status"`
	TasksActive    int       `json:"tasks_active"`
	TasksCompleted int       `json:"tasks_completed"`
	TasksFailed    int       `json:"tasks_failed"`
	AvgLatencyMs   int       `json:"avg_latency_ms"`
	LastSeenSecAgo float64   `json:"last_seen_sec_ago"`
	LastHeartbeat  time.Time `json:"last_heartbeat"`
}

// CrawlEvent represents an auditable activity feed log entry
type CrawlEvent struct {
	ID         int64     `json:"id"`
	JobID      string    `json:"job_id,omitempty"`
	WorkerID   string    `json:"worker_id,omitempty"`
	EventType  string    `json:"event_type"`
	URL        string    `json:"url,omitempty"`
	StatusCode int       `json:"status_code,omitempty"`
	LatencyMs  int       `json:"latency_ms,omitempty"`
	Message    string    `json:"message"`
	CreatedAt  time.Time `json:"created_at"`
}

// DomainMetric represents aggregate statistics per domain
type DomainMetric struct {
	Domain        string    `json:"domain"`
	TotalPages    int       `json:"total_pages"`
	TotalBytes    int64     `json:"total_bytes"`
	AvgLatencyMs  int       `json:"avg_latency_ms"`
	LastCrawledAt time.Time `json:"last_crawled_at"`
}

// StatusResponse contains the full cluster status, queues, active workers, and recent crawls.
type StatusResponse struct {
	Status       string         `json:"status,omitempty"`
	Mode         string         `json:"mode,omitempty"`
	QueueSize    int64          `json:"queue_size"`
	VisitedCount int64          `json:"visited_count"`
	DomainCount  int64          `json:"domain_count"`
	ActiveJobs   int            `json:"active_jobs,omitempty"`
	Workers      []WorkerInfo   `json:"workers"`
	RecentPages  []SearchResult `json:"recent_pages"`
	LiveLogs     []CrawlEvent   `json:"live_logs,omitempty"`
	UptimeSec    float64        `json:"uptime_sec,omitempty"`
}

// SubmitSeedsRequest represents custom URLs submitted by the user to start crawling.
type SubmitSeedsRequest struct {
	URLs     []string `json:"urls"`
	JobID    string   `json:"job_id,omitempty"`
	MaxDepth int      `json:"max_depth,omitempty"`
	MaxPages int      `json:"max_pages,omitempty"`
}

// SubmitSeedsResponse confirms enqueuing of custom seed URLs.
type SubmitSeedsResponse struct {
	Status   string `json:"status"`
	JobID    string `json:"job_id,omitempty"`
	Enqueued int    `json:"enqueued"`
}

// ResetRequest defines tiered reset parameters
type ResetRequest struct {
	Level             string `json:"level"` // "ui", "job", "queue", "data", "dev", "production"
	JobID             string `json:"job_id,omitempty"`
	ConfirmationPhrase string `json:"confirmation_phrase,omitempty"`
}

// ResetResponse confirms reset execution
type ResetResponse struct {
	Status  string `json:"status"`
	Level   string `json:"level"`
	Message string `json:"message"`
}
