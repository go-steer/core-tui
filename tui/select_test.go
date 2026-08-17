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

package tui

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Selecting and folding transcript items (issue #152). The contract
// under test:
//
//   - the marker says which item is current, and only while the
//     transcript holds the keyboard;
//   - moving the cursor renders nothing and re-wraps nothing;
//   - the cursor cannot leave the window;
//   - a fold takes a long item down to its head and back, exactly;
//   - a fold anywhere else in the transcript does not move the window.

// selectModel is a transcript of tall items — ten-line answers, which
// is the shape #152 is about: long enough that a fold changes the
// frame and that a single item can fill a window.
func selectModel(t *testing.T, turns, w, h int) Model {
	t.Helper()
	m := NewModel(Options{Agent: &bareAgent{id: "sel"}})
	m.styles = NewStylesWithTheme(true, goldenTheme())
	out, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m = out.(Model)
	for i := range turns {
		q := "turn " + strconv.Itoa(i) + ": what does this function do?"
		m.history.Append(Message{Role: RoleUser, Text: q, Rendered: q})
		a := "Turn " + strconv.Itoa(i) + " opens the answer.\n" +
			strings.Repeat("It reads the config, validates it, returns a handle.\n", 8) +
			"And that is the whole of it."
		m.history.Append(Message{Role: RoleAssistant, Text: a, Rendered: a})
	}
	m.refreshViewport()
	m.setFocus(focusTranscript)
	return m
}

// chatWindowRows is the set of rows the window currently shows.
func chatWindowRows(m *Model) map[int]bool {
	rows := map[int]bool{}
	m.chatVisitWindow(func(i int) { rows[i] = true })
	return rows
}

// markedFrameLines splits a drawn frame into the lines carrying the
// selection bar and the lines that do not.
func markedFrameLines(view string) (marked, plain int) {
	for _, ln := range strings.Split(view, "\n") {
		if strings.HasPrefix(ansi.Strip(ln), glyphSelectBar) {
			marked++
			continue
		}
		plain++
	}
	return marked, plain
}

// The marker is the whole point of the cursor: it has to be on the
// selected item, on every line of it, and on nothing else.
func TestSelect_MarkerCoversTheSelectedItemAndNothingElse(t *testing.T) {
	m := selectModel(t, 6, 80, 24)
	m.chatSelectLast()

	visible := len(m.chatRowLines(m.selIdx))
	if idx, line := m.viewport.Offset(); idx == m.selIdx {
		visible -= line // the item is the top row and partly scrolled off
	}
	marked, plain := markedFrameLines(m.chatView())
	if marked != visible {
		t.Errorf("the marker covers %d lines, want the selected item's %d", marked, visible)
	}
	if plain == 0 {
		t.Error("every line is marked; the marker no longer distinguishes anything")
	}
}

// Focus gates the drawing, not the state: with the keyboard in the
// composer the cursor is not what the operator is driving, and a lit
// marker would claim otherwise.
func TestSelect_MarkerIsDrawnOnlyWhileTheTranscriptHasTheKeyboard(t *testing.T) {
	m := selectModel(t, 6, 80, 24)
	m.chatSelectLast()
	if marked, _ := markedFrameLines(m.chatView()); marked == 0 {
		t.Fatal("setup: the marker is not drawn in transcript focus")
	}

	sel := m.selIdx
	m.setFocus(focusInput)
	if marked, _ := markedFrameLines(m.chatView()); marked != 0 {
		t.Errorf("%d lines still carry the marker with the keyboard in the composer", marked)
	}
	if m.selIdx != sel {
		t.Errorf("leaving focus mode moved the cursor (%d → %d); it is the drawing that focus gates", sel, m.selIdx)
	}
}

// The gutter is reserved on every row so that the marker costs no
// re-wrap: an item's render must be the same bytes whether or not the
// cursor is on it, and walking the cursor over the whole transcript
// must not add a single cache entry.
func TestSelect_MovingTheCursorRendersNothing(t *testing.T) {
	m := selectModel(t, 20, 80, 24)
	_ = m.chatView()
	before := chatEntriesAtWidth(m)
	if before == 0 {
		t.Fatal("setup: the frame rendered nothing")
	}

	row := m.selIdx
	unselected := strings.Join(m.chatRowLines(row), "\n")
	for i := range m.history.Len() {
		m.selIdx = i
		_ = m.chatView()
	}
	if got := chatEntriesAtWidth(m); got != before {
		t.Errorf("moving the cursor over %d items rendered %d new rows; the marker is drawn, not rendered",
			m.history.Len(), got-before)
	}
	m.selIdx = row
	if got := strings.Join(m.chatRowLines(row), "\n"); got != unselected {
		t.Error("an item's render changed when the cursor landed on it; the gutter is supposed to be reserved on every row")
	}
}

// A cursor you cannot see is not a cursor. Every move has to leave
// the selected item on screen — including the moves that need a
// scroll to do it, and including items taller than the window.
func TestSelect_CursorNeverLeavesTheWindow(t *testing.T) {
	m := selectModel(t, 30, 80, 20)
	m.chatSelectFirst()

	for _, stroke := range []string{"down", "down", "down", "down", "down", "down", "down", "down"} {
		m = press(m, stroke)
		if !chatWindowRows(&m)[m.selIdx] {
			t.Fatalf("after %q the cursor is on item %d, which the window (%v) does not show", stroke, m.selIdx, m.viewport)
		}
	}
	for range 12 {
		m = press(m, "up")
		if !chatWindowRows(&m)[m.selIdx] {
			t.Fatalf("scrolling back up left the cursor on item %d, off screen", m.selIdx)
		}
	}
}

// Space folds the selected item down to its head plus a count, and
// space again restores it byte for byte — the fold is a view of the
// cached render, so an unfold cannot lose anything.
func TestCollapse_SpaceFoldsTheSelectedItemAndRestoresIt(t *testing.T) {
	m := selectModel(t, 6, 80, 40)
	m.selIdx = 1 // the first assistant turn
	total := m.history.Len()
	msg, _ := m.history.at(m.selIdx)

	full := append([]string(nil), m.chatMessageLines(m.selIdx, total, msg)...)
	if len(full) <= chatCollapsedLines+1 {
		t.Fatalf("setup: the fixture's item is only %d lines; nothing to fold", len(full))
	}

	m = press(m, "space")
	folded := m.chatMessageLines(m.selIdx, total, msg)
	if len(folded) != chatCollapsedLines+1 {
		t.Fatalf("a folded item is %d lines, want %d (its head plus the count)", len(folded), chatCollapsedLines+1)
	}
	for i := range chatCollapsedLines {
		if folded[i] != full[i] {
			t.Errorf("folded line %d is not the item's own line %d", i, i)
		}
	}
	summary := ansi.Strip(folded[len(folded)-1])
	if !strings.Contains(summary, strconv.Itoa(len(full)-chatCollapsedLines)) {
		t.Errorf("the fold's last line is %q, want a count of the %d hidden lines", summary, len(full)-chatCollapsedLines)
	}

	m = press(m, "space")
	if got := m.chatMessageLines(m.selIdx, total, msg); strings.Join(got, "\n") != strings.Join(full, "\n") {
		t.Error("unfolding did not restore the item exactly")
	}
}

// Folding an item that is already shorter than its own summary would
// make it grow. Nothing to fold is a no-op, not a taller item.
func TestCollapse_ShortItemsAreLeftAlone(t *testing.T) {
	m := followModel(t, 100, 40, 20) // one-line messages
	m.setFocus(focusTranscript)
	total := m.history.Len()
	msg, _ := m.history.at(m.selIdx)

	before := strings.Join(m.chatMessageLines(m.selIdx, total, msg), "\n")
	m = press(m, "space")
	if after := strings.Join(m.chatMessageLines(m.selIdx, total, msg), "\n"); after != before {
		t.Errorf("folding a one-line item changed it:\n before %q\n after  %q", before, after)
	}
}

// The dividend issue #152 was promised by #161: the scroll position
// is an item and a line within it, so a height change ANYWHERE ELSE
// in the transcript cannot invalidate it. The same window draws the
// same bytes even though the flat offset it would have had is gone.
func TestCollapse_FoldingAboveTheWindowDoesNotMoveIt(t *testing.T) {
	m := selectModel(t, 20, 80, 24)
	m.chatGotoBottom()
	before := m.chatView()
	beforeIdx, beforeLine := m.viewport.Offset()
	beforeFlat := m.chatYOffset()

	above, ok := m.history.at(3)
	if !ok {
		t.Fatal("setup: no item above the window")
	}
	m.collapsed = map[uint64]bool{above.ID: true}

	if got := m.chatView(); got != before {
		t.Error("folding an item three screens up redrew the window")
	}
	if idx, line := m.viewport.Offset(); idx != beforeIdx || line != beforeLine {
		t.Errorf("the scroll pair moved from (%d, %d) to (%d, %d)", beforeIdx, beforeLine, idx, line)
	}
	if flat := m.chatYOffset(); flat == beforeFlat {
		t.Errorf("the flat offset is unchanged at %d — the fold removed no lines, so this test proves nothing", flat)
	}
}

// Folding the newest item while pinned to the tail moves the tail up
// by however many lines went away. Follow is tracked intent (#93), so
// the operator stays pinned.
func TestCollapse_TheTailStaysPinnedWhenItFolds(t *testing.T) {
	m := selectModel(t, 6, 80, 24)
	m = press(m, "G")
	if !m.follow {
		t.Fatal("setup: G did not re-arm follow")
	}

	m = press(m, "space")
	if !m.follow {
		t.Error("folding the item under the cursor dropped follow")
	}
	if !m.chatAtBottom() {
		t.Error("follow is set but the window is no longer on the tail")
	}
}

// Entering focus mode has to leave a cursor on screen without
// scrolling: it is the operator's place in the transcript, not a jump
// to somewhere else in it.
func TestSelect_SeedLandsOnTheLastVisibleItemAndScrollsNothing(t *testing.T) {
	m := selectModel(t, 20, 80, 24)
	m.chatSetYOffset(m.chatYOffset() / 2)
	m.syncFollow()
	idx, line := m.viewport.Offset()

	m.setFocus(focusTranscript)
	rows := chatWindowRows(&m)
	if !rows[m.selIdx] {
		t.Errorf("the cursor was seeded at item %d, which the window does not show", m.selIdx)
	}
	for i := range rows {
		if i < m.history.Len() && i > m.selIdx {
			t.Errorf("the cursor was seeded at %d but item %d is also visible; seed at the last one", m.selIdx, i)
		}
	}
	if gotIdx, gotLine := m.viewport.Offset(); gotIdx != idx || gotLine != line {
		t.Errorf("entering focus mode scrolled from (%d, %d) to (%d, %d)", idx, line, gotIdx, gotLine)
	}
}

// Tabbing out to type and back is not a reason to lose your place.
func TestSelect_SeedKeepsAVisibleCursor(t *testing.T) {
	m := selectModel(t, 20, 80, 24)
	m.chatSelectBy(-2)
	want := m.selIdx

	m.setFocus(focusInput)
	m.setFocus(focusTranscript)
	if m.selIdx != want {
		t.Errorf("a round trip through the composer moved the cursor from %d to %d", want, m.selIdx)
	}
}

// The legend is where an operator learns what the arrows do without
// pressing one to find out, so it has to describe the keymap this
// issue left behind rather than the one it replaced.
func TestSelect_TheLegendNamesWhatTheArrowsDo(t *testing.T) {
	hint := focusedModel(t, 20).footerHint()
	for _, want := range []string{"↑↓ select", "space fold"} {
		if !strings.Contains(hint, want) {
			t.Errorf("legend %q does not mention %q", hint, want)
		}
	}
	if strings.Contains(hint, "↑↓ scroll") {
		t.Errorf("legend still says the arrows scroll: %q", hint)
	}
}

// A cleared transcript is a different transcript: the cursor and the
// folds go with it rather than being reapplied to whatever lands next.
func TestSelect_ClearForgetsTheCursorAndTheFolds(t *testing.T) {
	m := selectModel(t, 6, 80, 24)
	m = press(m, "space")
	if len(m.collapsed) == 0 {
		t.Fatal("setup: nothing was folded")
	}

	m.setFocus(focusInput) // the /clear confirmation is the composer's
	m.confirmingClear = true
	m = press(m, "enter")

	if m.history.Len() != 0 {
		t.Fatalf("setup: /clear left %d items", m.history.Len())
	}
	if m.selIdx != 0 {
		t.Errorf("the cursor survived /clear at item %d", m.selIdx)
	}
	if len(m.collapsed) != 0 {
		t.Errorf("%d folds survived /clear", len(m.collapsed))
	}
}
