package models

import "time"

// WorkerState constants represent distributed worker daemon lifecycle states.
const (
	WorkerStateStarting  = "STARTING"
	WorkerStateIdle      = "IDLE"
	WorkerStateActive    = "ACTIVE"
	WorkerStateDraining  = "DRAINING"
	WorkerStateUnhealthy = "UNHEALTHY"
	WorkerStateStopped   = "STOPPED"
)

// JobState constants represent crawl job lifecycle states.
const (
	JobStateQueued    = "QUEUED"
	JobStatePending   = "PENDING"
	JobStateRunning   = "RUNNING"
	JobStatePaused    = "PAUSED"
	JobStateCompleted = "COMPLETED"
	JobStateCancelled = "CANCELLED"
	JobStateFailed    = "FAILED"
)

// CrawlTask represents a structured crawl unit stored in the Redis frontier and leases.
type CrawlTask struct {
	URL             string    `json:"url"`
	JobID           string    `json:"job_id,omitempty"`
	Depth           int       `json:"depth"`
	MaxDepth        int       `json:"max_depth,omitempty"`
	Retries         int       `json:"retries,omitempty"`
	AllowedDomains  []string  `json:"allowed_domains,omitempty"`
	ExcludePatterns []string  `json:"exclude_patterns,omitempty"`
	PolitenessMs    int       `json:"politeness_ms,omitempty"`
	EnqueuedAt      time.Time `json:"enqueued_at,omitempty"`
}

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

// HeartbeatRequest payload sent periodically by worker daemons.
type HeartbeatRequest struct {
	WorkerID       string `json:"worker_id"`
	Hostname       string `json:"hostname,omitempty"`
	Version        string `json:"version,omitempty"`
	Status         string `json:"status"`
	CurrentTask    string `json:"current_task,omitempty"`
	TasksActive    int    `json:"tasks_active"`
	TasksCompleted int    `json:"tasks_completed"`
	TasksFailed    int    `json:"tasks_failed"`
	AvgLatencyMs   int    `json:"avg_latency_ms"`
	UptimeSec      int64  `json:"uptime_sec,omitempty"`
}

// HeartbeatResponse returned by master.
type HeartbeatResponse struct {
	Acknowledged bool   `json:"acknowledged"`
	ServerTime   string `json:"server_time"`
}

// PollRequest represents the poll payload from a worker.
type PollRequest struct {
	WorkerID string `json:"worker_id"`
}

// PollResponse contains the next crawl task assigned to the worker.
type PollResponse struct {
	URL             string   `json:"url"`
	JobID           string   `json:"job_id,omitempty"`
	Depth           int      `json:"depth,omitempty"`
	MaxDepth        int      `json:"max_depth,omitempty"`
	Retries         int      `json:"retries,omitempty"`
	AllowedDomains  []string `json:"allowed_domains,omitempty"`
	ExcludePatterns []string `json:"exclude_patterns,omitempty"`
	TaskLeaseID     string   `json:"task_lease_id,omitempty"`
	LeaseKey        string   `json:"lease_key,omitempty"`
}

// ReportRequest contains the crawl findings from a worker.
type ReportRequest struct {
	WorkerID      string   `json:"worker_id"`
	JobID         string   `json:"job_id,omitempty"`
	LeaseKey      string   `json:"lease_key,omitempty"`
	URL           string   `json:"url"`
	FinalURL      string   `json:"final_url,omitempty"`
	CanonicalURL  string   `json:"canonical_url,omitempty"`
	StatusCode    int      `json:"status_code"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	H1            string   `json:"h1,omitempty"`
	Headings      []string `json:"headings,omitempty"`
	ExtractedText string   `json:"extracted_text,omitempty"`
	ContentType   string   `json:"content_type,omitempty"`
	ContentLength int      `json:"content_length,omitempty"`
	LatencyMs     int      `json:"latency_ms,omitempty"`
	Depth         int      `json:"depth,omitempty"`
	Retries       int      `json:"retries,omitempty"`
	RobotsBlocked bool     `json:"robots_blocked,omitempty"`
	Links         []string `json:"links"`
	InternalLinks []string `json:"internal_links,omitempty"`
	ExternalLinks []string `json:"external_links,omitempty"`
	ErrorType     string   `json:"error_type,omitempty"`
	ErrorMessage  string   `json:"error_message,omitempty"`
}

// ReportResponse confirms receipt and lists enqueued URLs.
type ReportResponse struct {
	Status            string `json:"status"`
	NewLinksEnqueued  int    `json:"new_links_enqueued"`
	TotalVisitedCount int64  `json:"total_visited_count"`
}

// CrawlJob represents a structured crawl execution job.
type CrawlJob struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Status          string     `json:"status"`
	SeedURLs        []string   `json:"seed_urls"`
	MaxDepth        int        `json:"max_depth"`
	MaxPages        int        `json:"max_pages"`
	Concurrency     int        `json:"concurrency"`
	PolitenessMs    int        `json:"politeness_ms"`
	AllowedDomains  []string   `json:"allowed_domains,omitempty"`
	ExcludePatterns []string   `json:"exclude_patterns,omitempty"`
	PagesCrawled    int        `json:"pages_crawled"`
	PagesFailed     int        `json:"pages_failed"`
	DiscoveredCount int        `json:"discovered_count"`
	CurrentDepth    int        `json:"current_depth"`
	ErrorMessage    string     `json:"error_message,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	UpdatedAt       time.Time  `json:"updated_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
}

// CrawlError represents a recorded crawl failure.
type CrawlError struct {
	ID           int64     `json:"id"`
	JobID        string    `json:"job_id,omitempty"`
	URL          string    `json:"url"`
	WorkerID     string    `json:"worker_id,omitempty"`
	StatusCode   int       `json:"status_code"`
	ErrorType    string    `json:"error_type"`
	ErrorMessage string    `json:"error_message"`
	CreatedAt    time.Time `json:"created_at"`
}

// JobDetailResponse bundles a job with its seeds, errors, events, and workers.
type JobDetailResponse struct {
	Job     *CrawlJob    `json:"job"`
	Seeds   []string     `json:"seeds"`
	Errors  []CrawlError `json:"errors"`
	Events  []CrawlEvent `json:"events"`
	Workers []string     `json:"associated_workers"`
}

// SearchResult represents a matching crawled page search result.
type SearchResult struct {
	ID            int64     `json:"id,omitempty"`
	JobID         string    `json:"job_id,omitempty"`
	URL           string    `json:"url"`
	FinalURL      string    `json:"final_url,omitempty"`
	CanonicalURL  string    `json:"canonical_url,omitempty"`
	Domain        string    `json:"domain,omitempty"`
	Title         string    `json:"title"`
	Description   string    `json:"description"`
	H1            string    `json:"h1,omitempty"`
	Headings      []string  `json:"headings,omitempty"`
	ExtractedText string    `json:"extracted_text,omitempty"`
	ContentType   string    `json:"content_type,omitempty"`
	ContentLength int       `json:"content_length,omitempty"`
	StatusCode    int       `json:"status_code"`
	LatencyMs     int       `json:"latency_ms,omitempty"`
	Depth         int       `json:"depth,omitempty"`
	LinksCount    int       `json:"links_count,omitempty"`
	InternalLinks []string  `json:"internal_links,omitempty"`
	ExternalLinks []string  `json:"external_links,omitempty"`
	ErrorMessage  string    `json:"error_message,omitempty"`
	CrawledAt     time.Time `json:"crawled_at"`
	Relevance     float64   `json:"relevance,omitempty"`
}

// SearchResponse returns paginated search results and total count.
type SearchResponse struct {
	Results []SearchResult `json:"results"`
	Total   int            `json:"total"`
	Limit   int            `json:"limit"`
	Offset  int            `json:"offset"`
}

// WorkerInfo represents dashboard-friendly metrics about a worker node.
type WorkerInfo struct {
	ID             string    `json:"id"`
	Hostname       string    `json:"hostname"`
	Version        string    `json:"version,omitempty"`
	Status         string    `json:"status"`
	CurrentTask    string    `json:"current_task,omitempty"`
	TasksActive    int       `json:"tasks_active"`
	TasksCompleted int       `json:"tasks_completed"`
	TasksFailed    int       `json:"tasks_failed"`
	AvgLatencyMs   int       `json:"avg_latency_ms"`
	UptimeSec      int64     `json:"uptime_sec,omitempty"`
	LastSeenSecAgo float64   `json:"last_seen_sec_ago"`
	LastHeartbeat  time.Time `json:"last_heartbeat"`
	StartedAt      time.Time `json:"started_at,omitempty"`
}

// CrawlEvent represents an auditable activity feed log entry.
type CrawlEvent struct {
	ID         int64     `json:"id"`
	JobID      string    `json:"job_id,omitempty"`
	WorkerID   string    `json:"worker_id,omitempty"`
	EventType  string    `json:"event_type"`
	Severity   string    `json:"severity,omitempty"`
	URL        string    `json:"url,omitempty"`
	StatusCode int       `json:"status_code,omitempty"`
	LatencyMs  int       `json:"latency_ms,omitempty"`
	Message    string    `json:"message"`
	CreatedAt  time.Time `json:"created_at"`
}

// DomainMetric represents aggregate statistics per domain.
type DomainMetric struct {
	Domain        string    `json:"domain"`
	TotalPages    int       `json:"total_pages"`
	TotalBytes    int64     `json:"total_bytes"`
	AvgLatencyMs  int       `json:"avg_latency_ms"`
	ErrorCount    int       `json:"error_count"`
	LastCrawledAt time.Time `json:"last_crawled_at"`
}

// StatusResponse contains real-time cluster telemetry.
type StatusResponse struct {
	Status              string         `json:"status"`
	Mode                string         `json:"mode"`
	BackendURL          string         `json:"backend_url,omitempty"`
	QueueSize           int64          `json:"queue_size"`
	VisitedCount        int64          `json:"visited_count"`
	PagesIndexed        int64          `json:"pages_indexed"`
	DiscoveredURLsCount int64          `json:"discovered_urls_count"`
	DomainCount         int64          `json:"domain_count"`
	ActiveJobs          int            `json:"active_jobs"`
	CompletedJobs       int            `json:"completed_jobs"`
	FailedJobs          int            `json:"failed_jobs"`
	WorkersActive       int            `json:"workers_active"`
	LeasesActive        int64          `json:"leases_active"`
	LeasesReclaimed     int64          `json:"leases_reclaimed"`
	ThroughputRPM       float64        `json:"throughput_rpm"`
	AvgLatencyMs        int            `json:"avg_latency_ms"`
	SuccessRate         float64        `json:"success_rate"`
	RobotsBlocked       int64          `json:"robots_blocked"`
	UptimeSec           float64        `json:"uptime_sec"`
	Workers             []WorkerInfo   `json:"workers"`
	RecentPages         []SearchResult `json:"recent_pages"`
	RecentJobs          []CrawlJob     `json:"recent_jobs"`
	DomainBreakdown     []DomainMetric `json:"domain_breakdown"`
	LiveLogs            []CrawlEvent   `json:"live_logs"`
}

// AnalyticsResponse provides backend-aggregated metrics and distributions.
type AnalyticsResponse struct {
	Range              string            `json:"range"`
	TotalRequests      int64             `json:"total_requests"`
	PagesIndexed       int64             `json:"pages_indexed"`
	SuccessCount       int64             `json:"success_count"`
	FailedCount        int64             `json:"failed_count"`
	SuccessRate        float64           `json:"success_rate"`
	RobotsBlocked      int64             `json:"robots_blocked"`
	LeaseReclaims      int64             `json:"lease_reclaims"`
	RetryCount         int64             `json:"retry_count"`
	AvgLatencyMs       int               `json:"avg_latency_ms"`
	P50LatencyMs       int               `json:"p50_latency_ms"`
	P95LatencyMs       int               `json:"p95_latency_ms"`
	StatusBreakdown    []BucketCount     `json:"status_breakdown"`
	ErrorCategories    []BucketCount     `json:"error_categories"`
	DomainDistribution []DomainMetric    `json:"domain_distribution"`
	ThroughputSeries   []ThroughputPoint `json:"throughput_series"`
	WorkerUtilization  []WorkerInfo      `json:"worker_utilization"`
	JobBreakdown       map[string]int    `json:"job_breakdown"`
}

type BucketCount struct {
	Label string `json:"label"`
	Count int64  `json:"count"`
}

type ThroughputPoint struct {
	Bucket string `json:"bucket"`
	Pages  int64  `json:"pages"`
	Errors int64  `json:"errors"`
}

// SubmitSeedsRequest represents custom URLs submitted by the user to start crawling.
type SubmitSeedsRequest struct {
	Name            string   `json:"name,omitempty"`
	URL             string   `json:"url,omitempty"`
	URLs            []string `json:"urls"`
	JobID           string   `json:"job_id,omitempty"`
	Preset          string   `json:"preset,omitempty"`
	MaxDepth        int      `json:"max_depth,omitempty"`
	MaxPages        int      `json:"max_pages,omitempty"`
	Concurrency     int      `json:"concurrency,omitempty"`
	PolitenessMs    int      `json:"politeness_delay_ms,omitempty"`
	TimeoutSec      int      `json:"request_timeout_sec,omitempty"`
	AllowedDomains  []string `json:"allowed_domains,omitempty"`
	ExcludePatterns []string `json:"exclude_patterns,omitempty"`
	UserAgent       string   `json:"user_agent,omitempty"`
}

// SubmitSeedsResponse confirms enqueuing of a crawl job and seeds.
type SubmitSeedsResponse struct {
	Status   string    `json:"status"`
	JobID    string    `json:"job_id"`
	Enqueued int       `json:"enqueued"`
	Job      *CrawlJob `json:"job,omitempty"`
}

// ResetRequest defines tiered reset parameters.
type ResetRequest struct {
	Level              string `json:"level"` // "ui", "job", "queue", "job_data", "data", "dev", "production"
	JobID              string `json:"job_id,omitempty"`
	ConfirmationPhrase string `json:"confirmation_phrase,omitempty"`
}

// ResetResponse confirms reset execution.
type ResetResponse struct {
	Status  string `json:"status"`
	Level   string `json:"level"`
	Message string `json:"message"`
}
