package builtin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"corvus/internal/netpolicy"
)

func webSearchExec(t *testing.T, ws webSearch, query string) (string, error) {
	t.Helper()
	args, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		t.Fatal(err)
	}
	return ws.Execute(context.Background(), args)
}

func TestWebSearchSearxng(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		if r.URL.Query().Get("format") != "json" {
			t.Errorf("format = %q, want json", r.URL.Query().Get("format"))
		}
		io.WriteString(w, `{"results":[{"title":"Go Blog","url":"https://go.dev/blog","content":"The official Go blog."},{"title":"Go Docs","url":"https://go.dev/doc","content":"Documentation."}]}`)
	}))
	defer srv.Close()

	ws := webSearch{engine: "searxng", baseURL: srv.URL, maxResults: 5, client: srv.Client()}
	out, err := webSearchExec(t, ws, "golang")
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if gotQuery != "golang" {
		t.Errorf("query sent = %q, want golang", gotQuery)
	}
	for _, want := range []string{"Go Blog", "https://go.dev/blog", "The official Go blog.", "Go Docs"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestWebSearchSearxngAppendsSearchPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		io.WriteString(w, `{"results":[]}`)
	}))
	defer srv.Close()

	ws := webSearch{engine: "searxng", baseURL: srv.URL + "/", maxResults: 5, client: srv.Client()}
	if _, err := webSearchExec(t, ws, "x"); err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if gotPath != "/search" {
		t.Errorf("path = %q, want /search", gotPath)
	}
}

func TestWebSearchBraveSendsKey(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("X-Subscription-Token")
		io.WriteString(w, `{"web":{"results":[{"title":"Rust","url":"https://rust-lang.org","description":"A language."}]}}`)
	}))
	defer srv.Close()

	ws := webSearch{engine: "brave", baseURL: srv.URL, apiKey: "secret-key", maxResults: 5, client: srv.Client()}
	out, err := webSearchExec(t, ws, "rust")
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if gotAuth != "secret-key" {
		t.Errorf("X-Subscription-Token = %q, want secret-key", gotAuth)
	}
	if !strings.Contains(out, "Rust") || !strings.Contains(out, "https://rust-lang.org") {
		t.Errorf("output = %q, want Brave results", out)
	}
}

func TestWebSearchTavilyPostsJSON(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &body)
		io.WriteString(w, `{"results":[{"title":"Corvus","url":"https://example.com/corvus","content":"A terminal agent."}]}`)
	}))
	defer srv.Close()

	ws := webSearch{engine: "tavily", baseURL: srv.URL, apiKey: "tvly-key", maxResults: 5, client: srv.Client()}
	out, err := webSearchExec(t, ws, "corvus")
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if body["api_key"] != "tvly-key" || body["query"] != "corvus" {
		t.Errorf("request body = %v, want api_key + query", body)
	}
	if !strings.Contains(out, "Corvus") || !strings.Contains(out, "A terminal agent.") {
		t.Errorf("output = %q, want Tavily results", out)
	}
}

func TestWebSearchPolicyDeny(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		io.WriteString(w, `{"results":[]}`)
	}))
	defer srv.Close()

	ws := webSearch{engine: "searxng", baseURL: srv.URL, maxResults: 5, client: srv.Client(),
		policy: netpolicy.New(nil, []string{"127.0.0.1"}, netpolicy.Allow)}
	_, err := webSearchExec(t, ws, "x")
	if err == nil || !strings.Contains(err.Error(), "network policy denied") {
		t.Errorf("error = %v, want network policy denied", err)
	}
	if hits != 0 {
		t.Errorf("server hit %d times, want 0", hits)
	}
}

func TestWebSearchBadEngine(t *testing.T) {
	ws := webSearch{engine: "yahoo", baseURL: "https://example.com", maxResults: 5}
	_, err := webSearchExec(t, ws, "x")
	if err == nil || !strings.Contains(err.Error(), "unknown web_search engine") {
		t.Errorf("error = %v, want unknown engine error", err)
	}
}

func TestWebSearchMissingKey(t *testing.T) {
	ws := webSearch{engine: "brave", baseURL: "https://example.com", maxResults: 5}
	_, err := webSearchExec(t, ws, "x")
	if err == nil || !strings.Contains(err.Error(), "api_key") {
		t.Errorf("error = %v, want missing api_key error", err)
	}
}

func TestWebSearchEmptyQuery(t *testing.T) {
	ws := webSearch{engine: "searxng", baseURL: "https://example.com", maxResults: 5}
	_, err := webSearchExec(t, ws, "  ")
	if err == nil {
		t.Fatal("blank query succeeded, want error")
	}
}

func TestWebSearchServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	ws := webSearch{engine: "searxng", baseURL: srv.URL, maxResults: 5, client: srv.Client()}
	_, err := webSearchExec(t, ws, "x")
	if err == nil {
		t.Fatal("Execute succeeded, want server error")
	}
}

func TestWebSearchMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"results":[`)
	}))
	defer srv.Close()

	ws := webSearch{engine: "searxng", baseURL: srv.URL, maxResults: 5, client: srv.Client()}
	_, err := webSearchExec(t, ws, "x")
	if err == nil {
		t.Fatal("Execute succeeded, want malformed JSON error")
	}
}

func TestWebSearchMaxResultsApplied(t *testing.T) {
	var payload []string
	for i := 0; i < 5; i++ {
		payload = append(payload, `{"title":"R`+string(rune('0'+i))+`","url":"https://example.com/`+string(rune('0'+i))+`","content":"c"}`)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"results":[`+strings.Join(payload, ",")+`]}`)
	}))
	defer srv.Close()

	ws := webSearch{engine: "searxng", baseURL: srv.URL, maxResults: 2, client: srv.Client()}
	out, err := webSearchExec(t, ws, "x")
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	lines := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "- ") {
			lines++
		}
	}
	if lines != 2 {
		t.Errorf("result lines = %d, want 2:\n%s", lines, out)
	}
}

func TestWebSearchContract(t *testing.T) {
	ws := webSearch{engine: "searxng", baseURL: "https://example.com", maxResults: 5}
	if !ws.ReadOnly() {
		t.Error("web_search must be read-only")
	}
	if ws.Name() != "web_search" {
		t.Errorf("Name = %q, want web_search", ws.Name())
	}
	var schema map[string]any
	if err := json.Unmarshal(ws.Schema(), &schema); err != nil {
		t.Fatalf("invalid schema: %v", err)
	}
	props := schema["properties"].(map[string]any)
	if _, ok := props["query"]; !ok {
		t.Errorf("schema missing query property: %v", schema)
	}
}
