package db

import (
	"context"
	"database/sql"
	"testing"
)

func TestPostgresRepository_SaveAndGet(t *testing.T) {
	// Connect string pointing to host port 5433 (which maps to Postgres inside Docker)
	connStr := "postgres://crawler_user:crawler_password@localhost:5433/crawler_db?sslmode=disable"

	// 1. Initialize repo (runs migrations)
	repo, err := NewPostgresRepository(connStr)
	if err != nil {
		t.Skip("PostgreSQL Docker container is not running. Skipping integration tests.")
	}
	defer repo.Close()

	ctx := context.Background()

	// Truncate tables to ensure a clean state before testing
	err = repo.Truncate(ctx)
	if err != nil {
		t.Fatalf("Failed to clean database tables: %v", err)
	}

	// 2. Test saving a new page
	urlStr := "https://golang.org/doc/install"
	title := "Installing Go"
	desc := "Learn how to download and install Go."
	status := 200

	pageID, err := repo.SavePage(ctx, urlStr, title, desc, status)
	if err != nil {
		t.Fatalf("SavePage failed: %v", err)
	}

	if pageID <= 0 {
		t.Errorf("Expected positive page ID, got %d", pageID)
	}

	// 3. Verify domain was automatically created
	var hostCount int
	err = repo.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM domains WHERE host = $1", "golang.org").Scan(&hostCount)
	if err != nil {
		t.Fatalf("Querying domains failed: %v", err)
	}
	if hostCount != 1 {
		t.Errorf("Expected 1 domain registered for 'golang.org', got %d", hostCount)
	}

	// 4. Test retrieving the saved page
	getT, getD, getS, err := repo.GetPageByURL(ctx, urlStr)
	if err != nil {
		t.Fatalf("GetPageByURL failed: %v", err)
	}
	if getT != title || getD != desc || getS != status {
		t.Errorf("Retrieved values mismatch.\nGot: %q, %q, %d\nExpected: %q, %q, %d",
			getT, getD, getS, title, desc, status)
	}

	// 5. Test Upsert: Save the exact same URL but with an updated title (Recrawl simulation)
	updatedTitle := "Installing Go (Updated Version)"
	newPageID, err := repo.SavePage(ctx, urlStr, updatedTitle, desc, status)
	if err != nil {
		t.Fatalf("SavePage upsert failed: %v", err)
	}

	if newPageID != pageID {
		t.Errorf("Expected page ID to remain identical on update. Got %d, expected %d", newPageID, pageID)
	}

	// Verify update was saved
	resTitle, _, _, _ := repo.GetPageByURL(ctx, urlStr)
	if resTitle != updatedTitle {
		t.Errorf("Expected updated title %q, got %q", updatedTitle, resTitle)
	}

	// 6. Test getting a non-existent page
	_, _, _, err = repo.GetPageByURL(ctx, "https://nonexistent.com")
	if err != sql.ErrNoRows {
		t.Errorf("Expected sql.ErrNoRows for nonexistent URL, got: %v", err)
	}
}
