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
//  1. **Per event** — re-wrap only the messages the operator can
//     actually see (chatVisitWindow), and only on the LEADING event
//     of a drag; every event behind it does nothing but the geometry
//     (issue #247, and see below). Bounded by viewport height, not
//     by transcript length.
//  2. **On visible settle** — resizeVisibleWindow after the last
//     event, re-wrap the visible window again, this time at the
//     width the drag came to rest at.
//  3. **On backlog settle** — resizeSettleWindow after that, warm
//     the rest of the transcript in resizeWarmChunk-sized slices
//     separated by resizeWarmTick, never as one synchronous burst,
//     so no single frame blocks for a perceptible time.
//
// Every tick carries the resizeGen stamp taken when it was
// scheduled (same guard style as the sessionGen checks in
// update.go). A visible, settle or warm tick whose stamp is stale
// belongs to a superseded drag and is dropped, so a slow callback
// can never clobber the state of a newer resize.
//
// Why the events behind the leading one do no work at all (issue
// #247). The per-event visible reflow was already bounded — at a
// 40-row viewport it re-Glamoured about two assistant messages per
// event, flat in transcript length, which is what #104 and #161 were
// for. It was still 79% of the event's cost, because a single
// Glamour render of a 500-byte assistant turn measures ~5ms and that
// is a property of Glamour, not of anything this package can shrink:
// rendering the string "hello world" through Glamour's own bundled
// dark style measures ~0.3ms. Two of those per event put a drag's
// p95 at 15-17ms — on the wrong side of a 16ms frame, with a window
// already bounded by the viewport and nothing left to bound. So the
// events in the middle of a drag draw the rows they already have,
// cut to the new width at the draw site (chatCutLine), and the
// re-wrap is paid once the mouse stops. Measured over a real
// tea.Program at 30 events 30ms apart: enqueue-to-frame p95 falls
// from 15.1-17.1ms to 6.6-7.8ms and stays flat from 10 to 1000
// turns, at the cost of taking convergence after the last event from
// ~8ms to ~22ms.
//
// The invariant that gives up: for up to resizeVisibleWindow after a
// width change mid-drag, an on-screen row may be wrapped for a
// PREVIOUS width. Narrowing, it is cut and the operator sees a
// clipped right edge; widening, it is short of the new margin. Both
// resolve in one frame once the drag pauses. What does NOT give is
// the line count the lazy walk budgets against — chatCutLine cuts
// per drawn line precisely so the row's height is unchanged — so the
// window stays exact and the scroll geometry never lurches, and
// nothing is ever drawn WIDER than the terminal.
//
// Off-screen backlog is left at the previous width until the warm
// sequence reaches it, and since #161 that is harmless by
// construction rather than by timing: an item-addressed transcript
// never draws a row it has not rendered at the current width, so a
// row that has not been reflowed yet is a row nothing has asked
// for.

package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	// resizeVisibleWindow is how long the drag must be quiet before
	// the ON-SCREEN rows are re-wrapped. This is the one the operator
	// feels, so it is the short one: long enough to swallow the gap
	// between two events of a continuous drag (terminals emit them
	// every few ms while the mouse moves, and a stuttering drag over
	// a loaded SSH link is still well inside this), short enough that
	// a pause mid-drag reads as the text catching up rather than as a
	// delay. Deliberately far below the ~100ms at which a pointer
	// gesture stops feeling direct.
	resizeVisibleWindow = 40 * time.Millisecond

	// resizeSettleWindow is how much longer the drag must stay quiet,
	// after the visible re-wrap, before the transcript-wide reflow
	// starts. The backlog is off screen by definition, so this one is
	// not perceived at all — it only has to be long enough that a
	// drag which resumes after a brief pause doesn't start warming
	// 400 turns at a width it is about to leave.
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

// resizeReflowMsg drives every phase of the debounced reflow after
// the event itself: the visible tick scheduled by the WindowSizeMsg
// handler, the backlog settle tick that follows it, and each
// follow-on warm tick. gen is the resizeGen stamp captured when the
// tick was scheduled — the Update handler drops the msg when it no
// longer matches, which is what makes a superseded drag's callbacks
// harmless. visible distinguishes the first tick from the rest;
// without it a stale-width row would have to be told apart from a
// cold backlog row by inspecting the transcript, and the two are the
// same row in different states.
type resizeReflowMsg struct {
	gen     uint64
	visible bool
}

// resizeVisibleTick fires the on-screen re-wrap once the drag has
// been quiet for resizeVisibleWindow.
func resizeVisibleTick(gen uint64) tea.Cmd {
	return tea.Tick(resizeVisibleWindow, func(time.Time) tea.Msg {
		return resizeReflowMsg{gen: gen, visible: true}
	})
}

// resizeSettleTick fires the first backlog slice once the drag has
// been quiet for a further resizeSettleWindow.
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
// handler. It stamps a fresh generation, re-wraps the screen if this
// is the leading edge, and returns the visible tick that will re-wrap
// it once the drag pauses. Runs in Update (never in a Cmd) — it
// mutates history.
//
// Leading-edge, not purely trailing: the FIRST width change after a
// quiet period pays the re-wrap synchronously, and only the events
// behind it are suppressed. Three reasons. A lone resize — a tmux
// pane bound to a key, a tiling WM snapping the window, the very
// first sizing of the program, where there is no previous width to
// fall back on and the rows have never been wrapped at all — is one
// event, and it should land in the frame it arrived in rather than
// 40ms later. A drag pays the cost once at the top and then runs
// free, which is the shape the operator feels as "it kept up". And
// the leading frame is the one the eye is least able to time, because
// it is simultaneous with the mouse-down.
func (m *Model) beginResizeReflow() tea.Cmd {
	m.resizeGen++
	// reflowHot doubles as "a drag is already in flight": it is set
	// at the bottom of this function and cleared by the visible tick,
	// so reading it here — before the write — is exactly the
	// leading-edge test.
	leading := !m.reflowHot
	m.reflowPending = true
	m.reflowCursor = 0
	// Only messages that already existed at the moment of the width
	// change can be carrying a stale wrap. Anything appended after
	// it (a turn that finalizes mid-drag) is rendered at the current
	// width by the commit path, so the reflow must leave it alone —
	// otherwise every repaint during a long-lived pending reflow
	// would re-Glamour fresh rows for nothing.
	//
	// Taken per event rather than only on the first event of a drag:
	// each event is its own width change, and a turn that finalized
	// between two of them is current at the width it committed to,
	// not at this one.
	m.reflowMaxID = m.history.LastID()
	if leading {
		m.reflowVisible()
	}
	m.reflowHot = true
	return resizeVisibleTick(m.resizeGen)
}

// finishResizeDrag is the visible tick's handler: the drag has
// stopped moving, so the rows on screen can be re-wrapped and the
// backlog settle can be scheduled behind them. Returns the repaint
// plus that tick.
func (m *Model) finishResizeDrag() tea.Cmd {
	// Cleared before the pending check: reflowHot is what tells the
	// next width change it is a leading edge, and a drag that ended
	// with nothing left to reflow still ended.
	m.reflowHot = false
	if !m.reflowPending {
		return nil
	}
	// refreshViewport runs reflowVisible itself, in the middle: after
	// the tail is rebuilt (which decides where a following operator's
	// window starts) and before the offset clamp (which needs the
	// line counts the re-wrap just changed). Calling it directly here
	// as well would only walk the window twice.
	//
	// Direct, not the coalescing path: this frame is the one the
	// operator has been waiting on since they stopped dragging, and
	// the 1ms coalescing window exists to merge paints nobody is
	// waiting on.
	m.refreshViewport()
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
// rather than transcript length. No-op unless a reflow is pending, so
// the common repaint path pays one bool check — and no-op while the
// drag is hot, which is the whole of #247: this is the pass that
// costs ~5ms per on-screen assistant turn, and a drag cannot afford
// it at any window size.
//
// The hot check lives here rather than at the call sites because
// refreshViewport is one of them: a repaint that lands mid-drag —
// a stream chunk, a spinner frame — must not quietly do the work the
// resize handler just declined to do. beginResizeReflow's leading
// edge calls this BEFORE setting the flag, which is how one event of
// a drag gets the re-wrap and the rest do not.
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
	if !m.reflowPending || m.reflowHot {
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
