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

// Worker represents a distributed crawling node that interacts with the Coordinator API.
type Worker struct {
	masterURL      string
	workerID       string
	status         string
	version        string
	parser         *crawler.Parser
	robotsCache    *crawler.RobotsCache
	client         *http.Client
	mu             sync.Mutex
	tasksActive    int
	tasksCompleted int
	tasksFailed    int
	totalLatencyMs int64
}

// NewWorker initializes a new worker daemon instance.
func NewWorker(masterURL string, parser *crawler.Parser) *Worker {
	return &Worker{
		masterURL:   masterURL,
		status:      models.WorkerStateStarting,
		version:     "1.0.0",
		parser:      parser,
		robotsCache: crawler.NewRobotsCache(parser.UserAgent()),
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// Register connects to the master and requests registration, saving the returned ID.
func (w *Worker) Register(ctx context.Context) error {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "worker-host"
	}

	reqBody, _ := json.Marshal(models.RegisterRequest{
		Hostname: hostname,
		Version:  w.version,
	})
	apiURL := fmt.Sprintf("%s/api/register", w.masterURL)

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		// Fallback to legacy route if api route fails
		apiURL = fmt.Sprintf("%s/register", w.masterURL)
		req, _ = http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(reqBody))
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

	log.Printf("🤖 Registered with Master. Received ID: %s\n", w.workerID)
	return nil
}

// SendHeartbeat dispatches current node telemetry to the master coordinator.
func (w *Worker) SendHeartbeat(ctx context.Context) error {
	w.mu.Lock()
	avgLatency := 0
	if w.tasksCompleted > 0 {
		avgLatency = int(w.totalLatencyMs / int64(w.tasksCompleted))
	}
	payload := models.HeartbeatRequest{
		WorkerID:       w.workerID,
		Status:         w.status,
		TasksActive:    w.tasksActive,
		TasksCompleted: w.tasksCompleted,
		TasksFailed:    w.tasksFailed,
		AvgLatencyMs:   avgLatency,
	}
	w.mu.Unlock()

	reqBody, _ := json.Marshal(payload)
	apiURL := fmt.Sprintf("%s/api/heartbeat", w.masterURL)

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

// PollTask requests the next URL to crawl from the Master.
func (w *Worker) PollTask(ctx context.Context) (string, error) {
	reqBody, _ := json.Marshal(models.PollRequest{WorkerID: w.workerID})
	apiURL := fmt.Sprintf("%s/api/poll", w.masterURL)

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		// Fallback to legacy route
		apiURL = fmt.Sprintf("%s/poll", w.masterURL)
		req, _ = http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(reqBody))
		req.Header.Set("Content-Type", "application/json")
		resp, err = w.client.Do(req)
		if err != nil {
			return "", err
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("master poll error status: %d", resp.StatusCode)
	}

	var res models.PollResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}

	return res.URL, nil
}

// ReportResult sends crawl findings (metadata and links) back to the Coordinator.
func (w *Worker) ReportResult(ctx context.Context, pageData *crawler.PageData) error {
	reqBody, _ := json.Marshal(models.ReportRequest{
		WorkerID:      w.workerID,
		URL:           pageData.URL,
		CanonicalURL:  pageData.CanonicalURL,
		StatusCode:    pageData.StatusCode,
		Title:         pageData.Title,
		Description:   pageData.Description,
		H1:            pageData.H1,
		ContentType:   pageData.ContentType,
		ContentLength: pageData.ContentLength,
		LatencyMs:     pageData.LatencyMs,
		Depth:         pageData.Depth,
		Links:         pageData.Links,
	})

	apiURL := fmt.Sprintf("%s/api/report", w.masterURL)
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		// Fallback to legacy route
		apiURL = fmt.Sprintf("%s/report", w.masterURL)
		req, _ = http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(reqBody))
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

	log.Printf("📊 Report accepted by Master | New Links Enqueued: %d | Total Visited: %d\n",
		res.NewLinksEnqueued, res.TotalVisitedCount)

	return nil
}

// StartLoop starts the continuous polling-crawl execution loop with automatic heartbeats.
func (w *Worker) StartLoop(ctx context.Context) {
	log.Println("⚙️ Worker polling loop started. Waiting for crawl tasks...")

	// 1. Launch background heartbeat ticker (every 5 seconds)
	heartbeatTicker := time.NewTicker(5 * time.Second)
	defer heartbeatTicker.Stop()

	go func() {
		for {
			select {
			case <-ctx.Done():
				// Report STOPPED state on shutdown
				w.mu.Lock()
				w.status = models.WorkerStateStopped
				w.mu.Unlock()
				_ = w.SendHeartbeat(context.Background())
				return
			case <-heartbeatTicker.C:
				_ = w.SendHeartbeat(ctx)
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			log.Println("Shutting down worker polling loop gracefully...")
			w.mu.Lock()
			w.status = models.WorkerStateDraining
			w.mu.Unlock()
			return
		default:
			// 1. Poll the Coordinator for a task
			taskURL, err := w.PollTask(ctx)
			if err != nil {
				log.Printf("⚠️ Failed to poll task from master: %v. Retrying in 3 seconds...\n", err)
				w.mu.Lock()
				w.status = models.WorkerStateIdle
				w.mu.Unlock()
				time.Sleep(3 * time.Second)
				continue
			}

			// 2. If queue is empty, set status to IDLE and sleep
			if taskURL == "" {
				w.mu.Lock()
				w.status = models.WorkerStateIdle
				w.mu.Unlock()
				time.Sleep(2 * time.Second)
				continue
			}

			// 3. Mark state as ACTIVE
			w.mu.Lock()
			w.status = models.WorkerStateActive
			w.tasksActive = 1
			w.mu.Unlock()

			// 4. Verify robots.txt compliance before crawling
			allowed, err := w.robotsCache.IsAllowed(ctx, taskURL)
			if err != nil {
				log.Printf("⚠️ Robots check failed on %s: %v. Proceeding cautiously.\n", taskURL, err)
				allowed = true
			}

			if !allowed {
				log.Printf("🚫 Crawl blocked by robots.txt: %s\n", taskURL)
				reportData := &crawler.PageData{
					URL:        taskURL,
					StatusCode: http.StatusForbidden,
					Title:      "Blocked by robots.txt",
				}
				_ = w.ReportResult(ctx, reportData)

				w.mu.Lock()
				w.tasksActive = 0
				w.tasksCompleted++
				w.status = models.WorkerStateIdle
				w.mu.Unlock()
				continue
			}

			log.Printf("🕸️ Crawling task: %s\n", taskURL)

			// 5. Execute page crawl & parse with exponential retries built-in
			crawlCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			data, err := w.parser.FetchAndParse(crawlCtx, taskURL)
			cancel()

			w.mu.Lock()
			w.tasksActive = 0
			if err != nil {
				w.tasksFailed++
				log.Printf("⚠️ Crawl error on %s: %v\n", taskURL, err)
				if data == nil {
					data = &crawler.PageData{
						URL:        taskURL,
						StatusCode: http.StatusInternalServerError,
						Title:      "Crawl Error",
					}
				}
			} else {
				w.tasksCompleted++
				w.totalLatencyMs += int64(data.LatencyMs)
			}
			w.status = models.WorkerStateIdle
			w.mu.Unlock()

			// 6. Report results back to Master
			if err := w.ReportResult(ctx, data); err != nil {
				log.Printf("❌ Failed to report results to master: %v\n", err)
			}

			// Politeness delay: sleep 500ms between page crawls
			time.Sleep(500 * time.Millisecond)
		}
	}
}

// ID returns the registered worker ID.
func (w *Worker) ID() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.workerID
}
