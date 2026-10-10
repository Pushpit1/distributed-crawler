package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"distributed-crawler/pkg/crawler"
	"distributed-crawler/pkg/models"
)

// Worker represents a distributed crawling node that interacts with the Coordinator API.
type Worker struct {
	masterURL   string
	workerID    string
	parser      *crawler.Parser
	robotsCache *crawler.RobotsCache
	client      *http.Client
}

// NewWorker initializes a new worker daemon instance.
func NewWorker(masterURL string, parser *crawler.Parser) *Worker {
	return &Worker{
		masterURL:   masterURL,
		parser:      parser,
		robotsCache: crawler.NewRobotsCache(parser.UserAgent()),
		client: &http.Client{
			Timeout: 10 * time.Second, // Timeout for API network calls to master coordinator
		},
	}
}

// Register connects to the master and requests registration, saving the returned ID.
func (w *Worker) Register(ctx context.Context) error {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown-host"
	}

	reqBody, _ := json.Marshal(models.RegisterRequest{Hostname: hostname})
	apiURL := fmt.Sprintf("%s/register", w.masterURL)

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("master server unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("master returned registration error status: %d", resp.StatusCode)
	}

	var res models.RegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return fmt.Errorf("failed to decode registration response: %w", err)
	}

	w.workerID = res.WorkerID
	log.Printf("🤖 Registered with Master. Received ID: %s\n", w.workerID)
	return nil
}

// PollTask requests the next URL to crawl from the Master.
func (w *Worker) PollTask(ctx context.Context) (string, error) {
	reqBody, _ := json.Marshal(models.PollRequest{WorkerID: w.workerID})
	apiURL := fmt.Sprintf("%s/poll", w.masterURL)

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return "", err
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
		WorkerID:    w.workerID,
		URL:         pageData.URL,
		StatusCode:  pageData.StatusCode,
		Title:       pageData.Title,
		Description: pageData.Description,
		Links:       pageData.Links,
	})

	apiURL := fmt.Sprintf("%s/report", w.masterURL)
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

// StartLoop starts the continuous polling-crawl execution loop.
func (w *Worker) StartLoop(ctx context.Context) {
	log.Println("⚙️ Worker polling loop started. Waiting for crawl tasks...")

	for {
		select {
		case <-ctx.Done():
			log.Println("Shutting down worker polling loop...")
			return
		default:
			// 1. Poll the Coordinator for a task
			taskURL, err := w.PollTask(ctx)
			if err != nil {
				log.Printf("⚠️ Failed to poll task from master: %v. Retrying in 3 seconds...\n", err)
				time.Sleep(3 * time.Second)
				continue
			}

			// 2. If queue is empty, sleep for a bit to avoid hammer-polling the API
			if taskURL == "" {
				log.Println("💤 Crawl queue is empty. Sleeping for 2 seconds...")
				time.Sleep(2 * time.Second)
				continue
			}

			// 3. Verify robots.txt compliance before crawling
			allowed, err := w.robotsCache.IsAllowed(ctx, taskURL)
			if err != nil {
				log.Printf("⚠️ Robots check failed on %s: %v. Proceeding cautiously.\n", taskURL, err)
				allowed = true // Fail open on temporary errors
			}

			if !allowed {
				log.Printf("🚫 Crawl blocked by robots.txt: %s\n", taskURL)
				// Report as Forbidden (403) so Coordinator marks it as visited but records 0 links
				reportData := &crawler.PageData{
					URL:        taskURL,
					StatusCode: http.StatusForbidden,
					Title:      "Blocked by robots.txt",
				}
				if err := w.ReportResult(ctx, reportData); err != nil {
					log.Printf("❌ Failed to report robots block: %v\n", err)
				}
				continue
			}

			log.Printf("🕸️ Crawling task: %s\n", taskURL)

			// 4. Execute page crawl & parse with exponential retries built-in
			crawlCtx, cancel := context.WithTimeout(ctx, 30*time.Second) // Force a 30s fetch boundary (allows retries)
			data, err := w.parser.FetchAndParse(crawlCtx, taskURL)
			cancel()

			if err != nil {
				log.Printf("⚠️ Crawl error on %s: %v\n", taskURL, err)
				// Even if fetch fails, report the status code so coordinator knows it's visited/faulty
				if data == nil {
					data = &crawler.PageData{
						URL:        taskURL,
						StatusCode: http.StatusInternalServerError,
					}
				}
			}

			// 5. Report results back to Master
			if err := w.ReportResult(ctx, data); err != nil {
				log.Printf("❌ Failed to report results to master: %v\n", err)
			}

			// Politeness delay: sleep 500ms between page crawls to be nice to servers
			time.Sleep(500 * time.Millisecond)
		}
	}
}

// ID returns the registered worker ID.
func (w *Worker) ID() string {
	return w.workerID
}
