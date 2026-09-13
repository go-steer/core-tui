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
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// errorRows returns the RoleError entries in history, which is what
// the absorb rule is ultimately about: how many warning blocks an
// operator sees for one guardrail halt.
func errorRows(m model) []Message {
	var out []Message
	for _, e := range m.history.entries {
		if e.Role == RoleError {
			out = append(out, e)
		}
	}
	return out
}

const (
	testTripReason = "watchdog halted the agent (repeated-tool-call): looping on read_file with identical args. Clear it with /guardrail reset watchdog."
	testCancelMsg  = "turn canceled"
)

func haltingTrip() guardrailTripMsg {
	return guardrailTripMsg{trip: GuardrailTrip{
		Guardrail:  GuardrailWatchdog,
		Reason:     testTripReason,
		HaltedTurn: true,
	}}
}

func boundaryTrip() guardrailTripMsg {
	return guardrailTripMsg{trip: GuardrailTrip{
		Guardrail:  GuardrailWatchdog,
		Reason:     testTripReason,
		HaltedTurn: false,
	}}
}

func canceledTurnError() turnErrorMsg {
	return turnErrorMsg{turnError: TurnError{
		Kind:    TurnErrorCanceled,
		Message: testCancelMsg,
	}}
}

// TestEmitEvent_GuardrailTripFansOut pins that a GuardrailTrip on an
// Event reaches the model as its own msg. The fan-out order matters
// as much as the msg does: a host that surfaced the trip AFTER the
// turn-error on the same Event would arm absorbNextCancel one frame
// too late and the cancel would render anyway.
func TestEmitEvent_GuardrailTripFansOut(t *testing.T) {
	ch := make(chan tea.Msg, 8)
	trip := GuardrailTrip{Guardrail: GuardrailCostCeiling, Reason: "over ceiling", HaltedTurn: true}
	emitEvent(context.Background(), ch, 0, Event{
		GuardrailTrip: &trip,
		TurnError:     &TurnError{Kind: TurnErrorCanceled},
	})
	got := drain(ch)
	if len(got) != 2 {
		t.Fatalf("expected 2 msgs (trip + turn-error), got %d (%v)", len(got), got)
	}
	gt, ok := got[0].(guardrailTripMsg)
	if !ok {
		t.Fatalf("first msg = %T, want guardrailTripMsg — the trip must precede the cancel it explains", got[0])
	}
	if gt.trip.Guardrail != GuardrailCostCeiling || !gt.trip.HaltedTurn {
		t.Errorf("trip payload = %+v, want cost_ceiling with halted_turn", gt.trip)
	}
	if _, ok := got[1].(turnErrorMsg); !ok {
		t.Fatalf("second msg = %T, want turnErrorMsg", got[1])
	}
}

// TestGuardrailTrip_AppendsStyledRow asserts the handler appends a
// RoleError Message carrying the structured payload and the renderer
// paints the guardrail block off it.
func TestGuardrailTrip_AppendsStyledRow(t *testing.T) {
	m := newModel(Options{ForceTheme: ThemeDark, Agent: &noopAgent{}})
	m.viewport.SetWidth(80)

	got, _ := m.Update(boundaryTrip())
	m2 := got.(model)
	rows := errorRows(m2)
	if len(rows) != 1 {
		t.Fatalf("guardrail-trip appended %d error rows, want 1", len(rows))
	}
	if rows[0].GuardrailTrip == nil {
		t.Fatal("appended Message.GuardrailTrip should be non-nil")
	}
	if rows[0].GuardrailTrip.Guardrail != GuardrailWatchdog {
		t.Errorf("Message.GuardrailTrip.Guardrail = %q, want %q", rows[0].GuardrailTrip.Guardrail, GuardrailWatchdog)
	}

	rendered := m2.renderMessage(rows[0])
	for _, want := range []string{"guardrail halted", GuardrailWatchdog, "repeated-tool-call", "/guardrail reset"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered guardrail-trip missing %q\n  output: %s", want, rendered)
		}
	}
	// The block is the trip's, not the turn-error renderer's: a halt
	// is not a turn outcome and must not borrow the kind header.
	if strings.Contains(rendered, "hint:") {
		t.Errorf("guardrail block should not grow a hint line — the reason already carries the reset affordance\n  output: %s", rendered)
	}
}

// TestGuardrailTrip_HaltedTurnAbsorbsFollowingCancel is the reason
// halted_turn exists. Protocol 1.13.0 stopped suppressing the
// guardrail-caused cancel on the producer (core-agent #891), so the
// contentless "⚠ canceled" block that core-agent #818 removed comes
// back unless this consumer drops it. One halt, one row.
func TestGuardrailTrip_HaltedTurnAbsorbsFollowingCancel(t *testing.T) {
	m := newModel(Options{ForceTheme: ThemeDark, Agent: &noopAgent{}})
	m.viewport.SetWidth(80)

	got, _ := m.Update(haltingTrip())
	got2, _ := got.(model).Update(canceledTurnError())
	m2 := got2.(model)

	rows := errorRows(m2)
	if len(rows) != 1 {
		t.Fatalf("a halting trip plus its cancel rendered %d error rows, want 1 (the trip)", len(rows))
	}
	if rows[0].GuardrailTrip == nil {
		t.Errorf("the surviving row should be the trip, got %+v", rows[0])
	}
	if m2.absorbNextCancel {
		t.Error("absorbNextCancel should be spent after the cancel it was armed for")
	}
}

// TestGuardrailTrip_BoundaryTripDoesNotAbsorb. A trip at a turn
// boundary cut nothing, so the turn's own terminal frame is
// unrelated. If a cancel follows — the operator hit Esc on the next
// turn, say — it is genuinely theirs and must render.
func TestGuardrailTrip_BoundaryTripDoesNotAbsorb(t *testing.T) {
	m := newModel(Options{ForceTheme: ThemeDark, Agent: &noopAgent{}})
	m.viewport.SetWidth(80)

	got, _ := m.Update(boundaryTrip())
	if got.(model).absorbNextCancel {
		t.Fatal("a halted_turn:false trip must not arm the absorb")
	}
	got2, _ := got.(model).Update(canceledTurnError())
	if rows := errorRows(got2.(model)); len(rows) != 2 {
		t.Fatalf("boundary trip plus a cancel rendered %d error rows, want 2", len(rows))
	}
}

// TestGuardrailTrip_CancelWithoutTripStillRenders guards the
// baseline the absorb must not disturb: with nothing armed, a cancel
// renders exactly as it did before 1.13.0.
func TestGuardrailTrip_CancelWithoutTripStillRenders(t *testing.T) {
	m := newModel(Options{ForceTheme: ThemeDark, Agent: &noopAgent{}})
	m.viewport.SetWidth(80)

	got, _ := m.Update(canceledTurnError())
	rows := errorRows(got.(model))
	if len(rows) != 1 || rows[0].TurnError == nil || rows[0].TurnError.Kind != TurnErrorCanceled {
		t.Fatalf("an unaccompanied cancel should render its own block, got %+v", rows)
	}
}

// TestGuardrailTrip_AbsorbIsOneShotOnKind. The arming promises
// exactly one following turn-error. If what arrives is a different
// kind, the promise is spent on it and it renders: a halt does not
// license swallowing an unrelated failure.
func TestGuardrailTrip_AbsorbIsOneShotOnKind(t *testing.T) {
	m := newModel(Options{ForceTheme: ThemeDark, Agent: &noopAgent{}})
	m.viewport.SetWidth(80)

	got, _ := m.Update(haltingTrip())
	got2, _ := got.(model).Update(turnErrorMsg{turnError: TurnError{
		Kind:    TurnErrorRateLimited,
		Message: "Vertex quota exceeded.",
	}})
	m2 := got2.(model)
	if rows := errorRows(m2); len(rows) != 2 {
		t.Fatalf("a non-cancel error after a halting trip rendered %d error rows, want 2", len(rows))
	}
	if m2.absorbNextCancel {
		t.Error("absorbNextCancel should be consumed by whatever turn-error arrives, not held for a later cancel")
	}
}

// TestGuardrailTrip_FinalizeTurnDisarmsAbsorb. The halting cancel
// arrives inside the turn it cut, so if the turn ends without one
// the arming is stale. Left armed it would swallow the operator's
// Esc on a LATER turn — the one cancel that must always be visible.
func TestGuardrailTrip_FinalizeTurnDisarmsAbsorb(t *testing.T) {
	m := newModel(Options{ForceTheme: ThemeDark, Agent: &noopAgent{}})
	m.viewport.SetWidth(80)

	got, _ := m.Update(haltingTrip())
	m2 := got.(model)
	if !m2.absorbNextCancel {
		t.Fatal("a halted_turn:true trip should arm the absorb")
	}
	m2.finalizeTurn(time.Second, "")
	if m2.absorbNextCancel {
		t.Fatal("finalizeTurn should disarm the absorb")
	}

	got3, _ := m2.Update(canceledTurnError())
	rows := errorRows(got3.(model))
	if len(rows) != 2 {
		t.Fatalf("a cancel on a later turn rendered %d error rows, want 2 (trip + cancel)", len(rows))
	}
	if rows[1].TurnError == nil || rows[1].TurnError.Kind != TurnErrorCanceled {
		t.Errorf("the second row should be the operator's cancel, got %+v", rows[1])
	}
}

// TestGuardrailTrip_StaleGenerationDropped. Session generation is
// the guard against frames from an attach that has already been
// replaced; a trip is no different, and must not arm the absorb for
// the session that succeeded it.
func TestGuardrailTrip_StaleGenerationDropped(t *testing.T) {
	m := newModel(Options{ForceTheme: ThemeDark, Agent: &noopAgent{}})
	m.sessionGen = 2
	trip := haltingTrip()
	trip.gen = 1

	got, _ := m.Update(trip)
	m2 := got.(model)
	if rows := errorRows(m2); len(rows) != 0 {
		t.Fatalf("a stale-generation trip rendered %d error rows, want 0", len(rows))
	}
	if m2.absorbNextCancel {
		t.Error("a stale-generation trip must not arm the absorb")
	}
}

// TestGuardrailTrip_SessionSwitchDisarmsAbsorb. The sessionGen guard
// does not cover this on its own: it drops the outgoing session's
// cancel — the frame the arming was waiting for — and would leave
// the arming to meet the incoming session's first cancel, which is
// an operator pressing Esc on a session that never tripped anything.
func TestGuardrailTrip_SessionSwitchDisarmsAbsorb(t *testing.T) {
	m := newModel(Options{ForceTheme: ThemeDark, Agent: &noopAgent{}})
	m.viewport.SetWidth(80)

	got, _ := m.Update(haltingTrip())
	m2 := got.(model)
	if !m2.absorbNextCancel {
		t.Fatal("a halted_turn:true trip should arm the absorb")
	}
	m2.applySwitchTarget(&SwitchTarget{Agent: &noopAgent{}})
	if m2.absorbNextCancel {
		t.Fatal("switching sessions should disarm the absorb")
	}

	cancel := canceledTurnError()
	cancel.gen = m2.sessionGen
	got3, _ := m2.Update(cancel)
	if rows := errorRows(got3.(model)); len(rows) != 1 {
		t.Fatalf("the new session's cancel rendered %d error rows, want 1", len(rows))
	}
}

// TestGuardrailTrip_RenderWithoutReason. The reason is the whole
// payload's content, but a producer that ships an empty one should
// still leave an operator with the guardrail's name rather than a
// blank warning row.
func TestGuardrailTrip_RenderWithoutReason(t *testing.T) {
	m := newModel(Options{ForceTheme: ThemeDark, Agent: &noopAgent{}})
	m.viewport.SetWidth(80)

	rendered := m.renderGuardrailTripBlock(GuardrailTrip{Guardrail: GuardrailCostCeiling}, 80)
	if !strings.Contains(rendered, GuardrailCostCeiling) {
		t.Errorf("reasonless trip dropped the guardrail name\n  output: %s", rendered)
	}
	if strings.Contains(rendered, "\n") {
		t.Errorf("reasonless trip should be a single header line, got\n%s", rendered)
	}

	unnamed := m.renderGuardrailTripBlock(GuardrailTrip{Reason: "something tripped"}, 80)
	if !strings.Contains(unnamed, "unknown") {
		t.Errorf("nameless trip should fall back to a placeholder\n  output: %s", unnamed)
	}
}
