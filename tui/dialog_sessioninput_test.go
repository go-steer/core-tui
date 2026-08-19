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

// Tests for SessionInfo action rows (issue #56) — the session
// picker → text-input handoff that backs a "+ Attach to endpoint…"
// row. Base /switch behavior lives in switch_test.go.

package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// attachRowAgent is a SessionSwitcher whose session list ends in an
// action row: Enter there opens a text input instead of switching.
type attachRowAgent struct {
	switchAgent

	attachCalls []string // values SessionInput.Submit was invoked with
	attachErr   error
	attachAgent Agent
	validate    func(string) string
	nilSubmit   bool
}

func newAttachRowAgent() *attachRowAgent {
	a := &attachRowAgent{attachAgent: &bareAgent{id: "attached"}}
	a.sessions = []SessionInfo{
		{ID: "cur", Display: "current", Current: true},
		{ID: "other", Display: "other-session"},
	}
	return a
}

// Sessions appends the synthetic action row to the base list. Built
// per call (not stored) so the closure always sees live test state.
func (a *attachRowAgent) Sessions() []SessionInfo {
	row := SessionInfo{
		ID:      "+attach",
		Display: "+ Attach to endpoint…",
		Input: &SessionInput{
			Title:       "Attach to Endpoint",
			Prompt:      "Daemon URL:",
			Placeholder: "http://host:7778",
			Validate:    a.validate,
		},
	}
	if !a.nilSubmit {
		row.Input.Submit = func(v string) (SwitchTarget, error) {
			a.attachCalls = append(a.attachCalls, v)
			if a.attachErr != nil {
				return SwitchTarget{}, a.attachErr
			}
			return SwitchTarget{Agent: a.attachAgent, Note: "Attached to " + v}, nil
		}
	}
	return append(append([]SessionInfo{}, a.sessions...), row)
}

// openAttachRow opens the session picker, moves the cursor onto the
// action row, and presses Enter — the common prelude for every test
// below. Asserts the handoff itself so the callers can focus on
// what happens after.
func openAttachRow(t *testing.T, m *model) {
	t.Helper()
	q := readySessionPicker(m)
	q.idx = 2 // the action row
	ans, cmd := q.Key(keyMsgFromStroke("enter"))
	if ans != nil || cmd == nil {
		t.Fatalf("enter on action row = (%#v, %v), want a Cmd and no answer", ans, cmd)
	}
	// The widget only NAMES the row; Update is what opens the input,
	// because overlay pops the front dialog after Key returns.
	for _, msg := range drainBatch(t, cmd) {
		out, _ := m.Update(msg)
		*m = out.(model)
	}
	if !m.overlayStack.hasID(sessionInputDialogID) {
		t.Fatalf("action row did not open the text-input dialog")
	}
	if !m.overlayStack.hasID(sessionPickerDialogID) {
		t.Fatalf("picker should stay open underneath the text input")
	}
}

// pressEnter drives Enter through the overlay the way handleKey does.
func pressEnter(m *model) (bool, tea.Cmd) {
	return m.overlayStack.handleKeyMsg(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}), m)
}

// commitAttach presses Enter on the text input and drives the commit
// all the way through: SessionInput.Submit runs off the Update
// goroutine now (issue #194), so Enter yields only a Cmd, and nothing
// reaches the model until that Cmd's reply has been fed back through
// Update. Returns whatever the applied reply produced — the listener
// batch, on a successful attach.
//
// The tests that care about the STAGING itself — that Enter is cheap,
// that a second Enter is refused, that a dismissed dialog drops its
// reply — drive the two halves by hand over in host_async_test.go.
// This is for the ones that only care where the flow ends up.
func commitAttach(t *testing.T, m *model) tea.Cmd {
	t.Helper()
	consumed, cmd := pressEnter(m)
	if !consumed {
		t.Fatalf("enter on the text input was not consumed")
	}
	if cmd == nil {
		t.Fatalf("enter produced no Cmd — SessionInput.Submit would never run")
	}
	msg := cmd()
	sub, ok := msg.(sessionInputSubmittedMsg)
	if !ok {
		t.Fatalf("enter produced %T, want sessionInputSubmittedMsg", msg)
	}
	out, next := m.Update(sub)
	*m = out.(model)
	return next
}

// TestSessionPicker_ActionRowOpensTextInput — Enter on a row with
// Input set stacks a text input and leaves the picker underneath;
// SwitchToSession never sees the action row's ID.
func TestSessionPicker_ActionRowOpensTextInput(t *testing.T) {
	agent := newAttachRowAgent()
	m := newModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	openAttachRow(t, &m)
	if len(agent.switchCalls) != 0 {
		t.Errorf("SwitchToSession called for an action row: %v", agent.switchCalls)
	}
	front := m.overlayStack.front()
	if front.ID() != sessionInputDialogID {
		t.Fatalf("front dialog = %q, want %q", front.ID(), sessionInputDialogID)
	}
	out := renderPlain(front, &m)
	for _, want := range []string{"Attach to Endpoint", "Daemon URL:", "http://host:7778", "esc back"} {
		if !strings.Contains(out, want) {
			t.Errorf("text input render missing %q:\n%s", want, out)
		}
	}
}

// TestSessionPicker_ActionRowSubmitAttaches — typing a value and
// hitting Enter runs SessionInput.Submit, applies the SwitchTarget,
// and closes BOTH dialogs.
func TestSessionPicker_ActionRowSubmitAttaches(t *testing.T) {
	agent := newAttachRowAgent()
	m := newModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	openAttachRow(t, &m)
	typeInto(t, m.overlayStack.front(), &m, "http://otherhost:7778")
	cmd := commitAttach(t, &m)

	if cmd == nil {
		t.Errorf("expected the applySwitchTarget listener batch as a Cmd")
	}
	if got := agent.attachCalls; len(got) != 1 || got[0] != "http://otherhost:7778" {
		t.Errorf("Submit calls = %v, want the typed URL once", got)
	}
	if m.opts.Agent != agent.attachAgent {
		t.Errorf("Agent not swapped to the attach target: %v", m.opts.Agent)
	}
	if m.overlayStack.hasDialogs() {
		t.Errorf("both dialogs should be closed after a successful attach")
	}
}

// TestSessionPicker_ActionRowSubmitError — a failing Submit leaves a
// RoleError row, keeps the current Agent, and closes both dialogs.
func TestSessionPicker_ActionRowSubmitError(t *testing.T) {
	agent := newAttachRowAgent()
	agent.attachErr = errors.New("dial tcp: connection refused")
	m := newModel(Options{Agent: agent})
	m.viewport.SetWidth(80)
	before := m.opts.Agent

	openAttachRow(t, &m)
	typeInto(t, m.overlayStack.front(), &m, "http://dead:1")
	commitAttach(t, &m)

	if m.opts.Agent != before {
		t.Errorf("Agent must not swap when Submit fails")
	}
	if m.overlayStack.hasDialogs() {
		t.Errorf("both dialogs should close on a failed attach")
	}
	last := m.history.Snapshot()[m.history.Len()-1]
	if last.Role != RoleError || !strings.Contains(last.Text, "connection refused") {
		t.Errorf("last row = %+v, want a RoleError naming the dial failure", last)
	}
}

// TestSessionPicker_ActionRowNilAgent — a Submit that returns a nil
// Agent is rejected the same way SwitchToSession's is.
func TestSessionPicker_ActionRowNilAgent(t *testing.T) {
	agent := newAttachRowAgent()
	agent.attachAgent = nil
	m := newModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	openAttachRow(t, &m)
	typeInto(t, m.overlayStack.front(), &m, "x")
	commitAttach(t, &m)

	last := m.history.Snapshot()[m.history.Len()-1]
	if last.Role != RoleError || !strings.Contains(last.Text, "nil Agent") {
		t.Errorf("last row = %+v, want a nil-Agent RoleError", last)
	}
	if m.overlayStack.hasDialogs() {
		t.Errorf("both dialogs should close on a nil-Agent target")
	}
}

// TestSessionPicker_ActionRowNilSubmit — a malformed action row
// (Input set, Submit nil) degrades to an error row, never a panic.
func TestSessionPicker_ActionRowNilSubmit(t *testing.T) {
	agent := newAttachRowAgent()
	agent.nilSubmit = true
	m := newModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	openAttachRow(t, &m)
	pressEnter(&m)

	last := m.history.Snapshot()[m.history.Len()-1]
	if last.Role != RoleError || !strings.Contains(last.Text, "no Submit closure") {
		t.Errorf("last row = %+v, want a no-Submit RoleError", last)
	}
}

// TestSessionPicker_ActionRowValidate — a failing Validate keeps the
// text input open and never reaches Submit.
func TestSessionPicker_ActionRowValidate(t *testing.T) {
	agent := newAttachRowAgent()
	agent.validate = func(v string) string {
		if !strings.HasPrefix(v, "http") {
			return "endpoint must be an http(s) URL"
		}
		return ""
	}
	m := newModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	openAttachRow(t, &m)
	typeInto(t, m.overlayStack.front(), &m, "otherhost")
	pressEnter(&m)

	if len(agent.attachCalls) != 0 {
		t.Errorf("Submit ran despite validation failure: %v", agent.attachCalls)
	}
	if !m.overlayStack.hasID(sessionInputDialogID) {
		t.Fatalf("text input should stay open on validation failure")
	}
	if out := renderPlain(m.overlayStack.front(), &m); !strings.Contains(out, "endpoint must be an http(s) URL") {
		t.Errorf("validation error not rendered:\n%s", out)
	}
}

// TestSessionPicker_ActionRowEscReturnsToPicker — esc on the nested
// text input pops back to the picker rather than out to chat, and
// no keystroke leaks into the chat textarea behind the modals.
func TestSessionPicker_ActionRowEscReturnsToPicker(t *testing.T) {
	agent := newAttachRowAgent()
	m := newModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	openAttachRow(t, &m)
	typeInto(t, m.overlayStack.front(), &m, "half-typed")

	out, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = out.(model)
	if m.overlayStack.hasID(sessionInputDialogID) {
		t.Errorf("esc should pop the text input")
	}
	if !m.overlayStack.hasID(sessionPickerDialogID) {
		t.Errorf("esc should leave the picker open underneath")
	}
	if v := m.input.Value(); v != "" {
		t.Errorf("dialog keystrokes leaked into the chat textarea: %q", v)
	}

	// A second esc closes the picker itself.
	out, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = out.(model)
	if m.overlayStack.hasDialogs() {
		t.Errorf("second esc should close the picker")
	}
}

// TestSessionPicker_ActionRowRender — action rows show a chevron and
// never the "(id)" / "(current)" session decorations.
func TestSessionPicker_ActionRowRender(t *testing.T) {
	agent := newAttachRowAgent()
	m := newModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	readySessionPicker(&m)
	lines := sessionPickerLines(&m)
	var row string
	for _, l := range lines {
		if strings.Contains(l, "Attach to endpoint") {
			row = l
		}
	}
	if row == "" {
		t.Fatalf("action row missing from the picker render:\n%s", strings.Join(lines, "\n"))
	}
	if strings.Contains(row, "+attach") {
		t.Errorf("action row leaked its magic ID: %q", row)
	}
	if strings.Contains(row, "(current)") {
		t.Errorf("action row marked (current): %q", row)
	}
	if !strings.Contains(row, glyphCollapsed) {
		t.Errorf("action row missing the %q affordance: %q", glyphCollapsed, row)
	}
}

// TestSlashSwitch_ActionRowByID — `/switch +attach` opens the row's
// text input instead of handing the magic ID to SwitchToSession.
func TestSlashSwitch_ActionRowByID(t *testing.T) {
	agent := newAttachRowAgent()
	m := newModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	m, _ = submitSwitch(t, m, "/switch +attach")

	if !m.overlayStack.hasID(sessionInputDialogID) {
		t.Errorf("/switch <action-id> should open the text input")
	}
	if len(agent.switchCalls) != 0 {
		t.Errorf("SwitchToSession called with an action-row ID: %v", agent.switchCalls)
	}
}

// TestSlashSwitch_PlainIDStillDirectJumps — the action-row lookup
// must not disturb `/switch <real-session-id>`.
func TestSlashSwitch_PlainIDStillDirectJumps(t *testing.T) {
	agent := newAttachRowAgent()
	m := newModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	m, _ = submitSwitch(t, m, "/switch other")

	if m.overlayStack.hasDialogs() {
		t.Errorf("a real session ID should switch directly, not open a dialog")
	}
	if got := agent.switchCalls; len(got) != 1 || got[0] != "other" {
		t.Errorf("switchCalls = %v, want [other]", got)
	}
}

// TestOverlay_ThemePickerEscRestores — the esc cascade now routes
// through the front dialog, so the theme picker's restore-on-cancel
// actually runs (it was unreachable while esc popped the stack
// directly).
func TestOverlay_ThemePickerEscRestores(t *testing.T) {
	m := newModel(Options{Agent: &bareAgent{id: "a"}})
	m.viewport.SetWidth(80)
	m.applyNamedTheme(BuiltinThemes()[0].Name)
	original := m.themeName

	askThemePicker(&m)
	pressPicker(t, &m, "down") // live-previews the next theme
	if m.themeName == original {
		t.Fatalf("cursor move should have previewed a different theme")
	}

	out, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = out.(model)
	if m.themeName != original {
		t.Errorf("esc left theme = %q, want the pre-picker %q", m.themeName, original)
	}
	if m.overlayStack.hasDialogs() {
		t.Errorf("esc should still close the picker")
	}
}
