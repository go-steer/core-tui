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
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// graceRig wires a real Prompter to a model and delivers a permission
// request through the normal msg path, so permissionShownAt is
// stamped exactly as it is in production. decisions receives whatever
// the blocked AskApproval call returns.
func graceRig(t *testing.T) (Model, *Prompter, <-chan PermissionDecision) {
	t.Helper()
	p := NewPrompter()
	decisions := make(chan PermissionDecision, 1)
	req := PermissionRequest{ToolName: "bash", Verb: "run", Detail: "rm -rf /tmp/x"}
	go func() {
		d, _ := p.AskApproval(context.Background(), req)
		decisions <- d
	}()
	if _, ok := p.nextRequest(context.Background()); !ok {
		t.Fatal("setup: nextRequest returned !ok with a pending request")
	}

	m := NewModel(Options{Prompter: p})
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = out.(Model)
	out, _ = m.Update(permissionRequestMsg{req: req})
	m = out.(Model)
	if m.pendingPermission == nil {
		t.Fatal("setup: expected the permission modal to be open")
	}
	if m.permissionShownAt.IsZero() {
		t.Fatal("setup: permissionRequestMsg did not stamp permissionShownAt")
	}
	return m, p, decisions
}

func typeWord(m Model, word string) Model {
	for _, r := range word {
		out, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)}))
		m = out.(Model)
	}
	return m
}

// Issue #95: the modal was decision-live from the frame it appeared,
// so keystrokes already in the terminal's input buffer were consumed
// as the answer. Typing "say" across three prompts dispatched
// allow-session, allow-always and allow-once — three grants, one
// persistent, from a word aimed at the prompt.
func TestPermissionGrace_BufferedKeysDoNotDecide(t *testing.T) {
	m, _, decisions := graceRig(t)

	m = typeWord(m, "say")

	if m.pendingPermission == nil {
		t.Fatal("buffered keystrokes answered a modal the operator had not seen")
	}
	select {
	case d := <-decisions:
		t.Fatalf("a decision (%v) was dispatched inside the grace window", d)
	default:
	}
	// And they aren't dropped either — they land where they were
	// aimed, in the prompt.
	if got := m.input.Value(); got != "say" {
		t.Errorf("prompt holds %q, want \"say\" — buffered input should be routed, not swallowed", got)
	}
}

// Once the window has passed the same key decides, and the decision
// reaches the host.
func TestPermissionGrace_ExpiresAndDecidesNormally(t *testing.T) {
	m, _, decisions := graceRig(t)
	// Backdate rather than sleep.
	m.permissionShownAt = time.Now().Add(-modalInputGrace - time.Millisecond)

	out, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'y', Text: "y"}))
	m = out.(Model)

	if m.pendingPermission != nil {
		t.Fatal("y after the grace window did not close the modal")
	}
	if got := m.input.Value(); got != "" {
		t.Errorf("decision key leaked into the prompt: %q", got)
	}
	select {
	case d := <-decisions:
		if d != DecisionAllowOnce {
			t.Errorf("decision = %v, want AllowOnce", d)
		}
	case <-time.After(time.Second):
		t.Fatal("no decision reached the host after the grace window")
	}
}

// Every one of R-PERM-2's six keys is held, including the two that
// widen authority beyond this call — the whole point of the window.
func TestPermissionGrace_CoversEveryDecisionKey(t *testing.T) {
	for _, stroke := range []string{"y", "n", "s", "v", "t", "a"} {
		m, _, decisions := graceRig(t)
		out, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: rune(stroke[0]), Text: stroke}))
		m = out.(Model)
		if m.pendingPermission == nil {
			t.Errorf("%q decided inside the grace window", stroke)
		}
		select {
		case d := <-decisions:
			t.Errorf("%q dispatched %v inside the grace window", stroke, d)
		default:
		}
	}
}

// Esc stays live: it denies, which is the fail-safe direction, and a
// buffered Esc costs at most a re-ask.
func TestPermissionGrace_EscStillDenies(t *testing.T) {
	m, _, decisions := graceRig(t)

	out, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = out.(Model)

	if m.pendingPermission != nil {
		t.Fatal("Esc did not close the modal")
	}
	select {
	case d := <-decisions:
		if d != DecisionDeny {
			t.Errorf("Esc dispatched %v, want Deny", d)
		}
	case <-time.After(time.Second):
		t.Fatal("Esc dispatched no decision")
	}
}

// The elicit modal has the identical shape, so the keys that commit a
// result are held too — while editing and navigation stay live.
func TestElicitGrace_HoldsCommitKeysOnly(t *testing.T) {
	e := NewElicitor().(*elicitor)
	results := make(chan ElicitResult, 1)
	req := ElicitRequest{
		Title: "creds",
		Fields: []ElicitField{
			{Name: "user", Type: ElicitFieldString},
			{Name: "note", Type: ElicitFieldString},
		},
	}
	go func() {
		r, _ := e.Elicit(context.Background(), "srv", req)
		results <- r
	}()
	if _, ok := e.nextRequest(context.Background()); !ok {
		t.Fatal("setup: nextRequest returned !ok with a pending request")
	}

	m := NewModel(Options{Elicitor: e})
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = out.(Model)
	out, _ = m.Update(elicitRequestMsg{serverName: "srv", req: req})
	m = out.(Model)
	if m.elicitShownAt.IsZero() {
		t.Fatal("setup: elicitRequestMsg did not stamp elicitShownAt")
	}

	// Enter inside the window must not submit.
	out, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = out.(Model)
	if m.pendingElicit == nil {
		t.Fatal("Enter submitted the form inside the grace window")
	}
	select {
	case r := <-results:
		t.Fatalf("a result (%v) was dispatched inside the grace window", r.Action)
	default:
	}

	// Editing and navigation are unaffected — they're visible and
	// reversible, and nothing leaves the TUI until a commit.
	m = typeWord(m, "ada")
	if got, _ := m.elicitValues["user"].(string); got != "ada" {
		t.Errorf("field edits blocked during the grace window: user = %q", got)
	}
	out, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	m = out.(Model)
	if m.elicitFieldIdx != 1 {
		t.Errorf("Tab nav blocked during the grace window: fieldIdx = %d", m.elicitFieldIdx)
	}

	// Past the window, Enter submits and the values reach the host.
	m.elicitShownAt = time.Now().Add(-modalInputGrace - time.Millisecond)
	out, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = out.(Model)
	if m.pendingElicit != nil {
		t.Fatal("Enter after the grace window did not submit")
	}
	select {
	case r := <-results:
		if r.Action != ElicitActionSubmit {
			t.Errorf("action = %v, want Submit", r.Action)
		}
		if got, _ := r.Values["user"].(string); got != "ada" {
			t.Errorf("submitted user = %q, want \"ada\"", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no result reached the host after the grace window")
	}
}

// A Model whose modal state was set directly (tests, hosts poking at
// fields) has a zero stamp and must behave as it always did.
func TestModalGrace_ZeroStampIsExpired(t *testing.T) {
	var m Model
	if m.withinGrace(time.Time{}) {
		t.Error("a zero stamp must read as expired, not as a fresh modal")
	}
	if !m.withinGrace(time.Now()) {
		t.Error("a stamp from now must be inside the window")
	}
	if m.withinGrace(time.Now().Add(-modalInputGrace - time.Millisecond)) {
		t.Error("a stamp older than the window must read as expired")
	}
}
