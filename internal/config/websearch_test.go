package config

import "testing"

func TestWebSearchDisabledByDefault(t *testing.T) {
	cfg := Default()
	if cfg.WebSearch.Enabled() {
		t.Errorf("default web_search engine = %q, want disabled", cfg.WebSearch.Engine)
	}
}

func TestNormalizeWebSearchTrimsAndClamps(t *testing.T) {
	cfg := Default()
	cfg.WebSearch = WebSearchConfig{Engine: "  SearXNG ", BaseURL: " https://search.example.com/ ", APIKey: " k ", MaxResults: 100}
	if !normalizeWebSearch(cfg) {
		t.Fatal("normalizeWebSearch reported no change")
	}
	if cfg.WebSearch.Engine != "searxng" {
		t.Errorf("engine = %q, want searxng", cfg.WebSearch.Engine)
	}
	if cfg.WebSearch.BaseURL != "https://search.example.com/" {
		t.Errorf("base_url = %q, want trimmed", cfg.WebSearch.BaseURL)
	}
	if cfg.WebSearch.APIKey != "k" {
		t.Errorf("api_key = %q, want trimmed", cfg.WebSearch.APIKey)
	}
	if cfg.WebSearch.MaxResults != 20 {
		t.Errorf("max_results = %d, want clamped to 20", cfg.WebSearch.MaxResults)
	}
}

func TestNormalizeWebSearchClampsNegative(t *testing.T) {
	cfg := Default()
	cfg.WebSearch = WebSearchConfig{Engine: "brave", MaxResults: -3}
	normalizeWebSearch(cfg)
	if cfg.WebSearch.MaxResults != 0 {
		t.Errorf("max_results = %d, want 0 (means default 8)", cfg.WebSearch.MaxResults)
	}
}

func TestNormalizeWebSearchIdempotent(t *testing.T) {
	cfg := Default()
	cfg.WebSearch = WebSearchConfig{Engine: "tavily", BaseURL: "https://api.tavily.com/search", APIKey: "x", MaxResults: 8}
	normalizeWebSearch(cfg)
	if normalizeWebSearch(cfg) {
		t.Error("second normalize reported a change on already-normalized config")
	}
}
