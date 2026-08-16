package netclient

import (
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNormalizeMode(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ModeAuto},
		{"   ", ModeAuto},
		{"auto", ModeAuto},
		{"AUTO", ModeAuto},
		{"Auto", ModeAuto},
		{"env", ModeEnv},
		{"ENV", ModeEnv},
		{"custom", ModeCustom},
		{"Custom", ModeCustom},
		{"off", ModeOff},
		{"OFF", ModeOff},
		{"garbage", ModeAuto},
		{"https", ModeAuto},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := NormalizeMode(tt.in); got != tt.want {
				t.Fatalf("NormalizeMode(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	valid := []ProxySpec{
		{Mode: ModeOff},
		{Mode: ModeEnv},
		{Mode: ModeAuto},
		{Mode: ModeCustom, URL: "http://proxy.example.com:8080"},
		{Mode: ModeCustom, URL: "socks5h://user:pass@proxy.example.com:1080"},
		{Mode: ModeCustom, Type: "http", Server: "proxy.example.com", Port: 8080},
		{Mode: ModeCustom, Type: "https", Server: "127.0.0.1", Port: 443},
		{Mode: ModeCustom, Type: "socks5", Server: "p", Port: 1080, Username: "u"},
	}
	for _, spec := range valid {
		t.Run(spec.Mode+"/"+spec.URL+spec.Type, func(t *testing.T) {
			if err := Validate(spec); err != nil {
				t.Fatalf("Validate(%+v) = %v, want nil", spec, err)
			}
		})
	}

	invalid := []struct {
		spec ProxySpec
		want string
	}{
		{ProxySpec{Mode: ModeCustom, URL: "ftp://proxy.example.com:21"}, "scheme"},
		{ProxySpec{Mode: ModeCustom, URL: "http://"}, "host is required"},
		{ProxySpec{Mode: ModeCustom, URL: "://bad"}, "proxy_url"},
		{ProxySpec{Mode: ModeCustom, Type: "quic", Server: "p", Port: 1}, "must be http|https|socks5|socks5h"},
		{ProxySpec{Mode: ModeCustom, Type: "http"}, "server is required"},
		{ProxySpec{Mode: ModeCustom, Type: "http", Server: "p"}, "port must be"},
		{ProxySpec{Mode: ModeCustom, Type: "http", Server: "p", Port: -1}, "port must be"},
		{ProxySpec{Mode: ModeCustom, Type: "http", Server: "p", Port: 65536}, "port must be"},
		{ProxySpec{Mode: ModeCustom, Type: "http", Server: "  ", Port: 8080}, "server is required"},
	}
	for _, tt := range invalid {
		t.Run(tt.want, func(t *testing.T) {
			err := Validate(tt.spec)
			if err == nil {
				t.Fatalf("Validate(%+v) = nil, want error containing %q", tt.spec, tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate(%+v) error = %q, want it to contain %q", tt.spec, err, tt.want)
			}
		})
	}
}

func TestCustomProxyURLComposition(t *testing.T) {
	tests := []struct {
		name       string
		spec       ProxySpec
		wantScheme string
		wantUser   string
		wantPass   bool
		wantPassV  string
	}{
		{
			name:       "structured https with credentials",
			spec:       ProxySpec{Mode: ModeCustom, Type: "https", Server: "p.example.com", Port: 8443, Username: "u", Password: "s3cret"},
			wantScheme: "https",
			wantUser:   "u",
			wantPass:   true,
			wantPassV:  "s3cret",
		},
		{
			name:       "structured username only",
			spec:       ProxySpec{Mode: ModeCustom, Type: "socks5h", Server: "p.example.com", Port: 1080, Username: "u"},
			wantScheme: "socks5h",
			wantUser:   "u",
			wantPass:   false,
		},
		{
			name:       "url with credentials",
			spec:       ProxySpec{Mode: ModeCustom, URL: "http://alice:wonder@proxy.example.com:3128"},
			wantScheme: "http",
			wantUser:   "alice",
			wantPass:   true,
			wantPassV:  "wonder",
		},
		{
			name:       "url without credentials",
			spec:       ProxySpec{Mode: ModeCustom, URL: "socks5://proxy.example.com:1080"},
			wantScheme: "socks5",
			wantUser:   "",
			wantPass:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := customProxyURL(tt.spec)
			if err != nil {
				t.Fatalf("customProxyURL: %v", err)
			}
			if u.Scheme != tt.wantScheme {
				t.Errorf("scheme = %q, want %q", u.Scheme, tt.wantScheme)
			}
			if tt.wantUser == "" {
				if u.User != nil {
					t.Errorf("User = %v, want nil", u.User)
				}
				return
			}
			if u.User == nil || u.User.Username() != tt.wantUser {
				t.Errorf("username = %v, want %q", u.User, tt.wantUser)
			}
			gotPass, hasPass := u.User.Password()
			if hasPass != tt.wantPass || (tt.wantPass && gotPass != tt.wantPassV) {
				t.Errorf("password = (%q, %v), want (%q, %v)", gotPass, hasPass, tt.wantPassV, tt.wantPass)
			}
		})
	}
}

func TestSummaryVariants(t *testing.T) {
	tests := []struct {
		name string
		spec ProxySpec
		want string
	}{
		{"off", ProxySpec{Mode: ModeOff}, "off (direct)"},
		{"off whitespace", ProxySpec{Mode: " off "}, "off (direct)"},
		{"env", ProxySpec{Mode: ModeEnv}, "env"},
		{"auto", ProxySpec{Mode: ModeAuto}, "auto (env)"},
		{"custom url redacted", ProxySpec{Mode: ModeCustom, URL: "socks5://user:secret@proxy.example.com:1080"}, "custom (socks5://user@proxy.example.com:1080)"},
		{"custom structured redacted", ProxySpec{Mode: ModeCustom, Type: "http", Server: "p.example.com", Port: 8080, Username: "alice", Password: "hunter2"}, "custom (http://alice@p.example.com:8080)"},
		{"custom invalid", ProxySpec{Mode: ModeCustom, Type: "http"}, "custom (invalid)"},
		{"custom invalid scheme", ProxySpec{Mode: ModeCustom, URL: "ftp://p:1"}, "custom (invalid)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Summary(tt.spec); got != tt.want {
				t.Fatalf("Summary(%+v) = %q, want %q", tt.spec, got, tt.want)
			}
		})
	}
	// Passwords must never leak into the summary.
	if got := Summary(ProxySpec{Mode: ModeCustom, URL: "http://u:pw@h:1"}); strings.Contains(got, "pw") {
		t.Fatalf("summary leaked password: %q", got)
	}
}

func TestNewHTTPClientErrorPropagation(t *testing.T) {
	if c, err := NewHTTPClient(ProxySpec{Mode: ModeCustom, Type: "http"}, TransportOptions{}); err == nil || c != nil {
		t.Fatalf("NewHTTPClient(invalid) = (%v, %v), want error", c, err)
	}
	c, err := NewHTTPClient(ProxySpec{Mode: ModeOff}, TransportOptions{})
	if err != nil || c == nil || c.Transport == nil {
		t.Fatalf("NewHTTPClient(off) = (%v, %v), want client", c, err)
	}
}

func TestNewTransportKnobs(t *testing.T) {
	tr, err := NewTransport(ProxySpec{Mode: ModeOff}, TransportOptions{
		DialTimeout:           7 * time.Second,
		KeepAlive:             9 * time.Second,
		TLSHandshakeTimeout:   11 * time.Second,
		ResponseHeaderTimeout: 13 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if tr.DialContext == nil {
		t.Error("DialContext not installed when dial knobs are set")
	}
	if tr.TLSHandshakeTimeout != 11*time.Second {
		t.Errorf("TLSHandshakeTimeout = %v, want 11s", tr.TLSHandshakeTimeout)
	}
	if tr.ResponseHeaderTimeout != 13*time.Second {
		t.Errorf("ResponseHeaderTimeout = %v, want 13s", tr.ResponseHeaderTimeout)
	}

	// Zero knobs keep the cloned default transport's own values untouched.
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Fatal("http.DefaultTransport is not *http.Transport")
	}
	tr2, err := NewTransport(ProxySpec{Mode: ModeOff}, TransportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if tr2.TLSHandshakeTimeout != base.TLSHandshakeTimeout {
		t.Errorf("zero knobs changed TLSHandshakeTimeout to %v, want default %v",
			tr2.TLSHandshakeTimeout, base.TLSHandshakeTimeout)
	}
	if tr2.ResponseHeaderTimeout != 0 {
		t.Errorf("zero knobs set ResponseHeaderTimeout to %v, want 0", tr2.ResponseHeaderTimeout)
	}
}

func TestEnvProxyResolution(t *testing.T) {
	t.Setenv("http_proxy", "")
	t.Setenv("HTTP_PROXY", "http://http-proxy.test:8080")
	t.Setenv("https_proxy", "")
	t.Setenv("HTTPS_PROXY", "http://https-proxy.test:8443")
	t.Setenv("no_proxy", "")
	t.Setenv("NO_PROXY", "bypass.test, sub.test")

	pf, err := proxyFunc(ProxySpec{Mode: ModeEnv})
	if err != nil {
		t.Fatal(err)
	}
	httpReq := &http.Request{URL: mustURL("http://service.test/x")}
	got, err := pf(httpReq)
	if err != nil || got == nil || got.Host != "http-proxy.test:8080" {
		t.Fatalf("env http proxy = (%v, %v), want http-proxy.test:8080", got, err)
	}
	httpsReq := &http.Request{URL: mustURL("https://service.test/x")}
	got, err = pf(httpsReq)
	if err != nil || got == nil || got.Host != "https-proxy.test:8443" {
		t.Fatalf("env https proxy = (%v, %v), want https-proxy.test:8443", got, err)
	}
	for _, host := range []string{"bypass.test", "api.sub.test", "SUB.TEST"} {
		req := &http.Request{URL: mustURL("https://" + host + "/x")}
		got, err := pf(req)
		if err != nil || got != nil {
			t.Fatalf("env NO_PROXY %q = (%v, %v), want direct", host, got, err)
		}
	}
}

func TestAutoModeFallsBackToDirectWithoutEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("auto mode consults the OS system proxy on Windows")
	}
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("http_proxy", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("https_proxy", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	pf, err := proxyFunc(ProxySpec{Mode: ModeAuto})
	if err != nil {
		t.Fatal(err)
	}
	got, err := pf(&http.Request{URL: mustURL("https://service.test/x")})
	if err != nil {
		t.Fatalf("auto fallback lookup: %v", err)
	}
	if got != nil {
		t.Fatalf("auto with no env proxy = %v, want nil (direct)", got)
	}
}

// TestEnvProxyCGIErrorPropagation proves the one error path httpproxy exposes:
// in a CGI environment HTTP_PROXY is refused outright.
func TestEnvProxyCGIErrorPropagation(t *testing.T) {
	t.Setenv("REQUEST_METHOD", "POST")
	t.Setenv("HTTP_PROXY", "http://proxy.test:8080")
	t.Setenv("http_proxy", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("https_proxy", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	pf, err := proxyFunc(ProxySpec{Mode: ModeEnv})
	if err != nil {
		t.Fatal(err)
	}
	_, err = pf(&http.Request{URL: mustURL("http://service.test/x")})
	if err == nil {
		t.Fatal("CGI + HTTP_PROXY should surface an error")
	}
	if !strings.Contains(err.Error(), "CGI") {
		t.Fatalf("error = %q, want CGI refusal", err)
	}
}

// TestEnvProxyInvalidURLFallsBackToDirect documents httpproxy's semantics: a
// malformed proxy URL is silently treated as unset (direct), never surfacing
// as an error from the resolver.
func TestEnvProxyInvalidURLFallsBackToDirect(t *testing.T) {
	t.Setenv("REQUEST_METHOD", "")
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("http_proxy", "")
	t.Setenv("HTTPS_PROXY", "http://exa%mple.com")
	t.Setenv("https_proxy", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	pf, err := proxyFunc(ProxySpec{Mode: ModeEnv})
	if err != nil {
		t.Fatal(err)
	}
	got, err := pf(&http.Request{URL: mustURL("https://service.test/x")})
	if err != nil || got != nil {
		t.Fatalf("invalid env proxy URL = (%v, %v), want (nil, nil) direct", got, err)
	}
}

func TestWithDirectHostsBehavior(t *testing.T) {
	calls := 0
	proxyURL := mustURL("http://proxy.test:8080")
	base := func(req *http.Request) (*url.URL, error) {
		calls++
		return proxyURL, nil
	}

	// Nil or empty host lists pass the base function through unchanged.
	if got := withDirectHosts(nil, []string{"a.test"}); got != nil {
		t.Fatal("withDirectHosts(nil pf) should stay nil")
	}
	if got := withDirectHosts(base, nil); got == nil {
		t.Fatal("withDirectHosts(nil hosts) should keep the proxy func")
	}

	pf := withDirectHosts(base, []string{"  Direct.Example.COM ", "api.test", "", " "})
	req := func(host string) *http.Request {
		return &http.Request{URL: mustURL("https://" + host + "/x")}
	}
	for _, host := range []string{"direct.example.com", "Direct.Example.COM", "sub.direct.example.com", "api.test", "x.api.test"} {
		got, err := pf(req(host))
		if err != nil || got != nil {
			t.Fatalf("direct host %q = (%v, %v), want nil", host, got, err)
		}
		if calls != 0 {
			t.Fatalf("base proxy func called for direct host %q", host)
		}
	}
	for _, host := range []string{"other.test", "test.api.testx", "example.com"} {
		got, err := pf(req(host))
		if err != nil || got == nil || got.Host != "proxy.test:8080" {
			t.Fatalf("proxied host %q = (%v, %v), want proxy", host, got, err)
		}
	}
	// A host with a port bypasses on the hostname alone.
	got, err := pf(req("direct.example.com:443"))
	if err != nil || got != nil {
		t.Fatalf("direct host with port = (%v, %v), want nil", got, err)
	}
}

func TestRedactURL(t *testing.T) {
	withCreds := mustURL("socks5://user:secret@proxy.test:1080")
	if got := redactURL(withCreds); got != "socks5://user@proxy.test:1080" {
		t.Errorf("redactURL with creds = %q", got)
	}
	userOnly := mustURL("http://alice@proxy.test:8080")
	if got := redactURL(userOnly); got != "http://alice@proxy.test:8080" {
		t.Errorf("redactURL username-only = %q", got)
	}
	noUser := mustURL("http://proxy.test:8080")
	if got := redactURL(noUser); got != "http://proxy.test:8080" {
		t.Errorf("redactURL without user = %q", got)
	}
}
