package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"corvus/internal/tool"
)

func init() { tool.RegisterBuiltin(toolSearch{}) }

// toolSearch is the on-demand tool discovery built-in. The model calls it with
// a query when it needs a capability it cannot name; the snapshot callback
// (bound at boot) sees every tool currently registered, including MCP tools
// added at runtime. The bare init instance has no snapshot and fails closed.
type toolSearch struct {
	snapshot func() []tool.ContractEntry
}

// NewToolSearchTool returns tool_search bound to a registry snapshot provider.
func NewToolSearchTool(snapshot func() []tool.ContractEntry) tool.Tool {
	return toolSearch{snapshot: snapshot}
}

func (toolSearch) Name() string { return "tool_search" }

func (toolSearch) Description() string {
	return "Search the currently registered tools by keyword and return matching tool names with descriptions. Use when you need a capability you cannot name precisely, when a task could be done by a tool you are not sure is available, or when the available tool set may include MCP or on-demand sources. The query may contain several words; results are ranked by name match first, then description match. Returns up to limit (default 8) tools."
}

func (toolSearch) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "query":{"type":"string","description":"Keywords to search tool names and descriptions with"},
  "limit":{"type":"integer","description":"Maximum number of results to return (1-25, default 8)"}
},
"required":["query"]
}`)
}

func (toolSearch) ReadOnly() bool { return true }

const toolSearchDefaultLimit = 8
const toolSearchMaxLimit = 25

func (t toolSearch) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("tool_search: invalid arguments: %w", err)
	}
	query := strings.TrimSpace(p.Query)
	if query == "" {
		return "", fmt.Errorf("tool_search: query must not be empty")
	}
	if t.snapshot == nil {
		return "", fmt.Errorf("tool_search: unavailable (no tool registry bound)")
	}
	limit := p.Limit
	if limit <= 0 {
		limit = toolSearchDefaultLimit
	}
	if limit > toolSearchMaxLimit {
		limit = toolSearchMaxLimit
	}

	terms := strings.Fields(strings.ToLower(query))
	entries := t.snapshot()
	matched := make([]tool.ContractEntry, 0, len(entries))
	for _, e := range entries {
		if e.Name == "tool_search" {
			continue
		}
		if score := toolSearchScore(e, terms); score > 0 {
			e.Description = strings.TrimSpace(e.Description)
			matched = append(matched, e)
		}
	}
	sort.SliceStable(matched, func(i, j int) bool {
		si, sj := toolSearchScore(matched[i], terms), toolSearchScore(matched[j], terms)
		if si != sj {
			return si > sj
		}
		return matched[i].Name < matched[j].Name
	})
	if len(matched) > limit {
		matched = matched[:limit]
	}

	if len(matched) == 0 {
		return fmt.Sprintf("No tools match %q. connect_tool_source can enable additional tool sources (skills, task, web_fetch, lsp, sessions, memory).", query), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d tool(s) match %q:\n", len(matched), query)
	for _, e := range matched {
		desc := e.Description
		if desc == "" {
			desc = "(no description)"
		}
		flag := ""
		if e.ReadOnly {
			flag = " (read-only)"
		}
		fmt.Fprintf(&b, "- %s — %s%s\n", e.Name, desc, flag)
	}
	return b.String(), nil
}

// toolSearchScore ranks an entry against the query terms: exact name match and
// name-prefix matches dominate; description matches count only when the whole
// query appears or every term appears in the description.
func toolSearchScore(e tool.ContractEntry, terms []string) int {
	name := strings.ToLower(e.Name)
	desc := strings.ToLower(e.Description)
	query := strings.Join(terms, " ")

	score := 0
	if name == query {
		score += 100
	}
	if score < 60 && strings.HasPrefix(name, query) {
		score += 60
	}
	if score < 40 && strings.Contains(name, query) {
		score += 40
	}
	if score < 30 && strings.Contains(desc, query) {
		score += 30
	}
	if score == 0 {
		all := true
		for _, term := range terms {
			if !strings.Contains(name, term) && !strings.Contains(desc, term) {
				all = false
				break
			}
		}
		if all {
			score += 10
		} else {
			for _, term := range terms {
				if strings.Contains(name, term) || strings.Contains(desc, term) {
					score += 5
				}
			}
		}
	}
	return score
}
