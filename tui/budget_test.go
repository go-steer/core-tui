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

// Chrome-budget tests (issue #121).
//
// Two properties, exercised from both ends:
//
//   - The input box keeps the height syncInputHeight computed for it,
//     up to textareaMaxHeight, so the auto-growing box the doc
//     comments describe actually appears on screen.
//   - No variable-height element can compose a frame taller than the
//     terminal. The row-budget grid in resize_budget_test.go proves
//     the arithmetic; these prove the consequences an operator would
//     see — the input box and the footer staying in the frame with
//     the help panel open, and clipFrame going back to being a net
//     that does not fire.

package tui

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// frameLines is the terminal-row count of a composed frame, ignoring
// a trailing newline the way assertFrameFits does.
func frameLines(frame string) int {
	lines := strings.Split(frame, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return len(lines)
}

// seedInput fills the textarea and reconciles the layout through the
// contract every keystroke path follows: syncInputHeight, then
// resize() when the height moved.
func seedInput(m *Model, text string) bool {
	m.input.SetValue(text)
	grew := m.syncInputHeight()
	if grew {
		m.resize()
	}
	return grew
}

// TestResize_KeepsGrownInputHeight is issue #121's repro. resize()
// reset the textarea to textareaMinHeight unconditionally, and every
// caller of syncInputHeight follows the grow with a resize — so the
// grown height survived zero frames and the multi-line input box was
// never rendered once.
func TestResize_KeepsGrownInputHeight(t *testing.T) {
	m := newFrameModel(StatusHeader, 100, 40)
	if !seedInput(&m, strings.Repeat("line\n", 6)) {
		t.Fatal("precondition: seeding 6 newlines should have grown the textarea")
	}
	if got := m.input.Height(); got != 7 {
		t.Fatalf("syncInputHeight sized the textarea to %d rows, want 7", got)
	}
	m.resize()
	if got := m.input.Height(); got != 7 {
		t.Errorf("resize() reset the textarea to %d rows; it must keep the %d "+
			"syncInputHeight computed", got, 7)
	}
	// And the rows are really in the frame, not just in the model.
	if got, want := lipgloss.Height(m.renderInputBox()), 8; got != want {
		t.Errorf("input box renders %d rows, want %d (7 textarea + 1 border)", got, want)
	}
}

// TestResize_InputReachesTextareaMaxHeight pins the other half of the
// issue: with resize() clobbering the height, textareaMaxHeight was
// unreachable dead configuration. On a terminal with room for it, it
// has to be reachable.
func TestResize_InputReachesTextareaMaxHeight(t *testing.T) {
	m := newFrameModel(StatusHeader, 100, 60)
	seedInput(&m, strings.Repeat("line\n", textareaMaxHeight*2))
	if got := m.input.Height(); got != textareaMaxHeight {
		t.Errorf("textarea is %d rows after pasting %d lines into a 60-row terminal, "+
			"want textareaMaxHeight (%d)", got, textareaMaxHeight*2, textareaMaxHeight)
	}
	assertBudgetExact(t, m)
	assertFrameFits(t, m.View().Content, 100, 60)
}

// TestResize_InputGrowthYieldsToChatFloor is the conflict the budget
// has to arbitrate: a textareaMaxHeight-tall box does not fit a short
// terminal alongside the header, the footer and a readable chat.
//
// The resolution the budget implements: the input box's MINIMUM
// outranks everything (an operator who cannot see the line they are
// typing cannot drive the TUI), but its GROWTH ranks below the chat
// floor (watching the transcript vanish while typing is worse than
// typing into a box that scrolls internally, which is what the
// textarea does past its height).
func TestResize_InputGrowthYieldsToChatFloor(t *testing.T) {
	for _, h := range []int{12, 16, 20, 24, 30, 40} {
		t.Run(strconv.Itoa(h), func(t *testing.T) {
			m := newFrameModel(StatusHeader, 100, h)
			m = withHostileTranscript(m)
			seedInput(&m, strings.Repeat("line\n", textareaMaxHeight*2))

			if got := m.viewport.Height(); got < chatMinHeight {
				t.Errorf("chat viewport shrank to %d rows, below the %d-row floor",
					got, chatMinHeight)
			}
			if got := m.input.Height(); got < textareaMinHeight {
				t.Errorf("textarea shrank to %d rows, below its %d-row minimum",
					got, textareaMinHeight)
			}
			assertBudgetExact(t, m)
			assertFrameFits(t, m.View().Content, 100, h)
		})
	}
}

// TestSyncInputHeight_AgreesWithResize is the anti-churn property.
// syncInputHeight clamps to the same budget resize() does, so once
// the layout has settled a further keystroke that does not change the
// line count reports no change — otherwise every keystroke in a long
// paste would report a height change, trigger a resize that took the
// rows straight back, and repaint the transcript for nothing.
func TestSyncInputHeight_AgreesWithResize(t *testing.T) {
	for _, h := range []int{10, 12, 16, 24, 40} {
		t.Run(strconv.Itoa(h), func(t *testing.T) {
			m := newFrameModel(StatusHeader, 100, h)
			seedInput(&m, strings.Repeat("line\n", textareaMaxHeight*2))
			if m.syncInputHeight() {
				t.Errorf("syncInputHeight still wants to move the textarea after resize "+
					"settled it at %d rows (budget affords %d)",
					m.input.Height(), m.chrome.inputMax)
			}
		})
	}
}

// TestResize_HelpPanelKeepsInputAndFooterInFrame is the motivating
// case. renderHelpPanel emits 38 rows at every width (issue #119), so
// `?` on an 80x24 terminal composed a 48-row frame; clipFrame kept
// the first 24 and the input box and the footer were not in the frame
// at all — the operator pressed `?` and lost the box they were typing
// into plus the only row that says how to quit.
//
// The budget does not fix the panel; it stops the panel from evicting
// anything. What the panel itself should look like is #119's call.
func TestResize_HelpPanelKeepsInputAndFooterInFrame(t *testing.T) {
	const w, h = 80, 24
	m := newFrameModel(StatusHeader, w, h)
	m = withHostileTranscript(m)
	const typed = "the-prompt-being-typed"
	seedInput(&m, typed)

	// Opened but not yet reconciled: the panel's natural height, with
	// no cap on it, is the thing that has to be too tall for this to
	// be testing anything.
	m.helpOpen = true
	if got := lipgloss.Height(m.renderHelpPanel(w)); got <= h {
		t.Skipf("help panel is %d rows, no longer taller than a %d-row terminal", got, h)
	}
	m.resize()
	m.refreshViewport()

	frame := ansi.Strip(m.View().Content)
	if got := frameLines(frame); got != h {
		t.Errorf("frame is %d rows in a %d-row terminal", got, h)
	}
	if !strings.Contains(frame, typed) {
		t.Error("the input box is not in the frame with the help panel open")
	}
	if !strings.Contains(frame, "ctrl+c quit") {
		t.Error("the footer is not in the frame with the help panel open")
	}
	// The panel took what was left and said what it dropped.
	if !strings.Contains(frame, GlyphTruncate+" ") {
		t.Error("the truncated help panel does not say how many rows it dropped")
	}
	assertBudgetExact(t, m)
}

// TestResize_PaletteShrinksItsWindow covers the third element with
// the same shape: maxPaletteRows plus the panel's own chrome is more
// than a short terminal can spare, so the visible window shrinks
// instead of the frame overflowing.
func TestResize_PaletteShrinksItsWindow(t *testing.T) {
	tall := withPalette(newFrameModel(StatusHeader, 100, 50))
	if got := tall.paletteWindow(); got != maxPaletteRows {
		t.Fatalf("precondition: a 50-row terminal should afford the full %d-row "+
			"palette window, got %d", maxPaletteRows, got)
	}

	short := withPalette(newFrameModel(StatusHeader, 100, 14))
	if got := short.paletteWindow(); got >= maxPaletteRows {
		t.Errorf("palette window is %d rows on a 14-row terminal; it must shrink "+
			"below the %d-row cap", got, maxPaletteRows)
	}
	if got := short.paletteWindow(); got < 1 {
		t.Errorf("palette window shrank to %d rows; it must always show one item", got)
	}
	assertBudgetExact(t, short)
	assertFrameFits(t, short.View().Content, 100, 14)
}

// TestClipFrame_DoesNotFireInNormalUse restores the contract
// clipFrame's own doc comment states: it is a safety net, and once
// the row budget is right it should never fire. "Normal use" is a
// terminal at least as tall as the irreducible chrome — every size an
// operator would plausibly run, in every state that composes
// variable-height chrome.
//
// Measured as "the composed frame is already the terminal height", so
// a frame clipFrame silently trimmed fails here even though View()'s
// output looks correct.
func TestClipFrame_DoesNotFireInNormalUse(t *testing.T) {
	sizes := []struct{ w, h int }{{80, 24}, {100, 30}, {100, 50}, {120, 50}, {200, 24}}
	for _, lay := range []StatusLayout{StatusHeader, StatusSidebar} {
		for _, st := range budgetStates() {
			for _, s := range sizes {
				name := st.name + "/" + strconv.Itoa(s.w) + "x" + strconv.Itoa(s.h)
				if lay == StatusSidebar {
					name = "sidebar/" + name
				}
				t.Run(name, func(t *testing.T) {
					m := newFrameModel(lay, s.w, s.h)
					m = st.setup(t, m, s.w, s.h)
					if got := composedRows(m); got != s.h {
						t.Fatalf("pre-clip frame is %d rows in a %d-row terminal; "+
							"clipFrame would trim %d", got, s.h, got-s.h)
					}
					if got := frameLines(m.View().Content); got != s.h {
						t.Errorf("composed frame is %d rows, want %d", got, s.h)
					}
				})
			}
		}
	}
}

// TestFitPanelRows pins the clamp itself: an exact row count, a
// marker that reports the drop, and no marker at all when the panel
// already fits.
func TestFitPanelRows(t *testing.T) {
	m := newFrameModel(StatusHeader, 80, 24)
	lines := make([]string, 10)
	for i := range lines {
		lines[i] = "row " + strconv.Itoa(i)
	}

	for _, ceiling := range []int{1, 2, 5, 9} {
		got := m.fitPanelRows(lines, ceiling, 80)
		if len(got) != ceiling {
			t.Errorf("fitPanelRows(cap=%d) returned %d rows", ceiling, len(got))
			continue
		}
		marker := ansi.Strip(got[len(got)-1])
		want := len(lines) - ceiling + 1
		if !strings.Contains(marker, strconv.Itoa(want)+" more rows") {
			t.Errorf("fitPanelRows(cap=%d) marker is %q, want it to report %d dropped rows",
				ceiling, marker, want)
		}
	}

	for _, ceiling := range []int{0, 10, 20} {
		got := m.fitPanelRows(lines, ceiling, 80)
		if len(got) != len(lines) {
			t.Errorf("fitPanelRows(cap=%d) clamped a panel that fits: %d rows", ceiling, len(got))
		}
	}

	// The marker is chrome like any other row and has to respect the
	// column it is drawn in.
	narrow := m.fitPanelRows(lines, 1, 6)
	if got := ansi.StringWidth(narrow[0]); got > 6 {
		t.Errorf("marker is %d cols in a 6-col panel: %q", got, ansi.Strip(narrow[0]))
	}
}

// TestResize_BudgetSurvivesResizeSequence walks one model through a
// drag with the input grown and the help panel open. State that
// survives a resize — the textarea's height, the panel caps — is
// where a stale budget would hide from the fresh-model grids.
func TestResize_BudgetSurvivesResizeSequence(t *testing.T) {
	m := newFrameModel(StatusHeader, 120, 40)
	m = withHostileTranscript(m)
	m.helpOpen = true
	m.resize()
	seedInput(&m, strings.Repeat("line\n", textareaMaxHeight*2))

	seq := []struct{ w, h int }{
		{120, 40}, {80, 24}, {40, 12}, {200, 50}, {100, 14}, {41, 10}, {120, 40},
	}
	for _, s := range seq {
		out, _ := m.Update(tea.WindowSizeMsg{Width: s.w, Height: s.h})
		m = out.(Model)
		t.Run(strconv.Itoa(s.w)+"x"+strconv.Itoa(s.h), func(t *testing.T) {
			assertBudgetExact(t, m)
			assertFrameFits(t, m.View().Content, s.w, s.h)
		})
	}
}

// TestUpdate_NewlineKeyGrowsTheBox drives the auto-grow through the
// real key path. The newline arm of handleKey's switch returns early,
// so it never reached the reconciliation at the bottom of the
// function: the one gesture whose entire purpose is to add a row left
// the box at its previous height until the operator typed something
// else. Fixing resize()'s clobber (issue #121) makes that lag
// visible for the first time, so it is fixed with it.
func TestUpdate_NewlineKeyGrowsTheBox(t *testing.T) {
	m := newFrameModel(StatusHeader, 100, 40)
	for i := 1; i <= 6; i++ {
		out, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'j', Mod: tea.ModCtrl}))
		m = out.(Model)
		want := i + 1
		if want < textareaMinHeight {
			want = textareaMinHeight
		}
		if got := m.input.Height(); got != want {
			t.Fatalf("after %d newlines the box is %d rows, want %d (line count %d)",
				i, got, want, m.input.LineCount())
		}
	}
	assertBudgetExact(t, m)
	assertFrameFits(t, m.View().Content, 100, 40)
}

// TestUpdate_PasteGrowsTheBox is the same property for the other way
// rows arrive. Bracketed paste never goes through handleKey — it
// falls out of Update's switch to the shared forward-to-the-widgets
// tail — so a pasted block used to sit inside a three-row box until
// an unrelated keystroke reconciled the layout, which is precisely
// the "multi-line paste grows the box visibly" case syncInputHeight's
// doc comment promises.
func TestUpdate_PasteGrowsTheBox(t *testing.T) {
	m := newFrameModel(StatusHeader, 100, 40)
	out, _ := m.Update(tea.PasteMsg{Content: strings.Repeat("pasted line\n", 5) + "pasted line"})
	m = out.(Model)

	if got, want := m.input.LineCount(), 6; got != want {
		t.Fatalf("paste produced %d lines, want %d", got, want)
	}
	if got := m.input.Height(); got != 6 {
		t.Errorf("box is %d rows after a 6-line paste, want 6", got)
	}
	assertBudgetExact(t, m)
	assertFrameFits(t, m.View().Content, 100, 40)
}
