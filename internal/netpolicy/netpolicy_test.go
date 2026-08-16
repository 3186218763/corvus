package netpolicy

import (
	"strings"
	"testing"
)

func TestDecisionStringAndParse(t *testing.T) {
	for _, tc := range []struct {
		d    Decision
		want string
	}{
		{Allow, "allow"},
		{Ask, "ask"},
		{Deny, "deny"},
		{Decision(99), "unknown"},
	} {
		if got := tc.d.String(); got != tc.want {
			t.Errorf("Decision(%d).String() = %q, want %q", int(tc.d), got, tc.want)
		}
	}
}

func TestDecide(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		allow    []string
		deny     []string
		def      Decision
		want     Decision
		wantRule string
	}{
		// No rules, default allow: everything passes (status quo).
		{name: "empty policy default allow", url: "https://anything.example", want: Allow},

		// Exact host matching.
		{name: "exact host allow", url: "https://example.com/x", allow: []string{"example.com"}, want: Allow, wantRule: "example.com"},
		{name: "exact host no subdomain defaults to deny", url: "https://a.example.com/x", allow: []string{"example.com"}, def: Deny, want: Deny},
		{name: "exact host with default deny", url: "https://example.com/x", allow: []string{"example.com"}, def: Deny, want: Allow, wantRule: "example.com"},
		{name: "exact host deny", url: "https://example.com/", deny: []string{"example.com"}, want: Deny, wantRule: "example.com"},
		{name: "subdomain not denied by exact", url: "https://a.example.com/", deny: []string{"example.com"}, want: Allow},

		// Subdomain wildcard: *.example.com matches one level down, not the apex.
		{name: "star matches subdomain", url: "https://a.example.com/", deny: []string{"*.example.com"}, want: Deny, wantRule: "*.example.com"},
		{name: "star matches deep subdomain", url: "https://a.b.example.com/", deny: []string{"**.example.com"}, want: Deny, wantRule: "**.example.com"},
		{name: "star does not match apex", url: "https://example.com/", deny: []string{"*.example.com"}, want: Allow},
		{name: "star one label only", url: "https://a.b.example.com/", deny: []string{"*.example.com"}, want: Allow},
		{name: "star star matches deep", url: "https://a.b.example.com/", deny: []string{"**.example.com"}, want: Deny, wantRule: "**.example.com"},
		{name: "star star requires a label", url: "https://example.com/", deny: []string{"**.example.com"}, want: Allow},

		// Port stripping.
		{name: "port stripped", url: "http://example.com:8080/p", deny: []string{"example.com"}, want: Deny, wantRule: "example.com"},
		{name: "port on wildcard", url: "https://a.example.com:443/x", deny: []string{"*.example.com"}, want: Deny, wantRule: "*.example.com"},

		// IP literals.
		{name: "ipv4 exact", url: "http://192.168.1.1/", deny: []string{"192.168.1.1"}, want: Deny, wantRule: "192.168.1.1"},
		{name: "ipv4 with port", url: "http://10.0.0.5:9000/", deny: []string{"10.0.0.5"}, want: Deny, wantRule: "10.0.0.5"},
		{name: "bare ipv4 no scheme", url: "10.1.2.3", deny: []string{"10.1.2.3"}, want: Deny, wantRule: "10.1.2.3"},
		{name: "ipv4 wildcard labels", url: "http://192.168.7.9/", deny: []string{"192.168.*.*"}, want: Deny, wantRule: "192.168.*.*"},
		{name: "ipv6 bracketed", url: "http://[2001:db8::1]:80/x", deny: []string{"2001:db8::1"}, want: Deny, wantRule: "2001:db8::1"},
		{name: "ipv6 bare", url: "2001:db8::1", deny: []string{"2001:db8::1"}, want: Deny, wantRule: "2001:db8::1"},
		{name: "ipv6 loopback", url: "http://[::1]/", deny: []string{"::1"}, want: Deny, wantRule: "::1"},

		// Case insensitivity.
		{name: "upper host lower rule", url: "HTTPS://Example.COM:443/a", deny: []string{"example.com"}, want: Deny, wantRule: "example.com"},
		{name: "lower host upper rule", url: "https://a.example.com/", deny: []string{"*.EXAMPLE.COM"}, want: Deny, wantRule: "*.EXAMPLE.COM"},

		// No scheme forms.
		{name: "bare host", url: "example.com", deny: []string{"example.com"}, want: Deny, wantRule: "example.com"},
		{name: "bare host path", url: "example.com/path?q=1", deny: []string{"example.com"}, want: Deny, wantRule: "example.com"},
		{name: "bare host with scheme-like colon", url: "example.com:8080/x", deny: []string{"example.com"}, want: Deny, wantRule: "example.com"},
		{name: "userinfo stripped", url: "http://user:pass@example.com/", deny: []string{"example.com"}, want: Deny, wantRule: "example.com"},

		// Paths, queries, fragments ignored.
		{name: "path query fragment ignored", url: "https://example.com/a/b?x=1#frag", deny: []string{"example.com"}, want: Deny, wantRule: "example.com"},

		// FQDN trailing dot.
		{name: "trailing dot host", url: "https://example.com./", deny: []string{"example.com"}, want: Deny, wantRule: "example.com"},
		{name: "trailing dot rule", url: "https://example.com/", deny: []string{"example.com."}, want: Deny, wantRule: "example.com."},

		// Precedence: deny > allow > default.
		{name: "deny beats allow", url: "https://a.example.com/", allow: []string{"*.example.com"}, deny: []string{"a.example.com"}, want: Deny, wantRule: "a.example.com"},
		{name: "allow beats default deny", url: "https://ok.example.com/", allow: []string{"*.example.com"}, def: Deny, want: Allow, wantRule: "*.example.com"},
		{name: "default deny no rules", url: "https://anything.example/", def: Deny, want: Deny},
		{name: "default deny nonmatching deny", url: "https://x.org/", deny: []string{"example.com"}, def: Deny, want: Deny},
		{name: "default ask no rules", url: "https://anything.example/", def: Ask, want: Ask},
		{name: "default ask allow rule wins", url: "https://x.org/", allow: []string{"x.org"}, def: Ask, want: Allow, wantRule: "x.org"},

		// Unparseable URLs fail closed.
		{name: "unparseable denied", url: "not a url at all", want: Deny},
		{name: "unparseable empty", url: "", want: Deny},
		{name: "unparseable bad port", url: "example.com:abc", want: Deny},
		{name: "unparseable no host", url: "http:///path", want: Deny},
		{name: "unparseable scheme only", url: "https://", want: Deny},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := New(tc.allow, tc.deny, tc.def)
			got, rule := p.Decide(tc.url)
			if got != tc.want {
				t.Errorf("Decide(%q) = %v (rule %q), want %v", tc.url, got, rule, tc.want)
			}
			if rule != tc.wantRule {
				t.Errorf("Decide(%q) rule = %q, want %q", tc.url, rule, tc.wantRule)
			}
		})
	}
}

func TestDecideUnparseableRuleEmpty(t *testing.T) {
	p := New(nil, nil, Allow)
	if d, rule := p.Decide("###"); d != Deny || rule != "" {
		t.Errorf("unparseable: got (%v, %q), want (Deny, \"\")", d, rule)
	}
}

func TestValidate(t *testing.T) {
	valid := [][]string{
		nil,
		{"example.com"},
		{"*.example.com"},
		{"**.example.com"},
		{"a.b-c.example.com"},
		{"1.2.3.4"},
		{"2001:db8::1"},
		{"192.168.*.*"},
		{"*"},
		{"**"},
		{"*."},
		{"example.com."},
		{"localhost"},
		{"_internal.svc"},
	}
	for _, rules := range valid {
		if err := (Policy{Allow: rules}).Validate(); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", rules, err)
		}
	}

	invalid := []struct {
		rule string
		want string
	}{
		{"", "empty"},
		{"  ", "empty"},
		{"https://example.com", "not a URL"},
		{"example.com/path", "not a URL"},
		{"example.com?x=1", `"?" is not supported`},
		{"example.com#frag", "not a URL"},
		{"user@example.com", "not a URL"},
		{"example.com:8080", "ports are not allowed"},
		{"*.example.com:443", "ports are not allowed"},
		{"[::1]", "without brackets"},
		{"foo bar.com", "whitespace"},
		{"ex?ample.com", `"?" is not supported`},
		{"ex*ample.com", "wildcards must be a whole label"},
		{"example..com", "consecutive dots"},
		{"a b", "whitespace"},
	}
	for _, tc := range invalid {
		err := (Policy{Deny: []string{tc.rule}}).Validate()
		if err == nil {
			t.Errorf("Validate(deny %q) = nil, want error", tc.rule)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Validate(deny %q) = %q, want it to mention %q", tc.rule, err, tc.want)
		}
	}
}

func TestValidateReportsRuleGroup(t *testing.T) {
	err := (Policy{Allow: []string{"ok.com"}, Deny: []string{"bad:8080"}}).Validate()
	if err == nil || !strings.Contains(err.Error(), `deny rule "bad:8080"`) {
		t.Errorf("Validate = %v, want deny rule mention", err)
	}
}

func TestNewCopiesSlices(t *testing.T) {
	allow := []string{"a.com"}
	deny := []string{"b.com"}
	p := New(allow, deny, Deny)
	allow[0] = "mutated.com"
	deny[0] = "mutated.org"
	if p.Allow[0] != "a.com" || p.Deny[0] != "b.com" {
		t.Errorf("New must copy rule slices, got %v / %v", p.Allow, p.Deny)
	}
}

func TestZeroPolicyIsAllowAll(t *testing.T) {
	var p Policy
	if d, rule := p.Decide("https://anything.example/"); d != Allow || rule != "" {
		t.Errorf("zero policy Decide = (%v, %q), want (Allow, \"\")", d, rule)
	}
}

func TestMatchPatternEdge(t *testing.T) {
	tests := []struct {
		pattern string
		host    string
		want    bool
	}{
		{"*", "localhost", true},
		{"*", "a.b", false},
		{"**", "a.b.c", true},
		{"**", "x", true},
		{"a.*.com", "a.b.com", true},
		{"a.*.com", "a.com", false},
		{"*.*", "a.b", true},
		{"*.*", "a", false},
		{"**.**", "a.b.c", true},
		{"Example.COM", "example.com", true},
		{"*.example.com", "EXAMPLE.com", false}, // apex: star needs a label
		{"a.**.com", "a.com", false},            // ** must consume >= 1 label
		{"a.**.com", "a.b.com", true},
		{"a.**.com", "a.b.c.com", true},
	}
	for _, tc := range tests {
		if got := matchPattern(tc.pattern, tc.host); got != tc.want {
			t.Errorf("matchPattern(%q, %q) = %v, want %v", tc.pattern, tc.host, got, tc.want)
		}
	}
}

func TestHostOf(t *testing.T) {
	tests := []struct {
		raw  string
		want string
		err  bool
	}{
		{"https://example.com/x", "example.com", false},
		{"HTTPS://Example.COM:443/a?b=1", "example.com", false},
		{"http://user:pass@example.com/p", "example.com", false},
		{"http://[2001:db8::1]:80/x", "2001:db8::1", false},
		{"http://[::1]/", "::1", false},
		{"[::1]:8080", "::1", false},
		{"2001:db8::1", "2001:db8::1", false},
		{"example.com", "example.com", false},
		{"example.com:8080", "example.com", false},
		{"example.com/path?q=1#f", "example.com", false},
		{"1.2.3.4:8080", "1.2.3.4", false},
		{"10.1.2.3", "10.1.2.3", false},
		{"example.com.", "example.com", false},
		{"", "", true},
		{"   ", "", true},
		{"example.com:abc", "", true},
		{"http:///nohost", "", true},
		{"https://", "", true},
		{"foo bar", "", true},
		{"[::1", "", true},
		{"http://[zzz]/", "", true},
	}
	for _, tc := range tests {
		got, err := hostOf(tc.raw)
		if tc.err {
			if err == nil {
				t.Errorf("hostOf(%q) = %q, want error", tc.raw, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("hostOf(%q) error: %v", tc.raw, err)
			continue
		}
		if got != tc.want {
			t.Errorf("hostOf(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}
