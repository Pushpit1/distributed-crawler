package crawler

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"time"

	"github.com/redis/go-redis/v9"
)

// Frontier manages the crawl queue (FIFO) and visited deduplication set in Redis.
type Frontier struct {
	rdb      *redis.Client
	queueKey string // Redis List key for pending URLs (e.g. "crawler:queue")
	setKey   string // Redis Set key for visited URLs (e.g. "crawler:visited")
}

// NewFrontier initializes a new Redis-backed URL Frontier.
func NewFrontier(rdb *redis.Client, queueKey, setKey string) *Frontier {
	return &Frontier{
		rdb:      rdb,
		queueKey: queueKey,
		setKey:   setKey,
	}
}

// Submit checks a list of URLs against the deduplication set and enqueues new ones.
// It returns the number of newly enqueued URLs.
func (f *Frontier) Submit(ctx context.Context, urls ...string) (int, error) {
	newCount := 0

	for _, rawURL := range urls {
		if rawURL == "" {
			continue
		}

		// SADD adds elements to the visited set.
		// Returns 1 if the element is new and was added.
		// Returns 0 if the element already existed in the set.
		// This is an atomic operation performed in-memory by Redis.
		added, err := f.rdb.SAdd(ctx, f.setKey, rawURL).Result()
		if err != nil {
			return newCount, fmt.Errorf("failed SADD check for URL %s: %w", rawURL, err)
		}

		if added == 1 {
			// The URL is unique! Push it to our FIFO queue (Left Push)
			err := f.rdb.LPush(ctx, f.queueKey, rawURL).Err()
			if err != nil {
				return newCount, fmt.Errorf("failed LPUSH for URL %s: %w", rawURL, err)
			}
			newCount++
		}
	}

	return newCount, nil
}

// GetNext pulls the next URL from the frontier queue (Right Pop).
// Returns an empty string if the queue is empty.
func (f *Frontier) GetNext(ctx context.Context) (string, error) {
	// RPOP removes and returns the last element of the list.
	val, err := f.rdb.RPop(ctx, f.queueKey).Result()
	if err == redis.Nil {
		// Redis returns redis.Nil when the key does not exist or list is empty
		return "", nil
	} else if err != nil {
		return "", fmt.Errorf("failed RPOP from queue: %w", err)
	}

	return val, nil
}

// GetPoliteTask retrieves the next crawl task, enforcing a politeness rate-limit delay per domain.
// It checks if the host was crawled too recently. If it was, the URL is re-queued,
// and it tries to find another URL from a different domain.
func (f *Frontier) GetPoliteTask(ctx context.Context, politenessDelay time.Duration) (string, error) {
	// We will inspect up to 10 URLs in the queue to find one that is not rate-limited.
	// This prevents infinite loops if the queue contains only rate-limited domains.
	maxAttempts := 10

	for attempt := 0; attempt < maxAttempts; attempt++ {
		// Pop the next URL from the queue
		uStr, err := f.GetNext(ctx)
		if err != nil {
			return "", err
		}
		if uStr == "" {
			return "", nil // Queue is empty
		}

		parsed, err := url.Parse(uStr)
		if err != nil {
			// If URL is corrupt, ignore it and continue
			continue
		}
		host := parsed.Host
		if host == "" {
			continue
		}

		// Check if this host is currently rate-limited in Redis
		rateLimitKey := fmt.Sprintf("rate_limit:host:%s", host)
		isLimited, err := f.rdb.Exists(ctx, rateLimitKey).Result()
		if err != nil {
			return "", fmt.Errorf("failed to check rate limit in Redis: %w", err)
		}

		if isLimited == 1 {
			// Host is rate-limited! Re-enqueue this URL to the back (left) of the queue.
			// This gives it another chance later while allowing other domains to be processed.
			err := f.rdb.LPush(ctx, f.queueKey, uStr).Err()
			if err != nil {
				return "", fmt.Errorf("failed to re-enqueue rate-limited URL: %w", err)
			}
			continue
		}

		// Host is NOT rate-limited!
		// Set the rate-limit flag in Redis with a TTL of our politeness delay if it is greater than 0.
		if politenessDelay > 0 {
			err = f.rdb.Set(ctx, rateLimitKey, "1", politenessDelay).Err()
			if err != nil {
				return "", fmt.Errorf("failed to set rate limit in Redis: %w", err)
			}
		}

		return uStr, nil
	}

	return "", nil // All inspected items were rate-limited
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

// Clear flushes both the queue and visited set keys in Redis (useful for resets).
func (f *Frontier) Clear(ctx context.Context) error {
	err := f.rdb.Del(ctx, f.queueKey, f.setKey).Err()
	if err != nil {
		return fmt.Errorf("failed to clear frontier keys: %w", err)
	}
	log.Println("🧹 Frontier queue and deduplication set cleared successfully.")
	return nil
}
