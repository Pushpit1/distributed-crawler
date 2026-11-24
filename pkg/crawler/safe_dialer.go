package crawler

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var (
	ErrSSRFBlocked   = errors.New("request blocked by SSRF security policy: private or reserved network destination")
	ErrInvalidScheme = errors.New("unsupported protocol scheme: only http and https are allowed")
)

// List of disallowed CIDR network blocks (RFC 1918, loopback, link-local, multicast, cloud metadata)
var disallowedCIDRs []*net.IPNet

func init() {
	cidrs := []string{
		"127.0.0.0/8",    // IPv4 Loopback
		"::1/128",        // IPv6 Loopback
		"10.0.0.0/8",     // RFC 1918 Private Class A
		"172.16.0.0/12",  // RFC 1918 Private Class B
		"192.168.0.0/16", // RFC 1918 Private Class C
		"169.254.0.0/16", // IPv4 Link-local & AWS/GCP metadata
		"fe80::/10",      // IPv6 Link-local
		"100.64.0.0/10",  // Carrier-grade NAT
		"0.0.0.0/8",      // "This" network
		"224.0.0.0/4",    // IPv4 Multicast
		"ff00::/8",       // IPv6 Multicast
		"240.0.0.0/4",    // Reserved for future use
	}

	for _, cidr := range cidrs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err == nil {
			disallowedCIDRs = append(disallowedCIDRs, ipNet)
		}
	}
}

// IsPrivateOrReservedIP checks if an IP is private, loopback, link-local, or reserved.
func IsPrivateOrReservedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}

	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}

	for _, block := range disallowedCIDRs {
		if block.Contains(ip) {
			return true
		}
	}

	return false
}

// ValidateTargetHost resolves the hostname and checks if any resolved IP address is in a forbidden range.
func ValidateTargetHost(host string) error {
	h := host
	if strings.Contains(host, ":") {
		if sh, _, err := net.SplitHostPort(host); err == nil {
			h = sh
		}
	}

	if ip := net.ParseIP(h); ip != nil {
		if IsPrivateOrReservedIP(ip) {
			return fmt.Errorf("%w: %s is a private or reserved IP", ErrSSRFBlocked, ip.String())
		}
		return nil
	}

	lower := strings.ToLower(h)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") || strings.HasSuffix(lower, ".local") || strings.HasSuffix(lower, ".internal") {
		return fmt.Errorf("%w: %s is a local or internal domain name", ErrSSRFBlocked, h)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resolver := net.DefaultResolver
	ips, err := resolver.LookupIP(ctx, "ip", h)
	if err != nil {
		return fmt.Errorf("DNS resolution failed for %s: %w", h, err)
	}

	if len(ips) == 0 {
		return fmt.Errorf("no IP addresses resolved for host %s", h)
	}

	for _, ip := range ips {
		if IsPrivateOrReservedIP(ip) {
			return fmt.Errorf("%w: %s resolves to private address %s", ErrSSRFBlocked, h, ip.String())
		}
	}

	return nil
}

// NewSSRFSafeTransport builds an http.Transport with custom dialer that blocks private IP connections.
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
			if err != nil {
				return nil, err
			}

			ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}

			var safeIP net.IP
			for _, ip := range ips {
				if !IsPrivateOrReservedIP(ip) {
					safeIP = ip
					break
				}
			}

			if safeIP == nil {
				return nil, fmt.Errorf("%w: %s resolved only to private/reserved addresses", ErrSSRFBlocked, host)
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

// SafeRedirectPolicy validates redirects to prevent SSRF hopping to internal IP addresses.
func SafeRedirectPolicy(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 HTTP redirects")
	}

	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return fmt.Errorf("%w: %s", ErrInvalidScheme, req.URL.Scheme)
	}

	return ValidateTargetHost(req.URL.Host)
}

// ValidateTargetURL parses and verifies URL scheme and host against SSRF rules.
func ValidateTargetURL(rawURL string, allowPrivate bool) (*url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL syntax: %w", err)
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("%w: %s", ErrInvalidScheme, parsed.Scheme)
	}

	if parsed.Host == "" {
		return nil, errors.New("missing host in target URL")
	}

	if !allowPrivate {
		if err := ValidateTargetHost(parsed.Host); err != nil {
			return nil, err
		}
	}

	return parsed, nil
}
