package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"distributed-crawler/internal/worker"
	"distributed-crawler/pkg/crawler"
)

func main() {
	timeoutSec := flag.Int("timeout", 10, "HTTP request timeout in seconds")
	userAgent := flag.String("ua", "", "Custom User-Agent header")
	masterURL := flag.String("master", "http://localhost:8080", "Coordinator Master URL")
	concurrency := flag.Int("concurrency", 2, "Worker bounded goroutine concurrency")
	allowPrivate := flag.Bool("allow-private", false, "Allow loopback/private subnets (strictly for local dev/test targets)")
	flag.Parse()

	if envConc := os.Getenv("WORKER_CONCURRENCY"); envConc != "" {
		if val, err := strconv.Atoi(envConc); err == nil && val > 0 {
			*concurrency = val
		}
	}

	parser := crawler.NewParser(time.Duration(*timeoutSec)*time.Second, *userAgent)
	if *allowPrivate {
		parser.SetAllowPrivateSubnets(true)
	}

	args := flag.Args()
	if len(args) >= 1 {
		// Standalone single-page crawl
		targetURL := args[0]
		log.Printf("Starting standalone single-page crawl on: %s\n", crawler.SanitizeForLog(targetURL))
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeoutSec)*time.Second)
		defer cancel()

		data, err := parser.FetchAndParse(ctx, targetURL)
		if err != nil {
			if data != nil && data.StatusCode != 0 {
				log.Printf("⚠️ Server returned status code: %d. Error: %v\n", data.StatusCode, err)
				return
			}
			log.Fatalf("❌ Crawl failed: %v\n", err)
		}

		fmt.Println("\n================ CRAWL RESULT ================")
		fmt.Printf("URL:         %s\n", data.URL)
		fmt.Printf("Final URL:   %s\n", data.FinalURL)
		fmt.Printf("Status Code: %d\n", data.StatusCode)
		fmt.Printf("Title:       %s\n", data.Title)
		fmt.Printf("Description: %s\n", data.Description)
		fmt.Printf("Latency:     %dms\n", data.LatencyMs)
		fmt.Printf("Crawled At:  %s\n", data.CrawledAt.Format(time.RFC3339))
		fmt.Printf("Total links extracted: %d (%d internal, %d external)\n",
			len(data.Links), len(data.InternalLinks), len(data.ExternalLinks))
		fmt.Println("==============================================")
		return
	}

	mURL := os.Getenv("MASTER_URL")
	if mURL == "" {
		mURL = *masterURL
	}

	log.Printf("Starting worker daemon connecting to master at %s (concurrency=%d)...\n", mURL, *concurrency)
	w := worker.NewWorkerWithConcurrency(mURL, parser, *concurrency)
	if *allowPrivate {
		w.SetAllowPrivateSubnets(true)
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Retry initial registration briefly while master starts up
	var regErr error
	for attempt := 1; attempt <= 5; attempt++ {
		if err := w.Register(sigCtx); err == nil {
			regErr = nil
			break
		} else {
			regErr = err
			log.Printf("⏳ Waiting for master registration (attempt %d/5): %v\n", attempt, err)
			select {
			case <-sigCtx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
	}
	if regErr != nil {
		log.Fatalf("❌ Worker registration failed: %v", regErr)
	}

	w.StartLoop(sigCtx)
}
