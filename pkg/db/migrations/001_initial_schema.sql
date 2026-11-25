-- Migration 001: Complete Distributed Crawler Schema (Idempotent & Upgrade-Safe)

CREATE TABLE IF NOT EXISTS schema_migrations (
    version VARCHAR(64) PRIMARY KEY,
    applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

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
    status VARCHAR(32) NOT NULL DEFAULT 'RUNNING',
    seed_urls TEXT[] NOT NULL,
    max_depth INT DEFAULT 3,
    max_pages INT DEFAULT 100,
    concurrency INT DEFAULT 4,
    politeness_ms INT DEFAULT 1000,
    allowed_domains TEXT[] DEFAULT '{}',
    exclude_patterns TEXT[] DEFAULT '{}',
    pages_crawled INT DEFAULT 0,
    pages_failed INT DEFAULT 0,
    discovered_count INT DEFAULT 0,
    current_depth INT DEFAULT 0,
    error_message TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    started_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
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
    final_url TEXT,
    canonical_url TEXT,
    title TEXT,
    description TEXT,
    h1 TEXT,
    headings TEXT[] DEFAULT '{}',
    extracted_text TEXT,
    content_type VARCHAR(128),
    content_length INT DEFAULT 0,
    status_code INT NOT NULL,
    latency_ms INT DEFAULT 0,
    depth INT DEFAULT 0,
    links_count INT DEFAULT 0,
    internal_links TEXT[] DEFAULT '{}',
    external_links TEXT[] DEFAULT '{}',
    error_message TEXT,
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
    status_code INT DEFAULT 0,
    error_type VARCHAR(64) NOT NULL,
    error_message TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS worker_registry (
    id VARCHAR(128) PRIMARY KEY,
    hostname VARCHAR(255) NOT NULL,
    version VARCHAR(32) DEFAULT '2.0.0',
    status VARCHAR(32) NOT NULL DEFAULT 'ACTIVE',
    current_task TEXT,
    tasks_active INT DEFAULT 0,
    tasks_completed INT DEFAULT 0,
    tasks_failed INT DEFAULT 0,
    avg_latency_ms INT DEFAULT 0,
    uptime_sec BIGINT DEFAULT 0,
    started_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_heartbeat TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS crawl_events (
    id SERIAL PRIMARY KEY,
    job_id VARCHAR(64),
    worker_id VARCHAR(128),
    event_type VARCHAR(64) NOT NULL,
    severity VARCHAR(16) DEFAULT 'INFO',
    url TEXT,
    status_code INT DEFAULT 0,
    latency_ms INT DEFAULT 0,
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

-- Upgrade existing legacy tables safely BEFORE indexing evolved columns
ALTER TABLE domains ADD COLUMN IF NOT EXISTS total_pages INT DEFAULT 0;
ALTER TABLE domains ADD COLUMN IF NOT EXISTS created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP;
ALTER TABLE domains ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP;

ALTER TABLE crawl_jobs ADD COLUMN IF NOT EXISTS concurrency INT DEFAULT 4;
ALTER TABLE crawl_jobs ADD COLUMN IF NOT EXISTS politeness_ms INT DEFAULT 1000;
ALTER TABLE crawl_jobs ADD COLUMN IF NOT EXISTS allowed_domains TEXT[] DEFAULT '{}';
ALTER TABLE crawl_jobs ADD COLUMN IF NOT EXISTS exclude_patterns TEXT[] DEFAULT '{}';
ALTER TABLE crawl_jobs ADD COLUMN IF NOT EXISTS discovered_count INT DEFAULT 0;
ALTER TABLE crawl_jobs ADD COLUMN IF NOT EXISTS current_depth INT DEFAULT 0;
ALTER TABLE crawl_jobs ADD COLUMN IF NOT EXISTS started_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP;

ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS job_id VARCHAR(64);
ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS final_url TEXT;
ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS canonical_url TEXT;
ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS h1 TEXT;
ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS headings TEXT[] DEFAULT '{}';
ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS extracted_text TEXT;
ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS content_type VARCHAR(128);
ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS content_length INT DEFAULT 0;
ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS latency_ms INT DEFAULT 0;
ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS depth INT DEFAULT 0;
ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS links_count INT DEFAULT 0;
ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS internal_links TEXT[] DEFAULT '{}';
ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS external_links TEXT[] DEFAULT '{}';
ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS error_message TEXT;
ALTER TABLE crawled_pages ADD COLUMN IF NOT EXISTS tsv tsvector;

ALTER TABLE worker_registry ADD COLUMN IF NOT EXISTS current_task TEXT;
ALTER TABLE worker_registry ADD COLUMN IF NOT EXISTS uptime_sec BIGINT DEFAULT 0;
ALTER TABLE crawl_events ADD COLUMN IF NOT EXISTS severity VARCHAR(16) DEFAULT 'INFO';

-- Indexes (guaranteed columns exist even on legacy upgraded databases)
CREATE INDEX IF NOT EXISTS idx_crawled_pages_crawled_at ON crawled_pages(crawled_at DESC);
CREATE INDEX IF NOT EXISTS idx_crawled_pages_domain_id ON crawled_pages(domain_id);
CREATE INDEX IF NOT EXISTS idx_crawled_pages_job_id ON crawled_pages(job_id);
CREATE INDEX IF NOT EXISTS idx_crawled_pages_status_code ON crawled_pages(status_code);
CREATE INDEX IF NOT EXISTS idx_crawled_pages_tsv ON crawled_pages USING GIN(tsv);
CREATE INDEX IF NOT EXISTS idx_discovered_urls_job_id ON discovered_urls(job_id);
CREATE INDEX IF NOT EXISTS idx_crawl_errors_job_id ON crawl_errors(job_id);
CREATE INDEX IF NOT EXISTS idx_crawl_events_created_at ON crawl_events(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_crawl_events_job_id ON crawl_events(job_id);

-- Backfill FTS vector on legacy upgraded rows
UPDATE crawled_pages
SET tsv = setweight(to_tsvector('english', COALESCE(title, '')), 'A') ||
          setweight(to_tsvector('english', COALESCE(h1, '')), 'B') ||
          setweight(to_tsvector('english', COALESCE(description, '')), 'B') ||
          setweight(to_tsvector('english', COALESCE(extracted_text, '')), 'C') ||
          setweight(to_tsvector('simple', COALESCE(url, '')), 'D')
WHERE tsv IS NULL;

-- Backfill domain_statistics safely from legacy domains & crawled_pages if empty
INSERT INTO domain_statistics (domain, total_pages, total_bytes, avg_latency_ms, error_count, last_crawled_at)
SELECT
    d.host,
    GREATEST(COUNT(p.id), COALESCE(d.total_pages, 0)),
    COALESCE(SUM(p.content_length), 0),
    COALESCE(AVG(p.latency_ms)::INT, 0),
    COUNT(p.id) FILTER (WHERE p.status_code >= 400 OR p.status_code = 0),
    COALESCE(MAX(p.crawled_at), d.updated_at, d.created_at, NOW())
FROM domains d
LEFT JOIN crawled_pages p ON p.domain_id = d.id
GROUP BY d.host, d.total_pages, d.updated_at, d.created_at
ON CONFLICT (domain) DO NOTHING;
