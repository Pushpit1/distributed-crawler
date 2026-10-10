package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"time"

	"distributed-crawler/pkg/models"

	_ "github.com/lib/pq"
)

// SQL Schemas
const schemaSQL = `
CREATE TABLE IF NOT EXISTS domains (
    id SERIAL PRIMARY KEY,
    host VARCHAR(255) UNIQUE NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS crawled_pages (
    id SERIAL PRIMARY KEY,
    domain_id INT REFERENCES domains(id) ON DELETE CASCADE,
    url TEXT UNIQUE NOT NULL,
    title TEXT,
    description TEXT,
    status_code INT NOT NULL,
    crawled_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_crawled_pages_crawled_at ON crawled_pages(crawled_at);
`

// PostgresRepository manages database connection and queries.
type PostgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository establishes a connection pool to PostgreSQL and initializes the schema.
func NewPostgresRepository(connStr string) (*PostgresRepository, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open postgres connection: %w", err)
	}

	// Configure database connection pool parameters
	db.SetMaxOpenConns(25)                 // Maximum number of open connections to the database
	db.SetMaxIdleConns(5)                  // Maximum number of connections in the idle connection pool
	db.SetConnMaxLifetime(5 * time.Minute) // Maximum amount of time a connection may be reused

	// Ping database to verify connection
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping postgres: %w", err)
	}

	repo := &PostgresRepository{db: db}

	// Run migrations (table creation)
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

// initSchema executes migrations.
func (r *PostgresRepository) initSchema(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, schemaSQL)
	return err
}

// SavePage persists a crawled page to the database. It handles registering the domain first if missing.
func (r *PostgresRepository) SavePage(ctx context.Context, pageURL, title, description string, statusCode int) (int64, error) {
	// 1. Extract host from URL
	parsedURL, err := url.Parse(pageURL)
	if err != nil {
		return 0, fmt.Errorf("invalid URL parsed during database save: %w", err)
	}
	host := parsedURL.Host
	if host == "" {
		return 0, fmt.Errorf("missing host in URL: %s", pageURL)
	}

	// 2. Insert host into domains table, returning its ID.
	// If it already exists, retrieve the existing ID using ON CONFLICT DO UPDATE.
	// ON CONFLICT DO UPDATE is a PostgreSQL feature that acts as an atomic Upsert!
	var domainID int64
	queryDomain := `
		INSERT INTO domains (host) 
		VALUES ($1) 
		ON CONFLICT (host) DO UPDATE SET host = EXCLUDED.host
		RETURNING id;
	`
	err = r.db.QueryRowContext(ctx, queryDomain, host).Scan(&domainID)
	if err != nil {
		return 0, fmt.Errorf("failed to upsert domain: %w", err)
	}

	// 3. Insert page metadata into crawled_pages table.
	// If the URL already exists (e.g., recrawling), update the page content.
	var pageID int64
	queryPage := `
		INSERT INTO crawled_pages (domain_id, url, title, description, status_code, crawled_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (url) DO UPDATE SET 
			title = EXCLUDED.title,
			description = EXCLUDED.description,
			status_code = EXCLUDED.status_code,
			crawled_at = NOW()
		RETURNING id;
	`
	err = r.db.QueryRowContext(ctx, queryPage, domainID, pageURL, title, description, statusCode).Scan(&pageID)
	if err != nil {
		return 0, fmt.Errorf("failed to upsert crawled page: %w", err)
	}

	return pageID, nil
}

// GetPageByURL retrieves a page record from PostgreSQL.
func (r *PostgresRepository) GetPageByURL(ctx context.Context, pageURL string) (string, string, int, error) {
	query := `
		SELECT title, description, status_code 
		FROM crawled_pages 
		WHERE url = $1;
	`
	var title, description string
	var statusCode int
	err := r.db.QueryRowContext(ctx, query, pageURL).Scan(&title, &description, &statusCode)
	if err != nil {
		return "", "", 0, err
	}
	return title, description, statusCode, nil
}

// Truncate clears the domains and crawled_pages tables (useful for test resets).
func (r *PostgresRepository) Truncate(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, "TRUNCATE TABLE crawled_pages, domains RESTART IDENTITY CASCADE;")
	return err
}

// SearchPages finds pages matching the keyword in title, description, or URL, returning up to 30 results.
func (r *PostgresRepository) SearchPages(ctx context.Context, keyword string) ([]models.SearchResult, error) {
	query := `
		SELECT url, COALESCE(title, ''), COALESCE(description, ''), status_code 
		FROM crawled_pages 
		WHERE title ILIKE $1 OR description ILIKE $1 OR url ILIKE $1
		ORDER BY crawled_at DESC 
		LIMIT 30;
	`
	// Match pattern %keyword% for SQL ILIKE
	pattern := "%" + keyword + "%"

	rows, err := r.db.QueryContext(ctx, query, pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to query search pages: %w", err)
	}
	defer rows.Close()

	var results []models.SearchResult
	for rows.Next() {
		var res models.SearchResult
		err := rows.Scan(&res.URL, &res.Title, &res.Description, &res.StatusCode)
		if err != nil {
			return nil, fmt.Errorf("failed to scan search result row: %w", err)
		}
		results = append(results, res)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error during row iteration: %w", err)
	}

	return results, nil
}

// GetDomainCount returns the total number of unique hosts registered in the domains table.
func (r *PostgresRepository) GetDomainCount(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM domains;").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count domains: %w", err)
	}
	return count, nil
}

// GetRecentPages retrieves the latest crawled pages up to the specified limit.
func (r *PostgresRepository) GetRecentPages(ctx context.Context, limit int) ([]models.SearchResult, error) {
	query := `
		SELECT url, COALESCE(title, ''), COALESCE(description, ''), status_code 
		FROM crawled_pages 
		ORDER BY crawled_at DESC 
		LIMIT $1;
	`
	rows, err := r.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query recent pages: %w", err)
	}
	defer rows.Close()

	var results []models.SearchResult
	for rows.Next() {
		var res models.SearchResult
		err := rows.Scan(&res.URL, &res.Title, &res.Description, &res.StatusCode)
		if err != nil {
			return nil, fmt.Errorf("failed to scan recent page row: %w", err)
		}
		results = append(results, res)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error during row iteration: %w", err)
	}

	return results, nil
}
