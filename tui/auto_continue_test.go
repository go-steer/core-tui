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

// inboxAgent stubs Agent + InjectableAgent + InboxDrainer.
// `inject` lands messages on the inbox; `DrainInbox` returns +
// clears them. Track calls to DrainInbox so tests can verify it
// was (or wasn't) invoked.
type inboxAgent struct {
	mu         []string
	drainCount int
	injectErr  error
}

func (a *inboxAgent) Run(_ context.Context, _ string) iter.Seq2[Event, error] {
	return func(_ func(Event, error) bool) {}
}
func (a *inboxAgent) Inject(s string) error {
	if a.injectErr != nil {
		return a.injectErr
	}
	a.mu = append(a.mu, s)
	return nil
}
func (a *inboxAgent) DrainInbox() []string {
	a.drainCount++
	out := a.mu
	a.mu = nil
	return out
}
func (a *inboxAgent) PendingInboxCount() int { return len(a.mu) }

func TestMaybeAutoContinue_NoOpForWrongMode(t *testing.T) {
	agent := &inboxAgent{mu: []string{"hello"}}
	m := NewModel(Options{
		Agent:                agent,
		MidTurnInjectionMode: QueueForNext, // not AutoContinueFromInbox
	})
	_, _, ok := m.maybeAutoContinue()
	if ok {
		t.Errorf("expected no-op for QueueForNext mode")
	}
	if agent.drainCount != 0 {
		t.Errorf("DrainInbox should not have been called, got %d", agent.drainCount)
	}
}

func TestMaybeAutoContinue_NoOpWhenAgentDoesNotSatisfyInboxDrainer(t *testing.T) {
	// Plain Agent (no InboxDrainer) → AutoContinueFromInbox falls
	// through cleanly.
	agent := &slashAgent{} // satisfies Agent only
	m := NewModel(Options{
		Agent:                agent,
		MidTurnInjectionMode: AutoContinueFromInbox,
	})
	_, _, ok := m.maybeAutoContinue()
	if ok {
		t.Errorf("expected no-op when agent doesn't satisfy InboxDrainer")
	}
}

func TestMaybeAutoContinue_NoOpWhenInboxEmpty(t *testing.T) {
	agent := &inboxAgent{} // empty
	m := NewModel(Options{
		Agent:                agent,
		MidTurnInjectionMode: AutoContinueFromInbox,
	})
	_, _, ok := m.maybeAutoContinue()
	if ok {
		t.Errorf("expected no-op when inbox is empty")
	}
	if agent.drainCount != 1 {
		t.Errorf("expected DrainInbox called once, got %d", agent.drainCount)
	}
}

func TestMaybeAutoContinue_SubmitsSyntheticTurn(t *testing.T) {
	agent := &inboxAgent{mu: []string{"first note", "second note"}}
	m := NewModel(Options{
		Agent:                agent,
		MidTurnInjectionMode: AutoContinueFromInbox,
	})
	m.viewport.SetWidth(80)
	next, _, ok := m.maybeAutoContinue()
	if !ok {
		t.Fatal("expected auto-continue to fire")
	}
	if agent.drainCount != 1 {
		t.Errorf("expected DrainInbox called once, got %d", agent.drainCount)
	}
	// The history should now carry the synthetic user-row marked
	// AutoContinue=true.
	entries := next.history.Snapshot()
	if len(entries) == 0 {
		t.Fatal("expected synthetic user-row appended")
	}
	last := entries[len(entries)-1]
	if !last.AutoContinue {
		t.Errorf("expected AutoContinue=true on synthesized row")
	}
	if !strings.Contains(last.Text, "first note") || !strings.Contains(last.Text, "second note") {
		t.Errorf("expected drained notes in formatted prompt, got:\n%q", last.Text)
	}
	if !strings.Contains(last.Text, "Continue.") {
		t.Errorf("expected 'Continue.' instruction in default formatter, got:\n%q", last.Text)
	}
	if next.consecutiveAutoContinues != 1 {
		t.Errorf("expected consecutiveAutoContinues=1, got %d", next.consecutiveAutoContinues)
	}
}

func TestMaybeAutoContinue_HonorsCustomFormatter(t *testing.T) {
	agent := &inboxAgent{mu: []string{"x", "y"}}
	m := NewModel(Options{
		Agent:                agent,
		MidTurnInjectionMode: AutoContinueFromInbox,
		AutoContinueFormatter: func(msgs []string) string {
			return "CUSTOM " + strings.Join(msgs, "|")
		},
	})
	m.viewport.SetWidth(80)
	next, _, ok := m.maybeAutoContinue()
	if !ok {
		t.Fatal("expected auto-continue to fire")
	}
	last := next.history.Snapshot()[next.history.Len()-1]
	if last.Text != "CUSTOM x|y" {
		t.Errorf("custom formatter not applied, got: %q", last.Text)
	}
}

func TestMaybeAutoContinue_SoftCapStopsLoop(t *testing.T) {
	agent := &inboxAgent{mu: []string{"hi"}}
	m := NewModel(Options{
		Agent:                agent,
		MidTurnInjectionMode: AutoContinueFromInbox,
		AutoContinueCap:      3,
	})
	m.viewport.SetWidth(80)
	m.consecutiveAutoContinues = 3 // already at cap

	_, _, ok := m.maybeAutoContinue()
	if ok {
		t.Errorf("expected soft cap to halt the loop")
	}
	if agent.drainCount != 0 {
		t.Errorf("DrainInbox must not be called once cap is hit, got %d", agent.drainCount)
	}
}

func TestMaybeAutoContinue_NegativeCapDisablesIt(t *testing.T) {
	agent := &inboxAgent{mu: []string{"hi"}}
	m := NewModel(Options{
		Agent:                agent,
		MidTurnInjectionMode: AutoContinueFromInbox,
		AutoContinueCap:      -1, // disabled
	})
	m.viewport.SetWidth(80)
	m.consecutiveAutoContinues = 1000 // would otherwise be way past
	_, _, ok := m.maybeAutoContinue()
	if !ok {
		t.Errorf("negative cap should disable the check entirely")
	}
}

func TestMaybeAutoContinue_FiltersBlankMessages(t *testing.T) {
	agent := &inboxAgent{mu: []string{"real", "  ", "", "also real"}}
	m := NewModel(Options{
		Agent:                agent,
		MidTurnInjectionMode: AutoContinueFromInbox,
	})
	m.viewport.SetWidth(80)
	next, _, ok := m.maybeAutoContinue()
	if !ok {
		t.Fatal("expected auto-continue to fire")
	}
	last := next.history.Snapshot()[next.history.Len()-1]
	if strings.Contains(last.Text, "- \n") || strings.Contains(last.Text, "-   \n") {
		t.Errorf("expected blank entries filtered out, got:\n%q", last.Text)
	}
}

func TestMaybeAutoContinue_MarksMatchingQueueEntriesDone(t *testing.T) {
	agent := &inboxAgent{mu: []string{"queued-text"}}
	m := NewModel(Options{
		Agent:                agent,
		MidTurnInjectionMode: AutoContinueFromInbox,
	})
	m.viewport.SetWidth(80)
	m.queue = []QueueEntry{
		{Text: "queued-text", State: QueueQueued, Created: time.Now()},
		{Text: "unrelated", State: QueueQueued, Created: time.Now()},
	}
	next, _, ok := m.maybeAutoContinue()
	if !ok {
		t.Fatal("expected auto-continue to fire")
	}
	if next.queue[0].State != QueueDone {
		t.Errorf("expected matching queue entry → Done, got %v", next.queue[0].State)
	}
	if next.queue[1].State != QueueQueued {
		t.Errorf("expected unrelated queue entry untouched (Queued), got %v", next.queue[1].State)
	}
}

func TestDefaultAutoContinueFormatter_Shape(t *testing.T) {
	got := defaultAutoContinueFormatter([]string{"alpha", "beta"})
	if !strings.HasPrefix(got, "[Operator notes added during the previous task]") {
		t.Errorf("expected operator-notes prefix, got:\n%q", got)
	}
	if !strings.Contains(got, "- alpha\n") || !strings.Contains(got, "- beta\n") {
		t.Errorf("expected bulleted entries, got:\n%q", got)
	}
	if !strings.HasSuffix(got, "Continue.") {
		t.Errorf("expected 'Continue.' suffix, got:\n%q", got)
	}
}

func TestCompactNonEmpty(t *testing.T) {
	got := compactNonEmpty([]string{"a", "", " ", "b", "\t\n"})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("expected [a b], got %v", got)
	}
}

func TestEnqueueDuringStream_AutoContinueMode_InjectsAndQueues(t *testing.T) {
	agent := &inboxAgent{}
	m := NewModel(Options{
		Agent:                agent,
		MidTurnInjectionMode: AutoContinueFromInbox,
	})
	m.enqueueDuringStream("hello")
	if len(m.queue) != 1 {
		t.Fatalf("expected 1 queue entry, got %d", len(m.queue))
	}
	if m.queue[0].State != QueueQueued {
		t.Errorf("expected QueueQueued (stays pending), got %v", m.queue[0].State)
	}
	if !m.queue[0].Injected {
		t.Errorf("expected Injected=true marker")
	}
	if len(agent.mu) != 1 || agent.mu[0] != "hello" {
		t.Errorf("expected Inject called with 'hello', got %v", agent.mu)
	}
}
