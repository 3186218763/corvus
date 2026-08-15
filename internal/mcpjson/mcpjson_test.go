package mcpjson

import (
	"reflect"
	"testing"
)

// The canonical schema round-trips the full field set, including the Corvus
// policy extensions earlier parsers dropped (timeouts, tier, auto_start).
func TestParseRoundTripsFullSpec(t *testing.T) {
	auto := false
	doc, err := Parse([]byte(`{"mcpServers": {"srv": {
		"type": "http",
		"command": "c",
		"args": ["a", "b"],
		"env": {"K": "V"},
		"url": "https://example.test",
		"headers": {"H": "V"},
		"auto_start": false,
		"startup_timeout_seconds": 5,
		"call_timeout_seconds": 60,
		"tool_timeout_seconds": {"slow-tool": 120},
		"tier": "eager",
		"title": "Srv",
		"description": "desc"
	}}}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	spec, ok := doc.MCPServers["srv"]
	if !ok {
		t.Fatalf("srv missing: %+v", doc)
	}
	want := ServerSpec{
		Type: "http", Command: "c", Args: []string{"a", "b"},
		Env: map[string]string{"K": "V"}, URL: "https://example.test",
		Headers: map[string]string{"H": "V"}, AutoStart: &auto,
		StartupTimeoutSeconds: 5, CallTimeoutSeconds: 60,
		ToolTimeoutSeconds: map[string]int{"slow-tool": 120},
		Tier:               "eager", Title: "Srv", Description: "desc",
	}
	if !reflect.DeepEqual(spec, want) {
		t.Fatalf("spec = %+v, want %+v", spec, want)
	}
	if names := doc.SortedNames(); !reflect.DeepEqual(names, []string{"srv"}) {
		t.Fatalf("SortedNames = %v", names)
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	if _, err := Parse([]byte(`{"mcpServers":`)); err == nil {
		t.Fatal("malformed document parsed without error")
	}
}

func TestNormalizeType(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"", "", true},
		{"stdio", "stdio", true},
		{"HTTP", "http", true},
		{"streamable-http", "http", true},
		{" streamable-HTTP ", "http", true},
		{"sse", "sse", true},
		{"carrier-pigeon", "carrier-pigeon", false},
	}
	for _, c := range cases {
		got, ok := NormalizeType(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("NormalizeType(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestNormalizeTier(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"eager", "eager", true},
		{"EAGER", "eager", true},
		{"background", "background", true},
		{"lazy", "background", true},
		{"", "background", true},
		{"urgent", "background", false},
	}
	for _, c := range cases {
		got, ok := NormalizeTier(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("NormalizeTier(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}
