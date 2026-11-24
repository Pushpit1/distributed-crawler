package crawler

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Frontier manages the crawl queue (FIFO), visited deduplication set, and task leases in Redis.
type Frontier struct {
	rdb      *redis.Client
	queueKey string // Redis List key for pending URLs (e.g. "crawler:queue")
	setKey   string // Redis Set key for visited URLs (e.g. "crawler:visited")
	leaseKey string // Redis Hash key for in-flight task leases (e.g. "crawler:leases")
}

// NewFrontier initializes a new Redis-backed URL Frontier.
func NewFrontier(rdb *redis.Client, queueKey, setKey string) *Frontier {
	return &Frontier{
		rdb:      rdb,
		queueKey: queueKey,
		setKey:   setKey,
		leaseKey: queueKey + ":leases",
	}
}

// Submit checks a list of URLs against the deduplication set and enqueues new ones.
// It returns the number of newly enqueued URLs.
func (f *Frontier) Submit(ctx context.Context, urls ...string) (int, error) {
	newCount := 0

	for _, rawURL := range urls {
		trimmed := strings.TrimSpace(rawURL)
		if trimmed == "" {
			continue
		}

		added, err := f.rdb.SAdd(ctx, f.setKey, trimmed).Result()
		if err != nil {
			return newCount, fmt.Errorf("failed SADD check for URL %s: %w", trimmed, err)
		}

		if added == 1 {
			err := f.rdb.LPush(ctx, f.queueKey, trimmed).Err()
			if err != nil {
				return newCount, fmt.Errorf("failed LPUSH for URL %s: %w", trimmed, err)
			}
			newCount++
		}
	}

	return newCount, nil
}

// GetNext pulls the next URL from the frontier queue (Right Pop).
func (f *Frontier) GetNext(ctx context.Context) (string, error) {
	val, err := f.rdb.RPop(ctx, f.queueKey).Result()
	if err == redis.Nil {
		return "", nil
	} else if err != nil {
		return "", fmt.Errorf("failed RPOP from queue: %w", err)
	}

	return val, nil
}

// GetPoliteTask retrieves the next crawl task, enforcing a politeness rate-limit delay per domain.
func (f *Frontier) GetPoliteTask(ctx context.Context, politenessDelay time.Duration) (string, error) {
	return f.GetPoliteTaskWithLease(ctx, politenessDelay, "anonymous-worker")
}

// GetPoliteTaskWithLease retrieves the next crawl task and records an in-flight visibility lease.
func (f *Frontier) GetPoliteTaskWithLease(ctx context.Context, politenessDelay time.Duration, workerID string) (string, error) {
	maxAttempts := 10

	for attempt := 0; attempt < maxAttempts; attempt++ {
		uStr, err := f.GetNext(ctx)
		if err != nil {
			return "", err
		}
		if uStr == "" {
			return "", nil
		}

		parsed, err := url.Parse(uStr)
		if err != nil || parsed.Host == "" {
			continue
		}
		host := strings.ToLower(parsed.Host)

		rateLimitKey := fmt.Sprintf("rate_limit:host:%s", host)
		isLimited, err := f.rdb.Exists(ctx, rateLimitKey).Result()
		if err != nil {
			return "", fmt.Errorf("failed to check rate limit in Redis: %w", err)
		}

		if isLimited == 1 {
			// Domain is rate-limited; re-enqueue to back of queue
			_ = f.rdb.LPush(ctx, f.queueKey, uStr).Err()
			continue
		}

		if politenessDelay > 0 {
			_ = f.rdb.Set(ctx, rateLimitKey, "1", politenessDelay).Err()
		}

		// Record task lease: stores workerID and lease start timestamp
		leaseVal := fmt.Sprintf("%s:%d", workerID, time.Now().Unix())
		_ = f.rdb.HSet(ctx, f.leaseKey, uStr, leaseVal).Err()

		return uStr, nil
	}

	return "", nil
}

// AckTask acknowledges task completion and releases the in-flight lease.
func (f *Frontier) AckTask(ctx context.Context, taskURL string) error {
	return f.rdb.HDel(ctx, f.leaseKey, taskURL).Err()
}

// ReclaimExpiredLeases scans active leases and re-enqueues tasks whose lease duration exceeds timeout.
func (f *Frontier) ReclaimExpiredLeases(ctx context.Context, timeout time.Duration) (int, error) {
	leases, err := f.rdb.HGetAll(ctx, f.leaseKey).Result()
	if err != nil {
		return 0, err
	}

	now := time.Now().Unix()
	timeoutSec := int64(timeout.Seconds())
	reclaimed := 0

	for taskURL, val := range leases {
		parts := strings.Split(val, ":")
		if len(parts) < 2 {
			continue
		}

		ts, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			continue
		}

		if (now - ts) > timeoutSec {
			// Lease expired! Reclaim task by pushing back into FIFO queue
			_ = f.rdb.LPush(ctx, f.queueKey, taskURL).Err()
			_ = f.rdb.HDel(ctx, f.leaseKey, taskURL).Err()
			reclaimed++
			log.Printf("♻️ Reclaimed abandoned task lease for %s (worker: %s)\n", taskURL, parts[0])
		}
	}

	return reclaimed, nil
}

// QueueSize returns the current number of pending URLs in the crawl queue.
func (f *Frontier) QueueSize(ctx context.Context) (int64, error) {
	size, err := f.rdb.LLen(ctx, f.queueKey).Result()
	if err != nil {
		return 0, fmt.Errorf("failed LLEN check: %w", err)
	}
	return size, nil
}

// VisitedCount returns the total number of unique URLs crawled so far.
func (f *Frontier) VisitedCount(ctx context.Context) (int64, error) {
	count, err := f.rdb.SCard(ctx, f.setKey).Result()
	if err != nil {
		return 0, fmt.Errorf("failed SCARD check: %w", err)
	}
	return count, nil
}

// Clear flushes the queue, visited set, and task leases in Redis.
func (f *Frontier) Clear(ctx context.Context) error {
	err := f.rdb.Del(ctx, f.queueKey, f.setKey, f.leaseKey).Err()
	if err != nil {
		return fmt.Errorf("failed to clear frontier keys: %w", err)
	}
	log.Println("🧹 Frontier queue, leases, and deduplication set cleared successfully.")
	return nil
}
