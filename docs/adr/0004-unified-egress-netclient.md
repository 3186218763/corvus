# ADR-0004: All egress through netclient; one SSRF guard; no silent fallbacks

- Date: 2026-08-16
- Status: Accepted

## Context

Every HTTP client that leaves the process made its own networking decisions.
The main chain (provider chat, billing, web_search, the MCP server binary)
built clients via `netclient.NewHTTPClient` and honored the user's proxy
settings. Five shadow clients did not:

- `openai.FetchModels` used a bare `&http.Client{}` — proxy users could chat
  but never fetch model lists.
- The responses provider silently fell back to a bare direct client when
  `netclient.NewHTTPClient` errored.
- The plugin HTTP/SSE transports used bare clients with two verbatim-duplicated
  redirect lambdas — remote MCP connections bypassed the user's proxy.
- billing kept a package-default client with zero production callers, and the
  injected one was built without the 12s timeout its own comment promised.
- install_source wrapped a *proxied* client's DialContext with its SSRF guard:
  the guard then inspected the **proxy's** address, rejecting LAN proxies
  (clash on 192.168.x) outright, and never saw the real destination through
  the tunnel. web_fetch had already solved this with a proxy-aware guard; the
  two guards' blocklist trio (`blockedFetchIP`/`cgnatRange`/`mustCIDR`) was
  duplicated byte-for-byte with a "kept in sync by hand" comment.

## Decision

1. All outbound HTTP goes through `netclient` (proxy spec resolved from user
   config). New call sites must not construct `&http.Client{}` for egress.
   Exceptions: in-package test clients and injectable defaults that a host
   fills with a netclient-built client.
2. Construction failures are loud: boot validates the proxy spec before
   building providers (existing behavior), and every client builder errors
   out instead of falling back to a direct connection.
3. The SSRF guard lives once in `internal/ssrfguard` (blocklist + proxy-aware
   guarded client). web_fetch and install_source both use it. Through a
   proxy, targets are checked as IP literals only; the proxy itself is never
   blocked. Wrapping a proxied transport's DialContext with a guard is the
   anti-pattern this package prevents.
4. User-configured endpoints (MCP server URLs, provider base URLs) get proxy
   support but **no** SSRF guard — the guard's threat model is URLs a model
   can influence, and those endpoints are user-authored config.
5. The shared redirect policy for plugin transports (same-origin only, so
   credentials never cross origins) is one function, `sameOriginRedirect`.

## Consequences

- install_source works again for LAN-proxy users (regression-pinned by
  `TestGuardedClientDoesNotRefusePrivateProxy`), and proxied fetches now
  check target IP literals like web_fetch.
- `openai.New`-style constructors returning `(Provider, error)` is the
  convention; responses.New was aligned.
- billing requires an injected client; boot's carries the 12s bound.
- `netclient.TransportOptions.Timeout` must be set by body-consuming callers
  (per the type's own doc).
