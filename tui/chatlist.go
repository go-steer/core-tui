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

// Item-addressed transcript (issue #161).
//
// The transcript used to be concatenated into one string and handed
// to a viewport that sliced the visible window out of it. Every
// repaint therefore cost O(total scrollback) to produce a screenful:
// at 400 turns a warm repaint was 3.59ms and 50,297 allocations, and
// a resize drag — which is a stream of width-changing events, not one
// — spent 238ms at p95 against a 16ms interactivity target.
//
// The substitution that fixes it is small to describe and everything
// else here follows from it: the scroll position is a PAIR,
// (offsetIdx, offsetLine) — the window's top row is line offsetLine
// of row offsetIdx — rather than a flat line offset into a blob. A
// flat offset cannot be resolved to a position in the transcript
// without knowing the height of every row above it, and knowing that
// is the O(N). With the pair, chatView starts at a row it already
// knows and walks FORWARD until the height budget is spent. Rows past
// the fold are never rendered and never even measured.
//
// Two things keep that honest:
//
//   - Rows are rendered through listCache, keyed by (id, width,
//     version), so a walk that revisits a row pays nothing. The cache
//     already existed for the concat path; what is new is that it now
//     stores the SPLIT lines as well, because the height of a row is
//     needed by every offset calculation and re-splitting a cached
//     string per query would put the O(row size) back.
//   - Nothing on the frame path may ask for a global total. There is
//     no chatTotalHeight; the two questions that sound like they need
//     one — "are we at the bottom" and "what is the flat offset" —
//     are answered by chatBottomOffset, which walks UP from the end
//     and stops after one viewport's worth of rows, and by
//     chatYOffset, which is exhaustive and therefore confined to
//     tests and to SetYOffset's inverse.
//
// The live tail (the in-progress assistant block, the spinner line,
// the empty-state hint) is deliberately NOT item-addressed. It is one
// row, it changes on every chunk so a cache would never hit, and
// refreshViewport already runs on every change that can affect it —
// so it is rendered there, once, into m.chatTail, and read from there
// by everything else. That also keeps chatView pure enough to run
// under View's value receiver.
package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// chatViewport is the transcript's window: how big it is, and where
// it sits in the row list.
//
// It holds no content. Everything that needs to know what a row looks
// like goes through the Model, which owns the history and the render
// cache — which is why the scroll operations are Model methods rather
// than methods here. What is left is the geometry, and the geometry
// is what the layout code (resize, budget) reads.
type chatViewport struct {
	width  int
	height int

	// offsetIdx / offsetLine is the scroll position: the window's top
	// row is line offsetLine of row offsetIdx.
	offsetIdx  int
	offsetLine int
}

// Width is the column count the transcript renders at.
func (v chatViewport) Width() int { return v.width }

// Height is the row count the transcript occupies in the frame.
func (v chatViewport) Height() int { return v.height }

// SetWidth sets the render width. Negative widths are clamped to
// zero: callers derive it by subtracting chrome from the terminal
// width and a narrow terminal can take that below zero.
func (v *chatViewport) SetWidth(w int) {
	if w < 0 {
		w = 0
	}
	v.width = w
}

// SetHeight sets the visible row count.
func (v *chatViewport) SetHeight(h int) {
	if h < 0 {
		h = 0
	}
	v.height = h
}

// Offset exposes the scroll pair. Tests and the resize path read it;
// nothing outside this file writes it.
func (v chatViewport) Offset() (int, int) { return v.offsetIdx, v.offsetLine }

// ---------------------------------------------------------------
// Rows
// ---------------------------------------------------------------

// chatRowCount is the number of rows in the transcript: one per
// history message, plus the live tail when it has anything to show.
func (m Model) chatRowCount() int {
	n := m.history.Len()
	if len(m.chatTail) > 0 {
		n++
	}
	return n
}

// chatRowLines returns the rendered lines of row i, including the
// separator that stands above it. Returns nil for an out-of-range
// index so callers can walk without bounds-checking twice.
//
// Every read of a row's geometry goes through here — the view walk,
// the bottom-offset walk, the flat-offset conversion — so a row is
// rendered at most once no matter how many of them ask, and usually
// zero times because listCache already has it.
func (m Model) chatRowLines(i int) []string {
	n := m.history.Len()
	if i < 0 || i > n {
		return nil
	}
	if i == n {
		// The live tail already carries its own separator: it is
		// built by buildChatTail, which knows whether there is
		// history above it.
		return m.chatTail
	}
	msg, ok := m.history.at(i)
	if !ok {
		return nil
	}
	body := m.chatMessageLines(i, n, msg)
	if i == 0 {
		return body
	}
	// Separator lines are not cached. They are a rule or a blank —
	// cheaper to rebuild than to look up, and folding them into the
	// cached entry would key content on a row's POSITION, which
	// changes under /clear and transcript resume while the message
	// ID does not.
	sep := m.chatSeparator(msg)
	out := make([]string, 0, len(sep)+len(body))
	out = append(out, sep...)
	return append(out, body...)
}

// chatSeparator is the gap above a message. A user turn opens with a
// rule (it starts a new exchange); everything else gets a blank line.
//
// The line counts here are what the concatenating renderer produced —
// "\n" + rule + "\n\n" between a message and a following user turn,
// "\n\n" otherwise — so the transcript is byte-identical to what the
// flat path drew.
func (m Model) chatSeparator(msg Message) []string {
	if msg.Role != RoleUser {
		return []string{""}
	}
	return []string{m.chatRuleLine(), ""}
}

// chatRuleLine is the separator rule above a user turn, off the memo
// refreshViewport keeps.
//
// The rule is one lipgloss render of one repeated glyph, which reads
// as free until you notice how often a row is measured: a repaint
// walks the window up to three times (reflow, tail pin, warm) and
// every user row in it would rebuild the same string each time. The
// memo is rebuilt every refresh, so the only way to reach it stale is
// a resize that has not repainted yet — hence the width guard and the
// fall back to building it here.
func (m Model) chatRuleLine() string {
	if m.chatRule != "" && m.chatRuleWidth == m.viewport.Width() {
		return m.chatRule
	}
	return m.buildChatRule()
}

// buildChatRule renders the separator rule at the current width.
func (m Model) buildChatRule() string {
	return m.styles.Rule.Render(strings.Repeat(GlyphRule, m.viewport.Width()))
}

// chatMessageLines returns the cached lines for history entry i,
// rendering on a miss.
//
// There is no approximation branch here, and its absence is issue
// #161's dividend. The concatenating renderer had to touch every row
// to build the frame, so a resize drag needed a way to serve rows the
// operator could not see without re-assembling them — it parked the
// previous width's render under the new width, flagged it
// approximate, and the settle retired the flags a slice at a time.
// A window that only asks for the rows it draws does not need any of
// that: an off-screen row at a stale width is simply never
// requested, and there is nothing to retire.
// A fold (issue #152) is applied on the way out rather than being
// rendered: it is the head of the ordinary render plus a count, so
// the cache neither knows nor needs to know that the row is folded.
func (m Model) chatMessageLines(i, total int, msg Message) []string {
	width := m.viewport.Width()
	item := messageItem{msg: msg, idx: i, total: total}
	lines, ok := m.listCache.getLines(item, width)
	if !ok {
		lines = m.listCache.put(item, width, m.renderMessage(msg))
	}
	if m.chatRowCollapsed(msg) {
		return m.chatCollapsedRow(lines)
	}
	return lines
}

// chatRowCached reports whether history row i already has an exact
// render at the current width. The resize warm pass uses it to decide
// whether a row still owes work.
func (m Model) chatRowCached(i, total int, msg Message) bool {
	_, ok := m.listCache.getLines(messageItem{msg: msg, idx: i, total: total}, m.viewport.Width())
	return ok
}

// chatVisitWindow calls visit for each row the window shows, and for
// no others.
//
// visit runs BEFORE the row is measured, and that ordering is what
// lets the answer be exact. "Which rows are visible" needs their
// heights, the heights come from renders, and the caller that most
// wants the answer — the resize path — is deciding which rows to
// render: asked up front the question is circular, and the honest
// reply is an over-estimate of a whole screenful. Asked one row at a
// time it is not circular at all, because the row is rendered and then
// measured, and the measurement is what decides whether the walk
// continues. A resize therefore re-wraps the seven rows on screen
// rather than the forty it might otherwise have to assume.
//
// Two directions, because there are two ways to know where the window
// is. Detached, the offset says, and the walk runs forward from it.
// Following, the window sits at the END of the content — an offset a
// resize has not corrected yet, since chatGotoBottom runs at the end
// of refreshViewport — so the walk runs backward from the last row
// instead. Trusting m.follow over the stale offset is the same rule
// refreshViewport follows (issue #93).
func (m *Model) chatVisitWindow(visit func(i int)) {
	height := m.viewport.Height()
	n := m.chatRowCount()
	if height <= 0 || n == 0 {
		return
	}
	if m.follow {
		for i, need := n-1, height; i >= 0 && need > 0; i-- {
			visit(i)
			need -= len(m.chatRowLines(i))
		}
		return
	}
	// A partly scrolled top row contributes only the lines below the
	// fold, so the budget starts in deficit by offsetLine.
	used := -m.viewport.offsetLine
	for i := m.viewport.offsetIdx; i < n && used < height; i++ {
		visit(i)
		used += len(m.chatRowLines(i))
	}
}

// ---------------------------------------------------------------
// Drawing
// ---------------------------------------------------------------

// chatView draws the visible window.
//
// Start at offsetIdx, skip its first offsetLine lines, then walk
// forward accumulating into a budget equal to the window height and
// stop the moment it is spent. That loop is the whole mechanism: what
// it costs is bounded by what is on screen, not by what exists.
//
// The selection gutter (issue #152) is prefixed here, per line, as
// the lines are collected. It is the reason the block is padded to
// width + chatGutterWidth: the viewport's width is what a ROW renders
// at, the gutter is reserved on top of it, and the sum is the column
// the frame gave the transcript.
//
// The lipgloss pad at the end reproduces what the viewport did — the
// transcript block is always exactly height rows of exactly that many
// columns, so the frame it is joined into does not shift when the
// transcript is short.
func (m Model) chatView() string {
	w, h := m.viewport.Width(), m.viewport.Height()
	if w <= 0 || h <= 0 {
		return ""
	}
	marked, plain := m.chatGutterPrefixes()
	out := make([]string, 0, h)
	skip := m.viewport.offsetLine
	for i := m.viewport.offsetIdx; i < m.chatRowCount() && len(out) < h; i++ {
		lines := m.chatRowLines(i)
		if skip > 0 {
			if skip >= len(lines) {
				skip -= len(lines)
				continue
			}
			lines = lines[skip:]
			skip = 0
		}
		gutter := plain
		if m.chatRowMarked(i) {
			gutter = marked
		}
		for _, ln := range lines {
			if len(out) == h {
				break
			}
			out = append(out, gutter+ln)
		}
	}
	return lipgloss.NewStyle().
		Width(w + chatGutterWidth).
		Height(h).
		Render(strings.Join(out, "\n"))
}

// warmChatWindow renders the rows the window is about to draw, so
// that View is a cache read rather than a render.
//
// A walk with nothing to do per row: measuring a row is what renders
// it, so visiting the window is the whole job. It costs what the frame
// costs — bounded by the height, not by the transcript. What it buys
// is that the work stays where the rest of the transcript's work is: a
// refresh renders, a draw reads. View has a value receiver and runs on
// a copy, so a render that happens there still populates the cache (it
// is behind a pointer) but nothing else it computes can be kept, and
// the resize path's "is this row exact yet" contract is about what a
// repaint has settled, not about what the last paint happened to
// touch.
func (m *Model) warmChatWindow() {
	m.chatVisitWindow(func(int) {})
}

// buildChatTail renders the live tail — the in-progress assistant
// block and spinner line (R-CHAT-3, R-CHAT-4), or the empty-state
// hint — into its lines, separator included.
//
// Called from refreshViewport only. The tail is the one row whose
// content changes on essentially every message the model handles, so
// caching it would never hit; instead it is rendered once per refresh
// and read from m.chatTail by every walk in this file. Everything
// that can change it already ends in a refresh.
func (m *Model) buildChatTail() []string {
	var b strings.Builder
	if inProgress := m.renderInProgress(); inProgress != "" {
		if m.history.Len() > 0 {
			b.WriteString("\n")
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
	if b.Len() == 0 {
		return nil
	}
	return clampChatLines(strings.Split(b.String(), "\n"), m.viewport.Width())
}

// clampChatLines enforces the transcript's half of the width
// contract: no line leaves a row wider than the width it was rendered
// at.
//
// Rows overrun, and not because they are careless — Glamour adds a
// margin the wrap width does not account for, a tool row prefixes its
// result lines. The flat path never had to care because the viewport
// cut every line on the way out; without it, an oversized line
// reaches the compositor, shifts everything to its right and the
// frame clip eats a panel.
//
// Truncation rather than re-wrapping, deliberately: it is the only
// bound that leaves the LINE COUNT alone, and the line count is what
// the whole lazy walk budgets against. It runs on a cache miss only.
func clampChatLines(lines []string, width int) []string {
	if width <= 0 {
		return lines
	}
	for i, ln := range lines {
		if ansi.StringWidth(ln) > width {
			lines[i] = ansi.Truncate(ln, width, "")
		}
	}
	return lines
}

// ---------------------------------------------------------------
// Scrolling
// ---------------------------------------------------------------

// chatBottomOffset is the (idx, line) pair that puts the last content
// line on the window's last row.
//
// Bounded, and that is the point of keeping it distinct from any
// exhaustive total: it walks UP from the end and stops as soon as it
// has covered a window's worth of lines, so it costs the same at
// 4,000 rows as at 10.
func (m Model) chatBottomOffset() (int, int) {
	n := m.chatRowCount()
	if n == 0 {
		return 0, 0
	}
	return m.chatEndOffset(n - 1)
}

// chatEndOffset is the (idx, line) pair that puts the last line of
// row `last` on the window's last row. The generalization of
// chatBottomOffset, and bounded for the same reason: it stops as soon
// as it has covered a window's worth of lines. The selection reveal
// (issue #152) uses it to scroll an item just far enough into view.
func (m Model) chatEndOffset(last int) (int, int) {
	need := m.viewport.Height()
	for i := last; i >= 0; i-- {
		h := len(m.chatRowLines(i))
		if h >= need {
			return i, h - need
		}
		need -= h
	}
	return 0, 0
}

// chatAtBottom reports whether the last content line is visible.
func (m Model) chatAtBottom() bool {
	idx, line := m.chatBottomOffset()
	cur, curLine := m.viewport.Offset()
	return cur > idx || (cur == idx && curLine >= line)
}

// chatGotoBottom pins the window to the end of the transcript.
func (m *Model) chatGotoBottom() {
	m.viewport.offsetIdx, m.viewport.offsetLine = m.chatBottomOffset()
}

// chatGotoTop pins the window to the start.
func (m *Model) chatGotoTop() {
	m.viewport.offsetIdx, m.viewport.offsetLine = 0, 0
}

// chatScrollBy moves the window n lines, positive for down. Stops at
// either end.
func (m *Model) chatScrollBy(n int) {
	for ; n > 0; n-- {
		m.chatScrollDown()
	}
	for ; n < 0; n++ {
		m.chatScrollUp()
	}
}

// chatPageBy moves the window n screenfuls, positive for down.
func (m *Model) chatPageBy(n int) {
	m.chatScrollBy(n * m.viewport.Height())
}

func (m *Model) chatScrollDown() {
	bi, bl := m.chatBottomOffset()
	cur, curLine := m.viewport.Offset()
	if cur > bi || (cur == bi && curLine >= bl) {
		return
	}
	m.viewport.offsetLine++
	if m.viewport.offsetLine >= len(m.chatRowLines(m.viewport.offsetIdx)) {
		m.viewport.offsetLine = 0
		m.viewport.offsetIdx++
	}
}

func (m *Model) chatScrollUp() {
	if m.viewport.offsetLine > 0 {
		m.viewport.offsetLine--
		return
	}
	if m.viewport.offsetIdx == 0 {
		return
	}
	m.viewport.offsetIdx--
	m.viewport.offsetLine = atLeast(len(m.chatRowLines(m.viewport.offsetIdx))-1, 0)
}

// clampChatOffset drags the scroll position back inside the content
// after the content changed under it — /clear, a session switch, a
// finalized turn replacing the tail with a history row.
//
// Costs one row render in the common case (the offset row is on
// screen and therefore cached) and nothing at all when the transcript
// is empty.
func (m *Model) clampChatOffset() {
	n := m.chatRowCount()
	if n == 0 {
		m.viewport.offsetIdx, m.viewport.offsetLine = 0, 0
		return
	}
	if m.viewport.offsetIdx >= n {
		m.chatGotoBottom()
		return
	}
	if h := len(m.chatRowLines(m.viewport.offsetIdx)); m.viewport.offsetLine >= h {
		m.viewport.offsetLine = atLeast(h-1, 0)
	}
}

// chatWheelDelta is how many lines one wheel tick moves the
// transcript. Three is what the viewport moved before it was
// replaced, and a wheel tick that scrolls a different distance in
// the transcript than in a modal list reads as a bug.
const chatWheelDelta = 3

// forwardChatScroll applies the scroll bindings the viewport used to
// own, for a message no earlier handler claimed.
//
// The bindings are spelled out here rather than configured on a
// widget, and that is the improvement over what it replaces: the
// viewport arrived with a KeyMap full of vim conventions — h/j/k/l to
// scroll, b/f to page, space for page down, u/d for half a page —
// and every keystroke was forwarded to it, so NewModel had to
// overwrite the whole KeyMap with non-letter forms to stop typing "b"
// into the prompt from paging the transcript. A switch cannot acquire
// a binding nobody asked for.
//
// Right/Left are still absent, deliberately. Panning belongs to #154,
// which has to solve it without the cut-from-the-left behaviour the
// old xOffset had.
//
// ctrl+d is absent for a different reason: it quits unconditionally
// (update.go), so the half-page-down binding the viewport carried was
// already dead code. Only its ctrl+u half survives, and it is kept
// because half of a pair still beats none — pgup/pgdn cover the
// other direction.
func (m *Model) forwardChatScroll(msg tea.Msg) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "pgdown":
			m.chatPageBy(1)
		case "pgup":
			m.chatPageBy(-1)
		case "ctrl+u":
			m.chatScrollBy(-atLeast(m.viewport.Height()/2, 1))
		}
	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelDown:
			m.chatScrollBy(chatWheelDelta)
		case tea.MouseWheelUp:
			m.chatScrollBy(-chatWheelDelta)
		}
	}
}

// chatYOffset is the flat line offset of the window's top row.
//
// EXHAUSTIVE — it renders every row above the window, which is
// exactly the O(N) the pair offset exists to avoid. It is here
// because a flat offset is the natural way to ASSERT on a scroll
// position, and because SetYOffset needs an inverse. Nothing on the
// frame path may call it.
func (m Model) chatYOffset() int {
	total := 0
	for i := 0; i < m.viewport.offsetIdx && i < m.chatRowCount(); i++ {
		total += len(m.chatRowLines(i))
	}
	return total + m.viewport.offsetLine
}

// chatSetYOffset moves the window to a flat line offset, resolving it
// against the rows. The inverse of chatYOffset and equally
// exhaustive; same rule about the frame path.
func (m *Model) chatSetYOffset(y int) {
	if y <= 0 {
		m.chatGotoTop()
		return
	}
	n := m.chatRowCount()
	for i := 0; i < n; i++ {
		h := len(m.chatRowLines(i))
		if y < h {
			m.viewport.offsetIdx, m.viewport.offsetLine = i, y
			m.clampChatOffset()
			return
		}
		y -= h
	}
	m.chatGotoBottom()
}
