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
//
// The blank separator is a shared slice rather than a fresh literal,
// because a literal is an allocation per non-user row per frame and
// the value is a constant. Nothing here owns the slices it hands
// back — chatRowLines copies whatever it gets into the merged row,
// and the body it merges is the render cache's own slice — so a
// shared one is no weaker a contract than the file already keeps.
var chatBlankSeparator = []string{""}

func (m Model) chatSeparator(msg Message) []string {
	if msg.Role != RoleUser {
		return chatBlankSeparator
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
	// Looked up by ID rather than through Item so the hit path does
	// not build (and heap-allocate) a messageItem it would throw away
	// — issue #204. The box is deferred to the miss branch, where put
	// needs the interface and a render is being paid for regardless.
	lines, ok := m.listCache.getLinesByID(msg.ID, msg.Version, width)
	if !ok {
		item := messageItem{msg: msg, idx: i, total: total}
		lines = m.listCache.put(item, width, m.renderMessage(msg))
	}
	if m.chatRowCollapsed(msg) {
		return m.chatCollapsedRow(lines)
	}
	return lines
}

// chatRowCached reports whether a history row already has an exact
// render at the current width. The resize warm pass uses it to decide
// whether a row still owes work.
//
// It takes the Message alone. The row index and transcript length it
// used to take were only ever there to fill in a messageItem for the
// lookup, and a lookup by ID does not want one — so asking for them
// was asking the caller to assemble a value purely to be discarded
// (issue #204).
func (m Model) chatRowCached(msg Message) bool {
	_, ok := m.listCache.getLinesByID(msg.ID, msg.Version, m.viewport.Width())
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
// The pad at the end reproduces what the viewport did — the transcript
// block is always exactly height rows of exactly that many columns, so
// the frame it is joined into does not shift when the transcript is
// short. See chatBlock for why that pad is hand-rolled.
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
			out = append(out, gutter+chatCutLine(ln, m.chatX, w))
		}
	}
	return chatBlock(out, w+chatGutterWidth, h)
}

// chatBlock assembles the drawn rows into the transcript block:
// exactly height rows of exactly width cells, short rows padded out
// with spaces and absent rows supplied as blank ones.
//
// That shape is a contract with the compositor rather than cosmetics.
// JoinVertical sizes the frame from what it is handed, so a short
// transcript that returned short rows would let the panel beside it
// move as the session fills up.
//
// This used to be
//
//	lipgloss.NewStyle().Width(width).Height(height).
//		Render(strings.Join(rows, "\n"))
//
// and replacing it is issue #160. It is the same defect issue #157
// fixed in scrollView, one level up and batched over the whole
// visible chat instead of one modal body: a lipgloss Style with a
// width set is a WORD-WRAPPER, not a padder, and every row handed to
// it here has already been cut to exactly this width by chatCutLine.
// The wrap is therefore guaranteed to be a no-op, and it is not a
// cheap one — lipgloss.Wrap pipes the whole block through a
// WrapWriter, which copies it into the output ONE BYTE AT A TIME
// through an io.Writer, i.e. a `[]byte{b}` allocation per byte of the
// visible frame. On a 100x40 window that single call was 51% of every
// repaint's allocations.
//
// Two things it does besides wrapping have to survive, which is why
// the Style is still here as a fallback rather than deleted:
//
//   - tabs. sanitizeLine deliberately exempts TAB, and lipgloss
//     expands it to four spaces on the way through
//     (maybeConvertTabs). Appending spaces to a row containing a tab
//     would leave the row narrow.
//   - open styling. WrapWriter closes a style that is still switched
//     on at a newline and re-opens it on the next line, so a row
//     carrying escapes straight from a host (a tool result, a
//     subagent transcript) does not bleed into the row below it.
//
// Both are rare and neither is cheap to reproduce, so chatRowPlain
// detects them and the block falls back whole. Falling back whole
// rather than per row keeps the answer exactly the string the Style
// would have returned — mixing the two per row would have to
// reproduce WrapWriter's cross-row style state, which is the part
// that is actually hard.
func chatBlock(rows []string, width, height int) string {
	if s, ok := chatPadBlock(rows, width, height); ok {
		return s
	}
	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		Render(strings.Join(rows, "\n"))
}

// chatPadBlock is chatBlock's fast path: join and space-pad, no
// wrapping. Reports false the moment it meets a row it cannot prove
// wraps to itself, in which case it has written nothing the caller
// can use and chatBlock re-does the work through lipgloss.
//
// Bailing out mid-build rather than pre-scanning every row is
// deliberate: the pre-scan would walk the bytes twice on the path
// that matters (every frame, every row plain) to save a partial
// build on the path that essentially never happens.
func chatPadBlock(rows []string, width, height int) (string, bool) {
	// Degenerate geometry goes to the Style, which has its own
	// answers for it: a zero width leaves the rows unpadded but a
	// zero height still pads them, and more rows than the height
	// budget renders all of them rather than dropping the excess.
	// None of the three is reachable from chatView — it returns early
	// on a zero width or height and its loop stops at h — so
	// reproducing them here would be untested code guarding against
	// nothing.
	if width <= 0 || height <= 0 || len(rows) > height {
		return "", false
	}
	var b strings.Builder
	// height rows of width cells plus the newline between each pair.
	// An undercount when rows carry escape sequences, which is fine:
	// Builder grows. The point is to not grow from zero.
	b.Grow(height * (width + 1))
	for i := range height {
		if i > 0 {
			b.WriteByte('\n')
		}
		if i >= len(rows) {
			chatWriteSpaces(&b, width)
			continue
		}
		ln := rows[i]
		// Wider than the block means the Style would have wrapped it
		// onto a second row, and the whole premise here is that it
		// does not have to. chatCutLine makes that true; this is the
		// assertion, not a case to handle.
		w := ansi.StringWidth(ln)
		if w > width || !chatRowPlain(ln) {
			return "", false
		}
		b.WriteString(ln)
		chatWriteSpaces(&b, width-w)
	}
	return b.String(), true
}

// chatRowPlain reports whether appending spaces to a row is the same
// thing lipgloss would do to it — see chatBlock for why the two can
// differ.
//
// Rejected: any C0 control or DEL, because tab is expanded on the way
// through lipgloss and the rest perturb ansi.Wrap's column
// accounting; any OSC introducer, because a hyperlink left open is
// re-opened across a newline the way an open style is. ESC itself is
// exempt from the control-character rule — CSI styling is the common
// case here, not the exception — and is instead checked by sgrOpen,
// which answers the one question that matters about it: is anything
// still switched on at the end of the row.
//
// Deliberately a byte scan and deliberately conservative. A false
// negative costs one frame's worth of the old path; a false positive
// corrupts the frame.
func chatRowPlain(ln string) bool {
	for i := range len(ln) {
		c := ln[i]
		if c >= 0x20 && c != 0x7f {
			continue
		}
		if c != 0x1b {
			return false
		}
		if i+1 < len(ln) && ln[i+1] == ']' {
			return false
		}
	}
	return !sgrOpen(ln)
}

// chatSpaceSlab is the run of spaces chatWriteSpaces slices its
// padding out of, so padding a row is a copy rather than a
// strings.Repeat allocation. 128 covers a comfortable terminal in one
// write and the loop covers the rest — sizing it to the widest
// plausible terminal would be a bigger constant for no fewer
// allocations, since there are none either way.
const chatSpaceSlab = "                                                                " +
	"                                                                "

// chatWriteSpaces writes n spaces.
func chatWriteSpaces(b *strings.Builder, n int) {
	for n > 0 {
		c := min(n, len(chatSpaceSlab))
		b.WriteString(chatSpaceSlab[:c])
		n -= c
	}
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
		// Startup wordmark above the hint (issue #165). "" whenever
		// the banner cannot or should not draw, in which case what
		// follows is exactly the empty state as it was before it
		// existed. Measured against the chat window rather than the
		// terminal, because that is the room it actually gets.
		if banner := m.renderBanner(m.viewport.Width(), m.viewport.Height()); banner != "" {
			b.WriteString(banner)
			b.WriteString("\n\n")
		}
		hint := m.opts.Branding.EmptyStateHint
		if hint == "" {
			hint = "Ask me anything to get started."
		}
		b.WriteString(m.styles.SystemText.Render(hint))
	}
	if b.Len() == 0 {
		return nil
	}
	// Not cut to width here: chatView cuts every line it draws, this
	// one included (issue #154).
	return strings.Split(b.String(), "\n")
}

// chatCutLine enforces the transcript's half of the width contract —
// no line reaches the frame wider than the window — and applies the
// horizontal pan (issue #154) in the same operation, because they are
// the same operation: both are "which cells of this line are on
// screen".
//
// Rows overrun, and not because they are careless — Glamour adds a
// margin the wrap width does not account for, a tool row prefixes its
// result lines, and a table or a diff does not wrap at all. The flat
// path never had to care because the viewport cut every line on the
// way out; without it, an oversized line reaches the compositor,
// shifts everything to its right and the frame clip eats a panel.
//
// This used to happen on the way INTO the render cache, which is what
// made panning impossible: the columns to pan to had already been
// thrown away. Doing it here instead is strictly stronger — every
// line the frame contains passes through this function, including the
// live tail and the fold summary, which the cache-side version had to
// be applied to separately — and costs one cut per drawn line per
// frame, which is bounded by the window, like everything else on this
// path.
//
// Cutting rather than re-wrapping, deliberately: it is the only bound
// that leaves the LINE COUNT alone, and the line count is what the
// whole lazy walk budgets against.
//
// It is the transcript's render-site enforcement (issue #159), sited
// at the LAST moment rather than the first on purpose. The obvious
// reading of "bound it where the content is rendered" is to cut on
// the way into the render cache, which is where it used to happen and
// is what #154 had to undo: a cached row already cut to the window
// has no columns left to pan to. Drawing is the earliest point at
// which the window is the only thing left to serve, so it is the
// earliest point at which the cut costs nothing. Nothing upstream has
// to re-measure, because the cut leaves the row's line count alone.
//
// The unpanned case goes through fitCells (view.go) — the same bound
// renderSidebar owes its fixed column, and where the ansi.Truncate
// caveats are written down — rather than a zero-origin ansi.Cut, so
// the frame stays byte-identical to what it was before panning
// existed.
func chatCutLine(ln string, x, width int) string {
	if width <= 0 {
		return ln
	}
	if x > 0 {
		return ansi.Cut(ln, x, x+width)
	}
	return fitCells(ln, width)
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
