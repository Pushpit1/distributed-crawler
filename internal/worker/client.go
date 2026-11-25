package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"distributed-crawler/pkg/crawler"
	"distributed-crawler/pkg/models"
)

// Worker represents an independent distributed crawling node communicating with the Coordinator Master.
type Worker struct {
	masterURL      string
	workerID       string
	hostname       string
	status         string
	version        string
	concurrency    int
	startedAt      time.Time
	parser         *crawler.Parser
	robotsCache    *crawler.RobotsCache
	client         *http.Client
	mu             sync.Mutex
	currentTasks   map[int]string
	tasksActive    int
	tasksCompleted int
	tasksFailed    int
	totalLatencyMs int64
}

// NewWorker initializes a new worker daemon instance with default concurrency = 2.
func NewWorker(masterURL string, parser *crawler.Parser) *Worker {
	return NewWorkerWithConcurrency(masterURL, parser, 2)
}

// NewWorkerWithConcurrency initializes a worker daemon with explicit bounded concurrency.
func NewWorkerWithConcurrency(masterURL string, parser *crawler.Parser, concurrency int) *Worker {
	if concurrency <= 0 {
		concurrency = 2
	}
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "worker-host"
	}
	return &Worker{
		masterURL:    masterURL,
		hostname:     hostname,
		status:       models.WorkerStateStarting,
		version:      "2.0.0",
		concurrency:  concurrency,
		startedAt:    time.Now(),
		parser:       parser,
		robotsCache:  crawler.NewRobotsCache(parser.UserAgent()),
		currentTasks: make(map[int]string),
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// SetAllowPrivateSubnets enables loopback crawling strictly for deterministic local integration testing.
func (w *Worker) SetAllowPrivateSubnets(allow bool) {
	w.parser.SetAllowPrivateSubnets(allow)
	w.robotsCache.SetAllowPrivateSubnets(allow)
}

// Register connects to the coordinator master and registers this worker node.
func (w *Worker) Register(ctx context.Context) error {
	reqBody, _ := json.Marshal(models.RegisterRequest{
		Hostname: w.hostname,
		Version:  w.version,
	})
	apiURL := fmt.Sprintf("%s/api/register", w.masterURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		apiURL = fmt.Sprintf("%s/register", w.masterURL)
		req, _ = http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewBuffer(reqBody))
		req.Header.Set("Content-Type", "application/json")
		resp, err = w.client.Do(req)
		if err != nil {
			return fmt.Errorf("master server unreachable: %w", err)
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("master returned registration error status: %d", resp.StatusCode)
	}

	var res models.RegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return fmt.Errorf("failed to decode registration response: %w", err)
	}

	w.mu.Lock()
	w.workerID = res.WorkerID
	w.status = models.WorkerStateIdle
	w.mu.Unlock()

	log.Printf("🤖 Worker registered with Master: id=%s host=%s concurrency=%d\n", w.workerID, w.hostname, w.concurrency)
	return nil
}

// SendHeartbeat dispatches current node telemetry to the master coordinator.
func (w *Worker) SendHeartbeat(ctx context.Context) error {
	w.mu.Lock()
	if w.workerID == "" {
		w.mu.Unlock()
		return nil
	}
	avgLatency := 0
	if w.tasksCompleted > 0 {
		avgLatency = int(w.totalLatencyMs / int64(w.tasksCompleted))
	}
	var activeTask string
	for _, t := range w.currentTasks {
		activeTask = t
		break
	}
	payload := models.HeartbeatRequest{
		WorkerID:       w.workerID,
		Hostname:       w.hostname,
		Version:        w.version,
		Status:         w.status,
		CurrentTask:    activeTask,
		TasksActive:    w.tasksActive,
		TasksCompleted: w.tasksCompleted,
		TasksFailed:    w.tasksFailed,
		AvgLatencyMs:   avgLatency,
		UptimeSec:      int64(time.Since(w.startedAt).Seconds()),
	}
	w.mu.Unlock()

	reqBody, _ := json.Marshal(payload)
	apiURL := fmt.Sprintf("%s/api/heartbeat", w.masterURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// PollStructuredTask claims the next structured crawl task (with lease & job policy) from Master.
func (w *Worker) PollStructuredTask(ctx context.Context) (*models.PollResponse, error) {
	reqBody, _ := json.Marshal(models.PollRequest{WorkerID: w.ID()})
	apiURL := fmt.Sprintf("%s/api/poll", w.masterURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		apiURL = fmt.Sprintf("%s/poll", w.masterURL)
		req, _ = http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewBuffer(reqBody))
		req.Header.Set("Content-Type", "application/json")
		resp, err = w.client.Do(req)
		if err != nil {
			return nil, err
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("master poll error status: %d", resp.StatusCode)
	}

	var res models.PollResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return &res, nil
}

// PollTask requests the next URL string to crawl from Master (backwards compatible).
func (w *Worker) PollTask(ctx context.Context) (string, error) {
	res, err := w.PollStructuredTask(ctx)
	if err != nil || res == nil {
		return "", err
	}
	return res.URL, nil
}

// ReportStructuredResult submits full crawl results (including job metadata, errors, and lease key) to Master.
func (w *Worker) ReportStructuredResult(ctx context.Context, payload models.ReportRequest) error {
	if payload.WorkerID == "" {
		payload.WorkerID = w.ID()
	}
	reqBody, _ := json.Marshal(payload)

	apiURL := fmt.Sprintf("%s/api/report", w.masterURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		apiURL = fmt.Sprintf("%s/report", w.masterURL)
		req, _ = http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewBuffer(reqBody))
		req.Header.Set("Content-Type", "application/json")
		resp, err = w.client.Do(req)
		if err != nil {
			return err
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("master report status error: %d", resp.StatusCode)
	}

	var res models.ReportResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return err
	}

	log.Printf("📊 Report accepted | url=%s status=%d new_links=%d visited_total=%d\n",
		crawler.SanitizeForLog(payload.URL), payload.StatusCode, res.NewLinksEnqueued, res.TotalVisitedCount)
	return nil
}

// ReportResult sends PageData findings to Master (backwards compatible).
func (w *Worker) ReportResult(ctx context.Context, pageData *crawler.PageData) error {
	return w.ReportStructuredResult(ctx, models.ReportRequest{
		WorkerID:      w.ID(),
		URL:           pageData.URL,
		FinalURL:      pageData.FinalURL,
		CanonicalURL:  pageData.CanonicalURL,
		StatusCode:    pageData.StatusCode,
		Title:         pageData.Title,
		Description:   pageData.Description,
		H1:            pageData.H1,
		Headings:      pageData.Headings,
		ExtractedText: pageData.ExtractedText,
		ContentType:   pageData.ContentType,
		ContentLength: pageData.ContentLength,
		LatencyMs:     pageData.LatencyMs,
		Depth:         pageData.Depth,
		Retries:       pageData.Retries,
		Links:         pageData.Links,
		InternalLinks: pageData.InternalLinks,
		ExternalLinks: pageData.ExternalLinks,
	})
}

// StartLoop launches the heartbeat reporter and bounded worker goroutine pool until ctx is cancelled.
func (w *Worker) StartLoop(ctx context.Context) {
	log.Printf("⚙️ Worker daemon started (%d concurrent goroutines). Waiting for crawl tasks...\n", w.concurrency)

	var wg sync.WaitGroup

	// 1. Background Heartbeat Loop
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		// Immediate initial heartbeat
		_ = w.SendHeartbeat(ctx)

		for {
			select {
			case <-ctx.Done():
				w.mu.Lock()
				w.status = models.WorkerStateStopped
				w.mu.Unlock()
				shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				_ = w.SendHeartbeat(shutCtx)
				cancel()
				return
			case <-ticker.C:
				_ = w.SendHeartbeat(ctx)
			}
		}
	}()

	// 2. Bounded Worker Slot Pool
	for slot := 0; slot < w.concurrency; slot++ {
		wg.Add(1)
		go func(slotID int) {
			defer wg.Done()
			w.runWorkerSlot(ctx, slotID)
		}(slot)
	}

	<-ctx.Done()
	log.Println("🛑 Shutdown signal received — draining active crawl slots...")
	w.mu.Lock()
	w.status = models.WorkerStateDraining
	w.mu.Unlock()

	wg.Wait()
	log.Println("✅ Worker daemon shut down cleanly.")
}

func (w *Worker) runWorkerSlot(ctx context.Context, slotID int) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		task, err := w.PollStructuredTask(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("⚠️ [slot-%d] Master poll failed: %v. Retrying in 2s...\n", slotID, err)
			w.updateState()
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}

		if task == nil || task.URL == "" {
			w.updateState()
			select {
			case <-ctx.Done():
				return
			case <-time.After(1500 * time.Millisecond):
			}
			continue
		}

		w.markSlotActive(slotID, task.URL)
		w.runWorkerSlotTask(ctx, slotID, task)
		w.markSlotIdle(slotID)

		select {
		case <-ctx.Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// runWorkerSlotTask validates policy, checks robots.txt, crawls URL, and reports structured findings.
func (w *Worker) runWorkerSlotTask(ctx context.Context, slotID int, task *models.PollResponse) {
	// Enforced domain allowlist & exclusion pattern filter
	if ok, reason := crawler.MatchesURLPolicy(task.URL, task.AllowedDomains, task.ExcludePatterns); !ok {
		log.Printf("⏭️ [slot-%d] Skipping %s by job URL policy (%s)\n", slotID, crawler.SanitizeForLog(task.URL), reason)
		_ = w.ReportStructuredResult(ctx, models.ReportRequest{
			WorkerID:     w.ID(),
			JobID:        task.JobID,
			LeaseKey:     task.LeaseKey,
			URL:          task.URL,
			StatusCode:   http.StatusForbidden,
			Title:        "Excluded by Job Policy",
			Depth:        task.Depth,
			ErrorType:    "POLICY_EXCLUDED",
			ErrorMessage: reason,
		})
		return
	}

	// Check robots.txt compliance (SSRF-safe transport + cached per host)
	allowed, err := w.robotsCache.IsAllowed(ctx, task.URL)
	if err != nil {
		log.Printf("🚫 [slot-%d] Robots/SSRF check blocked %s: %v\n", slotID, crawler.SanitizeForLog(task.URL), err)
		errType := "ROBOTS_ERROR"
		if err != nil {
			errType = "SSRF_OR_ROBOTS_BLOCKED"
		}
		_ = w.ReportStructuredResult(context.Background(), models.ReportRequest{
			WorkerID:      w.ID(),
			JobID:         task.JobID,
			LeaseKey:      task.LeaseKey,
			URL:           task.URL,
			StatusCode:    http.StatusForbidden,
			Title:         "Blocked by Security / Robots Policy",
			Depth:         task.Depth,
			RobotsBlocked: true,
			ErrorType:     errType,
			ErrorMessage:  err.Error(),
		})
		w.recordTaskOutcome(0, true)
		return
	}

	if !allowed {
		log.Printf("🚫 [slot-%d] Crawl disallowed by robots.txt: %s\n", slotID, crawler.SanitizeForLog(task.URL))
		_ = w.ReportStructuredResult(context.Background(), models.ReportRequest{
			WorkerID:      w.ID(),
			JobID:         task.JobID,
			LeaseKey:      task.LeaseKey,
			URL:           task.URL,
			StatusCode:    http.StatusForbidden,
			Title:         "Disallowed by robots.txt",
			Depth:         task.Depth,
			RobotsBlocked: true,
			ErrorType:     "ROBOTS_DISALLOWED",
			ErrorMessage:  "Path disallowed by host robots.txt policy",
		})
		w.recordTaskOutcome(0, false)
		return
	}

	log.Printf("🕸️ [slot-%d] Crawling URL (job=%s depth=%d): %s\n", slotID, task.JobID, task.Depth, crawler.SanitizeForLog(task.URL))

	crawlCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	data, fetchErr := w.parser.FetchAndParse(crawlCtx, task.URL)
	cancel()

	report := models.ReportRequest{
		WorkerID: w.ID(),
		JobID:    task.JobID,
		LeaseKey: task.LeaseKey,
		URL:      task.URL,
		Depth:    task.Depth,
	}

	if fetchErr != nil {
		if data != nil {
			report.FinalURL = data.FinalURL
			report.StatusCode = data.StatusCode
			report.LatencyMs = data.LatencyMs
			report.Retries = data.Retries
		}
		if report.StatusCode == 0 {
			report.StatusCode = http.StatusBadGateway
		}
		report.Title = fmt.Sprintf("Crawl Error (%d)", report.StatusCode)
		report.ErrorType = "FETCH_ERROR"
		report.ErrorMessage = fetchErr.Error()
		w.recordTaskOutcome(report.LatencyMs, true)
	} else if data != nil {
		report.FinalURL = data.FinalURL
		report.CanonicalURL = data.CanonicalURL
		report.StatusCode = data.StatusCode
		report.Title = data.Title
		report.Description = data.Description
		report.H1 = data.H1
		report.Headings = data.Headings
		report.ExtractedText = data.ExtractedText
		report.ContentType = data.ContentType
		report.ContentLength = data.ContentLength
		report.LatencyMs = data.LatencyMs
		report.Retries = data.Retries
		report.Links = data.Links
		report.InternalLinks = data.InternalLinks
		report.ExternalLinks = data.ExternalLinks
		w.recordTaskOutcome(data.LatencyMs, false)
	}

	repCtx, repCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := w.ReportStructuredResult(repCtx, report); err != nil {
		log.Printf("❌ [slot-%d] Failed to report task result: %v\n", slotID, err)
	}
	repCancel()
}

func (w *Worker) markSlotActive(slotID int, taskURL string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.currentTasks[slotID] = taskURL
	w.tasksActive = len(w.currentTasks)
	w.status = models.WorkerStateActive
}

func (w *Worker) markSlotIdle(slotID int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.currentTasks, slotID)
	w.tasksActive = len(w.currentTasks)
	if w.tasksActive == 0 && w.status != models.WorkerStateDraining && w.status != models.WorkerStateStopped {
		w.status = models.WorkerStateIdle
	}
}

func (w *Worker) updateState() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.tasksActive = len(w.currentTasks)
	if w.tasksActive == 0 && w.status != models.WorkerStateDraining && w.status != models.WorkerStateStopped {
		w.status = models.WorkerStateIdle
	}
}

func (w *Worker) recordTaskOutcome(latencyMs int, failed bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if failed {
		w.tasksFailed++
	} else {
		w.tasksCompleted++
		w.totalLatencyMs += int64(latencyMs)
	}
}

// ID returns the registered worker ID safely.
func (w *Worker) ID() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.workerID
}
