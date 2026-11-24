package master

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"distributed-crawler/internal/worker"
	"distributed-crawler/pkg/crawler"
	"distributed-crawler/pkg/db"

	"github.com/redis/go-redis/v9"
)

func TestServerHealthAndCORS(t *testing.T) {
	srv := NewCoordinatorServer(nil, nil, 0)

	// Test HandleHealth
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	srv.HandleHealth(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("Expected CORS header, got %q", w.Header().Get("Access-Control-Allow-Origin"))
	}

	// Test HandleCORS
	corsReq := httptest.NewRequest(http.MethodOptions, "/api/status", nil)
	corsW := httptest.NewRecorder()
	srv.HandleCORS(corsW, corsReq)

	if corsW.Code != http.StatusOK {
		t.Errorf("Expected 200 on OPTIONS preflight, got %d", corsW.Code)
	}
}

func TestMasterWorkerIntegration(t *testing.T) {
	// 1. Setup Redis connection
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	ctx := context.Background()

	// Ping check Redis
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		t.Skip("Docker Redis is offline. Skipping integration tests.")
	}

	// Use unique test keys
	queueKey := "test:m4:queue"
	setKey := "test:m4:visited"
	
	// Clean up Redis
	rdb.Del(ctx, queueKey, setKey)
	defer rdb.Del(ctx, queueKey, setKey)

	// Connect to PostgreSQL pointing to host port 5433
	pgConnStr := "postgres://crawler_user:crawler_password@localhost:5433/crawler_db?sslmode=disable"
	pgRepo, err := db.NewPostgresRepository(pgConnStr)
	if err != nil {
		t.Skip("PostgreSQL Docker container is offline. Skipping integration tests.")
	}
	defer pgRepo.Close()

	// Truncate tables for a clean test slate
	err = pgRepo.Truncate(ctx)
	if err != nil {
		t.Fatalf("Failed to clean postgres tables for test: %v", err)
	}

	// 2. Initialize Frontier & Coordinator Server with DB dependency (0 politeness delay for tests)
	frontier := crawler.NewFrontier(rdb, queueKey, setKey)
	srv := NewCoordinatorServer(frontier, pgRepo, 0)

	// 3. Register HTTP handlers on a test ServeMux
	mux := http.NewServeMux()
	mux.HandleFunc("POST /register", srv.HandleRegister)
	mux.HandleFunc("POST /poll", srv.HandlePoll)
	mux.HandleFunc("POST /report", srv.HandleReport)

	// 4. Start local in-memory HTTP server
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// 5. Populate some seed URLs in Redis queue directly
	_, err = frontier.Submit(ctx, "https://example.com/seed-1", "https://example.com/seed-2")
	if err != nil {
		t.Fatalf("Failed to queue seed URLs: %v", err)
	}

	// 6. Initialize Worker client pointing to our test server
	parser := crawler.NewParser(2*time.Second, "TestAgent/1.0")
	w := worker.NewWorker(ts.URL, parser)

	// 7. Verify Registration
	if err := w.Register(ctx); err != nil {
		t.Fatalf("Worker registration failed: %v", err)
	}
	if w.ID() == "" {
		t.Error("Expected worker to receive a non-empty worker_id")
	}

	// 8. Verify Polling (Should pop the first seed URL: seed-1)
	taskURL, err := w.PollTask(ctx)
	if err != nil {
		t.Fatalf("Worker polling failed: %v", err)
	}
	if taskURL != "https://example.com/seed-1" {
		t.Errorf("Expected polled URL to be 'https://example.com/seed-1', got %q", taskURL)
	}

	// 9. Verify Reporting
	pageData := &crawler.PageData{
		URL:         taskURL,
		StatusCode:  200,
		Title:       "Seed One Page",
		Description: "A test page",
		Links: []string{
			"https://example.com/discovered-link-1", // New link
			"https://example.com/seed-2",             // Duplicate (already in queue)
		},
	}

	if err := w.ReportResult(ctx, pageData); err != nil {
		t.Fatalf("Reporting result failed: %v", err)
	}

	// 10. Verify that new link was enqueued in Redis, and duplicate was ignored
	qSize, _ := frontier.QueueSize(ctx)
	// Remaining in queue: seed-2 (1 item) + new discovered-link-1 (1 item) = 2 items
	if qSize != 2 {
		t.Errorf("Expected queue size to be 2, got %d", qSize)
	}

	// Verify we can pop seed-2 next
	nextTask, _ := w.PollTask(ctx)
	if nextTask != "https://example.com/seed-2" {
		t.Errorf("Expected next task to be 'https://example.com/seed-2', got %q", nextTask)
	}

	// 11. Verify that the reported page was successfully persisted to PostgreSQL!
	dbTitle, dbDesc, dbStatus, err := pgRepo.GetPageByURL(ctx, "https://example.com/seed-1")
	if err != nil {
		t.Fatalf("Failed to retrieve crawled page from PostgreSQL: %v", err)
	}
	if dbTitle != "Seed One Page" || dbDesc != "A test page" || dbStatus != 200 {
		t.Errorf("Database values mismatch.\nGot: %q, %q, %d\nExpected: %q, %q, %d",
			dbTitle, dbDesc, dbStatus, "Seed One Page", "A test page", 200)
	}
}
