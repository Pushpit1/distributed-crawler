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
	// MaxResponseBodyBytes sets an upper ceiling on HTML reads to prevent decompression bombs (10 MB).
	MaxResponseBodyBytes = 10 * 1024 * 1024
	// MaxExtractedTextBytes caps persisted visible text excerpt per page for lean DB storage.
	MaxExtractedTextBytes = 8192
)

// PageData represents the structured result of crawling and parsing a webpage.
type PageData struct {
	URL           string    // Normalized target URL
	FinalURL      string    // Final destination URL after redirects
	CanonicalURL  string    // Canonical link tag href if present
	Title         string    // Content of <title> tag
	Description   string    // Content of <meta name="description"> tag
	H1            string    // Content of primary <h1> tag
	Headings      []string  // Extracted h1-h3 headings (capped)
	ExtractedText string    // Visible body text excerpt (capped)
	ContentType   string    // MIME type from headers
	ContentLength int       // Response byte count
	Links         []string  // All unique normalized links
	InternalLinks []string  // Outgoing links on same domain
	ExternalLinks []string  // Outgoing links to external domains
	StatusCode    int       // HTTP response status code
	LatencyMs     int       // Roundtrip latency in milliseconds
	Depth         int       // Distance from seed
	Retries       int       // Number of retry attempts performed
	CrawledAt     time.Time // Timestamp of the crawl
}

// Parser configures the HTTP fetching client and HTML extraction behavior.
type Parser struct {
	client              *http.Client
	userAgent           string
	allowPrivateSubnets bool
}

// NewParser creates a Parser with customizable timeout, user-agent, SSRF-safe transport, and safe redirects.
func NewParser(timeout time.Duration, userAgent string) *Parser {
	if userAgent == "" {
		userAgent = "DistributedCrawler/2.0 (+https://github.com/Pushpit1/distributed-crawler)"
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

// SetAllowPrivateSubnets permits loopback/private subnet connections strictly for local unit tests.
func (p *Parser) SetAllowPrivateSubnets(allow bool) {
	p.allowPrivateSubnets = allow
	if allow {
		p.client.Transport = &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		}
		p.client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("stopped after 5 HTTP redirects")
			}
			return nil
		}
	} else {
		p.client.Transport = NewSSRFSafeTransport(5 * time.Second)
		p.client.CheckRedirect = SafeRedirectPolicy
	}
}

// UserAgent returns the configured User-Agent string.
func (p *Parser) UserAgent() string {
	return p.userAgent
}

// FetchAndParse fetches a webpage safely with exponential backoff retries and extracts metadata.
func (p *Parser) FetchAndParse(ctx context.Context, targetURL string) (*PageData, error) {
	parsedTarget, err := ValidateTargetURL(targetURL, p.allowPrivateSubnets)
	if err != nil {
		return nil, fmt.Errorf("SSRF safety validation failed for %s: %w", SanitizeForLog(targetURL), err)
	}

	var resp *http.Response
	var fetchErr error
	maxAttempts := 3
	retriesPerformed := 0
	startTime := time.Now()

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("User-Agent", p.userAgent)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

		resp, fetchErr = p.client.Do(req)

		if fetchErr == nil {
			if resp.StatusCode == http.StatusTooManyRequests ||
				resp.StatusCode == http.StatusInternalServerError ||
				resp.StatusCode == http.StatusBadGateway ||
				resp.StatusCode == http.StatusServiceUnavailable ||
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

		retriesPerformed++
		backoff := time.Duration(attempt*attempt) * 150 * time.Millisecond
		log.Printf("⚠️ Fetch attempt %d failed for %s: %v. Retrying in %s...\n", attempt, SanitizeForLog(targetURL), fetchErr, backoff)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
	}

	latencyMs := int(time.Since(startTime).Milliseconds())

	if fetchErr != nil {
		return &PageData{
			URL:       targetURL,
			FinalURL:  targetURL,
			LatencyMs: latencyMs,
			Retries:   retriesPerformed,
			CrawledAt: time.Now().UTC(),
		}, fmt.Errorf("fetch failed after %d attempts: %w", maxAttempts, fetchErr)
	}
	defer resp.Body.Close()

	limitedBody := io.LimitReader(resp.Body, MaxResponseBodyBytes)
	contentType := resp.Header.Get("Content-Type")
	finalURL := targetURL
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}

	pageData := &PageData{
		URL:         targetURL,
		FinalURL:    finalURL,
		StatusCode:  resp.StatusCode,
		ContentType: contentType,
		LatencyMs:   latencyMs,
		Retries:     retriesPerformed,
		CrawledAt:   time.Now().UTC(),
	}

	if resp.StatusCode != http.StatusOK {
		return pageData, fmt.Errorf("non-200 status code: %d", resp.StatusCode)
	}

	if !strings.Contains(strings.ToLower(contentType), "text/html") &&
		!strings.Contains(strings.ToLower(contentType), "application/xhtml+xml") {
		return pageData, fmt.Errorf("target is not HTML (content-type: %s)", contentType)
	}

	baseURLForLinks := parsedTarget
	if finalParsed, err := url.Parse(finalURL); err == nil && finalParsed.Host != "" {
		baseURLForLinks = finalParsed
	}

	extracted, bytesRead, err := p.parseHTML(limitedBody, baseURLForLinks)
	if err != nil {
		return pageData, fmt.Errorf("failed to parse HTML: %w", err)
	}

	pageData.Title = extracted.title
	pageData.Description = extracted.description
	pageData.CanonicalURL = extracted.canonicalURL
	pageData.H1 = extracted.h1
	pageData.Headings = extracted.headings
	pageData.ExtractedText = extracted.extractedText
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
	headings      []string
	extractedText string
	links         []string
	internalLinks []string
	externalLinks []string
}

// parseHTML processes the HTML stream and returns extracted metadata, visible text excerpt, and links.
func (p *Parser) parseHTML(r io.Reader, baseURL *url.URL) (*extractedHTML, int, error) {
	cr := &countingReader{r: r}
	doc, err := html.Parse(cr)
	if err != nil {
		return nil, cr.bytesRead, err
	}

	result := &extractedHTML{}
	linkSet := make(map[string]bool)
	headingSet := make(map[string]bool)
	var textBuilder strings.Builder
	baseHost := strings.ToLower(baseURL.Hostname())

	var traverse func(*html.Node, bool)
	traverse = func(n *html.Node, inBody bool) {
		if n.Type == html.ElementNode {
			tag := strings.ToLower(n.Data)
			// Skip non-visible structural/executable containers
			if tag == "script" || tag == "style" || tag == "noscript" || tag == "template" || tag == "svg" {
				return
			}

			switch tag {
			case "body":
				inBody = true
			case "title":
				if result.title == "" {
					result.title = extractNodeText(n)
				}
			case "h1":
				txt := extractNodeText(n)
				if result.h1 == "" && txt != "" {
					result.h1 = txt
				}
				if txt != "" && !headingSet[txt] && len(result.headings) < 20 {
					headingSet[txt] = true
					result.headings = append(result.headings, "H1: "+txt)
				}
			case "h2", "h3":
				txt := extractNodeText(n)
				if txt != "" && !headingSet[txt] && len(result.headings) < 20 {
					headingSet[txt] = true
					result.headings = append(result.headings, strings.ToUpper(tag)+": "+txt)
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
				if rel == "canonical" && href != "" && result.canonicalURL == "" {
					result.canonicalURL = normalizeURL(href, baseURL)
				}
			case "a":
				for _, attr := range n.Attr {
					if strings.ToLower(attr.Key) == "href" {
						resolvedURL := normalizeURL(attr.Val, baseURL)
						if resolvedURL != "" && !linkSet[resolvedURL] {
							linkSet[resolvedURL] = true
							result.links = append(result.links, resolvedURL)

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
		} else if n.Type == html.TextNode && inBody {
			if textBuilder.Len() < MaxExtractedTextBytes {
				txt := strings.Join(strings.Fields(n.Data), " ")
				if txt != "" {
					if textBuilder.Len() > 0 {
						textBuilder.WriteByte(' ')
					}
					remaining := MaxExtractedTextBytes - textBuilder.Len()
					if len(txt) > remaining {
						txt = txt[:remaining]
					}
					textBuilder.WriteString(txt)
				}
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			traverse(c, inBody)
		}
	}

	traverse(doc, false)
	result.extractedText = strings.TrimSpace(textBuilder.String())
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
	return strings.Join(strings.Fields(sb.String()), " ")
}

// normalizeURL converts relative URLs to absolute URLs, strips fragments, and removes disallowed schemes.
func normalizeURL(href string, baseURL *url.URL) string {
	href = strings.TrimSpace(href)
	if href == "" ||
		strings.HasPrefix(strings.ToLower(href), "javascript:") ||
		strings.HasPrefix(strings.ToLower(href), "mailto:") ||
		strings.HasPrefix(strings.ToLower(href), "tel:") ||
		strings.HasPrefix(strings.ToLower(href), "data:") ||
		strings.HasPrefix(href, "#") {
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
	resolved.User = nil

	if len(resolved.String()) > MaxURLLength {
		return ""
	}

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
