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

// Hardware-cursor placement (issue #105).
//
// Every text surface used to draw its own caret glyph — bubbles'
// "virtual cursor", a reverse-video block painted into the frame —
// and tea.View.Cursor was never set, so the terminal's REAL cursor
// stayed wherever the last write left it. Three things break when
// that happens, none of them visible to a sighted operator typing
// ASCII:
//
//   - IME anchoring. A CJK / emoji candidate window follows the
//     hardware cursor, so it opens in the wrong place.
//   - Cursor shape and blink. The operator's terminal configuration
//     is simply not consulted when the caret is a painted block.
//   - Screen readers. Assistive tech tracks the terminal cursor;
//     a painted block is invisible to it.
//
// The fix is the one the widgets are built for: turn the virtual
// cursor OFF (textarea.SetVirtualCursor / textinput.SetVirtualCursor)
// and hand bubbletea a real position through tea.View.Cursor.
//
// The whole difficulty is coordinates. A widget's Cursor() is
// relative to the widget's own top-left cell, and View composes the
// frame out of JoinVertical / JoinHorizontal / lipgloss.Place — so
// the translation into absolute frame coordinates differs per layout
// and per modal, and a centered modal's origin is a function of its
// own rendered size. That arithmetic lives here, in one place, next
// to the clamp that keeps the result inside the clipped frame.

package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// cursorDialog is the optional extension for a Dialog whose body
// owns a text-editing surface and can therefore say where the
// terminal cursor belongs. Dialogs that don't implement it get no
// cursor, which is correct for the tool-call and subagent detail
// overlays: read-only bodies with a scroll position and nothing to
// type into. The three pickers were in that category until issue
// #117 gave them a filter row.
//
// This is deliberately shaped as an optional extension, exactly like
// KeyMsgDialog, because "where does the cursor go" is slated to
// become part of the Dialog contract itself (issue #105's closing
// note, tracked under #115). When that lands, the change is to move
// DialogCursor into Dialog and delete the type assertion in
// Overlay.cursor — the callers and the coordinate arithmetic below
// do not move.
type cursorDialog interface {
	Dialog

	// DialogCursor returns the cursor position RELATIVE to the
	// dialog block's own top-left cell, or nil when the dialog has
	// no meaningful cursor right now. The caller adds the origin
	// the dialog was composited at; a Dialog never needs to know
	// where on screen it was placed.
	DialogCursor(width int, m *Model) *tea.Cursor
}

// Cursor returns the front-most dialog's cursor, dialog-relative, or
// nil when the stack is empty or the front-most dialog has no text
// surface. Mirrors Render: only the FRONT dialog is consulted,
// because only the front dialog receives keystrokes.
func (o *Overlay) cursor(width int, m *Model) *tea.Cursor {
	front := o.Front()
	if front == nil {
		return nil
	}
	cd, ok := front.(cursorDialog)
	if !ok {
		return nil
	}
	return cd.DialogCursor(width, m)
}

// textInputCursor is the caret for a bubbles textinput, corrected for
// issue #125. Returns nil when the widget is blurred or painting its
// own virtual cursor, matching textinput.Cursor's contract; the
// result is relative to the widget's own top-left cell, so callers
// still add their chrome.
//
// textinput.Cursor() in the pinned bubbles/v2 v2.1.0 has two bugs
// (textinput/textinput.go:916-936, byte-identical in v2.1.1, so a
// version bump is not the fix):
//
//  1. xOffset = m.Position() + promptWidth adds a RUNE INDEX to a
//     CELL WIDTH. Every double-width rune before the caret — CJK,
//     most emoji — undercounts it by one cell. That is exactly the
//     IME anchoring that setting tea.View.Cursor exists to get right
//     (issue #105).
//  2. m.offset, the horizontal scroll position, is ignored entirely.
//     View() twenty lines earlier gets it right (pos := max(0,
//     m.pos-m.offset)); Cursor() computes against the UNSCROLLED
//     string and the min() clamp then pins the caret to the right
//     edge of the box. No wide runes needed: type past the width of
//     the input and walk left.
//
// What this fixes and what it does not:
//
//	fits in the box     corrected here, exactly, for any script.
//	scrolled            left to upstream, still wrong.
//
// The split is forced. m.offset has no exported accessor, so
// reproducing bug 2 downstream would mean reimplementing
// handleOverflow's scroll arithmetic against a value we can only see
// through Value() and Position() — guessing where the widget scrolled
// to, and being wrong in a different way. But handleOverflow sets
// offset to 0 whenever the whole value fits (textinput.go:355-359),
// so in that case Value() and Position() ARE the complete state and
// ansi.StringWidth over the prefix is the true cell offset. Width()
// <= 0 means SetWidth was never called, which takes the same
// early-return and is likewise unscrolled.
//
// Both halves are reported upstream and open: charmbracelet/bubbles
// #906 (rune index vs cell width) and #1001 (ignored scroll offset).
// Deleting this is a one-liner once upstream measures the prefix
// itself: the correction becomes a no-op, not a double-correction.
func textInputCursor(ti textinput.Model, prompt string) *tea.Cursor {
	c := ti.Cursor()
	if c == nil {
		return nil
	}
	value := ti.Value()
	if w := ti.Width(); w > 0 && ansi.StringWidth(value) > w {
		return c // scrolled: blocked on the unexported offset
	}
	runes := []rune(value)
	pos := min(max(ti.Position(), 0), len(runes))
	c.X = lipgloss.Width(prompt) + ansi.StringWidth(string(runes[:pos]))
	return c
}

// inputOrigin is the frame cell the input box's top-left corner was
// composited at. View records it while it stacks the chrome, because
// only View knows which parts it actually emitted — the help panel,
// the palette and the toast are all conditional, and each one that
// renders pushes the input box down a row.
type inputOrigin struct {
	x, y int
}

// stackedHeight is the number of terminal rows lipgloss.JoinVertical
// will spend on parts. Join pads blocks to the widest one but never
// changes a block's line count, so the row a later part starts on is
// just the sum of the heights before it.
func stackedHeight(parts []string) int {
	n := 0
	for _, p := range parts {
		n += lipgloss.Height(p)
	}
	return n
}

// stackColumn joins parts into a column exactly width cells wide,
// WITHOUT changing the row count. It is the other half of
// stackedHeight's contract and lives beside it for that reason: one
// predicts where a part lands, the other composes the column, and the
// prediction is only true if the composition cannot move a row.
//
// This used to be
//
//	lipgloss.NewStyle().Width(width).Render(lipgloss.JoinVertical(...))
//
// and the swap is issue #203 — the same substitution fitRow made for
// the modal body in #157, for the same reason. A width-setting
// lipgloss Style is a WORD-WRAPPER, not a padder: handed a row wider
// than the column it breaks it onto a second line, and the caller's
// row arithmetic — computed from the parts, before the join — never
// hears about it. Every part below the break shifts down by as many
// rows as the wrap added, which is a function of the CONTENT, so the
// error is not a constant anyone could correct for downstream.
//
// The row that overflows is a defect wherever it came from; what this
// function guarantees is only that it stays one row, so the geometry
// around it survives. Truncation is what buys that. Widening the
// column instead would push the sidebar off the right edge, and
// wrapping is exactly what is being removed.
func stackColumn(parts []string, width int) string {
	joined := lipgloss.JoinVertical(lipgloss.Left, parts...)
	if width <= 0 {
		return joined
	}
	rows := strings.Split(joined, "\n")
	for i, row := range rows {
		rows[i] = fitRow(row, width)
	}
	return strings.Join(rows, "\n")
}

// frameCursor resolves tea.View.Cursor for the frame View has just
// composed: the front-most modal's text surface when one is open,
// the chat textarea otherwise, and nil when nothing owns input.
//
// modal is the front-most modal's rendered block ("" when none is
// open). It is passed in rather than re-rendered because deriving
// the lipgloss.Place origin needs the block's measured size, and
// re-rendering a modal to measure it would double the per-frame cost
// of the most expensive thing View does.
//
// Called after clipFrame so the clamp is against the frame the
// operator will actually see.
func (m Model) frameCursor(origin inputOrigin, modal string) *tea.Cursor {
	c, covered := m.modalCursor(modal)
	if !covered {
		// No overlay: the frame is the chat layout, and the textarea
		// is the only thing on it that takes typed input.
		c = m.textareaCursor(origin)
	}
	return clampCursor(c, m.width, m.height)
}

// textareaCursor translates the chat textarea's widget-relative
// cursor into frame coordinates.
//
// Returns nil when the textarea is blurred (bubbles returns nil
// there), which is the "nothing should own the cursor" case for the
// base layout — better a hidden cursor than one parked on an
// arbitrary cell.
func (m Model) textareaCursor(origin inputOrigin) *tea.Cursor {
	c := m.input.Cursor()
	if c == nil {
		return nil
	}
	c.X += origin.x
	// +1 for renderInputBox's top rule: the box is one border row
	// followed by the textarea, so the textarea's row 0 is the box's
	// row 1.
	c.Y += origin.y + 1
	return c
}

// modalCursor returns the frame-absolute cursor for the front-most
// modal. covered reports whether a modal is on screen at all — the
// caller needs that separately from the cursor, because "a modal
// covers the frame and owns no caret" and "no modal is open" are
// different answers, and only the second one falls through to the
// chat textarea.
//
// The switch mirrors View's z-order cascade and has to stay in
// lockstep with it — same ordering rationale as handleEsc's cascade
// (update.go). Read-only modals (the /btw side answer, the read-only
// detail overlays, a permission prompt answered with y/n/s/v/t/a)
// return no cursor deliberately: there is nothing to type into them,
// so the terminal should hide the caret rather than park it on a
// border. The pickers left that set in #117 — their filter row
// implements DialogCursor, and without it this arm would answer
// (nil, true) for a surface the operator is typing CJK into.
func (m Model) modalCursor(modal string) (c *tea.Cursor, covered bool) {
	switch {
	case m.pendingForm != nil:
		// huh renders its own caret and exposes no tea.Cursor in the
		// pinned huh/v2 v2.0.3, so there is nothing to forward. Left
		// unset rather than guessed at: a wrong position is worse
		// than none for an IME.
		return nil, true
	case m.sideAnswer != nil:
		return nil, true // read-only viewer
	case m.overlayStack.HasDialogs():
		// An inline question covers nothing: it is drawn in the
		// transcript, not over the frame, so the caret belongs to the
		// composer exactly as it would with no modal open. Answering
		// covered=true here would hide the caret while the operator
		// can still see and use their own input box.
		if _, inline := m.overlayStack.inlineFront(); inline {
			return nil, false
		}
		c = m.overlayStack.cursor(m.width, &m)
	default:
		return nil, false
	}
	if c == nil {
		return nil, true
	}
	// Every arm above is drawn by compositeModal, which centres the
	// block with these two calls, so the modal's top-left cell is
	// derived from its own measured size. Both sides call
	// placeCenterOffset rather than each spelling out gap/2: the draw
	// origin and the caret origin have to agree exactly, and nothing
	// would catch them drifting apart — the golden corpus captures
	// tea.View.Content, not tea.View.Cursor.
	c.X += placeCenterOffset(m.width, lipgloss.Width(modal))
	c.Y += placeCenterOffset(m.height, lipgloss.Height(modal))
	return c, true
}

// placeCenterOffset is the leading padding lipgloss.Place adds when
// it centers an inner block of size inner inside outer.
//
// This is not an approximation of Place — it is the same arithmetic.
// Place splits the gap with math.Round on the TRAILING half
// (position.go: `split := round(gap * pos); left := gap - split`),
// which for Center (0.5) leaves exactly floor(gap/2) leading, on both
// axes. Returns 0 when the block is at least as large as the frame,
// matching Place's `gap <= 0` short-circuit.
func placeCenterOffset(outer, inner int) int {
	gap := outer - inner
	if gap <= 0 {
		return 0
	}
	return gap / 2
}

// clampCursor keeps a cursor inside the clipped frame (issue #102's
// post-pass runs immediately before this) and drops it entirely when
// its surface was clipped away.
//
// Rows and columns are treated differently on purpose:
//
//   - A row past the bottom was DELETED by clipFrame. The surface
//     that owned the cursor is not on screen at all, so nil — a
//     clamped-to-the-last-row cursor would be a stale position
//     pointing at unrelated content.
//   - A column past the right edge is clamped to the last cell. The
//     caret legitimately sits one cell PAST the final glyph while
//     typing, so nilling on x == width would blink the cursor out
//     exactly when the operator fills the line.
//
// A zero-size frame (pre-WindowSizeMsg, or a bare Model in a test)
// has no cell to point at, so it gets no cursor.
func clampCursor(c *tea.Cursor, width, height int) *tea.Cursor {
	if c == nil || width <= 0 || height <= 0 {
		return nil
	}
	if c.Y < 0 || c.Y >= height {
		return nil
	}
	if c.X < 0 {
		c.X = 0
	}
	if c.X >= width {
		c.X = width - 1
	}
	return c
}
