package crawler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/temoto/robotstxt"
)

const MaxRobotsTxtBytes = 512 * 1024 // 512 KB cap per RFC 9309 recommendations

// RobotsCache caches robots.txt rules per host to minimize redundant network calls.
type RobotsCache struct {
	mu                  sync.RWMutex
	cache               map[string]*robotstxt.Group
	client              *http.Client
	userAgent           string
	allowPrivateSubnets bool
}

// NewRobotsCache creates an initialized RobotsCache instance protected by SSRFSafeTransport.
func NewRobotsCache(userAgent string) *RobotsCache {
	return &RobotsCache{
		cache: make(map[string]*robotstxt.Group),
		client: &http.Client{
			Transport:     NewSSRFSafeTransport(5 * time.Second),
			CheckRedirect: SafeRedirectPolicy,
			Timeout:       6 * time.Second,
		},
		userAgent:           userAgent,
		allowPrivateSubnets: false,
	}
}

// SetAllowPrivateSubnets permits loopback/private subnet connections strictly for local unit tests.
func (c *RobotsCache) SetAllowPrivateSubnets(allow bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.allowPrivateSubnets = allow
	if allow {
		c.client = &http.Client{
			Timeout: 5 * time.Second,
		}
	} else {
		c.client = &http.Client{
			Transport:     NewSSRFSafeTransport(5 * time.Second),
			CheckRedirect: SafeRedirectPolicy,
			Timeout:       6 * time.Second,
		}
	}
}

// IsAllowed checks if our User-Agent is allowed to crawl the specified URL.
// Validates SSRF constraints before dispatching any robots.txt network request.
func (c *RobotsCache) IsAllowed(ctx context.Context, urlStr string) (bool, error) {
	c.mu.RLock()
	allowPrivate := c.allowPrivateSubnets
	c.mu.RUnlock()

	parsedURL, err := ValidateTargetURL(urlStr, allowPrivate)
	if err != nil {
		return false, fmt.Errorf("SSRF/URL check blocked robots lookup: %w", err)
	}

	host := parsedURL.Host
	if host == "" {
		return false, fmt.Errorf("missing host in URL: %s", urlStr)
	}

	// 1. Check in-memory cache first (thread-safe read lock)
	c.mu.RLock()
	group, cached := c.cache[host]
	c.mu.RUnlock()

	if cached {
		if group == nil {
			return true, nil
		}
		return group.Test(parsedURL.Path), nil
	}

	// 2. Cache miss. Fetch robots.txt from the host safely
	group, err = c.fetchRobots(ctx, parsedURL.Scheme, host)
	if err != nil {
		// Fail-open on transient network failures while caching nil for unreachable robots.txt
		group = nil
	}

	// 3. Write to cache (thread-safe write lock)
	c.mu.Lock()
	c.cache[host] = group
	c.mu.Unlock()

	if group == nil {
		return true, nil
	}
	return group.Test(parsedURL.Path), nil
}

// fetchRobots downloads and parses robots.txt for the given host safely with size bounds.
func (c *RobotsCache) fetchRobots(ctx context.Context, scheme, host string) (*robotstxt.Group, error) {
	robotsURL := (&url.URL{Scheme: scheme, Host: host, Path: "/robots.txt"}).String()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, robotsURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch robots.txt: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	} else if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("robots.txt returned status: %d", resp.StatusCode)
	}

	limited := io.LimitReader(resp.Body, MaxRobotsTxtBytes)
	bodyBytes, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("failed reading robots.txt payload: %w", err)
	}

	data, err := robotstxt.FromStatusAndBytes(resp.StatusCode, bodyBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse robots.txt payload: %w", err)
	}

	group := data.FindGroup(c.userAgent)
	return group, nil
}
