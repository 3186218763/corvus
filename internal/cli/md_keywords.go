package cli

import (
	"regexp"
	"sort"
	"strings"
)

const maxProseHighlights = 4

type proseHighlightClass uint8

const (
	proseInfo proseHighlightClass = iota
	proseSuccess
	proseDanger
	proseWarn
	proseSecondary
	proseAccent
)

type proseHighlightBudget struct {
	remaining int
	seen      map[string]struct{}
}

func newProseHighlightBudget() proseHighlightBudget {
	return proseHighlightBudget{
		remaining: maxProseHighlights,
		seen:      make(map[string]struct{}),
	}
}

type proseKeyword struct {
	text  string
	class proseHighlightClass
}

var proseKeywords = []proseKeyword{
	{class: proseInfo, text: "Explored"},
	{class: proseInfo, text: "Read"},
	{class: proseInfo, text: "Search"},
	{class: proseInfo, text: "List"},
	{class: proseInfo, text: "Fetch"},
	{class: proseInfo, text: "读取"},
	{class: proseInfo, text: "搜索"},
	{class: proseSuccess, text: "Edited"},
	{class: proseSuccess, text: "Created"},
	{class: proseSuccess, text: "Updated"},
	{class: proseSuccess, text: "Moved"},
	{class: proseSuccess, text: "Wrote"},
	{class: proseSuccess, text: "PASS"},
	{class: proseSuccess, text: "Success"},
	{class: proseSuccess, text: "Done"},
	{class: proseSuccess, text: "Completed"},
	{class: proseSuccess, text: "Ready"},
	{class: proseSuccess, text: "Passed"},
	{class: proseSuccess, text: "通过"},
	{class: proseSuccess, text: "成功"},
	{class: proseSuccess, text: "完成"},
	{class: proseSuccess, text: "就绪"},
	{class: proseDanger, text: "FAIL"},
	{class: proseDanger, text: "Error"},
	{class: proseDanger, text: "Failed"},
	{class: proseDanger, text: "Failure"},
	{class: proseDanger, text: "Blocked"},
	{class: proseDanger, text: "Invalid"},
	{class: proseDanger, text: "Panic"},
	{class: proseDanger, text: "错误"},
	{class: proseDanger, text: "失败"},
	{class: proseDanger, text: "阻塞"},
	{class: proseDanger, text: "无效"},
	{class: proseWarn, text: "Ran"},
	{class: proseWarn, text: "Build"},
	{class: proseWarn, text: "Test"},
	{class: proseWarn, text: "Run"},
	{class: proseWarn, text: "Warn"},
	{class: proseWarn, text: "Warning"},
	{class: proseWarn, text: "Retry"},
	{class: proseWarn, text: "Skipped"},
	{class: proseWarn, text: "Pending"},
	{class: proseWarn, text: "警告"},
	{class: proseWarn, text: "重试"},
	{class: proseWarn, text: "跳过"},
	{class: proseWarn, text: "等待"},
	{class: proseSecondary, text: "Task"},
	{class: proseSecondary, text: "MCP"},
	{class: proseSecondary, text: "Agent"},
	{class: proseSecondary, text: "Wait"},
	{class: proseSecondary, text: "renderer"},
	{class: proseSecondary, text: "parser"},
	{class: proseSecondary, text: "theme"},
	{class: proseSecondary, text: "cache"},
	{class: proseSecondary, text: "API"},
	{class: proseSecondary, text: "TUI"},
	{class: proseSecondary, text: "model"},
	{class: proseSecondary, text: "tool"},
	{class: proseSecondary, text: "渲染器"},
	{class: proseSecondary, text: "解析器"},
	{class: proseSecondary, text: "主题"},
	{class: proseSecondary, text: "缓存"},
	{class: proseAccent, text: "go"},
	{class: proseAccent, text: "git"},
	{class: proseAccent, text: "make"},
	{class: proseAccent, text: "npm"},
	{class: proseAccent, text: "cargo"},
	{class: proseAccent, text: "docker"},
	{class: proseAccent, text: "curl"},
	{class: proseAccent, text: "rg"},
	{class: proseAccent, text: "sed"},
}

var proseStructuralRe = regexp.MustCompile(
	`(?:\./)?(?:[A-Za-z0-9_.-]+/)+[A-Za-z0-9_.-]+\.[A-Za-z0-9]+|[A-Z][A-Za-z0-9_]*\(\)|[A-Za-z_][A-Za-z0-9_]*\.[A-Za-z_][A-Za-z0-9_]*`,
)

type proseMatch struct {
	start int
	end   int
	key   string
	class proseHighlightClass
}

func highlightProseText(text string, budget *proseHighlightBudget) string {
	if !colorOn() || text == "" {
		return text
	}
	if budget == nil {
		fresh := newProseHighlightBudget()
		budget = &fresh
	}
	if budget.remaining <= 0 {
		return text
	}

	matches := proseKeywordMatches(text)
	if len(matches) == 0 {
		return text
	}
	var b strings.Builder
	pos := 0
	for _, match := range matches {
		if match.start < pos || budget.remaining <= 0 {
			continue
		}
		key := strings.ToLower(text[match.start:match.end])
		if _, seen := budget.seen[key]; seen {
			continue
		}
		budget.seen[key] = struct{}{}
		b.WriteString(text[pos:match.start])
		b.WriteString(themeFg(proseKeywordColor(match.class), text[match.start:match.end]))
		pos = match.end
		budget.remaining--
	}
	if pos == 0 {
		return text
	}
	b.WriteString(text[pos:])
	return b.String()
}

func proseKeywordMatches(text string) []proseMatch {
	lower := strings.ToLower(text)
	matches := make([]proseMatch, 0, len(proseKeywords))
	for _, keyword := range proseKeywords {
		needle := strings.ToLower(keyword.text)
		for from := 0; from <= len(lower)-len(needle); {
			rel := strings.Index(lower[from:], needle)
			if rel < 0 {
				break
			}
			start := from + rel
			end := start + len(needle)
			if !isNonASCII(keyword.text) && !asciiTokenBoundary(lower, start, end) {
				from = end
				continue
			}
			matches = append(matches, proseMatch{
				start: start,
				end:   end,
				key:   strings.ToLower(keyword.text),
				class: keyword.class,
			})
			from = end
		}
	}
	for _, loc := range proseStructuralRe.FindAllStringIndex(text, -1) {
		matches = append(matches, proseMatch{
			start: loc[0],
			end:   loc[1],
			key:   strings.ToLower(text[loc[0]:loc[1]]),
			class: proseAccent,
		})
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].start != matches[j].start {
			return matches[i].start < matches[j].start
		}
		return matches[i].end > matches[j].end
	})
	return matches
}

func isNonASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return true
		}
	}
	return false
}

func asciiTokenBoundary(text string, start, end int) bool {
	if start > 0 && isASCIIWordByte(text[start-1]) {
		return false
	}
	return end >= len(text) || !isASCIIWordByte(text[end])
}

func isASCIIWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_'
}

func proseKeywordColor(class proseHighlightClass) cliColor {
	switch class {
	case proseInfo:
		return activeCLITheme.info
	case proseSuccess:
		return activeCLITheme.success
	case proseDanger:
		return activeCLITheme.danger
	case proseWarn:
		return activeCLITheme.warn
	case proseSecondary:
		return activeCLITheme.secondary
	case proseAccent:
		return activeCLITheme.accent
	default:
		return activeCLITheme.muted
	}
}
