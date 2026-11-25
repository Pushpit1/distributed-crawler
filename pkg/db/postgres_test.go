package db

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"distributed-crawler/pkg/models"
)

func getTestDatabaseURL() string {
	if env := os.Getenv("TEST_DATABASE_URL"); env != "" {
		return env
	}
	return "postgres://crawler_user:crawler_password@localhost:5433/crawler_db?sslmode=disable"
}

func TestPostgresRepository_FullLifecycle(t *testing.T) {
	connStr := getTestDatabaseURL()

	repo, err := NewPostgresRepository(connStr)
	if err != nil {
		t.Skipf("PostgreSQL test container offline (%v). Skipping integration tests.", err)
	}
	defer repo.Close()

	ctx := context.Background()
	if unlock, lockErr := repo.AcquireAdvisoryLock(ctx, 99887766); lockErr == nil {
		defer unlock()
	}
	if err := repo.Truncate(ctx); err != nil {
		t.Fatalf("Failed to clean database tables: %v", err)
	}

	// 1. Create crawl job (validates pq.Array on TEXT[] seed_urls, allowed_domains, exclude_patterns)
	job := models.CrawlJob{
		ID:              "job-db-test-1",
		Name:            "Integration DB Crawl",
		Status:          models.JobStateRunning,
		SeedURLs:        []string{"https://golang.org/doc/install"},
		MaxDepth:        3,
		MaxPages:        10,
		Concurrency:     2,
		PolitenessMs:    500,
		AllowedDomains:  []string{"golang.org"},
		ExcludePatterns: []string{"/admin"},
	}
	if err := repo.CreateJob(ctx, job); err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}

	// Verify job retrieval
	loadedJob, err := repo.GetJob(ctx, job.ID)
	if err != nil || loadedJob.Name != job.Name || len(loadedJob.SeedURLs) != 1 {
		t.Fatalf("GetJob mismatch: job=%+v err=%v", loadedJob, err)
	}

	// 2. Save full page record with headings, extracted text, internal/external links
	pageID, err := repo.SavePageFull(ctx, models.ReportRequest{
		WorkerID:      "worker-test-1",
		JobID:         job.ID,
		URL:           "https://golang.org/doc/install",
		FinalURL:      "https://go.dev/doc/install",
		CanonicalURL:  "https://go.dev/doc/install",
		StatusCode:    200,
		Title:         "Installing Go Runtime",
		Description:   "Download and install Go compiler toolchain.",
		H1:            "Download and install",
		Headings:      []string{"H1: Download and install", "H2: Linux instructions"},
		ExtractedText: "Follow the steps to unpack the Go binary archive and configure PATH.",
		ContentType:   "text/html",
		ContentLength: 2048,
		LatencyMs:     95,
		Depth:         0,
		Links:         []string{"https://go.dev/doc/", "https://github.com/golang/go"},
		InternalLinks: []string{"https://go.dev/doc/"},
		ExternalLinks: []string{"https://github.com/golang/go"},
	})
	if err != nil || pageID <= 0 {
		t.Fatalf("SavePageFull failed: id=%d err=%v", pageID, err)
	}

	// 3. Verify page inspection by ID
	fullPage, err := repo.GetPageByID(ctx, pageID)
	if err != nil || fullPage.FinalURL != "https://go.dev/doc/install" || len(fullPage.Headings) != 2 {
		t.Fatalf("GetPageByID mismatch: %+v err=%v", fullPage, err)
	}

	// 4. Verify real domain_statistics metrics (no hardcoded 0,0)
	domains, err := repo.GetDomainMetrics(ctx, 10)
	if err != nil || len(domains) != 1 || domains[0].AvgLatencyMs != 95 || domains[0].TotalBytes != 2048 {
		t.Fatalf("GetDomainMetrics returned unexpected values: %+v err=%v", domains, err)
	}

	// 5. Verify PostgreSQL full-text search ranking & pagination
	results, total, err := repo.SearchPagesWithTotal(ctx, "toolchain archive", "golang.org", "2xx", "", "", "relevance", 10, 0)
	if err != nil || total != 1 || len(results) != 1 {
		t.Fatalf("SearchPagesWithTotal FTS failed: total=%d len=%d err=%v", total, len(results), err)
	}

	// 6. Verify error persistence + analytics aggregation
	_, err = repo.SavePageFull(ctx, models.ReportRequest{
		WorkerID:     "worker-test-1",
		JobID:        job.ID,
		URL:          "https://golang.org/missing",
		StatusCode:   404,
		Title:        "Not Found",
		LatencyMs:    30,
		Depth:        1,
		ErrorType:    "HTTP_4XX",
		ErrorMessage: "non-200 status code: 404",
	})
	if err != nil {
		t.Fatalf("Failed saving error page: %v", err)
	}

	analytics, err := repo.GetAnalytics(ctx, "24h")
	if err != nil || analytics.PagesIndexed != 2 || analytics.SuccessCount != 1 || analytics.FailedCount != 1 {
		t.Fatalf("GetAnalytics mismatch: %+v err=%v", analytics, err)
	}

	// 7. Verify Worker registry & heartbeats
	_ = repo.RegisterWorker(ctx, models.WorkerInfo{ID: "worker-test-1", Hostname: "host-a", Version: "2.0.0", Status: models.WorkerStateIdle})
	_ = repo.UpdateWorkerHeartbeat(ctx, models.HeartbeatRequest{WorkerID: "worker-test-1", Status: models.WorkerStateActive, TasksCompleted: 2, AvgLatencyMs: 62})
	workers, err := repo.ListWorkers(ctx)
	if err != nil || len(workers) == 0 {
		t.Fatalf("ListWorkers failed: %v", err)
	}

	// 8. Verify non-existent row check
	_, _, _, err = repo.GetPageByURL(ctx, "https://nonexistent.invalid")
	if err != sql.ErrNoRows {
		t.Errorf("Expected sql.ErrNoRows for nonexistent URL, got: %v", err)
	}
}
