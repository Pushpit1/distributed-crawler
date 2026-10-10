package crawler

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/temoto/robotstxt"
)

// RobotsCache caches robots.txt rules per host to minimize redundant network calls.
type RobotsCache struct {
	mu        sync.RWMutex
	cache     map[string]*robotstxt.Group
	client    *http.Client
	userAgent string
}

// NewRobotsCache creates an initialized RobotsCache instance.
func NewRobotsCache(userAgent string) *RobotsCache {
	return &RobotsCache{
		cache: make(map[string]*robotstxt.Group),
		client: &http.Client{
			Timeout: 5 * time.Second, // Short timeout for fetching robots.txt files
		},
		userAgent: userAgent,
	}
}

// IsAllowed checks if our User-Agent is allowed to crawl the specified URL.
func (c *RobotsCache) IsAllowed(ctx context.Context, urlStr string) (bool, error) {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return false, fmt.Errorf("invalid URL for robots check: %w", err)
	}

	host := parsedURL.Host
	if host == "" {
		return false, fmt.Errorf("missing host in URL: %s", urlStr)
	}

	// 1. Check in-memory cache first (Thread-safe read lock)
	c.mu.RLock()
	group, cached := c.cache[host]
	c.mu.RUnlock()

	if cached {
		if group == nil {
			// A nil cached group implies the server had no robots.txt or failed (default: allowed)
			return true, nil
		}
		return group.Test(parsedURL.Path), nil
	}

	// 2. Cache miss. Fetch robots.txt from the host
	group, err = c.fetchRobots(ctx, parsedURL.Scheme, host)
	if err != nil {
		// Log the error but default to allowed (fail-open) so network blips don't halt the crawl
		group = nil
	}

	// 3. Write to cache (Thread-safe write lock)
	c.mu.Lock()
	c.cache[host] = group
	c.mu.Unlock()

	if group == nil {
		return true, nil
	}
	return group.Test(parsedURL.Path), nil
}

// fetchRobots downloads and parses robots.txt for the given host.
func (c *RobotsCache) fetchRobots(ctx context.Context, scheme, host string) (*robotstxt.Group, error) {
	robotsURL := fmt.Sprintf("%s://%s/robots.txt", scheme, host)

	req, err := http.NewRequestWithContext(ctx, "GET", robotsURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch robots.txt: %w", err)
	}
	defer resp.Body.Close()

	// If robots.txt doesn't exist (404), then we are allowed to crawl everything
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	} else if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("robots.txt returned status: %d", resp.StatusCode)
	}

	// Parse the robots.txt stream
	data, err := robotstxt.FromResponse(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse robots.txt payload: %w", err)
	}

	// Get the group rules matching our User-Agent
	group := data.FindGroup(c.userAgent)
	return group, nil
}
