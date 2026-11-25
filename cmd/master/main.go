package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"distributed-crawler/internal/master"
	"distributed-crawler/pkg/crawler"
	"distributed-crawler/pkg/db"
	"distributed-crawler/pkg/models"

	"github.com/redis/go-redis/v9"
)

func resolveDatabaseURL() string {
	if url := os.Getenv("DATABASE_URL"); url != "" {
		return url
	}
	host := os.Getenv("DB_HOST")
	port := os.Getenv("DB_PORT")
	user := os.Getenv("DB_USER")
	pass := os.Getenv("DB_PASSWORD")
	name := os.Getenv("DB_NAME")
	ssl := os.Getenv("DB_SSLMODE")
	if host != "" {
		if port == "" {
			port = "5432"
		}
		if user == "" {
			user = "crawler_user"
		}
		if pass == "" {
			pass = "crawler_password"
		}
		if name == "" {
			name = "crawler_db"
		}
		if ssl == "" {
			ssl = "disable"
		}
		return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s", user, pass, host, port, name, ssl)
	}
	return "postgres://crawler_user:crawler_password@localhost:5433/crawler_db?sslmode=disable"
}

func main() {
	port := flag.Int("port", 8080, "Port for Coordinator API server")
	redisAddr := flag.String("redis", "localhost:6379", "Redis connection address")
	clearQueue := flag.Bool("clear", false, "Clear Redis queue and visited keys on startup")
	politenessMs := flag.Int("politeness", 1000, "Politeness delay per domain in milliseconds")
	allowPrivate := flag.Bool("allow-private", false, "Allow loopback/private targets (strictly for local dev/tests)")
	flag.Parse()

	log.Println("🚀 Starting Gopher Crawler Coordinator Master v2.0.0...")

	rAddr := os.Getenv("REDIS_ADDR")
	if rAddr == "" {
		rAddr = *redisAddr
	}
	rPass := os.Getenv("REDIS_PASSWORD")

	pgConnStr := resolveDatabaseURL()

	serverPort := strconv.Itoa(*port)
	if envPort := os.Getenv("PORT"); envPort != "" {
		serverPort = envPort
	}
	serverAddr := ":" + serverPort

	pMs := *politenessMs
	if envPol := os.Getenv("POLITENESS_MS"); envPol != "" {
		if val, err := strconv.Atoi(envPol); err == nil {
			pMs = val
		}
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:     rAddr,
		Password: rPass,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := rdb.Ping(ctx).Err(); err != nil {
		cancel()
		log.Fatalf("❌ Failed to connect to Redis at %s: %v", rAddr, err)
	}
	cancel()
	log.Printf("✅ Connected to Redis (%s).\n", rAddr)

	frontier := crawler.NewFrontier(rdb, "crawler:queue", "crawler:visited")

	if *clearQueue {
		if err := frontier.Clear(context.Background()); err != nil {
			log.Printf("⚠️ Failed to clear frontier keys: %v\n", err)
		}
	}

	pgRepo, err := db.NewPostgresRepository(pgConnStr)
	if err != nil {
		log.Fatalf("❌ Failed to initialize PostgreSQL database: %v", err)
	}
	defer pgRepo.Close()

	srv := master.NewCoordinatorServer(frontier, pgRepo, time.Duration(pMs)*time.Millisecond)
	defer srv.Close()
	if *allowPrivate {
		srv.SetAllowPrivateSubnets(true)
	}

	// Enqueue startup CLI seeds cleanly inside a tracked job
	seedUrls := flag.Args()
	if len(seedUrls) > 0 {
		jobID := fmt.Sprintf("job-boot-%d", time.Now().Unix())
		_ = pgRepo.CreateJob(context.Background(), models.CrawlJob{
			ID:       jobID,
			Name:     fmt.Sprintf("Bootstrap Crawl (%s)", seedUrls[0]),
			Status:   models.JobStateRunning,
			SeedURLs: seedUrls,
			MaxDepth: 3,
			MaxPages: 250,
		})
		var tasks []models.CrawlTask
		for _, u := range seedUrls {
			tasks = append(tasks, models.CrawlTask{URL: u, JobID: jobID, Depth: 0, MaxDepth: 3})
		}
		enqueued, err := frontier.SubmitJobTasks(context.Background(), jobID, tasks...)
		if err != nil {
			log.Printf("⚠️ Failed to enqueue bootstrap seed URLs: %v\n", err)
		} else {
			log.Printf("🌱 Bootstrapped job %s with %d seed task(s).\n", jobID, enqueued)
		}
	}

	handler := srv.Router("web")
	httpServer := &http.Server{
		Addr:              serverAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("🌐 Coordinator REST API & Dashboard listening on http://localhost%s\n", serverAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("❌ Server failed: %v\n", err)
		}
	}()

	<-sigCtx.Done()
	log.Println("🛑 Gracefully shutting down Coordinator HTTP server...")
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutCancel()
	if err := httpServer.Shutdown(shutCtx); err != nil {
		log.Printf("⚠️ HTTP server forced shutdown: %v\n", err)
	}
	log.Println("✅ Coordinator shutdown complete.")
}
