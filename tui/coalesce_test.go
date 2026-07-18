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

// Tests for the event-driven viewport-refresh coalescing pattern
// (markViewportDirty + scheduleCoalescedRefresh + coalescedRefreshMsg).
// Attach-to-long-session used to be O(N²) — one full-history concat
// + viewport.SetContent per incoming SSE event, even at 100% list-
// cache hit rate. The pattern collapses many events landing inside
// coalesceWindow into a single refreshViewport call.
//
// The invariant these tests guard: state mutation stays synchronous
// (each handler still updates history / usage / model fields
// immediately, and the sessionGen guards + stale-drop tests continue
// to pass), but the paint half is deferred and coalesced.

package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestCoalescedRefresh_BurstOfEvents_SchedulesOneRefresh — the core
// coalescing invariant. Feed N stream-chunk events through Update
// and assert that refreshPending latches high (i.e. exactly one tick
// is in flight) even though every handler flipped viewportDirty.
func TestCoalescedRefresh_BurstOfEvents_SchedulesOneRefresh(t *testing.T) {
	m := NewModel(Options{Agent: newLiveAgentStub()})
	m.viewport.SetWidth(80)
	if !m.liveMode {
		t.Fatal("setup: expected liveMode=true so liveStreamRenderCmd folds in the scheduler")
	}

	const burst = 25
	for i := 0; i < burst; i++ {
		out, _ := m.Update(streamChunkMsg{text: "chunk\n\n", partial: false})
		m = out.(Model)
		if !m.viewportDirty {
			t.Fatalf("iteration %d: viewportDirty must latch true after streamChunkMsg", i)
		}
		if !m.refreshPending {
			t.Fatalf("iteration %d: refreshPending must latch true after first mark — got a redundant tick schedule", i)
		}
	}
}

// TestCoalescedRefresh_MsgHandlerClearsFlagsAndRefreshes — the paired
// paint half. When coalescedRefreshMsg lands, both flags reset and
// refreshViewport runs exactly once for the whole burst.
func TestCoalescedRefresh_MsgHandlerClearsFlagsAndRefreshes(t *testing.T) {
	m := NewModel(Options{Agent: newLiveAgentStub()})
	m.viewport.SetWidth(80)

	// Prime: one event dirties + schedules.
	out, _ := m.Update(streamChunkMsg{text: "hello\n\n", partial: false})
	m = out.(Model)
	if !m.viewportDirty || !m.refreshPending {
		t.Fatal("prime: expected both flags true after first event")
	}

	// Flush.
	out, cmd := m.Update(coalescedRefreshMsg{})
	m = out.(Model)
	if m.viewportDirty {
		t.Error("viewportDirty should be false after coalescedRefreshMsg handler")
	}
	if m.refreshPending {
		t.Error("refreshPending should be false after coalescedRefreshMsg handler")
	}
	if cmd != nil {
		t.Errorf("coalescedRefreshMsg handler should return nil Cmd, got %T", cmd)
	}

	// A subsequent burst re-schedules cleanly (flags reset properly).
	out, _ = m.Update(streamChunkMsg{text: "world\n\n", partial: false})
	m = out.(Model)
	if !m.viewportDirty || !m.refreshPending {
		t.Fatal("post-flush: expected both flags true again after next event")
	}
}

// TestScheduleCoalescedRefresh_NoOpWhenClean — the guard against
// spurious tick scheduling. When nothing has marked dirty, the
// scheduler returns nil so callers (liveStreamRenderCmd, spinner
// tick handler) don't wake bubble-tea for a refresh that has no
// state to paint.
func TestScheduleCoalescedRefresh_NoOpWhenClean(t *testing.T) {
	m := NewModel(Options{})
	m.viewport.SetWidth(80)
	if cmd := m.scheduleCoalescedRefresh(); cmd != nil {
		t.Errorf("expected nil Cmd on clean model, got %T", cmd)
	}
	if m.refreshPending {
		t.Error("refreshPending should NOT be set when nothing was dirty")
	}
}

// TestScheduleCoalescedRefresh_ReturnsTickThenNil — first call
// schedules, subsequent calls collapse to nil while the tick is in
// flight. This is the O(N²) → O(N × batch-size) mechanism: N events
// in a burst produce exactly ONE tick, not N ticks that each fire a
// refresh.
func TestScheduleCoalescedRefresh_ReturnsTickThenNil(t *testing.T) {
	m := NewModel(Options{})
	m.viewport.SetWidth(80)
	m.markViewportDirty()

	first := m.scheduleCoalescedRefresh()
	if first == nil {
		t.Fatal("first schedule with dirty flag should return non-nil tick Cmd")
	}
	if !m.refreshPending {
		t.Error("refreshPending should latch true after first schedule")
	}

	// Second event lands while tick is in flight — dirty stays,
	// but no redundant tick is scheduled.
	m.markViewportDirty()
	second := m.scheduleCoalescedRefresh()
	if second != nil {
		t.Errorf("second schedule while pending should collapse to nil, got %T", second)
	}

	// The tick eventually fires a coalescedRefreshMsg. Verify.
	if msg := first(); msg == nil {
		t.Fatal("first tick's Cmd should produce a Msg")
	} else if _, ok := msg.(coalescedRefreshMsg); !ok {
		t.Errorf("tick should produce coalescedRefreshMsg, got %T", msg)
	}
}

// TestCoalescedRefresh_ToolCallAndResult_AlsoCoalesce — verify the
// other high-frequency handlers (toolCallMsg, toolResultMsg) share
// the same tick as streamChunkMsg. Realistic mixed burst mirroring
// what an attach-to-long-session actually replays.
func TestCoalescedRefresh_ToolCallAndResult_AlsoCoalesce(t *testing.T) {
	m := NewModel(Options{Agent: newLiveAgentStub()})
	m.viewport.SetWidth(80)

	seq := []tea.Msg{
		streamChunkMsg{text: "reasoning\n\n", partial: false},
		toolCallMsg{id: "t-1", name: "bash", args: map[string]any{"command": "ls"}},
		toolResultMsg{id: "t-1", name: "bash", response: map[string]any{"stdout": "file"}},
		streamChunkMsg{text: "wrap up\n\n", partial: false},
	}
	for i, msg := range seq {
		out, _ := m.Update(msg)
		m = out.(Model)
		if !m.viewportDirty {
			t.Fatalf("step %d (%T): viewportDirty must be true", i, msg)
		}
		if !m.refreshPending {
			t.Fatalf("step %d (%T): refreshPending must stay latched — one tick services the whole burst", i, msg)
		}
	}
}

// TestMarkViewportDirty_JustFlipsFlag — cheap idempotent flag flip;
// no Cmd, no scheduling side effect. Isolates the primitive from
// scheduleCoalescedRefresh so callers that want state-mutation-
// without-scheduling (e.g. apply* helpers running inside a handler
// that composes its own Cmd via liveStreamRenderCmd) have a clean
// building block.
func TestMarkViewportDirty_JustFlipsFlag(t *testing.T) {
	m := NewModel(Options{})
	if m.viewportDirty {
		t.Fatal("setup: viewportDirty should start false")
	}
	m.markViewportDirty()
	if !m.viewportDirty {
		t.Error("markViewportDirty should flip viewportDirty true")
	}
	if m.refreshPending {
		t.Error("markViewportDirty alone must NOT set refreshPending — that's scheduleCoalescedRefresh's job")
	}
	// Idempotent.
	m.markViewportDirty()
	if !m.viewportDirty {
		t.Error("second markViewportDirty should keep viewportDirty true")
	}
}

// TestWindowSizeMsg_NoOpWhenUnchanged — the adjacent perf win: a
// second WindowSizeMsg with identical dimensions must NOT re-run
// rerenderHistoryMarkdown (which walks the whole history and bumps
// Version on every assistant row, invalidating listCache entries).
// Bubble Tea can emit the initial + terminal-negotiated sizes back-
// to-back; the guard keeps that from wasting an O(N) Glamour pass.
func TestWindowSizeMsg_NoOpWhenUnchanged(t *testing.T) {
	m := NewModel(Options{})
	// Prime the width so a later identical msg is a true no-op.
	first := tea.WindowSizeMsg{Width: 100, Height: 40}
	out, _ := m.Update(first)
	m = out.(Model)

	// Seed an assistant message with a Rendered value and pin its
	// Version — rerenderHistoryMarkdown bumps Version on every
	// assistant row, so a Version bump is the fingerprint we watch
	// for.
	m.history.Append(Message{Role: RoleAssistant, Text: "hi", Rendered: "hi"})
	snap := m.history.Snapshot()
	if len(snap) == 0 {
		t.Fatal("setup: expected seeded assistant message")
	}
	versionBefore := snap[len(snap)-1].Version

	// Second WindowSizeMsg — identical dimensions. Guard must
	// short-circuit before rerenderHistoryMarkdown runs.
	out, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = out.(Model)

	snap = m.history.Snapshot()
	versionAfter := snap[len(snap)-1].Version
	if versionAfter != versionBefore {
		t.Errorf("WindowSizeMsg with unchanged dimensions must NOT bump Version: before=%d after=%d", versionBefore, versionAfter)
	}
}

// TestWindowSizeMsg_HeightOnlyChange_SkipsMarkdownRerender — height
// changes don't affect Glamour wrapping (which is width-pinned), so
// the O(N) rerenderHistoryMarkdown pass is wasted work. Only width
// changes should bump assistant Version.
func TestWindowSizeMsg_HeightOnlyChange_SkipsMarkdownRerender(t *testing.T) {
	m := NewModel(Options{})
	// Prime.
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = out.(Model)

	m.history.Append(Message{Role: RoleAssistant, Text: "hi", Rendered: "hi"})
	versionBefore := m.history.Snapshot()[0].Version

	// Height-only change.
	out, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 50})
	m = out.(Model)

	versionAfter := m.history.Snapshot()[0].Version
	if versionAfter != versionBefore {
		t.Errorf("height-only WindowSizeMsg must NOT bump assistant Version: before=%d after=%d", versionBefore, versionAfter)
	}
}
