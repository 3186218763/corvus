package config

import (
	"strings"
	"testing"

	"corvus/internal/netpolicy"
)

func TestNetPolicyDefaultIsAllowAll(t *testing.T) {
	cfg := Default()
	p, err := cfg.NetPolicy()
	if err != nil {
		t.Fatalf("Default().NetPolicy() error: %v", err)
	}
	if p.Default != netpolicy.Allow {
		t.Errorf("default policy default = %v, want allow", p.Default)
	}
	if len(p.Allow) != 0 || len(p.Deny) != 0 {
		t.Errorf("default policy must have no rules, got allow=%v deny=%v", p.Allow, p.Deny)
	}
	if d, rule := p.Decide("https://example.com/"); d != netpolicy.Allow || rule != "" {
		t.Errorf("default policy Decide = (%v, %q), want (allow, \"\")", d, rule)
	}
}

func TestNetPolicyParsesSection(t *testing.T) {
	cfg := Default()
	cfg.NetworkPolicy = NetworkPolicyConfig{
		Allow:   []string{"docs.example.com"},
		Deny:    []string{"*.internal.corp"},
		Default: "deny",
	}
	p, err := cfg.NetPolicy()
	if err != nil {
		t.Fatalf("NetPolicy() error: %v", err)
	}
	if p.Default != netpolicy.Deny {
		t.Errorf("default = %v, want deny", p.Default)
	}
	cases := []struct {
		url  string
		want netpolicy.Decision
	}{
		{"https://x.internal.corp/", netpolicy.Deny},   // deny rule wins
		{"https://docs.example.com/", netpolicy.Allow}, // allow rule beats default deny
		{"https://other.example.com/", netpolicy.Deny}, // default deny
	}
	for _, tc := range cases {
		if d, _ := p.Decide(tc.url); d != tc.want {
			t.Errorf("Decide(%q) = %v, want %v", tc.url, d, tc.want)
		}
	}
}

func TestNetPolicyAskDefault(t *testing.T) {
	cfg := Default()
	cfg.NetworkPolicy.Default = "ask"
	p, err := cfg.NetPolicy()
	if err != nil {
		t.Fatalf("NetPolicy() error: %v", err)
	}
	if p.Default != netpolicy.Ask {
		t.Errorf("default = %v, want ask", p.Default)
	}
	if d, _ := p.Decide("https://anything.example/"); d != netpolicy.Ask {
		t.Errorf("Decide = %v, want ask (caller resolves without approval UI)", d)
	}
}

func TestNetPolicyCaseInsensitiveDefault(t *testing.T) {
	cfg := Default()
	cfg.NetworkPolicy.Default = " DENY "
	p, err := cfg.NetPolicy()
	if err != nil {
		t.Fatalf("NetPolicy() error: %v", err)
	}
	if p.Default != netpolicy.Deny {
		t.Errorf("default = %v, want deny", p.Default)
	}
}

func TestNetPolicyInvalidDefault(t *testing.T) {
	cfg := Default()
	cfg.NetworkPolicy.Default = "maybe"
	if _, err := cfg.NetPolicy(); err == nil || !strings.Contains(err.Error(), `network_policy.default "maybe"`) {
		t.Errorf("invalid default error = %v, want mention of network_policy.default", err)
	}
}

func TestNetPolicyInvalidRule(t *testing.T) {
	cfg := Default()
	cfg.NetworkPolicy.Deny = []string{"example.com:8080"}
	_, err := cfg.NetPolicy()
	if err == nil || !strings.Contains(err.Error(), `deny rule "example.com:8080"`) {
		t.Errorf("invalid rule error = %v, want deny rule mention", err)
	}
}

func TestNetPolicyEmptyRulesAreIgnored(t *testing.T) {
	cfg := Default()
	cfg.NetworkPolicy.Allow = []string{"", "  ", "ok.example.com"}
	p, err := cfg.NetPolicy()
	if err != nil {
		t.Fatalf("NetPolicy() error: %v", err)
	}
	if len(p.Allow) != 1 || p.Allow[0] != "ok.example.com" {
		t.Errorf("allow = %v, want only ok.example.com", p.Allow)
	}
}
