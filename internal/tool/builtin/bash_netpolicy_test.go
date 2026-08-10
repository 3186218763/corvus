package builtin

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"corvus/internal/netpolicy"
)

func TestURLCandidates(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    []string
	}{
		{name: "curl simple", command: `curl https://example.com/a`, want: []string{"https://example.com/a"}},
		{name: "curl flags", command: `curl -sL https://example.com/a`, want: []string{"https://example.com/a"}},
		{name: "curl output file skipped", command: `curl -o out.html https://example.com/a`, want: []string{"https://example.com/a"}},
		{name: "curl long output skipped", command: `curl --output out.html https://example.com/a`, want: []string{"https://example.com/a"}},
		{name: "curl multiple urls", command: `curl https://a.com https://b.com`, want: []string{"https://a.com", "https://b.com"}},
		{name: "curl bare host", command: `curl example.com:8080/x`, want: []string{"example.com:8080/x"}},
		{name: "curl no urls", command: `curl --version`, want: nil},
		{name: "wget", command: `wget -qO- http://b.org/file`, want: []string{"http://b.org/file"}},
		{name: "wget output doc skipped", command: `wget -O out.html http://b.org`, want: []string{"http://b.org"}},
		{name: "curl.exe", command: `curl.exe -s https://c.net`, want: []string{"https://c.net"}},
		{name: "invoke-webrequest uri", command: `Invoke-WebRequest -Uri https://c.net -Method GET`, want: []string{"https://c.net"}},
		{name: "irm positional", command: `irm https://d.io`, want: []string{"https://d.io"}},
		{name: "invoke-restmethod uri case", command: `Invoke-RestMethod -URI "https://e.org"`, want: []string{"https://e.org"}},
		{name: "echo not scanned", command: `echo https://e.com`, want: nil},
		{name: "git not scanned", command: `git clone https://github.com/x/y`, want: nil},
		{name: "pipe rejected", command: `curl -s https://example.com | grep foo`, want: nil},
		{name: "variable rejected", command: `curl $URL`, want: nil},
		{name: "redirect rejected", command: `curl https://x > out`, want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := urlCandidates(tc.command)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("urlCandidates(%q) = %v, want %v", tc.command, got, tc.want)
			}
		})
	}
}

func TestBashDenyNetworkEgressURLs(t *testing.T) {
	policy := netpolicy.New(nil, []string{"*.example.com", "internal.corp"}, netpolicy.Allow)
	cases := []struct {
		name    string
		command string
		blocked bool
	}{
		{name: "curl denied", command: `curl -s https://a.example.com/x`, blocked: true},
		{name: "curl apex allowed", command: `curl -s https://example.com/x`, blocked: false},
		{name: "curl allowed host", command: `curl -s https://ok.org/x`, blocked: false},
		{name: "wget denied", command: `wget http://b.example.com`, blocked: true},
		{name: "powershell denied", command: `Invoke-WebRequest -Uri https://c.example.com`, blocked: true},
		{name: "irm positional denied", command: `irm https://d.example.com`, blocked: true},
		{name: "echo never blocked", command: `echo https://a.example.com`, blocked: false},
		{name: "text never blocked", command: `printf '%s\n' "hello a.example.com world"`, blocked: false},
		{name: "git not scanned", command: `git clone https://a.example.com/repo`, blocked: false},
		{name: "curl version not blocked", command: `curl --version`, blocked: false},
		{name: "output file arg not blocked", command: `curl -o a.example.com.html https://ok.org/x`, blocked: false},
		{name: "pipe skipped not blocked", command: `curl -s https://a.example.com | head`, blocked: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := bash{netPolicy: policy}
			err := b.denyNetworkEgressURLs(tc.command)
			if tc.blocked {
				if err == nil {
					t.Fatalf("denyNetworkEgressURLs(%q) = nil, want policy error", tc.command)
				}
				if !strings.Contains(err.Error(), "network policy denied") || !strings.Contains(err.Error(), "matched deny rule") {
					t.Errorf("error = %q, want network policy denied with matched rule", err)
				}
				return
			}
			if err != nil {
				t.Errorf("denyNetworkEgressURLs(%q) = %v, want nil", tc.command, err)
			}
		})
	}
}

func TestBashZeroPolicySkipsScanning(t *testing.T) {
	var b bash // zero netPolicy: no-op even for clearly denied URLs
	if err := b.denyNetworkEgressURLs(`curl https://a.example.com`); err != nil {
		t.Errorf("zero policy must not block, got %v", err)
	}
}

func TestBashExecuteDeniedByPolicy(t *testing.T) {
	policy := netpolicy.New(nil, []string{"*.example.com"}, netpolicy.Allow)
	b := bash{netPolicy: policy}
	out, err := b.Execute(context.Background(), argsJSON(t, map[string]any{"command": `curl https://a.example.com/x`}))
	if err == nil {
		t.Fatalf("Execute = %q, want policy error", out)
	}
	if !strings.Contains(err.Error(), `matched deny rule "*.example.com"`) {
		t.Errorf("error = %q, want matched deny rule", err)
	}
}

func TestBashExecuteOrdinaryCommandNotBlocked(t *testing.T) {
	policy := netpolicy.New(nil, []string{"*.example.com"}, netpolicy.Allow)
	b := bash{netPolicy: policy}
	out, err := b.Execute(context.Background(), argsJSON(t, map[string]any{"command": echoForShell(b.resolved(), "policy-ok")}))
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, "policy-ok") {
		t.Errorf("output = %q, want echoed marker", out)
	}
}

func TestBashExecuteDeniedOnPowerShellUri(t *testing.T) {
	policy := netpolicy.New(nil, []string{"*.example.com"}, netpolicy.Allow)
	b := bash{netPolicy: policy}
	_, err := b.Execute(context.Background(), argsJSON(t, map[string]any{"command": `Invoke-WebRequest -Uri https://c.example.com`}))
	if err == nil || !strings.Contains(err.Error(), "network policy denied") {
		t.Errorf("error = %v, want network policy denial", err)
	}
}

func TestBashBackgroundJobAlsoDenied(t *testing.T) {
	policy := netpolicy.New(nil, []string{"*.example.com"}, netpolicy.Allow)
	b := bash{netPolicy: policy}
	_, err := b.Execute(context.Background(), argsJSON(t, map[string]any{"command": `curl https://a.example.com/x`, "run_in_background": true}))
	if err == nil || !strings.Contains(err.Error(), "network policy denied") {
		t.Errorf("error = %v, want network policy denial before job start", err)
	}
}
