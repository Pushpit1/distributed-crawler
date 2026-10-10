package crawler

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// setupTestRedis connects to our local Docker Redis and initializes a Frontier instance.
// It returns a cleanup function to flush keys and close the connection.
func setupTestFrontier(t *testing.T) (*Frontier, func()) {
	// Connect to local Redis running in Docker
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	// Quick connectivity ping
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("Docker Redis is not running. Skipping integration tests.")
	}

	// Use isolated test keys to prevent conflict with active workloads
	queueKey := "test:crawler:queue"
	setKey := "test:crawler:visited"

	frontier := NewFrontier(rdb, queueKey, setKey)

	// Clean up any stale state before running tests
	rdb.Del(context.Background(), queueKey, setKey)

	cleanup := func() {
		rdb.Del(context.Background(), queueKey, setKey)
		rdb.Close()
	}

	return frontier, cleanup
}

func TestFrontier_SubmitAndDeduplicate(t *testing.T) {
	frontier, cleanup := setupTestFrontier(t)
	defer cleanup()

	ctx := context.Background()

	// 1. Submit unique URLs
	urls := []string{
		"https://example.com/page-1",
		"https://example.com/page-2",
		"https://example.com/page-3",
	}

	enqueued, err := frontier.Submit(ctx, urls...)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	if enqueued != 3 {
		t.Errorf("Expected 3 enqueued URLs, got %d", enqueued)
	}

	// Check counts
	qSize, _ := frontier.QueueSize(ctx)
	if qSize != 3 {
		t.Errorf("Expected queue size to be 3, got %d", qSize)
	}

	vCount, _ := frontier.VisitedCount(ctx)
	if vCount != 3 {
		t.Errorf("Expected visited count to be 3, got %d", vCount)
	}

	// 2. Resubmit the same URLs (should be deduplicated)
	enqueuedDup, err := frontier.Submit(ctx, urls...)
	if err != nil {
		t.Fatalf("Submit duplicates failed: %v", err)
	}

	if enqueuedDup != 0 {
		t.Errorf("Expected 0 enqueued duplicates, got %d", enqueuedDup)
	}

	// Double check counts are still 3
	qSize, _ = frontier.QueueSize(ctx)
	if qSize != 3 {
		t.Errorf("Expected queue size to remain 3, got %d", qSize)
	}

	// 3. Submit mixed new and duplicate URLs
	mixedUrls := []string{
		"https://example.com/page-1", // duplicate
		"https://example.com/page-4", // new!
		"https://example.com/page-2", // duplicate
	}

	enqueuedMixed, err := frontier.Submit(ctx, mixedUrls...)
	if err != nil {
		t.Fatalf("Submit mixed failed: %v", err)
	}

	if enqueuedMixed != 1 {
		t.Errorf("Expected 1 new URL enqueued from mixed list, got %d", enqueuedMixed)
	}

	// Queue size should now be 4
	qSize, _ = frontier.QueueSize(ctx)
	if qSize != 4 {
		t.Errorf("Expected queue size to be 4, got %d", qSize)
	}
}

func TestFrontier_FIFOFlow(t *testing.T) {
	frontier, cleanup := setupTestFrontier(t)
	defer cleanup()

	ctx := context.Background()

	// Submit URLs in order: Page 1, Page 2, Page 3
	_, _ = frontier.Submit(ctx, "https://example.com/page-1")
	_, _ = frontier.Submit(ctx, "https://example.com/page-2")
	_, _ = frontier.Submit(ctx, "https://example.com/page-3")

	// Verify FIFO: we should pop Page 1 first, then Page 2, then Page 3
	expectedOrder := []string{
		"https://example.com/page-1",
		"https://example.com/page-2",
		"https://example.com/page-3",
	}

	for _, expected := range expectedOrder {
		next, err := frontier.GetNext(ctx)
		if err != nil {
			t.Fatalf("GetNext failed: %v", err)
		}
		if next != expected {
			t.Errorf("Expected to pop %s, got %s", expected, next)
		}
	}

	// Queue should now be empty
	next, err := frontier.GetNext(ctx)
	if err != nil {
		t.Fatalf("GetNext on empty queue failed: %v", err)
	}
	if next != "" {
		t.Errorf("Expected empty string on empty queue, got %s", next)
	}

	qSize, _ := frontier.QueueSize(ctx)
	if qSize != 0 {
		t.Errorf("Expected queue size to be 0, got %d", qSize)
	}
}

func TestFrontier_Clear(t *testing.T) {
	frontier, cleanup := setupTestFrontier(t)
	defer cleanup()

	ctx := context.Background()

	_, _ = frontier.Submit(ctx, "https://example.com/page-1", "https://example.com/page-2")

	// Clear keys
	if err := frontier.Clear(ctx); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}

	// Size metrics should reset to 0
	qSize, _ := frontier.QueueSize(ctx)
	if qSize != 0 {
		t.Errorf("Expected queue size to be 0 after clear, got %d", qSize)
	}

	vCount, _ := frontier.VisitedCount(ctx)
	if vCount != 0 {
		t.Errorf("Expected visited count to be 0 after clear, got %d", vCount)
	}
}

func TestFrontier_Politeness(t *testing.T) {
	frontier, cleanup := setupTestFrontier(t)
	defer cleanup()

	ctx := context.Background()

	// Enqueue two URLs from the SAME host
	_, _ = frontier.Submit(ctx, "https://example.com/1", "https://example.com/2")

	// Pop the first URL with a 1-second politeness delay
	first, err := frontier.GetPoliteTask(ctx, 1*time.Second)
	if err != nil {
		t.Fatalf("First GetPoliteTask failed: %v", err)
	}
	if first != "https://example.com/1" {
		t.Errorf("Expected first URL to be https://example.com/1, got %s", first)
	}

	// Immediately pop the second URL. Since it is from the same host, it should be rate-limited,
	// re-enqueued, and GetPoliteTask should return empty string "" because no other hosts exist.
	second, err := frontier.GetPoliteTask(ctx, 1*time.Second)
	if err != nil {
		t.Fatalf("Second GetPoliteTask failed: %v", err)
	}
	if second != "" {
		t.Errorf("Expected second task to be empty due to rate-limiting, got %s", second)
	}

	// Confirm that the second URL was re-enqueued at the back of the queue (left side)
	qSize, _ := frontier.QueueSize(ctx)
	if qSize != 1 {
		t.Errorf("Expected queue size to remain 1 (re-enqueued), got %d", qSize)
	}

	// Wait for the rate-limit to expire (1.1 seconds)
	time.Sleep(1100 * time.Millisecond)

	// Now it should succeed and return the second URL
	third, err := frontier.GetPoliteTask(ctx, 1*time.Second)
	if err != nil {
		t.Fatalf("Third GetPoliteTask failed: %v", err)
	}
	if third != "https://example.com/2" {
		t.Errorf("Expected third task to be https://example.com/2 after rate-limit expired, got %s", third)
	}
}
