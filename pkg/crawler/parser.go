package crawler

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// PageData represents the structured result of crawling and parsing a webpage.
type PageData struct {
	URL         string    // Absolute URL of the page
	Title       string    // Content of the <title> tag
	Description string    // Content of the <meta name="description"> tag
	Links       []string  // List of normalized absolute links found on the page
	StatusCode  int       // HTTP response status code
	CrawledAt   time.Time // Timestamp of the crawl
}

// Parser client configures our web fetcher client
type Parser struct {
	client    *http.Client
	userAgent string
}

// NewParser creates a new Parser with customizable timeout and user-agent
// We reuse the same http.Client pool to benefit from HTTP connection keep-alive
func NewParser(timeout time.Duration, userAgent string) *Parser {
	if userAgent == "" {
		userAgent = "DistributedCrawler/1.0 (+https://github.com/pushp/distributed-crawler)"
	}
	return &Parser{
		client: &http.Client{
			Timeout: timeout,
		},
		userAgent: userAgent,
	}
}

// UserAgent returns the configured User-Agent string.
func (p *Parser) UserAgent() string {
	return p.userAgent
}

// FetchAndParse fetches a webpage and extracts its structural content and links.
// It features an automatic retry loop with exponential backoff for transient network issues.
func (p *Parser) FetchAndParse(ctx context.Context, targetURL string) (*PageData, error) {
	// Validate target URL
	parsedTarget, err := url.Parse(targetURL)
	if err != nil || parsedTarget.Scheme == "" || parsedTarget.Host == "" {
		return nil, fmt.Errorf("invalid target URL: %w", err)
	}

	var resp *http.Response
	var fetchErr error
	maxAttempts := 3

	// Exponential backoff retry loop
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		// Re-create the request to ensure a clean state (headers and contexts) on every try
		req, err := http.NewRequestWithContext(ctx, "GET", targetURL, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		// Set headers
		req.Header.Set("User-Agent", p.userAgent)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

		resp, fetchErr = p.client.Do(req)

		if fetchErr == nil {
			// Check if we hit a transient server error (502 Bad Gateway, 503 Service Unavailable, 504 Gateway Timeout)
			if resp.StatusCode == http.StatusServiceUnavailable ||
				resp.StatusCode == http.StatusBadGateway ||
				resp.StatusCode == http.StatusGatewayTimeout {
				
				resp.Body.Close()
				fetchErr = fmt.Errorf("transient HTTP status: %d", resp.StatusCode)
			} else {
				// Successful HTTP connection (even if 404, we don't retry permanent errors)
				break
			}
		}

		// If this was our last attempt, don't sleep
		if attempt == maxAttempts {
			break
		}

		// Calculate backoff delay: attempt 1 -> 200ms, attempt 2 -> 400ms
		backoff := time.Duration(attempt) * 200 * time.Millisecond
		log.Printf("⚠️ Fetch attempt %d failed for %s: %v. Retrying in %s...\n", attempt, targetURL, fetchErr, backoff)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
	}

	// If we exhausted all attempts and still have an error, return it
	if fetchErr != nil {
		return nil, fmt.Errorf("fetch failed after %d attempts: %w", maxAttempts, fetchErr)
	}
	defer resp.Body.Close()

	pageData := &PageData{
		URL:        targetURL,
		StatusCode: resp.StatusCode,
		CrawledAt:  time.Now(),
	}

	// Only process success status codes (e.g. 200 OK)
	if resp.StatusCode != http.StatusOK {
		return pageData, fmt.Errorf("non-200 status code: %d", resp.StatusCode)
	}

	// Double-check the content type is HTML (don't try to parse PDF, Zip, or Image files)
	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(strings.ToLower(contentType), "text/html") {
		return pageData, fmt.Errorf("target is not HTML (content-type: %s)", contentType)
	}

	// Parse the HTML DOM tree
	title, description, links, err := p.parseHTML(resp.Body, parsedTarget)
	if err != nil {
		return pageData, fmt.Errorf("failed to parse HTML: %w", err)
	}

	pageData.Title = title
	pageData.Description = description
	pageData.Links = links

	return pageData, nil
}

// parseHTML processes the HTML reader and returns structural values and links.
func (p *Parser) parseHTML(r io.Reader, baseURL *url.URL) (string, string, []string, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return "", "", nil, err
	}

	var title, description string
	var links []string
	linkSet := make(map[string]bool) // Set to avoid duplicate links on the same page

	// DFS helper function to traverse the HTML node tree
	var traverse func(*html.Node)
	traverse = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "title":
				// <title>text</title> - text node is a child of element node title
				if n.FirstChild != nil && n.FirstChild.Type == html.TextNode {
					title = strings.TrimSpace(n.FirstChild.Data)
				}
			case "meta":
				// <meta name="description" content="hello description">
				var name, content string
				for _, attr := range n.Attr {
					if strings.ToLower(attr.Key) == "name" {
						name = strings.ToLower(attr.Val)
					}
					if strings.ToLower(attr.Key) == "content" {
						content = attr.Val
					}
				}
				if name == "description" {
					description = strings.TrimSpace(content)
				}
			case "a":
				// <a href="/path">anchor text</a>
				for _, attr := range n.Attr {
					if strings.ToLower(attr.Key) == "href" {
						resolvedURL := normalizeURL(attr.Val, baseURL)
						if resolvedURL != "" {
							if !linkSet[resolvedURL] {
								linkSet[resolvedURL] = true
								links = append(links, resolvedURL)
							}
						}
					}
				}
			}
		}

		// Traverse siblings and children
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			traverse(c)
		}
	}

	traverse(doc)
	return title, description, links, nil
}

// normalizeURL converts relative URLs to absolute URLs and cleans them.
func normalizeURL(href string, baseURL *url.URL) string {
	href = strings.TrimSpace(href)
	if href == "" || strings.HasPrefix(href, "javascript:") || strings.HasPrefix(href, "mailto:") || strings.HasPrefix(href, "tel:") {
		return ""
	}

	parsedHref, err := url.Parse(href)
	if err != nil {
		return ""
	}

	// Resolve reference converts a relative path to absolute against the baseURL
	resolved := baseURL.ResolveReference(parsedHref)

	// Keep schemes clean (http/https only)
	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return ""
	}

	// Strip URL fragment/anchor (e.g. #section-1) so we crawl the canonical page
	resolved.Fragment = ""

	// Ensure Hostname is lowercase
	resolved.Host = strings.ToLower(resolved.Host)

	return resolved.String()
}
