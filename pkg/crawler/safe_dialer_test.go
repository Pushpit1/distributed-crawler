package crawler

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestIsPrivateOrReservedIP(t *testing.T) {
	tests := []struct {
		ip      string
		blocked bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"::ffff:127.0.0.1", true},
		{"::ffff:169.254.169.254", true},
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true},
		{"fe80::1", true},
		{"fc00::1", true},
		{"100.64.0.1", true},
		{"0.0.0.0", true},
		{"224.0.0.1", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"142.250.190.46", false},
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
		"http://[::1]:6379",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.5:6379",
		"http://192.168.1.1/router",
		"http://user:secret@8.8.8.8/",
		"ftp://example.com/file.txt",
		"file:///etc/passwd",
		"javascript:alert(1)",
	}

	for _, u := range blockedURLs {
		_, err := ValidateTargetURL(u, false)
		if err == nil {
			t.Errorf("Expected URL %s to be blocked by SSRF validation, but it passed", u)
		}
	}

	validURLs := []string{
		"https://8.8.8.8",
		"http://1.1.1.1/dns-query",
	}
	for _, u := range validURLs {
		if _, err := ValidateTargetURL(u, false); err != nil {
			t.Errorf("Expected public IP URL %s to pass validation, got: %v", u, err)
		}
	}
}

func TestSafeRedirectPolicy_BlocksInternalHop(t *testing.T) {
	u, _ := url.Parse("http://169.254.169.254/latest/meta-data/")
	req := &http.Request{URL: u}
	if err := SafeRedirectPolicy(req, []*http.Request{{}}); err == nil {
		t.Fatal("Expected SafeRedirectPolicy to reject redirect to metadata IP")
	}

	// Verify infinite redirect ceiling (>5)
	pub, _ := url.Parse("https://8.8.8.8/")
	pubReq := &http.Request{URL: pub}
	via := make([]*http.Request, 5)
	if err := SafeRedirectPolicy(pubReq, via); err == nil {
		t.Fatal("Expected SafeRedirectPolicy to cap redirect chain at 5 hops")
	}
}

func TestSanitizeForLog_And_MatchesURLPolicy(t *testing.T) {
	dirty := "https://example.com\n[FAKE LOG INJECTION]\r\t"
	clean := SanitizeForLog(dirty)
	if clean == dirty || clean[len("https://example.com")] == '\n' {
		t.Errorf("Expected CR/LF stripped from log output, got %q", clean)
	}

	// Domain allowlist + exclude pattern test
	ok, _ := MatchesURLPolicy("https://docs.go.dev/intro", []string{"go.dev"}, []string{"/login"})
	if !ok {
		t.Error("Expected subdomain of allowed go.dev to pass")
	}
	ok, _ = MatchesURLPolicy("https://docs.go.dev/login", []string{"go.dev"}, []string{"/login"})
	if ok {
		t.Error("Expected /login path to be excluded")
	}
	ok, _ = MatchesURLPolicy("https://evil.com/page", []string{"go.dev"}, nil)
	if ok {
		t.Error("Expected non-allowed domain evil.com to fail policy")
	}

	// Suppress unused import warning on fmt/httptest if referenced cleanly
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	ts.Close()
}
