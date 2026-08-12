package boot

import (
	"fmt"
	"time"

	"corvus/internal/config"
	"corvus/internal/netclient"
	"corvus/internal/netpolicy"
	"corvus/internal/tool"
	"corvus/internal/tool/builtin"
)

// buildWebSearchTool constructs the local web_search built-in when a
// [web_search] engine is configured, or returns nil (tool absent from the
// model-visible tool set). Configuration errors refuse startup rather than
// installing a tool that fails on every call.
func buildWebSearchTool(cfg *config.Config, proxySpec netclient.ProxySpec, netPolicy netpolicy.Policy) (tool.Tool, error) {
	ws := cfg.WebSearch
	if !ws.Enabled() {
		return nil, nil
	}
	client, err := netclient.NewHTTPClient(proxySpec, netclient.TransportOptions{
		DialTimeout:           webSearchDialTimeout,
		TLSHandshakeTimeout:   webSearchDialTimeout,
		ResponseHeaderTimeout: webSearchDialTimeout,
		// Cap the whole request including the body: a search backend that
		// answers headers but stalls its body must not hang the turn forever.
		Timeout: webSearchRequestTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("web_search: network client: %w", err)
	}
	tl, err := builtin.NewWebSearchTool(ws.Engine, ws.BaseURL, ws.APIKey, ws.MaxResults, client, netPolicy)
	if err != nil {
		return nil, fmt.Errorf("web_search: %w", err)
	}
	return tl, nil
}

const (
	webSearchDialTimeout    = 15 * time.Second
	webSearchRequestTimeout = 30 * time.Second
)
