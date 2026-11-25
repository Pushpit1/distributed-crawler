package crawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestRobotsCache_IsAllowed(t *testing.T) {
	// Custom robots.txt payload
	mockRobots := `
	User-agent: MyCrawlerBot
	Disallow: /private/
	Disallow: /admin
	Allow: /public/
	
	User-agent: *
	Disallow: /super-private/
	`

	// Track the number of times robots.txt is actually hit (fetched) on our mock server
	var fetchCount int64

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			atomic.AddInt64(&fetchCount, 1)
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, mockRobots)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockServer.Close()

	// Initialize cache matching our User-Agent
	cache := NewRobotsCache("MyCrawlerBot")

	// Verify SSRF firewall blocks private/loopback targets by default BEFORE hitting cache/network
	if _, ssrfErr := cache.IsAllowed(context.Background(), mockServer.URL+"/public/index.html"); ssrfErr == nil {
		t.Fatal("Expected default RobotsCache to block loopback URL by SSRF policy")
	}

	// Permit local mock server strictly for deterministic unit testing
	cache.SetAllowPrivateSubnets(true)
	ctx := context.Background()

	tests := []struct {
		name          string
		targetURL     string
		expectedAllow bool
	}{
		{
			name:          "Allowed public path",
			targetURL:     mockServer.URL + "/public/index.html",
			expectedAllow: true,
		},
		{
			name:          "Disallowed private path",
			targetURL:     mockServer.URL + "/private/secret.json",
			expectedAllow: false,
		},
		{
			name:          "Disallowed admin path prefix match",
			targetURL:     mockServer.URL + "/admin-dashboard",
			expectedAllow: false,
		},
		{
			name:          "Allowed root page",
			targetURL:     mockServer.URL + "/",
			expectedAllow: true,
		},
		{
			name:          "Disallowed path for other bot (*) but allowed for us",
			targetURL:     mockServer.URL + "/super-private/data",
			expectedAllow: true, // Since MyCrawlerBot has explicit rules, it ignores * rules
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowed, err := cache.IsAllowed(ctx, tt.targetURL)
			if err != nil {
				t.Fatalf("IsAllowed check failed: %v", err)
			}
			if allowed != tt.expectedAllow {
				t.Errorf("IsAllowed(%s) = %v; expected %v", tt.targetURL, allowed, tt.expectedAllow)
			}
		})
	}

	// 4. Verify Caching: We performed 5 URL checks, but robots.txt should have been requested EXACTLY once!
	finalFetchCount := atomic.LoadInt64(&fetchCount)
	if finalFetchCount != 1 {
		t.Errorf("Expected robots.txt to be fetched exactly once (cached), but it was fetched %d times", finalFetchCount)
	}
}
