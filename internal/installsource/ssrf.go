package installsource

import (
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
		ct.DialContext = ssrfguard.GuardedDialContext(inner)
		guarded.Transport = ct
	} else {
		// Non-*http.Transport (or nil Transport): build a fresh guarded transport.
		// The real paths — tests' httptest clients — are always *http.Transport,
		// so this branch only covers a bare &http.Client{}.
		guarded.Transport = &http.Transport{DialContext: ssrfguard.GuardedDialContext((&net.Dialer{}).DialContext)}
	}
	return &guarded
}
