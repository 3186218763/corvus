// Formats tool calls in Codex-style density: "• Ran/Edited/Explored" with
// cyan tree verbs and └ gutters (not multi-color category ● cards).
package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"corvus/internal/tool"
)

// connector is the tree gutter under a tool header (output / multi-line cmd).
const connector = "  └ "

// exploreMaxLeaves caps visible Explored tree rows before "+N more".
const exploreMaxLeaves = 5

// connectorBlock renders lines under the connector: the first carries the └
// gutter, the rest align beneath it. Returns "" for no lines.
func connectorBlock(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	indent := strings.Repeat(" ", len([]rune(connector)))
	out := dim(connector) + lines[0]
	for _, ln := range lines[1:] {
		out += "\n" + indent + ln
	}
	return out
}

// toolVerb maps a tool's snake_case id to the verb shown in its card.
var toolVerb = map[string]string{
	"bash":           "Ran",
	"bash_output":    "Output",
	"kill_shell":     "Kill",
	"wait":           "Wait",
	"read_file":      "Read",
	"write_file":     "Edited",
	"edit_file":      "Edited",
	"multi_edit":     "Edited",
	"move_file":      "Move",
	"delete_range":   "Edited",
	"delete_symbol":  "Edited",
	"notebook_edit":  "Edited",
	"glob":           "Glob",
	"grep":           "Search",
	"ls":             "List",
	"web_fetch":      "Fetch",
	"web_search":     "Search",
	"complete_step":  "Step",
	"task":           "Task",
	"use_capability": "MCP",
}

// toolArgKey is the JSON field shown as the primary arg for each tool.
var toolArgKey = map[string]string{
	"bash":          "command",
	"bash_output":   "job_id",
	"kill_shell":    "job_id",
	"read_file":     "path",
	"write_file":    "path",
	"edit_file":     "path",
	"multi_edit":    "path",
	"move_file":     "source_path",
	"delete_range":  "path",
	"delete_symbol": "name",
	"notebook_edit": "path",
	"glob":          "pattern",
	"grep":          "pattern",
	"ls":            "path",
	"web_fetch":     "url",
	"web_search":    "query",
	"complete_step": "summary",
	"task":          "description",
}

// toolCategoryColor returns a legacy category color (diff headers / errors).
// New tool cards use dim • + cyan tree verbs instead of category-colored ●.
func toolCategoryColor(name string) cliColor {
	switch toolCategory[name] {
	case "read":
		return activeCLITheme.toolRead
	case "write":
		return activeCLITheme.success
	case "exec":
		return activeCLITheme.warn
	case "proc":
		return activeCLITheme.toolProc
	default:
		return activeCLITheme.accent
	}
}

// toolBullet is the default dim Codex marker for tool/agent rows.
func toolBullet() string { return dim("•") }

// toolBulletOK is a green success marker (completed Ran).
func toolBulletOK() string { return themeFg(activeCLITheme.success, bold("•")) }

// toolBulletErr is a red failure marker.
func toolBulletErr() string { return themeFg(activeCLITheme.danger, bold("•")) }

// toolDot is kept for call sites that still want a bullet; always dim •.
func toolDot(name string) string {
	_ = name
	return toolBullet()
}

var toolCategory = map[string]string{
	"read_file": "read", "ls": "read", "glob": "read", "grep": "read",
	"web_fetch": "read", "web_search": "read", "bash_output": "read",
	"write_file": "write", "edit_file": "write", "multi_edit": "write",
	"move_file": "write", "delete_range": "write", "delete_symbol": "write", "notebook_edit": "write",
	"bash": "exec",
	"wait": "proc", "kill_shell": "proc",
}

// isExploreCoalesceTool reports whether consecutive calls of this tool merge
// into one • Explored cell (read-category minus process readbacks).
func isExploreCoalesceTool(name string) bool {
	switch name {
	case "read_file", "ls", "glob", "grep", "web_fetch", "web_search":
		return true
	default:
		return false
	}
}

// isWriteTool reports tools that render as • Edited.
func isWriteTool(name string) bool {
	return toolCategory[name] == "write"
}

// toolDisplayName returns the card verb for a tool.
func toolDisplayName(name string) string {
	if _, short, ok := tool.SplitMCPName(name); ok {
		return short
	}
	if v, ok := toolVerb[name]; ok {
		return v
	}
	return name
}

// treeVerbColor is cyan for Explored tree labels (Search/Read/List/…).
func treeVerbColor(verb string) string {
	return themeFg(activeCLITheme.info, verb)
}

// toolArg pulls the primary argument shown beside the verb.
func toolArg(name, args string) string {
	var m map[string]any
	if json.Unmarshal([]byte(args), &m) != nil {
		return ""
	}
	if name == "wait" {
		return argList(m["job_ids"])
	}
	if name == "use_capability" {
		if id, ok := m["capability_id"].(string); ok && strings.TrimSpace(id) != "" {
			return strings.TrimSpace(id)
		}
		if action, ok := m["action"].(string); ok {
			return strings.TrimSpace(action)
		}
		return ""
	}
	v, ok := m[toolArgKey[name]]
	if !ok {
		return ""
	}
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case []any:
		return argList(x)
	case float64:
		return strconv.Itoa(int(x))
	default:
		return ""
	}
}

func argList(v any) string {
	arr, ok := v.([]any)
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, ", ")
}

// exploreLeaf is one row under • Explored.
type exploreLeaf struct {
	Verb string
	Arg  string
}

// exploreLeafFrom builds a leaf from a tool dispatch.
func exploreLeafFrom(name, args string) exploreLeaf {
	return exploreLeaf{Verb: toolDisplayName(name), Arg: toolArg(name, args)}
}

// Codex Explored nest: one hanging └ for the first leaf, four-space indent for
// the rest (prefix_lines semantics — not a sibling ├/└ tree).
const (
	exploreHangPrefix = "  └ " // first leaf only
	exploreContPrefix = "    " // subsequent leaves / +N more
)

// exploredCard renders:
//
//	• Explored
//	  └ Search …
//	    Read a.go, b.go
func exploredCard(leaves []exploreLeaf, width int) string {
	if width < 8 {
		width = 8
	}
	head := "  " + toolBullet() + " " + bold("Explored")
	if len(leaves) == 0 {
		return head
	}
	// Merge consecutive Read leaves into one "Read a, b, c" row (Codex).
	rows := coalesceReadLeaves(leaves)
	show := rows
	extra := 0
	if len(show) > exploreMaxLeaves {
		extra = len(show) - exploreMaxLeaves
		show = show[:exploreMaxLeaves]
	}
	var out strings.Builder
	out.WriteString(head)
	for i, leaf := range show {
		prefix := exploreContPrefix
		if i == 0 {
			prefix = exploreHangPrefix
		}
		prefixW := len([]rune(prefix))
		avail := width - prefixW - visibleWidth(leaf.Verb) - 1
		if avail < 4 {
			avail = 4
		}
		line := treeVerbColor(leaf.Verb)
		if leaf.Arg != "" {
			line += " " + clampPlain(leaf.Arg, avail)
		}
		out.WriteByte('\n')
		if i == 0 {
			out.WriteString(dim(prefix))
		} else {
			out.WriteString(prefix)
		}
		out.WriteString(line)
	}
	if extra > 0 {
		out.WriteByte('\n')
		out.WriteString(exploreContPrefix)
		out.WriteString(dim(fmt.Sprintf("+%d more", extra)))
	}
	return out.String()
}

// coalesceReadLeaves merges consecutive Read verbs into one comma-joined row.
func coalesceReadLeaves(leaves []exploreLeaf) []exploreLeaf {
	out := make([]exploreLeaf, 0, len(leaves))
	for _, leaf := range leaves {
		if leaf.Verb == "Read" && len(out) > 0 && out[len(out)-1].Verb == "Read" {
			if leaf.Arg == "" {
				continue
			}
			if out[len(out)-1].Arg == "" {
				out[len(out)-1].Arg = leaf.Arg
			} else {
				out[len(out)-1].Arg += ", " + leaf.Arg
			}
			continue
		}
		out = append(out, leaf)
	}
	return out
}

// encodeExploreLeaves serializes leaves for transcriptSource.aux re-render.
func encodeExploreLeaves(leaves []exploreLeaf) string {
	type row struct {
		Verb string `json:"verb"`
		Arg  string `json:"arg"`
	}
	rows := make([]row, len(leaves))
	for i, leaf := range leaves {
		rows[i] = row{Verb: leaf.Verb, Arg: leaf.Arg}
	}
	b, err := json.Marshal(map[string]any{"leaves": rows})
	if err != nil {
		return "{}"
	}
	return string(b)
}

// decodeExploreLeaves restores leaves from encodeExploreLeaves.
func decodeExploreLeaves(args string) []exploreLeaf {
	var payload struct {
		Leaves []struct {
			Verb string `json:"verb"`
			Arg  string `json:"arg"`
		} `json:"leaves"`
	}
	if json.Unmarshal([]byte(args), &payload) != nil {
		return nil
	}
	out := make([]exploreLeaf, 0, len(payload.Leaves))
	for _, leaf := range payload.Leaves {
		out = append(out, exploreLeaf{Verb: leaf.Verb, Arg: leaf.Arg})
	}
	return out
}

// toolCard renders a single tool dispatch in Codex density form.
func toolCard(name, args string, width int) string {
	if name == "explored" {
		return exploredCard(decodeExploreLeaves(args), width)
	}
	if name == "bash" {
		return bashToolCard(name, args, width)
	}
	if isExploreCoalesceTool(name) {
		return exploredCard([]exploreLeaf{exploreLeafFrom(name, args)}, width)
	}
	if isWriteTool(name) {
		return editedCard(name, args, width)
	}
	// MCP / other: • Verb arg
	label := toolDisplayName(name)
	arg := toolArg(name, args)
	head := "  " + toolBullet() + " " + bold(label)
	if arg == "" {
		return head
	}
	avail := width - 4 - len([]rune(label)) - 1
	return head + " " + clampPlain(arg, max(avail, 4))
}

// editedCard renders "  • Edited path".
func editedCard(name, args string, width int) string {
	path := toolArg(name, args)
	head := "  " + toolBullet() + " " + bold("Edited")
	if path == "" {
		// Fall back to display name for non-path writes (e.g. delete_symbol).
		label := toolDisplayName(name)
		if label != "Edited" {
			head = "  " + toolBullet() + " " + bold(label)
		}
		return head
	}
	avail := width - 4 - len([]rune("Edited")) - 1
	return head + " " + clampPlain(path, max(avail, 4))
}

// Ran command wrap / output gutters (Codex EXEC_DISPLAY_LAYOUT).
const (
	ranCmdPipePrefix        = "  │ "
	toolCallPreviewMaxLines = 5
	ranCmdContinuationMax   = 2
)

// bashToolCard renders "  • Ran <highlighted command>" with │ continuations.
func bashToolCard(name, args string, width int) string {
	cmd := strings.TrimSpace(toolArg(name, args))
	label := "Ran"
	if cmd == "" {
		return "  " + toolBullet() + " " + bold(label)
	}
	lines := strings.Split(cmd, "\n")
	headW := width - 4 - len([]rune(label)) - 1 // "  • Ran "
	first := highlightBash(clampPlain(lines[0], max(headW, 4)))
	head := "  " + toolBullet() + " " + bold(label) + " " + first
	if len(lines) == 1 {
		return head
	}
	rest := lines[1:]
	extra := 0
	if len(rest) > ranCmdContinuationMax {
		extra = len(rest) - ranCmdContinuationMax
		rest = rest[:ranCmdContinuationMax]
	}
	pipeW := len([]rune(ranCmdPipePrefix))
	var out strings.Builder
	out.WriteString(head)
	for _, ln := range rest {
		out.WriteByte('\n')
		out.WriteString(dim(ranCmdPipePrefix))
		out.WriteString(highlightBash(clampPlain(ln, max(width-pipeW, 4))))
	}
	if extra > 0 {
		out.WriteByte('\n')
		out.WriteString(dim(ranCmdPipePrefix))
		out.WriteString(dim(fmt.Sprintf("… +%d lines", extra)))
	}
	return out.String()
}

// renderToolCardCollapsed is the default finished card: short preview + outcome.
func renderToolCardCollapsed(name, args, output string, width int, ok bool, durationMs int64) string {
	card := toolCard(name, args, width)
	if block := renderToolOutputPreview(output, width, toolCallPreviewMaxLines); block != "" {
		card += "\n" + block
	}
	if line := toolOutcomeLine(ok, "", durationMs); line != "" {
		card += "\n" + line
	}
	return card
}

// renderToolCardExpanded renders the tool card plus full output under └.
func renderToolCardExpanded(name, args, output string, width int) string {
	return renderToolCardExpandedWithOutcome(name, args, output, width, true, 0)
}

func renderToolCardExpandedWithOutcome(name, args, output string, width int, ok bool, durationMs int64) string {
	card := toolCard(name, args, width)
	if block := renderToolOutputBlock(output, width); block != "" {
		card += "\n" + block
	}
	if line := toolOutcomeLine(ok, "", durationMs); line != "" {
		card += "\n" + line
	}
	return card
}

// renderToolOutputPreview is the default ≤N-line output nest under └.
func renderToolOutputPreview(output string, width, maxLines int) string {
	if strings.TrimSpace(output) == "" {
		return ""
	}
	if maxLines < 1 {
		maxLines = toolCallPreviewMaxLines
	}
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	show := min(len(lines), maxLines)
	rendered := make([]string, show)
	bodyW := max(width-len([]rune(connector)), 4)
	for i := 0; i < show; i++ {
		rendered[i] = dim(clampPlain(lines[i], bodyW))
	}
	if len(lines) > maxLines {
		rendered = append(rendered, dim(fmt.Sprintf("… +%d lines", len(lines)-maxLines)))
	}
	return connectorBlock(rendered)
}

// renderToolOutputBlock renders full output under the └ connector (Ctrl+B).
func renderToolOutputBlock(output string, width int) string {
	if strings.TrimSpace(output) == "" {
		return ""
	}
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	show := min(len(lines), shellExpandMaxLines)
	rendered := make([]string, show)
	for i := 0; i < show; i++ {
		rendered[i] = dim(clampPlain(lines[i], max(width-len([]rune(connector)), 4)))
	}
	if len(lines) > shellExpandMaxLines {
		rendered = append(rendered, dim(fmt.Sprintf("… %d more lines", len(lines)-shellExpandMaxLines)))
	}
	return connectorBlock(rendered)
}

// toolOutcomeLine renders "  ✓ · 0.41s" / "  ✗ · 1.5s". durationMs < 0 omits time.
func toolOutcomeLine(ok bool, exitHint string, durationMs int64) string {
	var mark string
	if ok {
		mark = themeFg(activeCLITheme.success, bold("✓"))
	} else {
		mark = themeFg(activeCLITheme.danger, bold("✗"))
		if exitHint != "" {
			mark += " " + dim("("+exitHint+")")
		}
	}
	if durationMs < 0 {
		return "  " + mark
	}
	sec := float64(durationMs) / 1000
	// Compact like Codex: 0.41s / 1.5s
	var dur string
	if sec < 10 {
		dur = fmt.Sprintf("%.2fs", sec)
	} else {
		dur = fmt.Sprintf("%.1fs", sec)
	}
	return "  " + mark + dim(" · "+dur)
}

// toolHead builds a bold, category-tinted verb + optional arg for diff headers.
func toolHead(name, arg string, width int) string {
	label := toolDisplayName(name)
	// Diff headers keep category color so write/read/exec stay scannable.
	// For write tools the display name is "Edited" (not "Update").
	head := themeFg(toolCategoryColor(name), bold(label))
	if arg != "" {
		avail := width - 4 - len([]rune(label)) - 2
		head += dim("(") + themeFg(activeCLITheme.toolArg, clampPlain(arg, max(avail, 4))) + dim(")")
	}
	return head
}
