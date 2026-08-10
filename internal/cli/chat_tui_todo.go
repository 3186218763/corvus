package cli

import (
	"encoding/json"
	"fmt"
	"strings"
)

// renderTodoPanel renders the task list pinned below the input from the latest
// todo_write call (m.todoArgs): a "Tasks done/total" header, completed items
// dimmed/checked, the in-progress one highlighted (its activeForm if given),
// pending ones muted. It returns "" when there's no list or every item is done,
// so the panel appears while work is outstanding and clears itself when finished.
func (m chatTUI) renderTodoPanel() string {
	todos, done := m.todoPanelState()
	if len(todos) == 0 || done == len(todos) {
		return ""
	}

	rowBudget := m.todoPanelRowBudget()
	if m.height > 0 && !m.hideComposer() && rowBudget == 0 {
		return ""
	}
	itemLimit := m.todoPanelItemLimit(todos, done, rowBudget)
	if itemLimit < 0 {
		return ""
	}
	return m.renderTodoPanelItems(todos, done, itemLimit)
}

// renderTodoPanelItems draws a specific task window. Callers measure this
// final styled string rather than estimating logical rows, so borders and
// wrapped CJK/long task labels are part of the layout budget.
func (m chatTUI) renderTodoPanelItems(todos []todoPanelTodo, done, itemLimit int) string {
	if itemLimit == 0 {
		summary := fmt.Sprintf("%d/%d", done, len(todos))
		return todoPanelStyle.Width(max(m.width, 10)).Render(accent("To-dos") + " " + dim(summary))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", accent("To-dos"), dim(fmt.Sprintf("%d/%d", done, len(todos))))
	start, end := todoPanelWindow(todos, itemLimit)
	if start > 0 {
		b.WriteString(dim(fmt.Sprintf("  +%d above", start)) + "\n")
	}
	for _, t := range todos[start:end] {
		indent := "  "
		if t.Level >= 1 {
			indent = "      " // sub-steps sit under their phase
		}
		switch t.Status {
		case "completed":
			b.WriteString(indent + green("✔") + " " + dim(t.Content) + "\n")
		case "in_progress":
			label := t.Content
			if t.ActiveForm != "" {
				label = t.ActiveForm
			}
			b.WriteString(indent + yellow("▶ "+label) + "\n")
		default:
			b.WriteString(indent + dim("○ "+t.Content) + "\n")
		}
	}
	if end < len(todos) {
		b.WriteString(dim(fmt.Sprintf("  +%d more", len(todos)-end)) + "\n")
	}
	return todoPanelStyle.Width(max(m.width, 10)).Render(strings.TrimRight(b.String(), "\n"))
}

func (m chatTUI) todoPanelState() ([]todoPanelTodo, int) {
	var p struct {
		Todos []todoPanelTodo `json:"todos"`
	}
	if err := json.Unmarshal([]byte(m.todoArgs), &p); err != nil || len(p.Todos) == 0 {
		return nil, 0
	}
	done := 0
	for _, t := range p.Todos {
		if t.Status == "completed" {
			done++
		}
	}
	return p.Todos, done
}

// todoPanelRowBudget is the coordinated allocation for the persistent panel.
// A zero budget means no terminal frame is available yet, so callers retain
// the normal item cap for initial/non-interactive renders.
func (m chatTUI) todoPanelRowBudget() int {
	return m.interactivePanelBudget().todoRows
}

// todoPanelItemLimit turns a full-panel row budget into the number of todo
// entries that can be shown. Measure the styled panel so its border, wrapping,
// and any "+N" markers all fit the same frame budget.
func (m chatTUI) todoPanelItemLimit(todos []todoPanelTodo, done, rowBudget int) int {
	limit := min(todoPanelMaxRows, len(todos))
	if rowBudget <= 0 {
		return limit
	}
	for ; limit >= 0; limit-- {
		if renderedLineCount(m.renderTodoPanelItems(todos, done, limit)) <= rowBudget {
			return limit
		}
	}
	return -1
}

func (m chatTUI) todoPanelDesiredRows(todos []todoPanelTodo, done int) int {
	limit := min(todoPanelMaxRows, len(todos))
	return renderedLineCount(m.renderTodoPanelItems(todos, done, limit))
}

func renderedLineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func todoPanelWindow(todos []todoPanelTodo, maxItems int) (int, int) {
	if len(todos) <= maxItems {
		return 0, len(todos)
	}
	active := -1
	for i, t := range todos {
		if t.Status == "in_progress" {
			active = i
			break
		}
	}
	if active < 0 {
		return 0, maxItems
	}
	start := active - maxItems/2
	if start < 0 {
		start = 0
	}
	if maxStart := len(todos) - maxItems; start > maxStart {
		start = maxStart
	}
	return start, start + maxItems
}
