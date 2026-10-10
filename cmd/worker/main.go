package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"distributed-crawler/internal/worker"
	"distributed-crawler/pkg/crawler"
)

func main() {
	// CLI configuration flags
	timeoutSec := flag.Int("timeout", 5, "HTTP request timeout in seconds")
	userAgent := flag.String("ua", "", "Custom User-Agent header")
	masterURL := flag.String("master", "http://localhost:8080", "Coordinator Master URL")
	flag.Parse()

	// Create our parser instance
	parser := crawler.NewParser(time.Duration(*timeoutSec)*time.Second, *userAgent)

	args := flag.Args()
	if len(args) >= 1 {
		// Mode A: Standalone single-page crawl
		targetURL := args[0]
		log.Printf("Starting standalone single-page crawl on: %s\n", targetURL)
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeoutSec)*time.Second)
		defer cancel()

		data, err := parser.FetchAndParse(ctx, targetURL)
		if err != nil {
			if data != nil && data.StatusCode != 0 {
				log.Printf("⚠️ Server returned error status code: %d. Error: %v\n", data.StatusCode, err)
				return
			}
			log.Fatalf("❌ Crawl failed: %v\n", err)
		}

		fmt.Println("\n================ CRAWL RESULT ================")
		fmt.Printf("URL:         %s\n", data.URL)
		fmt.Printf("Status Code: %d\n", data.StatusCode)
		fmt.Printf("Title:       %s\n", data.Title)
		fmt.Printf("Description: %s\n", data.Description)
		fmt.Printf("Crawled At:  %s\n", data.CrawledAt.Format(time.RFC3339))
		fmt.Println("\nExtracted Links:")
		for i, link := range data.Links {
			fmt.Printf(" [%d] %s\n", i+1, link)
		}
		fmt.Printf("\nTotal links extracted: %d\n", len(data.Links))
		fmt.Println("==============================================")
		return
	}

	// Environment variable configuration for Docker environments
	mURL := os.Getenv("MASTER_URL")
	if mURL == "" {
		mURL = *masterURL
	}

	// Mode B: Distributed daemon loop
	log.Printf("Starting worker daemon connecting to master at: %s\n", mURL)
	w := worker.NewWorker(mURL, parser)

	ctx := context.Background()
	// Register with master
	if err := w.Register(ctx); err != nil {
		log.Fatalf("❌ Worker registration failed: %v. Ensure Coordinator Master is running.", err)
	}

	// Start polling loop (blocking)
	w.StartLoop(ctx)
}
