package ssrfguard

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestBlockedIP(t *testing.T) {
	blocked := []string{
		"169.254.169.254",      // cloud metadata (link-local)
		"10.1.2.3",             // RFC1918
		"172.16.5.6",           // RFC1918
		"192.168.1.1",          // RFC1918
		"0.0.0.0",              // unspecified
		"fe80::1",              // IPv6 link-local
		"fc00::1",              // IPv6 unique-local
		"::ffff:10.0.0.1",      // IPv4-mapped private
		"100.100.100.200",      // Alibaba Cloud metadata (CGNAT)
		"100.64.0.1",           // RFC 6598 shared space
		"::ffff:100.100.100.1", // IPv4-mapped CGNAT
	}
	for _, s := range blocked {
		if !BlockedIP(net.ParseIP(s)) {
			t.Errorf("%s should be blocked", s)
		}
	}
	allowed := []string{"8.8.8.8", "1.1.1.1", "127.0.0.1", "::1", "93.184.216.34"}
	for _, s := range allowed {
		if BlockedIP(net.ParseIP(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
}

// TestGuardedClientDoesNotRefusePrivateProxy pins the ADR-0004 fix: the guard
// must never reject the proxy itself. install_source previously wrapped a
// proxied transport's DialContext, so a LAN proxy (clash on 192.168.x.x) was
// itself "refused" as an internal address and every fetch failed. The proxy is
// dialed directly here; only unreachable-port noise may come back.
func TestGuardedClientDoesNotRefusePrivateProxy(t *testing.T) {
	resolver := func(*http.Request) (string, error) { return "http://127.0.0.1:1", nil }
	cli := GuardedClient(resolver, 5*time.Second)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.invalid/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = cli.Do(req)
	if err == nil {
		t.Fatal("dial to closed port 1 should fail")
	}
	if strings.Contains(err.Error(), "refusing to fetch internal address") {
		t.Fatalf("guard must not refuse the proxy itself: %v", err)
	}
}

// TestGuardedClientRefusesIPLiteralThroughProxy: even proxied, a request to a
// blocked IP literal is refused before any dial.
func TestGuardedClientRefusesIPLiteralThroughProxy(t *testing.T) {
	resolver := func(*http.Request) (string, error) { return "http://127.0.0.1:1", nil }
	cli := GuardedClient(resolver, 5*time.Second)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://10.1.2.3/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = cli.Do(req)
	if err == nil || !strings.Contains(err.Error(), "refusing to fetch internal address") {
		t.Fatalf("blocked IP literal must be refused even when proxied, got: %v", err)
	}
}
