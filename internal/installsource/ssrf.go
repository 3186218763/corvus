package installsource

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"corvus/internal/ssrfguard"
)

// ssrfGuardClient wraps base so every fetch refuses to connect to private,
// link-local, CGNAT, or unspecified addresses (see internal/ssrfguard for the
// blocklist and its rationale). Loopback is allowed: the agent can already
// reach localhost via bash, and the install tests serve over 127.0.0.1. The
// check runs at dial time on the resolved IP and then dials that vetted IP, so
// a public host that DNS-rebinds to an internal address is caught too.
//
// This wrap covers injected (direct) clients only — production fetches go
// through ssrfguard.GuardedClient, whose proxy-aware guard is the one that can
// see targets behind a proxy (ADR-0004).
func ssrfGuardClient(base *http.Client) *http.Client {
	guarded := *base // copy Timeout etc.
	if t, ok := base.Transport.(*http.Transport); ok && t != nil {
		ct := t.Clone()
		inner := ct.DialContext
		if inner == nil {
			inner = (&net.Dialer{}).DialContext
		}
		ct.DialContext = ssrfDial(inner)
		guarded.Transport = ct
	} else {
		// Non-*http.Transport (or nil Transport): build a fresh guarded transport.
		// The real paths — tests' httptest clients — are always *http.Transport,
		// so this branch only covers a bare &http.Client{}.
		guarded.Transport = &http.Transport{DialContext: ssrfDial((&net.Dialer{}).DialContext)}
	}
	return &guarded
}

func ssrfDial(inner func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
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
			if ssrfguard.BlockedIP(ip.IP) {
				return nil, fmt.Errorf("refusing to fetch internal address %s (resolves to %s)", host, ip.IP)
			}
		}
		// Dial the vetted IP, not the hostname, so the connection can't re-resolve
		// to a different (internal) address (DNS rebinding).
		return inner(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
}
