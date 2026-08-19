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
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Debounced resize reflow (issue #104). The contract under test:
//
//   - a width-changing event reflows only what is on screen;
//   - the settle tick warms the rest, in slices;
//   - a settle tick from a superseded drag is ignored;
//   - follow state survives a drag (issue #93 must not regress);
//   - the list cache is not dropped when the width did not change.

// maxRenderedWidth returns the widest display line in s, ignoring
// ANSI escapes. Glamour word-wraps to its configured width, so this
// is how a test checks that a message really was reflowed.
func maxRenderedWidth(s string) int {
	widest := 0
	for _, line := range strings.Split(s, "\n") {
		if w := lipgloss.Width(strings.TrimRight(line, " ")); w > widest {
			widest = w
		}
	}
	return widest
}

// settleResizeReflow delivers settle + warm ticks (plus the
// coalesced repaint each schedules) until the reflow retires.
// Mirrors what the Bubble Tea runtime does with the returned Cmds,
// minus the wall-clock waits.
func settleResizeReflow(t *testing.T, m model) model {
	t.Helper()
	m = deliverResizeVisibleTick(m)
	for i := 0; m.reflowPending; i++ {
		if i > 10000 {
			t.Fatal("resize reflow never retired")
		}
		out, _ := m.Update(resizeReflowMsg{gen: m.resizeGen})
		m = out.(model)
		out, _ = m.Update(coalescedRefreshMsg{})
		m = out.(model)
	}
	return m
}

// assistantVersions returns the per-index Version of every
// assistant row — the fingerprint of "this message was re-rendered".
func assistantVersions(m model) map[int]uint64 {
	out := map[int]uint64{}
	for i, msg := range m.history.Snapshot() {
		if msg.Role == RoleAssistant {
			out[i] = msg.Version
		}
	}
	return out
}

// TestResizeEvent_ReflowsOnlyVisible — the core of issue #104. A
// single width-changing WindowSizeMsg must NOT push the whole
// transcript back through Glamour inside the handler; it re-renders
// the on-screen window and defers the rest.
func TestResizeEvent_ReflowsOnlyVisible(t *testing.T) {
	m := newBenchDragModel(40) // 80 rows, 40 of them assistant
	before := assistantVersions(m)

	out, cmd := m.Update(tea.WindowSizeMsg{Width: 70, Height: 40})
	m = out.(model)
	if cmd == nil {
		t.Fatal("a width-changing resize must return the settle tick Cmd")
	}

	after := assistantVersions(m)
	bumped := 0
	for i, v := range after {
		if v != before[i] {
			bumped++
		}
	}
	if bumped == 0 {
		t.Fatal("expected the visible window to reflow synchronously, got no re-renders")
	}
	if bumped >= len(before) {
		t.Fatalf("resize re-rendered the whole transcript inline (%d/%d) — the pass was supposed to be deferred", bumped, len(before))
	}
	if !m.reflowPending {
		t.Error("reflowPending must stay set until the deferred warm pass retires")
	}
}

// TestResizeEvent_VisibleTextIsCorrectWidthImmediately — the
// operator must see correct-width text for the visible region right
// away, not after the settle. The tail is what a following operator
// sees, so the last assistant row must already be wrapped narrow.
func TestResizeEvent_VisibleTextIsCorrectWidthImmediately(t *testing.T) {
	m := newBenchDragModel(40)

	out, _ := m.Update(tea.WindowSizeMsg{Width: 70, Height: 40})
	m = out.(model)

	snap := m.history.Snapshot()
	last := snap[len(snap)-1]
	if last.Role != RoleAssistant {
		t.Fatalf("setup: expected an assistant row at the tail, got role %v", last.Role)
	}
	width := m.viewport.Width()
	if got := maxRenderedWidth(last.Rendered); got > width {
		t.Errorf("tail message still wrapped at the old width: widest line %d > viewport %d", got, width)
	}
	if last.renderedWidth != width {
		t.Errorf("tail message renderedWidth = %d, want %d", last.renderedWidth, width)
	}
}

// TestResizeSettle_ReflowsWholeHistory — after the drag settles,
// nothing is left at a stale wrap width.
func TestResizeSettle_ReflowsWholeHistory(t *testing.T) {
	m := newBenchDragModel(40)

	// A drag: several width-changing events back to back.
	for _, w := range []int{110, 104, 96, 88, 80} {
		out, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: 40})
		m = out.(model)
	}
	m = settleResizeReflow(t, m)

	width := m.viewport.Width()
	for i, msg := range m.history.Snapshot() {
		if msg.Role != RoleAssistant {
			continue
		}
		if msg.renderedWidth != width {
			t.Fatalf("message %d not reflowed after settle: renderedWidth=%d want %d", i, msg.renderedWidth, width)
		}
		if got := maxRenderedWidth(msg.Rendered); got > width {
			t.Fatalf("message %d wrapped too wide after settle: %d > %d", i, got, width)
		}
	}
}

// TestResizeSettle_WarmsIncrementally — the warm pass must not be
// one synchronous burst. With 40 assistant rows and a chunk of
// resizeWarmChunk, retiring the backlog has to take several ticks.
func TestResizeSettle_WarmsIncrementally(t *testing.T) {
	m := newBenchDragModel(40)
	out, _ := m.Update(tea.WindowSizeMsg{Width: 70, Height: 40})
	m = out.(model)

	ticks := 0
	for m.reflowPending {
		ticks++
		if ticks > 10000 {
			t.Fatal("resize reflow never retired")
		}
		out, cmd := m.Update(resizeReflowMsg{gen: m.resizeGen})
		m = out.(model)
		if m.reflowPending && cmd == nil {
			t.Fatal("an unfinished warm pass must return the next warm tick")
		}
	}
	if ticks < 2 {
		t.Errorf("warm pass retired in %d tick(s) — it is supposed to be sliced, not a single burst", ticks)
	}
}

// TestResizeSettle_StaleTickDropped — the generation guard. Two
// drags overlap; the older drag's settle tick must not resume a walk
// that a newer resize has already restarted.
func TestResizeSettle_StaleTickDropped(t *testing.T) {
	m := newBenchDragModel(40)

	out, _ := m.Update(tea.WindowSizeMsg{Width: 70, Height: 40})
	m = out.(model)
	staleGen := m.resizeGen

	// Advance the first drag's warm walk a little so a stale tick
	// resuming it would be visible as cursor movement.
	out, _ = m.Update(resizeReflowMsg{gen: staleGen})
	m = out.(model)
	if m.reflowCursor == 0 {
		t.Fatal("setup: expected the warm walk to have advanced")
	}

	// A newer resize supersedes it and restarts the walk.
	out, _ = m.Update(tea.WindowSizeMsg{Width: 60, Height: 40})
	m = out.(model)
	if m.resizeGen == staleGen {
		t.Fatal("a width change must bump resizeGen")
	}
	cursorAfterNewResize := m.reflowCursor
	versionsAfterNewResize := assistantVersions(m)

	// The superseded drag's settle tick lands late.
	out, cmd := m.Update(resizeReflowMsg{gen: staleGen})
	m = out.(model)
	if cmd != nil {
		t.Error("a stale settle tick must not schedule follow-on work")
	}
	if m.reflowCursor != cursorAfterNewResize {
		t.Errorf("stale settle tick advanced the newer walk: cursor %d → %d", cursorAfterNewResize, m.reflowCursor)
	}
	for i, v := range assistantVersions(m) {
		if v != versionsAfterNewResize[i] {
			t.Fatalf("stale settle tick re-rendered message %d", i)
		}
	}

	// The current drag still settles correctly.
	m = settleResizeReflow(t, m)
	width := m.viewport.Width()
	for i, msg := range m.history.Snapshot() {
		if msg.Role == RoleAssistant && msg.renderedWidth != width {
			t.Fatalf("message %d left stale at width %d (want %d) after the newer drag settled", i, msg.renderedWidth, width)
		}
	}
}

// TestResizeDrag_KeepsFollow — issue #93's follow bit must survive a
// drag: an operator pinned to the tail stays pinned.
func TestResizeDrag_KeepsFollow(t *testing.T) {
	m := newBenchDragModel(40)
	if !m.follow {
		t.Fatal("setup: expected the model to start following the tail")
	}

	for i := range dragBurst {
		out, _ := m.Update(tea.WindowSizeMsg{Width: dragWidthAt(i), Height: 40})
		m = out.(model)
	}
	if !m.follow {
		t.Fatal("drag dropped the operator off the tail")
	}
	m = settleResizeReflow(t, m)
	if !m.follow {
		t.Fatal("settle dropped the operator off the tail")
	}
	if !m.chatAtBottom() {
		t.Error("following model is not scrolled to the bottom after a drag")
	}
}

// TestResizeDrag_KeepsScrollbackDetached — the mirror case: an
// operator who has scrolled UP must not be yanked to the tail by a
// resize, and the rows they are looking at must still reflow.
func TestResizeDrag_KeepsScrollbackDetached(t *testing.T) {
	m := newBenchDragModel(40)
	m.chatSetYOffset(0)
	m.syncFollow()
	if m.follow {
		t.Fatal("setup: expected follow to drop after scrolling to the top")
	}

	out, _ := m.Update(tea.WindowSizeMsg{Width: 70, Height: 40})
	m = out.(model)
	if m.follow {
		t.Error("resize re-armed follow for an operator reading scrollback")
	}
	if m.chatAtBottom() {
		t.Error("resize yanked a detached operator to the tail")
	}

	// The rows on screen at the top of the transcript are the ones
	// that had to reflow.
	snap := m.history.Snapshot()
	width := m.viewport.Width()
	for i, msg := range snap {
		if msg.Role != RoleAssistant {
			continue
		}
		if msg.renderedWidth != width {
			t.Errorf("first visible assistant row (index %d) was not reflowed: renderedWidth=%d want %d", i, msg.renderedWidth, width)
		}
		break
	}
}

// TestResize_ListCacheSurvivesUnchangedWidth — a height-only resize
// (and any repaint) must not drop the render memo. Before #104 the
// cache pinned a single width and reset on any mismatch; the entries
// are now width-keyed, so an unchanged width keeps every hit.
func TestResize_ListCacheSurvivesUnchangedWidth(t *testing.T) {
	m := newBenchDragModel(20)
	entriesBefore := len(m.listCache.entries)
	if entriesBefore == 0 {
		t.Fatal("setup: expected a warm list cache")
	}

	out, _ := m.Update(tea.WindowSizeMsg{Width: benchDragBaseWidth, Height: 50})
	m = out.(model)

	if got := len(m.listCache.entries); got < entriesBefore {
		t.Errorf("height-only resize dropped cache entries: %d → %d", entriesBefore, got)
	}
	snap := m.history.Snapshot()
	item := messageItem{msg: snap[0], idx: 0, total: len(snap)}
	if _, ok := m.listCache.get(item, m.viewport.Width()); !ok {
		t.Error("expected a cache hit at the unchanged width")
	}
}

// TestResize_ListCacheKeepsEarlierWidth — a message already rendered
// at the incoming width survives the resize, so dragging back to a
// width the cache has seen is a hit rather than a cold rebuild.
func TestResize_ListCacheKeepsEarlierWidth(t *testing.T) {
	m := newBenchDragModel(20)
	wide := m.viewport.Width()
	snap := m.history.Snapshot()
	// Pick a row the visible-window reflow will not touch, so its
	// Version (and therefore its cache entry) stays valid.
	item := messageItem{msg: snap[0], idx: 0, total: len(snap)}
	if _, ok := m.listCache.get(item, wide); !ok {
		t.Fatal("setup: expected a warm entry at the starting width")
	}

	out, _ := m.Update(tea.WindowSizeMsg{Width: benchDragBaseWidth - 6, Height: 40})
	m = out.(model)
	if m.viewport.Width() == wide {
		t.Fatal("setup: expected the chat width to change")
	}

	snap = m.history.Snapshot()
	item = messageItem{msg: snap[0], idx: 0, total: len(snap)}
	if _, ok := m.listCache.get(item, wide); !ok {
		t.Error("the width-keyed entry from before the resize should have survived")
	}
}

// TestResizeSettle_WarmsTheBacklogTheDragSkipped — the two halves of
// the resize contract, in order.
//
// An item-addressed window (#161) only ever asks for the rows it is
// about to draw, so a drag does not re-assemble the backlog at each
// intermediate width — those rows are simply absent from the cache
// there. That absence IS the bound on the per-event cost, and it is
// what the first half asserts. The settle then owes the backlog: after
// it, every row must be present at the settled width and byte-
// identical to a cold render, so nothing carries the drag's
// intermediate state into a later scroll.
func TestResizeSettle_WarmsTheBacklogTheDragSkipped(t *testing.T) {
	m := newBenchDragModel(40)
	for _, w := range []int{112, 100, 92, 84} {
		out, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: 40})
		m = out.(model)
	}
	width := m.viewport.Width()
	drawn := 0
	for key := range m.listCache.entries {
		if key.width == width {
			drawn++
		}
	}
	snap := m.history.Snapshot()
	if drawn == 0 {
		t.Fatal("the drag drew nothing at the settled width — the visible window must render immediately")
	}
	if drawn >= len(snap) {
		t.Fatalf("the drag assembled %d of %d rows at width %d; it is supposed to draw only the visible window", drawn, len(snap), width)
	}

	m = settleResizeReflow(t, m)

	// Byte-for-byte equality with a cold render at the settled
	// width: nothing is left carrying the drag's intermediate state.
	snap = m.history.Snapshot()
	for i, msg := range snap {
		item := messageItem{msg: msg, idx: i, total: len(snap)}
		got, ok := m.listCache.get(item, width)
		if !ok {
			t.Fatalf("message %d has no exact cache entry after the settle", i)
		}
		if want := m.renderMessage(msg); got != want {
			t.Fatalf("message %d is not the cold render at width %d", i, width)
		}
	}
}

// TestResizeMidWarm_ScrollExposesExactRows — the operator scrolls
// into cold backlog before the warm pass has reached it. The rows
// that scroll into view must be exact in that same frame, not
// carried-over.
//
// Mid-WARM, not mid-drag: the visible tick is delivered first, which
// is what ends the drag and lifts the #247 suppression. During the
// drag itself a scroll deliberately gets the stale-width render cut
// to the window instead — see TestResizeDrag_ScrolledRowsStayInside.
func TestResizeMidWarm_ScrollExposesExactRows(t *testing.T) {
	m := newBenchDragModel(40)
	out, _ := m.Update(tea.WindowSizeMsg{Width: 72, Height: 40})
	m = out.(model)
	m = deliverResizeVisibleTick(m)
	if !m.reflowPending {
		t.Fatal("setup: expected a pending reflow")
	}

	// Jump to the top of the transcript — the coldest region.
	m.chatGotoTop()
	m.syncFollow()
	m.refreshViewport()

	width := m.viewport.Width()
	snap := m.history.Snapshot()
	for i := 0; i < 2 && i < len(snap); i++ {
		item := messageItem{msg: snap[i], idx: i, total: len(snap)}
		got, ok := m.listCache.get(item, width)
		if !ok {
			t.Fatalf("row %d scrolled into view but has no exact render", i)
		}
		if want := m.renderMessage(snap[i]); got != want {
			t.Fatalf("row %d scrolled into view carrying a stale-width render", i)
		}
		if snap[i].Role == RoleAssistant && snap[i].renderedWidth != width {
			t.Fatalf("row %d scrolled into view without being reflowed (renderedWidth=%d want %d)", i, snap[i].renderedWidth, width)
		}
	}
}

// --- Leading-edge suppression (issue #247) ---------------------
//
// #104 bounded the per-event reflow by the viewport. #247 removes it
// from all but one event of a drag, because the bound was not
// enough: two Glamour renders is what a 40-row window costs, and two
// Glamour renders is ~10ms. The four tests below are the contract
// that replaces "every event reflows what is on screen".

// TestResizeDrag_OnlyTheLeadingEventRuns — the shape of the
// suppression. The first event of a drag re-renders the visible
// window; every event behind it re-renders nothing at all.
func TestResizeDrag_OnlyTheLeadingEventRuns(t *testing.T) {
	m := newBenchDragModel(40)

	before := assistantVersions(m)
	out, _ := m.Update(tea.WindowSizeMsg{Width: dragWidthAt(0), Height: 40})
	m = out.(model)
	leading := assistantVersions(m)
	if bumpedBetween(before, leading) == 0 {
		t.Fatal("the leading event of a drag must re-render the visible window")
	}

	for i := 1; i < dragBurst; i++ {
		prev := assistantVersions(m)
		out, _ := m.Update(tea.WindowSizeMsg{Width: dragWidthAt(i), Height: 40})
		m = out.(model)
		if n := bumpedBetween(prev, assistantVersions(m)); n != 0 {
			t.Fatalf("event %d of the drag re-rendered %d message(s) through Glamour; "+
				"only the leading event is allowed to", i, n)
		}
	}
}

// TestResizeDrag_VisibleTickReflowsTheScreen — the other half: the
// suppression is a deferral, not a drop. One tick after the drag
// stops, nothing on screen is left at a stale wrap width.
func TestResizeDrag_VisibleTickReflowsTheScreen(t *testing.T) {
	m := newBenchDragModel(40)
	for i := range dragBurst {
		out, _ := m.Update(tea.WindowSizeMsg{Width: dragWidthAt(i), Height: 40})
		m = out.(model)
	}
	width := m.viewport.Width()

	stale := staleVisibleRows(&m, width)
	if stale == 0 {
		t.Fatal("setup: expected the drag to leave the screen at a stale width")
	}

	m = deliverResizeVisibleTick(m)
	if got := staleVisibleRows(&m, width); got != 0 {
		t.Errorf("%d on-screen row(s) still at a stale wrap width one tick after the drag ended", got)
	}
}

// TestResizeDrag_LeadingEdgeRearms — the leading edge is per drag,
// not per session. Once the visible tick has ended one drag, the
// first event of the next one pays for its own re-wrap.
func TestResizeDrag_LeadingEdgeRearms(t *testing.T) {
	m := newBenchDragModel(40)
	for i := range dragBurst {
		out, _ := m.Update(tea.WindowSizeMsg{Width: dragWidthAt(i), Height: 40})
		m = out.(model)
	}
	m = deliverResizeVisibleTick(m)

	before := assistantVersions(m)
	out, _ := m.Update(tea.WindowSizeMsg{Width: 64, Height: 40})
	m = out.(model)
	if bumpedBetween(before, assistantVersions(m)) == 0 {
		t.Error("the first event of a second drag was treated as a continuation of the first")
	}
}

// TestResizeDrag_ScrolledRowsStayInside — what the suppression gives
// up, and what it must not.
//
// Mid-drag the operator scrolls into cold backlog. Those rows are
// wrapped for a width the terminal no longer has, and #247 accepts
// that: the alternative is the Glamour pass this whole path exists
// to avoid. What it does NOT accept is a line escaping the frame.
// chatCutLine cuts at the draw site, per drawn line, so a row
// wrapped 30 columns too wide is clipped rather than wrapped — the
// right edge is lost for a frame, the layout is not.
func TestResizeDrag_ScrolledRowsStayInside(t *testing.T) {
	m := newBenchDragModel(40)
	for i := range dragBurst {
		out, _ := m.Update(tea.WindowSizeMsg{Width: dragWidthAt(i), Height: 40})
		m = out.(model)
	}
	if !m.reflowHot {
		t.Fatal("setup: expected the drag to still be hot")
	}

	m.chatGotoTop()
	m.syncFollow()
	m.refreshViewport()

	if stale := staleVisibleRows(&m, m.viewport.Width()); stale == 0 {
		t.Fatal("setup: expected the scroll to expose rows at a stale width")
	}
	assertFrameFits(t, m.View().Content, m.width, m.height)
}

// bumpedBetween counts the assistant rows whose Version changed.
func bumpedBetween(before, after map[int]uint64) int {
	n := 0
	for i, v := range after {
		if v != before[i] {
			n++
		}
	}
	return n
}

// staleVisibleRows counts the assistant rows inside the current
// window whose Glamour render is pinned to some width other than
// want.
func staleVisibleRows(m *model, want int) int {
	n := 0
	total := m.history.Len()
	m.chatVisitWindow(func(i int) {
		if i >= total {
			return
		}
		msg, ok := m.history.at(i)
		if !ok || msg.Role != RoleAssistant || msg.Text == "" {
			return
		}
		if msg.renderedWidth != want {
			n++
		}
	})
	return n
}

// TestResize_HeightOnlyDoesNotScheduleReflow — height changes leave
// wrapping identical; they must not arm the settle timer at all.
func TestResize_HeightOnlyDoesNotScheduleReflow(t *testing.T) {
	m := newBenchDragModel(5)
	genBefore := m.resizeGen

	out, cmd := m.Update(tea.WindowSizeMsg{Width: benchDragBaseWidth, Height: 55})
	m = out.(model)

	if cmd != nil {
		t.Error("height-only resize must not schedule a settle tick")
	}
	if m.resizeGen != genBefore {
		t.Errorf("height-only resize bumped resizeGen: %d → %d", genBefore, m.resizeGen)
	}
	if m.reflowPending {
		t.Error("height-only resize armed a reflow")
	}
}
