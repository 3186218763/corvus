package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"corvus/internal/agent"
	"corvus/internal/control"
	"corvus/internal/i18n"
	"corvus/internal/provider"
)

func transcriptContentWidth(termW int, nativeScrollback bool) int {
	if !nativeScrollback {
		termW-- // reserve the last column for the transcript scrollbar
	}
	return max(termW, 1)
}

func (m *chatTUI) clearTranscriptDisplay() {
	if m.pendingCommit != nil {
		*m.pendingCommit = (*m.pendingCommit)[:0]
	}
	m.transcript = nil
	m.transcriptSources = nil
	m.wrappedLines = nil
	m.blockLineCounts = nil
	m.liveDirtyIdx = nil
	m.turnReceipt = ""
	m.viewport.SetContent("")
	m.shellOutputs = make(map[string]string)
	m.shellExpanded = make(map[string]bool)
	m.shellMeta = make(map[string]shellRunMeta)
	m.shellNativeFlushed = make(map[string]bool)
	m.shellLiveIdx = make(map[string]int)
	m.shellTranscriptIdx = make(map[string]int)
	m.toolCardIdx = make(map[string]int)
	m.toolStreams = make(map[string]*toolProgressState)
	m.answerIdx = -1
	m.answerFlushed = 0
	m.reasoningLineIdx = -1
	m.reasoningTextIdx = -1
	m.reasoningView = m.reasoningView[:0]
	m.toolStreamID = ""
	m.toolStreamIdx = -1
	m.toolTail = nil
	m.toolPartial = ""
	m.toolLineCount = 0
	m.flushExploreCard()
}

// flushExploreCard closes the open • Explored coalesce buffer so the next
// non-read tool or user turn starts a fresh cell.
func (m *chatTUI) flushExploreCard() {
	m.exploreIdx = -1
	m.exploreLeaves = nil
}

// appendExploreTool merges a read-category tool into the open Explored cell
// (or opens one). All tool ids in the group share the same transcript index.
func (m *chatTUI) appendExploreTool(id, name, args string) {
	leaf := exploreLeafFrom(name, args)
	// exploreIdx zero-value is 0; require non-empty leaves so a fresh TUI
	// (exploreIdx unset) never overwrites transcript[0].
	open := m.exploreIdx >= 0 && m.exploreIdx < len(m.transcript) && len(m.exploreLeaves) > 0
	if !open {
		m.exploreLeaves = []exploreLeaf{leaf}
		m.ensureBlank()
		m.commitTranscriptSource(transcriptSource{
			kind:    transcriptSourceToolCard,
			raw:     "explored",
			aux:     encodeExploreLeaves(m.exploreLeaves),
			shellID: id,
		})
		m.exploreIdx = len(m.transcript) - 1
		m.hadWorkActivity = true
		if id != "" {
			m.toolCardIdx[id] = m.exploreIdx
		}
		return
	}
	m.exploreLeaves = append(m.exploreLeaves, leaf)
	m.hadWorkActivity = true
	if id != "" {
		m.toolCardIdx[id] = m.exploreIdx
	}
	m.reRenderExploreCard()
}

// reRenderExploreCard rewrites the open Explored transcript block from leaves.
func (m *chatTUI) reRenderExploreCard() {
	if m.exploreIdx < 0 || m.exploreIdx >= len(m.transcript) {
		return
	}
	aux := encodeExploreLeaves(m.exploreLeaves)
	src := transcriptSource{kind: transcriptSourceToolCard, raw: "explored", aux: aux}
	block := exploredCard(m.exploreLeaves, transcriptContentWidth(m.width, m.nativeScrollback))
	m.setTranscriptBlock(m.exploreIdx, block, src)
	m.transcriptDirty = true
}

// scrollChunkHeight is the largest block (in lines) finalize prints at once in
// native-scrollback mode, leaving room for the pinned bottom frame.
func (m chatTUI) scrollChunkHeight() int {
	if m.height <= 0 {
		return 100
	}
	if n := m.height - m.bottomRows(); n > 1 {
		return n
	}
	return 1
}

// chunkLines splits s into blocks of at most n lines each, preserving order and
// line content. A single block is returned when it already fits.
func chunkLines(s string, n int) []string {
	if n < 1 {
		n = 1
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return []string{s}
	}
	var out []string
	for i := 0; i < len(lines); i += n {
		end := i + n
		if end > len(lines) {
			end = len(lines)
		}
		out = append(out, strings.Join(lines[i:end], "\n"))
	}
	return out
}

// clampWidth hard-breaks any line wider than width so no scrollback line wraps
// in the terminal. bubbletea's inline renderer estimates how far to scroll for
// each printed block from each line's width (insertAbove: offset += width/w); an
// over-wide line that the terminal wraps throws that estimate off and drifts the
// pinned input box off-screen. Lines already within width are left byte-for-byte
// untouched (chunkByWidth preserves content and ANSI), so rendered tables and the
// wrapped answer — which the markdown renderer already fit to width — are safe;
// only stray long lines (tool-dispatch args, unwrapped code) get broken.
func clampWidth(s string, width int) string {
	if width <= 0 {
		return s
	}
	// ansi.Hardwrap breaks any line over `width` visible cols on grapheme
	// boundaries, preserving ANSI and counting wide chars — exactly what we want,
	// and lines already within width pass through unchanged.
	return ansi.Hardwrap(s, width, false)
}

// commitLine queues one finalized block for the next scrollback flush.
func (m *chatTUI) commitLine(s string) {
	*m.pendingCommit = append(*m.pendingCommit, s)
	m.appendTranscriptBlock(s, transcriptSource{kind: transcriptSourceFixed})
}

// ensureBlank guarantees a single blank line before the next cell.
// No-op at top of transcript or when a blank already trails.
func (m *chatTUI) ensureBlank() {
	if n := len(m.transcript); n > 0 && strings.TrimSpace(m.transcript[n-1]) != "" {
		m.commitLine("")
	}
}

func (m *chatTUI) commitSpacer() { m.ensureBlank() }

// bottomRows is the terminal-row height of the pinned bottom region: any open
// bottom panels (todo / approval / chooser / rewind / completion), the composer
// when visible, and the two fixed status rows. Full-screen managers such as MCP
// and skills normally render inside the main transcript area; in native
// scrollback mode they join the bottom rail because there is no main viewport.
func (m chatTUI) bottomRows() int {
	rows := 0
	if m.panelsValid {
		rows = m.panels.rows
	} else {
		rows = m.renderBottomPanels().rows
	}
	// composerRaisedRows mirrors the currently visible panels. It is kept as a
	// separate value for cursor/layout consumers, but never reserves rows from a
	// panel that has already closed.
	if !m.hideComposer() && m.composerRaisedRows > rows {
		rows = m.composerRaisedRows
	}
	// Remove the hardcoded working-line increment — it is counted inside
	// statusLineCount via computeStatusLineCount, which also accounts for
	// wrapping. The fallback to 1 (unwrapped) covers the initial frame and
	// tests that don't call Update first.
	if !m.hideComposer() {
		rows += m.input.Height()
		rows += m.queueIndicatorRows(m.composerFrameWidth())
	}
	if m.statusLineCount > 0 {
		return rows + m.statusLineCount
	}
	return rows + 1 // fallback for tests that don't set statusLineCount
}

// reasoningBlock renders raw thinking text as dim, width-wrapped lines under a
// "⎿" connector that ties the block to the "▎ thinking…" marker above it. A
// positive maxLines keeps only the trailing visual lines (the live view); 0
// renders all (verbose collapse).
func reasoningBlock(raw string, width, maxLines int) string {
	w := width - len([]rune(connector))
	if w < 8 {
		w = 8
	}
	var lines []string
	for _, ln := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
		for _, wl := range strings.Split(ansi.Wrap(expandTabs(ln), w, ""), "\n") {
			lines = append(lines, dim(wl))
		}
	}
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return connectorBlock(lines)
}

// streamToolOutput appends a chunk of a running tool's output and re-renders its
// live block (the last toolStreamTailLines lines) under the tool card, opening
// the block on the first chunk. Mirrors streamReasoning.
func (m *chatTUI) streamToolOutput(id, chunk string) {
	if id == "" {
		return
	}
	if m.toolStreams == nil {
		m.toolStreams = make(map[string]*toolProgressState)
	}
	state := m.toolStreams[id]
	if state == nil {
		state = &toolProgressState{startedAt: time.Now()}
		m.toolStreams[id] = state
	}

	liveIdx := -1
	if !m.nativeScrollback {
		if idx, ok := m.shellLiveIdx[id]; ok && idx >= 0 && idx < len(m.transcript) && m.transcriptSources[idx].kind == transcriptSourceFixed {
			liveIdx = idx
		} else {
			liveIdx = len(m.transcript)
			m.commitLine("") // placeholder; setLiveBlock fills it
			m.shellLiveIdx[id] = liveIdx
		}
	} else {
		delete(m.shellLiveIdx, id)
	}
	// Accumulate full output for shell commands so Ctrl+B can expand it.
	if strings.HasPrefix(id, "shell-") {
		m.shellOutputs[id] += chunk
	}
	// Fold completed lines into the bounded tail; keep the trailing partial.
	data := state.partial + chunk
	for {
		i := strings.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		pushToolProgressLine(state, strings.TrimRight(data[:i], "\r"))
		data = data[i+1:]
	}
	state.partial = data

	vis := state.tail
	if state.partial != "" {
		vis = append(append([]string{}, state.tail...), state.partial)
	}
	m.toolStreamID = id
	m.toolStreamIdx = liveIdx
	m.toolTail = state.tail
	m.toolPartial = state.partial
	m.toolLineCount = state.lineCount
	m.toolStreamStart = state.startedAt
	if m.nativeScrollback || liveIdx < 0 {
		return
	}
	lines := make([]string, len(vis))
	contentWidth := transcriptContentWidth(m.width, m.nativeScrollback)
	for i, ln := range vis {
		lines[i] = dim(clampPlain(ln, contentWidth-len([]rune(connector))))
	}
	m.setLiveBlock(liveIdx, connectorBlock(lines))
}

// pushToolProgressLine appends one completed line to a tool-scoped bounded tail.
func pushToolProgressLine(state *toolProgressState, line string) {
	state.lineCount++
	state.tail = append(state.tail, line)
	if len(state.tail) > toolStreamTailLines {
		copy(state.tail, state.tail[1:])
		state.tail = state.tail[:toolStreamTailLines]
	}
}

// collapseToolOutput finalises a finished tool's live stream block: the live
// canvas is removed and the card is re-rendered with default ≤5-line preview +
// outcome (Ctrl+B still expands full output). No-op when id isn't streaming.
func (m *chatTUI) collapseToolOutput(id string) {
	if id == "" {
		return
	}
	if m.nativeScrollback {
		// Native scrollback cannot rewrite earlier cards: emit preview once.
		if !m.shellNativeFlushed[id] {
			if meta, hasMeta := m.shellMeta[id]; hasMeta {
				full := m.shellOutputs[id]
				if block := renderToolOutputPreview(full, m.width, toolCallPreviewMaxLines); block != "" {
					m.commitLine(block)
				}
				if line := toolOutcomeLine(meta.ok, "", meta.durationMs); line != "" {
					m.commitLine(line)
				}
				m.shellNativeFlushed[id] = true
			}
		}
		if m.toolStreamID == id {
			m.toolStreamIdx = -1
			m.toolStreamID = ""
			m.toolTail = m.toolTail[:0]
			m.toolPartial = ""
			m.toolLineCount = 0
		}
		delete(m.toolStreams, id)
		delete(m.shellLiveIdx, id)
		return
	}
	// Remove this id's live stream canvas if still present.
	idx := -1
	if m.toolStreamID == id && m.toolStreamIdx >= 0 {
		idx = m.toolStreamIdx
	} else if liveIdx, ok := m.shellLiveIdx[id]; ok {
		idx = liveIdx
	}
	if idx >= 0 && idx < len(m.transcript) && m.transcriptSources[idx].kind == transcriptSourceFixed {
		m.removeTranscriptBlock(idx)
	}
	delete(m.shellLiveIdx, id)
	if m.toolStreamID == id {
		m.toolStreamIdx = -1
		m.toolStreamID = ""
		m.toolTail = m.toolTail[:0]
		m.toolPartial = ""
		m.toolLineCount = 0
	}
	delete(m.toolStreams, id)
	// Re-anchor Ctrl+B and paint collapsed preview on the card.
	if cardIdx, ok := m.toolCardIdx[id]; ok && cardIdx >= 0 && cardIdx < len(m.transcript) {
		m.shellTranscriptIdx[id] = cardIdx
		m.shellExpanded[id] = false
		if cardIdx < len(m.transcriptSources) {
			src := m.transcriptSources[cardIdx]
			m.setLiveBlock(cardIdx, m.renderTranscriptSource(src, m.width, markerNone))
		}
	}
}

// toggleShellOutput expands or collapses shell output on the card block.
// Collapsed = ≤5-line preview + outcome; expanded = full output + outcome.
// Called on Ctrl+B.
func (m *chatTUI) toggleShellOutput() {
	// Prefer toolCardIdx (stable card anchors) over shellTranscriptIdx, which
	// may lag after stream collapse / blank gap rows.
	var lastID string
	lastIdx := -1
	for id, idx := range m.toolCardIdx {
		if idx < 0 || idx >= len(m.transcriptSources) {
			continue
		}
		if strings.TrimSpace(m.shellOutputs[id]) == "" {
			continue
		}
		src := m.transcriptSources[idx]
		if src.kind != transcriptSourceToolCard || src.shellID != id {
			continue
		}
		if idx > lastIdx {
			lastID = id
			lastIdx = idx
		}
	}
	if lastID == "" || lastIdx < 0 {
		return
	}
	src := m.transcriptSources[lastIdx]
	m.shellExpanded[lastID] = !m.shellExpanded[lastID]
	m.shellTranscriptIdx[lastID] = lastIdx
	m.setLiveBlock(lastIdx, m.renderTranscriptSource(src, m.width, markerNone))
}

// beginToolRunning arms streaming state for a just-dispatched tool without
// painting a transcript "working…" wall (Codex keeps progress ambient above
// the composer). streamToolOutput opens a live block on the first real chunk;
// collapseToolOutput closes it on the result.
func (m *chatTUI) beginToolRunning(id string) {
	if id == "" {
		return
	}
	if m.toolStreams == nil {
		m.toolStreams = make(map[string]*toolProgressState)
	}
	state := &toolProgressState{startedAt: time.Now()}
	m.toolStreams[id] = state
	m.toolStreamID = id
	m.toolTail = state.tail
	m.toolPartial = state.partial
	m.toolLineCount = state.lineCount
	// Clear accumulated output and expansion state for this tool ID so a re-run
	// (e.g. repeated !pwd with the same "shell-pwd" id) doesn't append to old
	// output or inherit the previous run's expansion.
	delete(m.shellOutputs, id)
	delete(m.shellExpanded, id)
	delete(m.shellMeta, id)
	delete(m.shellNativeFlushed, id)
	delete(m.shellLiveIdx, id)
	m.toolStreamStart = state.startedAt
	m.toolStreamFrame = 0
	m.toolStreamIdx = -1 // no transcript wall until real output streams
	// Ctrl+B still anchors to the card (set at dispatch); do not pre-create a
	// live stream slot that would paint "working…" into history.
}

// tickToolRunning is intentionally a no-op: tool progress is ambient
// (runningWorkingLine above the composer), not a transcript wall.
func (m *chatTUI) tickToolRunning() {}

// pruneOlderReasoningBlocks removes committed reasoning transcript blocks so
// the history only shows the latest thinking. keep is the index to retain
// (-1 removes every reasoning block). removeTranscriptBlock already shifts
// live reasoning/answer/tool indices.
func (m *chatTUI) pruneOlderReasoningBlocks(keep int) {
	m.ensureTranscriptSources()
	for i := len(m.transcriptSources) - 1; i >= 0; i-- {
		if i == keep {
			continue
		}
		if m.transcriptSources[i].kind == transcriptSourceReasoning {
			m.removeTranscriptBlock(i)
			if keep > i {
				keep--
			}
		}
	}
}

// commitReasoning closes any live thinking surface. Default mode never put a
// wall in the transcript (ambient working line only). Verbose mode keeps the
// full thinking text for the *latest* turn only. Reports whether a reasoning
// block remains visible (answer spacing).
func (m *chatTUI) commitReasoning() bool {
	if m.reasoningNative {
		kept := m.showReasoning && strings.TrimSpace(m.reasoning.String()) != ""
		if kept {
			m.commitSpacer()
			m.commitLine(reasoningBlock(m.reasoning.String(), transcriptContentWidth(m.width, m.nativeScrollback), 0))
		}
		m.reasoning.Reset()
		m.reasoningView = m.reasoningView[:0]
		m.reasoningNative = false
		m.thinkStart = time.Time{}
		return kept
	}
	// Default (non-verbose): no transcript rows for thinking.
	if !m.showReasoning {
		if m.reasoningTextIdx >= 0 {
			m.removeTranscriptBlock(m.reasoningTextIdx)
		}
		if m.reasoningLineIdx >= 0 {
			m.removeTranscriptBlock(m.reasoningLineIdx)
		}
		m.reasoning.Reset()
		m.reasoningView = m.reasoningView[:0]
		m.reasoningLineIdx = -1
		m.reasoningTextIdx = -1
		m.thinkStart = time.Time{}
		m.transcriptDirty = true
		return false
	}
	// Verbose: keep full text body if any.
	kept := false
	if strings.TrimSpace(m.reasoning.String()) != "" {
		raw := m.reasoning.String()
		contentWidth := transcriptContentWidth(m.width, m.nativeScrollback)
		if m.reasoningTextIdx >= 0 {
			m.pruneOlderReasoningBlocks(m.reasoningTextIdx)
			m.setTranscriptBlock(m.reasoningTextIdx, reasoningBlock(raw, contentWidth, 0), transcriptSource{
				kind: transcriptSourceReasoning, raw: raw,
			})
			kept = true
		} else {
			m.pruneOlderReasoningBlocks(-1)
			m.commitSpacer()
			m.commitLine(reasoningBlock(raw, contentWidth, 0))
			// commitLine doesn't set source kind; fix last block if possible.
			if idx := len(m.transcript) - 1; idx >= 0 {
				m.setTranscriptBlock(idx, reasoningBlock(raw, contentWidth, 0), transcriptSource{
					kind: transcriptSourceReasoning, raw: raw,
				})
			}
			kept = true
		}
	} else if m.reasoningTextIdx >= 0 {
		m.removeTranscriptBlock(m.reasoningTextIdx)
	}
	if m.reasoningLineIdx >= 0 {
		m.removeTranscriptBlock(m.reasoningLineIdx)
	}
	m.transcriptDirty = true
	m.reasoning.Reset()
	m.reasoningView = m.reasoningView[:0]
	m.reasoningLineIdx = -1
	m.reasoningTextIdx = -1
	m.thinkStart = time.Time{}
	return kept
}

// commitReasoningBeforeAnswer closes a real reasoning block and leaves exactly
// one blank transcript row before the assistant answer — but only when a
// reasoning block is still visible (verbose mode). Default ambient thinking
// leaves no transcript rows.
func (m *chatTUI) commitReasoningBeforeAnswer() {
	hadReasoning := m.reasoningNative || m.reasoningLineIdx >= 0 || m.reasoningTextIdx >= 0 || m.reasoning.Len() > 0
	kept := m.commitReasoning()
	if hadReasoning && kept {
		m.commitSpacer()
	}
}

// streamAnswer renders the answer streamed so far up to its last completed
// paragraph (flushableMarkdownPrefix) and writes it as one transcript block,
// rewritten in place as later paragraphs land — so a long reply appears chunk by
// chunk instead of all at once on turn end. The trailing, still-streaming block
// stays buffered (a half-written fence/list never renders early), and it only
// re-renders when a new paragraph actually closes.
func (m *chatTUI) streamAnswer() {
	if m.nativeScrollback {
		return
	}
	prefix := flushableMarkdownPrefix(m.pending.String())
	if len(prefix) <= m.answerFlushed {
		return
	}
	source := transcriptSource{kind: transcriptSourceMarkdown, raw: prefix}
	m.answerFlushed = len(prefix)
	if m.answerIdx < 0 {
		m.answerIdx = len(m.transcript)
		m.commitTranscriptSource(source)
	} else {
		markers := currentTranscriptMarkers(m.transcriptSources)
		var marker transcriptMarker
		if m.answerIdx >= 0 && m.answerIdx < len(markers) {
			marker = markers[m.answerIdx]
		}
		block := m.renderTranscriptSource(source, m.width, marker)
		m.setTranscriptBlock(m.answerIdx, block, source)
		m.transcriptDirty = true
	}
}

// commitPending freezes the full accumulated answer as markdown — overwriting the
// streamed block if one is open (streamAnswer), else committing fresh. Joining
// commitReasoning then commitPending puts the answer on its own line, restoring
// the thinking→answer break the renderer strips.
func (m *chatTUI) commitPending() {
	if strings.TrimSpace(m.pending.String()) == "" {
		m.answerIdx = -1
		m.answerFlushed = 0
		m.pending.Reset()
		return
	}
	raw := m.pending.String()
	source := transcriptSource{kind: transcriptSourceMarkdown, raw: raw}
	if m.answerIdx < 0 {
		m.commitTranscriptSource(source)
	} else {
		markers := currentTranscriptMarkers(m.transcriptSources)
		var marker transcriptMarker
		if m.answerIdx >= 0 && m.answerIdx < len(markers) {
			marker = markers[m.answerIdx]
		}
		block := m.renderTranscriptSource(source, m.width, marker)
		m.setTranscriptBlock(m.answerIdx, block, source)
		m.transcriptDirty = true
	}
	m.pending.Reset()
	m.answerIdx = -1
	m.answerFlushed = 0
}

// flushableMarkdownPrefix returns the longest prefix of buf made of complete
// markdown blocks — text up to the last blank line outside any open fenced code
// block. A blank line inside a ``` / ~~~ fence isn't a boundary, so a half-written
// code block stays buffered until it closes.
func flushableMarkdownPrefix(buf string) string {
	lines := strings.Split(buf, "\n")
	inFence := false
	boundary := -1
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
			continue
		}
		if !inFence && t == "" {
			boundary = i
		}
	}
	if boundary <= 0 {
		return ""
	}
	return strings.Join(lines[:boundary], "\n")
}

// replaySectionsFor turns a loaded session into scrollback blocks. Normal tool
// results remain quiet, while interrupted-turn reasoning and tool cards replay
// from provider-excluded LocalOnly records so restart matches the live view.
func replaySectionsFor(history []provider.Message, width int) []string {
	return replaySectionsForWithAssistantRenderer(
		history,
		width,
		renderAssistantMarkdown,
		func(raw string, width int, current bool) string {
			return renderUserBubble(raw, width, false, current)
		},
		false,
		false,
	)
}

// replaySectionsForWithAssistantRenderer renders replay history sections. When
// nameLast/lastUserFull are set, the last assistant body and the last user
// bubble of the section list carry the live markers (used when this bundle is
// the bottom-most block); every other section renders demoted history.
func replaySectionsForWithAssistantRenderer(
	history []provider.Message,
	width int,
	renderAssistant func(string, int, bool) string,
	renderUser func(string, int, bool) string,
	nameLast bool,
	lastUserFull bool,
) []string {
	lastUserSection := -1
	lastAssistantBody := -1
	for i, m := range history {
		switch {
		case m.LocalOnly:
			if strings.TrimSpace(m.Content) != "" {
				lastAssistantBody = i
			}
		case m.Role == provider.RoleUser:
			if _, isSteer := agent.SteerText(m.Content); !isSteer {
				lastUserSection = i
			}
		case m.Role == provider.RoleAssistant:
			if strings.TrimSpace(m.Content) != "" {
				lastAssistantBody = i
			}
		}
	}
	// Mirror currentTranscriptMarkers: an assistant body is only ever named
	// when no user section follows it (a trailing user demotes it, as in
	// [u, a, u]).
	if lastUserSection > lastAssistantBody {
		lastAssistantBody = -1
	}
	var out []string
	for i, m := range history {
		if m.LocalOnly {
			// Interrupted-turn partial reasoning stays part of the recovery
			// replay; completed-turn reasoning never renders in history.
			if reasoning := strings.TrimSpace(m.ReasoningContent); reasoning != "" {
				out = append(out, dim("  ▎ "+i18n.M.ChatThinking)+"\n"+reasoningBlock(reasoning, width, 0)+"\n\n")
			}
			if body := strings.TrimSpace(m.Content); body != "" {
				out = append(out, renderAssistant(body, width, i == lastAssistantBody && nameLast)+"\n\n")
			}
			for _, call := range m.ToolCalls {
				out = append(out, toolCard(call.Name, "", width)+"\n\n")
			}
			if m.InterruptedTurn != nil {
				out = append(out, fmt.Sprintf("  · %s\n\n", interruptedTurnDisplayNotice()))
			}
			continue
		}
		switch m.Role {
		case provider.RoleUser:
			// Steer messages are surfaced as a notice line, not a user bubble.
			if steerText, isSteer := agent.SteerText(m.Content); isSteer {
				out = append(out, fmt.Sprintf("  ↪ %s\n\n", steerText))
				continue
			}
			content := control.StripComposePrefixes(m.Content)
			out = append(out, renderUser(content, width, i == lastUserSection && lastUserFull)+"\n\n")
		case provider.RoleAssistant:
			body := strings.TrimSpace(m.Content)
			if body != "" {
				out = append(out, renderAssistant(body, width, i == lastAssistantBody && nameLast)+"\n\n")
			}
			for _, call := range m.ToolCalls {
				out = append(out, toolCard(call.Name, call.Arguments, width)+"\n\n")
			}
		}
	}
	return out
}

func interruptedTurnDisplayNotice() string {
	return i18n.M.InterruptedRecovery
}

// renderTUIBanner is the single-line session wordmark + model label printed
// once at the top of the session (optional missing-key warning may follow).
// The ◆ wordmark shares the transcript's two-column gutter with user › and
// assistant • markers. ChatTip is intentionally omitted for Codex density.
func renderTUIBanner(label, missing string, width int) string {
	var b strings.Builder
	if width >= 60 {
		b.WriteString("  " + accent("◆") + " " + bold("corvus") + "  " + dim("· "+label) + "\n")
	} else {
		line := "  " + accent("◆") + " " + bold("corvus") + " " + dim("· "+label)
		b.WriteString(ansi.Truncate(line, width, "…"))
	}
	if missing != "" {
		b.WriteString(wrapForViewport("  ! "+missing, width, activeCLITheme.warn) + "\n")
	}
	return b.String()
}

// wrapForViewport hard-wraps text to fit width columns and colours every line.
func wrapForViewport(text string, width int, fg cliColor) string {
	if width <= 0 {
		width = 80
	}
	return themeStyle(fg).Width(width).Render(text)
}

// renderUserBubble renders the just-submitted prompt as a single transcript
// line. User messages are differentiated with their foreground treatment, not
// a full-width surface that adds two blank rows and becomes grey in ANSI-256
// terminals.
func renderUserBubble(line string, width int, planMode bool, current bool) string {
	line = displayLineForImageRefs(line)
	prefix := "› "
	if planMode {
		prefix = "› [plan] "
	}
	_ = width
	if !colorOn() {
		return prefix + line
	}
	fg := activeCLITheme.accent
	if !current {
		fg = activeCLITheme.userBubbleFaded
	}
	// Bold › marker + accent/faded body. Separate themeFg calls keep bold's
	// trailing reset from stripping the body colour.
	body := bold(themeFg(fg, prefix)) + themeFg(fg, line)
	return body
}

func displayLineForImageRefs(line string) string {
	idx := 0
	out := cliImageRefRe.ReplaceAllStringFunc(line, func(_ string) string {
		idx++
		return " [image" + strconv.Itoa(idx) + "]"
	})
	return strings.TrimSpace(out)
}
