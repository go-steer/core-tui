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

// Debounced resize reflow (issue #104).
//
// A terminal pane drag does not emit one tea.WindowSizeMsg — it
// emits a stream of them, one per column crossed. The old
// WindowSizeMsg handler ran rerenderHistoryMarkdown inline: every
// assistant message in the transcript went back through Glamour,
// synchronously, inside the message handler. That measured 91.5ms
// per event at 100 turns (~91% of it the Glamour pass), so a drag
// froze the UI for as long as the operator held the mouse down.
//
// The shape here is three-phase:
//
//  1. **Per event** — reflow only the messages the operator can
//     actually see (chatVisitWindow), then schedule a settle tick.
//     Bounded by viewport height, not by transcript length, so the
//     cost per event no longer grows with session age.
//  2. **On settle** — resizeSettleWindow after the LAST event, warm
//     the rest of the transcript.
//  3. **Warming** — in resizeWarmChunk-sized slices separated by
//     resizeWarmTick, never as one synchronous burst, so no single
//     frame blocks for a perceptible time.
//
// Every tick carries the resizeGen stamp taken when it was
// scheduled (same guard style as the sessionGen checks in
// update.go). A settle or warm tick whose stamp is stale belongs to
// a superseded drag and is dropped, so a slow callback can never
// clobber the state of a newer resize.
//
// The invariant this preserves: anything ON SCREEN is rendered at
// the current width — reflowVisible runs from refreshViewport too,
// so a scroll into cold backlog mid-warm reflows the rows it exposes
// in that same frame. Off-screen backlog is left at the previous
// width until the warm sequence reaches it, and since #161 that is
// harmless by construction rather than by timing: an item-addressed
// transcript never draws a row it has not rendered at the current
// width, so a row that has not been reflowed yet is a row nothing has
// asked for.

package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	// resizeSettleWindow is how long the drag must be quiet before
	// the transcript-wide reflow starts. Long enough to swallow a
	// whole drag (terminals emit resize events every few ms while
	// the mouse moves), short enough that the operator does not
	// perceive the settle as a separate event: releasing the mouse
	// and seeing the backlog reflow reads as one action.
	resizeSettleWindow = 120 * time.Millisecond

	// resizeWarmWindow is the gap between warm slices. It exists to
	// yield the event loop back to Bubble Tea between slices —
	// keystrokes and stream chunks land at full priority while the
	// backlog warms behind them.
	resizeWarmWindow = 2 * time.Millisecond

	// resizeWarmChunk is how many messages one warm slice re-renders
	// through Glamour. At ~1ms per assistant message this keeps a
	// slice comfortably inside a frame budget while still retiring
	// a 400-turn backlog in well under a second.
	resizeWarmChunk = 8
)

// resizeReflowMsg drives both phases of the debounced reflow: the
// settle tick scheduled by the WindowSizeMsg handler, and each
// follow-on warm tick. gen is the resizeGen stamp captured when the
// tick was scheduled — the Update handler drops the msg when it no
// longer matches, which is what makes a superseded drag's callbacks
// harmless.
type resizeReflowMsg struct{ gen uint64 }

// resizeSettleTick fires resizeReflowMsg once the drag has been
// quiet for resizeSettleWindow.
func resizeSettleTick(gen uint64) tea.Cmd {
	return tea.Tick(resizeSettleWindow, func(time.Time) tea.Msg {
		return resizeReflowMsg{gen: gen}
	})
}

// resizeWarmTick fires the next warm slice.
func resizeWarmTick(gen uint64) tea.Cmd {
	return tea.Tick(resizeWarmWindow, func(time.Time) tea.Msg {
		return resizeReflowMsg{gen: gen}
	})
}

// beginResizeReflow is the width-change half of the WindowSizeMsg
// handler. It stamps a fresh generation, reflows what the operator
// can see right now, and returns the settle tick that will warm the
// rest once the drag stops. Runs in Update (never in a Cmd) — it
// mutates history.
func (m *Model) beginResizeReflow() tea.Cmd {
	m.resizeGen++
	m.reflowPending = true
	m.reflowCursor = 0
	// Only messages that already existed at the moment of the width
	// change can be carrying a stale wrap. Anything appended after
	// it (a turn that finalizes mid-drag) is rendered at the current
	// width by the commit path, so the reflow must leave it alone —
	// otherwise every repaint during a long-lived pending reflow
	// would re-Glamour fresh rows for nothing.
	m.reflowMaxID = m.history.LastID()
	m.reflowVisible()
	return resizeSettleTick(m.resizeGen)
}

// continueResizeReflow runs one warm slice and returns the Cmd for
// whatever comes next: another warm tick while messages remain, or
// just the coalesced repaint once the backlog is fully warm. The
// generation guard lives in the Update handler.
func (m *Model) continueResizeReflow() tea.Cmd {
	if !m.reflowPending {
		return nil
	}
	done := m.warmReflowSlice()
	if !done {
		// No repaint between slices. Everything ON screen is already
		// at the current width (reflowVisible runs from
		// refreshViewport), and each slice leaves the rows it warmed
		// fully re-rendered in the cache — so an intermediate paint
		// would re-concatenate the whole transcript to show nothing
		// new. At 400 turns that concat is the single most expensive
		// thing in the settle, and doing it per slice made the warm
		// pass quadratic.
		return resizeWarmTick(m.resizeGen)
	}
	m.reflowPending = false
	// One repaint at the end, through the existing 1ms coalescing
	// path (markViewportDirty + scheduleCoalescedRefresh in
	// agentcmd.go) rather than a direct refreshViewport: a settle
	// that lands in the same window as a stream chunk then shares
	// one paint with it instead of forcing a second. Line counts
	// have changed under the viewport, so this paint is what
	// re-establishes the scroll geometry (and the tail pin, for a
	// following operator).
	m.markViewportDirty()
	return m.scheduleCoalescedRefresh()
}

// reflowVisible re-renders the assistant messages inside the current
// viewport window at the current width. Bounded by viewport height
// rather than transcript length — this is the work a resize event is
// allowed to do synchronously. No-op unless a reflow is pending, so
// the common repaint path pays one bool check.
//
// The walk is chatVisitWindow's, which re-wraps a row and then
// measures it to decide whether to continue. Ordering the two that way
// is what keeps the pass exact: the row the budget is spent on is the
// row at its new width, so the window it stops at is the window the
// operator will actually see rather than a screenful-shaped guess.
//
// history.at, not Snapshot: this runs from every refreshViewport while
// a reflow is pending, and copying the whole transcript to look at a
// screenful of it costs ~220KB per repaint at 400 turns.
func (m *Model) reflowVisible() {
	if !m.reflowPending {
		return
	}
	mr := m.ensureMarkdown()
	if mr == nil {
		return
	}
	n := m.history.Len()
	m.chatVisitWindow(func(i int) {
		if i >= n {
			// The live tail, which refreshViewport rebuilds from
			// scratch every pass — it never carries a stale width.
			return
		}
		msg, ok := m.history.at(i)
		if !ok {
			return
		}
		m.reflowMessage(i, msg, mr)
	})
}

// warmReflowSlice retires up to resizeWarmChunk still-stale messages,
// resuming from reflowCursor. Returns true when the walk reached the
// end of the transcript — i.e. nothing is left at a stale width.
//
// A slice owns BOTH halves of a row's cost: the Glamour re-wrap and
// the re-assembly into a cached styled row. Leaving the second half
// to the next paint would make it land as one burst the first time
// refreshViewport ran, which is the thing this whole path exists to
// avoid.
//
// Since #161 this pass is a PREFETCH rather than a repair. An
// item-addressed window never draws a row it has not rendered at the
// current width, so a backlog left cold is correct, just slow to
// scroll into. Warming it here is what keeps that scroll instant, and
// spreading it over ticks is what keeps the warming itself
// imperceptible.
func (m *Model) warmReflowSlice() bool {
	mr := m.ensureMarkdown()
	if mr == nil {
		return true
	}
	n := m.history.Len()
	if m.reflowCursor < 0 {
		m.reflowCursor = 0
	}
	budget := resizeWarmChunk
	for ; m.reflowCursor < n; m.reflowCursor++ {
		msg, ok := m.history.at(m.reflowCursor)
		if !ok {
			break
		}
		work := m.reflowMessage(m.reflowCursor, msg, mr)
		// A row whose markdown did not have to change can still be
		// missing from the cache at the settled width — user cards
		// and tool rows never touch Glamour, and the drag never drew
		// them. They owe an assembly, and it is charged like any
		// other so a screenful of them cannot land in one slice.
		if !m.chatRowCached(msg) {
			work = true
		}
		if !work {
			continue
		}
		m.warmRowCache(m.reflowCursor, n)
		budget--
		if budget == 0 {
			m.reflowCursor++
			return m.reflowCursor >= n
		}
	}
	return true
}

// warmRowCache re-renders row i at the current width and stores it,
// so the next paint reads it straight out of the cache.
func (m *Model) warmRowCache(i, total int) {
	if m.listCache == nil {
		return
	}
	msg, ok := m.history.at(i)
	if !ok {
		return
	}
	item := messageItem{msg: msg, idx: i, total: total}
	m.listCache.put(item, m.viewport.Width(), m.renderMessage(msg))
}

// reflowMessage re-runs Glamour on entry i at the renderer's width
// when the entry's cached render is pinned to some other width.
// Returns true when it actually rendered — callers use that to
// charge their slice budget only for real work.
//
// The width comparison is what makes the pass idempotent: dragging
// a pane narrow and back wide again leaves the messages that were
// never touched at their original width, and they are skipped
// outright on the way back.
func (m *Model) reflowMessage(i int, msg Message, mr *markdownRenderer) bool {
	if msg.Role != RoleAssistant || msg.Text == "" {
		return false
	}
	if msg.ID > m.reflowMaxID {
		// Appended after the width change — already current.
		return false
	}
	if msg.Rendered != "" && msg.renderedWidth == mr.width {
		return false
	}
	m.history.setRenderedAt(i, mr.renderMarkdown(msg.Text), mr.width)
	return true
}
