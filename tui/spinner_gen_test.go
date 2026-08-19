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

// Generation guard for the spinner tick chain (issue #112). Every
// test here drives Update with a hand-built spinnerTickMsg rather
// than executing the tea.Tick Cmd — the same technique the resize
// debounce tests use — so nothing waits out a spinner tick.

package tui

import (
	"testing"
	"time"
)

// streamingModel returns a model that has just started a turn, plus
// the generation stamp the tick chain for that turn carries.
func streamingModel(t *testing.T) (model, uint64) {
	t.Helper()
	m := newModel(Options{Agent: stubAgent{}})
	m.viewport.SetWidth(80)
	m = m.submitTurn("first prompt")
	if m.state != stateStreaming {
		t.Fatalf("setup: state = %v, want stateStreaming", m.state)
	}
	return m, m.spinnerGen
}

// TestSubmitTurn_BumpsSpinnerGeneration pins the boundary the guard
// is built on: every turn owns a distinct spinner generation, so a
// second turn inside one session can be told apart from the first.
// sessionGen deliberately cannot do this — it only moves on a
// session switch (applySwitchTarget).
func TestSubmitTurn_BumpsSpinnerGeneration(t *testing.T) {
	m, first := streamingModel(t)
	before := m.sessionGen

	m = m.submitTurn("second prompt")
	if m.spinnerGen == first {
		t.Errorf("spinnerGen unchanged across turns (%d) — a second turn would share the first's tick chain", first)
	}
	if m.sessionGen != before {
		t.Errorf("sessionGen = %d, want %d — a turn is not a session switch", m.sessionGen, before)
	}
}

// TestSpinnerTick_StaleChainDroppedAndDoesNotReArm is the core
// regression. Before the guard, a tick armed by turn N survived into
// turn N+1: m.state is back to stateStreaming by the time it lands,
// so the level gate passes it and it re-arms. Two chains, forever.
func TestSpinnerTick_StaleChainDroppedAndDoesNotReArm(t *testing.T) {
	m, stale := streamingModel(t)

	// Second turn begins while turn one's tick is still in flight.
	m = m.submitTurn("second prompt")
	if m.state != stateStreaming {
		t.Fatalf("setup: state = %v, want stateStreaming", m.state)
	}
	idxBefore := m.spinnerFrame

	next, cmd := m.Update(spinnerTickMsg{gen: stale})
	nm := next.(model)

	if cmd != nil {
		t.Error("stale tick re-armed the spinner — the superseded chain is still alive")
	}
	if nm.spinnerFrame != idxBefore {
		t.Errorf("stale tick rotated the verb pool: spinnerFrame %d -> %d", idxBefore, nm.spinnerFrame)
	}
}

// TestSpinnerTick_CurrentChainRotatesAndReArms is the liveness half:
// the guard must not stop the chain that is supposed to be running.
func TestSpinnerTick_CurrentChainRotatesAndReArms(t *testing.T) {
	m, gen := streamingModel(t)
	idxBefore := m.spinnerFrame

	next, cmd := m.Update(spinnerTickMsg{gen: gen})
	nm := next.(model)

	if cmd == nil {
		t.Fatal("current-generation tick did not re-arm the spinner")
	}
	if nm.spinnerFrame != idxBefore+1 {
		t.Errorf("spinnerFrame = %d, want %d", nm.spinnerFrame, idxBefore+1)
	}
	if nm.spinnerGen != gen {
		t.Errorf("spinnerGen = %d, want %d — re-arming continues the live chain, it does not start a new one", nm.spinnerGen, gen)
	}
}

// TestSpinnerTick_TwoChainsAdvanceVerbPoolOnce reproduces the
// user-visible symptom directly. Two chains armed, one cadence
// elapses, so both deliver a tick: the verb pool must advance once,
// not twice.
func TestSpinnerTick_TwoChainsAdvanceVerbPoolOnce(t *testing.T) {
	m, stale := streamingModel(t)
	m = m.submitTurn("second prompt")
	fresh := m.spinnerGen
	idxBefore := m.spinnerFrame

	next, _ := m.Update(spinnerTickMsg{gen: stale})
	next, _ = next.(model).Update(spinnerTickMsg{gen: fresh})
	nm := next.(model)

	if got := nm.spinnerFrame - idxBefore; got != 1 {
		t.Errorf("verb pool advanced %dx in one cadence, want 1x", got)
	}
}

// TestSpinnerTick_QueueDrainRetiresPreviousChain walks the suspected
// real-world path from the issue end to end through Update: a turn
// finishes with a prompt already queued, maybeDrainQueue submits it
// as a fresh turn, and the finishing turn's tick lands afterwards.
func TestSpinnerTick_QueueDrainRetiresPreviousChain(t *testing.T) {
	m, stale := streamingModel(t)
	m.queue = []QueueEntry{{Text: "queued prompt", State: QueueQueued, Created: time.Now()}}

	next, _ := m.Update(turnDoneMsg{gen: m.sessionGen, elapsed: time.Second})
	drained := next.(model)
	if drained.state != stateStreaming {
		t.Fatalf("setup: queue drain did not start a turn (state = %v)", drained.state)
	}
	if drained.spinnerGen == stale {
		t.Fatal("queue drain re-used the finished turn's spinner generation")
	}
	idxBefore := drained.spinnerFrame

	// The finished turn's tick arrives after the drain. m.state is
	// stateStreaming again, so the pre-existing level gate lets it
	// through — only the stamp can stop it.
	after, cmd := drained.Update(spinnerTickMsg{gen: stale})
	if cmd != nil {
		t.Error("tick from the drained turn re-armed — the drain produced a second live chain")
	}
	if got := after.(model).spinnerFrame; got != idxBefore {
		t.Errorf("spinnerFrame = %d, want %d — stale chain rotated the verb pool", got, idxBefore)
	}
}

// TestSpinnerTick_AutoContinueRetiresPreviousChain covers the other
// turn-chaining path (issue #9's AutoContinueFromInbox), which arms
// its own spinner alongside maybeDrainQueue's.
func TestSpinnerTick_AutoContinueRetiresPreviousChain(t *testing.T) {
	agent := &inboxAgent{mu: []string{"background note"}}
	m := newModel(Options{
		Agent:                agent,
		MidTurnInjectionMode: AutoContinueFromInbox,
	})
	m.viewport.SetWidth(80)
	m = m.submitTurn("first prompt")
	stale := m.spinnerGen

	out, cmd, ok := m.maybeAutoContinue()
	if !ok {
		t.Fatal("setup: maybeAutoContinue declined to submit")
	}
	if cmd == nil {
		t.Fatal("auto-continue returned no Cmd")
	}
	if out.spinnerGen == stale {
		t.Error("auto-continue re-used the previous turn's spinner generation")
	}

	after, tickCmd := out.Update(spinnerTickMsg{gen: stale})
	if tickCmd != nil {
		t.Error("tick from the previous turn re-armed after auto-continue")
	}
	if after.(model).spinnerFrame != out.spinnerFrame {
		t.Error("stale tick rotated the verb pool after auto-continue")
	}
}

// TestSpinnerTick_LiveAgentStretchStillAnimates guards the path that
// never touches submitTurn: LiveAgent hosts flip spinnerActive from
// applyStreamChunk. That stretch has to get its own generation and
// keep ticking — the new guard must not starve it.
func TestSpinnerTick_LiveAgentStretchStillAnimates(t *testing.T) {
	m := newModel(Options{Agent: newLiveAgentStub()})
	m.viewport.SetWidth(80)

	next, cmd := m.Update(streamChunkMsg{gen: m.sessionGen, text: "tok", partial: true})
	live := next.(model)
	if !live.spinnerActive {
		t.Fatal("setup: expected spinnerActive after a partial chunk in liveMode")
	}
	if cmd == nil {
		t.Fatal("liveMode partial chunk did not arm a spinner")
	}
	if live.spinnerGen == 0 {
		t.Error("liveMode stretch did not take a spinner generation")
	}
	idxBefore := live.spinnerFrame

	after, tickCmd := live.Update(spinnerTickMsg{gen: live.spinnerGen})
	am := after.(model)
	if tickCmd == nil {
		t.Fatal("liveMode spinner did not re-arm — the guard starved the LiveAgent path")
	}
	if am.spinnerFrame != idxBefore+1 {
		t.Errorf("spinnerFrame = %d, want %d", am.spinnerFrame, idxBefore+1)
	}

	// A second stretch (commit, then a new partial) must supersede
	// the first rather than run beside it.
	stale := am.spinnerGen
	am.applyStreamChunk(streamChunkMsg{text: "done", partial: false})
	restarted, _ := am.Update(streamChunkMsg{gen: am.sessionGen, text: "more", partial: true})
	rm := restarted.(model)
	if rm.spinnerGen == stale {
		t.Fatal("second liveMode stretch re-used the first stretch's generation")
	}
	if _, cmd := rm.Update(spinnerTickMsg{gen: stale}); cmd != nil {
		t.Error("tick from the previous liveMode stretch re-armed")
	}
}
