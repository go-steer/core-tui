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
		// Wrap to (width - prefix) so each emitted line — prefix +
		// wrapped chunk — fits within width. The prior version wrapped
		// the full prefixed line to width, then re-prepended prefix to
		// continuations, overflowing the terminal by len(prefix) cols
		// and causing mid-word hard-wraps in /tools descriptions.
		prefix := roleIndent + leading
		wrapWidth := width - len(prefix)
		if wrapWidth <= 0 {
			wrapWidth = width
		}
		stripped := sl[len(leading):]
		wrapped := wordWrap(stripped, wrapWidth)
		for k, wl := range strings.Split(wrapped, "\n") {
			if k > 0 {
				out.WriteByte('\n')
			}
			out.WriteString(prefix)
			out.WriteString(wl)
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

// chromeWidth is the column the chrome around the chat viewport is
// rendered in: the chat column in sidebar layout — View wraps the
// whole left stack to it so the sidebar is not pushed off the right
// edge — and the full terminal width otherwise. It is what View
// passes to renderFooter / renderHelpPanel / renderPalette and what
// resize() measures them at, so anything outside the render path that
// needs to reason about a panel's geometry (advanceHelp, the tests)
// asks here rather than re-deriving it.
func (m Model) chromeWidth() int {
	if m.effectiveLayout() == StatusSidebar {
		return m.width - sidebarWidth - 3
	}
	return m.width
}

// View composes the full TUI. Returns a tea.View with AltScreen on,
// mouse capture per Options.Mouse, and the terminal's real cursor
// parked on whichever text surface owns input (cursor.go). Layout is
// governed by m.statusLayout (R-USE-2).
func (m Model) View() tea.View {
	if m.width == 0 || m.height == 0 {
		return tea.NewView("")
	}

	chat := m.chatView()
	input := m.renderInputBox()

	// origin is where the input box's top-left cell lands in the
	// composed frame. The textarea reports a cursor relative to
	// ITSELF, so the cursor path (cursor.go) needs this to translate
	// it into absolute frame coordinates. It is recorded here, by the
	// code doing the stacking, rather than re-derived afterwards:
	// which parts get emitted is a run-time decision (help panel,
	// palette, toast are all conditional) and it differs per layout.
	var origin inputOrigin

	var body string
	switch m.effectiveLayout() {
	case StatusSidebar:
		// Footer wraps to the chat column, NOT to m.width — otherwise
		// the left block grows wider than the chat column and the
		// sidebar gets pushed off the right edge of the terminal.
		chatWidth := m.chromeWidth()
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
		// The left column starts at column 0 of the frame, so the
		// input box's x origin is 0 — but it is only chatWidth wide,
		// not m.width. Row origin is the stacked height of everything
		// JoinVertical puts above it.
		origin = inputOrigin{x: 0, y: stackedHeight(leftParts)}
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
		// Header layout: the input box spans the full width at column
		// 0, one row below the header + chat + any open panels.
		origin = inputOrigin{x: 0, y: stackedHeight(parts)}
		parts = append(parts, input)
		if t := m.renderToast(m.width); t != "" {
			parts = append(parts, t)
		}
		parts = append(parts, footer)
		body = lipgloss.JoinVertical(lipgloss.Left, parts...)
	}

	// Overlay any active modal centered over the body. Permission
	// prompts render INLINE inside renderInProgress (chat flow)
	// by default — preserves the assistant text + tool-call
	// context the operator is approving. Hosts that prefer the
	// centered modal flip Options.PermissionLayout = PermissionOverlay.
	//
	// modalFrame retains the front-most modal's rendered block. It is
	// what the cursor path measures to re-derive the origin
	// lipgloss.Place gave it — a centered block's top-left cell is a
	// function of its own size, so it has to be computed, not guessed.
	var modalFrame string
	switch {
	case m.pendingPermission != nil && m.opts.PermissionLayout == PermissionOverlay:
		modalFrame = m.renderPermissionModal()
		body = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modalFrame)
	case m.pendingElicit != nil:
		modalFrame = m.renderElicitModal()
		body = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modalFrame)
	case m.pendingForm != nil:
		// Embedded huh.Form (e.g. /pricing set) takes priority
		// over all other overlays — its keystrokes are routed
		// in Update before any cascade runs (see Update). huh's
		// View returns a string (not bubbletea v2's tea.View),
		// so wrap directly.
		formView := m.pendingForm.View()
		modalFrame = m.styles.ModalBorder.Padding(1, 2).Render(formView)
		body = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modalFrame)
	case m.sideAnswer != nil:
		modalFrame = m.renderSideAnswer()
		body = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modalFrame)
	case m.overlayStack.HasDialogs():
		modalFrame = m.overlayStack.Render(m.width, &m)
		body = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modalFrame)
	}

	// Clamp the composed frame to the terminal (issue #102). Every
	// join above — JoinVertical, JoinHorizontal, lipgloss.Place,
	// plain concatenation — can overflow, and nothing downstream
	// catches it: Bubble Tea happily writes 122 columns into a
	// 100-column terminal and, when the frame is too TALL, drops
	// the top lines so the operator silently loses the header.
	// This is the last thing View does, deliberately: it is a
	// safety net under the layout, not part of it.
	body = clipFrame(body, m.width, m.height)

	v := tea.NewView(body)
	v.AltScreen = true
	v.BackgroundColor = nil // respect the terminal's own background
	// Park the terminal's real cursor on whichever text surface owns
	// input (issue #105). Computed AFTER clipFrame so it can never
	// point at a row or column the clamp just removed. Nil hides the
	// cursor, which is what we want whenever no surface owns it.
	v.Cursor = m.frameCursor(origin, modalFrame)
	// Cell-motion mouse capture so the wheel scrolls the viewport.
	// Operators who want native terminal text-select hold Shift to
	// bypass capture (matches internal/tui + Claude Code). Hosts
	// can disable capture entirely via Options.Mouse (set to a
	// pointer to false); the /mouse slash toggles it at runtime.
	if m.opts.Mouse != nil && !*m.opts.Mouse {
		v.MouseMode = tea.MouseModeNone
	} else {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

// clipFrame clamps a composed frame to a width x height terminal.
// It is the final post-pass in View (issue #102) and exists because
// the composition above has no single choke point: chrome is built
// with lipgloss.JoinVertical / JoinHorizontal / Place and plain
// concatenation, so overflow is possible at every join and nothing
// caught it at the end.
//
// Width: every line is truncated with ansi.Truncate, which is
// escape-aware — it counts display cells, keeps the SGR state
// intact, and re-emits a reset. Byte-level slicing would cut an
// escape sequence in half and spill the raw bytes into the
// terminal, so it is never used here. Width comparisons likewise go
// through ansi.StringWidth, not len: a frame full of box-drawing
// glyphs and CJK is not one byte per column.
//
// Height: the FIRST height lines are kept and the rest dropped.
// That choice is deliberate. Keeping the head is deterministic and
// trivially explainable, which is what a safety net needs to be —
// and the head is where the status header lives, the one row whose
// silent disappearance (Bubble Tea drops from the top when it
// overflows) is hardest for an operator to notice. It is not a
// layout policy: the row BUDGET is issue #103's job, and once that
// lands this cap should never fire at all. If it does fire, resize
// gave the frame more rows than the terminal has, and that is the
// bug to fix — not the tie-break here.
//
// A non-positive width or height means we don't know the terminal
// geometry (pre-WindowSizeMsg, or a host reporting nonsense), so
// the body passes through untouched rather than being clipped to
// nothing. A frame that already fits comes back byte-identical.
func clipFrame(body string, width, height int) string {
	if width <= 0 || height <= 0 {
		return body
	}
	lines := strings.Split(body, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	changed := len(lines) != strings.Count(body, "\n")+1
	for i, line := range lines {
		if ansi.StringWidth(line) <= width {
			continue
		}
		// No ellipsis tail: this is a clamp, not a summary. An
		// added "…" would itself consume a column and would read
		// as intentional content in a frame that is simply too
		// wide.
		lines[i] = ansi.Truncate(line, width, "")
		changed = true
	}
	if !changed {
		return body
	}
	return strings.Join(lines, "\n")
}

// chatMinHeight is the floor resize will shrink the viewport to.
// Below three rows the chat column stops being able to show a
// message at all, so we stop subtracting and let clipFrame trim the
// overflow instead (see clipFrame's note on who owns the budget).
const chatMinHeight = 3

// resize recomputes the viewport and textarea dimensions from the
// current width / height + status layout. Called after WindowSizeMsg
// and after the user toggles StatusHeader <-> StatusSidebar.
//
// The row budget here is the mirror image of what View composes: the
// terminal's m.height rows are handed out to everything View joins
// around the chat viewport, and the viewport gets what is left. The
// rule the whole thing follows is *measure, never assume* — each term
// renders the very same string View will render and takes its
// lipgloss.Height. Hardcoding a row count was how issue #103
// happened: the header was pinned at 2 rows even though renderHeader
// word-wraps to as many as 6 on a narrow terminal, so the frame
// overflowed and clipFrame ate the footer.
//
// The allocation itself — which element yields to which when the rows
// run out — lives in allocateChrome (budget.go) with the priority
// order and its justification. resize is the part that has to happen
// first: the widths, because renderInputBox reads
// m.viewport.Width() and every wrap-sensitive measurement depends on
// the width it is rendered at.
//
// Only siblings of the viewport are charged. Anything that renders
// INSIDE the viewport (the prompt queue panel, the spinner line, the
// in-progress assistant block) is already paid for out of the
// viewport's own rows and must not be subtracted again — see the
// queue note in budget.go.
func (m *Model) resize() {
	if m.width == 0 || m.height == 0 {
		return
	}

	layout := m.effectiveLayout()
	chatWidth := m.width
	if layout == StatusSidebar {
		chatWidth = m.width - sidebarWidth - 3
	}
	if chatWidth < 1 {
		chatWidth = 1
	}

	// Chrome wrap width must match the column each element will
	// actually render in — chatWidth in sidebar mode (View wraps the
	// whole left stack to the chat column so the sidebar isn't pushed
	// off the right edge), m.width in header mode.
	chromeWidth := m.width
	if layout == StatusSidebar {
		chromeWidth = chatWidth
	}

	// Set the widths before measuring: renderInputBox reads
	// m.viewport.Width(), and every wrap-sensitive row count in the
	// budget depends on the width it is rendered at.
	//
	// The viewport's width is what a transcript ROW renders at, which
	// is the chat column less the selection gutter (issue #152). The
	// gutter is reserved on every row rather than appearing under the
	// cursor, so that moving the cursor cannot re-wrap the item it
	// lands on; chatView adds it back as it draws, and the block it
	// returns is chatWidth wide again.
	m.viewport.SetWidth(atLeast(chatWidth-chatGutterWidth, 1))
	m.input.SetWidth(chatWidth - 2) // leave room for the input border

	m.chrome = m.allocateChrome(layout, chromeWidth)
	m.viewport.SetHeight(m.chrome.chat)
}

// syncInputHeight clamps the textarea's height to its current line
// count, between textareaMinHeight and whatever the last chrome
// budget can afford (budget.go). Returns true when the height changed
// so callers can trigger a layout reconciliation (resize + refresh +
// bottom-snap if pinned).
//
// Called from every keystroke-forward + after any programmatic
// input mutation so multi-line paste / typed newlines grow the
// box visibly, and Ctrl+U / Esc-out-of-history shrinks it back.
//
// It clamps to the budget rather than straight to textareaMaxHeight
// so that it agrees with the resize() the caller is about to run. The
// two used to disagree — resize reset the box to its minimum on every
// call, so the grow computed here was discarded before it could be
// rendered and the box never appeared (issue #121). Asking here for a
// height that resize would only take back again would leave the same
// disagreement in place, one keystroke at a time.
func (m *Model) syncInputHeight() bool {
	desired := m.input.LineCount()
	if desired < textareaMinHeight {
		desired = textareaMinHeight
	}
	if desired > textareaMaxHeight {
		desired = textareaMaxHeight
	}
	if ceiling := m.chrome.inputMax; ceiling > 0 && desired > ceiling {
		desired = atLeast(ceiling, textareaMinHeight)
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
	// An explicit jump to the tail is also a re-arm: the operator
	// asked to see the newest content, so subsequent repaints should
	// keep them there.
	m.follow = true
	m.refreshViewport()
	m.chatGotoBottom()
}

func (m *Model) refreshViewport() {
	if m.width == 0 {
		return
	}
	// History rows are NOT assembled here. They are rendered on
	// demand by the walk in chatView, out of listCache, so this
	// function costs what the tail costs rather than what the
	// transcript costs — that is issue #161, and it is why there is
	// no builder in this function any more.
	//
	// The tail is rebuilt BEFORE the reflow because it is a row like
	// any other: a following operator's window is measured backward
	// from the last row, so a tail that grew by three lines this
	// chunk changes which history row the window starts at, and the
	// reflow has to be told which rows those are.
	m.chatTail = m.buildChatTail()
	m.chatRule, m.chatRuleWidth = m.buildChatRule(), m.viewport.Width()

	// Issue #104: a width change reflows the on-screen rows only, so
	// anything the operator can see is at the current wrap width by
	// the time it paints — including rows a mid-warm scroll just
	// exposed. No-op (one bool read) when no resize is in flight.
	m.reflowVisible()

	// Preserve scroll position across re-renders: only auto-scroll
	// to the bottom when the operator is following the tail. If
	// they've scrolled up to read backlog, an incoming stream chunk
	// must not yank them back (parity with internal/tui:512).
	//
	// m.follow, not a live at-bottom test: sampling here read the
	// geometry AFTER a resize had already changed it, so a shrunken
	// viewport reported "not at bottom" and follow was lost mid-
	// stream (issue #93).
	//
	// The clamp is what the flat path got for free from SetContent:
	// rows can vanish under the offset (a /clear, a session switch,
	// a turn finalizing and folding the tail into history), and an
	// offset past the end would draw an empty transcript.
	m.clampChatOffset()
	// The cursor is an index into the same rows and goes stale the
	// same ways (issue #152).
	m.clampChatSelection()
	if m.follow {
		m.chatGotoBottom()
	}
	m.warmChatWindow()
}

// syncFollow re-derives the follow flag from where the viewport
// actually sits. Call it right after a user-driven scroll (a key or
// wheel event forwarded to the viewport) and only then: the sample is
// only meaningful while the geometry is the one the operator scrolled
// in. Scrolling up drops follow; scrolling back down to the bottom
// re-arms it.
func (m *Model) syncFollow() {
	m.follow = m.chatAtBottom()
}

// turnInFlight reports whether there is output in flight for the
// in-progress block to paint — accumulated assistant text, a spinner
// verb line, or both.
//
// Two paths answer that question differently, and the difference is
// issue #135. The per-turn Run path carries it in m.state, which
// submitTurn sets to stateStreaming. The LiveAgent path (#22) never
// calls submitTurn: it is driven from applyStreamChunk, which
// accumulates m.inProgressText and flips m.spinnerActive but leaves
// m.state at stateIdle. A gate that asks only about m.state therefore
// answers "nothing in flight" for the whole of an autonomous stretch,
// and the operator watches an idle-looking TUI until finalizeTurn
// commits the message in one lump — the opposite of what the
// capability exists to provide.
//
// This is a method rather than an inlined condition because
// renderInProgress asks the question twice: once to bail out early,
// once to decide whether to emit the body. Those two copies drifting
// apart is the actual defect shape — widening only the outer one
// leaves the body still skipping the text, for the same invisible
// result.
//
// Deliberately NOT "set stateStreaming on the live path too". Nine
// other reads of stateStreaming gate interrupt and submit behaviour
// (update.go, slash_builtin.go), and cancelTurn is documented as
// non-nil while state == stateStreaming (model.go) while the live
// path has no cancelTurn at all. Widening the render gate keeps
// m.state meaning "a Run-driven turn is in flight"; teaching the live
// path to adopt that state is a separate decision with its own blast
// radius.
func (m Model) turnInFlight() bool {
	return m.state == stateStreaming || (m.liveMode && m.spinnerActive)
}

// renderInProgress returns the live block at the bottom of the chat
// while a turn is streaming: the accumulated assistant text rendered
// through Glamour (R-CHAT-4), followed by the spinner verb line
// (R-CHAT-3) and the prompt queue panel (R-CHAT-10). Empty string
// when no turn is in flight AND the queue is empty.
func (m *Model) renderInProgress() string {
	if !m.turnInFlight() && len(m.queue) == 0 {
		return ""
	}
	var parts []string
	if m.turnInFlight() {
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
		// Inline permission prompt (default layout): renders right
		// under the streaming assistant text so the operator sees
		// what's being approved in context. Suppresses the spinner
		// because we're waiting on operator, not on model.
		if m.pendingPermission != nil && m.opts.PermissionLayout == PermissionInline {
			parts = append(parts, m.renderPermissionInline())
		} else {
			parts = append(parts, m.renderSpinnerLine())
		}
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

// renderSpinnerLine renders the rotating cognition verb (R-CHAT-3)
// prefixed with a pre-rendered Braille spinner glyph cycling
// through a 10-frame color ramp between theme.Primary and
// theme.Secondary (agentic-tui skill §7). The glyph reads as
// "alive" without distracting from the verb.
//
// A muted elapsed-time suffix trails the verb once the turn is older
// than turnElapsedFloor (issue #111): "alive" answers whether
// anything is happening, the readout answers whether it is taking
// too long. It rides the existing spinner repaint (spinnerTickMsg →
// markViewportDirty), so there is no new timer.
//
// GlyphTruncate is the single authority for the trailing affordance,
// so the verb is normalized through trimTrailingEllipsis first — a
// phrase that punctuates itself would otherwise render "Thinking...…"
// (issue #141). That applies to Options.ThinkingPhrases /
// Options.WorkingPhrases exactly as it does to the built-in pools.
func (m *Model) renderSpinnerLine() string {
	pool := m.thinkingPhrases()
	if m.toolActive {
		pool = m.workingPhrases()
	}
	if len(pool) == 0 {
		return ""
	}
	verb := trimTrailingEllipsis(pool[m.thinkingIdx%len(pool)])
	glyph := m.renderBrailleFrame(m.thinkingIdx)
	body := m.styles.Muted.Italic(true).Render(verb+GlyphTruncate) + m.renderTurnElapsed()
	if glyph == "" {
		return body
	}
	return glyph + " " + body
}

// trimTrailingEllipsis strips one trailing ellipsis — ASCII "...",
// the "…" glyph, or any mix and repetition of the two, plus the
// whitespace around it — from a spinner verb, so the caller can
// append GlyphTruncate without double-punctuating (issue #141).
//
// Only a TRAILING run is considered: "Wait... then more" keeps its
// mid-phrase ellipsis. A run has to actually read as an ellipsis to
// be removed (three or more dots, or at least one "…" glyph), so a
// phrase ending in a single sentence period keeps it. The function is
// idempotent, and safe on an empty or all-punctuation phrase — both
// normalize to "", leaving GlyphTruncate alone on the line.
func trimTrailingEllipsis(s string) string {
	trimmed := strings.TrimRight(s, ". \t"+GlyphTruncate)
	run := s[len(trimmed):]
	if !strings.Contains(run, GlyphTruncate) && strings.Count(run, ".") < 3 {
		// Not an ellipsis (bare period, "..", trailing blanks only).
		return strings.TrimRight(s, " \t")
	}
	return strings.TrimRight(trimmed, " \t")
}

// renderMessage renders a single Message row with the correct glyph
// + style for its Role (style.md §2 + §4). Output is word-wrapped to
// the viewport width so narrow terminals don't run text off-screen.
func (m Model) renderMessage(msg Message) string {
	width := m.viewport.Width()
	switch msg.Role {
	case RoleUser:
		// User prompts render as a "card" so they stand out from
		// assistant text and tool calls: full-width background
		// tint on every wrapped line + bold body. The ❯ prefix
		// stays in its UserPrefix style (bold blue) so the visual
		// anchor is unchanged; everything else just gets brighter.
		//
		// AutoContinue-synthesized turns (issue #9) get a distinct
		// ↻ glyph and a muted body — same card shape so they line
		// up with operator prompts, but visually quieter so the
		// operator can scan for what THEY typed.
		body := wordWrapIndent(msg.Display(), width-2, "  ")
		bodyStyle := m.styles.UserText.Bold(true)
		prefixStyle := m.styles.UserPrefix
		glyph := GlyphUserPrompt
		if msg.AutoContinue {
			glyph = GlyphAutoContinue
			bodyStyle = m.styles.Muted
			prefixStyle = m.styles.Muted
		}
		bg := lipgloss.NewStyle().Background(m.styles.Theme.BgElevated)
		prefixStyled := bg.Inherit(prefixStyle).Render(glyph + " ")
		lines := strings.Split(body, "\n")
		for i, line := range lines {
			// Pad each line to full width so the background tint
			// reads as a continuous strip across the chat column.
			padTo := width
			if i == 0 {
				padTo = width - lipgloss.Width(prefixStyled)
			}
			pad := padTo - lipgloss.Width(line)
			if pad < 0 {
				pad = 0
			}
			styled := bg.Inherit(bodyStyle).Render(line + strings.Repeat(" ", pad))
			if i == 0 {
				lines[i] = prefixStyled + styled
			} else {
				lines[i] = styled
			}
		}
		return strings.Join(lines, "\n")
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
	case RoleNotice:
		// Host-initiated row (issue #30). ◇ glyph + NoticeText
		// (muted, non-italic). Distinct from RoleSystem's ℹ +
		// italic so operators glance-tell "framework speaking"
		// from "agent system response."
		return m.styles.NoticeText.Render(wordWrapIndent("◇  "+msg.Display(), width, "   "))
	case RoleError:
		// Push-mode structured error (issue #40 / spec §2.6): when
		// a turn-error event lands, the handler attaches a TurnError
		// payload to the Message so we can render the richer
		// "header / message / hint / retryable" block instead of a
		// flat text row. Legacy error rows (TurnError nil) keep the
		// simple "⚠ <text>" rendering.
		if msg.TurnError != nil {
			return m.renderTurnErrorBlock(*msg.TurnError, width)
		}
		return m.styles.ErrorText.Render(wordWrapIndent(GlyphWarn+"  "+msg.Display(), width, "   "))
	case RoleTool:
		// Per-tool rendering strategy (toolrender.go). The factory
		// dispatches by tool name; bash gets accent coloring, file
		// tools get an underlined path, everything else falls back
		// to the generic muted-args layout.
		//
		// Glyph: ▶ (active) vs › (done). Active = this is the
		// most-recent tool call that hasn't been followed by any
		// text yet (m.activeToolID == msg.ID). Both glyphs are
		// text-class so they take the foreground color cleanly
		// — no emoji-default ovverride.
		glyph := GlyphTool
		nameStyle := m.styles.ToolHead
		if msg.ID != 0 && msg.ID == m.activeToolID {
			glyph = GlyphToolActive
			// Active call: keep the same color as inactive but
			// add italic so the row visually pulses without
			// requiring per-frame ticks.
			nameStyle = m.styles.ToolHead.Italic(true)
		}
		head := nameStyle.Render(glyph + " " + msg.ToolName)
		return toolRendererFor(msg.ToolName).RenderCall(msg, head, width, m.styles)
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
	// Wordmark · [agent-identity ·] model. The cursor block that
	// used to sit between wordmark and model is gone — the
	// AsyncSlashProvider work added a sticky "running" segment
	// on the right of the status line, which now carries the
	// "alive, accepting input" affordance the cursor used to
	// (and renders only when work is actually happening, which
	// is the more useful signal).
	//
	// When Branding.AgentIdentity is set (typically from a host
	// like core-agent's cfg.Agent.DisplayName) and differs from
	// the wordmark, the identity sits between the wordmark and
	// the model so the operator can tell which agent deployment
	// they're talking to in multi-window setups.
	parts := []string{m.styles.RenderWordmark(m.wordmark())}
	if id := m.opts.Branding.AgentIdentity; id != "" && id != m.wordmark() {
		parts = append(parts,
			m.sep(),
			m.styles.AgentIdentity.Render(id),
		)
	}
	parts = append(parts,
		m.sep(),
		m.styles.AgentIdentity.Render(GlyphModel+" "+m.displayModelName()),
	)
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
	// Issue #13: persistent indicator for an in-flight async slash.
	// Always visible on the status header so the operator can verify
	// the slash dispatched and is still working — complements the
	// transient toast banner above the chat (which may have already
	// cleared by the time the eye reaches it on a long call).
	if m.inFlightSlash != nil {
		parts = append(parts,
			m.sep(),
			m.styles.Accent.Render("▸ /"+m.inFlightSlash.name+" running"),
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
//
// The border doubles as the focus indicator (issue #151): it drops to
// the quiet border token while the transcript holds the keyboard.
// That is the theme's own vocabulary — BorderActive is documented as
// "focused input / open dialog" and BorderQuiet as the passive edge —
// so the indicator needs no new style field and follows every theme
// for free. It is the second of three signals, alongside the footer
// legend and the hardware cursor setFocus takes away: a mode reachable
// in one keystroke has to be visible without one.
func (m Model) renderInputBox() string {
	width := m.viewport.Width()
	if width <= 0 {
		width = m.width
	}
	border := m.styles.InputBorderTop
	if m.focus != focusInput {
		border = m.styles.Rule
	}
	top := border.Render(strings.Repeat(GlyphRule, width))
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
	case m.confirmingClear:
		return "Confirm clear?" + sep + "type y / yes to wipe" + sep + "anything else cancels"
	case m.sideAnswer != nil:
		if m.modalScroll != nil && m.modalScroll.overflows() {
			return "Side answer" + sep + "↑↓ scroll" + sep + "enter/space/esc dismiss"
		}
		return "Side answer" + sep + "enter/space/esc dismiss"
	case m.focus == focusTranscript:
		// Above the streaming arm on purpose (issue #151). Reading
		// back through the transcript while a turn runs is the case
		// this mode exists for, so it is the last moment to drop the
		// indicator — and the streaming legend would be wrong here
		// anyway: from focus mode esc returns the keyboard and enter
		// queues nothing.
		return "Transcript" + sep + "↑↓ scroll" + sep + "pgup/pgdn page" + sep +
			"g/G top/bottom" + sep + "tab/esc composer"
	case m.state == stateStreaming:
		return "Streaming…" + sep + "esc interrupt" + sep + "enter queues prompt" + sep + "ctrl+c cancel turn"
	case m.palette != nil:
		if m.palette.kind == paletteFile {
			return "Files" + sep + "↑↓ choose" + sep + "enter insert" + sep + "esc cancel"
		}
		return "Slash" + sep + "↑↓ choose" + sep + "enter run" + sep + "tab insert" + sep + "esc cancel"
	}
	hint := m.newlineHint
	if hint == "" {
		hint = "ctrl+j"
	}
	return "enter submit" + sep + hint + " newline" + sep + "ctrl+c quit" + sep + "? for more"
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
		// Wrapped to the modal, not to the chat column — see
		// ensureModalMarkdown.
		mr := m.ensureModalMarkdown(width - 4)
		body = strings.TrimRight(mr.renderMarkdown(m.sideAnswer.Answer), "\n")
	}

	// A /btw answer is a full Glamour document and routinely runs
	// past the bottom of the screen. Window it (two columns reserved
	// on the right for the scrollbar) and record the geometry so the
	// keystroke / wheel handlers can clamp against it.
	lines := strings.Split(body, "\n")
	view := modalBodyHeight(m.height, modalChromeRows)
	sc := m.scroll()
	sc.measure(len(lines), view)
	body = strings.Join(scrollView(m.styles, lines, nonNeg(width-4), view, sc.offset), "\n")

	dismiss := "esc / enter / space dismiss"
	if hint := scrollHint(sc.overflows()); hint != "" {
		dismiss = hint + "  " + GlyphSeparator + "  " + dismiss
	}
	footerRule := m.styles.ModalBorder.Render(strings.Repeat(GlyphRule, nonNeg(width-2)))
	footerLine := m.styles.ModalFooter.Render(dismiss)

	content := fitModalContent(width, m.height, titleLine, body, footerRule, footerLine)
	return m.styles.ModalBorder.Padding(0, 1).Width(width).Render(content)
}

// renderToast renders the transient wake banner (R-WAKE-1) between
// the input box and the footer. Empty string when no toast is
// active or its TTL has elapsed; the on-render TTL check is the
// secondary defense behind toastClearMsg in case the timer Cmd was
// dropped.
//
// Sticky exception (issue #13): when an async slash is in flight
// the toast is the operator's only in-chat "still running" signal,
// so the render-time TTL check is bypassed. Mirrors the
// toastClearMsg handler's sticky guard — without this bypass, a
// >4s slash would lose the toast at the secondary check even
// though the slash hasn't completed and toastClearMsg correctly
// left the field set.
func (m Model) renderToast(width int) string {
	if m.toast == "" || width <= 0 {
		return ""
	}
	if m.inFlightSlash == nil && time.Since(m.toastSetAt) > toastTTL {
		return ""
	}
	body := "  " + GlyphWarn + "  " + m.toast
	if w := lipgloss.Width(body); w < width {
		body += strings.Repeat(" ", width-w)
	}
	return m.styles.PermissionWarn.Render(body)
}

// permissionKeyHint builds the "y allow once · n deny · …" key
// legend for both permission renderers. Spaces WITHIN a key+action
// pair are non-breaking (U+00A0) so wordWrap's space-break never
// splits "a allow always" mid-pair onto two lines. The " · "
// separator stays breakable so the legend wraps cleanly between
// keys instead.
func permissionKeyHint(verb string) string {
	const nbsp = " "
	pair := func(key, action string) string {
		return key + nbsp + strings.ReplaceAll(action, " ", nbsp)
	}
	keys := []string{
		pair("y", "allow once"),
		pair("n", "deny"),
		pair("s", "allow session"),
	}
	if verb != "" {
		keys = append(keys, pair("v", "allow verb"))
	}
	keys = append(keys,
		pair("t", "allow tool"),
		pair("a", "allow always"),
		pair("esc", "deny"),
	)
	return strings.Join(keys, " "+GlyphSeparator+" ")
}

// renderPermissionInline renders the permission prompt as a
// block inside the chat viewport flow (PermissionInline layout).
// Uses a left rule (│) gutter to set it apart visually from the
// surrounding chat while keeping the prompt in the natural scroll
// position right under the tool call that triggered it.
//
// Same content as renderPermissionModal — header + detail + key
// hints — but without the centered frame / dimmed surroundings.
// Keystroke dispatch (y/n/s/v/t/a/esc) is shared; this is purely
// the visual path.
func (m *Model) renderPermissionInline() string {
	req := m.pendingPermission
	if req == nil {
		return ""
	}
	width := m.viewport.Width()
	if width <= 0 {
		width = 80
	}
	const gutter = "│ "
	bodyWidth := width - lipgloss.Width(gutter) - 1
	if bodyWidth < 20 {
		bodyWidth = 20
	}

	var lines []string
	header := m.styles.Accent.Render("⚠ Permission required: " + req.ToolName)
	lines = append(lines, header)
	if req.Source != "" {
		lines = append(lines, m.styles.Muted.Render("from sub-agent: "+req.Source))
	}
	if req.Verb != "" {
		lines = append(lines, m.styles.Muted.Render("verb: "+req.Verb))
	}
	if req.Detail != "" {
		lines = append(lines, "", m.renderPermissionDetail(req, bodyWidth))
	}
	lines = append(lines, "", m.styles.Muted.Render(permissionKeyHint(req.Verb)))

	// Prefix each line with the left-rule gutter (in accent so the
	// block reads as a focused affordance, not a quiet quote).
	rule := m.styles.Accent.Render("│ ")
	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		// Wrap each source line at bodyWidth so long shell commands
		// fold cleanly under the gutter.
		wrapped := strings.Split(wordWrap(line, bodyWidth), "\n")
		for j, wl := range wrapped {
			if j > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(rule)
			b.WriteString(wl)
		}
	}
	return b.String()
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

	hint := permissionKeyHint(req.Verb)
	footerRule := m.styles.ModalBorder.Render(strings.Repeat(GlyphRule, nonNeg(width-2)))

	// Window the body. A large diff or a long shell script used to
	// run off the bottom of the screen with no way to reach the
	// rest — and this is the one modal where reading all of it
	// before answering actually matters. The key hint wraps on
	// narrow terminals, so measure it rather than assuming one row.
	body := strings.Join(lines, "\n")
	bodyLines := strings.Split(body, "\n")
	chrome := modalChromeRows - 1 + wrappedRows(hint, width-2)
	view := modalBodyHeight(m.height, chrome)
	sc := m.scroll()
	sc.measure(len(bodyLines), view)
	body = strings.Join(scrollView(m.styles, bodyLines, nonNeg(width-4), view, sc.offset), "\n")
	if sc.overflows() {
		hint = scrollHint(true) + " " + GlyphSeparator + " " + hint
	}
	footerLine := m.styles.ModalFooter.Render(hint)

	content := fitModalContent(width, m.height, titleLine, body, footerRule, footerLine)
	return m.styles.ModalBorder.Padding(0, 1).Width(width).Render(content)
}

// renderPermissionDetail renders the payload styled per
// DetailKind. Shell + JSON go through a plain bordered block
// (Glamour adds document margins + code-fence frames that don't
// compose cleanly inside the modal frame — the closing bar ends
// up indented and pushed off the right). Diff still rides
// Glamour because unified-diff syntax highlighting is the whole
// point of the diff fence.
func (m *Model) renderPermissionDetail(req *PermissionRequest, width int) string {
	if req.Detail == "" {
		return ""
	}
	switch req.DetailKind {
	case DetailDiff:
		mr := m.ensureMarkdown()
		return strings.TrimSpace(mr.renderMarkdown("```diff\n" + req.Detail + "\n```"))
	case DetailShell:
		return renderShellDetail(req.Detail, width, m.styles)
	case DetailHTTP:
		return renderShellDetail(req.Detail, width, m.styles)
	case DetailArgs:
		return renderArgsDetail(req.Detail, width, m.styles)
	default:
		return wordWrap(req.Detail, width)
	}
}

// renderShellDetail formats a bash / HTTP command as `$ <cmd>`
// in the accent color, word-wrapped to width with continuation
// indent so multi-line commands stay aligned under the `$`.
// Avoids Glamour's code-fence pipeline so the modal frame stays
// composed cleanly.
func renderShellDetail(text string, width int, styles Styles) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	prefix := styles.Accent.Render("$ ")
	// width-2 because of the "$ " prefix; continuation indent
	// 2 spaces so wrapped lines hang under the start of the
	// command.
	body := wordWrapIndent(text, width-2, "  ")
	return prefix + styles.AssistantText.Render(body)
}

// renderArgsDetail formats a JSON / key=value args blob plainly,
// muted, word-wrapped. Same rationale as renderShellDetail —
// keep the modal frame composition clean.
func renderArgsDetail(text string, width int, styles Styles) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return styles.Muted.Render(wordWrap(text, width))
}

// elicitModalWidth is the elicit modal's column count: 72 clamped to
// the terminal, floored at 30. Shared with the cursor path
// (cursor.go), which has to lay out the focused field row at exactly
// the width the renderer used.
func (m Model) elicitModalWidth() int {
	width := 72
	if m.width > 0 && width > m.width-4 {
		width = m.width - 4
	}
	if width < 30 {
		width = 30
	}
	return width
}

// renderElicitModal renders an MCP elicit request as either a form
// (per-field) or URL action row (R-ELIC-1 / R-ELIC-2).
func (m *Model) renderElicitModal() string {
	req := m.pendingElicit
	if req == nil {
		return ""
	}
	width := m.elicitModalWidth()

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

	var (
		bodyLines []string
		focusLine = -1 // -1 = no cursor to keep on screen (URL mode)
		footer    string
	)
	if req.Mode == ElicitURLMode {
		body := m.styles.Accent.Render(req.URL)
		if req.Description != "" {
			body = m.styles.Muted.Render(req.Description) + "\n\n" + body
		}
		bodyLines = strings.Split(body, "\n")
		footer = "a / enter accept " + GlyphSeparator + " n decline " + GlyphSeparator + " esc cancel"
	} else {
		bodyLines, focusLine = m.elicitFormLines(width - 4)
		footer = "tab next " + GlyphSeparator + " shift+tab prev " + GlyphSeparator +
			" space toggle " + GlyphSeparator + " ←/→ enum " + GlyphSeparator +
			" enter submit " + GlyphSeparator + " esc cancel"
	}

	// Window the field list. A form with more fields than the
	// terminal has rows used to bury everything past the fold — and
	// Tab could walk the focus down into the invisible part. The
	// offset follows the focused field (listWindow) while arrow /
	// page keys and the wheel move it directly.
	chrome := modalChromeRows - 1 + wrappedRows(footer, width-2)
	view := modalBodyHeight(m.height, chrome)
	sc := m.scroll()
	sc.measure(len(bodyLines), view)
	if focusLine >= 0 {
		sc.follow(focusLine)
	}
	body := strings.Join(scrollView(m.styles, bodyLines, nonNeg(width-4), view, sc.offset), "\n")
	if sc.overflows() {
		footer = scrollHint(true) + " " + GlyphSeparator + " " + footer
	}

	footerRule := m.styles.ModalBorder.Render(strings.Repeat(GlyphRule, nonNeg(width-2)))
	footerLine := m.styles.ModalFooter.Render(footer)
	content := fitModalContent(width, m.height, titleLine, body, footerRule, footerLine)
	return m.styles.ModalBorder.Padding(0, 1).Width(width).Render(content)
}

// elicitFormLines renders the form's fields, one per terminal row
// with the focused row highlighted in the accent color, and returns
// the row index the focused field starts on. The caller needs both to
// window a form taller than the modal while keeping the Tab cursor on
// screen — a field row can be two lines when it carries a
// description, so "field index" and "row index" differ.
func (m *Model) elicitFormLines(width int) (lines []string, focus int) {
	req := m.pendingElicit
	if req == nil {
		return nil, 0
	}
	for i, f := range req.Fields {
		focused := i == m.elicitFieldIdx
		if focused {
			focus = len(lines)
		}
		lines = append(lines, strings.Split(m.renderElicitField(f, focused, width), "\n")...)
	}
	return lines, focus
}

// elicitFieldRow lays out one "  label:           value" form row.
// Factored out of renderElicitField so the cursor path (cursor.go)
// can measure the prefix — everything left of the value — with the
// exact padding the renderer applies, instead of re-deriving the
// column arithmetic and drifting from it. The focused row swaps the
// two-space lead for "> ", which is the same width.
func elicitFieldRow(label, value string) string {
	return fmt.Sprintf("  %-16s %s", label, value)
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
	row := elicitFieldRow(label+":", value)
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

// renderTurnErrorBlock paints a structured turn-error from a
// push-mode SSE event (spec §2.6 / issue #40). Multi-line block:
//
//	⚠ <kind> · <code-if-present>
//	   <message>
//	   hint: <hint-if-present>
//	   ↻ retryable
//
// Header (kind line) is bold-error. Body lines indented to match
// other multi-line block renders (RoleSystem, RoleNotice).
func (m Model) renderTurnErrorBlock(te TurnError, width int) string {
	kind := te.Kind
	if kind == "" {
		kind = TurnErrorUnknown
	}
	headerParts := []string{GlyphWarn + "  " + kind}
	if te.Code != "" {
		headerParts = append(headerParts, GlyphSeparator+" "+te.Code)
	}
	header := m.styles.ErrorText.Render(strings.Join(headerParts, " "))

	lines := []string{header}
	if te.Message != "" {
		lines = append(lines, m.styles.ErrorText.Render(wordWrapIndent("   "+te.Message, width, "   ")))
	}
	if te.Hint != "" {
		lines = append(lines, m.styles.Muted.Render(wordWrapIndent("   hint: "+te.Hint, width, "         ")))
	}
	if te.Retryable {
		lines = append(lines, m.styles.Muted.Render("   ↻ retryable"))
	}
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
