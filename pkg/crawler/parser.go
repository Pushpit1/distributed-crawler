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

const (
	// MaxResponseBodyBytes sets an upper ceiling on HTML reads to prevent decompression bombs (10 MB)
	MaxResponseBodyBytes = 10 * 1024 * 1024
)

// PageData represents the structured result of crawling and parsing a webpage.
type PageData struct {
	URL           string    // Absolute URL of the page
	CanonicalURL  string    // Canonical link tag href if present
	Title         string    // Content of <title> tag
	Description   string    // Content of <meta name="description"> tag
	H1            string    // Content of primary <h1> tag
	ContentType   string    // MIME type from headers
	ContentLength int       // Response byte count
	Links         []string  // All unique normalized links
	InternalLinks []string  // Outgoing links on same domain
	ExternalLinks []string  // Outgoing links to external domains
	StatusCode    int       // HTTP response status code
	LatencyMs     int       // Roundtrip latency in milliseconds
	Depth         int       // Distance from seed
	CrawledAt     time.Time // Timestamp of the crawl
}

// Parser client configures our web fetcher client
type Parser struct {
	client              *http.Client
	userAgent           string
	allowPrivateSubnets bool
}

// NewParser creates a new Parser with customizable timeout and user-agent.
// Uses SSRFSafeTransport to block connections to private / loopback IP subnets.
func NewParser(timeout time.Duration, userAgent string) *Parser {
	if userAgent == "" {
		userAgent = "DistributedCrawler/1.0 (+https://github.com/Pushpit1/distributed-crawler)"
	}

	return &Parser{
		client: &http.Client{
			Transport:     NewSSRFSafeTransport(timeout),
			CheckRedirect: SafeRedirectPolicy,
			Timeout:       timeout + 5*time.Second,
		},
		userAgent:           userAgent,
		allowPrivateSubnets: false,
	}
}

// SetAllowPrivateSubnets permits loopback/private subnet connections (strictly for unit tests).
func (p *Parser) SetAllowPrivateSubnets(allow bool) {
	p.allowPrivateSubnets = allow
	if allow {
		p.client.Transport = &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		}
		p.client.CheckRedirect = nil
	}
}

// UserAgent returns the configured User-Agent string.
func (p *Parser) UserAgent() string {
	return p.userAgent
}

// FetchAndParse fetches a webpage safely and extracts its structural content and links.
func (p *Parser) FetchAndParse(ctx context.Context, targetURL string) (*PageData, error) {
	// Validate target URL against protocol and SSRF constraints
	parsedTarget, err := ValidateTargetURL(targetURL, p.allowPrivateSubnets)
	if err != nil {
		return nil, fmt.Errorf("SSRF safety validation failed for %s: %w", targetURL, err)
	}

	var resp *http.Response
	var fetchErr error
	maxAttempts := 3
	startTime := time.Now()

	// Exponential backoff retry loop for transient failures
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "GET", targetURL, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("User-Agent", p.userAgent)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

		resp, fetchErr = p.client.Do(req)

		if fetchErr == nil {
			if resp.StatusCode == http.StatusServiceUnavailable ||
				resp.StatusCode == http.StatusBadGateway ||
				resp.StatusCode == http.StatusGatewayTimeout {
				resp.Body.Close()
				fetchErr = fmt.Errorf("transient HTTP status: %d", resp.StatusCode)
			} else {
				break
			}
		}

		if attempt == maxAttempts {
			break
		}

		backoff := time.Duration(attempt) * 200 * time.Millisecond
		log.Printf("⚠️ Fetch attempt %d failed for %s: %v. Retrying in %s...\n", attempt, targetURL, fetchErr, backoff)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
	}

	latencyMs := int(time.Since(startTime).Milliseconds())

	if fetchErr != nil {
		return nil, fmt.Errorf("fetch failed after %d attempts: %w", maxAttempts, fetchErr)
	}
	defer resp.Body.Close()

	// Wrap body in LimitReader to prevent memory exhaustion and decompression bombs
	limitedBody := io.LimitReader(resp.Body, MaxResponseBodyBytes)

	contentType := resp.Header.Get("Content-Type")

	pageData := &PageData{
		URL:         targetURL,
		StatusCode:  resp.StatusCode,
		ContentType: contentType,
		LatencyMs:   latencyMs,
		CrawledAt:   time.Now(),
	}

	if resp.StatusCode != http.StatusOK {
		return pageData, fmt.Errorf("non-200 status code: %d", resp.StatusCode)
	}

	if !strings.Contains(strings.ToLower(contentType), "text/html") {
		return pageData, fmt.Errorf("target is not HTML (content-type: %s)", contentType)
	}

	// Parse HTML DOM tree and extract rich metadata
	extracted, bytesRead, err := p.parseHTML(limitedBody, parsedTarget)
	if err != nil {
		return pageData, fmt.Errorf("failed to parse HTML: %w", err)
	}

	pageData.Title = extracted.title
	pageData.Description = extracted.description
	pageData.CanonicalURL = extracted.canonicalURL
	pageData.H1 = extracted.h1
	pageData.ContentLength = bytesRead
	pageData.Links = extracted.links
	pageData.InternalLinks = extracted.internalLinks
	pageData.ExternalLinks = extracted.externalLinks

	return pageData, nil
}

type extractedHTML struct {
	title         string
	description   string
	canonicalURL  string
	h1            string
	links         []string
	internalLinks []string
	externalLinks []string
}

// parseHTML processes the HTML stream and returns extracted content and link lists.
func (p *Parser) parseHTML(r io.Reader, baseURL *url.URL) (*extractedHTML, int, error) {
	// Use a counting reader to track downloaded bytes accurately
	cr := &countingReader{r: r}
	doc, err := html.Parse(cr)
	if err != nil {
		return nil, cr.bytesRead, err
	}

	result := &extractedHTML{}
	linkSet := make(map[string]bool)
	baseHost := strings.ToLower(baseURL.Hostname())

	var traverse func(*html.Node)
	traverse = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch strings.ToLower(n.Data) {
			case "title":
				if result.title == "" && n.FirstChild != nil && n.FirstChild.Type == html.TextNode {
					result.title = strings.TrimSpace(n.FirstChild.Data)
				}
			case "h1":
				if result.h1 == "" {
					result.h1 = extractNodeText(n)
				}
			case "meta":
				var name, content string
				for _, attr := range n.Attr {
					key := strings.ToLower(attr.Key)
					if key == "name" || key == "property" {
						name = strings.ToLower(attr.Val)
					}
					if key == "content" {
						content = attr.Val
					}
				}
				if (name == "description" || name == "og:description") && result.description == "" {
					result.description = strings.TrimSpace(content)
				}
			case "link":
				var rel, href string
				for _, attr := range n.Attr {
					if strings.ToLower(attr.Key) == "rel" {
						rel = strings.ToLower(attr.Val)
					}
					if strings.ToLower(attr.Key) == "href" {
						href = attr.Val
					}
				}
				if rel == "canonical" && href != "" {
					result.canonicalURL = normalizeURL(href, baseURL)
				}
			case "a":
				for _, attr := range n.Attr {
					if strings.ToLower(attr.Key) == "href" {
						resolvedURL := normalizeURL(attr.Val, baseURL)
						if resolvedURL != "" && !linkSet[resolvedURL] {
							linkSet[resolvedURL] = true
							result.links = append(result.links, resolvedURL)

							// Check internal vs external
							if parsedLink, err := url.Parse(resolvedURL); err == nil {
								if strings.ToLower(parsedLink.Hostname()) == baseHost {
									result.internalLinks = append(result.internalLinks, resolvedURL)
								} else {
									result.externalLinks = append(result.externalLinks, resolvedURL)
								}
							}
						}
					}
				}
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			traverse(c)
		}
	}

	traverse(doc)
	return result, cr.bytesRead, nil
}

func extractNodeText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(curr *html.Node) {
		if curr.Type == html.TextNode {
			sb.WriteString(curr.Data)
		}
		for c := curr.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(sb.String())
}

// normalizeURL converts relative URLs to absolute URLs and cleans them.
func normalizeURL(href string, baseURL *url.URL) string {
	href = strings.TrimSpace(href)
	if href == "" || strings.HasPrefix(href, "javascript:") || strings.HasPrefix(href, "mailto:") || strings.HasPrefix(href, "tel:") || strings.HasPrefix(href, "#") {
		return ""
	}

	parsedHref, err := url.Parse(href)
	if err != nil {
		return ""
	}

	resolved := baseURL.ResolveReference(parsedHref)

	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return ""
	}

	resolved.Fragment = ""
	resolved.Host = strings.ToLower(resolved.Host)

	return resolved.String()
}

type countingReader struct {
	r         io.Reader
	bytesRead int
}

func (c *countingReader) Read(p []byte) (n int, err error) {
	n, err = c.r.Read(p)
	c.bytesRead += n
	return
}
