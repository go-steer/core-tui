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
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// A host that implements Pauser AND RemoteInterrupter is the shape the
// attach example has and the shape a real core-agent daemon has, and
// until issue #280 it was the one shape nothing tested. Every
// RemoteInterrupter stub omitted Pauser, and every Pauser stub omitted
// RemoteInterrupter — so each half's tests exercised the arm the other
// capability would have taken away, and the arm a host with both
// actually reached was asserted by nobody.
//
// What the operator saw: esc on a per-turn attach host reported the
// agent held while the daemon carried the turn on to completion, and
// its output surfaced under whatever prompt reopened the stream.

// perTurnHoldAgent is Agent + Pauser + RemoteInterrupter with no
// Events method: the per-turn shape, where the TUI owns the loop and
// m.state is what says a turn is running.
type perTurnHoldAgent struct {
	perTurnPausable
	interruptCalls atomic.Int32
	interruptErr   error
	// pausedFirst pins the order. Shutting the gate before stopping
	// the work is a window in which the host can finish the turn the
	// operator just cancelled.
	pausedFirst atomic.Bool
}

func (a *perTurnHoldAgent) Interrupt(_ context.Context) error {
	if len(a.pauses()) > 0 {
		a.pausedFirst.Store(true)
	}
	a.interruptCalls.Add(1)
	return a.interruptErr
}

// liveHoldAgent is the same pair in observer mode, where m.state stays
// stateIdle and the spinner is the only thing that says a turn is
// running (see turnInFlight).
type liveHoldAgent struct {
	pausableAgent
	interruptCalls atomic.Int32
}

func (a *liveHoldAgent) Interrupt(_ context.Context) error {
	a.interruptCalls.Add(1)
	return nil
}

// heldHostMidTurn returns a model wired to agent with a locally driven
// turn in flight, and reports whether the local cancel fired.
func heldHostMidTurn(agent Agent) (model, *bool) {
	m := newModel(Options{Agent: agent})
	m.width, m.height = 100, 40
	cancelled := false
	m.state = stateStreaming
	m.cancelTurn = func() { cancelled = true }
	return m, &cancelled
}

// TestEsc_StopsTheRemoteTurnAsWellAsHolding is the core of issue #280.
//
// The local cancel is not the whole gesture on a host with a gate: it
// cancels this client's subscription, and the daemon runs the turn on
// a context we do not hold. Pause is not the whole gesture either — it
// means "start no new turn" and says nothing about the one already
// running. Only the pair stops the work and keeps it stopped.
func TestEsc_StopsTheRemoteTurnAsWellAsHolding(t *testing.T) {
	agent := &perTurnHoldAgent{}
	m, cancelled := heldHostMidTurn(agent)

	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	msg := runCmd(t, cmd)

	if !*cancelled {
		t.Error("local cancelTurn did not fire; the local stack stays wedged")
	}
	if got := agent.interruptCalls.Load(); got != 1 {
		t.Errorf("Interrupt calls = %d, want 1 — the daemon's turn was left running under a banner saying it had stopped", got)
	}
	if got := agent.pauses(); len(got) != 1 {
		t.Errorf("Pause calls = %v, want exactly one hold", got)
	}
	if agent.pausedFirst.Load() {
		t.Error("the gate shut before the work stopped; that window is a turn the operator cancelled finishing anyway")
	}
	done, ok := msg.(pauseDoneMsg)
	if !ok {
		t.Fatalf("hold produced %T, want pauseDoneMsg", msg)
	}
	if done.err != nil {
		t.Errorf("happy-path hold reported %v", done.err)
	}
}

// TestEsc_AtIdleHoldsWithoutInterrupting. Esc between turns means "get
// ahead of the next one", not "kill this one" — and the difference is
// visible: PauseInfo.Interrupted is what makes the banner read
// "Interrupted —" rather than "Agent held", which is the first thing an
// operator asks. Sending a cancel with nothing in flight would have the
// host set that bit on a hold that killed nothing.
func TestEsc_AtIdleHoldsWithoutInterrupting(t *testing.T) {
	agent := &perTurnHoldAgent{}
	m := newModel(Options{Agent: agent})
	m.width, m.height = 100, 40
	if m.turnInFlight() {
		t.Fatal("setup: expected an idle model")
	}

	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	runCmd(t, cmd)

	if got := agent.interruptCalls.Load(); got != 0 {
		t.Errorf("Interrupt calls = %d at idle, want 0 — there is no turn to cancel", got)
	}
	if got := agent.pauses(); len(got) != 1 {
		t.Errorf("Pause calls = %v, want exactly one hold", got)
	}
}

// TestSlashInterrupt_StopsTheRemoteTurnAsWellAsHolding. /interrupt took
// the same either/or as esc, one arm lower down: its local-cancel arm
// returned a bare Pause. Same host, same defect, and an operator who
// types the command rather than pressing the key must not get a
// different answer.
func TestSlashInterrupt_StopsTheRemoteTurnAsWellAsHolding(t *testing.T) {
	agent := &perTurnHoldAgent{}
	m, cancelled := heldHostMidTurn(agent)

	handled, _, cmd := m.dispatchBuiltinSlash("interrupt", "")
	if !handled {
		t.Fatal("dispatchBuiltinSlash returned handled=false for /interrupt")
	}
	runCmd(t, cmd)

	if !*cancelled {
		t.Error("local cancelTurn did not fire")
	}
	if got := agent.interruptCalls.Load(); got != 1 {
		t.Errorf("Interrupt calls = %d, want 1", got)
	}
	if got := agent.pauses(); len(got) != 1 {
		t.Errorf("Pause calls = %v, want exactly one hold", got)
	}
}

// TestSlashInterrupt_ObserverModeStopsAndHolds covers the other arm:
// no local turn context at all, because the daemon started the turn.
// This is where the old comment claimed Pauser was "the richer remote
// path" and skipped the cancel outright.
func TestSlashInterrupt_ObserverModeStopsAndHolds(t *testing.T) {
	agent := &liveHoldAgent{}
	m := newModel(Options{Agent: agent})
	m.width, m.height = 100, 40
	if !m.liveMode {
		t.Fatal("setup: expected liveMode for a LiveAgent host")
	}
	if !m.beginLiveStretch() {
		t.Fatal("setup: expected to open a stretch on a fresh model")
	}

	handled, _, cmd := m.dispatchBuiltinSlash("interrupt", "")
	if !handled {
		t.Fatal("dispatchBuiltinSlash returned handled=false for /interrupt")
	}
	runCmd(t, cmd)

	if got := agent.interruptCalls.Load(); got != 1 {
		t.Errorf("Interrupt calls = %d, want 1", got)
	}
	if got := agent.pauses(); len(got) != 1 {
		t.Errorf("Pause calls = %v, want exactly one hold", got)
	}
}

// TestHold_InterruptFailureStillShutsTheGate. A failed cancel is when
// the gate matters most — the turn that would not die is still running,
// and an open gate lets the scheduler stack another one on top of it.
// The operator has to hear about it either way: they are about to
// assume an agent stopped when it did not.
func TestHold_InterruptFailureStillShutsTheGate(t *testing.T) {
	agent := &perTurnHoldAgent{interruptErr: errors.New("endpoint returned 500")}
	m, _ := heldHostMidTurn(agent)

	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	msg := runCmd(t, cmd)

	if got := agent.pauses(); len(got) != 1 {
		t.Errorf("Pause calls = %v, want the gate shut even though the cancel failed", got)
	}
	done, ok := msg.(pauseDoneMsg)
	if !ok {
		t.Fatalf("hold produced %T, want pauseDoneMsg", msg)
	}
	if done.err == nil {
		t.Fatal("a failed cancel was reported as a clean hold")
	}
	if !strings.Contains(done.err.Error(), "interrupt") || !strings.Contains(done.err.Error(), "endpoint returned 500") {
		t.Errorf("error = %q, want it to name the half that failed and why", done.err)
	}

	out, _ := m.Update(done)
	if got := lastText(out.(model)); !strings.Contains(got, "endpoint returned 500") {
		t.Errorf("last row = %q, want the failure surfaced inline", got)
	}
}

// TestHold_WithoutARemoteInterrupterIsUnchanged. A Pauser-only host has
// nothing to cancel with, and must still hold rather than erroring or
// dispatching nothing.
func TestHold_WithoutARemoteInterrupterIsUnchanged(t *testing.T) {
	agent := &perTurnPausable{}
	m, cancelled := heldHostMidTurn(agent)

	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	msg := runCmd(t, cmd)

	if !*cancelled {
		t.Error("local cancelTurn did not fire")
	}
	if got := agent.pauses(); len(got) != 1 {
		t.Errorf("Pause calls = %v, want exactly one hold", got)
	}
	if _, ok := msg.(pauseDoneMsg); !ok {
		t.Fatalf("hold produced %T, want pauseDoneMsg", msg)
	}
}

// TestPauseEvent_ReplayDoesNotReArmTheGateOnAPerTurnHost is the third
// row of the operator's paste: a bare "Resumed." arriving after a turn
// that had nothing to do with a hold.
//
// The replayed backlog was already silent (see
// TestPauseEvent_ReplayedBacklogIsSilentOnAPerTurnHost) but it still
// applied state, so a stale paused frame shut a gate the host had long
// since opened. The next poll saw the host was not paused, called that
// a transition and narrated it — with lastResumeMode already spent by
// the real resume, which is why the row was bare.
func TestPauseEvent_ReplayDoesNotReArmTheGateOnAPerTurnHost(t *testing.T) {
	agent := &perTurnPausable{}
	m := newModel(Options{Agent: agent})
	m.width, m.height = 100, 40
	if m.liveMode {
		t.Fatal("setup: perTurnPausable was detected as a LiveAgent")
	}
	m = pollGate(t, m, agent) // host is not paused, and never was here
	before := len(systemRows(m))

	out, _ := m.Update(pauseEventMsg{gen: m.sessionGen, event: PauseEvent{
		State:       PauseStatePaused,
		Reason:      "operator interrupt",
		Interrupted: true,
	}})
	replayed := out.(model)

	if replayed.pause.paused() {
		t.Error("a stale replayed frame shut the gate; on a per-turn host the poll owns it")
	}
	if replayed.renderPauseBanner(80) != "" {
		t.Error("the banner came up for a hold that was over before the frame arrived")
	}

	// And the poll that follows has nothing to narrate, because
	// nothing moved.
	polled := pollGate(t, replayed, agent)
	if got := systemRows(polled); len(got) != before {
		t.Errorf("the poll narrated %d rows after a replayed frame: %q", len(got)-before, got[before:])
	}
}
