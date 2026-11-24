package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"distributed-crawler/internal/master"
	"distributed-crawler/pkg/crawler"
	"distributed-crawler/pkg/db"

	"github.com/redis/go-redis/v9"
)

func main() {
	// CLI configuration flags
	port := flag.Int("port", 8080, "Port for Coordinator API server")
	redisAddr := flag.String("redis", "localhost:6379", "Redis connection address")
	clearQueue := flag.Bool("clear", false, "Clear Redis queue and visited keys on startup")
	politenessMs := flag.Int("politeness", 1000, "Politeness delay per domain in milliseconds")
	flag.Parse()

	log.Println("Starting Coordinator Master Node...")

	// 1. Resolve configuration from environment variables or CLI flags
	rAddr := os.Getenv("REDIS_ADDR")
	if rAddr == "" {
		rAddr = *redisAddr
	}

	pgConnStr := os.Getenv("DATABASE_URL")
	if pgConnStr == "" {
		// Default local PostgreSQL mapped port
		pgConnStr = "postgres://crawler_user:crawler_password@localhost:5433/crawler_db?sslmode=disable"
	}

	serverPort := strconv.Itoa(*port)
	if envPort := os.Getenv("PORT"); envPort != "" {
		serverPort = envPort
	}
	serverAddr := ":" + serverPort

	pMs := *politenessMs
	if envPoliteness := os.Getenv("POLITENESS_MS"); envPoliteness != "" {
		if val, err := strconv.Atoi(envPoliteness); err == nil {
			pMs = val
		}
	}

	// 2. Establish connection to Redis
	rdb := redis.NewClient(&redis.Options{
		Addr: rAddr,
	})

	// Connection smoke test
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("❌ Failed to connect to Redis: %v. Ensure Docker is running.", err)
	}
	log.Println("✅ Connected to Redis successfully.")

	// 3. Initialize the Frontier
	queueKey := "crawler:queue"
	setKey := "crawler:visited"
	frontier := crawler.NewFrontier(rdb, queueKey, setKey)

	// Clean out keys if explicit clear flag is set
	if *clearQueue {
		if err := frontier.Clear(context.Background()); err != nil {
			log.Printf("⚠️ Failed to clear frontier keys: %v\n", err)
		}
	}

	// 4. Enqueue seed URLs if provided
	seedUrls := flag.Args()
	if len(seedUrls) > 0 {
		log.Printf("🌱 Enqueueing %d seed URLs: %v\n", len(seedUrls), seedUrls)
		enqueued, err := frontier.Submit(context.Background(), seedUrls...)
		if err != nil {
			log.Fatalf("❌ Failed to enqueue seed URLs: %v", err)
		}
		log.Printf("✅ Successfully enqueued %d new seed URLs.\n", enqueued)
	} else {
		// Check if the queue is empty
		qSize, err := frontier.QueueSize(context.Background())
		if err != nil {
			log.Fatalf("Failed checking queue size: %v", err)
		}
		if qSize == 0 {
			log.Println("⚠️ Warning: No seed URLs provided and Redis queue is empty. Crawl will wait for seeds.")
		}
	}

	// 5. Connect to PostgreSQL
	pgRepo, err := db.NewPostgresRepository(pgConnStr)
	if err != nil {
		log.Fatalf("❌ Failed to initialize PostgreSQL database: %v", err)
	}
	defer pgRepo.Close()

	// 6. Create the REST server instance
	srv := master.NewCoordinatorServer(frontier, pgRepo, time.Duration(pMs)*time.Millisecond)

	// 7. Setup route mappings
	mux := http.NewServeMux()

	// Health & Readiness checks
	mux.HandleFunc("GET /health", srv.HandleHealth)
	mux.HandleFunc("GET /api/health", srv.HandleHealth)
	mux.HandleFunc("GET /ready", srv.HandleReady)
	mux.HandleFunc("GET /api/ready", srv.HandleReady)

	// Worker Registration & Protocol
	mux.HandleFunc("POST /register", srv.HandleRegister)
	mux.HandleFunc("POST /api/register", srv.HandleRegister)
	mux.HandleFunc("OPTIONS /register", srv.HandleCORS)
	mux.HandleFunc("OPTIONS /api/register", srv.HandleCORS)

	// Worker Heartbeat Telemetry
	mux.HandleFunc("POST /heartbeat", srv.HandleHeartbeat)
	mux.HandleFunc("POST /api/heartbeat", srv.HandleHeartbeat)
	mux.HandleFunc("OPTIONS /heartbeat", srv.HandleCORS)
	mux.HandleFunc("OPTIONS /api/heartbeat", srv.HandleCORS)

	// Worker Task Polling
	mux.HandleFunc("POST /poll", srv.HandlePoll)
	mux.HandleFunc("POST /api/poll", srv.HandlePoll)
	mux.HandleFunc("OPTIONS /poll", srv.HandleCORS)
	mux.HandleFunc("OPTIONS /api/poll", srv.HandleCORS)

	// Worker Results Report
	mux.HandleFunc("POST /report", srv.HandleReport)
	mux.HandleFunc("POST /api/report", srv.HandleReport)
	mux.HandleFunc("OPTIONS /report", srv.HandleCORS)
	mux.HandleFunc("OPTIONS /api/report", srv.HandleCORS)

	// Telemetry, Workers & Metrics
	mux.HandleFunc("GET /status", srv.HandleStatus)
	mux.HandleFunc("GET /api/status", srv.HandleStatus)
	mux.HandleFunc("OPTIONS /status", srv.HandleCORS)
	mux.HandleFunc("OPTIONS /api/status", srv.HandleCORS)

	mux.HandleFunc("GET /api/metrics", srv.HandleMetrics)
	mux.HandleFunc("OPTIONS /api/metrics", srv.HandleCORS)

	mux.HandleFunc("GET /api/workers", srv.HandleWorkers)
	mux.HandleFunc("OPTIONS /api/workers", srv.HandleCORS)

	mux.HandleFunc("GET /api/activity", srv.HandleActivity)
	mux.HandleFunc("OPTIONS /api/activity", srv.HandleCORS)

	// Crawl Job Management
	mux.HandleFunc("POST /submit", srv.HandleSubmitSeeds)
	mux.HandleFunc("POST /api/submit", srv.HandleSubmitSeeds)
	mux.HandleFunc("OPTIONS /submit", srv.HandleCORS)
	mux.HandleFunc("OPTIONS /api/submit", srv.HandleCORS)

	mux.HandleFunc("GET /api/jobs", srv.HandleJobs)
	mux.HandleFunc("OPTIONS /api/jobs", srv.HandleCORS)
	mux.HandleFunc("POST /api/jobs/", srv.HandleJobOperation)
	mux.HandleFunc("OPTIONS /api/jobs/", srv.HandleCORS)

	// Search & Domain Exploration
	mux.HandleFunc("GET /search", srv.HandleSearch)
	mux.HandleFunc("GET /api/search", srv.HandleSearch)
	mux.HandleFunc("OPTIONS /search", srv.HandleCORS)
	mux.HandleFunc("OPTIONS /api/search", srv.HandleCORS)

	mux.HandleFunc("GET /api/pages/", srv.HandlePageDetails)
	mux.HandleFunc("OPTIONS /api/pages/", srv.HandleCORS)

	mux.HandleFunc("GET /api/domains", srv.HandleDomains)
	mux.HandleFunc("OPTIONS /api/domains", srv.HandleCORS)

	// Tiered Administration & Config
	mux.HandleFunc("POST /reset", srv.HandleReset)
	mux.HandleFunc("POST /api/reset", srv.HandleReset)
	mux.HandleFunc("OPTIONS /reset", srv.HandleCORS)
	mux.HandleFunc("OPTIONS /api/reset", srv.HandleCORS)

	mux.HandleFunc("GET /api/config", srv.HandleConfig)
	mux.HandleFunc("OPTIONS /api/config", srv.HandleCORS)

	// Serve frontend static files from the "web" directory
	mux.Handle("GET /", http.FileServer(http.Dir("web")))

	log.Printf("🚀 Coordinator API server listening on http://localhost%s\n", serverAddr)

	// Start server (blocking call)
	if err := http.ListenAndServe(serverAddr, mux); err != nil {
		log.Printf("❌ Server error: %v\n", err)
		os.Exit(1)
	}
}
