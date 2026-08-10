// Package netpolicy decides whether a URL may be fetched, layering a
// hostname allow/deny policy over the coarse [sandbox] network switch (which
// is only on/off). The core is a pure Policy: no I/O, no config parsing, so it
// is trivially testable and usable from both web_fetch and the bash URL guard.
//
// Rule semantics: rules are dot-separated label globs matched against the URL's
// bare hostname (scheme, port, userinfo, path, query, and fragment are all
// ignored). "*" matches exactly one label, "**" matches one or more labels, and
// a literal label matches exactly — so "example.com" matches example.com but not
// subdomains, "*.example.com" matches a.example.com but not example.com itself,
// and "**.example.com" matches a.b.example.com too. Matching is anchored,
// full-host, and case-insensitive. Rules may also be IP literals (IPv4 or bare
// IPv6); port numbers are deliberately rejected by Validate because rules
// match the port-stripped hostname.
//
// Precedence: an explicit deny rule always wins over an allow rule and over
// the Default; an explicit allow rule wins over Default=deny. When no rule
// matches, Decide returns the policy Default.
//
// Ask: a Default of Ask means "ask the user", and since the decisioner itself
// has no approval UI, Decide returns Ask and the caller resolves it per its
// Default — which, in this environment, makes an ask default behave like the
// permission package's nil approver: allow (autonomous runs must not stall).
// Unparseable URLs (no extractable hostname) fail closed: Decide returns Deny
// with an empty matched rule, so callers can distinguish "could not parse"
// from "a deny rule matched".
package netpolicy

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Decision is the outcome of evaluating a URL against a Policy.
type Decision int

const (
	// Allow fetches the URL without prompting.
	Allow Decision = iota
	// Ask defers to an interactive approver; without one, callers fall back
	// to the policy Default (an ask default resolves to Allow).
	Ask
	// Deny blocks the URL in every mode.
	Deny
)

func (d Decision) String() string {
	switch d {
	case Allow:
		return "allow"
	case Ask:
		return "ask"
	case Deny:
		return "deny"
	default:
		return "unknown"
	}
}

// ParseDecision maps a config string to a Decision. Unknown / empty input
// defaults to Ask, the conservative posture, mirroring internal/permission.
func ParseDecision(s string) Decision {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "allow":
		return Allow
	case "deny":
		return Deny
	default:
		return Ask
	}
}

// Policy is a set of hostname glob rules plus the fallback decision. The zero
// value is safe: no rules and a Default of Allow, which reproduces today's
// unconfined web_fetch behaviour.
type Policy struct {
	// Allow lists hostname patterns that are permitted even when Default is
	// deny. A deny rule still wins over an allow rule.
	Allow []string
	// Deny lists hostname patterns that are refused even when Default is
	// allow. This is the exfiltration surface the policy exists for.
	Deny []string
	// Default is the decision when no rule matches: Allow (the fail-open
	// status quo), Deny (fail closed), or Ask (no approval UI here — resolves
	// to Allow, see the package doc).
	Default Decision
}

// New returns a Policy copying the given rule lists. No validation is done
// here; call Validate before use, or rely on config.NetworkPolicy which does.
func New(allow, deny []string, def Decision) Policy {
	return Policy{
		Allow:   append([]string(nil), allow...),
		Deny:    append([]string(nil), deny...),
		Default: def,
	}
}

// Validate reports whether every rule is a legal hostname pattern. It returns
// the first offending rule with a message explaining the problem, so a
// typo'd allowlist cannot silently match nothing.
func (p Policy) Validate() error {
	for _, group := range []struct {
		name  string
		rules []string
	}{
		{"allow", p.Allow},
		{"deny", p.Deny},
	} {
		for _, r := range group.rules {
			if err := validateRule(r); err != nil {
				return fmt.Errorf("network_policy %s rule %q: %w", group.name, r, err)
			}
		}
	}
	return nil
}

// Decide evaluates rawURL against the policy and returns the decision plus
// the rule that matched (empty when the Default applies or the URL could not
// be parsed). An unparseable URL is denied with an empty rule — fail closed.
func (p Policy) Decide(rawURL string) (Decision, string) {
	host, err := hostOf(rawURL)
	if err != nil {
		return Deny, ""
	}
	for _, rule := range p.Deny {
		if matchPattern(rule, host) {
			return Deny, rule
		}
	}
	for _, rule := range p.Allow {
		if matchPattern(rule, host) {
			return Allow, rule
		}
	}
	return p.Default, ""
}

// validateRule checks a single hostname pattern.
func validateRule(rule string) error {
	r := strings.TrimSpace(rule)
	if r == "" {
		return errors.New("empty rule")
	}
	if strings.ContainsAny(r, " \t\r\n") {
		return errors.New("contains whitespace")
	}
	if strings.Contains(r, "?") {
		return errors.New(`"?" is not supported; use "*" or "**" as whole labels`)
	}
	if strings.Contains(r, "://") || strings.ContainsAny(r, "/#@") {
		return errors.New("must be a bare hostname pattern, not a URL or path")
	}
	if strings.ContainsAny(r, "[]") {
		return errors.New("IPv6 literals are matched without brackets")
	}
	if strings.Contains(r, ":") && net.ParseIP(r) == nil {
		return errors.New("ports are not allowed; rules match the bare hostname")
	}
	for _, label := range strings.Split(strings.TrimSuffix(r, "."), ".") {
		if label == "" {
			return errors.New("empty label (consecutive dots)")
		}
		if label == "*" || label == "**" {
			continue
		}
		for _, ch := range label {
			if !isValidLabelChar(ch) {
				return fmt.Errorf("invalid character %q in label %q (wildcards must be a whole label)", ch, label)
			}
		}
	}
	return nil
}

func isValidLabelChar(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' ||
		r >= '0' && r <= '9' || r == '-' || r == '_' || r == ':'
}

// matchPattern reports whether host matches pattern (see the package doc for
// the label-glob semantics). Both sides are lowercased and a single trailing
// dot on either side is ignored, so "example.com." matches "example.com".
func matchPattern(pattern, host string) bool {
	pattern = strings.ToLower(strings.TrimSuffix(pattern, "."))
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if pattern == host {
		return true
	}
	return matchLabels(strings.Split(pattern, "."), strings.Split(host, "."))
}

// matchLabels matches pattern labels against host labels. "*" consumes
// exactly one label; "**" consumes one or more (backtracking).
func matchLabels(p, h []string) bool {
	for len(p) > 0 && p[0] != "**" {
		if len(h) == 0 || p[0] != "*" && p[0] != h[0] {
			return false
		}
		p, h = p[1:], h[1:]
	}
	if len(p) == 0 {
		return len(h) == 0
	}
	// p[0] == "**": must consume at least one label, then the rest must match.
	for i := 1; i <= len(h); i++ {
		if matchLabels(p[1:], h[i:]) {
			return true
		}
	}
	return false
}

// hostOf extracts the bare, lowercased hostname from rawURL. It accepts
// absolute URLs with a scheme and bare host[:port][/path] forms without one.
// Ports, userinfo, path, query, and fragment are stripped; a single trailing
// dot (fully-qualified form) is trimmed. Anything without an extractable
// hostname is an error, which Decide turns into a fail-closed Deny.
func hostOf(rawURL string) (string, error) {
	s := strings.TrimSpace(rawURL)
	if s == "" {
		return "", errors.New("empty URL")
	}
	if i := strings.Index(s, "://"); i > 0 {
		u, err := url.Parse(s)
		if err != nil || u == nil {
			return "", fmt.Errorf("parse URL: %w", err)
		}
		if u.Host == "" {
			return "", errors.New("URL has no host")
		}
		return normalizeHost(u.Hostname()), nil
	}
	// Bare host[:port][/path][?query][#frag] without a scheme.
	host := s
	if i := strings.IndexAny(host, "/?#"); i >= 0 {
		host = host[:i]
	}
	if i := strings.LastIndexByte(host, '@'); i >= 0 {
		host = host[i+1:]
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return "", errors.New("no host")
	}
	if strings.ContainsAny(host, " \t\r\n") {
		return "", errors.New("host contains whitespace")
	}
	switch {
	case strings.HasPrefix(host, "["):
		// Bracketed IPv6, optionally followed by :port.
		if i := strings.IndexByte(host, ']'); i >= 0 {
			ip := host[1:i]
			if net.ParseIP(ip) == nil {
				return "", fmt.Errorf("invalid IPv6 literal %q", ip)
			}
			return normalizeHost(ip), nil
		}
		return "", errors.New("unterminated IPv6 literal")
	case strings.Count(host, ":") == 0:
		return normalizeHost(host), nil
	case strings.Count(host, ":") == 1:
		h, port, _ := strings.Cut(host, ":")
		if port != "" {
			if _, err := strconv.Atoi(port); err != nil {
				return "", fmt.Errorf("invalid port %q", port)
			}
		}
		if h == "" {
			return "", errors.New("no host")
		}
		return normalizeHost(h), nil
	default:
		// Bare IPv6 literal without brackets.
		if net.ParseIP(host) != nil {
			return normalizeHost(host), nil
		}
		return "", errors.New("invalid host")
	}
}

func normalizeHost(h string) string {
	return strings.TrimSuffix(strings.ToLower(h), ".")
}
