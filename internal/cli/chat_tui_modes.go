package cli

import (
	"strings"

	"corvus/internal/config"
	"corvus/internal/control"
	"corvus/internal/i18n"
)

// cycleMode handles the Shift+Tab gesture using the same three safe modes users
// see in Claude Code: Ask → Auto → Plan → Ask. YOLO stays outside this cycle and
// remains an explicit Ctrl+Y choice.
func (m *chatTUI) cycleMode() {
	if m.ctrl == nil || m.ctrl.ToolApprovalMode() == control.ToolApprovalYolo {
		return
	}
	switch {
	case m.planMode:
		m.planMode = false
		m.ctrl.SetToolApprovalMode(control.ToolApprovalAsk)
	case m.ctrl.ToolApprovalMode() == control.ToolApprovalDontAsk:
		m.ctrl.SetToolApprovalMode(control.ToolApprovalAsk)
	case m.ctrl.ToolApprovalMode() == control.ToolApprovalAsk:
		m.ctrl.SetToolApprovalMode(control.ToolApprovalAuto)
	case m.ctrl.ToolApprovalMode() == control.ToolApprovalAuto:
		m.planMode = true
		m.ctrl.SetToolApprovalMode(control.ToolApprovalAsk)
		m.ctrl.ClearGoal()
	}
	m.ctrl.SetPlanMode(m.planMode)
}

func (m chatTUI) desktopShortcutLayout() bool {
	return m.cfg != nil && m.cfg.UIShortcutLayout() == "desktop"
}

func (m *chatTUI) toggleYoloMode() {
	if m.ctrl == nil {
		return
	}
	if m.ctrl.ToolApprovalMode() == control.ToolApprovalYolo {
		restore := m.yoloRestoreToolApprovalMode
		if restore != control.ToolApprovalAuto {
			restore = control.ToolApprovalAsk
		}
		m.ctrl.SetToolApprovalMode(restore)
		m.yoloRestoreToolApprovalMode = ""
		return
	}
	restore := m.ctrl.ToolApprovalMode()
	if restore != control.ToolApprovalAuto {
		restore = control.ToolApprovalAsk
	}
	m.yoloRestoreToolApprovalMode = restore
	m.ctrl.SetToolApprovalMode(control.ToolApprovalYolo)
}

func (m chatTUI) modeTagText() string {
	goalMode := strings.TrimSpace(m.ctrl.Goal()) != "" && m.ctrl.GoalStatus() == control.GoalStatusRunning
	toolApprovalMode := m.ctrl.ToolApprovalMode()
	if m.desktopShortcutLayout() {
		switch {
		case m.planMode && toolApprovalMode == control.ToolApprovalYolo:
			return "Plan+YOLO"
		case goalMode && toolApprovalMode == control.ToolApprovalYolo:
			return "Goal+YOLO"
		case toolApprovalMode == control.ToolApprovalYolo:
			return "YOLO"
		case m.planMode:
			return "Plan"
		case goalMode && toolApprovalMode == control.ToolApprovalAuto:
			return "Goal+Auto"
		case goalMode:
			return "Goal"
		case toolApprovalMode == control.ToolApprovalAuto:
			return "Auto"
		case toolApprovalMode == control.ToolApprovalDontAsk:
			return "Don't Ask"
		default:
			return "Ask"
		}
	}
	switch {
	case m.planMode && toolApprovalMode == control.ToolApprovalYolo:
		return "Plan+YOLO"
	case m.planMode && toolApprovalMode == control.ToolApprovalAuto:
		return "Plan+Approve"
	case goalMode && toolApprovalMode == control.ToolApprovalYolo:
		return "Goal+YOLO"
	case goalMode && toolApprovalMode == control.ToolApprovalAuto:
		return "Goal+Approve"
	case toolApprovalMode == control.ToolApprovalYolo:
		return "YOLO"
	case toolApprovalMode == control.ToolApprovalAuto:
		return "Auto+Approve"
	case toolApprovalMode == control.ToolApprovalDontAsk:
		return "Don't Ask"
	case m.planMode:
		return "Plan"
	case goalMode:
		return "Goal"
	default:
		return "Auto"
	}
}

func (m *chatTUI) toggleVerboseReasoning(notify bool) {
	m.showReasoning = !m.showReasoning
	var saveErr error
	if m.cfg != nil {
		_ = m.cfg.SetShowReasoning(m.showReasoning)
		path := config.SourcePath()
		if path == "" {
			path = config.ProjectConfigPathForRoot(".")
		}
		saveErr = config.EditConfigFile(path, func(cfg *config.Config) error {
			return cfg.SetShowReasoning(m.showReasoning)
		})
	}
	if !notify {
		return
	}
	suffix := ""
	if saveErr != nil {
		suffix = "\npreference was not saved: " + saveErr.Error()
	}
	if m.showReasoning {
		m.notice("verbose on — thinking text will be shown" + suffix)
	} else {
		m.notice("verbose off — thinking text will stay collapsed" + suffix)
	}
}

// toggleMouseCapture flips whether Corvus owns the mouse. It's session-only
// (unlike /verbose, this accommodates the terminal/multiplexer at hand rather
// than recording a lasting preference) — mirrors nativeScrollback, which is
// likewise never persisted to config. Clears any in-app selection/scrollbar
// drag in flight so a stale one can't be found mid-gesture once the terminal
// starts intercepting the events that would have finished it.
func (m *chatTUI) toggleMouseCapture() {
	m.mouseCaptureOff = !m.mouseCaptureOff
	m.sel = selection{}
	m.composerSel = composerSelection{}
	m.scrollbarDrag = false
	m.autoScroll = 0
	if m.mouseCaptureOff {
		m.notice(i18n.M.MouseCaptureOffHint)
	} else {
		m.notice(i18n.M.MouseCaptureOnHint)
	}
}
