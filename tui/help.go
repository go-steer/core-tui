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

// The help panel's layout (issue #119).
//
// renderHelpPanel used to emit the same 38 rows at every terminal
// size. Its width argument reached exactly two places — the `<= 0`
// guard and the horizontal rule — so the key rows were a constant
// `"    " + key(padded to 24) + description`, 81 cells at the widest,
// independent of the column they were drawn in. Two defects came out
// of that:
//
//   - **Height.** 38 rows do not fit an 80x24 terminal. Before the
//     chrome budget (issue #121) they pushed the composed frame to 48
//     rows and clipFrame kept the FIRST 24, so pressing `?` deleted
//     the input box and the footer from the frame. The budget stopped
//     the eviction, but the panel still overflowed its allowance and
//     got blunt-elided with a `… N more rows` marker.
//   - **Width.** An 81-cell row in an 80-column terminal, and in a
//     40-column one it was twice the terminal. It also made the
//     budget itself wrong: helpRows is measured with lipgloss.Height,
//     which counts a visually-wrapped row as one.
//
// This file replaces the constant with a layout that is a function of
// both dimensions.
//
// # Width
//
// helpKeyCol derives the key column from the data and the width
// rather than hardcoding 24, and descriptions WRAP into the remainder
// with a hanging indent instead of running off the edge. Below
// helpDescMin columns of remainder the aligned form stops being
// readable at all, so the panel switches to a stacked form — key on
// its own row, description indented under it. Every row is measured
// against the column it is drawn in, so lipgloss.Height and the
// terminal agree and the budget's arithmetic is sound.
//
// # Height: pagination, on the panel's own key
//
// m.chrome.helpCap is the row ceiling the budget hands the panel.
// Laying out TO it beats being clipped BY it, and of the mechanisms
// available:
//
//   - **Scrolling** (dialog_scroll.go) is the engine the modals use,
//     and it needs keys. The panel is not a modal — it is stacked
//     under the chat and the operator keeps typing while it is open —
//     so the only scroll keys not already claimed by the textarea are
//     pgup / pgdn, which are the chat's. Taking them would make the
//     panel's own Navigation section ("pgup / pgdn — scroll chat")
//     into exactly the kind of lie issue #119's second half is about.
//   - **Dropping sections** answers the height question by deleting
//     the content, which is what the elision marker already does.
//   - **A compact columnar summary** halves the row count at best,
//     and it buys that by halving the column each description gets —
//     it trades the height defect straight back for the width one.
//   - **Pagination** needs no new key: `?` already opens the panel
//     and only fires when the input is empty, so it can walk the
//     pages and then close, and the title row says which page you are
//     on and which key advances it. Nothing is dropped, the chat
//     keeps its scroll keys, and every page is built to fit the cap
//     rather than being trimmed to it.
//
// Pages break on section boundaries wherever they can: sections are
// packed greedily and a section that would straddle a break starts
// the next page instead. A page therefore usually comes in UNDER the
// cap, and the rows it leaves on the table go back to the chat
// viewport — the budget measures the panel after rendering it, so
// unspent help rows are transcript rows. Only a section taller than a
// whole page is split mid-way, and its continuation carries no
// repeated heading so the rows under a heading are always that
// section's.
//
// fitPanelRows still terminates the render. With a cap of 4 or more
// it is inert by construction; it stays as the backstop for the
// degenerate caps (1-3 rows) where the two rules and the title alone
// do not fit.

package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	// helpIndent is the column every key row starts at.
	helpIndent = 4

	// helpStackIndent is where a stacked description sits — one step
	// in from its key, so the pairing survives without alignment.
	helpStackIndent = 6

	// helpKeyColMax is the widest key column the aligned form will
	// use. The longest key string ("↑↓ / pgup / pgdn / mouse wheel")
	// is 29 cells; aligning all 22 rows to it would push every
	// description five columns right to spare one row a two-space
	// gap, so the column is capped and that one row runs long.
	helpKeyColMax = 24

	// helpDescMin is the narrowest description column worth aligning
	// to. Below it the aligned form wraps nearly every description
	// into three or four ragged rows, and the stacked form — full
	// width for the description, key on its own row — is both shorter
	// and easier to read.
	helpDescMin = 24

	// helpChromeRows is what the panel spends on itself: the top
	// rule, the title, and the bottom rule. Subtracted from the cap
	// to get the per-page content budget.
	helpChromeRows = 3

	// helpWrapMin is the narrowest column prose is wrapped into. Below
	// it a wrap stops being a wrap and becomes a vertical stack of
	// syllables, so the description is truncated to a single row
	// instead — which says as much and costs one row rather than
	// twenty.
	helpWrapMin = 12

	// helpMinBodyRows is the shortest page worth paging through. A
	// two-row window is not a page, it is a peephole: the panel would
	// come to twenty-odd pages and `?` would stop being a key and
	// start being a chore. Below this the panel does not paginate and
	// fitPanelRows' elision marker is the honest answer — which is
	// also all a terminal of fifteen rows or fewer can be told.
	helpMinBodyRows = 3
)

// helpSection is one titled group of key/description pairs.
type helpSection struct {
	title string
	keys  [][2]string
}

// helpSections is the panel's content. Hoisted out of the renderer so
// the layout code and the tests can both walk it — the Navigation
// rows in particular are asserted key by key against the real
// bindings (TestHelpPanel_NavigationKeysAreAllBound).
func helpSections() []helpSection {
	return []helpSection{
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
			{"↑↓ / pgup / pgdn", "scroll a long answer"},
			{"esc / enter / space", "dismiss modal (answer doesn't land in history)"},
		}},
		{"Navigation", [][2]string{
			{"pgup / pgdn", "scroll chat"},
			{"ctrl+l", "jump to top (stops following the stream)"},
			{"end", "jump to bottom, when the input is empty (resumes following)"},
		}},
		{"Transcript focus", [][2]string{
			{"tab", "move the keyboard between composer and transcript"},
			{"↑ / ↓ / k / j", "move the selection an item at a time"},
			{"space", "fold / unfold the selected item"},
			{"shift+↑ / shift+↓", "scroll a line inside a long item"},
			{"shift+← / shift+→", "pan sideways over a wide table or diff"},
			{"y / c", "copy the item / just the code in it, to the clipboard"},
			{"(osc 52)", "a copy is sent to the terminal, which has to allow clipboard writes"},
			{"g / G", "first / last item (G resumes following the stream)"},
			{"enter", "back to the composer (esc does too)"},
		}},
		{"Layout & mode", [][2]string{
			{"ctrl+b", "toggle header / sidebar"},
			{"shift+tab", "cycle permission mode"},
		}},
		{"Modals", [][2]string{
			{"ctrl+g", "model picker (when ModelSwapper is wired)"},
			{"ctrl+x", "expand a tool call (args + response detail)"},
			{"↑↓ / pgup / pgdn / mouse wheel", "scroll any modal body"},
			{"esc", "close / cancel any open modal"},
		}},
		{"Interrupt / quit", [][2]string{
			{"esc", "interrupt in-flight turn (doesn't clear queue)"},
			{"ctrl+c, ctrl+d", "exit"},
		}},
	}
}

// renderHelpPanel renders the bottom-anchored stacked help panel when
// m.helpOpen is true. Returns empty string when closed so callers can
// conditionally include it without branching on `if helpOpen` in the
// View() composition. Width sets the column width — pass chatWidth in
// sidebar mode and m.width in header mode.
//
// One page of the panel, laid out to width and to the row cap the
// chrome budget recorded in m.chrome.helpCap (0 = unsized model or no
// ceiling, in which case the whole panel is one page). See the file
// comment for why the height mechanism is pagination.
func (m Model) renderHelpPanel(width int) string {
	if !m.helpOpen || width <= 0 {
		return ""
	}
	pages := m.helpPages(width)
	if len(pages) == 0 {
		return ""
	}
	page := min(nonNeg(m.helpPage), len(pages)-1)

	rule := m.styles.Rule.Render(strings.Repeat(GlyphRule, width))
	lines := make([]string, 0, len(pages[page])+helpChromeRows)
	lines = append(lines, rule, m.helpTitle(page, len(pages), width))
	lines = append(lines, pages[page]...)
	lines = append(lines, rule)
	return m.joinPanelRows(lines, m.chrome.helpCap, width)
}

// helpPages lays the whole panel out at width and packs it into pages
// that fit the current cap. Always at least one page for a non-empty
// panel; a cap of 0 means "no ceiling" and yields exactly one.
func (m Model) helpPages(width int) [][]string {
	sections := helpSections()
	keyCol := helpKeyCol(sections, width)
	blocks := make([][]string, 0, len(sections))
	for _, sec := range sections {
		blocks = append(blocks, m.helpSectionRows(sec, width, keyCol))
	}
	return paginateHelp(blocks, helpBodyRows(m.chrome.helpCap))
}

// helpPageCount is how many pages the panel currently has, at the
// width View will render it in. Read by advanceHelp to know when `?`
// has reached the end and should close the panel instead.
func (m Model) helpPageCount() int {
	width := m.chromeWidth()
	if width <= 0 {
		return 1
	}
	return atLeast(len(m.helpPages(width)), 1)
}

// advanceHelp is the whole of the `?` key: it opens the panel, then
// walks it a page at a time, then closes it. Reusing the opening key
// for paging is what lets the panel be height-aware without taking
// pgup / pgdn away from the chat (see the file comment).
func (m *Model) advanceHelp() {
	if !m.helpOpen {
		m.helpOpen, m.helpPage = true, 0
		return
	}
	// The cap moves with the terminal, so a page index that was valid
	// when it was set can be past the end after a resize shrank the
	// panel. Clamp before advancing — the operator is looking at the
	// clamped page, not the stored one.
	pages := m.helpPageCount()
	if m.helpPage >= pages-1 {
		m.closeHelp()
		return
	}
	m.helpPage++
}

// closeHelp shuts the panel and forgets the page. Called from the
// last `?` of the cycle and from Esc's cascade — reopening always
// starts at page one.
func (m *Model) closeHelp() {
	m.helpOpen, m.helpPage = false, 0
}

// helpBodyRows is the per-page content budget: the cap less what the
// panel spends on its own rules and title. Returns 0 — "do not
// paginate" — for a panel with no ceiling (an unsized model) and for
// a ceiling too short to leave a navigable page behind, where
// fitPanelRows takes over.
func helpBodyRows(rowCap int) int {
	if rows := rowCap - helpChromeRows; rowCap > 0 && rows >= helpMinBodyRows {
		return rows
	}
	return 0
}

// paginateHelp packs section blocks into pages of at most bodyRows,
// breaking on section boundaries. bodyRows <= 0 returns a single page
// carrying everything.
//
// One page always beats two, so before splitting anything it tries
// the panel again without the blank rows between sections: the
// headings separate the sections perfectly well on their own, and six
// blank rows are frequently the entire difference between a panel
// that fits a 50-row terminal and one that does not.
//
// A section that will not fit the rows left on the current page moves
// to the next one whole rather than straddling the break, so a page
// usually comes in under the cap and hands the difference back to the
// chat viewport. Only a section taller than an entire page is split,
// and then without repeating its heading, so every row that follows a
// heading belongs to that heading's section.
func paginateHelp(blocks [][]string, bodyRows int) [][]string {
	if len(blocks) == 0 {
		return nil
	}
	if spaced := flattenHelp(blocks, true); bodyRows <= 0 || len(spaced) <= bodyRows {
		return [][]string{spaced}
	}
	if compact := flattenHelp(blocks, false); len(compact) <= bodyRows {
		return [][]string{compact}
	}

	var (
		pages [][]string
		page  []string
	)
	flush := func() {
		if len(page) > 0 {
			pages = append(pages, page)
			page = nil
		}
	}
	for _, block := range blocks {
		// A blank row separates sections that share a page; the first
		// section on a page does not pay for one.
		gap := 0
		if len(page) > 0 {
			gap = 1
		}
		if len(page)+gap+len(block) > bodyRows {
			flush()
			gap = 0
			for len(block) > bodyRows {
				pages = append(pages, block[:bodyRows])
				block = block[bodyRows:]
			}
			if len(block) == 0 {
				continue
			}
		}
		if gap > 0 {
			page = append(page, "")
		}
		page = append(page, block...)
		if len(page) >= bodyRows {
			flush()
		}
	}
	flush()
	return pages
}

// flattenHelp concatenates every section block into one body, with or
// without a blank row between sections.
func flattenHelp(blocks [][]string, spaced bool) []string {
	var all []string
	for i, block := range blocks {
		if i > 0 && spaced {
			all = append(all, "")
		}
		all = append(all, block...)
	}
	return all
}

// helpKeyCol is the column descriptions start at in the aligned form,
// measured from the end of the row indent. Derived from the widest
// key actually present so the data can change without a constant
// going stale, capped at helpKeyColMax, and never allowed past half
// the available width — on a narrow terminal a key column that eats
// the description column is the width bug in a different costume.
func helpKeyCol(sections []helpSection, width int) int {
	widest := 0
	for _, sec := range sections {
		for _, kv := range sec.keys {
			if w := lipgloss.Width(kv[0]); w > widest {
				widest = w
			}
		}
	}
	col := min(widest+2, helpKeyColMax)
	return atLeast(min(col, (width-helpIndent)/2), 1)
}

// helpSectionRows renders one section — its heading plus its key rows
// — into the column width.
func (m Model) helpSectionRows(sec helpSection, width, keyCol int) []string {
	rows := []string{fitRowWidth("  "+m.styles.SidebarHeading.Render(sec.title), width)}
	stacked := width-helpIndent-keyCol < helpDescMin
	for _, kv := range sec.keys {
		rows = append(rows, m.helpKeyRows(kv[0], kv[1], width, keyCol, stacked)...)
	}
	return rows
}

// helpKeyRows renders one key/description pair, wrapping the
// description into whatever column is left and hanging the
// continuations under its first line.
//
// The segments are styled individually and concatenated rather than
// rendered as one sized lipgloss block: the surrounding chrome does
// the same, deliberately, so that an inner reset cannot kill the
// outer color for the rest of the row.
func (m Model) helpKeyRows(key, desc string, width, keyCol int, stacked bool) []string {
	indent := strings.Repeat(" ", helpIndent)
	styledKey := indent + m.styles.AssistantText.Bold(true).Render(key)

	if stacked {
		rows := []string{fitRowWidth(styledKey, width)}
		for _, line := range helpWrap(desc, width-helpStackIndent) {
			rows = append(rows, strings.Repeat(" ", helpStackIndent)+m.styles.Muted.Render(line))
		}
		return rows
	}

	pad := atLeast(keyCol-lipgloss.Width(key), 1)
	start := helpIndent + lipgloss.Width(key) + pad
	lines := helpWrap(desc, width-start)
	if len(lines) == 0 {
		return []string{fitRowWidth(styledKey, width)}
	}
	rows := []string{styledKey + strings.Repeat(" ", pad) + m.styles.Muted.Render(lines[0])}
	for _, line := range lines[1:] {
		rows = append(rows, strings.Repeat(" ", start)+m.styles.Muted.Render(line))
	}
	return rows
}

// helpTitle is the panel's first row: what it is, and — when it has
// more than one page — where the operator is in it and which key
// moves. A single-page panel keeps the original wording, so the
// paging vocabulary only appears on a terminal that needs it.
func (m Model) helpTitle(page, pages, width int) string {
	hint := "(? to close)"
	if pages > 1 {
		hint = fmt.Sprintf("(page %d/%d %s ? next %s esc close)",
			page+1, pages, GlyphSeparator, GlyphSeparator)
	}
	return fitRowWidth(m.styles.Accent.Render("Help")+"  "+m.styles.Muted.Render(hint), width)
}

// helpWrap word-wraps a description to width, hard-breaking a word
// too long to fit rather than letting it overhang. Returns nil when
// there is no column left to wrap into, which drops the description
// and leaves the key — the last thing worth showing.
func helpWrap(s string, width int) []string {
	switch {
	case width <= 0:
		return nil
	case width < helpWrapMin:
		return []string{ansi.Truncate(s, width, GlyphTruncate)}
	default:
		return strings.Split(ansi.Wrap(s, width, " -"), "\n")
	}
}

// fitRowWidth is the per-row width backstop: rows are built to fit,
// so this only fires on a terminal narrower than a single styled
// token (a section heading, mostly). Truncating here rather than
// leaving it to clipFrame keeps lipgloss.Height honest, which is what
// the chrome budget measures the panel with.
func fitRowWidth(row string, width int) string {
	if width > 0 && ansi.StringWidth(row) > width {
		return ansi.Truncate(row, width, GlyphTruncate)
	}
	return row
}
