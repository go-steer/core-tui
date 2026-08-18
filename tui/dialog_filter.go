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

// The pickers' type-to-filter row (issue #117).
//
// Typing a letter into the model, session or theme picker used to do
// nothing: all three HandleKey methods were a four-case switch over
// up / down / enter / esc with a catch-all that consumed everything
// else so it could not leak into the chat textarea behind the modal.
// On a host advertising forty models, or a project with a hundred
// sessions, the arrow keys were the only navigation there was.
//
// This file is the part all three share: one line of text input, the
// match count beside it, the caret inside it, and the highlight for
// the span the filter matched. What each picker keeps for itself is
// the list it narrows and what Enter means — see rows() in each.
//
// Two deliberate shapes:
//
//   - The input is a real bubbles textinput fed real
//     tea.KeyPressMsg values through keyMsgDialog, not
//     dialog.HandleKey. HandleKey receives a NORMALIZED stroke
//     ("ctrl+u", "shift+enter"), which drops Key.Text — the grapheme
//     the terminal actually delivered, which is the one thing a text
//     input needs — and cannot carry a bracketed paste.
//   - The filter row costs one body row, paid for by passing
//     modalChromeRows+1 at the picker call sites rather than by
//     changing the const. modalChromeRows is shared by four modals
//     and by view.go in three places; only these three grew a row.

package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// filterPromptRail marks where typing lands, matching the text-input
// dialog's rail so the two surfaces read as the same widget.
const filterPromptRail = "▎ "

// filterPlaceholder is the affordance: the pickers had no visible
// hint that typing did anything, because it didn't.
const filterPlaceholder = "type to filter"

// pickerFilter is the filter row embedded by the three picker
// dialogs. The zero value is not usable — construct with
// newPickerFilter, which focuses the input and turns off the virtual
// cursor (issue #105: the terminal owns the caret).
type pickerFilter struct {
	input textinput.Model

	// styled / styledDark / styledTheme cache which palette the
	// input's styles were built for, so render only rebuilds them on
	// an actual theme flip. The theme picker previews on every cursor
	// move, so this one is not hypothetical.
	styled      bool
	styledDark  bool
	styledTheme string
}

func newPickerFilter() pickerFilter {
	ti := textinput.New()
	ti.Prompt = filterPromptRail
	ti.Placeholder = filterPlaceholder
	ti.SetVirtualCursor(false)
	_ = ti.Focus()
	return pickerFilter{input: ti}
}

// value is the active filter: the buffer with surrounding whitespace
// trimmed, because a trailing space the operator has not noticed
// would silently match nothing.
func (f *pickerFilter) value() string { return strings.TrimSpace(f.input.Value()) }

// active reports whether anything is being filtered right now.
func (f *pickerFilter) active() bool { return f.value() != "" }

// handleKeyMsg feeds an editing keystroke to the input and reports
// whether the buffer changed, which is the pickers' signal to
// re-clamp their cursor against a list that may have just shrunk.
func (f *pickerFilter) handleKeyMsg(msg tea.KeyPressMsg) (cmd tea.Cmd, changed bool) {
	before := f.input.Value()
	f.input, cmd = f.input.Update(msg)
	return cmd, f.input.Value() != before
}

// pickerNavStroke reports whether a stroke belongs to the picker's
// list rather than to the filter input. Everything else — every
// printable grapheme, backspace, the word-kill and line-kill bindings
// the widget already implements — goes to the input.
//
// Deliberately small. Home / End and the page keys are left to the
// input so the caret can move inside a long filter; the list is
// navigated with the arrows and their ctrl+p / ctrl+n twins, exactly
// as it was before this file existed.
func pickerNavStroke(stroke string) bool {
	switch stroke {
	case "up", "down", "ctrl+p", "ctrl+n", "enter", "esc":
		return true
	}
	return false
}

// render returns the filter row: the input box, and once a filter is
// active a right-aligned "matched/total" count — the palette's
// match-count affordance, which the pickers never had.
//
// width is the DIALOG width; the row is laid out inside the modal's
// box edge and the one column of padding within it. The widget
// paints prompt + Width() + 1 cells (the trailing cell is the caret's
// own), so the count's column is reserved out of the width handed to
// SetWidth rather than trimmed off afterwards.
func (f *pickerFilter) render(width, matched, total int, s styleSet) string {
	f.syncStyles(s)

	inner := modalInnerWidth(width)
	count := ""
	if f.active() {
		count = fmt.Sprintf("%d/%d", matched, total)
	}
	reserved := ansi.StringWidth(count)
	if reserved > 0 {
		reserved += 2 // gutter between the box and the count
	}
	f.input.SetWidth(nonNeg(inner - lipgloss.Width(f.input.Prompt) - 1 - reserved))

	// Belt and braces on the width. The widget scrolls a too-long
	// value to its box in handleOverflow, which runs from Update() —
	// so a value that arrived in one gulp (a paste, an Initial) is
	// still full-length on the frame that first sizes the box, and
	// would paint past the dialog's edge exactly once. Cutting it
	// here keeps the row one row wide in every frame rather than
	// relying on a widget invariant we do not control.
	line := ansi.Truncate(f.input.View(), nonNeg(inner-reserved), "")
	if count == "" {
		return line
	}
	pad := nonNeg(inner - ansi.StringWidth(line) - ansi.StringWidth(count))
	return line + strings.Repeat(" ", pad) + s.Muted.Render(count)
}

// cursor is the caret inside the filter row, relative to the row's
// own first cell. Goes through textInputCursor rather than the widget
// so the caret is measured in cells (issue #125) — which is the whole
// point of putting a caret here at all, since a picker filter is
// exactly where someone types CJK and needs the IME anchored.
func (f *pickerFilter) cursor() *tea.Cursor {
	return textInputCursor(f.input, f.input.Prompt)
}

// filterRowCursor lifts a filter caret into dialog-relative
// coordinates. renderContext's chrome is fixed — the box edge, one
// column of horizontal padding, a title line and a blank row above
// the body — and the filter row is always the FIRST row of the body,
// so modalContentX / modalBodyTop are the whole offset, for all three
// pickers.
func filterRowCursor(c *tea.Cursor) *tea.Cursor {
	if c == nil {
		return nil
	}
	c.X += modalContentX
	c.Y += modalBodyTop
	return c
}

// syncStyles rebuilds the input's palette from the active theme.
func (f *pickerFilter) syncStyles(s styleSet) {
	if f.styled && f.styledDark == s.Dark && f.styledTheme == s.Theme.Name {
		return
	}
	f.styled = true
	f.styledDark = s.Dark
	f.styledTheme = s.Theme.Name
	f.input.SetStyles(textInputStyles(s))
}

// highlightSpan renders text under base with the span filter matched
// picked out. Returns base.Render(text) unchanged when nothing
// matched, which covers the empty filter.
//
// The emphasis is bold + underline rather than a colour swap so it
// composes with whichever base the row is using — the selected row is
// already accent-coloured, and a second colour on top of it would
// read as a third state.
//
// One span, not a scatter of runes: the ranker matches contiguous
// substrings only (rank.go). The issue's "matched runes" wording
// describes fuzzy-subsequence highlighting, which is a different
// matcher; evaluating one for the palette and the pickers together is
// a follow-up.
func highlightSpan(text, filter string, base lipgloss.Style) string {
	start, end, ok := matchSpan(text, filter)
	if !ok {
		return base.Render(text)
	}
	hi := base.Bold(true).Underline(true)
	return base.Render(text[:start]) + hi.Render(text[start:end]) + base.Render(text[end:])
}

// quoteFilter renders the active filter for a picker's "nothing
// matched" line. Quoted because a filter of a single space, or one
// the operator has stopped looking at, is otherwise invisible in the
// sentence.
func quoteFilter(filter string) string { return "“" + filter + "”" }

// pickerKey is the string the ranker matches a picker row against.
//
// It is the row's visible label, plus the row's ID when the host
// advertises one that differs from it. A host that sets Display to
// "GPT-4o" for the ID "openai/gpt-4o" means both for the operator to
// read, and typing either has to find the row — filtering on the
// label alone would make an ID printed right there on screen
// unsearchable.
func pickerKey(display, id string) string {
	switch {
	case display == "":
		return id
	case id == "" || id == display:
		return display
	default:
		return display + " " + id
	}
}

// clampIndex keeps a list cursor inside [0, n). Returns 0 for an
// empty list, which is the state a filter can put a picker into
// between one keystroke and the next: the model and theme pickers
// wrapped with (idx ± 1 + len) % len, which is a division by zero the
// moment the list they index is empty, and was only ever safe because
// before this the list could not shrink under them.
func clampIndex(idx, n int) int {
	if n <= 0 || idx < 0 {
		return 0
	}
	if idx >= n {
		return n - 1
	}
	return idx
}

// stepIndex moves a list cursor by delta with wraparound, clamping
// first so a stale index from a larger list cannot escape the range.
// An empty list stays at 0 rather than panicking.
func stepIndex(idx, delta, n int) int {
	if n <= 0 {
		return 0
	}
	idx = clampIndex(idx, n)
	return ((idx+delta)%n + n) % n
}
