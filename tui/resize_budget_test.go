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

// Row-budget tests for resize() (issue #103).
//
// The frame-invariant grid in frame_invariant_test.go proves the
// frame never EXCEEDS the terminal — but it is satisfied by the
// clipping post-pass at the end of View(), so it stays green even
// when resize() hands out more rows than exist and clipFrame
// silently eats the footer. These tests assert the property
// clipFrame cannot fake: the rows resize() budgets add up to the
// terminal exactly, so nothing is clipped and nothing is wasted.

package tui

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
)

// composedRows sums the height of every element View joins around
// the viewport, plus the viewport itself. This is the frame height
// BEFORE clipFrame runs, which is the number resize() is actually
// responsible for and the one View()'s output can no longer show.
//
// It deliberately mirrors View's composition rather than calling
// View: the whole point is to catch a budget that only looks right
// because the frame got trimmed on the way out.
func composedRows(m Model) int {
	chromeWidth := m.width
	rows := 0
	if m.effectiveLayout() == StatusSidebar {
		chromeWidth = m.width - sidebarWidth - 3
	} else {
		rows += lipgloss.Height(m.renderHeader())
	}
	rows += m.viewport.Height()
	if m.helpOpen {
		rows += lipgloss.Height(m.renderHelpPanel(chromeWidth))
	}
	if m.palette != nil {
		rows += lipgloss.Height(m.renderPalette(chromeWidth))
	}
	rows += lipgloss.Height(m.renderInputBox())
	if toast := m.renderToast(chromeWidth); toast != "" {
		rows += lipgloss.Height(toast)
	}
	rows += lipgloss.Height(m.renderFooter(chromeWidth))
	return rows
}

// assertBudgetExact checks that the composed frame is exactly
// m.height rows. Skipped when the viewport has bottomed out at
// chatMinHeight: at that point the terminal is too short to hold
// the chrome at all, resize() has stopped subtracting, and
// clipFrame legitimately takes over.
func assertBudgetExact(t *testing.T, m Model) {
	t.Helper()
	if m.viewport.Height() <= chatMinHeight {
		t.Skipf("viewport bottomed out at the %d-row floor; budget is exhausted, not wrong", chatMinHeight)
	}
	if got := composedRows(m); got != m.height {
		t.Errorf("composed frame is %d rows in a %d-row terminal (delta %+d); "+
			"resize budgeted a viewport of %d",
			got, m.height, got-m.height, m.viewport.Height())
	}
}

// TestResize_NarrowHeaderIsMeasured is issue #103 part 1. At width
// 20 the status line word-wraps well past the two rows resize()
// used to hardcode, so the viewport was sized as though the header
// were 2 rows while it occupied 4 — and the composed frame ran a
// row over the terminal, where clipFrame dropped the bottom of the
// footer.
func TestResize_NarrowHeaderIsMeasured(t *testing.T) {
	pinChromaStyle(t)
	pinCwd(t)
	for _, w := range []int{20, 24, 30, 40} {
		t.Run("width-"+strconv.Itoa(w), func(t *testing.T) {
			m := goldenModel(t, w, 24)
			headerRows := lipgloss.Height(m.renderHeader())
			if headerRows <= 2 {
				t.Fatalf("precondition: header should wrap past 2 rows at width %d, got %d",
					w, headerRows)
			}
			assertBudgetExact(t, m)
			// The viewport must leave room for the REAL header, not
			// the assumed one.
			if room := m.height - m.viewport.Height(); room < headerRows {
				t.Errorf("viewport of %d rows leaves only %d rows of chrome for a %d-row header",
					m.viewport.Height(), room, headerRows)
			}
			assertFrameFits(t, m.View().Content, w, 24)
		})
	}
}

// TestResize_WideHeaderStillTwoRows pins the other side of the same
// change: measuring must not move the budget on a terminal wide
// enough for the status line to fit on one row.
func TestResize_WideHeaderStillTwoRows(t *testing.T) {
	pinChromaStyle(t)
	pinCwd(t)
	m := goldenModel(t, 120, 24)
	if got := lipgloss.Height(m.renderHeader()); got != 2 {
		t.Fatalf("header at width 120 = %d rows, want 2 (status + spacer)", got)
	}
	assertBudgetExact(t, m)
}

// TestResize_ToastIsBudgeted is issue #103 part 2. View slots the
// wake banner between the input box and the footer; resize() had no
// toast term, so a visible toast made the frame one row taller than
// the budget.
func TestResize_ToastIsBudgeted(t *testing.T) {
	pinChromaStyle(t)
	pinCwd(t)
	base := goldenModel(t, 100, 24)
	quiet := base.viewport.Height()

	m := goldenModel(t, 100, 24)
	m.toast = "woke up: agent switched to sonnet"
	m.toastSetAt = time.Now()
	m.resize()

	if m.renderToast(m.width) == "" {
		t.Fatal("precondition: the toast should be visible")
	}
	if m.viewport.Height() != quiet-1 {
		t.Errorf("viewport is %d rows with a toast and %d without; the toast row was not reserved",
			m.viewport.Height(), quiet)
	}
	assertBudgetExact(t, m)
	assertFrameFits(t, m.View().Content, 100, 24)
}

// TestResize_ExpiredToastIsNotBudgeted guards the other direction:
// the budget is keyed on what renderToast actually returns, so a
// toast whose TTL has elapsed must not hold a row hostage.
func TestResize_ExpiredToastIsNotBudgeted(t *testing.T) {
	pinChromaStyle(t)
	pinCwd(t)
	quiet := goldenModel(t, 100, 24).viewport.Height()

	m := goldenModel(t, 100, 24)
	m.toast = "stale"
	m.toastSetAt = time.Now().Add(-10 * toastTTL)
	m.resize()

	if m.renderToast(m.width) != "" {
		t.Fatal("precondition: the toast should have expired")
	}
	if m.viewport.Height() != quiet {
		t.Errorf("expired toast still cost %d viewport rows", quiet-m.viewport.Height())
	}
	assertBudgetExact(t, m)
}

// TestResize_QueuePanelIsNotChargedTwice is the third finding in
// issue #103, which flagged it as unverified. It IS a real
// double-charge, and the evidence is the first assertion here:
// renderQueuePanel's output arrives via renderInProgress inside the
// viewport's CONTENT, not as a sibling row joined by View. Charging
// it in resize() shrank the viewport by the panel's height and then
// spent that height drawing the panel inside the now-smaller
// viewport — the operator lost the rows twice and the frame came up
// short of the terminal.
func TestResize_QueuePanelIsNotChargedTwice(t *testing.T) {
	pinChromaStyle(t)
	pinCwd(t)
	quiet := goldenModel(t, 100, 24).viewport.Height()

	m := goldenModel(t, 100, 24)
	m.queue = []QueueEntry{
		{Text: "first queued prompt", State: QueueQueued},
		{Text: "second queued prompt", State: QueueQueued},
	}
	m.resize()
	m.refreshViewport()

	// The premise: the panel lives inside the viewport.
	if !strings.Contains(m.viewport.View(), "queue (") {
		t.Fatal("precondition: the queue panel should render inside the viewport content")
	}
	if panelRows := lipgloss.Height(m.renderQueuePanel()); panelRows < 2 {
		t.Fatalf("precondition: expected a multi-row queue panel, got %d", panelRows)
	}
	if m.viewport.Height() != quiet {
		t.Errorf("queue shrank the viewport to %d rows (from %d); the panel is rendered "+
			"inside the viewport, so it must not also be subtracted from it",
			m.viewport.Height(), quiet)
	}
	assertBudgetExact(t, m)
	assertFrameFits(t, m.View().Content, 100, 24)
}

// TestResize_HelpAndPaletteStayBudgeted is the control for the
// queue case: help and palette ARE siblings of the viewport in
// View, so they must keep costing rows.
func TestResize_HelpAndPaletteStayBudgeted(t *testing.T) {
	pinChromaStyle(t)
	pinCwd(t)
	quiet := goldenModel(t, 100, 60).viewport.Height()

	m := goldenModel(t, 100, 60)
	m.helpOpen = true
	m.resize()
	helpRows := lipgloss.Height(m.renderHelpPanel(100))
	if m.viewport.Height() != quiet-helpRows {
		t.Errorf("help panel of %d rows moved the viewport from %d to %d",
			helpRows, quiet, m.viewport.Height())
	}
	assertBudgetExact(t, m)
}

// TestResize_BudgetExactGrid sweeps the same axes as the
// frame-invariant grid but asserts exactness rather than the
// one-sided "does not overflow". Anything View renders that resize
// forgets — or measures at the wrong width — shows up here as a
// non-zero delta in one cell.
func TestResize_BudgetExactGrid(t *testing.T) {
	layouts := []struct {
		name   string
		layout StatusLayout
	}{
		{"header", StatusHeader},
		{"sidebar", StatusSidebar},
	}
	for _, lay := range layouts {
		for _, w := range frameWidths {
			for _, h := range frameHeights {
				name := lay.name + "/" + strconv.Itoa(w) + "x" + strconv.Itoa(h)
				t.Run(name, func(t *testing.T) {
					m := newFrameModel(lay.layout, w, h)
					m = withHostileTranscript(m)
					assertBudgetExact(t, m)
				})
			}
		}
	}
}
