// Package ssrfguard builds HTTP clients that refuse to connect to private,
// link-local, CGNAT, or unspecified addresses — the SSRF surface a
// prompt-injected URL would aim at (cloud metadata at 169.254.169.254,
// RFC1918 internal services). Loopback is allowed: the agent can already
// reach localhost via bash, so local dev servers stay fetchable. The check
// runs at dial time on the resolved IP, so a public host that redirects or
// DNS-rebinds to an internal address is caught too.
//
// Proxy-aware (ADR-0004): when the effective proxy for a request is an
// http/https CONNECT or socks5/socks5h tunnel, the proxy resolves the target
// remotely — so targets are checked as IP literals only, and the proxy itself
// (frequently on a private/LAN address the user configured) is dialed
// directly, never blocked. Wrapping a proxied transport's DialContext instead
// is the mistake this package exists to prevent: it rejects LAN proxies and
// never inspects the real destination.
package ssrfguard

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/net/proxy"
)

// GuardedClient returns a client whose every request dials through a guarded
// transport as described in the package comment. proxyURLFor resolves the
// effective proxy URL per request ("" = direct); timeout bounds each request
// including body reads, so a stalled backend cannot hang the caller.
func GuardedClient(proxyURLFor func(*http.Request) (string, error), timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: roundTripper{proxyURLFor: proxyURLFor},
	}
}

type roundTripper struct {
	proxyURLFor func(*http.Request) (string, error)
}

func (rt roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	proxyURL, err := rt.proxyURLFor(req)
	if err != nil {
		return nil, fmt.Errorf("resolve proxy: %w", err)
	}
	return guardedTransport(proxyURL).RoundTrip(req)
}

// GuardedDialContext wraps inner so a connection only ever dials a locally
// resolved, blocklist-vetted IP: it resolves DNS itself, refuses when any
// resolved address is blocked, then dials the vetted IP (not the hostname)
// to prevent DNS rebinding. Exported so other packages can retrofit the same
// guard onto a plain transport (see installsource.ssrfGuardClient).
func GuardedDialContext(inner func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if BlockedIP(ip.IP) {
				return nil, fmt.Errorf("refusing to fetch internal address %s (resolves to %s)", host, ip.IP)
			}
		}
		return inner(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
}

func guardedTransport(proxyURL string) *http.Transport {
	dialer := &net.Dialer{Timeout: 15 * time.Second}

	tr := &http.Transport{
		DialContext: GuardedDialContext(dialer.DialContext),
	}

	if proxyURL != "" {
		pu, err := url.Parse(proxyURL)
		if err == nil && pu.Host != "" {
			switch pu.Scheme {
			case "http", "https":
				// HTTP CONNECT: dial proxy → send CONNECT with the ORIGINAL
				// hostname (not a locally-resolved IP) so the proxy handles DNS.
				// This is essential for users whose local DNS is blocked (GFW).
				// SSRF protection: IP literals are checked directly; domain names
				// go through the trusted proxy which resolves them.
				proxyDialer := dialer
				tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
					host, port, err := net.SplitHostPort(addr)
					if err != nil {
						return nil, err
					}
					// SSRF check on IP literals only — domain names go through
					// the trusted proxy which resolves them on the remote side.
					if ip := net.ParseIP(host); ip != nil {
						if BlockedIP(ip) {
							return nil, fmt.Errorf("refusing to fetch internal address %s (resolves to %s)", host, ip)
						}
					}
					// Dial the proxy (proxy address is never an SSRF target — the
					// user configured it, and it's almost certainly an IP or a
					// resolvable hostname reachable from the local network).
					proxyConn, err := proxyDialer.DialContext(ctx, "tcp", pu.Host)
					if err != nil {
						return nil, fmt.Errorf("connect to proxy %s: %w", pu.Host, err)
					}
					// CONNECT the ORIGINAL hostname through the proxy, letting
					// the proxy resolve DNS on the remote side. If this is an IP
					// literal we already vetted it above.
					targetAddr := net.JoinHostPort(host, port)
					connectReq := &http.Request{
						Method: http.MethodConnect,
						URL:    &url.URL{Host: targetAddr},
						Host:   targetAddr,
						Header: make(http.Header),
					}
					if pu.User != nil {
						user := pu.User.Username()
						pass, _ := pu.User.Password()
						auth := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
						connectReq.Header.Set("Proxy-Authorization", "Basic "+auth)
					}
					if err := connectReq.Write(proxyConn); err != nil {
						proxyConn.Close()
						return nil, fmt.Errorf("write CONNECT to proxy: %w", err)
					}
					br := bufio.NewReader(proxyConn)
					resp, err := http.ReadResponse(br, connectReq)
					if err != nil {
						proxyConn.Close()
						return nil, fmt.Errorf("read CONNECT response: %w", err)
					}
					if resp.StatusCode != http.StatusOK {
						proxyConn.Close()
						return nil, fmt.Errorf("proxy CONNECT failed: %s", resp.Status)
					}
					return proxyConn, nil
				}
				tr.Proxy = nil

			case "socks5", "socks5h":
				// Tunnel through SOCKS5. Dial the trusted proxy with a plain
				// dialer (a proxy on a private/LAN address must not be rejected
				// by the SSRF guard), then route the target through it. IP-literal
				// targets are still SSRF-checked; hostnames are resolved by the
				// proxy — the same boundary as the HTTP CONNECT path above.
				var auth *proxy.Auth
				if pu.User != nil {
					pass, _ := pu.User.Password()
					auth = &proxy.Auth{User: pu.User.Username(), Password: pass}
				}
				if sd, err := proxy.SOCKS5("tcp", pu.Host, auth, dialer); err == nil {
					if cd, ok := sd.(proxy.ContextDialer); ok {
						tr.Proxy = nil
						tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
							host, _, err := net.SplitHostPort(addr)
							if err != nil {
								return nil, err
							}
							if ip := net.ParseIP(host); ip != nil && BlockedIP(ip) {
								return nil, fmt.Errorf("refusing to fetch internal address %s (resolves to %s)", host, ip)
							}
							return cd.DialContext(ctx, network, addr)
						}
					}
				}
			}
		}
	}

	return tr
}

// cgnatRange is RFC 6598 shared address space (100.64.0.0/10). Go's IsPrivate
// doesn't cover it, yet some clouds host instance metadata there (Alibaba
// Cloud at 100.100.100.200), so it's an SSRF target to refuse too.
var cgnatRange = mustCIDR("100.64.0.0/10")

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// BlockedIP reports whether ip is an address guarded clients must not reach
// directly. Loopback is intentionally allowed.
func BlockedIP(ip net.IP) bool {
	return ip.IsPrivate() || // RFC1918 + IPv6 unique-local (fc00::/7)
		ip.IsLinkLocalUnicast() || // 169.254.0.0/16 (incl. cloud metadata) + fe80::/10
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || // 0.0.0.0 / ::
		cgnatRange.Contains(ip) // 100.64.0.0/10 (incl. Alibaba Cloud metadata)
}
