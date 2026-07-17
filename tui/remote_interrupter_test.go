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
)

// remoteInterrupterAgent implements Agent + LiveAgent + RemoteInterrupter
// for the fallthrough tests. Interrupt captures whether it was called
// so we can pin the dispatch path.
type remoteInterrupterAgent struct {
	liveAgentStub
	interruptCalls atomic.Int32
	interruptErr   error
}

func (r *remoteInterrupterAgent) Interrupt(_ context.Context) error {
	r.interruptCalls.Add(1)
	return r.interruptErr
}

// TestInterrupt_LocalCancelWinsWhenAvailable pins that the local
// Run-path cancel path stays authoritative — when the operator
// submitted a turn in-process, /interrupt cancels that turn
// synchronously and does NOT reach for the remote hook (even when
// the host implements both). Regression signal against accidentally
// short-circuiting the load-bearing local path.
func TestInterrupt_LocalCancelWinsWhenAvailable(t *testing.T) {
	agent := &remoteInterrupterAgent{}
	m := NewModel(Options{Agent: agent})
	// Simulate an in-flight local turn: state=streaming AND
	// cancelTurn set. Both are the gate the slash handler checks.
	m.state = stateStreaming
	cancelled := false
	m.cancelTurn = func() { cancelled = true }

	handled, next, cmd := m.dispatchBuiltinSlash("interrupt", "")
	if !handled {
		t.Fatalf("dispatchBuiltinSlash returned handled=false for /interrupt")
	}
	if !cancelled {
		t.Errorf("expected local cancelTurn to fire when both paths available")
	}
	if got := agent.interruptCalls.Load(); got != 0 {
		t.Errorf("remote Interrupt should NOT fire when local cancel available; got %d calls", got)
	}
	// No follow-up cmd needed on the local path — the cancel is
	// synchronous.
	if cmd != nil {
		t.Errorf("local cancel should return nil cmd (synchronous); got %T", cmd())
	}
	_ = next
}

// TestInterrupt_RemoteFallthrough_FiresRemoteAndReportsOutcome pins
// the LiveAgent / observer-mode path: when there's no local turn
// context to cancel but the host implements RemoteInterrupter,
// /interrupt dispatches via the remote hook and surfaces the result
// as a follow-up system row (success) or error row (failure).
func TestInterrupt_RemoteFallthrough_FiresRemoteAndReportsOutcome(t *testing.T) {
	agent := &remoteInterrupterAgent{}
	m := NewModel(Options{Agent: agent})
	// No local turn — the state and cancelTurn stay at their
	// zero values. This mirrors observer-mode reality (LiveAgent
	// drains events but nothing ever populated cancelTurn).

	handled, next, cmd := m.dispatchBuiltinSlash("interrupt", "")
	if !handled {
		t.Fatal("dispatchBuiltinSlash returned handled=false")
	}
	if cmd == nil {
		t.Fatal("remote fallthrough should return a Cmd to invoke Interrupt off the Update loop")
	}
	// Placeholder row landed synchronously.
	nm := next.(Model)
	entries := nm.history.Snapshot()
	if len(entries) == 0 || !strings.Contains(entries[len(entries)-1].Text, "cancelling remote turn") {
		t.Errorf("expected 'cancelling remote turn…' placeholder in history, got %+v", entries)
	}
	// Cmd fires the remote call synchronously (network hop is stubbed
	// by our in-memory Interrupt method).
	msg := cmd()
	if agent.interruptCalls.Load() != 1 {
		t.Errorf("expected exactly one remote Interrupt call, got %d", agent.interruptCalls.Load())
	}
	done, ok := msg.(remoteInterruptDoneMsg)
	if !ok {
		t.Fatalf("cmd should return remoteInterruptDoneMsg, got %T", msg)
	}
	if done.err != nil {
		t.Errorf("happy-path remote should have no error, got %v", done.err)
	}

	// Feed the done msg through Update — should append the
	// success row.
	next2, _ := nm.Update(done)
	nm2 := next2.(Model)
	entries2 := nm2.history.Snapshot()
	last := entries2[len(entries2)-1]
	if last.Role != RoleSystem {
		t.Errorf("expected RoleSystem row on success, got Role=%v", last.Role)
	}
	if !strings.Contains(last.Text, "remote turn cancelled") {
		t.Errorf("expected 'remote turn cancelled' text, got %q", last.Text)
	}
}

// TestInterrupt_RemoteFallthrough_ErrorSurfaces pins that a failed
// remote Interrupt surfaces as an inline RoleError row so the
// operator knows to escalate (retry, restart daemon, manual kill).
func TestInterrupt_RemoteFallthrough_ErrorSurfaces(t *testing.T) {
	agent := &remoteInterrupterAgent{interruptErr: errors.New("endpoint returned 500")}
	m := NewModel(Options{Agent: agent})

	handled, next, cmd := m.dispatchBuiltinSlash("interrupt", "")
	if !handled || cmd == nil {
		t.Fatalf("expected handled with cmd, got handled=%v cmd=%v", handled, cmd)
	}
	nm := next.(Model)
	done := cmd().(remoteInterruptDoneMsg)
	if done.err == nil {
		t.Fatal("expected propagated error from Interrupt")
	}
	next2, _ := nm.Update(done)
	nm2 := next2.(Model)
	entries := nm2.history.Snapshot()
	last := entries[len(entries)-1]
	if last.Role != RoleError {
		t.Errorf("expected RoleError on failure, got Role=%v", last.Role)
	}
	if !strings.Contains(last.Text, "endpoint returned 500") {
		t.Errorf("expected underlying error text in row, got %q", last.Text)
	}
}

// TestInterrupt_NoLocalNoRemote_FallsBackToNoTurnMessage pins the
// original behavior for hosts implementing neither path — the
// operator still sees the historical "no turn in flight" message
// instead of a silent no-op.
func TestInterrupt_NoLocalNoRemote_FallsBackToNoTurnMessage(t *testing.T) {
	// stubAgent implements Agent only — no RemoteInterrupter, no
	// live turn.
	m := NewModel(Options{Agent: stubAgent{}})

	handled, next, cmd := m.dispatchBuiltinSlash("interrupt", "")
	if !handled {
		t.Fatal("expected handled=true even on the no-turn path")
	}
	if cmd != nil {
		t.Errorf("no-turn path should return nil cmd, got %T", cmd())
	}
	nm := next.(Model)
	entries := nm.history.Snapshot()
	last := entries[len(entries)-1]
	if !strings.Contains(last.Text, "no turn in flight") {
		t.Errorf("expected 'no turn in flight' fallback, got %q", last.Text)
	}
}
