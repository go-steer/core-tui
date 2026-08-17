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

// Session picker tests. The dialog's Enter paths are covered from
// switch_test.go (the /switch built-in that opens it); this file is
// about the dialog itself, starting with type-to-filter (#117). A
// hundred-session project is the case that motivates the filter
// hardest, and it is the one the picker had no answer for.

package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// pickerSessions mixes the row kinds the picker renders: the attached
// row, plain rows with IDs that differ from their display names, and
// an action row (issue #56) that opens a text input instead of
// switching.
func pickerSessions() []SessionInfo {
	return []SessionInfo{
		{ID: "sess-001", Display: "nightly refactor", Current: true},
		{ID: "sess-002", Display: "docs sweep"},
		{ID: "sess-003", Display: "flaky test hunt", Description: "2 days ago"},
		{ID: "prod-042", Display: "prod incident"},
		{ID: "attach", Display: "+ Attach to endpoint…", Input: &SessionInput{
			Prompt: "Daemon URL:",
			Submit: func(string) (SwitchTarget, error) {
				return SwitchTarget{Agent: &bareAgent{id: "remote"}}, nil
			},
		}},
	}
}

func openSessionPickerFixture(t *testing.T) (Model, *sessionPickerDialog) {
	t.Helper()
	m := NewModel(Options{Agent: &switchAgent{id: "cur", sessions: pickerSessions()}})
	m.styles = NewStylesWithTheme(true, goldenTheme())
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = out.(Model)
	d := newSessionPickerDialog()
	d.applySessions(pickerSessions())
	m.overlayStack.Open(d)
	return m, d
}

func sessionIDs(sessions []SessionInfo) []string {
	out := make([]string, len(sessions))
	for i, s := range sessions {
		out[i] = s.ID
	}
	return out
}

// TestSessionPicker_TypingNarrowsTheList covers the display name, the
// ID, and the action row — all three are visible on the row, so all
// three have to be findable.
func TestSessionPicker_TypingNarrowsTheList(t *testing.T) {
	cases := []struct {
		filter string
		want   []string
	}{
		{filter: "docs", want: []string{"sess-002"}},
		// All three are tier-4 substring matches (the key is
		// "<display> <id>", and "sess-00" is inside neither the
		// display name nor a whole segment of it), so the shared
		// tiebreak orders them by key length — shortest first.
		{filter: "sess-00", want: []string{"sess-002", "sess-003", "sess-001"}},
		{filter: "prod", want: []string{"prod-042"}},
		{filter: "attach", want: []string{"attach"}},
		{filter: "zzz", want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.filter, func(t *testing.T) {
			m, d := openSessionPickerFixture(t)
			typeIntoPicker(&m, tc.filter)
			assertNameOrder(t, sessionIDs(d.rows()), tc.want)
		})
	}
}

// TestSessionPicker_EnterAttachesTheFilteredRow: the cursor indexes
// rows(), so a filtered Enter must attach to what is highlighted and
// not to the row at that index of the unfiltered snapshot.
func TestSessionPicker_EnterAttachesTheFilteredRow(t *testing.T) {
	m, d := openSessionPickerFixture(t)
	typeIntoPicker(&m, "prod")
	act := d.HandleKey("enter", &m)
	if !act.Consumed || act.Close {
		t.Errorf("enter = %+v, want Consumed and not Close", act)
	}
	if d.switching != "prod-042" {
		t.Errorf("switching = %q, want the filtered row prod-042", d.switching)
	}
	if act.Cmd == nil {
		t.Fatal("expected the off-loop SwitchToSession Cmd")
	}
	act.Cmd()
	agent := m.opts.Agent.(*switchAgent)
	if len(agent.switchCalls) != 1 || agent.switchCalls[0] != "prod-042" {
		t.Errorf("switchCalls = %v, want [prod-042]", agent.switchCalls)
	}
}

// TestSessionPicker_EnterOnAFilteredActionRow: filtering down to the
// action row and pressing Enter must still stack the text-input
// dialog on top rather than trying to attach to a session ID that
// names no session.
func TestSessionPicker_EnterOnAFilteredActionRow(t *testing.T) {
	m, d := openSessionPickerFixture(t)
	typeIntoPicker(&m, "endpoint")
	if got := sessionIDs(d.rows()); len(got) != 1 || got[0] != "attach" {
		t.Fatalf("filter matched %v, want just the action row", got)
	}
	d.HandleKey("enter", &m)
	if !m.overlayStack.HasID(sessionInputDialogID) {
		t.Error("enter on the filtered action row did not open its text input")
	}
	if !m.overlayStack.HasID(sessionPickerDialogID) {
		t.Error("the picker should stay open underneath so esc returns to the list")
	}
	if d.switching != "" {
		t.Errorf("an action row started a switch to %q", d.switching)
	}
}

// TestSessionPicker_FilterMatchingNothingStaysOpen keeps the two
// empty states apart: nothing enumerated by the host closes with a
// message, nothing matched by the filter does not.
func TestSessionPicker_FilterMatchingNothingStaysOpen(t *testing.T) {
	m, d := openSessionPickerFixture(t)
	typeIntoPicker(&m, "zzz")
	for _, stroke := range []string{"down", "up", "enter"} {
		if act := d.HandleKey(stroke, &m); !act.Consumed || act.Close {
			t.Errorf("%q on an empty filter result = %+v, want Consumed and not Close", stroke, act)
		}
	}
	if n := len(m.history.Snapshot()); n != 0 {
		t.Errorf("an empty filter result wrote %d chat messages", n)
	}
	body := ansi.Strip(d.Render(100, &m))
	if !strings.Contains(body, "no sessions match") {
		t.Errorf("empty-result body does not say so:\n%s", body)
	}
	if !strings.Contains(body, "0/5") {
		t.Errorf("empty-result body is missing the 0/5 count:\n%s", body)
	}
}

// TestSessionPicker_HostWithNoSessionsStillCloses pins the pre-filter
// behaviour for an empty enumeration.
func TestSessionPicker_HostWithNoSessionsStillCloses(t *testing.T) {
	m := NewModel(Options{Agent: &switchAgent{id: "cur"}})
	m.viewport.SetWidth(80)
	d := newSessionPickerDialog()
	d.applySessions(nil)
	m.overlayStack.Open(d)

	act := d.HandleKey("down", &m)
	if !act.Consumed || !act.Close {
		t.Errorf("down on an empty host list = %+v, want Consumed and Close", act)
	}
	snap := m.history.Snapshot()
	if len(snap) != 1 || !strings.Contains(snap[0].Text, "no sessions available") {
		t.Errorf("history = %v, want the 'no sessions available' system message", snap)
	}
}

// TestSessionPicker_ShrinkingListNeverPanics types a filter that runs
// out of matches while the arrows are pressed at every step.
func TestSessionPicker_ShrinkingListNeverPanics(t *testing.T) {
	m, d := openSessionPickerFixture(t)
	for _, r := range "sess-00xy" {
		typeIntoPicker(&m, string(r))
		for _, stroke := range []string{"down", "down", "down", "up"} {
			d.HandleKey(stroke, &m)
		}
		if n := len(d.rows()); n > 0 && (d.idx < 0 || d.idx >= n) {
			t.Fatalf("cursor %d is outside the %d filtered rows", d.idx, n)
		}
		d.Render(100, &m)
	}
}

// TestSessionPicker_CursorSitsInTheFilterRow — same #105 / #123
// requirement as the model picker's, and the same #125 cell-vs-rune
// trap in the CJK case.
func TestSessionPicker_CursorSitsInTheFilterRow(t *testing.T) {
	for _, typed := range []string{"docs", "セッション"} {
		t.Run(typed, func(t *testing.T) {
			m, _ := openSessionPickerFixture(t)
			typeIntoPicker(&m, typed)
			assertCursorFollows(t, m.View(), filterPromptRail+typed)
		})
	}
}

// ---------------------------------------------------------------
// Two-line cells (issue #163)
// ---------------------------------------------------------------

// manySessions builds n rows big enough to overflow any window the
// tests below set up. Display and ID are deliberately unrelated
// strings, so an assertion that finds one has definitely not found
// the other, and both are fixed-width so no row's tokens are a
// substring of another row's.
func manySessions(n int) []SessionInfo {
	out := make([]SessionInfo, n)
	for i := range out {
		out[i] = SessionInfo{
			ID:      fmt.Sprintf("sess-%03d", i),
			Display: fmt.Sprintf("title-%03d", i),
		}
	}
	return out
}

// openSessionPickerSized is openSessionPickerFixture with the
// terminal geometry and the session list under the test's control —
// which is what the windowing tests need, since the window only
// engages once the body is taller than modalBodyHeight.
func openSessionPickerSized(t *testing.T, height int, sessions []SessionInfo) (Model, *sessionPickerDialog) {
	t.Helper()
	m := NewModel(Options{Agent: &switchAgent{id: "cur", sessions: sessions}})
	m.styles = NewStylesWithTheme(true, goldenTheme())
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: height})
	m = out.(Model)
	d := newSessionPickerDialog()
	d.applySessions(sessions)
	m.overlayStack.Open(d)
	return m, d
}

// sessionPickerLines renders the picker and returns the lines inside
// its box edge, ANSI stripped and the right-hand padding removed —
// fitRow pads every windowed row out to the content width, and that
// padding is noise for a "which lines are on screen" assertion, as is
// the edge glyph now sitting on both ends of it.
func sessionPickerLines(d *sessionPickerDialog, m *Model) []string {
	return modalContentLines(d.Render(100, m))
}

// lineWith returns the index of the first line containing want, or -1.
func lineWith(lines []string, want string) int {
	for i, ln := range lines {
		if strings.Contains(ln, want) {
			return i
		}
	}
	return -1
}

// TestSessionPicker_RendersTwoLineCells is the shape of the change:
// the title on its own line, the ID and the metadata on the next one,
// and never both on the same line.
func TestSessionPicker_RendersTwoLineCells(t *testing.T) {
	m, d := openSessionPickerFixture(t)
	lines := sessionPickerLines(d, &m)

	title := lineWith(lines, "flaky test hunt")
	if title < 0 {
		t.Fatalf("display name is not on screen:\n%s", strings.Join(lines, "\n"))
	}
	if strings.Contains(lines[title], "sess-003") {
		t.Errorf("the ID is still sharing the title's line: %q", lines[title])
	}
	if title+1 >= len(lines) {
		t.Fatalf("title line %d is the last line; the cell has no detail line", title)
	}
	detail := lines[title+1]
	if !strings.Contains(detail, "sess-003") {
		t.Errorf("detail line = %q, want the session ID on it", detail)
	}
	// Description is metadata, so it rides with the ID rather than
	// crowding the title.
	if !strings.Contains(detail, "2 days ago") {
		t.Errorf("detail line = %q, want the Description on it", detail)
	}

	// "(current)" is metadata too, and belongs under its title.
	cur := lineWith(lines, "nightly refactor")
	if cur < 0 {
		t.Fatal("the attached session is not on screen")
	}
	if strings.Contains(lines[cur], "(current)") {
		t.Errorf("(current) is still on the title line: %q", lines[cur])
	}
	if !strings.Contains(lines[cur+1], "(current)") {
		t.Errorf("detail line = %q, want (current) on it", lines[cur+1])
	}

	// Action rows (issue #56) have no identity to put underneath, so
	// their chevron stays on the title line with the label.
	act := lineWith(lines, "+ Attach to endpoint…")
	if act < 0 {
		t.Fatal("the action row is not on screen")
	}
	if !strings.Contains(lines[act], GlyphCollapsed) {
		t.Errorf("action row = %q, want the chevron on the title line", lines[act])
	}
}

// TestSessionPicker_EmptyDisplayFallsBackToTheID covers the state
// every host is in until it starts populating Display: the title line
// must never come out blank, and the ID must not then be printed
// twice.
func TestSessionPicker_EmptyDisplayFallsBackToTheID(t *testing.T) {
	sessions := []SessionInfo{
		// Selected (idx 0 on open) and titled, so the bare row below
		// is an UNSELECTED one — its pad line is the one that has to
		// come out genuinely empty. The selected row's pad carries the
		// selection bar and is asserted separately.
		{ID: "sess-head", Display: "a real title"},
		{ID: "sess-bare"},
		{ID: "sess-desc", Description: "yesterday"},
		{ID: "sess-cur", Current: true},
	}
	m, d := openSessionPickerSized(t, 30, sessions)
	lines := sessionPickerLines(d, &m)

	for _, s := range sessions[1:] {
		at := lineWith(lines, s.ID)
		if at < 0 {
			t.Fatalf("%s is not on screen:\n%s", s.ID, strings.Join(lines, "\n"))
		}
		// The ID is the fallback TITLE, so it is on the first line of
		// the cell and must not be repeated on the second.
		if at+1 < len(lines) && strings.Contains(lines[at+1], s.ID) {
			t.Errorf("%s: ID printed on both lines of the cell (%q / %q)",
				s.ID, lines[at], lines[at+1])
		}
	}
	// The cell stays two lines tall whatever the host supplied: the
	// bare row pads, the others carry their metadata.
	bare := lineWith(lines, "sess-bare")
	if got := strings.TrimSpace(lines[bare+1]); got != "" {
		t.Errorf("bare session's pad line = %q, want it empty", got)
	}
	if got := lines[lineWith(lines, "sess-desc")+1]; !strings.Contains(got, "yesterday") {
		t.Errorf("detail line = %q, want the Description", got)
	}
	if got := lines[lineWith(lines, "sess-cur")+1]; !strings.Contains(got, "(current)") {
		t.Errorf("detail line = %q, want (current)", got)
	}

	// A SELECTED cell with nothing to say still draws its pad line:
	// the selection bar reaches the bottom of the cell, so the cursor
	// marks a CELL and not just its first line.
	d.HandleKey("down", &m)
	lines = sessionPickerLines(d, &m)
	if got := strings.TrimSpace(lines[lineWith(lines, "sess-bare")+1]); got != GlyphSelectBar {
		t.Errorf("selected bare cell's pad line = %q, want just the selection bar", got)
	}
}

// TestSessionPicker_SelectedCellIsNeverHalfWindowed is the trap issue
// #163 names, and the reason the window arithmetic calls listWindow
// twice. listWindow holds ONE line visible; a cell is two. Hand it the
// title and the detail line clips off the bottom while scrolling down;
// hand it the detail line and the title clips off the top while
// scrolling back up. So walk the cursor all the way down and all the
// way back, asserting at every step that BOTH lines of the selected
// cell are on screen and still adjacent.
func TestSessionPicker_SelectedCellIsNeverHalfWindowed(t *testing.T) {
	sessions := manySessions(12)
	m, d := openSessionPickerSized(t, 20, sessions)

	check := func(step string) {
		t.Helper()
		lines := sessionPickerLines(d, &m)
		want := sessions[d.idx]
		at := lineWith(lines, want.Display)
		if at < 0 {
			t.Fatalf("%s to idx %d: title %q is not in the window:\n%s",
				step, d.idx, want.Display, strings.Join(lines, "\n"))
		}
		if at+1 >= len(lines) || !strings.Contains(lines[at+1], want.ID) {
			t.Fatalf("%s to idx %d: detail line %q is not directly under its title:\n%s",
				step, d.idx, want.ID, strings.Join(lines, "\n"))
		}
	}

	check("open")
	for range len(sessions) - 1 {
		d.HandleKey("down", &m)
		check("down")
	}
	if d.idx != len(sessions)-1 {
		t.Fatalf("cursor is at %d, want the last row %d", d.idx, len(sessions)-1)
	}
	// The window has to have actually engaged, or everything above is
	// vacuously true: at the bottom the first cell is off screen.
	if lines := sessionPickerLines(d, &m); lineWith(lines, sessions[0].Display) >= 0 {
		t.Fatalf("nothing was windowed away — the fixture is too tall to test the trap:\n%s",
			strings.Join(lines, "\n"))
	}
	for range len(sessions) - 1 {
		d.HandleKey("up", &m)
		check("up")
	}
	if d.idx != 0 {
		t.Fatalf("cursor is at %d, want back at the top", d.idx)
	}
	if lines := sessionPickerLines(d, &m); lineWith(lines, sessions[len(sessions)-1].Display) >= 0 {
		t.Fatalf("the last cell is still on screen after scrolling back to the top:\n%s",
			strings.Join(lines, "\n"))
	}
}

// TestSessionPicker_ShortTerminal walks the heights where the window
// is smaller than a cell or barely equal to one. Two-line cells halve
// how many sessions fit, so these are the sizes the change makes
// tighter, and the invariant to hold is that the modal still composes
// inside the terminal and the selection is still legible.
func TestSessionPicker_ShortTerminal(t *testing.T) {
	sessions := manySessions(8)
	for _, h := range []int{3, 4, 6, 7, 8, 9, 12, 13, 14, 20} {
		t.Run(fmt.Sprint(h), func(t *testing.T) {
			m, d := openSessionPickerSized(t, 30, sessions)
			m.height = h
			// Drive the cursor to the bottom, where a window shorter
			// than a cell has to choose which half to keep.
			for range len(sessions) - 1 {
				d.HandleKey("down", &m)
				d.Render(100, &m)
			}
			rendered := d.Render(100, &m)
			if got := strings.Count(rendered, "\n") + 1; got > h {
				t.Errorf("picker is %d rows tall in a %d-row terminal:\n%s",
					got, h, ansi.Strip(rendered))
			}
			lines := sessionPickerLines(d, &m)
			// The filter row is never windowed away — it is what the
			// operator edits to get back.
			if !strings.Contains(ansi.Strip(rendered), filterPlaceholder) {
				t.Errorf("filter row scrolled out of the body:\n%s", ansi.Strip(rendered))
			}
			// A one-row window can only hold half the cell, and the
			// half worth keeping is the title — that is what the
			// ordering of the two listWindow calls buys.
			//
			// The shortest terminals are the exception, and it
			// predates this change: title + filter row + footer
			// already fills them, so fitModalContent clips the list
			// away entirely and there is no half to keep. The
			// assertion that matters at that size is the height
			// bound above. The exception was three rows until the
			// box edge claimed two more (issue #199) — it is stated
			// against modalEdgeRows so that it tracks the chrome
			// rather than having to be re-guessed.
			if h < 4+modalEdgeRows {
				return
			}
			want := sessions[d.idx]
			if lineWith(lines, want.Display) < 0 {
				t.Errorf("selected title %q is not on screen at height %d:\n%s",
					want.Display, h, strings.Join(lines, "\n"))
			}
		})
	}
}

// TestSessionPicker_NarrowTerminal: the dialog floors its width at 30,
// so below that the frame stops shrinking and rows are truncated by
// fitRow instead. Stacking the cell is what makes that survivable —
// truncation now eats the ID rather than the title — and the frame
// must stay rectangular whatever it cuts.
func TestSessionPicker_NarrowTerminal(t *testing.T) {
	sessions := manySessions(8)
	for _, w := range []int{12, 20, 34, 40, 100} {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			m, d := openSessionPickerSized(t, 20, sessions)
			d.HandleKey("down", &m)
			rendered := ansi.Strip(d.Render(w, &m))
			lines := strings.Split(rendered, "\n")
			width := ansi.StringWidth(lines[0])
			for i, ln := range lines {
				if got := ansi.StringWidth(ln); got != width {
					t.Fatalf("line %d is %d cells wide, want %d (frame is ragged):\n%s",
						i, got, width, rendered)
				}
			}
			// The title is the thing being chosen, so it is the thing
			// that has to survive the narrowest frame.
			if !strings.Contains(rendered, "title-001") {
				t.Errorf("selected title was truncated away at width %d:\n%s", w, rendered)
			}
		})
	}
}

// TestSessionPicker_UnfilteredStatesHaveNoCaret: nothing to narrow,
// no caret.
func TestSessionPicker_UnfilteredStatesHaveNoCaret(t *testing.T) {
	t.Run("loading", func(t *testing.T) {
		m, _ := openSessionPickerFixture(t)
		d := newSessionPickerDialog()
		if c := d.DialogCursor(100, &m); c != nil {
			t.Errorf("a picker with no snapshot claimed the caret at (%d,%d)", c.X, c.Y)
		}
	})
	t.Run("switching", func(t *testing.T) {
		m, d := openSessionPickerFixture(t)
		d.switching = "prod-042"
		if c := d.DialogCursor(100, &m); c != nil {
			t.Errorf("an in-flight attach claimed the caret at (%d,%d)", c.X, c.Y)
		}
	})
	t.Run("unwired-agent", func(t *testing.T) {
		m, d := openSessionPickerFixture(t)
		m.opts.Agent = &bareAgent{id: "bare"}
		if c := d.DialogCursor(100, &m); c != nil {
			t.Errorf("an unwired agent claimed the caret at (%d,%d)", c.X, c.Y)
		}
	})
}
