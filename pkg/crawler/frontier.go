package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"distributed-crawler/pkg/models"

	"github.com/redis/go-redis/v9"
)

// leaseRecord stores in-flight task metadata for crash recovery.
type leaseRecord struct {
	RawTask  string `json:"raw_task"`
	URL      string `json:"url"`
	JobID    string `json:"job_id,omitempty"`
	WorkerID string `json:"worker_id"`
	LeasedAt int64  `json:"leased_at"`
}

// Frontier manages the Redis FIFO crawl queue, deduplication set, job pause/cancel flags, and task leases.
type Frontier struct {
	rdb          *redis.Client
	queueKey     string // Redis List key for pending tasks (e.g. "crawler:queue")
	setKey       string // Redis Set key for global visited URLs (e.g. "crawler:visited")
	leaseKey     string // Redis Hash key for in-flight task leases
	pausedSetKey string // Redis Set of paused job IDs
	cancelSetKey string // Redis Set of cancelled job IDs
}

// NewFrontier initializes a Redis-backed URL Frontier.
func NewFrontier(rdb *redis.Client, queueKey, setKey string) *Frontier {
	return &Frontier{
		rdb:          rdb,
		queueKey:     queueKey,
		setKey:       setKey,
		leaseKey:     queueKey + ":leases",
		pausedSetKey: queueKey + ":paused_jobs",
		cancelSetKey: queueKey + ":cancelled_jobs",
	}
}

func pausedQueueKey(baseQueue, jobID string) string {
	return fmt.Sprintf("%s:paused:%s", baseQueue, jobID)
}

func decodeTaskEntry(raw string) models.CrawlTask {
	rawTrimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(rawTrimmed, "{") {
		var task models.CrawlTask
		if err := json.Unmarshal([]byte(rawTrimmed), &task); err == nil && task.URL != "" {
			return task
		}
	}
	return models.CrawlTask{URL: rawTrimmed}
}

func makeLeaseFieldKey(task models.CrawlTask) string {
	if task.JobID != "" {
		return task.JobID + "::" + task.URL
	}
	return task.URL
}

// Submit checks raw URLs against the global deduplication set and enqueues new ones (legacy compatibility).
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
			if err := f.rdb.LPush(ctx, f.queueKey, trimmed).Err(); err != nil {
				return newCount, fmt.Errorf("failed LPUSH for URL %s: %w", trimmed, err)
			}
			newCount++
		}
	}
	return newCount, nil
}

// SubmitJobTasks enqueues structured CrawlTask records scoped by job ID with deduplication per job.
func (f *Frontier) SubmitJobTasks(ctx context.Context, jobID string, tasks ...models.CrawlTask) (int, error) {
	newCount := 0
	dedupKey := f.setKey
	if jobID != "" {
		dedupKey = fmt.Sprintf("%s:job:%s", f.setKey, jobID)
	}

	for _, task := range tasks {
		trimmed := strings.TrimSpace(task.URL)
		if trimmed == "" {
			continue
		}
		task.URL = trimmed
		if task.JobID == "" {
			task.JobID = jobID
		}
		if task.EnqueuedAt.IsZero() {
			task.EnqueuedAt = time.Now().UTC()
		}

		added, err := f.rdb.SAdd(ctx, dedupKey, trimmed).Result()
		if err != nil {
			return newCount, fmt.Errorf("failed job SADD check for URL %s: %w", trimmed, err)
		}
		if added == 1 {
			// Also track globally for status visited metrics
			_, _ = f.rdb.SAdd(ctx, f.setKey, trimmed).Result()

			payload, err := json.Marshal(task)
			if err != nil {
				return newCount, err
			}
			if err := f.rdb.LPush(ctx, f.queueKey, string(payload)).Err(); err != nil {
				return newCount, fmt.Errorf("failed LPUSH task for URL %s: %w", trimmed, err)
			}
			newCount++
		}
	}
	return newCount, nil
}

// RequeueTask pushes an existing structured task back onto the frontier (e.g. retries).
func (f *Frontier) RequeueTask(ctx context.Context, task models.CrawlTask) error {
	payload, err := json.Marshal(task)
	if err != nil {
		return err
	}
	return f.rdb.LPush(ctx, f.queueKey, string(payload)).Err()
}

// PauseJob marks a job paused in Redis so queued tasks are parked until resumed.
func (f *Frontier) PauseJob(ctx context.Context, jobID string) error {
	if jobID == "" {
		return nil
	}
	_ = f.rdb.SRem(ctx, f.cancelSetKey, jobID).Err()
	return f.rdb.SAdd(ctx, f.pausedSetKey, jobID).Err()
}

// ResumeJob clears pause/cancel markers and moves parked tasks back into the active frontier.
func (f *Frontier) ResumeJob(ctx context.Context, jobID string) (int, error) {
	if jobID == "" {
		return 0, nil
	}
	_ = f.rdb.SRem(ctx, f.pausedSetKey, jobID).Err()
	_ = f.rdb.SRem(ctx, f.cancelSetKey, jobID).Err()

	parkedKey := pausedQueueKey(f.queueKey, jobID)
	restored := 0
	for {
		val, err := f.rdb.RPopLPush(ctx, parkedKey, f.queueKey).Result()
		if err == redis.Nil || val == "" {
			break
		}
		if err != nil {
			return restored, err
		}
		restored++
	}
	return restored, nil
}

// CancelJob marks a job cancelled and purges any parked tasks for that job.
func (f *Frontier) CancelJob(ctx context.Context, jobID string) error {
	if jobID == "" {
		return nil
	}
	_ = f.rdb.SRem(ctx, f.pausedSetKey, jobID).Err()
	_ = f.rdb.Del(ctx, pausedQueueKey(f.queueKey, jobID)).Err()
	return f.rdb.SAdd(ctx, f.cancelSetKey, jobID).Err()
}

// GetNext pulls the next raw entry from the frontier queue (Right Pop).
func (f *Frontier) GetNext(ctx context.Context) (string, error) {
	val, err := f.rdb.RPop(ctx, f.queueKey).Result()
	if err == redis.Nil {
		return "", nil
	} else if err != nil {
		return "", fmt.Errorf("failed RPOP from queue: %w", err)
	}
	return val, nil
}

// GetPoliteTask pulls the next URL string respecting domain politeness (legacy signature).
func (f *Frontier) GetPoliteTask(ctx context.Context, politenessDelay time.Duration) (string, error) {
	task, err := f.GetPoliteStructuredTask(ctx, politenessDelay, "anonymous-worker")
	if err != nil || task == nil {
		return "", err
	}
	return task.URL, nil
}

// GetPoliteTaskWithLease pulls the next URL respecting politeness and acquires a lease (legacy signature).
func (f *Frontier) GetPoliteTaskWithLease(ctx context.Context, politenessDelay time.Duration, workerID string) (string, error) {
	task, err := f.GetPoliteStructuredTask(ctx, politenessDelay, workerID)
	if err != nil || task == nil {
		return "", err
	}
	return task.URL, nil
}

// GetPoliteStructuredTask retrieves the next structured CrawlTask, skipping cancelled jobs,
// parking paused jobs, enforcing domain politeness, and recording an atomic lease.
func (f *Frontier) GetPoliteStructuredTask(ctx context.Context, politenessDelay time.Duration, workerID string) (*models.CrawlTask, error) {
	initialSize, err := f.QueueSize(ctx)
	if err != nil {
		return nil, err
	}
	if initialSize == 0 {
		return nil, nil
	}

	maxAttempts := int(initialSize)
	if maxAttempts > 25 {
		maxAttempts = 25
	}
	seenThisPoll := make(map[string]bool, maxAttempts)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		rawEntry, err := f.GetNext(ctx)
		if err != nil {
			return nil, err
		}
		if rawEntry == "" {
			return nil, nil
		}

		// Do not spin endlessly on the exact same rate-limited entry in a single poll sweep
		if seenThisPoll[rawEntry] {
			_ = f.rdb.LPush(ctx, f.queueKey, rawEntry).Err()
			return nil, nil
		}
		seenThisPoll[rawEntry] = true

		task := decodeTaskEntry(rawEntry)

		if task.JobID != "" {
			cancelled, _ := f.rdb.SIsMember(ctx, f.cancelSetKey, task.JobID).Result()
			if cancelled {
				continue // Permanently discard cancelled job tasks
			}
			paused, _ := f.rdb.SIsMember(ctx, f.pausedSetKey, task.JobID).Result()
			if paused {
				_ = f.rdb.LPush(ctx, pausedQueueKey(f.queueKey, task.JobID), rawEntry).Err()
				continue
			}
		}

		parsed, err := url.Parse(task.URL)
		if err != nil || parsed.Host == "" {
			continue
		}
		host := strings.ToLower(parsed.Host)

		effectiveDelay := politenessDelay
		if task.PolitenessMs > 0 {
			effectiveDelay = time.Duration(task.PolitenessMs) * time.Millisecond
		}

		rateLimitKey := fmt.Sprintf("rate_limit:host:%s", host)
		isLimited, err := f.rdb.Exists(ctx, rateLimitKey).Result()
		if err != nil {
			_ = f.rdb.LPush(ctx, f.queueKey, rawEntry).Err()
			return nil, fmt.Errorf("failed to check rate limit in Redis: %w", err)
		}

		if isLimited == 1 {
			_ = f.rdb.LPush(ctx, f.queueKey, rawEntry).Err()
			continue
		}

		if effectiveDelay > 0 {
			_ = f.rdb.Set(ctx, rateLimitKey, "1", effectiveDelay).Err()
		}

		leaseField := makeLeaseFieldKey(task)
		rec := leaseRecord{
			RawTask:  rawEntry,
			URL:      task.URL,
			JobID:    task.JobID,
			WorkerID: workerID,
			LeasedAt: time.Now().Unix(),
		}
		if recBytes, err := json.Marshal(rec); err == nil {
			_ = f.rdb.HSet(ctx, f.leaseKey, leaseField, string(recBytes)).Err()
		}

		return &task, nil
	}

	return nil, nil
}

// AckTask releases an in-flight task lease.
func (f *Frontier) AckTask(ctx context.Context, leaseKeyOrURL string, jobID ...string) error {
	if leaseKeyOrURL == "" {
		return nil
	}
	// Delete direct key and job-prefixed variant for complete cleanup
	keys := []string{leaseKeyOrURL}
	if len(jobID) > 0 && jobID[0] != "" {
		keys = append(keys, jobID[0]+"::"+leaseKeyOrURL)
	}
	return f.rdb.HDel(ctx, f.leaseKey, keys...).Err()
}

// ReclaimExpiredLeases scans active leases and re-enqueues tasks whose duration exceeds timeout.
func (f *Frontier) ReclaimExpiredLeases(ctx context.Context, timeout time.Duration) (int, error) {
	leases, err := f.rdb.HGetAll(ctx, f.leaseKey).Result()
	if err != nil {
		return 0, err
	}

	now := time.Now().Unix()
	timeoutSec := int64(timeout.Seconds())
	reclaimed := 0

	for fieldKey, val := range leases {
		var rec leaseRecord
		if err := json.Unmarshal([]byte(val), &rec); err != nil {
			continue
		}

		if (now - rec.LeasedAt) > timeoutSec {
			if rec.JobID != "" {
				cancelled, _ := f.rdb.SIsMember(ctx, f.cancelSetKey, rec.JobID).Result()
				if cancelled {
					_ = f.rdb.HDel(ctx, f.leaseKey, fieldKey).Err()
					continue
				}
			}

			rawToRequeue := rec.RawTask
			if rawToRequeue == "" {
				rawToRequeue = rec.URL
			}
			_ = f.rdb.LPush(ctx, f.queueKey, rawToRequeue).Err()
			_ = f.rdb.HDel(ctx, f.leaseKey, fieldKey).Err()
			reclaimed++
			log.Printf("♻️ Reclaimed expired task lease for %s (worker: %s)\n", SanitizeForLog(rec.URL), SanitizeForLog(rec.WorkerID))
		}
	}

	return reclaimed, nil
}

// ActiveLeaseCount returns how many crawl tasks are currently leased to workers.
func (f *Frontier) ActiveLeaseCount(ctx context.Context) (int64, error) {
	count, err := f.rdb.HLen(ctx, f.leaseKey).Result()
	if err != nil {
		return 0, err
	}
	return count, nil
}

// QueueSize returns the current number of pending tasks in the active crawl queue.
func (f *Frontier) QueueSize(ctx context.Context) (int64, error) {
	size, err := f.rdb.LLen(ctx, f.queueKey).Result()
	if err != nil {
		return 0, fmt.Errorf("failed LLEN check: %w", err)
	}
	return size, nil
}

// VisitedCount returns the total number of unique URLs registered in the visited set.
func (f *Frontier) VisitedCount(ctx context.Context) (int64, error) {
	count, err := f.rdb.SCard(ctx, f.setKey).Result()
	if err != nil {
		return 0, fmt.Errorf("failed SCARD check: %w", err)
	}
	return count, nil
}

// ClearQueueOnly purges pending queue items and active leases without erasing visited deduplication or DB state.
func (f *Frontier) ClearQueueOnly(ctx context.Context) error {
	keys := []string{f.queueKey, f.leaseKey}
	if pausedPatternKeys, err := f.rdb.Keys(ctx, f.queueKey+":paused:*").Result(); err == nil {
		keys = append(keys, pausedPatternKeys...)
	}
	if err := f.rdb.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("failed to flush frontier queue: %w", err)
	}
	log.Println("🧹 Pending frontier queue and leases flushed (visited set preserved).")
	return nil
}

// Clear flushes the queue, visited sets, pause/cancel markers, and task leases in Redis.
func (f *Frontier) Clear(ctx context.Context) error {
	keys := []string{f.queueKey, f.setKey, f.leaseKey, f.pausedSetKey, f.cancelSetKey}
	if extraKeys, err := f.rdb.Keys(ctx, f.queueKey+":paused:*").Result(); err == nil {
		keys = append(keys, extraKeys...)
	}
	if dedupKeys, err := f.rdb.Keys(ctx, f.setKey+":job:*").Result(); err == nil {
		keys = append(keys, dedupKeys...)
	}
	if err := f.rdb.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("failed to clear frontier keys: %w", err)
	}
	log.Println("🧹 Frontier queue, leases, job sets, and deduplication keys cleared successfully.")
	return nil
}
