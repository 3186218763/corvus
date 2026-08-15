// Package mcpjson defines the canonical wire schema for Claude-compatible
// .mcp.json server entries — the one place the field set and its aliases live
// (ADR-0007). Every reader of an mcpServers map decodes into ServerSpec and
// applies its own policy (tolerance for unknown transports, tier warnings,
// name validation) on top; consumers keep their internal types and map from
// this schema. Before this package existed, four parsers each defined the
// shape with drifting field sets: timeouts dropped on plugin import, tier
// invisible to the main config reader, auto_start discarded on compat import.
package mcpjson

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ServerSpec is one "mcpServers" entry on the wire. Fields beyond Claude's
// own set (type/command/args/env/url/headers) are Corvus policy extensions:
// the three timeout fields, tier, auto_start, and the display title/
// description used when importing plugin manifests.
type ServerSpec struct {
	Type                  string            `json:"type,omitempty"`
	Command               string            `json:"command,omitempty"`
	Args                  []string          `json:"args,omitempty"`
	Env                   map[string]string `json:"env,omitempty"`
	URL                   string            `json:"url,omitempty"`
	Headers               map[string]string `json:"headers,omitempty"`
	AutoStart             *bool             `json:"auto_start,omitempty"`
	StartupTimeoutSeconds int               `json:"startup_timeout_seconds,omitempty"`
	CallTimeoutSeconds    int               `json:"call_timeout_seconds,omitempty"`
	ToolTimeoutSeconds    map[string]int    `json:"tool_timeout_seconds,omitempty"`
	Tier                  string            `json:"tier,omitempty"`
	Title                 string            `json:"title,omitempty"`
	Description           string            `json:"description,omitempty"`
}

// Document is the top-level shape: {"mcpServers": {name: ServerSpec}}.
type Document struct {
	MCPServers map[string]ServerSpec `json:"mcpServers"`
}

// Parse decodes one .mcp.json-style document.
func Parse(b []byte) (Document, error) {
	var doc Document
	if err := json.Unmarshal(b, &doc); err != nil {
		return Document{}, fmt.Errorf("mcpjson: parse: %w", err)
	}
	return doc, nil
}

// SortedNames returns the server names in a stable order for deterministic
// connection/import order.
func (d Document) SortedNames() []string {
	names := make([]string, 0, len(d.MCPServers))
	for name := range d.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// NormalizeType maps wire transport aliases onto the canonical set:
// "streamable-http" → "http"; "stdio", "http", "sse" → themselves; "" → ""
// (unresolved — callers infer by URL presence or default to stdio). The bool
// reports whether the value was recognized after aliasing. Unknown values
// ("carrier-pigeon") return (input, false); whether that is fatal is caller
// policy — strict importers reject, tolerant readers pass the raw value
// through for later validation.
func NormalizeType(t string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "":
		return "", true
	case "stdio":
		return "stdio", true
	case "http", "streamable-http":
		return "http", true
	case "sse":
		return "sse", true
	default:
		return t, false
	}
}

// NormalizeTier maps tier aliases: "eager" → "eager"; "background" and
// "lazy" → "background"; "" → "background". The bool reports recognition so
// callers can warn about typos instead of silently demoting to background.
func NormalizeTier(t string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "eager":
		return "eager", true
	case "background", "lazy", "":
		return "background", true
	default:
		return "background", false
	}
}
