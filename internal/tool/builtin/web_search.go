package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"corvus/internal/netpolicy"
	"corvus/internal/textutil"
	"corvus/internal/tool"
)

func init() { tool.RegisterBuiltin(webSearch{}) }

// webSearch is the configurable web-search built-in. Unlike the provider-side
// web_search toggle (which only exists for anthropic-compatible endpoints), it
// queries a search backend the user configures under [web_search] and works
// with every provider. The bare init instance has no engine and fails closed.
type webSearch struct {
	engine     string // searxng | brave | tavily
	baseURL    string
	apiKey     string
	maxResults int
	client     *http.Client
	// policy decides whether the engine endpoint may be contacted (see
	// internal/netpolicy). The zero value has no rules and a Default of Allow.
	policy netpolicy.Policy
}

// NewWebSearchTool returns web_search bound to a configured engine. engine is
// one of searxng, brave, or tavily; baseURL defaults per engine; apiKey is
// required for brave and tavily; maxResults clamps to 1-20 (0 = 8).
// Configuration errors are returned so an invalid [web_search] section refuses
// startup instead of installing a tool that fails on every call.
func NewWebSearchTool(engine, baseURL, apiKey string, maxResults int, client *http.Client, policy netpolicy.Policy) (tool.Tool, error) {
	engine = strings.ToLower(strings.TrimSpace(engine))
	baseURL = strings.TrimSpace(baseURL)
	apiKey = strings.TrimSpace(apiKey)
	switch engine {
	case "searxng", "brave", "tavily":
	default:
		return nil, fmt.Errorf("unknown web_search engine %q (supported: searxng, brave, tavily)", engine)
	}
	switch engine {
	case "searxng":
		if baseURL == "" {
			return nil, fmt.Errorf("engine %q requires base_url (your SearXNG instance)", engine)
		}
	case "brave", "tavily":
		if apiKey == "" {
			return nil, fmt.Errorf("engine %q requires api_key", engine)
		}
	}
	if client == nil {
		// Every configured engine must egress through the caller's netclient
		// transport (proxy policy, SSRF guard); a bare client here would be the
		// only request in the process that bypasses it (ADR-0004).
		return nil, fmt.Errorf("web_search: an HTTP client is required; pass the netclient-built client")
	}
	return webSearch{
		engine:     engine,
		baseURL:    baseURL,
		apiKey:     apiKey,
		maxResults: clampWebSearchMax(maxResults),
		client:     client,
		policy:     policy,
	}, nil
}

const (
	webSearchDefaultMax  = 8
	webSearchMaxMax      = 20
	webSearchMaxRead     = 1 << 20 // 1 MiB cap
	webSearchSnippetRune = 200
)

func clampWebSearchMax(n int) int {
	if n <= 0 {
		return webSearchDefaultMax
	}
	if n > webSearchMaxMax {
		return webSearchMaxMax
	}
	return n
}

func (webSearch) Name() string { return "web_search" }

func (webSearch) Description() string {
	return "Search the web through the configured search engine and return ranked results (title, URL, snippet). Use when you need current information, API documentation, or facts that are not in the workspace and not reachable via web_fetch. The engine is configured under [web_search] in .corvus/config.toml."
}

func (webSearch) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "query":{"type":"string","description":"Search query"},
  "max_results":{"type":"integer","description":"Maximum number of results to return (1-20, default 8)"}
},
"required":["query"]
}`)
}

func (webSearch) ReadOnly() bool { return true }

type webSearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}

func (t webSearch) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("web_search: invalid arguments: %w", err)
	}
	query := strings.TrimSpace(p.Query)
	if query == "" {
		return "", fmt.Errorf("web_search: query must not be empty")
	}
	if t.engine == "" {
		return "", fmt.Errorf("web_search: no engine configured; set [web_search] engine in .corvus/config.toml")
	}

	maxResults := t.maxResults
	if p.MaxResults > 0 {
		maxResults = clampWebSearchMax(p.MaxResults)
	}
	if maxResults <= 0 {
		maxResults = webSearchDefaultMax
	}
	switch t.engine {
	case "searxng", "brave", "tavily":
	default:
		return "", fmt.Errorf("web_search: unknown web_search engine %q (supported: searxng, brave, tavily)", t.engine)
	}
	if t.client == nil {
		return "", fmt.Errorf("web_search: no HTTP client configured")
	}
	client := t.client
	var results []webSearchResult
	var err error
	switch t.engine {
	case "searxng":
		results, err = t.searchSearxng(ctx, client, query, maxResults)
	case "brave":
		results, err = t.searchBrave(ctx, client, query, maxResults)
	case "tavily":
		results, err = t.searchTavily(ctx, client, query, maxResults)
	}
	if err != nil {
		return "", fmt.Errorf("web_search: %w", err)
	}
	if len(results) == 0 {
		return fmt.Sprintf("No web results for %q.", query), nil
	}
	if len(results) > maxResults {
		results = results[:maxResults]
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d result(s) for %q:\n", len(results), query)
	for _, r := range results {
		fmt.Fprintf(&b, "- %s — %s\n", strings.TrimSpace(r.Title), strings.TrimSpace(r.URL))
		if snippet := truncateGraphemes(strings.TrimSpace(r.Content), webSearchSnippetRune); snippet != "" {
			fmt.Fprintf(&b, "  %s\n", snippet)
		}
	}
	return b.String(), nil
}

func (t webSearch) engineURL() string {
	switch t.engine {
	case "brave":
		if t.baseURL == "" {
			return "https://api.search.brave.com/res/v1/web/search"
		}
	case "tavily":
		if t.baseURL == "" {
			return "https://api.tavily.com/search"
		}
	case "searxng":
		if t.baseURL == "" {
			return ""
		}
		if u, err := url.Parse(t.baseURL); err == nil && (u.Path == "" || u.Path == "/") {
			return strings.TrimRight(t.baseURL, "/") + "/search"
		}
		return t.baseURL
	}
	return t.baseURL
}

func (t webSearch) checkPolicy(endpoint string) error {
	if decision, rule := t.policy.Decide(endpoint); decision == netpolicy.Deny {
		if rule != "" {
			return fmt.Errorf("network policy denied web_search engine %s: matched deny rule %q", endpoint, rule)
		}
		return fmt.Errorf("network policy denied web_search engine %s: no rule matched and default is deny", endpoint)
	}
	return nil
}

func (t webSearch) searchSearxng(ctx context.Context, client *http.Client, query string, maxResults int) ([]webSearchResult, error) {
	endpoint := t.engineURL()
	if endpoint == "" {
		return nil, fmt.Errorf("searxng requires [web_search] base_url")
	}
	if err := t.checkPolicy(endpoint); err != nil {
		return nil, err
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid searxng base_url: %w", err)
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("format", "json")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("searxng returned %s", resp.Status)
	}
	var payload struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := decodeSearchJSON(resp.Body, &payload); err != nil {
		return nil, err
	}
	out := make([]webSearchResult, 0, len(payload.Results))
	for _, r := range payload.Results {
		out = append(out, webSearchResult{Title: r.Title, URL: r.URL, Content: r.Content})
	}
	return out, nil
}

func (t webSearch) searchBrave(ctx context.Context, client *http.Client, query string, maxResults int) ([]webSearchResult, error) {
	if t.apiKey == "" {
		return nil, fmt.Errorf("brave requires [web_search] api_key")
	}
	endpoint := t.engineURL()
	if err := t.checkPolicy(endpoint); err != nil {
		return nil, err
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid brave base_url: %w", err)
	}
	q := u.Query()
	q.Set("q", query)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Subscription-Token", t.apiKey)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("brave returned %s", resp.Status)
	}
	var payload struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := decodeSearchJSON(resp.Body, &payload); err != nil {
		return nil, err
	}
	out := make([]webSearchResult, 0, len(payload.Web.Results))
	for _, r := range payload.Web.Results {
		out = append(out, webSearchResult{Title: r.Title, URL: r.URL, Content: r.Description})
	}
	return out, nil
}

func (t webSearch) searchTavily(ctx context.Context, client *http.Client, query string, maxResults int) ([]webSearchResult, error) {
	if t.apiKey == "" {
		return nil, fmt.Errorf("tavily requires [web_search] api_key")
	}
	endpoint := t.engineURL()
	if err := t.checkPolicy(endpoint); err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]any{
		"api_key":     t.apiKey,
		"query":       query,
		"max_results": maxResults,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tavily returned %s", resp.Status)
	}
	var payload struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := decodeSearchJSON(resp.Body, &payload); err != nil {
		return nil, err
	}
	out := make([]webSearchResult, 0, len(payload.Results))
	for _, r := range payload.Results {
		out = append(out, webSearchResult{Title: r.Title, URL: r.URL, Content: r.Content})
	}
	return out, nil
}

func decodeSearchJSON(r io.Reader, dst any) error {
	raw, err := io.ReadAll(io.LimitReader(r, webSearchMaxRead))
	if err != nil {
		return fmt.Errorf("reading search response: %w", err)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("malformed search response: %w", err)
	}
	return nil
}

func truncateGraphemes(s string, n int) string {
	return textutil.TruncateGraphemes(s, n, "...")
}
