// Copyright 2026 The go-steer team
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// sidebarWidth is the fixed-column count of the StatusSidebar panel
// (style.md §5).
const sidebarWidth = 32

// sidebarMinChatWidth is the minimum chat-column count we'll accept
// before forcing a fallback to StatusHeader layout. Below this we
// can't fit useful chat content next to a 32-col sidebar.
const sidebarMinChatWidth = 40

// wordWrap word-wraps s at width cols, preserving ANSI escapes from
// any prior lipgloss styling. Width <= 0 returns s unchanged.
func wordWrap(s string, width int) string {
	if width <= 0 {
		return s
	}
	return ansi.Wordwrap(s, width, " -")
}

// wordWrapIndent wraps s line-by-line at width and prefixes each
// non-first source line with indent. The wrap is hanging-aware:
// wrap-introduced continuations inherit BOTH the role indent AND
// the source line's own leading whitespace, so a "      long
// description" that overflows wraps to a continuation also at
// column 6 + role indent, not at column 0 + role indent.
//
// Width <= 0 returns s unchanged. Mirrors internal/tui's
// wrapForChat (model.go:477-490).
func wordWrapIndent(s string, width int, indent string) string {
	if width <= 0 {
		return s
	}
	sourceLines := strings.Split(s, "\n")
	var out strings.Builder
	for i, sl := range sourceLines {
		if i > 0 {
			out.WriteByte('\n')
		}
		// Pull the leading whitespace off this source line so we can
		// reapply it as the hanging indent on wrap-introduced
		// continuations. byte-iteration is fine — leading whitespace
		// is ASCII space / tab, never multi-byte.
		leading := ""
		for j := 0; j < len(sl); j++ {
			if sl[j] != ' ' && sl[j] != '\t' {
				leading = sl[:j]
				break
			}
		}
		// Role indent applies to every source line after the first
		// (the first sits flush under the role prefix; e.g. "ℹ  ").
		roleIndent := indent
		if i == 0 {
			roleIndent = ""
		}
		prefixed := roleIndent + sl
		wrapped := wordWrap(prefixed, width)
		wlines := strings.Split(wrapped, "\n")
		for k, wl := range wlines {
			if k == 0 {
				out.WriteString(wl)
			} else {
				out.WriteByte('\n')
				out.WriteString(roleIndent)
				out.WriteString(leading)
				out.WriteString(wl)
			}
		}
	}
	return out.String()
}

// effectiveLayout returns the layout we'll actually render — falls
// back from StatusSidebar to StatusHeader when the terminal is too
// narrow to fit both the sidebar and a useful chat column.
func (m Model) effectiveLayout() StatusLayout {
	if m.statusLayout == StatusSidebar &&
		m.width-sidebarWidth-3 < sidebarMinChatWidth {
		return StatusHeader
	}
	return m.statusLayout
}

// View composes the full TUI. Returns a tea.View with AltScreen on
// and the brand cursor block. Layout is governed by m.statusLayout
// (R-USE-2).
func (m Model) View() tea.View {
	if m.width == 0 || m.height == 0 {
		return tea.NewView("")
	}

	chat := m.viewport.View()
	input := m.renderInputBox()

	var body string
	switch m.effectiveLayout() {
	case StatusSidebar:
		// Footer wraps to the chat column, NOT to m.width — otherwise
		// the left block grows wider than the chat column and the
		// sidebar gets pushed off the right edge of the terminal.
		chatWidth := m.width - sidebarWidth - 3
		footer := m.renderFooter(chatWidth)
		help := m.renderHelpPanel(chatWidth)
		pal := m.renderPalette(chatWidth)
		// Force `left` to exactly chatWidth wide so the sidebar lands
		// at column chatWidth + divider regardless of how short the
		// individual rows are.
		leftParts := []string{chat}
		if help != "" {
			leftParts = append(leftParts, help)
		}
		if pal != "" {
			leftParts = append(leftParts, pal)
		}
		leftParts = append(leftParts, input)
		if t := m.renderToast(chatWidth); t != "" {
			leftParts = append(leftParts, t)
		}
		leftParts = append(leftParts, footer)
		left := lipgloss.NewStyle().Width(chatWidth).Render(
			lipgloss.JoinVertical(lipgloss.Left, leftParts...),
		)
		sidebar := m.renderSidebar()
		divider := strings.Repeat(GlyphColumn+"\n", lipgloss.Height(left))
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			left,
			m.styles.SidebarDivider.Render(strings.TrimRight(divider, "\n")),
			sidebar,
		)
	default:
		header := m.renderHeader()
		footer := m.renderFooter(m.width)
		help := m.renderHelpPanel(m.width)
		pal := m.renderPalette(m.width)
		parts := []string{header, chat}
		if help != "" {
			parts = append(parts, help)
		}
		if pal != "" {
			parts = append(parts, pal)
		}
		parts = append(parts, input)
		if t := m.renderToast(m.width); t != "" {
			parts = append(parts, t)
		}
		parts = append(parts, footer)
		body = lipgloss.JoinVertical(lipgloss.Left, parts...)
	}

	// Overlay any active modal centered over the body. Precedence:
	// permission > elicit > sideAnswer > demo modals. Permission and
	// elicit gate real agent activity so they win the screen even
	// over a /btw side-answer.
	switch {
	case m.pendingPermission != nil:
		modal := m.renderPermissionModal()
		body = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal)
	case m.pendingElicit != nil:
		modal := m.renderElicitModal()
		body = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal)
	case m.sideAnswer != nil:
		modal := m.renderSideAnswer()
		body = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal)
	case m.overlay != overlayNone:
		modal := m.renderOverlay()
		body = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal)
	}

	v := tea.NewView(body)
	v.AltScreen = true
	v.BackgroundColor = nil // respect the terminal's own background
	// Cell-motion mouse capture so the wheel scrolls the viewport.
	// Operators who want native terminal text-select hold Shift to
	// bypass capture (matches internal/tui + Claude Code).
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// resize recomputes the viewport and textarea dimensions from the
// current width / height + status layout. Called after WindowSizeMsg
// and after the user toggles StatusHeader <-> StatusSidebar.
func (m *Model) resize() {
	if m.width == 0 || m.height == 0 {
		return
	}

	layout := m.effectiveLayout()
	chatWidth := m.width
	if layout == StatusSidebar {
		chatWidth = m.width - sidebarWidth - 3
	}

	const inputHeight = 3
	const footerHeight = 1
	headerHeight := 0
	if layout == StatusHeader {
		headerHeight = 2 // status line + a blank row
	}
	// Allow the footer to wrap onto a second line when the terminal
	// is narrow; reserve up to 2 rows for it. Footer wrap width must
	// match the column it'll render in — chatWidth in sidebar mode,
	// m.width in header mode.
	footerRows := footerHeight
	footerWidth := m.width
	if layout == StatusSidebar {
		footerWidth = chatWidth
	}
	if footerWidth > 0 {
		footerRows = lipgloss.Height(m.renderFooter(footerWidth))
		if footerRows < 1 {
			footerRows = 1
		}
	}
	helpRows := 0
	if m.helpOpen {
		helpRows = lipgloss.Height(m.renderHelpPanel(footerWidth))
	}
	palRows := 0
	if m.palette != nil {
		palRows = lipgloss.Height(m.renderPalette(footerWidth))
	}
	queueRows := 0
	if len(m.queue) > 0 {
		queueRows = lipgloss.Height(m.renderQueuePanel())
	}
	chatHeight := m.height - headerHeight - inputHeight - footerRows - helpRows - palRows - queueRows - 2 // 2 = input top border + spacer
	if chatHeight < 3 {
		chatHeight = 3
	}

	if chatWidth < 1 {
		chatWidth = 1
	}
	m.viewport.SetWidth(chatWidth)
	m.viewport.SetHeight(chatHeight)
	m.input.SetWidth(chatWidth - 2) // leave room for the input border
	m.input.SetHeight(inputHeight)
}

// syncInputHeight clamps the textarea's height to its current line
// count (between textareaMinHeight and textareaMaxHeight). Returns
// true when the height changed so callers can trigger a layout
// reconciliation (resize + refresh + bottom-snap if pinned).
//
// Called from every keystroke-forward + after any programmatic
// input mutation so multi-line paste / typed newlines grow the
// box visibly, and Ctrl+U / Esc-out-of-history shrinks it back.
func (m *Model) syncInputHeight() bool {
	desired := m.input.LineCount()
	if desired < textareaMinHeight {
		desired = textareaMinHeight
	}
	if desired > textareaMaxHeight {
		desired = textareaMaxHeight
	}
	if m.input.Height() == desired {
		return false
	}
	m.input.SetHeight(desired)
	return true
}

// refreshViewport rebuilds the viewport's content from history plus
// the in-progress assistant message (R-CHAT-4) and spinner verb
// (R-CHAT-3). Called after any change that affects rendered text:
// resize, style change, new message, stream chunk, spinner tick.
// refreshAndScroll rebuilds the viewport content AND forces a
// scroll to the bottom — used by operator-initiated paths (slash
// commands, submit) where the operator should always see the new
// content even if they'd previously scrolled up. Autonomous paths
// (stream chunks) use refreshViewport directly to preserve scroll.
func (m *Model) refreshAndScroll() {
	// Operator-initiated paths usually reset or replace the input
	// (slash dispatch, history recall, palette insert) which can
	// shrink the textarea back to MinHeight. Sync the height first
	// so the viewport gets the freed-up rows in this same render.
	if m.syncInputHeight() {
		m.resize()
	}
	m.refreshViewport()
	m.viewport.GotoBottom()
}

func (m *Model) refreshViewport() {
	if m.width == 0 {
		return
	}
	var b strings.Builder
	entries := m.history.Snapshot()
	rule := m.styles.Rule.Render(strings.Repeat(GlyphRule, m.viewport.Width()))

	width := m.viewport.Width()
	for i, msg := range entries {
		if i > 0 {
			if msg.Role == RoleUser {
				b.WriteString("\n")
				b.WriteString(rule)
				b.WriteString("\n\n")
			} else {
				b.WriteString("\n\n")
			}
		}
		// Lazy-render cache (listcache.go) — skip the Glamour /
		// word-wrap / lipgloss work for unchanged messages.
		item := messageItem{msg: msg, idx: i, total: len(entries)}
		if cached, ok := m.listCache.get(item, width); ok {
			b.WriteString(cached)
		} else {
			rendered := m.renderMessage(msg)
			m.listCache.put(item, width, rendered)
			b.WriteString(rendered)
		}
	}

	if inProgress := m.renderInProgress(); inProgress != "" {
		if m.history.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(inProgress)
	}

	if m.history.Len() == 0 && m.state == stateIdle {
		hint := m.opts.Branding.EmptyStateHint
		if hint == "" {
			hint = "Ask me anything to get started."
		}
		b.WriteString(m.styles.SystemText.Render(hint))
	}

	// Preserve scroll position across re-renders: only auto-scroll
	// to the bottom when the operator is already pinned there. If
	// they've scrolled up to read backlog, an incoming stream chunk
	// must not yank them back (parity with internal/tui:512).
	atBottom := m.viewport.AtBottom()
	m.viewport.SetContent(b.String())
	// Defensively pin horizontal scroll to 0. The viewport supports
	// xOffset (for terminals wider than the chat column), but the
	// TUI never wants it — we wrap to chatWidth ourselves, so any
	// non-zero xOffset would cut chars off the left of every line
	// (`ansi.Cut` at viewport.go:362). Catching it here neutralizes
	// any scroll that crept in via mouse wheel, palette key, or
	// future bindings.
	m.viewport.SetXOffset(0)
	if atBottom {
		m.viewport.GotoBottom()
	}
}

// renderInProgress returns the live block at the bottom of the chat
// while a turn is streaming: the accumulated assistant text rendered
// through Glamour (R-CHAT-4), followed by the spinner verb line
// (R-CHAT-3) and the prompt queue panel (R-CHAT-10). Empty string
// when no turn is in flight AND the queue is empty.
func (m *Model) renderInProgress() string {
	if m.state != stateStreaming && len(m.queue) == 0 {
		return ""
	}
	var parts []string
	if m.state == stateStreaming {
		if strings.TrimSpace(m.inProgressText) != "" {
			mr := m.ensureMarkdown()
			// Incremental Glamour: reuse the cached render of the
			// stable prefix (everything up to the latest \n\n
			// outside an open code fence) and only re-render the
			// trailing partial on each chunk. Updates the cache
			// fields in place so the next chunk can reuse the
			// same stable render.
			body, newPrefix, newRender := mr.renderIncremental(
				m.inProgressText,
				m.inProgressStablePrefix,
				m.inProgressStableRender,
			)
			m.inProgressStablePrefix = newPrefix
			m.inProgressStableRender = newRender
			parts = append(parts, m.styles.AssistantText.Render(body))
		}
		parts = append(parts, m.renderSpinnerLine())
	}
	if q := m.renderQueuePanel(); q != "" {
		parts = append(parts, q)
	}
	return strings.Join(parts, "\n")
}

// renderQueuePanel renders the prompt queue (R-CHAT-10) with per-
// entry state glyphs (○ queued · ● in-flight · ✓ done · ✗ failed).
// Done / Failed entries linger for cullTTL before falling off so the
// operator sees the result. Empty string when the queue is empty.
func (m *Model) renderQueuePanel() string {
	m.cullQueue()
	if len(m.queue) == 0 {
		return ""
	}
	const queuePanelMax = 4
	width := m.viewport.Width()
	if width <= 0 {
		width = 80
	}

	pending := 0
	for _, e := range m.queue {
		if e.State == QueueQueued {
			pending++
		}
	}
	headerText := fmt.Sprintf("queue (%d entries, %d pending)", len(m.queue), pending)
	if pending == 0 {
		headerText = fmt.Sprintf("queue (%d entries)", len(m.queue))
	}
	header := m.styles.Muted.Italic(true).Render(headerText)
	rows := []string{"", header}

	visible := m.queue
	tail := 0
	if len(visible) > queuePanelMax {
		// Keep the most recent entries — older queued ones still
		// rendered but the head of the panel shows what just fell
		// out of view as a truncation hint.
		tail = len(visible) - queuePanelMax
		visible = visible[len(visible)-queuePanelMax:]
	}
	if tail > 0 {
		rows = append(rows, m.styles.Muted.Render(
			fmt.Sprintf("  %s %d earlier entries", GlyphTruncate, tail),
		))
	}
	for _, e := range visible {
		rows = append(rows, m.renderQueueRow(e, width))
	}
	return strings.Join(rows, "\n")
}

// renderQueueRow renders a single queue entry with its state glyph
// and color treatment. Failed entries append a truncated error tail.
// Injected entries (R-CHAT-11 InjectIntoCurrent mode) get a dim
// "(injected)" suffix so the operator can tell them apart from
// queue-drained Done entries.
func (m Model) renderQueueRow(e QueueEntry, width int) string {
	glyph, style := m.queueRowStyle(e.State)
	body := trimToolArg(e.Text, width-6)
	row := "  " + glyph + " " + body
	if e.Injected && e.State == QueueDone {
		row += "  " + m.styles.Muted.Render("(injected)")
	}
	if e.State == QueueFailed && e.Err != "" {
		row += "  " + m.styles.ErrorText.Render("("+trimToolArg(e.Err, 32)+")")
	}
	return style.Render(row)
}

// queueRowStyle returns the (glyph, base style) pair for one queue
// state. Reuses the tool-state glyph vocabulary from style.md §2 so
// the panel matches the rest of the TUI.
func (m Model) queueRowStyle(s QueueState) (string, lipgloss.Style) {
	switch s {
	case QueueInFlight:
		return GlyphTool, m.styles.Accent
	case QueueDone:
		return GlyphToolDone, m.styles.Muted
	case QueueFailed:
		return GlyphToolFail, m.styles.ErrorText
	default:
		return GlyphToolPending, m.styles.Muted
	}
}

// renderSpinnerLine renders the rotating cognition verb (R-CHAT-3).
// Picks ThinkingPhrases when the model is generating and
// WorkingPhrases after a tool call until the next text chunk.
func (m Model) renderSpinnerLine() string {
	pool := m.thinkingPhrases()
	if m.toolActive {
		pool = m.workingPhrases()
	}
	if len(pool) == 0 {
		return ""
	}
	verb := pool[m.thinkingIdx%len(pool)]
	return m.styles.Muted.Italic(true).Render(verb + GlyphTruncate)
}

// renderMessage renders a single Message row with the correct glyph
// + style for its Role (style.md §2 + §4). Output is word-wrapped to
// the viewport width so narrow terminals don't run text off-screen.
func (m Model) renderMessage(msg Message) string {
	width := m.viewport.Width()
	switch msg.Role {
	case RoleUser:
		prefix := m.styles.UserPrefix.Render(GlyphUserPrompt)
		body := wordWrapIndent(msg.Display(), width-2, "  ")
		return prefix + " " + m.styles.UserText.Render(body)
	case RoleAssistant:
		// Display() returns the cached Glamour render (Rendered) when
		// available; otherwise the raw text. We word-wrap only the
		// raw path — the Glamour render already wrapped to the
		// renderer's WithWordWrap width.
		text := msg.Display()
		if msg.Rendered == "" {
			text = wordWrap(text, width)
		}
		body := m.styles.AssistantText.Render(text)
		if footer := m.renderTurnFooter(msg); footer != "" {
			// Blank line + `└ ` prefix on the footer subordinates the
			// metadata visually to the message above (git-log style).
			return body + "\n\n" + footer
		}
		return body
	case RoleSystem:
		return m.styles.SystemText.Render(wordWrapIndent("ℹ  "+msg.Display(), width, "   "))
	case RoleError:
		return m.styles.ErrorText.Render(wordWrapIndent(GlyphWarn+"  "+msg.Display(), width, "   "))
	case RoleTool:
		head := m.styles.ToolHead.Render(GlyphTool + " " + msg.ToolName)
		if msg.ToolArgs != "" {
			args := wordWrap(msg.ToolArgs, width-lipgloss.Width(head)-1)
			return head + " " + m.styles.ToolBody.Render(args)
		}
		return head
	}
	return wordWrap(msg.Display(), width)
}

// renderHeader renders the StatusHeader layout's top line — status
// row + a blank spacer row beneath it. When the assembled line
// overflows the terminal width, wordWrap breaks it onto additional
// rows so segments don't run off-screen (the terminal's own
// soft-wrap would split across ANSI escape boundaries and corrupt
// the trailing chrome).
func (m Model) renderHeader() string {
	status := m.renderStatusLine()
	if m.width > 0 {
		status = wordWrap(status, m.width)
	}
	return status + "\n"
}

// renderStatusLine renders the one-line status used in StatusHeader
// (style.md §7.2). Format intentionally puts the spend metrics in a
// human-readable form: `15.2K in · 4.1K out · $0.04 · 9% ctx` rather
// than the bare "9% (19.3K)" which conflated context-fill % with
// total tokens.
func (m Model) renderStatusLine() string {
	// Wordmark, cursor block, then identity glyph + model. The
	// cursor block visually pins the brand line so the eye
	// finds it even when the rest of the status drifts off the
	// right edge on narrow terminals (parity with internal/
	// tui:branding.go:48-54).
	parts := []string{
		m.styles.Wordmark.Render(m.wordmark()),
		m.styles.Accent.Render(GlyphCursor),
		m.sep(),
		m.styles.AgentIdentity.Render(GlyphModel + " " + m.displayModelName()),
	}
	if prov := m.displayProvider(); prov != "" {
		parts = append(parts, m.sep(), m.styles.Muted.Render("provider: "+prov))
	}
	if cwd := m.displayCwd(); cwd != "" {
		parts = append(parts, m.sep(), m.styles.Muted.Render(cwd))
	}
	if m.permissionModeWired() {
		parts = append(parts,
			m.sep(),
			m.renderPermissionChip(),
		)
	}
	if summary := m.usageSummaryOneLine(); summary != "" {
		parts = append(parts,
			m.sep(),
			m.styles.Muted.Render(summary),
		)
	}
	return strings.Join(parts, "")
}

// renderSidebar renders the StatusSidebar layout's right-hand panel
// (style.md §7.2). Stacks the model + mode + spend metrics in a
// readable vertical layout — separate input/output tokens, context-
// window %, cumulative cost — sourced live from the host's
// UsageTracker + SubagentLister. The "modified files" preview section
// was dropped pending a real file-watch capability; until one exists
// any rendered value is fiction.
func (m Model) renderSidebar() string {
	headerLines := []string{
		"  " + m.styles.AgentIdentity.Render(GlyphModel+" "+m.displayModelName()),
		"    " + m.styles.Muted.Render(m.permMode.String()),
	}
	if line1, line2 := m.usageSummaryStacked(); line1 != "" {
		headerLines = append(headerLines,
			"    "+m.styles.Muted.Render(line1),
			"    "+m.styles.Muted.Render(line2),
		)
	}
	header := lipgloss.JoinVertical(lipgloss.Left, headerLines...)
	sub := m.sidebarSection("subagents", m.subagentSummary()...)
	return lipgloss.JoinVertical(lipgloss.Left, header, "", sub)
}

// sidebarSection renders a `─ heading ─` section with body rows.
func (m Model) sidebarSection(heading string, rows ...string) string {
	hr := strings.Repeat(GlyphRule, sidebarWidth-len(heading)-4)
	if hr == "" {
		hr = "─"
	}
	head := "  " + m.styles.SidebarHeading.Render(GlyphRule+" "+heading+" ") + m.styles.Rule.Render(hr)
	body := make([]string, 0, len(rows)+1)
	body = append(body, head)
	for _, r := range rows {
		body = append(body, "    "+m.styles.Muted.Render(r))
	}
	return lipgloss.JoinVertical(lipgloss.Left, body...)
}

// renderPermissionChip renders the permission-mode chip (R-PERM-6).
// The bypassPermissions state uses the warning style.
func (m Model) renderPermissionChip() string {
	if m.permMode == PermissionModeBypass {
		return m.styles.PermissionWarn.Render(m.permMode.String())
	}
	return m.styles.PermissionChip.Render(m.permMode.String())
}

// renderInputBox renders the textarea with a thin top border
// (style.md §5).
func (m Model) renderInputBox() string {
	width := m.viewport.Width()
	if width <= 0 {
		width = m.width
	}
	top := m.styles.InputBorderTop.Render(strings.Repeat(GlyphRule, width))
	return top + "\n" + m.input.View()
}

// renderFooter renders the bottom keymap legend (style.md §7.1)
// wrapped to width. Pass chatWidth in StatusSidebar mode and m.width
// in StatusHeader mode — wrapping to the wrong width can push the
// right-side panels off-screen.
//
// Only the four essential keys are surfaced by default. Everything
// else (modal shortcuts, layout / mode cycling, newline insertion)
// is discoverable via `?`, mirroring how Antigravity and Claude Code
// keep their footer terse.
func (m Model) renderFooter(width int) string {
	hint := m.opts.Branding.FooterHint
	if hint == "" {
		hint = m.footerHint()
	}
	if width > 0 {
		hint = wordWrap(hint, width)
	}
	return m.styles.Footer.Render(hint)
}

// footerHint picks the right keymap legend for the current modal /
// flow state (parity with internal/tui:359-378). The legend is the
// operator's most discoverable affordance after `?`, so we surface
// the active flow's keys instead of the generic submit/newline
// reminder when something specific is open.
func (m Model) footerHint() string {
	sep := " " + GlyphSeparator + " "
	switch {
	case m.pendingPermission != nil:
		keys := []string{"y allow once", "n deny", "s allow session"}
		if m.pendingPermission.Verb != "" {
			keys = append(keys, "v allow verb")
		}
		keys = append(keys, "t allow tool", "a allow always", "esc deny")
		return "Permission required" + sep + strings.Join(keys, sep)
	case m.pendingElicit != nil:
		if m.pendingElicit.Mode == ElicitURLMode {
			return "MCP elicitation" + sep + "a/enter accept" + sep + "n decline" + sep + "esc cancel"
		}
		return "MCP elicitation" + sep + "tab next field" + sep + "enter submit" + sep + "esc cancel"
	case m.overlay == overlayModelPicker:
		return "Choose a model" + sep + "↑↓ navigate" + sep + "enter accept" + sep + "esc cancel"
	case m.confirmingClear:
		return "Confirm clear?" + sep + "type y / yes to wipe" + sep + "anything else cancels"
	case m.sideAnswer != nil:
		return "Side answer" + sep + "enter/space/esc dismiss"
	case m.state == stateStreaming:
		return "Streaming…" + sep + "esc interrupt" + sep + "enter queues prompt" + sep + "ctrl+c cancel turn"
	case m.palette != nil:
		if m.palette.kind == paletteFile {
			return "Files" + sep + "↑↓ choose" + sep + "enter insert" + sep + "esc cancel"
		}
		return "Slash" + sep + "↑↓ choose" + sep + "enter run" + sep + "tab insert" + sep + "esc cancel"
	}
	return "enter submit" + sep + "ctrl+j newline" + sep + "ctrl+c quit" + sep + "? for more"
}

// renderOverlay renders the currently active enum-driven modal.
// Today that's only the model picker (overlayModelPicker); the
// overlayPermission / overlayElicit enum values are vestigial demo
// hooks from the visual-preview slice — the real permission and
// elicit modals render via renderPermissionModal / renderElicitModal
// against pendingPermission / pendingElicit (see view.go:127-138).
//
// When the host's Agent doesn't implement ModelSwapper, the overlay
// renders a one-line "not available" notice rather than an empty
// frame so the operator knows why the picker is barren.
func (m Model) renderOverlay() string {
	if m.overlay != overlayModelPicker {
		return ""
	}

	width := 64
	if m.width > 0 && width > m.width-4 {
		width = m.width - 4
	}
	if width < 30 {
		width = 30
	}

	title := "Choose a Model"
	var body string
	swapper, ok := m.opts.Agent.(ModelSwapper)
	if !ok {
		body = m.styles.Muted.Render("agent does not implement ModelSwapper")
	} else {
		models := swapper.AvailableModels()
		if len(models) == 0 {
			body = m.styles.Muted.Render("(no models advertised by the agent)")
		} else {
			current := m.displayModelName()
			rows := make([]string, 0, len(models))
			for i, mi := range models {
				disp := mi.Display
				if disp == "" {
					disp = mi.ID
				}
				marker := "  "
				if i == m.modelPickerIdx {
					marker = "> "
				}
				row := marker + disp
				if mi.ID != disp {
					row += m.styles.Muted.Render("  (" + mi.ID + ")")
				}
				if mi.ID == current || disp == current {
					row += "  " + m.styles.Muted.Render("(current)")
				}
				if mi.Description != "" {
					row += "  " + m.styles.Muted.Render(mi.Description)
				}
				if i == m.modelPickerIdx {
					row = m.styles.Accent.Render(row)
				}
				rows = append(rows, row)
			}
			body = strings.Join(rows, "\n")
		}
	}
	footer := "↑↓ choose " + GlyphSeparator + " enter accept " + GlyphSeparator + " esc cancel"

	titleBar := m.styles.ModalTitle.Render(title)
	titleRule := m.styles.ModalBorder.Render(strings.Repeat(GlyphRule, nonNeg(width-lipgloss.Width(titleBar)-3)))
	titleLine := titleBar + " " + titleRule
	footerRule := m.styles.ModalBorder.Render(strings.Repeat(GlyphRule, nonNeg(width-2)))
	footerLine := m.styles.ModalFooter.Render(footer)

	content := lipgloss.JoinVertical(lipgloss.Left,
		titleLine,
		"",
		body,
		"",
		footerRule,
		footerLine,
	)
	return m.styles.ModalBorder.Padding(0, 1).Width(width).Render(content)
}

// sep returns the dim ` · ` separator used in status assembly.
func (m Model) sep() string {
	return m.styles.Muted.Render(" " + GlyphSeparator + " ")
}

// renderTurnFooter emits the per-turn assistant footer (R-USE-1)
// when the message carries Usage / Model / Elapsed metadata. Empty
// string when no metadata is present so seeded or mid-stream
// messages don't get an empty stub.
func (m Model) renderTurnFooter(msg Message) string {
	if msg.Usage == nil && msg.Model == "" && msg.Elapsed == 0 {
		return ""
	}
	parts := []string{}
	if msg.Model != "" {
		parts = append(parts, GlyphModel+" "+msg.Model)
	}
	if msg.Usage != nil {
		parts = append(parts,
			fmt.Sprintf("%s in", humanTokens(msg.Usage.InputTokens)),
			fmt.Sprintf("%s out", humanTokens(msg.Usage.OutputTokens)),
		)
	}
	if msg.CostUSD > 0 {
		parts = append(parts, fmt.Sprintf("$%.4f", msg.CostUSD))
	}
	if msg.Elapsed > 0 {
		parts = append(parts, msg.Elapsed.Round(100_000_000).String())
	}
	return m.styles.Muted.Italic(true).Render("└ " + strings.Join(parts, " "+GlyphSeparator+" "))
}

// renderSideAnswer renders the /btw-style side-answer modal (R-CMD-5).
// The question lands in the title bar (truncated), the answer renders
// through Glamour in the body, the footer shows the dismiss keys.
// Returns empty when no side-answer is active.
func (m *Model) renderSideAnswer() string {
	if m.sideAnswer == nil {
		return ""
	}
	width := 72
	if m.width > 0 && width > m.width-4 {
		width = m.width - 4
	}
	if width < 20 {
		width = 20
	}

	q := m.sideAnswer.Question
	if lipgloss.Width(q) > width-12 {
		q = string([]rune(q)[:width-13]) + GlyphTruncate
	}
	titleBar := m.styles.ModalTitle.Render("by the way: " + q)
	titleRule := m.styles.ModalBorder.Render(strings.Repeat(GlyphRule, nonNeg(width-lipgloss.Width(titleBar)-3)))
	titleLine := titleBar + " " + titleRule

	var body string
	switch {
	case m.sideAnswer.Err != nil:
		body = m.styles.ErrorText.Render(wordWrap(m.sideAnswer.Err.Error(), width-4))
	case strings.TrimSpace(m.sideAnswer.Answer) == "":
		body = m.styles.SystemText.Render("(no answer)")
	default:
		mr := m.ensureMarkdown()
		body = mr.renderMarkdown(m.sideAnswer.Answer)
	}

	footerRule := m.styles.ModalBorder.Render(strings.Repeat(GlyphRule, nonNeg(width-2)))
	footerLine := m.styles.ModalFooter.Render("esc / enter / space dismiss")

	content := lipgloss.JoinVertical(lipgloss.Left,
		titleLine,
		"",
		body,
		"",
		footerRule,
		footerLine,
	)
	return m.styles.ModalBorder.Padding(0, 1).Width(width).Render(content)
}

// renderToast renders the transient wake banner (R-WAKE-1) between
// the input box and the footer. Empty string when no toast is
// active or its TTL has elapsed; the on-render TTL check is the
// secondary defense behind toastClearMsg in case the timer Cmd was
// dropped.
func (m Model) renderToast(width int) string {
	if m.toast == "" || width <= 0 {
		return ""
	}
	if time.Since(m.toastSetAt) > toastTTL {
		return ""
	}
	body := "  " + GlyphWarn + "  " + m.toast
	if w := lipgloss.Width(body); w < width {
		body += strings.Repeat(" ", width-w)
	}
	return m.styles.PermissionWarn.Render(body)
}

// renderPermissionModal renders the permission-approval prompt
// (R-PERM-1 / R-PERM-2). Six decision keys spelled out in the
// footer; the per-tool payload (diff / shell / http / args)
// renders in the body styled per req.DetailKind.
func (m *Model) renderPermissionModal() string {
	req := m.pendingPermission
	if req == nil {
		return ""
	}
	width := 80
	if m.width > 0 && width > m.width-4 {
		width = m.width - 4
	}
	if width < 30 {
		width = 30
	}

	titleBar := m.styles.ModalTitle.Render("Permission required: " + req.ToolName)
	titleRule := m.styles.ModalBorder.Render(strings.Repeat(GlyphRule, nonNeg(width-lipgloss.Width(titleBar)-3)))
	titleLine := titleBar + " " + titleRule

	var lines []string
	if req.Source != "" {
		lines = append(lines, m.styles.Muted.Render("from sub-agent: "+req.Source))
	}
	if req.Verb != "" {
		lines = append(lines, m.styles.Muted.Render("verb: "+req.Verb))
	}
	if req.Detail != "" {
		lines = append(lines, m.renderPermissionDetail(req, width-4))
	}
	body := strings.Join(lines, "\n")

	keys := []string{
		"y allow once",
		"n deny",
		"s allow session",
	}
	if req.Verb != "" {
		keys = append(keys, "v allow verb")
	}
	keys = append(keys, "t allow tool", "a allow always", "esc deny")
	footerLine := m.styles.ModalFooter.Render(strings.Join(keys, " "+GlyphSeparator+" "))
	footerRule := m.styles.ModalBorder.Render(strings.Repeat(GlyphRule, nonNeg(width-2)))

	content := lipgloss.JoinVertical(lipgloss.Left,
		titleLine,
		"",
		body,
		"",
		footerRule,
		footerLine,
	)
	return m.styles.ModalBorder.Padding(0, 1).Width(width).Render(content)
}

// renderPermissionDetail renders the payload through Glamour with
// the right code-fence language tag from req.DetailKind so the
// diff / shell / http blocks get the expected styling.
func (m *Model) renderPermissionDetail(req *PermissionRequest, width int) string {
	if req.Detail == "" {
		return ""
	}
	mr := m.ensureMarkdown()
	switch req.DetailKind {
	case DetailDiff:
		return mr.renderMarkdown("```diff\n" + req.Detail + "\n```")
	case DetailShell:
		return mr.renderMarkdown("```bash\n" + req.Detail + "\n```")
	case DetailHTTP:
		return mr.renderMarkdown("```http\n" + req.Detail + "\n```")
	case DetailArgs:
		return mr.renderMarkdown("```json\n" + req.Detail + "\n```")
	default:
		return wordWrap(req.Detail, width)
	}
}

// renderElicitModal renders an MCP elicit request as either a form
// (per-field) or URL action row (R-ELIC-1 / R-ELIC-2).
func (m *Model) renderElicitModal() string {
	req := m.pendingElicit
	if req == nil {
		return ""
	}
	width := 72
	if m.width > 0 && width > m.width-4 {
		width = m.width - 4
	}
	if width < 30 {
		width = 30
	}

	title := req.Title
	if title == "" {
		title = "MCP request"
	}
	if m.pendingElicitSrv != "" {
		title = m.pendingElicitSrv + " " + GlyphSeparator + " " + title
	}
	titleBar := m.styles.ModalTitle.Render(title)
	titleRule := m.styles.ModalBorder.Render(strings.Repeat(GlyphRule, nonNeg(width-lipgloss.Width(titleBar)-3)))
	titleLine := titleBar + " " + titleRule

	var body, footer string
	if req.Mode == ElicitURLMode {
		body = m.styles.Accent.Render(req.URL)
		if req.Description != "" {
			body = m.styles.Muted.Render(req.Description) + "\n\n" + body
		}
		footer = "a / enter accept " + GlyphSeparator + " n decline " + GlyphSeparator + " esc cancel"
	} else {
		body = m.renderElicitForm(width - 4)
		footer = "tab next " + GlyphSeparator + " shift+tab prev " + GlyphSeparator +
			" space toggle " + GlyphSeparator + " ←/→ enum " + GlyphSeparator +
			" enter submit " + GlyphSeparator + " esc cancel"
	}

	footerRule := m.styles.ModalBorder.Render(strings.Repeat(GlyphRule, nonNeg(width-2)))
	footerLine := m.styles.ModalFooter.Render(footer)
	content := lipgloss.JoinVertical(lipgloss.Left,
		titleLine,
		"",
		body,
		"",
		footerRule,
		footerLine,
	)
	return m.styles.ModalBorder.Padding(0, 1).Width(width).Render(content)
}

// renderElicitForm renders the form's fields one per line, with
// the focused row highlighted in the accent color.
func (m *Model) renderElicitForm(width int) string {
	req := m.pendingElicit
	if req == nil {
		return ""
	}
	var rows []string
	for i, f := range req.Fields {
		row := m.renderElicitField(f, i == m.elicitFieldIdx, width)
		rows = append(rows, row)
	}
	return strings.Join(rows, "\n")
}

// renderElicitField renders one field row (label : value), styling
// the focused one accent-bold. Width is reserved for future
// per-field truncation; unused today but kept on the signature so
// callers don't have to refactor when it lands.
func (m *Model) renderElicitField(f ElicitField, focused bool, _ int) string {
	label := f.Name
	if f.Required {
		label += "*"
	}
	value := m.formatElicitValue(f)
	row := fmt.Sprintf("  %-16s %s", label+":", value)
	rendered := m.styles.AssistantText.Render(row)
	if focused {
		rendered = m.styles.Accent.Render("> " + strings.TrimPrefix(row, "  "))
	}
	// Per-field description (when set) renders on the line below
	// the value in muted text. Parity with internal/tui:191-195
	// so MCP elicits with explanatory help text actually surface
	// it to the operator.
	if f.Description != "" {
		desc := m.styles.Muted.Render("    " + strings.ReplaceAll(f.Description, "\n", " "))
		return rendered + "\n" + desc
	}
	return rendered
}

// formatElicitValue renders the current value of a field for the
// modal — booleans as checkboxes, enums with arrow hints, strings/
// numbers as the literal value or a placeholder.
func (m *Model) formatElicitValue(f ElicitField) string {
	switch f.Type {
	case ElicitFieldBoolean:
		on, _ := m.elicitValues[f.Name].(bool)
		if on {
			return "[✓]"
		}
		return "[ ]"
	case ElicitFieldEnum:
		v, _ := m.elicitValues[f.Name].(string)
		if v == "" && len(f.EnumChoices) > 0 {
			v = f.EnumChoices[0]
		}
		return "‹ " + v + " ›"
	default:
		v, _ := m.elicitValues[f.Name].(string)
		if v == "" {
			return m.styles.Muted.Render("(empty)")
		}
		return v
	}
}

// nonNeg returns x when x > 0, else 0. Used for the modal-width
// rule arithmetic where a too-narrow terminal can produce negative
// repeat counts; strings.Repeat panics on negative counts.
func nonNeg(x int) int {
	if x < 0 {
		return 0
	}
	return x
}

// renderHelpPanel renders the bottom-anchored stacked help panel when
// m.helpOpen is true. Returns empty string when closed so callers can
// conditionally include it without branching on `if helpOpen` in the
// View() composition. Width sets the column width — pass chatWidth in
// sidebar mode and m.width in header mode.
func (m Model) renderHelpPanel(width int) string {
	if !m.helpOpen || width <= 0 {
		return ""
	}
	sections := []struct {
		title string
		keys  [][2]string
	}{
		{"Input", [][2]string{
			{"enter", "submit (or enqueue if a turn is running)"},
			{"ctrl+j", "newline (shift+enter on terminals that distinguish it)"},
			{"?", "toggle this menu"},
		}},
		{"Palettes (live filter)", [][2]string{
			{"/ (at start)", "slash command palette"},
			{"@ (anywhere)", "project file palette"},
			{"↑ / ↓", "navigate palette"},
			{"tab", "complete prefix"},
			{"enter", "insert selection"},
			{"esc", "close palette"},
		}},
		{"Side-answer modal (R-CMD-5)", [][2]string{
			{"/btw <q>", "open a transient Glamour-rendered modal"},
			{"esc / enter / space", "dismiss modal (answer doesn't land in history)"},
		}},
		{"Navigation", [][2]string{
			{"pgup / pgdn", "scroll chat"},
			{"home / end", "top / bottom"},
		}},
		{"Layout & mode", [][2]string{
			{"ctrl+b", "toggle header / sidebar"},
			{"shift+tab", "cycle permission mode"},
		}},
		{"Modals", [][2]string{
			{"ctrl+g", "model picker (when ModelSwapper is wired)"},
			{"esc", "close / cancel any open modal"},
		}},
		{"Interrupt / quit", [][2]string{
			{"esc", "interrupt in-flight turn (doesn't clear queue)"},
			{"ctrl+c, ctrl+d", "exit"},
		}},
	}

	const keyCol = 24
	rule := m.styles.Rule.Render(strings.Repeat(GlyphRule, width))
	title := m.styles.Accent.Render("Help") + "  " +
		m.styles.Muted.Render("(? to close)")

	lines := []string{rule, title}
	for i, sec := range sections {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "  "+m.styles.SidebarHeading.Render(sec.title))
		for _, kv := range sec.keys {
			key := kv[0]
			pad := keyCol - lipgloss.Width(key)
			if pad < 1 {
				pad = 1
			}
			row := "    " + m.styles.AssistantText.Bold(true).Render(key) +
				strings.Repeat(" ", pad) + m.styles.Muted.Render(kv[1])
			lines = append(lines, row)
		}
	}
	lines = append(lines, rule)
	return strings.Join(lines, "\n")
}

// humanTokens formats an integer token count as a short string
// (4096 → "4.1K", 1_234_567 → "1.2M"). Used in both status and per-
// turn footer (R-USE-1 / R-USE-2).
func humanTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
