package config

import "strings"

// normalizeWebSearch trims and clamps the [web_search] section. It never
// rewrites provider entries: the decision to suppress provider-side server
// web_search when a local engine is configured happens at boot, so a config
// edit cannot silently destroy the user's per-provider toggle.
func normalizeWebSearch(c *Config) bool {
	w := &c.WebSearch
	engine := strings.ToLower(strings.TrimSpace(w.Engine))
	baseURL := strings.TrimSpace(w.BaseURL)
	apiKey := strings.TrimSpace(w.APIKey)
	maxResults := w.MaxResults
	if maxResults < 0 {
		maxResults = 0
	}
	if maxResults > 20 {
		maxResults = 20
	}
	changed := engine != w.Engine || baseURL != w.BaseURL || apiKey != w.APIKey || maxResults != w.MaxResults
	w.Engine = engine
	w.BaseURL = baseURL
	w.APIKey = apiKey
	w.MaxResults = maxResults
	return changed
}
