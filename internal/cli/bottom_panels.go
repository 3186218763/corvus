package cli

import "strings"

// bottomPanels holds the rendered bottom region so bottomRows() and View()
// share one render pass per event. Refreshed in the Update wrapper.
type bottomPanels struct {
	todo, banner, chooser, rewind, mcpImport, resumePick, quickPick, copyPick, cheatsheet, completion, manager, managerFooter string

	rows int
}

// renderBottomPanels renders every bottom panel once. panelRenderHook (nil in
// production) lets tests count renders per panel name.
func (m chatTUI) renderBottomPanels() bottomPanels {
	var p bottomPanels
	hook := func(name string, s string) string {
		if m.panelRenderHook != nil {
			m.panelRenderHook(name)
		}
		return s
	}
	p.todo = hook("todo", m.renderTodoPanel())
	p.banner = hook("banner", m.renderApprovalBanner())
	p.chooser = hook("chooser", m.renderChooser())
	p.rewind = hook("rewind", m.renderRewind())
	p.mcpImport = hook("mcpImport", m.renderMCPImport())
	p.resumePick = hook("resumePick", m.renderResumePicker())
	p.quickPick = hook("quickPick", m.renderQuickPicker())
	p.copyPick = hook("copyPick", m.renderCopyPicker())
	p.cheatsheet = hook("cheatsheet", m.renderCheatsheet())
	p.completion = hook("completion", m.renderCompletion())
	if m.nativeScrollback {
		p.manager = hook("manager", m.renderMainManager())
	}
	// The manager footer rides the bottom rail in both modes (see bottomRows()).
	p.managerFooter = hook("managerFooter", m.renderMainManagerFooter())
	for _, s := range []string{p.todo, p.banner, p.chooser, p.rewind, p.mcpImport, p.resumePick, p.quickPick, p.copyPick, p.cheatsheet, p.completion, p.manager, p.managerFooter} {
		if s != "" {
			p.rows += strings.Count(s, "\n") + 1
		}
	}
	return p
}
