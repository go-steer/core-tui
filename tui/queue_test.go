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
	"testing"
	"time"
)

// TestQueueState_StringLabels pins the wire format of state names —
// they show up in tests + diagnostics, so a rename here would be a
// breaking change for anyone grepping logs.
func TestQueueState_StringLabels(t *testing.T) {
	for _, tc := range []struct {
		s    QueueState
		want string
	}{
		{QueueQueued, "queued"},
		{QueueInFlight, "in-flight"},
		{QueueDone, "done"},
		{QueueFailed, "failed"},
	} {
		if got := tc.s.String(); got != tc.want {
			t.Errorf("State(%d).String() = %q, want %q", tc.s, got, tc.want)
		}
	}
}

// TestMarkInFlightTerminal_FlipsToDone pins that finalizeTurn's
// success path flips the InFlight entry to Done (R-CHAT-10).
func TestMarkInFlightTerminal_FlipsToDone(t *testing.T) {
	m := NewModel(Options{})
	m.queue = []QueueEntry{
		{Text: "x", State: QueueInFlight, Created: time.Now()},
	}
	m.markInFlightTerminal(true, "")
	if m.queue[0].State != QueueDone {
		t.Errorf("state = %v, want Done", m.queue[0].State)
	}
	if m.queue[0].Err != "" {
		t.Errorf("Err = %q, want empty on success", m.queue[0].Err)
	}
}

// TestMarkInFlightTerminal_FlipsToFailedWithReason pins the failure
// path: state goes Failed + Err carries the reason for rendering.
func TestMarkInFlightTerminal_FlipsToFailedWithReason(t *testing.T) {
	m := NewModel(Options{})
	m.queue = []QueueEntry{
		{Text: "x", State: QueueInFlight, Created: time.Now()},
	}
	m.markInFlightTerminal(false, "rate limit exceeded")
	if m.queue[0].State != QueueFailed {
		t.Errorf("state = %v, want Failed", m.queue[0].State)
	}
	if m.queue[0].Err != "rate limit exceeded" {
		t.Errorf("Err = %q, want %q", m.queue[0].Err, "rate limit exceeded")
	}
}

// TestCullQueue_DropsExpiredTerminal pins the cull TTL — terminal-
// state entries older than cullTTL get dropped on the next render;
// fresh terminal entries stay so the operator sees them.
func TestCullQueue_DropsExpiredTerminal(t *testing.T) {
	m := NewModel(Options{})
	old := time.Now().Add(-3 * cullTTL)
	fresh := time.Now()
	m.queue = []QueueEntry{
		{Text: "ancient-done", State: QueueDone, Created: old},
		{Text: "fresh-done", State: QueueDone, Created: fresh},
		{Text: "still-queued", State: QueueQueued, Created: old}, // age irrelevant for non-terminal
		{Text: "ancient-failed", State: QueueFailed, Created: old},
	}
	m.cullQueue()
	want := []string{"fresh-done", "still-queued"}
	if len(m.queue) != len(want) {
		t.Fatalf("queue len after cull = %d, want %d (kept: %v)",
			len(m.queue), len(want), entryTexts(m.queue))
	}
	for i, w := range want {
		if m.queue[i].Text != w {
			t.Errorf("kept[%d] = %q, want %q", i, m.queue[i].Text, w)
		}
	}
}

// TestCullQueue_DoesNotCullInFlight pins that an InFlight entry is
// NEVER culled even if its Created stamp is ancient — the entry
// stays in the panel for as long as the turn runs.
func TestCullQueue_DoesNotCullInFlight(t *testing.T) {
	m := NewModel(Options{})
	m.queue = []QueueEntry{
		{Text: "long-running", State: QueueInFlight, Created: time.Now().Add(-10 * cullTTL)},
	}
	m.cullQueue()
	if len(m.queue) != 1 || m.queue[0].State != QueueInFlight {
		t.Errorf("InFlight entry was culled or mutated: %+v", m.queue)
	}
}

// injectingAgent is a stub for the InjectIntoCurrent tests. tracks
// each Inject call so the test can assert delivery; nextErr lets a
// test simulate a host-side failure.
type injectingAgent struct {
	calls   []string
	nextErr error
}

func (a *injectingAgent) Run(_ context.Context, _ string) iter.Seq2[Event, error] {
	return func(_ func(Event, error) bool) {}
}
func (a *injectingAgent) Inject(msg string) error {
	a.calls = append(a.calls, msg)
	return a.nextErr
}

// TestEnqueueDuringStream_InjectsWhenModeSet pins R-CHAT-11: with
// MidTurnInjectionMode == InjectIntoCurrent and an agent that
// satisfies InjectableAgent, the entry routes through Inject and
// lands as Done (injected), NOT as Queued.
func TestEnqueueDuringStream_InjectsWhenModeSet(t *testing.T) {
	agent := &injectingAgent{}
	m := NewModel(Options{
		Agent:                agent,
		MidTurnInjectionMode: InjectIntoCurrent,
	})
	m.enqueueDuringStream("inject me")

	if len(agent.calls) != 1 || agent.calls[0] != "inject me" {
		t.Errorf("agent calls = %v, want [inject me]", agent.calls)
	}
	if len(m.queue) != 1 {
		t.Fatalf("queue len = %d, want 1", len(m.queue))
	}
	got := m.queue[0]
	if got.State != QueueDone {
		t.Errorf("state = %v, want Done", got.State)
	}
	if !got.Injected {
		t.Errorf("Injected = false, want true")
	}
}

// TestEnqueueDuringStream_FallsBackWhenAgentLacksInject pins that
// the mode degrades silently when the agent doesn't satisfy
// InjectableAgent — entry lands as Queued, no error.
func TestEnqueueDuringStream_FallsBackWhenAgentLacksInject(t *testing.T) {
	m := NewModel(Options{
		Agent:                stubAgent{}, // no Inject method
		MidTurnInjectionMode: InjectIntoCurrent,
	})
	m.enqueueDuringStream("buffer me")
	if len(m.queue) != 1 || m.queue[0].State != QueueQueued {
		t.Errorf("queue = %+v, want one Queued entry", m.queue)
	}
}

// TestEnqueueDuringStream_InjectErrorMarksFailed pins that an
// Inject failure renders the entry as Failed with the error tail,
// so the operator sees what happened instead of silent loss.
func TestEnqueueDuringStream_InjectErrorMarksFailed(t *testing.T) {
	agent := &injectingAgent{nextErr: errInjectStub}
	m := NewModel(Options{
		Agent:                agent,
		MidTurnInjectionMode: InjectIntoCurrent,
	})
	m.enqueueDuringStream("oops")
	if len(m.queue) != 1 || m.queue[0].State != QueueFailed {
		t.Fatalf("queue = %+v, want one Failed entry", m.queue)
	}
	if m.queue[0].Err == "" {
		t.Errorf("Err is empty; want the Inject error string")
	}
}

// errInjectStub is a sentinel for the Inject-failure test; declaring
// it here keeps the test file self-contained.
var errInjectStub = errInjectStubVal{}

type errInjectStubVal struct{}

func (errInjectStubVal) Error() string { return "inject stub failure" }

// entryTexts pulls Text values out of entries for diagnostic prints.
func entryTexts(es []QueueEntry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Text
	}
	return out
}
