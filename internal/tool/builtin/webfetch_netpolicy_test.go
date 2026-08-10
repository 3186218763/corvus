package builtin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"corvus/internal/netpolicy"
)

func webFetchTestServer(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte("hello from policy test"))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func webFetchExec(t *testing.T, wf webFetch, url string) (string, error) {
	t.Helper()
	args, err := json.Marshal(map[string]string{"url": url})
	if err != nil {
		t.Fatal(err)
	}
	return wf.Execute(context.Background(), args)
}

func TestWebFetchDeniedByPolicy(t *testing.T) {
	srv, hits := webFetchTestServer(t)
	wf := webFetch{policy: netpolicy.New(nil, []string{"127.0.0.1"}, netpolicy.Allow)}
	out, err := webFetchExec(t, wf, srv.URL+"/secret")
	if err == nil {
		t.Fatalf("Execute = %q, want policy error", out)
	}
	if !strings.Contains(err.Error(), "network policy denied") || !strings.Contains(err.Error(), `matched deny rule "127.0.0.1"`) {
		t.Errorf("error = %q, want network policy denied with matched rule", err)
	}
	if *hits != 0 {
		t.Errorf("server hit %d times, want 0 (denied before any request)", *hits)
	}
}

func TestWebFetchAllowedByPolicy(t *testing.T) {
	srv, hits := webFetchTestServer(t)
	wf := webFetch{policy: netpolicy.New([]string{"127.0.0.1"}, nil, netpolicy.Allow)}
	out, err := webFetchExec(t, wf, srv.URL+"/ok")
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, "hello from policy test") {
		t.Errorf("output = %q, want fetched body", out)
	}
	if *hits != 1 {
		t.Errorf("server hit %d times, want 1", *hits)
	}
}

func TestWebFetchDefaultDeny(t *testing.T) {
	srv, hits := webFetchTestServer(t)
	wf := webFetch{policy: netpolicy.New(nil, nil, netpolicy.Deny)}
	_, err := webFetchExec(t, wf, srv.URL+"/")
	if err == nil || !strings.Contains(err.Error(), "no rule matched and default is deny") {
		t.Errorf("error = %v, want default-deny refusal", err)
	}
	if *hits != 0 {
		t.Errorf("server hit %d times, want 0", *hits)
	}
}

func TestWebFetchAskDefaultFallsBackToAllow(t *testing.T) {
	srv, hits := webFetchTestServer(t)
	// An ask default has no approval UI here and resolves to allow.
	wf := webFetch{policy: netpolicy.New(nil, nil, netpolicy.Ask)}
	out, err := webFetchExec(t, wf, srv.URL+"/ask")
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, "hello from policy test") {
		t.Errorf("output = %q, want fetched body", out)
	}
	if *hits != 1 {
		t.Errorf("server hit %d times, want 1", *hits)
	}
}

func TestWebFetchAllowBeatsDefaultDeny(t *testing.T) {
	srv, hits := webFetchTestServer(t)
	wf := webFetch{policy: netpolicy.New([]string{"127.0.0.1"}, nil, netpolicy.Deny)}
	out, err := webFetchExec(t, wf, srv.URL+"/")
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, "hello from policy test") {
		t.Errorf("output = %q, want fetched body", out)
	}
	if *hits != 1 {
		t.Errorf("server hit %d times, want 1", *hits)
	}
}

func TestWebFetchZeroPolicyUnconfined(t *testing.T) {
	srv, hits := webFetchTestServer(t)
	wf := webFetch{} // zero policy: allow everything (status quo)
	out, err := webFetchExec(t, wf, srv.URL+"/")
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, "hello from policy test") {
		t.Errorf("output = %q, want fetched body", out)
	}
	if *hits != 1 {
		t.Errorf("server hit %d times, want 1", *hits)
	}
}
