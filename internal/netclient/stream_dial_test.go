package netclient

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestNewStreamDialerOffDialsDirect proves the off/direct path: no proxy is
// resolved and the dial reaches the local listener untouched.
func TestNewStreamDialerOffDialsDirect(t *testing.T) {
	ln := newEchoListener(t)
	d, err := NewStreamDialer(ProxySpec{Mode: ModeOff})
	if err != nil {
		t.Fatalf("NewStreamDialer: %v", err)
	}
	conn, err := d.DialContext(context.Background(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer conn.Close()
	assertEcho(t, conn)
}

// TestNewStreamDialerNoProxyBypassesCustomProxy proves NoProxy is honored by
// the stream dial path: the proxy must not be contacted.
func TestNewStreamDialerNoProxyBypassesCustomProxy(t *testing.T) {
	ln := newEchoListener(t)
	_, host, _ := net.SplitHostPort(ln.Addr().String())
	var proxyHits int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&proxyHits, 1)
	}))
	t.Cleanup(proxy.Close)

	d, err := NewStreamDialer(ProxySpec{
		Mode:    ModeCustom,
		URL:     proxy.URL,
		NoProxy: host,
	})
	if err != nil {
		t.Fatalf("NewStreamDialer: %v", err)
	}
	conn, err := d.DialContext(context.Background(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer conn.Close()
	assertEcho(t, conn)
	if got := atomic.LoadInt32(&proxyHits); got != 0 {
		t.Fatalf("proxy hits = %d, want 0 (NoProxy must bypass)", got)
	}
}

// TestNewStreamDialerHTTPConnectTunnel proves an http CONNECT proxy carries a
// raw stream: the proxy sees the CONNECT target and the bytes round-trip.
func TestNewStreamDialerHTTPConnectTunnel(t *testing.T) {
	var hits int32
	proxy := newConnectEchoProxy(t, "", "", false, &hits)
	t.Cleanup(proxy.Close)

	d, err := NewStreamDialer(ProxySpec{Mode: ModeCustom, URL: proxy.URL})
	if err != nil {
		t.Fatalf("NewStreamDialer: %v", err)
	}
	conn, err := d.DialContext(context.Background(), "tcp", "service.test:443")
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer conn.Close()
	assertEcho(t, conn)
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("CONNECT proxy hits = %d, want 1", got)
	}
}

// TestNewStreamDialerHTTPConnectSendsProxyAuth proves credentials reach the
// proxy as Proxy-Authorization on the CONNECT request.
func TestNewStreamDialerHTTPConnectSendsProxyAuth(t *testing.T) {
	var hits int32
	proxy := newConnectEchoProxy(t, "proxyuser", "proxypass", true, &hits)
	t.Cleanup(proxy.Close)

	u := strings.TrimPrefix(proxy.URL, "http://")
	d, err := NewStreamDialer(ProxySpec{Mode: ModeCustom, URL: "http://proxyuser:proxypass@" + u})
	if err != nil {
		t.Fatalf("NewStreamDialer: %v", err)
	}
	conn, err := d.DialContext(context.Background(), "tcp", "service.test:443")
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer conn.Close()
	assertEcho(t, conn)
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("CONNECT proxy hits = %d, want 1", got)
	}
}

// TestNewStreamDialerHTTPConnectErrorPropagates proves a non-200 CONNECT
// response surfaces with the proxy status in the error.
func TestNewStreamDialerHTTPConnectErrorPropagates(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden by policy", http.StatusForbidden)
	}))
	t.Cleanup(proxy.Close)

	d, err := NewStreamDialer(ProxySpec{Mode: ModeCustom, URL: proxy.URL})
	if err != nil {
		t.Fatalf("NewStreamDialer: %v", err)
	}
	conn, err := d.DialContext(context.Background(), "tcp", "service.test:443")
	if err == nil {
		conn.Close()
		t.Fatal("DialContext = nil error, want CONNECT failure")
	}
	if !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "CONNECT") {
		t.Fatalf("error = %q, want CONNECT failure with proxy status", err)
	}
}

// TestNewStreamDialerSOCKS5 proves the SOCKS5 path: the proxy receives the
// target address and the stream round-trips.
func TestNewStreamDialerSOCKS5(t *testing.T) {
	srv := newSocks5EchoServer(t, false)
	d, err := NewStreamDialer(ProxySpec{
		Mode: ModeCustom, Type: "socks5", Server: "127.0.0.1", Port: srv.port,
	})
	if err != nil {
		t.Fatalf("NewStreamDialer: %v", err)
	}
	conn, err := d.DialContext(context.Background(), "tcp", "service.test:443")
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer conn.Close()
	assertEcho(t, conn)
	select {
	case got := <-srv.targets:
		if got != "service.test:443" {
			t.Fatalf("SOCKS target = %q, want service.test:443", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SOCKS proxy never saw a connect request")
	}
}

// TestNewStreamDialerSOCKS5Auth proves username/password credentials complete
// the RFC 1929 exchange.
func TestNewStreamDialerSOCKS5Auth(t *testing.T) {
	srv := newSocks5EchoServer(t, true)
	d, err := NewStreamDialer(ProxySpec{
		Mode: ModeCustom, Type: "socks5", Server: "127.0.0.1", Port: srv.port,
		Username: "user", Password: "secret",
	})
	if err != nil {
		t.Fatalf("NewStreamDialer: %v", err)
	}
	conn, err := d.DialContext(context.Background(), "tcp", "service.test:443")
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer conn.Close()
	assertEcho(t, conn)
}

// TestNewStreamDialerSOCKS5AuthFailure proves a proxy rejecting the credentials
// propagates the auth error.
func TestNewStreamDialerSOCKS5AuthFailure(t *testing.T) {
	srv := newSocks5EchoServer(t, true)
	d, err := NewStreamDialer(ProxySpec{
		Mode: ModeCustom, Type: "socks5h", Server: "127.0.0.1", Port: srv.port,
		Username: "user", Password: "wrong",
	})
	if err != nil {
		t.Fatalf("NewStreamDialer: %v", err)
	}
	conn, err := d.DialContext(context.Background(), "tcp", "service.test:443")
	if err == nil {
		conn.Close()
		t.Fatal("DialContext = nil error, want auth failure")
	}
	if !strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("error = %q, want authentication failure", err)
	}
}

// TestNewStreamDialerSOCKS5ConnectError proves a SOCKS reply failure (other
// than success) propagates to the caller.
func TestNewStreamDialerSOCKS5ConnectError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(2 * time.Second))
				r := bufio.NewReader(c)
				header := make([]byte, 2)
				if _, err := io.ReadFull(r, header); err != nil || header[0] != 5 {
					return
				}
				methods := make([]byte, int(header[1]))
				if _, err := io.ReadFull(r, methods); err != nil {
					return
				}
				if _, err := c.Write([]byte{5, 0}); err != nil {
					return
				}
				req := make([]byte, 4)
				if _, err := io.ReadFull(r, req); err != nil {
					return
				}
				var atyp = req[3]
				switch atyp {
				case 1:
					_, _ = io.ReadFull(r, make([]byte, net.IPv4len))
				case 3:
					size, err := r.ReadByte()
					if err != nil {
						return
					}
					_, _ = io.ReadFull(r, make([]byte, int(size)))
				case 4:
					_, _ = io.ReadFull(r, make([]byte, net.IPv6len))
				default:
					return
				}
				_, _ = io.ReadFull(r, make([]byte, 2)) // port
				// Reply 0x05: connection refused.
				_, _ = c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
			}(conn)
		}
	}()

	_, portText, _ := net.SplitHostPort(ln.Addr().String())
	port := atoi(t, portText)
	d, err := NewStreamDialer(ProxySpec{
		Mode: ModeCustom, Type: "socks5", Server: "127.0.0.1", Port: port,
	})
	if err != nil {
		t.Fatalf("NewStreamDialer: %v", err)
	}
	conn, err := d.DialContext(context.Background(), "tcp", "service.test:443")
	if err == nil {
		conn.Close()
		t.Fatal("DialContext = nil error, want SOCKS connect failure")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("error = %q, want connection refused", err)
	}
}

// TestNewStreamDialerHTTPSProxyTunnel proves the CONNECT handshake for an
// https-scheme proxy happens inside TLS (so Proxy-Authorization is not sent in
// cleartext), with the proxy's certificate verified against the configured CA.
func TestNewStreamDialerHTTPSProxyTunnel(t *testing.T) {
	proxy := newTLSConnectEchoProxy(t)
	t.Cleanup(proxy.Close)
	caPEM, _ := testTLSAssets()
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", caFile)

	var hits int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT required", http.StatusMethodNotAllowed)
			return
		}
		atomic.AddInt32(&hits, 1)
		if got, want := r.Host, "service.test:443"; got != want {
			t.Errorf("CONNECT host = %q, want %q", got, want)
		}
		hijackAndEcho(t, w)
	})
	proxy.Config.Handler = handler
	proxy.StartTLS()

	d, err := NewStreamDialer(ProxySpec{
		Mode: ModeCustom, URL: "https://" + proxy.Listener.Addr().String(),
	})
	if err != nil {
		t.Fatalf("NewStreamDialer: %v", err)
	}
	conn, err := d.DialContext(context.Background(), "tcp", "service.test:443")
	if err != nil {
		t.Fatalf("DialContext through TLS proxy: %v", err)
	}
	defer conn.Close()
	assertEcho(t, conn)
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("TLS CONNECT proxy hits = %d, want 1", got)
	}
}

// TestNewStreamDialerUnsupportedScheme proves a proxy with a scheme the stream
// layer cannot tunnel (reachable via environment configuration) fails closed.
func TestNewStreamDialerUnsupportedScheme(t *testing.T) {
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("http_proxy", "")
	t.Setenv("HTTPS_PROXY", "ftp://proxy.example.com:21")
	t.Setenv("https_proxy", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	d, err := NewStreamDialer(ProxySpec{Mode: ModeEnv})
	if err != nil {
		t.Fatalf("NewStreamDialer: %v", err)
	}
	conn, err := d.DialContext(context.Background(), "tcp", "service.test:443")
	if err == nil {
		conn.Close()
		t.Fatal("DialContext = nil error, want unsupported scheme")
	}
	if !strings.Contains(err.Error(), `unsupported proxy scheme "ftp"`) {
		t.Fatalf("error = %q, want unsupported proxy scheme", err)
	}
}

// TestNewStreamDialerEnvModeUsesEnvProxy proves env mode routes raw streams
// through the environment proxy.
func TestNewStreamDialerEnvModeUsesEnvProxy(t *testing.T) {
	var hits int32
	proxy := newConnectEchoProxy(t, "", "", false, &hits)
	t.Cleanup(proxy.Close)
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("http_proxy", "")
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("https_proxy", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")

	d, err := NewStreamDialer(ProxySpec{Mode: ModeEnv})
	if err != nil {
		t.Fatalf("NewStreamDialer: %v", err)
	}
	conn, err := d.DialContext(context.Background(), "tcp", "service.test:443")
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer conn.Close()
	assertEcho(t, conn)
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("env CONNECT proxy hits = %d, want 1", got)
	}
}

// TestNewStreamDialerInvalidSpec proves configuration errors surface before
// any dial is attempted.
func TestNewStreamDialerInvalidSpec(t *testing.T) {
	if d, err := NewStreamDialer(ProxySpec{Mode: ModeCustom, Type: "http"}); err == nil || d != nil {
		t.Fatalf("NewStreamDialer(missing server) = (%v, %v), want error", d, err)
	}
	if d, err := NewStreamDialer(ProxySpec{Mode: ModeCustom, URL: "ftp://p:1"}); err == nil || d != nil {
		t.Fatalf("NewStreamDialer(bad scheme) = (%v, %v), want error", d, err)
	}
}

func TestDialerFuncAdapter(t *testing.T) {
	sentinel := errors.New("dial sentinel")
	f := DialerFunc(func(ctx context.Context, network, addr string) (net.Conn, error) {
		if network != "tcp" || addr != "x:1" {
			t.Errorf("DialContext(%q, %q)", network, addr)
		}
		return nil, sentinel
	})
	conn, err := f.DialContext(context.Background(), "tcp", "x:1")
	if conn != nil || !errors.Is(err, sentinel) {
		t.Fatalf("DialerFunc = (%v, %v), want (nil, sentinel)", conn, err)
	}
}

// TestBufferedConnRead proves bytes a proxy sent alongside the CONNECT 200 are
// not lost when the reader already buffered them.
func TestBufferedConnRead(t *testing.T) {
	bc := &bufferedConn{r: bufio.NewReader(strings.NewReader("buffered"))}
	buf := make([]byte, 8)
	n, err := bc.Read(buf)
	if err != nil || string(buf[:n]) != "buffered" {
		t.Fatalf("bufferedConn.Read = (%d, %v), want (8, nil)", n, err)
	}
}

// --- helpers ---

func assertEcho(t *testing.T, conn net.Conn) {
	t.Helper()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("echo = %q, want ping", buf)
	}
}

func newEchoListener(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()
	return ln
}

func newConnectEchoProxy(t *testing.T, wantUser, wantPass string, requireAuth bool, hits *int32) *httptest.Server {
	t.Helper()
	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte(wantUser+":"+wantPass))
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT required", http.StatusMethodNotAllowed)
			return
		}
		if requireAuth && r.Header.Get("Proxy-Authorization") != expected {
			http.Error(w, "proxy auth required", http.StatusProxyAuthRequired)
			return
		}
		atomic.AddInt32(hits, 1)
		hijackAndEcho(t, w)
	}))
}

func hijackAndEcho(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	clientConn, _, err := http.NewResponseController(w).Hijack()
	if err != nil {
		t.Errorf("hijack CONNECT: %v", err)
		return
	}
	defer clientConn.Close()
	if _, err := clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}
	_, _ = io.Copy(clientConn, clientConn)
}

type socks5EchoServer struct {
	port        int
	requireAuth bool
	targets     chan string
}

func newSocks5EchoServer(t *testing.T, requireAuth bool) *socks5EchoServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	s := &socks5EchoServer{
		port:        ln.Addr().(*net.TCPAddr).Port,
		requireAuth: requireAuth,
		targets:     make(chan string, 4),
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handle(conn)
		}
	}()
	return s
}

func (s *socks5EchoServer) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	r := bufio.NewReader(conn)

	header := make([]byte, 2)
	if _, err := io.ReadFull(r, header); err != nil || header[0] != 5 {
		return
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(r, methods); err != nil {
		return
	}
	if s.requireAuth {
		hasAuth := false
		for _, m := range methods {
			if m == 2 {
				hasAuth = true
			}
		}
		if !hasAuth {
			return
		}
		if _, err := conn.Write([]byte{5, 2}); err != nil {
			return
		}
		auth := make([]byte, 2)
		if _, err := io.ReadFull(r, auth); err != nil || auth[0] != 1 {
			return
		}
		user := make([]byte, int(auth[1]))
		if _, err := io.ReadFull(r, user); err != nil {
			return
		}
		// RFC 1929: VER ULEN UNAME PLEN PASSWD — no second version byte.
		plen, err := r.ReadByte()
		if err != nil {
			return
		}
		pass := make([]byte, int(plen))
		if _, err := io.ReadFull(r, pass); err != nil {
			return
		}
		if string(user) != "user" || string(pass) != "secret" {
			_, _ = conn.Write([]byte{1, 1})
			return
		}
		if _, err := conn.Write([]byte{1, 0}); err != nil {
			return
		}
	} else {
		if _, err := conn.Write([]byte{5, 0}); err != nil {
			return
		}
	}

	req := make([]byte, 4)
	if _, err := io.ReadFull(r, req); err != nil || req[0] != 5 || req[1] != 1 {
		return
	}
	var host string
	switch req[3] {
	case 1:
		ip := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(r, ip); err != nil {
			return
		}
		host = net.IP(ip).String()
	case 3:
		size, err := r.ReadByte()
		if err != nil {
			return
		}
		name := make([]byte, int(size))
		if _, err := io.ReadFull(r, name); err != nil {
			return
		}
		host = string(name)
	case 4:
		ip := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(r, ip); err != nil {
			return
		}
		host = net.IP(ip).String()
	default:
		return
	}
	port := make([]byte, 2)
	if _, err := io.ReadFull(r, port); err != nil {
		return
	}
	select {
	case s.targets <- net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port)))):
	default:
	}

	if _, err := conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	_, _ = io.Copy(conn, conn)
}

// tlsAssetsOnce generates the test CA and proxy leaf certificate once per test
// process. crypto/x509 caches the system root pool on first use, so the CA
// must stay stable across test iterations (e.g. `go test -count=N`) or the
// SSL_CERT_FILE override silently stops matching.
var (
	tlsAssetsOnce sync.Once
	tlsAssetsCA   []byte
	tlsAssetsCert tls.Certificate
)

func testTLSAssets() (caPEM []byte, cert tls.Certificate) {
	tlsAssetsOnce.Do(func() {
		caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			panic(err)
		}
		caTmpl := &x509.Certificate{
			SerialNumber:          big.NewInt(1),
			Subject:               pkix.Name{CommonName: "netclient test CA"},
			NotBefore:             time.Now().Add(-time.Hour),
			NotAfter:              time.Now().Add(24 * time.Hour),
			IsCA:                  true,
			BasicConstraintsValid: true,
			KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		}
		caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
		if err != nil {
			panic(err)
		}
		ca, err := x509.ParseCertificate(caDER)
		if err != nil {
			panic(err)
		}

		leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			panic(err)
		}
		leafTmpl := &x509.Certificate{
			SerialNumber: big.NewInt(2),
			Subject:      pkix.Name{CommonName: "127.0.0.1"},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(24 * time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		}
		leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, ca, &leafKey.PublicKey, caKey)
		if err != nil {
			panic(err)
		}
		leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
		keyDER, err := x509.MarshalECPrivateKey(leafKey)
		if err != nil {
			panic(err)
		}
		keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
		cert, err := tls.X509KeyPair(leafPEM, keyPEM)
		if err != nil {
			panic(err)
		}
		tlsAssetsCA = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
		tlsAssetsCert = cert
	})
	return tlsAssetsCA, tlsAssetsCert
}

// newTLSConnectEchoProxy returns an unstarted TLS server whose certificate is
// issued by a throwaway CA for 127.0.0.1. The caller sets the handler, calls
// StartTLS, and must trust the CA (see TestNewStreamDialerHTTPSProxyTunnel).
func newTLSConnectEchoProxy(t *testing.T) *httptest.Server {
	t.Helper()
	_, cert := testTLSAssets()
	srv := httptest.NewUnstartedServer(nil)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	return srv
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	var n int
	for _, r := range s {
		if r < '0' || r > '9' {
			t.Fatalf("not a number: %q", s)
		}
		n = n*10 + int(r-'0')
	}
	return n
}
