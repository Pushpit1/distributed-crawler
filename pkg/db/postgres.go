package db

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"distributed-crawler/pkg/models"

	"github.com/lib/pq"
)

//go:embed migrations/001_initial_schema.sql
var initialSchemaSQL string

// PostgresRepository manages the relational database pool, migrations, and queries.
type PostgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository establishes a connection pool to PostgreSQL and executes idempotent migrations.
func NewPostgresRepository(connStr string) (*PostgresRepository, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open postgres connection: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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

	log.Println("✅ PostgreSQL connection pool initialized and migrations verified.")
	return repo, nil
}

// Close closes the database connection pool cleanly.
func (r *PostgresRepository) Close() error {
	return r.db.Close()
}

// Ping verifies live PostgreSQL database connectivity.
func (r *PostgresRepository) Ping(ctx context.Context) error {
	return r.db.PingContext(ctx)
}

// initSchema executes versioned migrations and safe idempotent column evolution serialized via advisory lock.
func (r *PostgresRepository) initSchema(ctx context.Context) error {
	// Pin one session connection so pg_advisory_lock, DDL statements, and pg_advisory_unlock share the same PostgreSQL backend PID.
	var execFn = func(query string, args ...any) (sql.Result, error) {
		return r.db.ExecContext(ctx, query, args...)
	}
	if conn, lockErr := r.db.Conn(ctx); lockErr == nil {
		if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock(78493021);"); err == nil {
			execFn = func(query string, args ...any) (sql.Result, error) {
				return conn.ExecContext(ctx, query, args...)
			}
			defer func() {
				_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock(78493021);")
				_ = conn.Close()
			}()
		} else {
			_ = conn.Close()
		}
	}

	if _, err := execFn(initialSchemaSQL); err != nil {
		return err
	}

	// Safe column additions for upgrades from legacy schemas
	alterQueries := []string{
		"ALTER TABLE crawl_jobs ADD COLUMN IF NOT EXISTS concurrency INT DEFAULT 4;",
		"ALTER TABLE crawl_jobs ADD COLUMN IF NOT EXISTS politeness_ms INT DEFAULT 1000;",
		"ALTER TABLE crawl_jobs ADD COLUMN IF NOT EXISTS allowed_domains TEXT[] DEFAULT '{}';",
		"ALTER TABLE crawl_jobs ADD COLUMN IF NOT EXISTS exclude_patterns TEXT[] DEFAULT '{}';",
		"ALTER TABLE crawl_jobs ADD COLUMN IF NOT EXISTS discovered_count INT DEFAULT 0;",
		"ALTER TABLE crawl_jobs ADD COLUMN IF NOT EXISTS current_depth INT DEFAULT 0;",
		"ALTER TABLE crawl_jobs ADD COLUMN IF NOT EXISTS started_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP;",

		"ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS final_url TEXT;",
		"ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS canonical_url TEXT;",
		"ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS h1 TEXT;",
		"ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS headings TEXT[] DEFAULT '{}';",
		"ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS extracted_text TEXT;",
		"ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS content_type VARCHAR(128);",
		"ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS content_length INT DEFAULT 0;",
		"ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS latency_ms INT DEFAULT 0;",
		"ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS depth INT DEFAULT 0;",
		"ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS links_count INT DEFAULT 0;",
		"ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS internal_links TEXT[] DEFAULT '{}';",
		"ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS external_links TEXT[] DEFAULT '{}';",
		"ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS error_message TEXT;",
		"ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS job_id VARCHAR(64);",
		"ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS tsv tsvector;",
		"CREATE INDEX IF NOT EXISTS idx_crawled_pages_tsv ON crawled_pages USING GIN(tsv);",

		"ALTER TABLE worker_registry ADD COLUMN IF NOT EXISTS current_task TEXT;",
		"ALTER TABLE worker_registry ADD COLUMN IF NOT EXISTS uptime_sec BIGINT DEFAULT 0;",
		"ALTER TABLE crawl_events ADD COLUMN IF NOT EXISTS severity VARCHAR(16) DEFAULT 'INFO';",
	}

	for _, q := range alterQueries {
		if _, err := execFn(q); err != nil {
			log.Printf("⚠️ Non-fatal schema upgrade note on %q: %v\n", q, err)
		}
	}

	_, _ = execFn(
		`INSERT INTO schema_migrations (version) VALUES ('001_initial_schema') ON CONFLICT (version) DO NOTHING;`)

	// Ensure default settings rows exist cleanly
	defaultSettings := map[string]string{
		"default_politeness_ms": "1000",
		"default_max_depth":     "3",
		"default_max_pages":     "100",
		"default_concurrency":   "4",
		"lease_timeout_sec":     "60",
	}
	for k, v := range defaultSettings {
		_, _ = execFn(
			`INSERT INTO crawl_settings (key, value) VALUES ($1, $2) ON CONFLICT (key) DO NOTHING;`, k, v)
	}

	return nil
}

// SavePage persists a basic page record (backwards compatible helper).
func (r *PostgresRepository) SavePage(ctx context.Context, pageURL, title, description string, statusCode int) (int64, error) {
	return r.SavePageFull(ctx, models.ReportRequest{
		URL:         pageURL,
		Title:       title,
		Description: description,
		StatusCode:  statusCode,
	})
}

// SavePageFull persists page metadata, domain stats, discovered URLs, error records, and updates job counters inside one transaction.
func (r *PostgresRepository) SavePageFull(ctx context.Context, req models.ReportRequest) (int64, error) {
	parsedURL, err := url.Parse(req.URL)
	if err != nil {
		return 0, fmt.Errorf("invalid URL parsed during database save: %w", err)
	}
	host := strings.ToLower(parsedURL.Host)
	if host == "" {
		return 0, fmt.Errorf("missing host in URL: %s", req.URL)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

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
	if err = tx.QueryRowContext(ctx, queryDomain, host).Scan(&domainID); err != nil {
		return 0, fmt.Errorf("failed to upsert domain: %w", err)
	}

	headings := req.Headings
	if headings == nil {
		headings = []string{}
	}
	internalLinks := req.InternalLinks
	if internalLinks == nil {
		internalLinks = []string{}
	}
	externalLinks := req.ExternalLinks
	if externalLinks == nil {
		externalLinks = []string{}
	}
	linksCount := len(req.Links)
	if linksCount == 0 {
		linksCount = len(internalLinks) + len(externalLinks)
	}

	// 2. Upsert Crawled Page with weighted FTS tsvector
	var pageID int64
	queryPage := `
		INSERT INTO crawled_pages (
			domain_id, job_id, url, final_url, canonical_url, title, description, h1, headings, extracted_text,
			content_type, content_length, status_code, latency_ms, depth, links_count, internal_links, external_links, error_message, crawled_at, tsv
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
			$11, $12, $13, $14, $15, $16, $17, $18, $19, NOW(),
			setweight(to_tsvector('english', COALESCE($6, '')), 'A') ||
			setweight(to_tsvector('english', COALESCE($8, '')), 'B') ||
			setweight(to_tsvector('english', COALESCE($7, '')), 'B') ||
			setweight(to_tsvector('english', COALESCE($10, '')), 'C') ||
			setweight(to_tsvector('simple', COALESCE($3, '')), 'D')
		)
		ON CONFLICT (url) DO UPDATE SET 
			job_id = COALESCE(EXCLUDED.job_id, crawled_pages.job_id),
			final_url = EXCLUDED.final_url,
			canonical_url = EXCLUDED.canonical_url,
			title = EXCLUDED.title,
			description = EXCLUDED.description,
			h1 = EXCLUDED.h1,
			headings = EXCLUDED.headings,
			extracted_text = EXCLUDED.extracted_text,
			content_type = EXCLUDED.content_type,
			content_length = EXCLUDED.content_length,
			status_code = EXCLUDED.status_code,
			latency_ms = EXCLUDED.latency_ms,
			depth = EXCLUDED.depth,
			links_count = EXCLUDED.links_count,
			internal_links = EXCLUDED.internal_links,
			external_links = EXCLUDED.external_links,
			error_message = EXCLUDED.error_message,
			crawled_at = NOW(),
			tsv = EXCLUDED.tsv
		RETURNING id;
	`
	err = tx.QueryRowContext(ctx, queryPage,
		domainID,
		sqlNullString(req.JobID),
		req.URL,
		sqlNullString(req.FinalURL),
		sqlNullString(req.CanonicalURL),
		req.Title,
		req.Description,
		sqlNullString(req.H1),
		pq.Array(headings),
		sqlNullString(req.ExtractedText),
		sqlNullString(req.ContentType),
		req.ContentLength,
		req.StatusCode,
		req.LatencyMs,
		req.Depth,
		linksCount,
		pq.Array(internalLinks),
		pq.Array(externalLinks),
		sqlNullString(req.ErrorMessage),
	).Scan(&pageID)
	if err != nil {
		return 0, fmt.Errorf("failed to upsert crawled page: %w", err)
	}

	// 3. Upsert Domain Statistics with weighted running average latency
	queryDomainStats := `
		INSERT INTO domain_statistics (domain, total_pages, total_bytes, avg_latency_ms, error_count, last_crawled_at)
		VALUES ($1, 1, $2, $3, CASE WHEN $4 >= 400 OR $4 = 0 THEN 1 ELSE 0 END, NOW())
		ON CONFLICT (domain) DO UPDATE SET
			total_pages = domain_statistics.total_pages + 1,
			total_bytes = domain_statistics.total_bytes + EXCLUDED.total_bytes,
			avg_latency_ms = ((domain_statistics.avg_latency_ms * domain_statistics.total_pages) + EXCLUDED.avg_latency_ms) / (domain_statistics.total_pages + 1),
			error_count = domain_statistics.error_count + CASE WHEN EXCLUDED.error_count > 0 THEN 1 ELSE 0 END,
			last_crawled_at = NOW();
	`
	if _, err = tx.ExecContext(ctx, queryDomainStats, host, req.ContentLength, req.LatencyMs, req.StatusCode); err != nil {
		return 0, fmt.Errorf("failed updating domain_statistics: %w", err)
	}

	// 4. Record discovered URLs (cap insert batch to avoid huge transactions on hubs)
	discoveredSample := req.Links
	if len(discoveredSample) > 100 {
		discoveredSample = discoveredSample[:100]
	}
	for _, link := range discoveredSample {
		if linkParsed, lErr := url.Parse(link); lErr == nil && linkParsed.Host != "" {
			_, _ = tx.ExecContext(ctx,
				`INSERT INTO discovered_urls (job_id, source_page_id, url, domain, depth, status) VALUES ($1, $2, $3, $4, $5, 'QUEUED');`,
				sqlNullString(req.JobID), pageID, link, strings.ToLower(linkParsed.Host), req.Depth+1)
		}
	}

	// 5. Record error if non-200 or explicit ErrorMessage
	isError := req.StatusCode >= 400 || req.StatusCode == 0 || req.ErrorMessage != ""
	if isError {
		errType := req.ErrorType
		if errType == "" {
			switch {
			case req.RobotsBlocked:
				errType = "ROBOTS_DISALLOWED"
			case req.StatusCode >= 500:
				errType = "HTTP_5XX"
			case req.StatusCode >= 400:
				errType = "HTTP_4XX"
			default:
				errType = "NETWORK_OR_PARSE"
			}
		}
		errMsg := req.ErrorMessage
		if errMsg == "" {
			errMsg = fmt.Sprintf("HTTP status %d", req.StatusCode)
		}
		_, _ = tx.ExecContext(ctx,
			`INSERT INTO crawl_errors (job_id, url, worker_id, status_code, error_type, error_message) VALUES ($1, $2, $3, $4, $5, $6);`,
			sqlNullString(req.JobID), req.URL, sqlNullString(req.WorkerID), req.StatusCode, errType, errMsg)
	}

	// 6. Update Job progress counters if JobID provided
	if req.JobID != "" {
		failedInc := 0
		crawledInc := 1
		if isError {
			failedInc = 1
		}
		_, _ = tx.ExecContext(ctx, `
			UPDATE crawl_jobs SET
				pages_crawled = pages_crawled + $2,
				pages_failed = pages_failed + $3,
				discovered_count = discovered_count + $4,
				current_depth = GREATEST(current_depth, $5),
				updated_at = NOW(),
				status = CASE WHEN status = 'RUNNING' AND (pages_crawled + $2) >= max_pages THEN 'COMPLETED' ELSE status END,
				completed_at = CASE WHEN status = 'RUNNING' AND (pages_crawled + $2) >= max_pages THEN NOW() ELSE completed_at END
			WHERE id = $1;
		`, req.JobID, crawledInc, failedInc, len(req.Links), req.Depth)
	}

	if err = tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed committing page transaction: %w", err)
	}

	return pageID, nil
}

// GetPageByURL retrieves basic page fields by URL (backwards compatible).
func (r *PostgresRepository) GetPageByURL(ctx context.Context, pageURL string) (string, string, int, error) {
	query := `SELECT COALESCE(title, ''), COALESCE(description, ''), status_code FROM crawled_pages WHERE url = $1;`
	var title, description string
	var statusCode int
	err := r.db.QueryRowContext(ctx, query, pageURL).Scan(&title, &description, &statusCode)
	if err != nil {
		return "", "", 0, err
	}
	return title, description, statusCode, nil
}

// GetPageByID retrieves full rich metadata for a crawled page by primary key ID.
func (r *PostgresRepository) GetPageByID(ctx context.Context, id int64) (*models.SearchResult, error) {
	query := `
		SELECT p.id, COALESCE(p.job_id, ''), p.url, COALESCE(p.final_url, ''), COALESCE(p.canonical_url, ''),
		       COALESCE(d.host, ''), COALESCE(p.title, ''), COALESCE(p.description, ''), COALESCE(p.h1, ''),
		       COALESCE(p.headings, '{}'), COALESCE(p.extracted_text, ''),
		       COALESCE(p.content_type, ''), p.content_length, p.status_code, p.latency_ms,
		       p.depth, p.links_count, COALESCE(p.internal_links, '{}'), COALESCE(p.external_links, '{}'),
		       COALESCE(p.error_message, ''), p.crawled_at
		FROM crawled_pages p
		LEFT JOIN domains d ON p.domain_id = d.id
		WHERE p.id = $1;
	`
	var res models.SearchResult
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&res.ID, &res.JobID, &res.URL, &res.FinalURL, &res.CanonicalURL,
		&res.Domain, &res.Title, &res.Description, &res.H1,
		pq.Array(&res.Headings), &res.ExtractedText,
		&res.ContentType, &res.ContentLength, &res.StatusCode, &res.LatencyMs,
		&res.Depth, &res.LinksCount, pq.Array(&res.InternalLinks), pq.Array(&res.ExternalLinks),
		&res.ErrorMessage, &res.CrawledAt,
	)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// SearchPages executes default keyword search (backwards compatible).
func (r *PostgresRepository) SearchPages(ctx context.Context, keyword string) ([]models.SearchResult, error) {
	results, _, err := r.SearchPagesWithTotal(ctx, keyword, "", "", "", "", "relevance", 30, 0)
	return results, err
}

// SearchPagesAdvanced executes FTS search with domain and exact status filter.
func (r *PostgresRepository) SearchPagesAdvanced(ctx context.Context, keyword, domainFilter string, statusCodeFilter, limit, offset int) ([]models.SearchResult, error) {
	statusStr := ""
	if statusCodeFilter > 0 {
		statusStr = fmt.Sprintf("%d", statusCodeFilter)
	}
	results, _, err := r.SearchPagesWithTotal(ctx, keyword, domainFilter, statusStr, "", "", "relevance", limit, offset)
	return results, err
}

// SearchPagesWithTotal executes ranked FTS search with filtering, sorting, date windows, and pagination total count.
func (r *PostgresRepository) SearchPagesWithTotal(
	ctx context.Context,
	keyword, domainFilter, statusFilter, dateFrom, dateTo, sortBy string,
	limit, offset int,
) ([]models.SearchResult, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 30
	}
	if offset < 0 {
		offset = 0
	}

	var conditions []string
	var args []interface{}
	argIdx := 1

	hasKeyword := strings.TrimSpace(keyword) != ""
	rankExpr := "0.0::float8"

	if hasKeyword {
		kwPlaceholder := fmt.Sprintf("$%d", argIdx)
		args = append(args, strings.TrimSpace(keyword))
		argIdx++
		rankExpr = fmt.Sprintf("ts_rank(p.tsv, plainto_tsquery('english', %s))", kwPlaceholder)
		conditions = append(conditions, fmt.Sprintf(
			"(p.tsv @@ plainto_tsquery('english', %s) OR p.url ILIKE '%%' || %s || '%%' OR d.host ILIKE '%%' || %s || '%%')",
			kwPlaceholder, kwPlaceholder, kwPlaceholder,
		))
	}

	if strings.TrimSpace(domainFilter) != "" {
		conditions = append(conditions, fmt.Sprintf("d.host = $%d", argIdx))
		args = append(args, strings.ToLower(strings.TrimSpace(domainFilter)))
		argIdx++
	}

	switch strings.ToLower(strings.TrimSpace(statusFilter)) {
	case "", "0", "all":
	case "2xx":
		conditions = append(conditions, "p.status_code >= 200 AND p.status_code < 300")
	case "3xx":
		conditions = append(conditions, "p.status_code >= 300 AND p.status_code < 400")
	case "4xx":
		conditions = append(conditions, "p.status_code >= 400 AND p.status_code < 500")
	case "5xx":
		conditions = append(conditions, "p.status_code >= 500")
	default:
		conditions = append(conditions, fmt.Sprintf("p.status_code = $%d", argIdx))
		args = append(args, statusFilter)
		argIdx++
	}

	if dateFrom != "" {
		conditions = append(conditions, fmt.Sprintf("p.crawled_at >= $%d", argIdx))
		args = append(args, dateFrom)
		argIdx++
	}
	if dateTo != "" {
		conditions = append(conditions, fmt.Sprintf("p.crawled_at <= $%d", argIdx))
		args = append(args, dateTo)
		argIdx++
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	orderClause := "ORDER BY rank DESC, p.crawled_at DESC"
	switch sortBy {
	case "newest":
		orderClause = "ORDER BY p.crawled_at DESC"
	case "oldest":
		orderClause = "ORDER BY p.crawled_at ASC"
	case "latency_asc":
		orderClause = "ORDER BY p.latency_ms ASC, p.crawled_at DESC"
	case "latency_desc":
		orderClause = "ORDER BY p.latency_ms DESC, p.crawled_at DESC"
	case "status":
		orderClause = "ORDER BY p.status_code ASC, p.crawled_at DESC"
	default:
		if !hasKeyword {
			orderClause = "ORDER BY p.crawled_at DESC"
		}
	}

	limitPh := fmt.Sprintf("$%d", argIdx)
	args = append(args, limit)
	argIdx++
	offsetPh := fmt.Sprintf("$%d", argIdx)
	args = append(args, offset)

	query := fmt.Sprintf(`
		SELECT p.id, COALESCE(p.job_id, ''), p.url, COALESCE(p.final_url, ''), COALESCE(p.canonical_url, ''),
		       COALESCE(d.host, ''), COALESCE(p.title, ''), COALESCE(p.description, ''), COALESCE(p.h1, ''),
		       COALESCE(p.headings, '{}'), COALESCE(p.extracted_text, ''),
		       COALESCE(p.content_type, ''), p.content_length, p.status_code, p.latency_ms,
		       p.depth, p.links_count, COALESCE(p.internal_links, '{}'), COALESCE(p.external_links, '{}'),
		       COALESCE(p.error_message, ''), p.crawled_at, %s AS rank,
		       COUNT(*) OVER() AS total_count
		FROM crawled_pages p
		LEFT JOIN domains d ON p.domain_id = d.id
		%s
		%s
		LIMIT %s OFFSET %s;
	`, rankExpr, whereClause, orderClause, limitPh, offsetPh)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed searching crawled pages: %w", err)
	}
	defer rows.Close()

	results := []models.SearchResult{}
	total := 0
	for rows.Next() {
		var res models.SearchResult
		if err := rows.Scan(
			&res.ID, &res.JobID, &res.URL, &res.FinalURL, &res.CanonicalURL,
			&res.Domain, &res.Title, &res.Description, &res.H1,
			pq.Array(&res.Headings), &res.ExtractedText,
			&res.ContentType, &res.ContentLength, &res.StatusCode, &res.LatencyMs,
			&res.Depth, &res.LinksCount, pq.Array(&res.InternalLinks), pq.Array(&res.ExternalLinks),
			&res.ErrorMessage, &res.CrawledAt, &res.Relevance, &total,
		); err != nil {
			return nil, 0, fmt.Errorf("failed scanning search result row: %w", err)
		}
		results = append(results, res)
	}

	return results, total, nil
}

// GetRecentPages retrieves the latest crawled pages.
func (r *PostgresRepository) GetRecentPages(ctx context.Context, limit int) ([]models.SearchResult, error) {
	results, _, err := r.SearchPagesWithTotal(ctx, "", "", "", "", "", "newest", limit, 0)
	return results, err
}

// GetIndexedPagesCount returns total persisted pages in crawled_pages.
func (r *PostgresRepository) GetIndexedPagesCount(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM crawled_pages;").Scan(&count)
	return count, err
}

// GetDiscoveredURLsCount returns total discovered URLs across all crawls.
func (r *PostgresRepository) GetDiscoveredURLsCount(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM discovered_urls;").Scan(&count)
	return count, err
}

// GetDomainCount returns total unique hosts registered.
func (r *PostgresRepository) GetDomainCount(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM domains;").Scan(&count)
	return count, err
}

// GetDomainMetrics retrieves real persisted domain statistics from domain_statistics.
func (r *PostgresRepository) GetDomainMetrics(ctx context.Context, limit int) ([]models.DomainMetric, error) {
	if limit <= 0 {
		limit = 30
	}
	query := `
		SELECT domain, total_pages, total_bytes, avg_latency_ms, error_count, last_crawled_at
		FROM domain_statistics
		ORDER BY total_pages DESC, last_crawled_at DESC
		LIMIT $1;
	`
	rows, err := r.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	metrics := []models.DomainMetric{}
	for rows.Next() {
		var m models.DomainMetric
		if err := rows.Scan(&m.Domain, &m.TotalPages, &m.TotalBytes, &m.AvgLatencyMs, &m.ErrorCount, &m.LastCrawledAt); err != nil {
			return nil, err
		}
		metrics = append(metrics, m)
	}
	return metrics, nil
}

// CreateJob persists a crawl job and all seed URLs atomically.
func (r *PostgresRepository) CreateJob(ctx context.Context, job models.CrawlJob) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	allowed := job.AllowedDomains
	if allowed == nil {
		allowed = []string{}
	}
	excludes := job.ExcludePatterns
	if excludes == nil {
		excludes = []string{}
	}
	if job.Concurrency <= 0 {
		job.Concurrency = 4
	}
	if job.PolitenessMs <= 0 {
		job.PolitenessMs = 1000
	}

	queryJob := `
		INSERT INTO crawl_jobs (
			id, name, status, seed_urls, max_depth, max_pages, concurrency, politeness_ms,
			allowed_domains, exclude_patterns, created_at, started_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW(), NOW(), NOW());
	`
	_, err = tx.ExecContext(ctx, queryJob,
		job.ID, job.Name, job.Status, pq.Array(job.SeedURLs),
		job.MaxDepth, job.MaxPages, job.Concurrency, job.PolitenessMs,
		pq.Array(allowed), pq.Array(excludes),
	)
	if err != nil {
		return fmt.Errorf("failed to insert crawl job: %w", err)
	}

	for _, seed := range job.SeedURLs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO crawl_seeds (job_id, url, status) VALUES ($1, $2, 'QUEUED');`, job.ID, seed); err != nil {
			return fmt.Errorf("failed to insert crawl seed: %w", err)
		}
	}

	return tx.Commit()
}

// GetJob retrieves a crawl job by ID.
func (r *PostgresRepository) GetJob(ctx context.Context, id string) (*models.CrawlJob, error) {
	query := `
		SELECT id, name, status, seed_urls, max_depth, max_pages,
		       COALESCE(concurrency, 4), COALESCE(politeness_ms, 1000),
		       COALESCE(allowed_domains, '{}'), COALESCE(exclude_patterns, '{}'),
		       pages_crawled, pages_failed, COALESCE(discovered_count, 0), COALESCE(current_depth, 0),
		       COALESCE(error_message, ''), created_at, started_at, updated_at, completed_at
		FROM crawl_jobs
		WHERE id = $1;
	`
	var job models.CrawlJob
	var seedURLs, allowedDomains, excludePatterns []string
	var startedAt, completedAt sql.NullTime

	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&job.ID, &job.Name, &job.Status, pq.Array(&seedURLs),
		&job.MaxDepth, &job.MaxPages, &job.Concurrency, &job.PolitenessMs,
		pq.Array(&allowedDomains), pq.Array(&excludePatterns),
		&job.PagesCrawled, &job.PagesFailed, &job.DiscoveredCount, &job.CurrentDepth,
		&job.ErrorMessage, &job.CreatedAt, &startedAt, &job.UpdatedAt, &completedAt,
	)
	if err != nil {
		return nil, err
	}
	job.SeedURLs = seedURLs
	job.AllowedDomains = allowedDomains
	job.ExcludePatterns = excludePatterns
	if startedAt.Valid {
		job.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		job.CompletedAt = &completedAt.Time
	}
	return &job, nil
}

// ListJobs retrieves crawl jobs ordered by newest creation timestamp.
func (r *PostgresRepository) ListJobs(ctx context.Context, limit, offset int) ([]models.CrawlJob, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	query := `
		SELECT id, name, status, seed_urls, max_depth, max_pages,
		       COALESCE(concurrency, 4), COALESCE(politeness_ms, 1000),
		       COALESCE(allowed_domains, '{}'), COALESCE(exclude_patterns, '{}'),
		       pages_crawled, pages_failed, COALESCE(discovered_count, 0), COALESCE(current_depth, 0),
		       COALESCE(error_message, ''), created_at, started_at, updated_at, completed_at
		FROM crawl_jobs
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2;
	`
	rows, err := r.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	jobs := []models.CrawlJob{}
	for rows.Next() {
		var job models.CrawlJob
		var seedURLs, allowedDomains, excludePatterns []string
		var startedAt, completedAt sql.NullTime
		if err := rows.Scan(
			&job.ID, &job.Name, &job.Status, pq.Array(&seedURLs),
			&job.MaxDepth, &job.MaxPages, &job.Concurrency, &job.PolitenessMs,
			pq.Array(&allowedDomains), pq.Array(&excludePatterns),
			&job.PagesCrawled, &job.PagesFailed, &job.DiscoveredCount, &job.CurrentDepth,
			&job.ErrorMessage, &job.CreatedAt, &startedAt, &job.UpdatedAt, &completedAt,
		); err != nil {
			return nil, err
		}
		job.SeedURLs = seedURLs
		job.AllowedDomains = allowedDomains
		job.ExcludePatterns = excludePatterns
		if startedAt.Valid {
			job.StartedAt = &startedAt.Time
		}
		if completedAt.Valid {
			job.CompletedAt = &completedAt.Time
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

// GetJobDetails fetches full job context including seeds, recent errors, events, and workers.
func (r *PostgresRepository) GetJobDetails(ctx context.Context, id string) (*models.JobDetailResponse, error) {
	job, err := r.GetJob(ctx, id)
	if err != nil {
		return nil, err
	}

	errorsList := []models.CrawlError{}
	if rows, err := r.db.QueryContext(ctx, `
		SELECT id, COALESCE(job_id,''), url, COALESCE(worker_id,''), COALESCE(status_code,0), error_type, error_message, created_at
		FROM crawl_errors WHERE job_id = $1 ORDER BY created_at DESC LIMIT 50;
	`, id); err == nil {
		defer rows.Close()
		for rows.Next() {
			var ce models.CrawlError
			if rows.Scan(&ce.ID, &ce.JobID, &ce.URL, &ce.WorkerID, &ce.StatusCode, &ce.ErrorType, &ce.ErrorMessage, &ce.CreatedAt) == nil {
				errorsList = append(errorsList, ce)
			}
		}
	}

	eventsList := []models.CrawlEvent{}
	workerSet := map[string]bool{}
	if rows, err := r.db.QueryContext(ctx, `
		SELECT id, COALESCE(job_id,''), COALESCE(worker_id,''), event_type, COALESCE(severity,'INFO'),
		       COALESCE(url,''), COALESCE(status_code,0), COALESCE(latency_ms,0), COALESCE(message,''), created_at
		FROM crawl_events WHERE job_id = $1 ORDER BY created_at DESC LIMIT 50;
	`, id); err == nil {
		defer rows.Close()
		for rows.Next() {
			var ev models.CrawlEvent
			if rows.Scan(&ev.ID, &ev.JobID, &ev.WorkerID, &ev.EventType, &ev.Severity, &ev.URL, &ev.StatusCode, &ev.LatencyMs, &ev.Message, &ev.CreatedAt) == nil {
				eventsList = append(eventsList, ev)
				if ev.WorkerID != "" {
					workerSet[ev.WorkerID] = true
				}
			}
		}
	}

	workers := make([]string, 0, len(workerSet))
	for wID := range workerSet {
		workers = append(workers, wID)
	}

	return &models.JobDetailResponse{
		Job:     job,
		Seeds:   job.SeedURLs,
		Errors:  errorsList,
		Events:  eventsList,
		Workers: workers,
	}, nil
}

// GetFailedURLsForJob retrieves distinct failed URLs associated with a job for retries.
func (r *PostgresRepository) GetFailedURLsForJob(ctx context.Context, jobID string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT DISTINCT url FROM crawl_errors WHERE job_id = $1 LIMIT 200;`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var urls []string
	for rows.Next() {
		var u string
		if rows.Scan(&u) == nil {
			urls = append(urls, u)
		}
	}
	return urls, nil
}

// UpdateJobStatus modifies job state cleanly.
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
			SET status = $2, error_message = $3, updated_at = NOW(), completed_at = NULL
			WHERE id = $1;
		`
	}
	res, err := r.db.ExecContext(ctx, query, id, status, errMessage)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteJobData deletes data belonging solely to a selected job while preserving other jobs.
func (r *PostgresRepository) DeleteJobData(ctx context.Context, jobID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, _ = tx.ExecContext(ctx, `DELETE FROM discovered_urls WHERE job_id = $1;`, jobID)
	_, _ = tx.ExecContext(ctx, `DELETE FROM crawl_errors WHERE job_id = $1;`, jobID)
	_, _ = tx.ExecContext(ctx, `DELETE FROM crawled_pages WHERE job_id = $1;`, jobID)
	_, _ = tx.ExecContext(ctx, `DELETE FROM crawl_events WHERE job_id = $1;`, jobID)
	res, err := tx.ExecContext(ctx, `DELETE FROM crawl_jobs WHERE id = $1;`, jobID)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

// RegisterWorker updates or inserts a worker daemon record in Postgres.
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

// UpdateWorkerHeartbeat persists periodic worker telemetry.
func (r *PostgresRepository) UpdateWorkerHeartbeat(ctx context.Context, req models.HeartbeatRequest) error {
	query := `
		INSERT INTO worker_registry (id, hostname, version, status, current_task, tasks_active, tasks_completed, tasks_failed, avg_latency_ms, uptime_sec, started_at, last_heartbeat)
		VALUES ($1, COALESCE(NULLIF($2,''), 'worker'), COALESCE(NULLIF($3,''), '2.0.0'), $4, $5, $6, $7, $8, $9, $10, NOW(), NOW())
		ON CONFLICT (id) DO UPDATE SET
			status = EXCLUDED.status,
			current_task = EXCLUDED.current_task,
			tasks_active = EXCLUDED.tasks_active,
			tasks_completed = EXCLUDED.tasks_completed,
			tasks_failed = EXCLUDED.tasks_failed,
			avg_latency_ms = EXCLUDED.avg_latency_ms,
			uptime_sec = EXCLUDED.uptime_sec,
			last_heartbeat = NOW();
	`
	_, err := r.db.ExecContext(ctx, query,
		req.WorkerID, req.Hostname, req.Version, req.Status,
		sqlNullString(req.CurrentTask), req.TasksActive, req.TasksCompleted, req.TasksFailed,
		req.AvgLatencyMs, req.UptimeSec,
	)
	return err
}

// ListWorkers returns registered workers and marks heartbeats older than 15s as UNHEALTHY.
func (r *PostgresRepository) ListWorkers(ctx context.Context) ([]models.WorkerInfo, error) {
	query := `
		SELECT id, hostname, COALESCE(version,'2.0.0'), status, COALESCE(current_task,''),
		       tasks_active, tasks_completed, tasks_failed, avg_latency_ms, COALESCE(uptime_sec,0),
		       EXTRACT(EPOCH FROM (NOW() - last_heartbeat)) AS sec_ago, last_heartbeat, started_at
		FROM worker_registry
		ORDER BY last_heartbeat DESC;
	`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	workers := []models.WorkerInfo{}
	for rows.Next() {
		var w models.WorkerInfo
		if err := rows.Scan(
			&w.ID, &w.Hostname, &w.Version, &w.Status, &w.CurrentTask,
			&w.TasksActive, &w.TasksCompleted, &w.TasksFailed, &w.AvgLatencyMs, &w.UptimeSec,
			&w.LastSeenSecAgo, &w.LastHeartbeat, &w.StartedAt,
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

// RecordEvent records an auditable crawl activity log entry.
func (r *PostgresRepository) RecordEvent(ctx context.Context, e models.CrawlEvent) error {
	severity := e.Severity
	if severity == "" {
		switch {
		case strings.Contains(e.EventType, "ERROR") || strings.Contains(e.EventType, "FAIL") || strings.Contains(e.EventType, "SSRF"):
			severity = "ERROR"
		case strings.Contains(e.EventType, "LEASE") || strings.Contains(e.EventType, "WARN") || strings.Contains(e.EventType, "PAUSE") || strings.Contains(e.EventType, "ROBOTS"):
			severity = "WARN"
		default:
			severity = "INFO"
		}
	}
	query := `
		INSERT INTO crawl_events (job_id, worker_id, event_type, severity, url, status_code, latency_ms, message, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW());
	`
	_, err := r.db.ExecContext(ctx, query,
		sqlNullString(e.JobID),
		sqlNullString(e.WorkerID),
		e.EventType,
		severity,
		sqlNullString(e.URL),
		e.StatusCode,
		e.LatencyMs,
		e.Message,
	)
	return err
}

// GetRecentEvents retrieves activity events filtered optionally by severity or keyword.
func (r *PostgresRepository) GetRecentEvents(ctx context.Context, limit int) ([]models.CrawlEvent, error) {
	return r.QueryEvents(ctx, "", "", limit)
}

// QueryEvents retrieves filtered activity events.
func (r *PostgresRepository) QueryEvents(ctx context.Context, severity, search string, limit int) ([]models.CrawlEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := `
		SELECT id, COALESCE(job_id, ''), COALESCE(worker_id, ''), event_type, COALESCE(severity, 'INFO'),
		       COALESCE(url, ''), COALESCE(status_code, 0), COALESCE(latency_ms, 0),
		       COALESCE(message, ''), created_at
		FROM crawl_events
		WHERE ($1 = '' OR UPPER(severity) = UPPER($1))
		  AND ($2 = '' OR message ILIKE '%' || $2 || '%' OR url ILIKE '%' || $2 || '%' OR event_type ILIKE '%' || $2 || '%')
		ORDER BY created_at DESC
		LIMIT $3;
	`
	rows, err := r.db.QueryContext(ctx, query, strings.TrimSpace(severity), strings.TrimSpace(search), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []models.CrawlEvent{}
	for rows.Next() {
		var e models.CrawlEvent
		if err := rows.Scan(
			&e.ID, &e.JobID, &e.WorkerID, &e.EventType, &e.Severity,
			&e.URL, &e.StatusCode, &e.LatencyMs, &e.Message, &e.CreatedAt,
		); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, nil
}

// ListCrawlErrors returns recent persisted crawl errors.
func (r *PostgresRepository) ListCrawlErrors(ctx context.Context, limit int) ([]models.CrawlError, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, COALESCE(job_id,''), url, COALESCE(worker_id,''), COALESCE(status_code,0), error_type, error_message, created_at
		FROM crawl_errors ORDER BY created_at DESC LIMIT $1;
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	errs := []models.CrawlError{}
	for rows.Next() {
		var ce models.CrawlError
		if rows.Scan(&ce.ID, &ce.JobID, &ce.URL, &ce.WorkerID, &ce.StatusCode, &ce.ErrorType, &ce.ErrorMessage, &ce.CreatedAt) == nil {
			errs = append(errs, ce)
		}
	}
	return errs, nil
}

// GetAnalytics computes real backend SQL aggregations for analytics and overview telemetry.
func (r *PostgresRepository) GetAnalytics(ctx context.Context, rangeParam string) (*models.AnalyticsResponse, error) {
	res := &models.AnalyticsResponse{
		Range:              rangeParam,
		StatusBreakdown:    []models.BucketCount{},
		ErrorCategories:    []models.BucketCount{},
		DomainDistribution: []models.DomainMetric{},
		ThroughputSeries:   []models.ThroughputPoint{},
		WorkerUtilization:  []models.WorkerInfo{},
		JobBreakdown:       map[string]int{},
	}

	var avgF, p50F, p95F sql.NullFloat64
	err := r.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) AS total,
			COUNT(*) FILTER (WHERE status_code >= 200 AND status_code < 300) AS success,
			COUNT(*) FILTER (WHERE status_code >= 400 OR status_code = 0) AS failed,
			AVG(latency_ms),
			PERCENTILE_CONT(0.50) WITHIN GROUP (ORDER BY latency_ms),
			PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY latency_ms)
		FROM crawled_pages;
	`).Scan(&res.PagesIndexed, &res.SuccessCount, &res.FailedCount, &avgF, &p50F, &p95F)
	if err != nil {
		return nil, err
	}

	if avgF.Valid {
		res.AvgLatencyMs = int(avgF.Float64)
	}
	if p50F.Valid {
		res.P50LatencyMs = int(p50F.Float64)
	}
	if p95F.Valid {
		res.P95LatencyMs = int(p95F.Float64)
	}

	res.TotalRequests = res.PagesIndexed
	if res.TotalRequests > 0 {
		res.SuccessRate = float64(res.SuccessCount) * 100.0 / float64(res.TotalRequests)
	}

	// Status breakdown
	if rows, err := r.db.QueryContext(ctx, `
		SELECT
			CASE
				WHEN status_code >= 200 AND status_code < 300 THEN '2xx'
				WHEN status_code >= 300 AND status_code < 400 THEN '3xx'
				WHEN status_code >= 400 AND status_code < 500 THEN '4xx'
				ELSE '5xx/Err'
			END AS grp,
			COUNT(*)
		FROM crawled_pages
		GROUP BY grp ORDER BY grp;
	`); err == nil {
		defer rows.Close()
		for rows.Next() {
			var b models.BucketCount
			if rows.Scan(&b.Label, &b.Count) == nil {
				res.StatusBreakdown = append(res.StatusBreakdown, b)
			}
		}
	}

	// Error categories & robots blocked count
	if rows, err := r.db.QueryContext(ctx, `
		SELECT error_type, COUNT(*) FROM crawl_errors GROUP BY error_type ORDER BY COUNT(*) DESC;
	`); err == nil {
		defer rows.Close()
		for rows.Next() {
			var b models.BucketCount
			if rows.Scan(&b.Label, &b.Count) == nil {
				res.ErrorCategories = append(res.ErrorCategories, b)
				if b.Label == "ROBOTS_DISALLOWED" {
					res.RobotsBlocked = b.Count
				}
			}
		}
	}

	// Lease reclaims count
	_ = r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM crawl_events WHERE event_type = 'LEASE_RECLAIMED';`).Scan(&res.LeaseReclaims)

	// Throughput series (grouped by minute for 1h/24h clarity)
	if rows, err := r.db.QueryContext(ctx, `
		SELECT TO_CHAR(DATE_TRUNC('minute', crawled_at), 'HH24:MI') AS bucket,
		       COUNT(*) AS pages,
		       COUNT(*) FILTER (WHERE status_code >= 400 OR status_code = 0) AS errs
		FROM crawled_pages
		WHERE crawled_at >= NOW() - INTERVAL '24 hours'
		GROUP BY DATE_TRUNC('minute', crawled_at)
		ORDER BY DATE_TRUNC('minute', crawled_at) ASC
		LIMIT 30;
	`); err == nil {
		defer rows.Close()
		for rows.Next() {
			var tp models.ThroughputPoint
			if rows.Scan(&tp.Bucket, &tp.Pages, &tp.Errors) == nil {
				res.ThroughputSeries = append(res.ThroughputSeries, tp)
			}
		}
	}

	// Job counts by status
	if rows, err := r.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM crawl_jobs GROUP BY status;`); err == nil {
		defer rows.Close()
		for rows.Next() {
			var st string
			var cnt int
			if rows.Scan(&st, &cnt) == nil {
				res.JobBreakdown[st] = cnt
			}
		}
	}

	res.DomainDistribution, _ = r.GetDomainMetrics(ctx, 10)
	res.WorkerUtilization, _ = r.ListWorkers(ctx)

	return res, nil
}

// GetSettings returns all persisted crawl settings.
func (r *PostgresRepository) GetSettings(ctx context.Context) (map[string]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT key, value FROM crawl_settings ORDER BY key;`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	settings := map[string]string{}
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) == nil {
			settings[k] = v
		}
	}
	return settings, nil
}

// UpdateSetting persists a configuration setting cleanly.
func (r *PostgresRepository) UpdateSetting(ctx context.Context, key, value string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO crawl_settings (key, value, updated_at) VALUES ($1, $2, NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();
	`, key, value)
	return err
}

// Truncate clears pages, domains, events, errors, seeds, and jobs while keeping schema & workers intact.
func (r *PostgresRepository) Truncate(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, "TRUNCATE TABLE discovered_urls, crawl_errors, crawl_events, crawl_seeds, crawled_pages, domains, crawl_jobs, domain_statistics RESTART IDENTITY CASCADE;")
	return err
}

// TruncateData clears crawled pages and domain metrics while preserving crawl jobs and worker registrations.
func (r *PostgresRepository) TruncateData(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, "TRUNCATE TABLE discovered_urls, crawl_errors, crawled_pages, domains, domain_statistics RESTART IDENTITY CASCADE;")
	return err
}

// AcquireAdvisoryLock acquires a PostgreSQL session advisory lock on a dedicated connection
// and returns a release function that unlocks and closes the connection.
func (r *PostgresRepository) AcquireAdvisoryLock(ctx context.Context, lockID int64) (func(), error) {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1);", lockID); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1);", lockID)
		_ = conn.Close()
	}, nil
}

func sqlNullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: s, Valid: true}
}
