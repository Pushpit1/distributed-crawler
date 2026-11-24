package crawler

import (
	"net"
	"testing"
)

func TestIsPrivateOrReservedIP(t *testing.T) {
	tests := []struct {
		ip       string
		blocked  bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true}, // AWS / GCP metadata IP
		{"0.0.0.0", true},
		{"224.0.0.1", true},       // Multicast
		{"8.8.8.8", false},        // Public Google DNS
		{"1.1.1.1", false},        // Public Cloudflare DNS
		{"142.250.190.46", false}, // Public Google web IP
	}

	for _, tt := range tests {
		parsed := net.ParseIP(tt.ip)
		if parsed == nil {
			t.Fatalf("Failed to parse test IP %s", tt.ip)
		}
		got := IsPrivateOrReservedIP(parsed)
		if got != tt.blocked {
			t.Errorf("IsPrivateOrReservedIP(%s) = %v; want %v", tt.ip, got, tt.blocked)
		}
	}
}

func TestValidateTargetURL_SSRF(t *testing.T) {
	blockedURLs := []string{
		"http://127.0.0.1/admin",
		"http://localhost:8080/metrics",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.5:6379",
		"http://192.168.1.1/router",
		"ftp://example.com/file.txt",
		"file:///etc/passwd",
	}

	for _, u := range blockedURLs {
		_, err := ValidateTargetURL(u, false)
		if err == nil {
			t.Errorf("Expected URL %s to be blocked by SSRF validation, but it passed", u)
		}
	}

	// Valid public URLs should pass parsing and resolution
	validURLs := []string{
		"https://go.dev",
		"https://pkg.go.dev",
		"https://example.com",
	}

	for _, u := range validURLs {
		_, err := ValidateTargetURL(u, false)
		if err != nil {
			t.Errorf("Expected public URL %s to pass validation, got: %v", u, err)
		}
	}
}
