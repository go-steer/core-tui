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

// Shared scroll plumbing for every modal surface.
//
// "Can I scroll this modal?" used to be answered per-modal, and for
// most of them the answer was no: the tool-call and subagent
// overlays hand-rolled offset arithmetic against their own body
// slices, while /btw, the permission overlay, the elicit form and
// the three pickers rendered their full body and let the terminal
// clip whatever didn't fit. The mouse wheel reached none of them —
// MouseWheelMsg fell through Update to the chat viewport BEHIND the
// modal, so the wheel scrolled something the operator couldn't see.
//
// This file centralizes the three pieces a scrollable modal needs:
//
//	scrollState   offset + last-measured geometry that survives a
//	              render, so a keystroke can clamp without
//	              re-rendering the body.
//	scrollView /  window a []string body to the visible rows and
//	listWindow    glue on the Scrollbar column.
//	HandleWheel   the wheel vocabulary, routed to the front-most
//	              dialog (or to Model's inline modals from Update).
//
// Dialogs on the Overlay stack keep their offset in their own struct
// (they're pointers — mutations from Render persist). The inline
// modals (permission / elicit / side-answer) share Model.modalScroll
// because View() has a value receiver and can only write back
// through a pointer.

package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// wheelScrollLines is how many body rows one mouse-wheel tick moves.
// Three matches the terminal convention (and what bubbles' viewport
// uses), so the wheel feels the same inside a modal as it does on
// the chat behind it.
const wheelScrollLines = 3

// modalChromeRows is the row budget RenderContext spends on chrome:
// title line, blank, blank, footer rule, footer text. Body height is
// terminal height minus this minus modalMarginRows.
const modalChromeRows = 5

// modalMarginRows keeps a modal from painting edge-to-edge over the
// terminal — the operator keeps a sliver of the chat visible above
// and below so the overlay reads as an overlay. A nicety, and the
// first thing modalFullscreen gives up: see modalFullscreenBelow.
const modalMarginRows = 4

// minModalBodyRows is the floor for a modal body. On a short
// terminal we'd rather overflow slightly than render a modal with a
// one-row window that can't show anything useful — but only while
// there is still a taller modal to be had. In fullscreen mode the
// alternative to a one-row window is no window at all, so the floor
// is relaxed there (modalBodyHeight).
const minModalBodyRows = 3

// modalPickerChromeRows is the chrome budget of the TALLEST modal:
// the three pickers, which pay modalChromeRows+1 for the filter row
// (issue #117). Every threshold below is derived from this rather
// than from modalChromeRows so that all modal surfaces change regime
// at the same terminal height — an operator resizing past the
// threshold should not see the theme picker go fullscreen while the
// permission modal keeps its margin.
const modalPickerChromeRows = modalChromeRows + 1

// modalFullscreenBelow is the terminal height under which a modal
// stops reserving modalMarginRows and takes the whole terminal.
//
// Derived, not picked: the normal-regime body height is
// termHeight-chrome-modalMarginRows floored at minModalBodyRows, so
// the floor engages — i.e. the subtraction stops reflecting what
// actually fits — at exactly
//
//	chrome + modalMarginRows + minModalBodyRows
//
// which is 6+4+3 = 13 for the tallest chrome. At 13 rows and up
// every modal composes strictly inside the terminal with its margin
// intact and nothing needs to change. At 12 and below the margin is
// already fiction (the floor is quietly eating into it), and by 8
// rows the composed modal is taller than the terminal and clipFrame
// takes the footer hint off the bottom — the one row that tells the
// operator how to close the thing. So the margin goes at exactly the
// height where it stopped being real.
const modalFullscreenBelow = modalPickerChromeRows + modalMarginRows + minModalBodyRows

// modalFullscreen reports whether the terminal is too short to hold
// a normal modal, in which case the modal takes the full terminal:
// no margin, no body floor. A non-positive height is "unknown
// geometry", not "tiny", and is never fullscreen.
func modalFullscreen(termHeight int) bool {
	return termHeight > 0 && termHeight < modalFullscreenBelow
}

// modalMargin is how many rows a modal leaves to the chat behind it
// at this terminal height. modalMarginRows normally, zero once
// modalFullscreen trips.
func modalMargin(termHeight int) int {
	if modalFullscreen(termHeight) {
		return 0
	}
	return modalMarginRows
}

// modalBodyHeight is how many body rows fit at the current terminal
// height, given how many rows that modal's chrome eats.
//
// Returns 0 when the terminal height is unknown — a pre-resize frame
// or a bare Model{} in a test. Callers read 0 as "don't window",
// which keeps every unsized render showing its full body exactly as
// it did before scrolling existed.
//
// In fullscreen mode the body is whatever is left after chrome, with
// no minModalBodyRows floor: a two-row window is worth having when
// the alternative is a modal whose bottom is clipped off. Below the
// point where chrome alone fills the terminal there is nothing left
// to give, and this returns 1 rather than 0 or a negative — 0 means
// "don't window" to scrollView, which would render the entire body
// and overflow much worse. fitModalContent is what keeps the
// composed frame honest at those sizes.
func modalBodyHeight(termHeight, chrome int) int {
	if termHeight <= 0 {
		return 0
	}
	if modalFullscreen(termHeight) {
		return max(1, termHeight-chrome)
	}
	h := termHeight - chrome - modalMarginRows
	if h < minModalBodyRows {
		h = minModalBodyRows
	}
	return h
}

// wrappedRows estimates how many terminal rows a single-line string
// occupies once the enclosing lipgloss frame wraps it to width. Used
// to size modal chrome whose footer key-hint wraps on narrow
// terminals — under-counting there would let the body claim a row
// the footer needs.
func wrappedRows(s string, width int) int {
	if width <= 0 {
		return 1
	}
	w := lipgloss.Width(s)
	if w <= width {
		return 1
	}
	return (w + width - 1) / width
}

// scrollState is the scroll offset for a modal body plus the
// geometry measured at the last render. Keeping total/view here is
// what lets a keystroke clamp ("End" → bottom, "down" at the bottom
// → no-op) without re-rendering the body to count its lines.
//
// The inline modals hold one of these behind Model.modalScroll (a
// pointer, so View()'s value receiver can write the measurement
// back). Dialogs on the Overlay stack are already pointers and can
// embed it directly.
type scrollState struct {
	// offset is the index of the first visible body row.
	offset int
	// total / view are the content and window heights recorded by
	// the last render. Zero view means "not measured / unbounded",
	// in which case there is nothing to scroll.
	total int
	view  int
	// cursor is the focused row as of the last follow() call, for
	// bodies that have one (the elicit form). Tracked so following
	// happens on cursor MOVEMENT rather than on every render.
	cursor int
}

// measure records the geometry of the render that just happened and
// re-clamps the offset against it. Called from the render path.
func (s *scrollState) measure(total, view int) {
	s.total, s.view = total, view
	s.clamp()
}

// maxOffset is the largest first-visible-row index. Zero when the
// body fits (or hasn't been measured).
func (s *scrollState) maxOffset() int {
	if s.view <= 0 {
		return 0
	}
	return nonNeg(s.total - s.view)
}

// overflows reports whether the body is taller than its window —
// the condition for showing a scrollbar and a scroll key hint.
func (s *scrollState) overflows() bool { return s.maxOffset() > 0 }

func (s *scrollState) clamp() {
	s.offset = min(nonNeg(s.offset), s.maxOffset())
}

// to jumps to an absolute offset, clamped.
func (s *scrollState) to(offset int) {
	s.offset = offset
	s.clamp()
}

// by moves the offset by delta rows, clamped.
func (s *scrollState) by(delta int) { s.to(s.offset + delta) }

// follow scrolls the minimum needed to keep a focused row visible,
// but only when that row has MOVED since the last call. An operator
// who scrolled away from the focused field to read something else
// shouldn't be yanked back on every repaint — only when they press
// Tab and the focus itself moves.
func (s *scrollState) follow(cursor int) {
	if cursor == s.cursor {
		return
	}
	s.cursor = cursor
	s.offset = listWindow(s.offset, cursor, s.total, s.view)
}

// reset returns to the top. Called when a modal opens so a fresh
// body never inherits the previous one's scroll position.
func (s *scrollState) reset() { *s = scrollState{} }

// applyStroke moves the offset per a navigation keystroke and
// reports whether the stroke was a scroll key at all. The vocabulary
// is deliberately arrow/page keys only — no j/k — because the inline
// modals that use it (the elicit form especially) feed printable
// runes to their own fields.
func (s *scrollState) applyStroke(stroke string) bool {
	page := max(1, s.view-1)
	switch stroke {
	case "up", "ctrl+p":
		s.by(-1)
	case "down", "ctrl+n":
		s.by(1)
	case "pgup":
		s.by(-page)
	case "pgdown", "pgdn":
		s.by(page)
	case "home":
		s.to(0)
	case "end":
		s.to(s.maxOffset())
	default:
		return false
	}
	return true
}

// scroll returns the inline modals' shared scroll state, allocating
// it on first use. NewModel primes the field; the lazy branch covers
// a zero-value Model{} (the tests build those directly), where the
// allocation lands in whatever copy asked for it — harmless, since
// an unsized test model renders unwindowed bodies anyway.
func (m *Model) scroll() *scrollState {
	if m.modalScroll == nil {
		m.modalScroll = &scrollState{}
	}
	return m.modalScroll
}

// scrollView windows lines to view rows starting at offset and glues
// a Scrollbar column onto the right of each visible row. Returns
// lines untouched when everything fits (or when view is 0, the
// unsized-terminal case), so a short body renders exactly as it did
// before it was scrollable.
//
// contentWidth is the column the rows are padded to before the bar —
// callers reserve it out of the dialog width so the body doesn't
// reflow the moment it starts overflowing.
func scrollView(styles Styles, lines []string, contentWidth, view, offset int) []string {
	if view <= 0 || len(lines) <= view {
		return lines
	}
	offset = min(nonNeg(offset), len(lines)-view)
	visible := append([]string(nil), lines[offset:offset+view]...)
	bar := strings.Split(Scrollbar(styles, view, len(lines), view, offset), "\n")
	pad := lipgloss.NewStyle().Width(contentWidth)
	for i := range visible {
		if i < len(bar) {
			visible[i] = pad.Render(visible[i]) + " " + bar[i]
		}
	}
	return visible
}

// listWindow returns the window offset for a cursor list: the stored
// offset nudged by the minimum needed to keep cursor visible. Used
// by the pickers, whose scroll position follows the selection rather
// than being driven directly.
func listWindow(offset, cursor, total, view int) int {
	if view <= 0 || total <= view {
		return 0
	}
	offset = min(nonNeg(offset), total-view)
	if cursor < offset {
		offset = cursor
	}
	if cursor >= offset+view {
		offset = cursor - view + 1
	}
	return nonNeg(offset)
}

// scrollHint is the " ↑↓ scroll" fragment modal footers append when
// their body overflows — empty when it fits, so a short modal
// doesn't advertise a key that does nothing.
func scrollHint(overflows bool) string {
	if !overflows {
		return ""
	}
	return "↑↓ scroll"
}

// ScrollDialog is the optional extension for a Dialog whose body
// scrolls by lines rather than by cursor rows. The Overlay routes
// mouse-wheel ticks to ScrollBy; dialogs that don't implement it get
// one cursor step per tick synthesized from their up/down keys
// instead (the right behavior for the pickers).
type ScrollDialog interface {
	Dialog

	// ScrollBy moves the dialog's body by delta rows — negative is
	// toward the top. Clamping is the dialog's business.
	ScrollBy(delta int, m *Model)
}

// HandleWheel routes one wheel gesture to the front-most dialog.
// delta is in body rows, signed. Returns Consumed so Update knows
// whether to keep the event away from the chat viewport behind the
// modal — always true when anything is open, since a wheel tick that
// silently scrolls a surface hidden behind a modal is the bug this
// whole file exists to fix.
func (o *Overlay) HandleWheel(delta int, m *Model) (consumed bool, cmd tea.Cmd) {
	front := o.Front()
	if front == nil {
		return false, nil
	}
	if sd, ok := front.(ScrollDialog); ok {
		sd.ScrollBy(delta, m)
		return true, nil
	}
	// Cursor dialogs (the pickers): one selection step per tick,
	// not wheelScrollLines of them — a wheel nudge that jumps three
	// models past the one you wanted is worse than no wheel at all.
	stroke := "up"
	if delta > 0 {
		stroke = "down"
	}
	_, cmd = o.HandleKey(stroke, m)
	return true, cmd
}
