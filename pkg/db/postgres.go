package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"distributed-crawler/pkg/models"

	_ "github.com/lib/pq"
)

// SQL Migration Schema
const schemaSQL = `
CREATE TABLE IF NOT EXISTS domains (
    id SERIAL PRIMARY KEY,
    host VARCHAR(255) UNIQUE NOT NULL,
    total_pages INT DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS crawl_jobs (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'PENDING',
    seed_urls TEXT[] NOT NULL,
    max_depth INT DEFAULT 3,
    max_pages INT DEFAULT 100,
    pages_crawled INT DEFAULT 0,
    pages_failed INT DEFAULT 0,
    error_message TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    completed_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS crawl_seeds (
    id SERIAL PRIMARY KEY,
    job_id VARCHAR(64) REFERENCES crawl_jobs(id) ON DELETE CASCADE,
    url TEXT NOT NULL,
    status VARCHAR(32) DEFAULT 'QUEUED',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS crawled_pages (
    id SERIAL PRIMARY KEY,
    job_id VARCHAR(64),
    domain_id INT REFERENCES domains(id) ON DELETE CASCADE,
    url TEXT UNIQUE NOT NULL,
    canonical_url TEXT,
    title TEXT,
    description TEXT,
    h1 TEXT,
    content_type VARCHAR(128),
    content_length INT DEFAULT 0,
    status_code INT NOT NULL,
    latency_ms INT DEFAULT 0,
    depth INT DEFAULT 0,
    links_count INT DEFAULT 0,
    crawled_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    tsv tsvector
);

CREATE TABLE IF NOT EXISTS discovered_urls (
    id SERIAL PRIMARY KEY,
    job_id VARCHAR(64),
    source_page_id INT REFERENCES crawled_pages(id) ON DELETE CASCADE,
    url TEXT NOT NULL,
    domain VARCHAR(255) NOT NULL,
    depth INT DEFAULT 1,
    status VARCHAR(32) DEFAULT 'QUEUED',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS crawl_errors (
    id SERIAL PRIMARY KEY,
    job_id VARCHAR(64),
    url TEXT NOT NULL,
    worker_id VARCHAR(128),
    status_code INT,
    error_type VARCHAR(64) NOT NULL,
    error_message TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS worker_registry (
    id VARCHAR(128) PRIMARY KEY,
    hostname VARCHAR(255) NOT NULL,
    version VARCHAR(32) DEFAULT '1.0.0',
    status VARCHAR(32) NOT NULL DEFAULT 'ACTIVE',
    tasks_active INT DEFAULT 0,
    tasks_completed INT DEFAULT 0,
    tasks_failed INT DEFAULT 0,
    avg_latency_ms INT DEFAULT 0,
    started_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_heartbeat TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS crawl_events (
    id SERIAL PRIMARY KEY,
    job_id VARCHAR(64),
    worker_id VARCHAR(128),
    event_type VARCHAR(64) NOT NULL,
    url TEXT,
    status_code INT,
    latency_ms INT,
    message TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS domain_statistics (
    id SERIAL PRIMARY KEY,
    domain VARCHAR(255) UNIQUE NOT NULL,
    total_pages INT DEFAULT 0,
    total_bytes BIGINT DEFAULT 0,
    avg_latency_ms INT DEFAULT 0,
    error_count INT DEFAULT 0,
    last_crawled_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS crawl_settings (
    key VARCHAR(128) PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Indexes for performant lookup
CREATE INDEX IF NOT EXISTS idx_crawled_pages_crawled_at ON crawled_pages(crawled_at);
CREATE INDEX IF NOT EXISTS idx_crawled_pages_domain_id ON crawled_pages(domain_id);
CREATE INDEX IF NOT EXISTS idx_crawled_pages_job_id ON crawled_pages(job_id);
CREATE INDEX IF NOT EXISTS idx_crawled_pages_status_code ON crawled_pages(status_code);
CREATE INDEX IF NOT EXISTS idx_crawled_pages_tsv ON crawled_pages USING GIN(tsv);
CREATE INDEX IF NOT EXISTS idx_crawl_events_created_at ON crawl_events(created_at);
`

// PostgresRepository manages database connection and queries.
type PostgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository establishes a connection pool to PostgreSQL and initializes schemas.
func NewPostgresRepository(connStr string) (*PostgresRepository, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open postgres connection: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping postgres: %w", err)
	}

	repo := &PostgresRepository{db: db}

	if err := repo.initSchema(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	log.Println("✅ PostgreSQL connection pool initialized and schemas verified.")
	return repo, nil
}

// Close closes the database connection pool.
func (r *PostgresRepository) Close() error {
	return r.db.Close()
}

// initSchema executes migrations and column upgrades idempotently.
func (r *PostgresRepository) initSchema(ctx context.Context) error {
	if _, err := r.db.ExecContext(ctx, schemaSQL); err != nil {
		return err
	}

	// Idempotent column additions for existing tables
	alterQueries := []string{
		"ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS canonical_url TEXT;",
		"ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS h1 TEXT;",
		"ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS content_type VARCHAR(128);",
		"ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS content_length INT DEFAULT 0;",
		"ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS latency_ms INT DEFAULT 0;",
		"ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS depth INT DEFAULT 0;",
		"ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS links_count INT DEFAULT 0;",
		"ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS job_id VARCHAR(64);",
		"ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS tsv tsvector;",
		"CREATE INDEX IF NOT EXISTS idx_crawled_pages_tsv ON crawled_pages USING GIN(tsv);",
	}

	for _, q := range alterQueries {
		_, _ = r.db.ExecContext(ctx, q)
	}

	return nil
}

// SavePage persists a crawled page with full metadata and updates full-text search vectors.
func (r *PostgresRepository) SavePage(ctx context.Context, pageURL, title, description string, statusCode int) (int64, error) {
	return r.SavePageFull(ctx, models.ReportRequest{
		URL:         pageURL,
		Title:       title,
		Description: description,
		StatusCode:  statusCode,
	})
}

// SavePageFull persists complete page metadata, updates domain stats, and generates full-text TSVector.
func (r *PostgresRepository) SavePageFull(ctx context.Context, req models.ReportRequest) (int64, error) {
	parsedURL, err := url.Parse(req.URL)
	if err != nil {
		return 0, fmt.Errorf("invalid URL parsed during database save: %w", err)
	}
	host := strings.ToLower(parsedURL.Host)
	if host == "" {
		return 0, fmt.Errorf("missing host in URL: %s", req.URL)
	}

	// 1. Upsert Domain
	var domainID int64
	queryDomain := `
		INSERT INTO domains (host, total_pages, updated_at) 
		VALUES ($1, 1, NOW()) 
		ON CONFLICT (host) DO UPDATE SET 
			total_pages = domains.total_pages + 1,
			updated_at = NOW()
		RETURNING id;
	`
	err = r.db.QueryRowContext(ctx, queryDomain, host).Scan(&domainID)
	if err != nil {
		return 0, fmt.Errorf("failed to upsert domain: %w", err)
	}

	// 2. Upsert Crawled Page with Full-Text Vector
	var pageID int64
	queryPage := `
		INSERT INTO crawled_pages (
			domain_id, job_id, url, canonical_url, title, description, h1, 
			content_type, content_length, status_code, latency_ms, depth, links_count, crawled_at, tsv
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, 
			$8, $9, $10, $11, $12, $13, NOW(),
			setweight(to_tsvector('english', COALESCE($5, '')), 'A') ||
			setweight(to_tsvector('english', COALESCE($7, '')), 'B') ||
			setweight(to_tsvector('english', COALESCE($6, '')), 'C') ||
			setweight(to_tsvector('simple', COALESCE($3, '')), 'D')
		)
		ON CONFLICT (url) DO UPDATE SET 
			title = EXCLUDED.title,
			description = EXCLUDED.description,
			canonical_url = EXCLUDED.canonical_url,
			h1 = EXCLUDED.h1,
			content_type = EXCLUDED.content_type,
			content_length = EXCLUDED.content_length,
			status_code = EXCLUDED.status_code,
			latency_ms = EXCLUDED.latency_ms,
			depth = EXCLUDED.depth,
			links_count = EXCLUDED.links_count,
			crawled_at = NOW(),
			tsv = EXCLUDED.tsv
		RETURNING id;
	`
	err = r.db.QueryRowContext(ctx, queryPage,
		domainID,
		sqlNullString(req.JobID),
		req.URL,
		sqlNullString(req.CanonicalURL),
		req.Title,
		req.Description,
		sqlNullString(req.H1),
		sqlNullString(req.ContentType),
		req.ContentLength,
		req.StatusCode,
		req.LatencyMs,
		req.Depth,
		len(req.Links),
	).Scan(&pageID)

	if err != nil {
		return 0, fmt.Errorf("failed to upsert crawled page: %w", err)
	}

	// 3. Upsert Domain Statistics
	queryDomainStats := `
		INSERT INTO domain_statistics (domain, total_pages, total_bytes, avg_latency_ms, error_count, last_crawled_at)
		VALUES ($1, 1, $2, $3, CASE WHEN $4 >= 400 THEN 1 ELSE 0 END, NOW())
		ON CONFLICT (domain) DO UPDATE SET
			total_pages = domain_statistics.total_pages + 1,
			total_bytes = domain_statistics.total_bytes + EXCLUDED.total_bytes,
			avg_latency_ms = (domain_statistics.avg_latency_ms + EXCLUDED.avg_latency_ms) / 2,
			error_count = domain_statistics.error_count + CASE WHEN EXCLUDED.error_count > 0 THEN 1 ELSE 0 END,
			last_crawled_at = NOW();
	`
	_, _ = r.db.ExecContext(ctx, queryDomainStats, host, req.ContentLength, req.LatencyMs, req.StatusCode)

	return pageID, nil
}

// GetPageByURL retrieves a page record from PostgreSQL.
func (r *PostgresRepository) GetPageByURL(ctx context.Context, pageURL string) (string, string, int, error) {
	query := `SELECT title, description, status_code FROM crawled_pages WHERE url = $1;`
	var title, description string
	var statusCode int
	err := r.db.QueryRowContext(ctx, query, pageURL).Scan(&title, &description, &statusCode)
	if err != nil {
		return "", "", 0, err
	}
	return title, description, statusCode, nil
}

// GetPageByID retrieves a full page record by primary key ID.
func (r *PostgresRepository) GetPageByID(ctx context.Context, id int64) (*models.SearchResult, error) {
	query := `
		SELECT p.id, COALESCE(p.job_id, ''), p.url, COALESCE(p.canonical_url, ''),
		       d.host, COALESCE(p.title, ''), COALESCE(p.description, ''), COALESCE(p.h1, ''),
		       COALESCE(p.content_type, ''), p.content_length, p.status_code, p.latency_ms,
		       p.depth, p.links_count, p.crawled_at
		FROM crawled_pages p
		LEFT JOIN domains d ON p.domain_id = d.id
		WHERE p.id = $1;
	`
	var res models.SearchResult
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&res.ID, &res.JobID, &res.URL, &res.CanonicalURL,
		&res.Domain, &res.Title, &res.Description, &res.H1,
		&res.ContentType, &res.ContentLength, &res.StatusCode, &res.LatencyMs,
		&res.Depth, &res.LinksCount, &res.CrawledAt,
	)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// SearchPages executes hybrid Full-Text and substring search over crawled content.
func (r *PostgresRepository) SearchPages(ctx context.Context, keyword string) ([]models.SearchResult, error) {
	return r.SearchPagesAdvanced(ctx, keyword, "", 0, 30, 0)
}

// SearchPagesAdvanced executes full-text search with filtering, pagination, and ranking.
func (r *PostgresRepository) SearchPagesAdvanced(ctx context.Context, keyword, domainFilter string, statusCodeFilter, limit, offset int) ([]models.SearchResult, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}

	var query string
	var rows *sql.Rows
	var err error

	hasKeyword := strings.TrimSpace(keyword) != ""

	if hasKeyword {
		query = `
			SELECT p.id, COALESCE(p.job_id, ''), p.url, COALESCE(p.canonical_url, ''),
			       d.host, COALESCE(p.title, ''), COALESCE(p.description, ''), COALESCE(p.h1, ''),
			       COALESCE(p.content_type, ''), p.content_length, p.status_code, p.latency_ms,
			       p.depth, p.links_count, p.crawled_at,
			       ts_rank(p.tsv, plainto_tsquery('english', $1)) AS rank
			FROM crawled_pages p
			LEFT JOIN domains d ON p.domain_id = d.id
			WHERE (p.tsv @@ plainto_tsquery('english', $1) OR p.url ILIKE '%' || $1 || '%')
			  AND ($2 = '' OR d.host = $2)
			  AND ($3 = 0 OR p.status_code = $3)
			ORDER BY rank DESC, p.crawled_at DESC
			LIMIT $4 OFFSET $5;
		`
		rows, err = r.db.QueryContext(ctx, query, keyword, domainFilter, statusCodeFilter, limit, offset)
	} else {
		query = `
			SELECT p.id, COALESCE(p.job_id, ''), p.url, COALESCE(p.canonical_url, ''),
			       d.host, COALESCE(p.title, ''), COALESCE(p.description, ''), COALESCE(p.h1, ''),
			       COALESCE(p.content_type, ''), p.content_length, p.status_code, p.latency_ms,
			       p.depth, p.links_count, p.crawled_at, 0.0 AS rank
			FROM crawled_pages p
			LEFT JOIN domains d ON p.domain_id = d.id
			WHERE ($1 = '' OR d.host = $1)
			  AND ($2 = 0 OR p.status_code = $2)
			ORDER BY p.crawled_at DESC
			LIMIT $3 OFFSET $4;
		`
		rows, err = r.db.QueryContext(ctx, query, domainFilter, statusCodeFilter, limit, offset)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to search pages: %w", err)
	}
	defer rows.Close()

	var results []models.SearchResult
	for rows.Next() {
		var res models.SearchResult
		err := rows.Scan(
			&res.ID, &res.JobID, &res.URL, &res.CanonicalURL,
			&res.Domain, &res.Title, &res.Description, &res.H1,
			&res.ContentType, &res.ContentLength, &res.StatusCode, &res.LatencyMs,
			&res.Depth, &res.LinksCount, &res.CrawledAt, &res.Relevance,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan search result row: %w", err)
		}
		results = append(results, res)
	}

	return results, nil
}

// GetRecentPages retrieves the latest crawled pages up to the specified limit.
func (r *PostgresRepository) GetRecentPages(ctx context.Context, limit int) ([]models.SearchResult, error) {
	return r.SearchPagesAdvanced(ctx, "", "", 0, limit, 0)
}

// GetDomainCount returns the total number of unique hosts registered in domains table.
func (r *PostgresRepository) GetDomainCount(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM domains;").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count domains: %w", err)
	}
	return count, nil
}

// GetDomainMetrics retrieves aggregate statistics per domain.
func (r *PostgresRepository) GetDomainMetrics(ctx context.Context, limit int) ([]models.DomainMetric, error) {
	if limit <= 0 {
		limit = 10
	}
	query := `
		SELECT host, total_pages, 0, 0, updated_at
		FROM domains
		ORDER BY total_pages DESC
		LIMIT $1;
	`
	rows, err := r.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var metrics []models.DomainMetric
	for rows.Next() {
		var m models.DomainMetric
		if err := rows.Scan(&m.Domain, &m.TotalPages, &m.TotalBytes, &m.AvgLatencyMs, &m.LastCrawledAt); err != nil {
			return nil, err
		}
		metrics = append(metrics, m)
	}
	return metrics, nil
}

// CreateJob persists a new crawl job and associated seeds.
func (r *PostgresRepository) CreateJob(ctx context.Context, job models.CrawlJob) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	queryJob := `
		INSERT INTO crawl_jobs (id, name, status, seed_urls, max_depth, max_pages, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW());
	`
	_, err = tx.ExecContext(ctx, queryJob, job.ID, job.Name, job.Status, job.SeedURLs, job.MaxDepth, job.MaxPages)
	if err != nil {
		return fmt.Errorf("failed to insert crawl job: %w", err)
	}

	for _, seed := range job.SeedURLs {
		querySeed := `INSERT INTO crawl_seeds (job_id, url, status) VALUES ($1, $2, 'QUEUED');`
		if _, err := tx.ExecContext(ctx, querySeed, job.ID, seed); err != nil {
			return fmt.Errorf("failed to insert crawl seed: %w", err)
		}
	}

	return tx.Commit()
}

// GetJob retrieves a crawl job by ID.
func (r *PostgresRepository) GetJob(ctx context.Context, id string) (*models.CrawlJob, error) {
	query := `
		SELECT id, name, status, seed_urls, max_depth, max_pages, pages_crawled, pages_failed, 
		       COALESCE(error_message, ''), created_at, updated_at, completed_at
		FROM crawl_jobs
		WHERE id = $1;
	`
	var job models.CrawlJob
	var seedURLs []string
	var completedAt sql.NullTime

	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&job.ID, &job.Name, &job.Status, &seedURLs, &job.MaxDepth, &job.MaxPages,
		&job.PagesCrawled, &job.PagesFailed, &job.ErrorMessage,
		&job.CreatedAt, &job.UpdatedAt, &completedAt,
	)
	if err != nil {
		return nil, err
	}
	job.SeedURLs = seedURLs
	if completedAt.Valid {
		job.CompletedAt = &completedAt.Time
	}
	return &job, nil
}

// ListJobs retrieves all crawl jobs sorted by creation date.
func (r *PostgresRepository) ListJobs(ctx context.Context, limit, offset int) ([]models.CrawlJob, error) {
	if limit <= 0 {
		limit = 20
	}
	query := `
		SELECT id, name, status, seed_urls, max_depth, max_pages, pages_crawled, pages_failed,
		       COALESCE(error_message, ''), created_at, updated_at, completed_at
		FROM crawl_jobs
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2;
	`
	rows, err := r.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []models.CrawlJob
	for rows.Next() {
		var job models.CrawlJob
		var seedURLs []string
		var completedAt sql.NullTime
		if err := rows.Scan(
			&job.ID, &job.Name, &job.Status, &seedURLs, &job.MaxDepth, &job.MaxPages,
			&job.PagesCrawled, &job.PagesFailed, &job.ErrorMessage,
			&job.CreatedAt, &job.UpdatedAt, &completedAt,
		); err != nil {
			return nil, err
		}
		job.SeedURLs = seedURLs
		if completedAt.Valid {
			job.CompletedAt = &completedAt.Time
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

// UpdateJobStatus modifies job status and handles completion timestamp.
func (r *PostgresRepository) UpdateJobStatus(ctx context.Context, id, status, errMessage string) error {
	var query string
	if status == models.JobStateCompleted || status == models.JobStateFailed || status == models.JobStateCancelled {
		query = `
			UPDATE crawl_jobs 
			SET status = $2, error_message = $3, updated_at = NOW(), completed_at = NOW()
			WHERE id = $1;
		`
	} else {
		query = `
			UPDATE crawl_jobs 
			SET status = $2, error_message = $3, updated_at = NOW()
			WHERE id = $1;
		`
	}
	_, err := r.db.ExecContext(ctx, query, id, status, errMessage)
	return err
}

// RegisterWorker updates or inserts an active worker in the registry.
func (r *PostgresRepository) RegisterWorker(ctx context.Context, w models.WorkerInfo) error {
	query := `
		INSERT INTO worker_registry (id, hostname, version, status, started_at, last_heartbeat)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
		ON CONFLICT (id) DO UPDATE SET
			hostname = EXCLUDED.hostname,
			version = EXCLUDED.version,
			status = EXCLUDED.status,
			last_heartbeat = NOW();
	`
	_, err := r.db.ExecContext(ctx, query, w.ID, w.Hostname, w.Version, w.Status)
	return err
}

// UpdateWorkerHeartbeat updates live worker telemetry metrics.
func (r *PostgresRepository) UpdateWorkerHeartbeat(ctx context.Context, req models.HeartbeatRequest) error {
	query := `
		UPDATE worker_registry
		SET status = $2, tasks_active = $3, tasks_completed = $4, tasks_failed = $5,
		    avg_latency_ms = $6, last_heartbeat = NOW()
		WHERE id = $1;
	`
	_, err := r.db.ExecContext(ctx, query, req.WorkerID, req.Status, req.TasksActive, req.TasksCompleted, req.TasksFailed, req.AvgLatencyMs)
	return err
}

// ListWorkers returns active workers and flags stale workers older than 15 seconds.
func (r *PostgresRepository) ListWorkers(ctx context.Context) ([]models.WorkerInfo, error) {
	query := `
		SELECT id, hostname, version, status, tasks_active, tasks_completed, tasks_failed,
		       avg_latency_ms, EXTRACT(EPOCH FROM (NOW() - last_heartbeat)) AS sec_ago, last_heartbeat
		FROM worker_registry
		ORDER BY last_heartbeat DESC;
	`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workers []models.WorkerInfo
	for rows.Next() {
		var w models.WorkerInfo
		if err := rows.Scan(
			&w.ID, &w.Hostname, &w.Version, &w.Status, &w.TasksActive,
			&w.TasksCompleted, &w.TasksFailed, &w.AvgLatencyMs, &w.LastSeenSecAgo, &w.LastHeartbeat,
		); err != nil {
			return nil, err
		}
		if w.LastSeenSecAgo > 15.0 && w.Status != models.WorkerStateStopped {
			w.Status = models.WorkerStateUnhealthy
		}
		workers = append(workers, w)
	}
	return workers, nil
}

// RecordEvent records an auditable crawl activity event in the log.
func (r *PostgresRepository) RecordEvent(ctx context.Context, e models.CrawlEvent) error {
	query := `
		INSERT INTO crawl_events (job_id, worker_id, event_type, url, status_code, latency_ms, message, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW());
	`
	_, err := r.db.ExecContext(ctx, query,
		sqlNullString(e.JobID),
		sqlNullString(e.WorkerID),
		e.EventType,
		sqlNullString(e.URL),
		e.StatusCode,
		e.LatencyMs,
		e.Message,
	)
	return err
}

// GetRecentEvents retrieves the latest activity events for telemetry feeds.
func (r *PostgresRepository) GetRecentEvents(ctx context.Context, limit int) ([]models.CrawlEvent, error) {
	if limit <= 0 {
		limit = 20
	}
	query := `
		SELECT id, COALESCE(job_id, ''), COALESCE(worker_id, ''), event_type,
		       COALESCE(url, ''), COALESCE(status_code, 0), COALESCE(latency_ms, 0),
		       COALESCE(message, ''), created_at
		FROM crawl_events
		ORDER BY created_at DESC
		LIMIT $1;
	`
	rows, err := r.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []models.CrawlEvent
	for rows.Next() {
		var e models.CrawlEvent
		if err := rows.Scan(
			&e.ID, &e.JobID, &e.WorkerID, &e.EventType,
			&e.URL, &e.StatusCode, &e.LatencyMs, &e.Message, &e.CreatedAt,
		); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, nil
}

// Truncate clears pages, domains, events, and jobs for development testing.
func (r *PostgresRepository) Truncate(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, "TRUNCATE TABLE crawled_pages, domains, crawl_events, crawl_errors, crawl_seeds, crawl_jobs, domain_statistics RESTART IDENTITY CASCADE;")
	return err
}

// TruncateData clears crawled data while preserving jobs and workers.
func (r *PostgresRepository) TruncateData(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, "TRUNCATE TABLE crawled_pages, domains, domain_statistics, crawl_errors RESTART IDENTITY CASCADE;")
	return err
}

func sqlNullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: s, Valid: true}
}
