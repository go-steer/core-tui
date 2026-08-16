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

// Panning the transcript sideways (issue #154).
//
// Most of the transcript wraps, and wrapped text needs no panning.
// What does not wrap is exactly the content an agent produces most of:
// a table, a unified diff, a stack trace, one very long line of tool
// output. Those were cut at the right edge and the rest was simply
// gone — no scrollbar, no ellipsis, no key to see it.
//
// # Where the cut moved to
//
// The reason the rest was gone is that it was never kept. Rows were
// truncated to the viewport width on their way INTO the render cache,
// so by the time anything could have panned, the columns to pan to had
// been discarded. This moves that cut to the last possible moment —
// chatView, per drawn line, through chatCutLine — and the cache now
// holds rows at their natural width.
//
// That is a strictly stronger version of the same contract, not a
// weaker one. "No line leaves a row wider than the window" used to
// hold for cached rows and had to be re-applied by hand to everything
// that was not one (the live tail, the fold summary); now it holds for
// every line the frame contains, because there is exactly one place a
// line can reach the frame from. Nothing about heights changes —
// cutting a line does not split it, which is why the lazy walk's line
// budget is untouched.
//
// # Not the viewport's xOffset
//
// The predecessor of this file was a one-line change that unpinned the
// bubbles viewport's xOffset, and it produced corrupted output: that
// field made ansi.Cut remove cells from the LEFT of every line and
// keep the window's width from there, so the text slid left and the
// right edge stayed put. #161 removed that viewport entirely. This
// pans by cutting the window [x, x+width) out of a line that is still
// full width, which is the operation the name implies.
//
// # It resets, on purpose
//
// The offset is dropped when the selection moves and when the terminal
// is resized. A pan is a way of reading ONE wide thing; carrying it to
// the next item would leave the operator looking at column 40 of a
// paragraph, which reads as a broken frame rather than as a scrolled
// one. Line scrolling (shift+up / shift+down) deliberately does NOT
// reset it — scrolling down a forty-row table while panned is the
// case this exists for. And because a pan is invisible in a frame
// with no left edge to compare against, the focus legend says so
// while it is non-zero.

package tui

import "github.com/charmbracelet/x/ansi"

// chatPanStep is how far one press moves the window sideways.
//
// A column at a time is unusable — forty presses to reach a table's
// second column — and a full window at a time throws away the overlap
// that lets the eye carry a row across the jump. Eight cells is about
// one indent level, moves visibly, and is a fixed number rather than
// a fraction of the width so that a pan lands in the same place on a
// narrow terminal as on a wide one.
const chatPanStep = 8

// chatPanBy moves the window sideways, clamped at both ends: never
// left of column zero, never further right than the widest line
// currently on screen.
func (m *Model) chatPanBy(cols int) {
	if cols == 0 {
		return
	}
	m.chatX = min(atLeast(m.chatX+cols, 0), m.chatMaxPan())
}

// chatResetPan returns the window to the left edge. Called wherever
// the thing being panned is no longer the thing that was panned: a
// selection move, a resize.
func (m *Model) chatResetPan() { m.chatX = 0 }

// chatMaxPan is how far right there is anything to see: the widest
// line in the window, less the window.
//
// Measured over the window rather than the transcript, which is what
// keeps it bounded — the rule the rest of this path lives by. It is a
// slight over-estimate for a partly scrolled row, whose off-screen
// lines are measured too, and that is the harmless direction: the
// operator can pan to a blank column and press the other arrow, which
// is not true of a limit that stops short of real content.
func (m *Model) chatMaxPan() int {
	width := m.viewport.Width()
	if width <= 0 {
		return 0
	}
	widest := 0
	m.chatVisitWindow(func(i int) {
		for _, ln := range m.chatRowLines(i) {
			if n := ansi.StringWidth(ln); n > widest {
				widest = n
			}
		}
	})
	return atLeast(widest-width, 0)
}
