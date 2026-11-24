package crawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
	"time"
)

// TestNormalizeURL validates the behavior of converting various link formats
// into standard, clean, absolute URLs.
func TestNormalizeURL(t *testing.T) {
	baseURL, _ := url.Parse("https://example.com/blog/post-1?author=pushp")

	tests := []struct {
		name     string
		href     string
		expected string
	}{
		{
			name:     "Absolute HTTP link",
			href:     "http://google.com/search",
			expected: "http://google.com/search",
		},
		{
			name:     "Absolute HTTPS link",
			href:     "https://github.com/trending",
			expected: "https://github.com/trending",
		},
		{
			name:     "Relative path from root",
			href:     "/about",
			expected: "https://example.com/about",
		},
		{
			name:     "Relative path from current folder",
			href:     "next-post",
			expected: "https://example.com/blog/next-post",
		},
		{
			name:     "Relative path with parent directory navigation",
			href:     "../index.html",
			expected: "https://example.com/index.html",
		},
		{
			name:     "URL with fragments/anchors",
			href:     "/features#pricing-table",
			expected: "https://example.com/features", // anchor should be stripped
		},
		{
			name:     "URL with query parameters",
			href:     "/search?q=golang&page=2",
			expected: "https://example.com/search?q=golang&page=2", // queries must be preserved
		},
		{
			name:     "Invalid schemes (javascript)",
			href:     "javascript:void(0)",
			expected: "",
		},
		{
			name:     "Invalid schemes (mailto)",
			href:     "mailto:support@example.com",
			expected: "",
		},
		{
			name:     "Invalid URL syntax",
			href:     "://invalid-url",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeURL(tt.href, baseURL)
			if result != tt.expected {
				t.Errorf("normalizeURL(%q, %v) = %q; expected %q", tt.href, baseURL, result, tt.expected)
			}
		})
	}
}

// TestFetchAndParse simulates a real HTTP crawl using a mock HTTP server.
func TestFetchAndParse(t *testing.T) {
	// 1. Create a local mock web server that returns sample HTML content
	mockHTML := `
	<!DOCTYPE html>
	<html>
	<head>
		<title>My Distributed Crawler Blog</title>
		<meta name="description" content="A blog about engineering distributed systems in Go.">
	</head>
	<body>
		<h1>Welcome to the blog</h1>
		<a href="/about">About Me</a>
		<a href="https://google.com">Google Search</a>
		<a href="/about#skills">My Skills (Duplicate path)</a>
		<a href="javascript:alert('xss')">Invalid XSS Link</a>
	</body>
	</html>
	`

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, mockHTML)
	}))
	defer mockServer.Close()

	// 2. Initialize our parser
	parser := NewParser(2*time.Second, "TestAgent/1.0")
	parser.SetAllowPrivateSubnets(true) // Allow local test mock server on loopback

	// 3. Perform the fetch and parse on the mock server
	ctx := context.Background()
	data, err := parser.FetchAndParse(ctx, mockServer.URL)
	if err != nil {
		t.Fatalf("FetchAndParse failed unexpectedly: %v", err)
	}

	// 4. Validate output
	if data.Title != "My Distributed Crawler Blog" {
		t.Errorf("Expected title 'My Distributed Crawler Blog', got %q", data.Title)
	}

	if data.Description != "A blog about engineering distributed systems in Go." {
		t.Errorf("Expected description 'A blog about engineering distributed systems in Go.', got %q", data.Description)
	}

	if data.StatusCode != http.StatusOK {
		t.Errorf("Expected status code 200, got %d", data.StatusCode)
	}

	// We expect relative paths to resolve against the mock server URL
	expectedLinks := []string{
		mockServer.URL + "/about",
		"https://google.com",
	}

	if !reflect.DeepEqual(data.Links, expectedLinks) {
		t.Errorf("Extracted links mismatch.\nGot: %v\nExpected: %v", data.Links, expectedLinks)
	}
}
