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
	"context"
	"iter"
	"strings"
	"testing"
	"time"
)

// wakingAgent is a stub Agent + WakeRequester for wake tests. The
// wake channel is exposed so the test can fire signals into it.
type wakingAgent struct {
	wakeCh chan struct{}
}

func newWakingAgent() *wakingAgent {
	return &wakingAgent{wakeCh: make(chan struct{}, 4)}
}

func (a *wakingAgent) Run(_ context.Context, _ string) iter.Seq2[Event, error] {
	return func(_ func(Event, error) bool) {}
}
func (a *wakingAgent) WakeRequested() <-chan struct{} { return a.wakeCh }

// TestWakeListener_FiresWakeMsg pins R-WAKE-1: a value sent into the
// agent's wake channel surfaces as a wakeMsg through the Cmd the
// listener returns.
func TestWakeListener_FiresWakeMsg(t *testing.T) {
	agent := newWakingAgent()
	m := newModel(Options{Agent: agent})

	cmd := m.wakeListener()
	if cmd == nil {
		t.Fatal("wakeListener returned nil for an agent that implements WakeRequester")
	}

	// Fire a wake; the Cmd should receive it and return wakeMsg.
	agent.wakeCh <- struct{}{}
	got := cmd()
	if _, ok := got.(wakeMsg); !ok {
		t.Errorf("got %T, want wakeMsg", got)
	}
}

// TestWakeListener_NilForAgentWithoutCapability pins that a host
// whose agent doesn't satisfy WakeRequester gets nil from the
// listener — Init still launches cleanly, no goroutine leak.
func TestWakeListener_NilForAgentWithoutCapability(t *testing.T) {
	m := newModel(Options{Agent: stubAgent{}})
	if cmd := m.wakeListener(); cmd != nil {
		t.Errorf("wakeListener should be nil for an agent without WakeRequester")
	}
}

// TestWakeListener_NilChannelHandledGracefully pins that an agent
// that returns nil from WakeRequested() is treated as "no signals"
// — no panic, no leaked goroutine.
func TestWakeListener_NilChannelHandledGracefully(t *testing.T) {
	m := newModel(Options{Agent: nilWakeAgent{}})
	if cmd := m.wakeListener(); cmd != nil {
		t.Errorf("wakeListener should be nil when WakeRequested() returns nil")
	}
}

// TestUpdate_WakeMsgRaisesToast pins that the Update handler for
// wakeMsg sets m.toast and stamps toastSetAt so the render path can
// surface the banner.
func TestUpdate_WakeMsgRaisesToast(t *testing.T) {
	agent := newWakingAgent()
	m := newModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	before := time.Now()
	out, _ := m.Update(wakeMsg{})
	got := out.(model)

	if got.toast == "" {
		t.Errorf("toast = empty, want non-empty after wakeMsg")
	}
	if got.toastSetAt.Before(before) {
		t.Errorf("toastSetAt = %v, want >= %v", got.toastSetAt, before)
	}
}

// TestUpdate_WakeMsg_SuppressedWhenQueuePending pins issue #7:
// when the queue already shows a pending entry (operator typed
// during streaming), the wakeMsg handler must NOT raise the
// "background subagent" toast / system message — the queue panel
// is already the right surface for that signal.
func TestUpdate_WakeMsg_SuppressedWhenQueuePending(t *testing.T) {
	agent := newWakingAgent()
	m := newModel(Options{Agent: agent})
	m.viewport.SetWidth(80)
	m.queue = []QueueEntry{
		{Text: "queued by operator", State: QueueQueued, Created: time.Now()},
	}

	out, _ := m.Update(wakeMsg{})
	got := out.(model)

	if got.toast != "" {
		t.Errorf("expected empty toast when queue has pending entry, got %q", got.toast)
	}
	// No system message should have been appended — the queue panel
	// is the operator's confirmation surface.
	if got.history.Len() != 0 {
		t.Errorf("expected zero history entries when queue has pending entry, got %d", got.history.Len())
	}
}

// TestUpdate_WakeMsg_FiresWhenQueueEmpty pins the opposite: with
// no pending queue entry, the wakeMsg handler still raises the
// toast + system message (subagent / external-alert path).
func TestUpdate_WakeMsg_FiresWhenQueueEmpty(t *testing.T) {
	agent := newWakingAgent()
	m := newModel(Options{Agent: agent})
	m.viewport.SetWidth(80)
	// queue is empty by default

	out, _ := m.Update(wakeMsg{})
	got := out.(model)

	if got.toast == "" {
		t.Errorf("expected non-empty toast when queue is empty (subagent path)")
	}
	if got.history.Len() == 0 {
		t.Errorf("expected system message appended when queue is empty")
	}
}

// TestUpdate_WakeMsg_RowAssertsNothingAboutTheInbox pins R-WAKE-2.
// The wake signal is a payload-free `<-chan struct{}`, so the TUI
// cannot know whether an alert is sitting in the inbox or whether
// somebody just asked the loop to look now with nothing behind it.
// The row shipped through v0.22.0 stated the first as fact ("an
// external alert ... is waiting in the inbox"), which is wrong for
// every wake of the second kind — see go-steer/core-agent#802. This
// test fails against that copy.
func TestUpdate_WakeMsg_RowAssertsNothingAboutTheInbox(t *testing.T) {
	agent := newWakingAgent()
	m := newModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	out, _ := m.Update(wakeMsg{})
	got := out.(model)

	snap := got.history.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("history = %d rows, want 1", len(snap))
	}
	row := snap[0]
	if row.Role != RoleSystem {
		t.Errorf("role = %v, want RoleSystem", row.Role)
	}

	// Unconditional claims about what woke the agent. Each of these
	// is a statement the signal cannot support.
	for _, banned := range []string{
		"is waiting in the inbox",
		"alert is waiting",
	} {
		if strings.Contains(row.Text, banned) {
			t.Errorf("wake row asserts %q as fact; the signal carries no payload that could justify it.\nrow: %s", banned, row.Text)
		}
	}

	// The toast is the same promise in one line.
	if strings.Contains(got.toast, "alert in inbox") {
		t.Errorf("wake toast asserts an inbox alert as fact: %q", got.toast)
	}

	// Positive half: the row must still say a wake happened and must
	// say the signal is contentless, or it has stopped being useful.
	for _, want := range []string{"Wake signal received", "carries no detail"} {
		if !strings.Contains(row.Text, want) {
			t.Errorf("wake row missing %q.\nrow: %s", want, row.Text)
		}
	}
}

// TestUpdate_WakeMsg_SuppressedWithInFlightEntry covers the
// QueueInFlight branch of hasPendingQueueEntry (the entry has been
// drained from the queue and is the running turn).
func TestUpdate_WakeMsg_SuppressedWithInFlightEntry(t *testing.T) {
	agent := newWakingAgent()
	m := newModel(Options{Agent: agent})
	m.viewport.SetWidth(80)
	m.queue = []QueueEntry{
		{Text: "in flight", State: QueueInFlight, Created: time.Now()},
	}

	out, _ := m.Update(wakeMsg{})
	got := out.(model)

	if got.toast != "" {
		t.Errorf("expected suppression for QueueInFlight, got toast %q", got.toast)
	}
}

// TestUpdate_WakeMsg_FiresWhenAllQueueEntriesTerminal pins that
// only non-terminal entries suppress — a queue full of Done /
// Failed entries should NOT block the wake signal (those are
// fading-out remnants, not active work).
func TestUpdate_WakeMsg_FiresWhenAllQueueEntriesTerminal(t *testing.T) {
	agent := newWakingAgent()
	m := newModel(Options{Agent: agent})
	m.viewport.SetWidth(80)
	m.queue = []QueueEntry{
		{Text: "fading", State: QueueDone, Created: time.Now()},
		{Text: "failed", State: QueueFailed, Created: time.Now()},
	}

	out, _ := m.Update(wakeMsg{})
	got := out.(model)

	if got.toast == "" {
		t.Errorf("expected toast when only terminal entries present (no active queue work)")
	}
}

// TestCullTTL_LongerThan2s pins issue #8: the cull TTL must be
// long enough for the operator to actually see a Done entry. The
// fix bumps from 2s to something comfortably above reading speed.
func TestCullTTL_LongerThan2s(t *testing.T) {
	if cullTTL <= 2*time.Second {
		t.Errorf("cullTTL = %v, expected > 2s so fast-tier model turns leave Done entries visible", cullTTL)
	}
}

// TestRenderToast_RespectsTTL pins that a toast past its TTL renders
// as empty (the cull check in renderToast is the secondary defense
// behind the toastClearMsg timer).
func TestRenderToast_RespectsTTL(t *testing.T) {
	m := newModel(Options{})
	m.viewport.SetWidth(80)
	m.toast = "stale"
	m.toastSetAt = time.Now().Add(-2 * toastTTL)
	if got := m.renderToast(80); got != "" {
		t.Errorf("renderToast for expired toast = %q, want empty", got)
	}
}

// TestRenderToast_StickyWhenSlashInFlight pins the fix: when an
// async slash is in flight, the render-time TTL backstop must NOT
// hide the toast even if toastSetAt is stale. Mirrors what
// toastClearMsg already does — without this bypass, a slash that
// runs longer than toastTTL (4s) loses the "/<name> running…"
// indicator at the secondary check despite the slash still
// pending.
func TestRenderToast_StickyWhenSlashInFlight(t *testing.T) {
	m := newModel(Options{})
	m.viewport.SetWidth(80)
	m.toast = "▸ /compact running…"
	m.toastSetAt = time.Now().Add(-2 * toastTTL) // way past TTL
	m.inFlightSlash = &slashFlight{name: "compact", startedAt: time.Now().Add(-2 * toastTTL)}

	if got := m.renderToast(80); got == "" {
		t.Errorf("renderToast should keep sticky toast visible while slash in flight, got empty")
	}
}

// TestRenderToast_NoLongerStickyAfterSlashClears pins that the
// bypass is properly gated: once inFlightSlash is nil (e.g. result
// arrived) the TTL backstop takes over and the toast hides as
// expected.
func TestRenderToast_NoLongerStickyAfterSlashClears(t *testing.T) {
	m := newModel(Options{})
	m.viewport.SetWidth(80)
	m.toast = "stale"
	m.toastSetAt = time.Now().Add(-2 * toastTTL)
	m.inFlightSlash = nil // slash result landed

	if got := m.renderToast(80); got != "" {
		t.Errorf("renderToast without inFlightSlash should respect TTL, got %q", got)
	}
}

// nilWakeAgent satisfies WakeRequester but returns nil — exercises
// the defensive nil-channel branch in wakeListener.
type nilWakeAgent struct{}

func (nilWakeAgent) Run(_ context.Context, _ string) iter.Seq2[Event, error] {
	return func(_ func(Event, error) bool) {}
}
func (nilWakeAgent) WakeRequested() <-chan struct{} { return nil }
