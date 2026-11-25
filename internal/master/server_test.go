package master

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"distributed-crawler/internal/worker"
	"distributed-crawler/pkg/crawler"
	"distributed-crawler/pkg/db"
	"distributed-crawler/pkg/models"

	"github.com/redis/go-redis/v9"
)

func getTestInfraAddresses() (redisAddr, dbURL string) {
	redisAddr = os.Getenv("TEST_REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}
	dbURL = os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://crawler_user:crawler_password@localhost:5433/crawler_db?sslmode=disable"
	}
	return redisAddr, dbURL
}

func TestServerSecurityMetricsAndAdminReset(t *testing.T) {
	t.Setenv("ADMIN_API_TOKEN", "super-secret-admin-token")
	t.Setenv("ENABLE_PRODUCTION_RESET", "true")

	srv := NewCoordinatorServer(nil, nil, 0)
	handler := srv.Router("")

	// 1. Health & Security Headers & Request ID
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected /health 200, got %d", w.Code)
	}
	if w.Header().Get("X-Request-ID") == "" {
		t.Error("Expected X-Request-ID header on response")
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("Expected nosniff security header")
	}

	// 2. CORS preflight
	optReq := httptest.NewRequest(http.MethodOptions, "/api/status", nil)
	optW := httptest.NewRecorder()
	handler.ServeHTTP(optW, optReq)
	if optW.Code != http.StatusOK {
		t.Errorf("Expected 200 on OPTIONS, got %d", optW.Code)
	}

	// 3. Prometheus Metrics text format verification
	metReq := httptest.NewRequest(http.MethodGet, "/api/metrics", nil)
	metW := httptest.NewRecorder()
	handler.ServeHTTP(metW, metReq)
	body := metW.Body.String()
	if !strings.Contains(body, "crawl_requests_total") || !strings.Contains(body, "crawl_queue_depth") {
		t.Errorf("Expected Prometheus exposition metrics, got: %s", body)
	}

	// 4. SSRF blocking on /api/submit (must reject 169.254.169.254 / 127.0.0.1 immediately)
	ssrfReq := httptest.NewRequest(http.MethodPost, "/api/submit", strings.NewReader(`{"urls":["http://169.254.169.254/latest/meta-data/"]}`))
	ssrfW := httptest.NewRecorder()
	handler.ServeHTTP(ssrfW, ssrfReq)
	if ssrfW.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 SSRF rejection on /api/submit, got %d", ssrfW.Code)
	}

	// 5. Unauthorized reset rejected when ADMIN_API_TOKEN is set
	unauthReset := httptest.NewRequest(http.MethodPost, "/api/reset", strings.NewReader(`{"level":"queue"}`))
	unauthW := httptest.NewRecorder()
	handler.ServeHTTP(unauthW, unauthReset)
	if unauthW.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 for missing admin token on queue reset, got %d", unauthW.Code)
	}

	// 6. Production reset with valid admin token but WRONG phrase must fail with 403
	badPhrase := httptest.NewRequest(http.MethodPost, "/api/reset", strings.NewReader(`{"level":"production","confirmation_phrase":"wrong"}`))
	badPhrase.Header.Set("X-Admin-Token", "super-secret-admin-token")
	badPhraseW := httptest.NewRecorder()
	handler.ServeHTTP(badPhraseW, badPhrase)
	if badPhraseW.Code != http.StatusForbidden {
		t.Errorf("Expected 403 for wrong confirmation phrase, got %d", badPhraseW.Code)
	}

	// 7. Production reset with valid admin token AND WIPE ALL DATA passes cleanly
	goodProd := httptest.NewRequest(http.MethodPost, "/api/reset", strings.NewReader(`{"level":"production","confirmation_phrase":"WIPE ALL DATA"}`))
	goodProd.Header.Set("X-Admin-Token", "super-secret-admin-token")
	goodProdW := httptest.NewRecorder()
	handler.ServeHTTP(goodProdW, goodProd)
	if goodProdW.Code != http.StatusOK {
		t.Errorf("Expected 200 for authorized production reset, got %d (%s)", goodProdW.Code, goodProdW.Body.String())
	}
}

func TestEndToEndDistributedWorkflow_MultiWorkerAndLeaseRecovery(t *testing.T) {
	redisAddr, dbURL := getTestInfraAddresses()
	ctx := context.Background()

	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	defer rdb.Close()

	pingCtx, cancelPing := context.WithTimeout(ctx, 2*time.Second)
	defer cancelPing()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		t.Skipf("Redis unavailable at %s: %v", redisAddr, err)
	}

	pgRepo, err := db.NewPostgresRepository(dbURL)
	if err != nil {
		t.Skipf("PostgreSQL unavailable at %s: %v", dbURL, err)
	}
	defer pgRepo.Close()

	// Serialize shared PostgreSQL integration tests across packages
	if unlock, lockErr := pgRepo.AcquireAdvisoryLock(ctx, 99887766); lockErr == nil {
		defer unlock()
	}

	// Clean DB & isolated Redis frontier keys
	if err := pgRepo.Truncate(ctx); err != nil {
		t.Fatalf("Truncate failed: %v", err)
	}
	queueKey := "e2e:crawler:queue"
	setKey := "e2e:crawler:visited"
	frontier := crawler.NewFrontier(rdb, queueKey, setKey)
	_ = frontier.Clear(ctx)
	defer frontier.Clear(ctx)

	// 1. Controlled local multi-page test site (deterministic, zero external internet flakiness)
	testSite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "User-agent: *\nDisallow: /secret\n")
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><head><title>E2E Root Hub</title><meta name="description" content="Distributed crawler seed page"></head>
				<body><h1>Root Index</h1><p>Explore distributed web indexing.</p>
				<a href="/docs">Docs</a><a href="/about">About</a><a href="/secret">Secret Blocked</a><a href="/broken">Broken Link</a>
				</body></html>`)
		case "/docs":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><head><title>Architecture Docs</title></head>
				<body><h1>Architecture</h1><h2>Redis Leases</h2>
				<a href="/docs/deep">Deep Doc</a></body></html>`)
		case "/about":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><head><title>About Platform</title></head><body><h1>About Us</h1><p>Go worker fleet telemetry.</p></body></html>`)
		case "/docs/deep":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><head><title>Deep Page Depth 2</title></head><body><h1>Depth Two</h1><a href="/docs/too-deep">Too Deep</a></body></html>`)
		case "/docs/too-deep":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><head><title>Should Not Be Reached</title></head><body><h1>Depth Three Exceeded</h1></body></html>`)
		case "/secret":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><head><title>Disallowed Secret</title></head><body>Forbidden</body></html>`)
		case "/broken":
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, "Simulated 500 error")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer testSite.Close()

	// 2. Start Master Coordinator Server (politeness 20ms, lease timeout 1s for recovery test)
	srv := NewCoordinatorServer(frontier, pgRepo, 20*time.Millisecond)
	defer srv.Close()
	srv.SetAllowPrivateSubnets(true)
	srv.SetLeaseTimeout(1 * time.Second)
	ts := httptest.NewServer(srv.Router(""))
	defer func() {
		ts.CloseClientConnections()
		ts.Close()
		testSite.CloseClientConnections()
	}()

	// Verify /ready reports postgres + redis healthy
	readyResp, err := http.Get(ts.URL + "/ready")
	if err != nil || readyResp.StatusCode != http.StatusOK {
		t.Fatalf("Expected /ready 200, got %v %+v", err, readyResp)
	}
	readyResp.Body.Close()

	// 3. Submit structured crawl job (max_depth = 2, max_pages = 20)
	submitPayload, _ := json.Marshal(models.SubmitSeedsRequest{
		Name:     "E2E Full Workflow",
		URLs:     []string{testSite.URL + "/"},
		MaxDepth: 2,
		MaxPages: 20,
	})
	subResp, err := http.Post(ts.URL+"/api/submit", "application/json", bytes.NewReader(submitPayload))
	if err != nil || subResp.StatusCode != http.StatusOK {
		t.Fatalf("Submit job failed: %v", err)
	}
	var subBody models.SubmitSeedsResponse
	_ = json.NewDecoder(subResp.Body).Decode(&subBody)
	subResp.Body.Close()
	jobID := subBody.JobID

	// 4. Launch TWO REAL independent Go Workers concurrently polling Coordinator Master
	workerCtx, cancelWorkers := context.WithCancel(ctx)
	w1 := worker.NewWorkerWithConcurrency(ts.URL, crawler.NewParser(3*time.Second, "E2EBot/2.0"), 1)
	w1.SetAllowPrivateSubnets(true)
	w2 := worker.NewWorkerWithConcurrency(ts.URL, crawler.NewParser(3*time.Second, "E2EBot/2.0"), 1)
	w2.SetAllowPrivateSubnets(true)

	if err := w1.Register(workerCtx); err != nil {
		t.Fatalf("Worker 1 registration failed: %v", err)
	}
	if err := w2.Register(workerCtx); err != nil {
		t.Fatalf("Worker 2 registration failed: %v", err)
	}

	doneW1 := make(chan struct{})
	doneW2 := make(chan struct{})
	go func() { w1.StartLoop(workerCtx); close(doneW1) }()
	go func() { w2.StartLoop(workerCtx); close(doneW2) }()

	// 5. Wait for workers to complete all reachable pages (depth <= 2)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		job, _ := pgRepo.GetJob(ctx, jobID)
		qSize, _ := frontier.QueueSize(ctx)
		leases, _ := frontier.ActiveLeaseCount(ctx)
		if job != nil && job.PagesCrawled >= 6 && qSize == 0 && leases == 0 {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}

	// Shut down first worker cleanly & wait for drain
	cancelWorkers()
	<-doneW1
	<-doneW2

	// 6. Verify crawled pages, depth enforcement, robots.txt block, and errors persisted in PostgreSQL
	pages, total, err := pgRepo.SearchPagesWithTotal(ctx, "", "", "", "", "", "newest", 50, 0)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if total < 5 {
		t.Fatalf("Expected at least 5 processed URLs (/, /docs, /about, /docs/deep, /broken, /secret), got %d", total)
	}

	foundDepth2 := false
	foundTooDeep := false
	foundRobotsBlock := false
	for _, p := range pages {
		if strings.HasSuffix(p.URL, "/docs/deep") {
			foundDepth2 = true
		}
		if strings.HasSuffix(p.URL, "/docs/too-deep") {
			foundTooDeep = true
		}
		if strings.HasSuffix(p.URL, "/secret") && p.StatusCode == http.StatusForbidden {
			foundRobotsBlock = true
		}
	}
	if !foundDepth2 {
		t.Error("Expected depth=2 page /docs/deep to be crawled")
	}
	if foundTooDeep {
		t.Error("Expected max_depth=2 to prevent crawling /docs/too-deep (depth 3)")
	}
	if !foundRobotsBlock {
		t.Error("Expected /secret to be blocked by robots.txt with 403 record")
	}

	// 7. Verify Full-Text Search, Analytics, Metrics, Activity, and Export REST endpoints
	srResp, err := http.Get(ts.URL + "/api/search?q=Architecture&status=2xx")
	if err != nil || srResp.StatusCode != 200 {
		t.Fatalf("GET /api/search failed: %v", err)
	}
	var sr models.SearchResponse
	_ = json.NewDecoder(srResp.Body).Decode(&sr)
	srResp.Body.Close()
	if sr.Total != 1 || sr.Results[0].Title != "Architecture Docs" {
		t.Errorf("Unexpected search result for 'Architecture': %+v", sr)
	}

	anResp, err := http.Get(ts.URL + "/api/analytics?range=24h")
	if err != nil || anResp.StatusCode != 200 {
		t.Fatalf("GET /api/analytics failed: %v", err)
	}
	var an models.AnalyticsResponse
	_ = json.NewDecoder(anResp.Body).Decode(&an)
	anResp.Body.Close()
	if an.PagesIndexed == 0 || an.RobotsBlocked == 0 || an.FailedCount == 0 {
		t.Errorf("Expected real analytics counts (pages>0, robots>0, failed>0), got: %+v", an)
	}

	expResp, err := http.Get(ts.URL + "/api/export?format=csv")
	if err != nil || !strings.Contains(expResp.Header.Get("Content-Type"), "text/csv") {
		t.Fatalf("CSV export failed: %v", err)
	}
	csvBytes, _ := io.ReadAll(expResp.Body)
	expResp.Body.Close()
	if !strings.Contains(string(csvBytes), "Architecture Docs") {
		t.Errorf("CSV export missing crawled data: %s", string(csvBytes))
	}

	// 8. Verify Job Pause / Resume / Cancel lifecycle
	pResp, _ := http.Post(fmt.Sprintf("%s/api/jobs/%s/pause", ts.URL, jobID), "application/json", nil)
	pResp.Body.Close()
	rResp, _ := http.Post(fmt.Sprintf("%s/api/jobs/%s/resume", ts.URL, jobID), "application/json", nil)
	rResp.Body.Close()
	cResp, _ := http.Post(fmt.Sprintf("%s/api/jobs/%s/cancel", ts.URL, jobID), "application/json", nil)
	cResp.Body.Close()
	cancelledJob, _ := pgRepo.GetJob(ctx, jobID)
	if cancelledJob.Status != models.JobStateCancelled {
		t.Errorf("Expected CANCELLED job state, got %s", cancelledJob.Status)
	}

	// 9. SIMULATE WORKER CRASH & VERIFY TASK LEASE RECOVERY!
	crashURL := testSite.URL + "/about?lease_test=1"
	_, _ = frontier.Submit(ctx, crashURL)
	// CrashWorker polls task -> acquires Redis lease -> NEVER calls ReportResult!
	polledTask, err := frontier.GetPoliteStructuredTask(ctx, 0, "crashed-worker-99")
	if err != nil || polledTask == nil || polledTask.URL != crashURL {
		t.Fatalf("Failed acquiring lease for crash test: task=%+v err=%v", polledTask, err)
	}
	if activeLeases, _ := frontier.ActiveLeaseCount(ctx); activeLeases != 1 {
		t.Fatalf("Expected 1 active lease after poll, got %d", activeLeases)
	}

	// Wait >1s lease timeout (configured above) + background reaper sweep to recover abandoned lease
	recovered := false
	reclaimDeadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(reclaimDeadline) {
		qSize, _ := frontier.QueueSize(ctx)
		if qSize > 0 {
			recoveredTask, _ := frontier.GetPoliteStructuredTask(ctx, 0, "healthy-recovery-worker")
			if recoveredTask != nil && recoveredTask.URL == crashURL {
				recovered = true
				_ = frontier.AckTask(ctx, recoveredTask.URL)
				break
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	if !recovered {
		t.Fatal("Expected expired lease from crashed worker to be reclaimed back into frontier queue automatically!")
	}

	// 10. Verify Queue Flush preserves PostgreSQL pages, while Dev Reset clears cleanly
	qResetResp, _ := http.Post(ts.URL+"/api/reset", "application/json", strings.NewReader(`{"level":"queue"}`))
	qResetResp.Body.Close()
	if countAfterQ, _ := pgRepo.GetIndexedPagesCount(ctx); countAfterQ == 0 {
		t.Error("Expected Level Queue flush to preserve PostgreSQL indexed pages")
	}

	dResetResp, _ := http.Post(ts.URL+"/api/reset", "application/json", strings.NewReader(`{"level":"dev"}`))
	dResetResp.Body.Close()
	if countAfterDev, _ := pgRepo.GetIndexedPagesCount(ctx); countAfterDev != 0 {
		t.Errorf("Expected Dev reset to clear indexed pages, got %d", countAfterDev)
	}
}
