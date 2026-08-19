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

// Help-panel layout tests (issue #119).
//
// The panel used to emit 38 identical rows at every terminal size:
// 81 cells wide in an 80-column terminal, and 14 rows taller than an
// 80x24 one has to spare. The gates here are the two properties that
// were missing — every row fits the column it is drawn in, and the
// panel fits the row cap the chrome budget hands it — plus the
// content property those two must not be bought with: paging through
// the panel still shows every key it documents.

package tui

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// helpGridWidths / helpGridHeights sweep the panel across the sizes
// that matter to it: 40 and 60 are where the aligned key column stops
// fitting and the stacked form takes over, 24 rows is the terminal
// the original defect was measured on, and 10 rows is short enough
// that the cap leaves no navigable page at all.
var (
	helpGridWidths  = []int{40, 60, 80, 100, 120, 160, 200}
	helpGridHeights = []int{10, 16, 24, 40, 60}
)

// openHelp builds a model of the given size with the panel open and
// the layout reconciled, exactly as the `?` key leaves it.
func openHelp(layout StatusLayout, w, h int) model {
	m := newFrameModel(layout, w, h)
	m = withHostileTranscript(m)
	m.advanceHelp()
	m.resize()
	m.refreshViewport()
	return m
}

// helpRows renders the panel at the width View would use and returns
// its rows.
func helpRows(m model) []string {
	panel := m.renderHelpPanel(m.chromeWidth())
	if panel == "" {
		return nil
	}
	return strings.Split(panel, "\n")
}

// TestHelpPanel_RowsFitTheColumn is the width half of issue #119. The
// key rows were `"    " + key(padded to 24) + description` — a
// constant independent of the terminal, 81 cells at the widest. It
// also made the chrome budget wrong: helpRows is measured with
// lipgloss.Height, so a row that visually wraps but measures as one
// row is a row the budget never charged for.
func TestHelpPanel_RowsFitTheColumn(t *testing.T) {
	for _, lay := range []struct {
		name   string
		layout StatusLayout
	}{{"header", StatusHeader}, {"sidebar", StatusSidebar}} {
		for _, w := range helpGridWidths {
			for _, h := range helpGridHeights {
				name := lay.name + "/" + strconv.Itoa(w) + "x" + strconv.Itoa(h)
				t.Run(name, func(t *testing.T) {
					m := openHelp(lay.layout, w, h)
					width := m.chromeWidth()
					for page := 0; page < m.helpPageCount(); page++ {
						m.helpPage = page
						for i, row := range helpRows(m) {
							if got := ansi.StringWidth(row); got > width {
								t.Errorf("page %d row %d is %d cols in a %d-col panel (overflow %d): %q",
									page, i, got, width, got-width, ansi.Strip(row))
							}
						}
					}
				})
			}
		}
	}
}

// TestHelpPanel_LaysOutToItsCap is the height half. The panel must
// come in at or under the rows the budget allocated it — and, when
// the cap is big enough to hold a page at all, it must do that by
// laying out to the cap rather than by being blunt-elided at it. The
// elision marker is what the panel did before this issue and is the
// baseline being beaten; it is allowed to survive only for the
// degenerate caps that cannot hold the panel's own rules and title.
func TestHelpPanel_LaysOutToItsCap(t *testing.T) {
	for _, w := range helpGridWidths {
		for _, h := range helpGridHeights {
			t.Run(strconv.Itoa(w)+"x"+strconv.Itoa(h), func(t *testing.T) {
				m := openHelp(StatusHeader, w, h)
				rowCap := m.chrome.helpCap
				if rowCap <= 0 {
					t.Fatalf("an open panel was allocated %d rows", rowCap)
				}
				for page := 0; page < m.helpPageCount(); page++ {
					m.helpPage = page
					panel := m.renderHelpPanel(m.chromeWidth())
					if got := lipgloss.Height(panel); got > rowCap {
						t.Errorf("page %d is %d rows against a cap of %d", page, got, rowCap)
					}
					elided := strings.Contains(ansi.Strip(panel), "more rows")
					if navigable := helpBodyRows(rowCap) > 0; navigable && elided {
						t.Errorf("page %d was elided by the cap (%d rows) instead of laid out to it:\n%s",
							page, rowCap, ansi.Strip(panel))
					}
				}
			})
		}
	}
}

// TestHelpPanel_PagingShowsEveryKey is the property pagination must
// not trade away: the panel documents 22 keys, and walking the pages
// has to reach all of them. A layout that fits by dropping content
// would pass both geometry tests above and fail this one.
func TestHelpPanel_PagingShowsEveryKey(t *testing.T) {
	for _, w := range []int{40, 60, 80, 100, 160} {
		t.Run("width-"+strconv.Itoa(w), func(t *testing.T) {
			m := openHelp(StatusHeader, w, 24)
			var seen strings.Builder
			for page := 0; page < m.helpPageCount(); page++ {
				m.helpPage = page
				seen.WriteString(ansi.Strip(m.renderHelpPanel(m.chromeWidth())))
				seen.WriteString("\n")
			}
			all := seen.String()
			for _, sec := range helpSections() {
				for _, kv := range sec.keys {
					if !strings.Contains(all, kv[0]) {
						t.Errorf("key %q is on no page of the %d-col panel (%d pages)",
							kv[0], w, m.helpPageCount())
					}
				}
			}
		})
	}
}

// TestHelpPanel_QuestionMarkPagesThenCloses drives the cycle through
// the real key path. `?` is the panel's only key: it opens, then
// walks a page at a time, then closes — which is what lets the panel
// be height-aware without taking pgup / pgdn away from the chat.
func TestHelpPanel_QuestionMarkPagesThenCloses(t *testing.T) {
	m := newFrameModel(StatusHeader, 80, 24)
	m = withHostileTranscript(m)

	press := func() model {
		out, _ := m.Update(keyPress("?"))
		return out.(model)
	}

	m = press()
	if !m.helpOpen || m.helpPage != 0 {
		t.Fatalf("first `?` left helpOpen=%v page=%d, want open at page 0", m.helpOpen, m.helpPage)
	}
	pages := m.helpPageCount()
	if pages < 2 {
		t.Fatalf("precondition: an 80x24 terminal should not hold the panel in one page, got %d", pages)
	}
	for want := 1; want < pages; want++ {
		m = press()
		if !m.helpOpen {
			t.Fatalf("`?` closed the panel at page %d of %d", want, pages)
		}
		if m.helpPage != want {
			t.Fatalf("`?` moved to page %d, want %d", m.helpPage, want)
		}
	}
	m = press()
	if m.helpOpen {
		t.Errorf("`?` on the last page left the panel open at page %d", m.helpPage)
	}
	if m.helpPage != 0 {
		t.Errorf("closing left helpPage=%d, want 0 so reopening starts at the top", m.helpPage)
	}
}

// TestHelpPanel_TypedQuestionMarkStillTypes pins the guard the paging
// cycle inherits: `?` only drives the panel when the input is empty,
// so it can be typed mid-sentence.
func TestHelpPanel_TypedQuestionMarkStillTypes(t *testing.T) {
	m := newFrameModel(StatusHeader, 80, 24)
	m.input.SetValue("what now")
	out, _ := m.Update(keyPress("?"))
	m = out.(model)
	if m.helpOpen {
		t.Error("`?` opened the panel while the operator was typing")
	}
	if got := m.input.Value(); !strings.HasSuffix(got, "?") {
		t.Errorf("input is %q; the typed `?` did not reach the textarea", got)
	}
}

// TestHelpPanel_EscClosesAndForgetsThePage — Esc cascades to the
// panel (R-CHAT-6) and has to leave it in the state a fresh `?` would
// find, page included.
func TestHelpPanel_EscClosesAndForgetsThePage(t *testing.T) {
	m := openHelp(StatusHeader, 80, 24)
	m.advanceHelp()
	m.resize()
	if m.helpPage == 0 {
		t.Fatal("precondition: expected to be past the first page")
	}
	out, _ := m.Update(keyPress("esc"))
	m = out.(model)
	if m.helpOpen {
		t.Error("esc left the help panel open")
	}
	if m.helpPage != 0 {
		t.Errorf("esc left helpPage=%d, want 0", m.helpPage)
	}
}

// TestHelpPanel_StalePageSurvivesAShrink — the cap moves with the
// terminal, so a page index set on a 60-row terminal can be past the
// end on a 24-row one. The render clamps it and the next `?` closes
// the panel rather than paging into nothing.
func TestHelpPanel_StalePageSurvivesAShrink(t *testing.T) {
	m := openHelp(StatusHeader, 100, 60)
	m.helpPage = 12 // far past any page count

	out, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = out.(model)

	panel := m.renderHelpPanel(m.chromeWidth())
	if panel == "" {
		t.Fatal("a stale page rendered nothing")
	}
	if got, rowCap := lipgloss.Height(panel), m.chrome.helpCap; got > rowCap {
		t.Errorf("clamped page is %d rows against a cap of %d", got, rowCap)
	}
	m.advanceHelp()
	if m.helpOpen {
		t.Errorf("`?` past the last page left the panel open at page %d", m.helpPage)
	}
}

// TestHelpPanel_TallTerminalKeepsOnePage guards the other direction:
// pagination is a response to a short terminal, not a new permanent
// shape. A terminal with room for the whole panel gets it in one
// page, with the original wording on the title row.
func TestHelpPanel_TallTerminalKeepsOnePage(t *testing.T) {
	m := openHelp(StatusHeader, 100, 60)
	if got := m.helpPageCount(); got != 1 {
		t.Errorf("a 100x60 terminal paginated the panel into %d pages", got)
	}
	title := ansi.Strip(m.helpTitle(0, 1, 100))
	if !strings.Contains(title, "(? to close)") {
		t.Errorf("single-page title is %q, want the plain close hint", title)
	}
	paged := ansi.Strip(m.helpTitle(1, 4, 100))
	if !strings.Contains(paged, "page 2/4") || !strings.Contains(paged, "? next") {
		t.Errorf("multi-page title is %q, want the page counter and the paging key", paged)
	}
}

// TestHelpPanel_KeyColumnFollowsTheWidth pins the derivation the old
// `const keyCol = 24` had no equivalent of: the column comes from the
// data, is capped so one long key row cannot push all 22 descriptions
// right, and never takes more than half of a narrow panel.
func TestHelpPanel_KeyColumnFollowsTheWidth(t *testing.T) {
	sections := helpSections()
	cases := []struct{ width, want int }{
		{200, helpKeyColMax},
		{100, helpKeyColMax},
		{60, helpKeyColMax},
		{40, 18},
		{20, 8},
		{6, 1},
	}
	for _, tc := range cases {
		t.Run(strconv.Itoa(tc.width), func(t *testing.T) {
			if got := helpKeyCol(sections, tc.width); got != tc.want {
				t.Errorf("helpKeyCol(width=%d) = %d, want %d", tc.width, got, tc.want)
			}
		})
	}
}

// TestPaginateHelp_BreaksOnSections covers the packer directly: whole
// sections move to the next page rather than straddling a break, one
// page beats two when dropping the blank separators is enough to get
// there, and a section taller than a page is split rather than lost.
func TestPaginateHelp_BreaksOnSections(t *testing.T) {
	blocks := [][]string{
		{"A", "a1", "a2"},
		{"B", "b1", "b2"},
		{"C", "c1"},
	}
	t.Run("unbounded", func(t *testing.T) {
		got := paginateHelp(blocks, 0)
		if len(got) != 1 || len(got[0]) != 10 {
			t.Fatalf("want one 10-row page (8 rows + 2 separators), got %d pages of %v", len(got), pageSizes(got))
		}
	})
	t.Run("fits-with-separators", func(t *testing.T) {
		if got := paginateHelp(blocks, 10); len(got) != 1 {
			t.Errorf("want one page at a cap of 10, got %v", pageSizes(got))
		}
	})
	t.Run("compact-beats-splitting", func(t *testing.T) {
		got := paginateHelp(blocks, 8)
		if len(got) != 1 {
			t.Fatalf("dropping the two separators fits 8 rows exactly; got %v", pageSizes(got))
		}
		for _, row := range got[0] {
			if row == "" {
				t.Error("the compact page kept a blank separator row")
			}
		}
	})
	t.Run("breaks-between-sections", func(t *testing.T) {
		got := paginateHelp(blocks, 4)
		if len(got) != 3 {
			t.Fatalf("want a page per section at a cap of 4, got %v", pageSizes(got))
		}
		for i, want := range []string{"A", "B", "C"} {
			if got[i][0] != want {
				t.Errorf("page %d starts at %q, want section %q — a section straddled the break",
					i, got[i][0], want)
			}
		}
	})
	t.Run("packs-what-fits", func(t *testing.T) {
		// A + separator + B is exactly 7; C does not fit after it and
		// moves whole rather than being split across the break.
		got := paginateHelp(blocks, 7)
		if len(got) != 2 || len(got[0]) != 7 || got[1][0] != "C" {
			t.Errorf("want [7 2] with C starting page 2, got %v (%v)", pageSizes(got), got)
		}
	})
	t.Run("splits-an-oversized-section", func(t *testing.T) {
		got := paginateHelp([][]string{{"A", "a1", "a2", "a3", "a4"}}, 2)
		if len(got) != 3 {
			t.Fatalf("want 3 pages for a 5-row section at a cap of 2, got %v", pageSizes(got))
		}
		var rows []string
		for _, page := range got {
			rows = append(rows, page...)
		}
		if strings.Join(rows, ",") != "A,a1,a2,a3,a4" {
			t.Errorf("splitting lost or reordered rows: %v", rows)
		}
	})
}

func pageSizes(pages [][]string) []int {
	out := make([]int, 0, len(pages))
	for _, p := range pages {
		out = append(out, len(p))
	}
	return out
}
