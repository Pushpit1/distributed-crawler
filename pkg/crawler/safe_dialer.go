package crawler

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	ErrSSRFBlocked   = errors.New("request blocked by SSRF security policy: private or reserved network destination")
	ErrInvalidScheme = errors.New("unsupported protocol scheme: only http and https are allowed")
	ErrInvalidURL    = errors.New("invalid or malformed target URL")
)

const MaxURLLength = 2048

// disallowedCIDRs covers loopback, RFC1918, link-local, cloud metadata, CGNAT, ULA, and reserved ranges.
var disallowedCIDRs []*net.IPNet

func init() {
	cidrs := []string{
		"127.0.0.0/8",     // IPv4 Loopback
		"::1/128",         // IPv6 Loopback
		"10.0.0.0/8",      // RFC 1918 Private Class A
		"172.16.0.0/12",   // RFC 1918 Private Class B
		"192.168.0.0/16",  // RFC 1918 Private Class C
		"169.254.0.0/16",  // IPv4 Link-local & AWS/GCP/Azure metadata
		"fe80::/10",       // IPv6 Link-local
		"fc00::/7",        // IPv6 Unique Local Address (ULA)
		"100.64.0.0/10",   // Carrier-grade NAT (RFC 6598)
		"0.0.0.0/8",       // "This" network
		"192.0.0.0/24",    // IETF Protocol Assignments
		"192.0.2.0/24",    // TEST-NET-1
		"198.18.0.0/15",   // Network benchmark tests
		"198.51.100.0/24", // TEST-NET-2
		"203.0.113.0/24",  // TEST-NET-3
		"224.0.0.0/4",     // IPv4 Multicast
		"ff00::/8",        // IPv6 Multicast
		"240.0.0.0/4",     // Reserved for future use
		"2001::/32",       // Teredo tunneling
		"2002::/16",       // 6to4 tunneling
	}

	for _, cidr := range cidrs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err == nil {
			disallowedCIDRs = append(disallowedCIDRs, ipNet)
		}
	}
}

// SanitizeForLog removes CR/LF and ASCII control characters to prevent log injection.
func SanitizeForLog(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\r' || r == '\n' || r == '\t' || r < 0x20 || r == 0x7f {
			b.WriteByte(' ')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// IsPrivateOrReservedIP checks whether an IP address is private, loopback, link-local, or reserved.
func IsPrivateOrReservedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}

	// Normalize IPv4-mapped IPv6 addresses (e.g. ::ffff:127.0.0.1 -> 127.0.0.1)
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
	}

	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() ||
		ip.IsInterfaceLocalMulticast() {
		return true
	}

	for _, block := range disallowedCIDRs {
		if block.Contains(ip) {
			return true
		}
	}

	return false
}

// ValidateTargetHost resolves the hostname and verifies none of its IPs lie in forbidden ranges.
func ValidateTargetHost(host string) error {
	h := host
	if strings.Contains(host, ":") {
		if sh, _, err := net.SplitHostPort(host); err == nil {
			h = sh
		}
	}
	h = strings.Trim(h, "[]")

	if h == "" {
		return fmt.Errorf("%w: empty hostname", ErrInvalidURL)
	}

	if ip := net.ParseIP(h); ip != nil {
		if IsPrivateOrReservedIP(ip) {
			return fmt.Errorf("%w: %s is a private or reserved IP", ErrSSRFBlocked, ip.String())
		}
		return nil
	}

	lower := strings.ToLower(h)
	if lower == "localhost" ||
		strings.HasSuffix(lower, ".localhost") ||
		strings.HasSuffix(lower, ".local") ||
		strings.HasSuffix(lower, ".internal") ||
		strings.HasSuffix(lower, ".localdomain") ||
		strings.HasSuffix(lower, ".arpa") {
		return fmt.Errorf("%w: %s is a restricted internal domain name", ErrSSRFBlocked, h)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", h)
	if err != nil {
		return fmt.Errorf("DNS resolution failed for %s: %w", h, err)
	}

	if len(ips) == 0 {
		return fmt.Errorf("no IP addresses resolved for host %s", h)
	}

	for _, ip := range ips {
		if IsPrivateOrReservedIP(ip) {
			return fmt.Errorf("%w: %s resolves to forbidden address %s", ErrSSRFBlocked, h, ip.String())
		}
	}

	return nil
}

// NewSSRFSafeTransport builds an http.Transport that pins DialContext to verified public IPs
// to mitigate DNS rebinding between URL validation and socket dial.
func NewSSRFSafeTransport(timeout time.Duration) *http.Transport {
	dialer := &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
	}

	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, portStr, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}

			port, err := strconv.Atoi(portStr)
			if err != nil || port < 1 || port > 65535 {
				return nil, fmt.Errorf("invalid target port: %s", portStr)
			}

			if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
				if IsPrivateOrReservedIP(ip) {
					return nil, fmt.Errorf("%w: %s is private/reserved", ErrSSRFBlocked, ip.String())
				}
				return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), strconv.Itoa(port)))
			}

			ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}

			var safeIP net.IP
			for _, ip := range ips {
				if IsPrivateOrReservedIP(ip) {
					return nil, fmt.Errorf("%w: %s resolved to restricted address %s", ErrSSRFBlocked, host, ip.String())
				}
				if safeIP == nil {
					safeIP = ip
				}
			}

			if safeIP == nil {
				return nil, fmt.Errorf("%w: %s produced no usable public IP", ErrSSRFBlocked, host)
			}

			targetAddr := net.JoinHostPort(safeIP.String(), strconv.Itoa(port))
			return dialer.DialContext(ctx, network, targetAddr)
		},
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: timeout,
	}
}

// SafeRedirectPolicy validates HTTP redirect targets to prevent SSRF redirect hopping and infinite loops.
func SafeRedirectPolicy(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("stopped after 5 HTTP redirects")
	}

	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return fmt.Errorf("%w: %s", ErrInvalidScheme, req.URL.Scheme)
	}

	if req.URL.User != nil {
		return fmt.Errorf("%w: embedded URL credentials prohibited", ErrInvalidURL)
	}

	return ValidateTargetHost(req.URL.Host)
}

// ValidateTargetURL validates scheme, structure, credentials, length, and SSRF host rules.
func ValidateTargetURL(rawURL string, allowPrivate bool) (*url.URL, error) {
	trimmed := strings.TrimSpace(rawURL)
	if len(trimmed) == 0 || len(trimmed) > MaxURLLength {
		return nil, fmt.Errorf("%w: length must be between 1 and %d", ErrInvalidURL, MaxURLLength)
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("%w: %s", ErrInvalidScheme, parsed.Scheme)
	}

	if parsed.Host == "" || parsed.Hostname() == "" {
		return nil, fmt.Errorf("%w: missing host", ErrInvalidURL)
	}

	if parsed.User != nil {
		return nil, fmt.Errorf("%w: URL credentials are not allowed", ErrInvalidURL)
	}

	if !allowPrivate {
		if err := ValidateTargetHost(parsed.Host); err != nil {
			return nil, err
		}
	}

	return parsed, nil
}

// MatchesURLPolicy checks domain allowlists and substring/regex exclusion patterns.
func MatchesURLPolicy(rawURL string, allowedDomains, excludePatterns []string) (bool, string) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false, "invalid URL syntax"
	}

	host := strings.ToLower(parsed.Hostname())

	if len(allowedDomains) > 0 {
		matched := false
		for _, allowed := range allowedDomains {
			a := strings.ToLower(strings.TrimSpace(allowed))
			if a == "" {
				continue
			}
			if host == a || strings.HasSuffix(host, "."+a) {
				matched = true
				break
			}
		}
		if !matched {
			return false, fmt.Sprintf("domain %s not in allowed_domains", host)
		}
	}

	for _, pat := range excludePatterns {
		p := strings.TrimSpace(pat)
		if p == "" {
			continue
		}
		if re, err := regexp.Compile(p); err == nil {
			if re.MatchString(rawURL) {
				return false, fmt.Sprintf("URL matched exclude pattern %q", p)
			}
		} else if strings.Contains(strings.ToLower(rawURL), strings.ToLower(p)) {
			return false, fmt.Sprintf("URL matched excluded substring %q", p)
		}
	}

	return true, ""
}
