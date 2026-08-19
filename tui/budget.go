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

// The chrome row budget (issue #121).
//
// resize() used to size the chat viewport by subtracting every other
// row View composes from m.height, then flooring the remainder at
// chatMinHeight. That is correct only while the chrome is small
// enough to fit: the subtraction has no notion of a variable-height
// element yielding rows to a fixed one, so any chrome that wants more
// rows than the terminal has left simply takes them, the viewport
// bottoms out on its floor, and the composed frame runs past
// m.height. clipFrame then keeps the FIRST m.height rows — so the
// rows that disappear are the bottom ones: the input box and the
// footer.
//
// Three elements have that shape:
//
//   - The help panel. renderHelpPanel emits 38 rows at every width,
//     so `?` on an 80x24 terminal composed a 48-row frame and clipped
//     the input box and the footer out of it entirely.
//   - The input box, once #121's one-line fix lets syncInputHeight's
//     auto-grow survive resize: textareaMaxHeight is 15, which is
//     more rows than a short terminal can spare.
//   - The palette, at maxPaletteRows + its own chrome.
//
// The budget below replaces "subtract and floor" with an explicit
// allocation. Rows are handed out in priority order and every element
// is measured after it has been told what it may spend, so the sum is
// the frame height by construction rather than by hope.
//
// # Priority
//
// Highest first. The list is short because most of it is forced:
//
//  1. The input box's MINIMUM (its border plus textareaMinHeight).
//     An operator who cannot see the line they are typing has no way
//     to drive the TUI at all, so this outranks everything, including
//     the chat.
//  2. The footer. It is the only place the quit key is written down,
//     and it is one row on any sane width.
//  3. The header (StatusHeader only) and the toast. Both are small
//     and both tell the operator something about state they cannot
//     get anywhere else.
//  4. The chat viewport's floor, chatMinHeight, and one row for each
//     collapsible panel that is open — enough to say it is open.
//  5. The input box's GROWTH, up to textareaMaxHeight.
//  6. The palette.
//  7. The help panel.
//  8. Anything still unspent goes to the chat viewport.
//
// Two notes on that order, since #121 suggested a flatter one
// (input > footer > header > palette > help):
//
//   - The input box is split across 1 and 5. Ranking the whole box
//     first would let a 15-line paste erase the chat on a 20-row
//     terminal — the operator would watch their transcript vanish
//     while typing. Splitting it means the box grows into rows
//     nobody else needs, which is what "auto-grow" should mean, and
//     the chat keeps a readable floor while it does.
//   - The chat floor is inserted at 4, above the two collapsible
//     panels. The palette and the help panel are transient overlays
//     the operator opened a moment ago and can close with one key;
//     the transcript is the thing they are actually working in. So a
//     tall textarea does NOT get to push the chat below
//     chatMinHeight, but the help panel does get truncated to keep
//     the chat at it.
//
// Items 2 and 3 are unshrinkable: View draws the header, the toast
// and the footer at whatever height they render to, and resize has no
// lever to make them smaller. Their position in the list is therefore
// documentation rather than mechanism — they are reserved off the top
// and the ordering between them never has to be applied. Items 1 and
// 4-7 are the ones resize can actually control, via the textarea
// height, the viewport height, and the two panel caps recorded in the
// budget.
//
// # When even the minimum does not fit
//
// On a terminal shorter than items 1-4 combined (a 24-column, 4-row
// pane, say) no allocation can win: the header, the footer and the
// input box together are already taller than the screen. The budget
// records the shortfall in overflow and clipFrame stays the net for
// exactly that case — which is the contract clipFrame's doc comment
// describes. overflow is 0 in every terminal an operator would
// plausibly use, and the row-budget tests assert it cell by cell.

package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// chromeBudget is one resize()'s worth of row accounting: how many
// rows each element View stacks around the chat viewport is allowed
// to occupy, and how many it actually took.
//
// The zero value means "never sized" — the panel renderers read the
// caps directly and treat 0 as "no ceiling", so a model that has not
// been through resize() renders exactly as it did before the budget
// existed.
type chromeBudget struct {
	// Measured heights of what View composes, in stacking order.
	// They sum to frameRows(), which is m.height whenever the
	// terminal can hold the chrome at all.
	header  int
	chat    int
	palette int
	help    int
	input   int
	toast   int
	footer  int

	// helpCap / paletteCap are the ceilings handed to the two
	// collapsible panels. The renderers clamp themselves to these,
	// so measuring afterwards can never come back higher. 0 = no
	// ceiling (the panel is closed, or the model was never sized).
	helpCap    int
	paletteCap int

	// inputMax is the tallest textarea the budget can afford —
	// textareaMaxHeight on a roomy terminal, less on a short one.
	// syncInputHeight clamps to it so a keystroke cannot ask for a
	// height resize would only take away again. 0 = no ceiling.
	inputMax int

	// overflow is how many rows the irreducible chrome (priorities
	// 1-4) exceeds the terminal by. Non-zero means the frame cannot
	// fit and clipFrame will trim it; it is the only case in which
	// clipFrame is expected to fire.
	overflow int
}

// frameRows is the height of the frame this budget describes.
func (b chromeBudget) frameRows() int {
	return b.header + b.chat + b.palette + b.help + b.input + b.toast + b.footer
}

// allocateChrome hands out m.height's rows in the priority order
// documented above and returns the resulting budget. It is the whole
// of resize()'s row arithmetic; resize itself only sets the widths
// first (renderInputBox reads m.viewport.Width(), and every
// wrap-sensitive measurement below depends on the width it renders
// at) and applies the result.
//
// Mutating: it sizes the textarea and records the panel caps, because
// every term is a MEASUREMENT of the string View will render and the
// two collapsible panels cannot be measured until they have been told
// what they may spend. Nothing else in the model is touched.
//
// chromeWidth is the column the sibling chrome renders in — chatWidth
// in sidebar mode (View wraps the whole left stack to the chat
// column), m.width in header mode.
func (m *model) allocateChrome(layout StatusLayout, chromeWidth int) chromeBudget {
	var b chromeBudget

	// --- Unshrinkable chrome, reserved off the top -----------------
	//
	// Measured, never assumed (issue #103): renderHeader word-wraps
	// the status line to as many as 6 rows on a narrow terminal, and
	// the footer wraps too.
	if layout == StatusHeader {
		b.header = lipgloss.Height(m.renderHeader())
	}
	b.footer = lipgloss.Height(m.renderFooter(chromeWidth))
	if b.footer < 1 {
		b.footer = 1
	}
	// View slots the wake banner between the input box and the footer
	// whenever renderToast returns non-empty, so the budget is guarded
	// on exactly that condition rather than on m.toast != "" — the
	// renderer also applies the TTL and the sticky-slash bypass.
	if toast := m.renderToast(chromeWidth); toast != "" {
		b.toast = lipgloss.Height(toast)
	}

	free := m.height - b.header - b.footer - b.toast

	// --- Priority 4: the chat floor and the open panels' one row ---
	//
	// Reserved before the input box is allowed to GROW into them (the
	// box's minimum was already taken off the top by fitInputBox's
	// own floor). An open panel never gets zero rows: it would then
	// have no way to say it is open, and a 0 cap reads as "no
	// ceiling" to the renderers.
	reserved := chatMinHeight
	if m.palette != nil {
		reserved++
	}
	if m.helpOpen {
		reserved++
	}

	// --- Priority 1 + 5: the input box -----------------------------
	//
	// Grows to its line count, but only into rows nothing above it
	// reserved. Below its own minimum it does not go.
	b.input, b.inputMax = m.fitInputBox(free - reserved)
	free -= b.input

	b.chat = chatMinHeight
	free -= chatMinHeight

	// --- Priority 6 + 7: the collapsible panels --------------------
	//
	// Each gets what is left, and shrinks (palette: fewer visible
	// rows; help: elided) rather than overflowing. The one row an open
	// panel always keeps was reserved above; when there was nothing to
	// reserve it from, it is charged to overflow like any other
	// shortfall.
	//
	// Each cap has to be live on the model before its panel is
	// measured — the renderers read them off m.chrome — and the
	// palette is measured before the help panel's cap is computed so
	// help is offered the rows the palette did not want.
	if m.palette != nil {
		b.paletteCap = atLeast(free, 1)
		m.chrome.paletteCap = b.paletteCap
		b.palette = lipgloss.Height(m.renderPalette(chromeWidth))
		free -= b.palette
	}
	if m.helpOpen {
		b.helpCap = atLeast(free, 1)
		m.chrome.helpCap = b.helpCap
		b.help = lipgloss.Height(m.renderHelpPanel(chromeWidth))
		free -= b.help
	}

	// --- Priority 8: the remainder is the chat's --------------------
	//
	// No queue term anywhere above. renderQueuePanel is appended by
	// renderInProgress into the viewport's CONTENT, not joined beside
	// the viewport by View — unlike the footer / help / palette, which
	// are siblings. Charging it shrank the viewport by the panel's
	// height AND then spent that height rendering the panel inside the
	// now-smaller viewport, so a 4-row queue cost the operator 4 rows
	// of chat and left the frame 4 rows short of the terminal (issue
	// #103). The same goes for the spinner line and the in-progress
	// assistant block.
	if free > 0 {
		b.chat += free
	} else {
		b.overflow = -free
	}
	return b
}

// fitInputBox sizes the textarea to its content and returns the
// rendered box height plus the tallest textarea maxRows can afford.
//
// The box is the textarea plus renderInputBox's top rule, and the
// rule's height is derived by measurement rather than assumed to be
// one row — the same rule the rest of the budget follows. maxRows may
// be zero or negative on a terminal too short to hold the chrome; the
// box still gets its minimum, which is the one thing the budget will
// not take away (priority 1).
func (m *model) fitInputBox(maxRows int) (rows, inputMax int) {
	want := m.input.LineCount()
	if want < textareaMinHeight {
		want = textareaMinHeight
	}
	if want > textareaMaxHeight {
		want = textareaMaxHeight
	}
	m.input.SetHeight(want)
	rows = lipgloss.Height(m.renderInputBox())

	// Rows the box spends on chrome rather than on text.
	border := rows - want
	inputMax = atLeast(maxRows-border, textareaMinHeight)
	if inputMax > textareaMaxHeight {
		inputMax = textareaMaxHeight
	}
	if want > inputMax {
		m.input.SetHeight(inputMax)
		rows = lipgloss.Height(m.renderInputBox())
	}
	return rows, inputMax
}

// fitPanelRows clamps a rendered panel to maxRows, replacing the last
// row it keeps with a marker saying how many were dropped. Used by
// the two collapsible panels to hold themselves inside the ceiling
// the budget gave them (0 = no ceiling).
//
// The marker is deliberately a truncation notice and not a scrollbar:
// a panel that does not fit its column is a defect in the panel, and
// for the help panel that defect is issue #119's to fix. This is the
// mechanism that stops it reaching the frame, not a UI for living
// with it.
func (m model) fitPanelRows(lines []string, maxRows, width int) []string {
	if maxRows <= 0 || len(lines) <= maxRows {
		return lines
	}
	dropped := len(lines) - maxRows + 1
	marker := m.styles.Muted.Render(fmt.Sprintf("  %s %d more rows", GlyphTruncate, dropped))
	if width > 0 && ansi.StringWidth(marker) > width {
		marker = ansi.Truncate(marker, width, "")
	}
	if maxRows == 1 {
		return []string{marker}
	}
	out := make([]string, 0, maxRows)
	out = append(out, lines[:maxRows-1]...)
	return append(out, marker)
}

// joinPanelRows is fitPanelRows plus the join every panel renderer
// ends with.
func (m model) joinPanelRows(lines []string, maxRows, width int) string {
	return strings.Join(m.fitPanelRows(lines, maxRows, width), "\n")
}

// atLeast is a floor helper; the budget clamps a lot of subtractions.
func atLeast(v, floor int) int {
	if v < floor {
		return floor
	}
	return v
}
