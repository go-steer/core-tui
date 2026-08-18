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

// Tests for mid-session Agent switch (issues #48 / #53):
// SlashResult.SwitchTo, applySwitchTarget, /switch built-in, and the
// session picker dialog.

package tui

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// submitSwitch dispatches a `/switch <id>` and pumps the two host hops
// it takes since issue #137: the Sessions() enumerate, then — when the
// id turned out to name a session rather than an action row —
// SwitchToSession. Returns the model as of the last reply plus the Cmd
// that reply produced (the listener batch, on a switch that landed).
func submitSwitch(t *testing.T, m Model, text string) (Model, tea.Cmd) {
	t.Helper()
	out, cmd := m.dispatchSlash(text)
	m = out.(Model)
	for hop := 0; hop < 2 && cmd != nil; hop++ {
		msg := cmd()
		switch msg.(type) {
		case switchLookupMsg, sessionSwitchedMsg:
		default:
			return m, cmd
		}
		out, cmd = m.Update(msg)
		m = out.(Model)
	}
	return m, cmd
}

// bareAgent is a minimal Agent stub — no capabilities, no events.
type bareAgent struct{ id string }

func (b *bareAgent) Run(_ context.Context, _ string) iter.Seq2[Event, error] {
	return func(_ func(Event, error) bool) {}
}

// switchAgent implements Agent + SessionSwitcher for /switch tests.
// Sessions() returns a fixed list; SwitchToSession looks up by ID
// and hands back a bareAgent tagged with the picked ID.
type switchAgent struct {
	id       string
	sessions []SessionInfo

	switchCalls    []string      // IDs SwitchToSession was invoked with
	switchErr      error         // when non-nil, SwitchToSession returns it
	switchTarget   *SwitchTarget // when non-nil, override the auto-built target
	nextAgent      Agent         // when non-nil, the target's Agent
	nextTargetNote string        // set on the built target when non-empty
}

func (s *switchAgent) Run(_ context.Context, _ string) iter.Seq2[Event, error] {
	return func(_ func(Event, error) bool) {}
}
func (s *switchAgent) Sessions() []SessionInfo { return s.sessions }
func (s *switchAgent) SwitchToSession(id string) (SwitchTarget, error) {
	s.switchCalls = append(s.switchCalls, id)
	if s.switchErr != nil {
		return SwitchTarget{}, s.switchErr
	}
	if s.switchTarget != nil {
		return *s.switchTarget, nil
	}
	next := s.nextAgent
	if next == nil {
		next = &bareAgent{id: "next-" + id}
	}
	return SwitchTarget{Agent: next, Note: s.nextTargetNote}, nil
}

// TestApplySwitchTarget_ResetsState — seed a Model with history +
// streaming + modal + queue state; call applySwitchTarget; assert
// clean slate and swapped Agent.
func TestApplySwitchTarget_ResetsState(t *testing.T) {
	old := &bareAgent{id: "old"}
	m := NewModel(Options{Agent: old})
	m.viewport.SetWidth(80)

	// Seed heaping state so the reset has something to clear.
	m.history.Append(Message{Role: RoleUser, Text: "prior turn"})
	m.history.Append(Message{Role: RoleAssistant, Text: "prior reply"})
	m.state = stateStreaming
	m.spinnerActive = true
	m.inProgressText = "half done"
	m.currentModel = "old-model"
	m.overlayStack.ask(newPermissionQuestion(PermissionRequest{ToolName: "bash"}, PermissionInline), askAgent, nil)
	m.pendingExit = true
	m.confirmingClear = true
	m.queue = []QueueEntry{{Text: "queued", State: QueueQueued}}
	m.toast = "leftover"
	m.pushedProvider = "old-provider"
	beforeGen := m.sessionGen

	fresh := &bareAgent{id: "new"}
	cmd := m.applySwitchTarget(&SwitchTarget{
		Agent: fresh,
		Note:  "Attached to session new",
	})

	if m.opts.Agent != Agent(fresh) {
		t.Errorf("Agent = %v, want fresh", m.opts.Agent)
	}
	if m.sessionGen != beforeGen+1 {
		t.Errorf("sessionGen = %d, want %d", m.sessionGen, beforeGen+1)
	}
	if m.state != stateIdle {
		t.Errorf("state = %v, want stateIdle", m.state)
	}
	if m.spinnerActive {
		t.Errorf("spinnerActive should be false")
	}
	if m.inProgressText != "" {
		t.Errorf("inProgressText = %q, want empty", m.inProgressText)
	}
	if m.openPermission() != nil {
		t.Errorf("the permission question should be off the stack")
	}
	if m.pendingExit || m.confirmingClear {
		t.Errorf("pendingExit / confirmingClear should be cleared")
	}
	if len(m.queue) != 0 {
		t.Errorf("queue should be nil / empty, got %d entries", len(m.queue))
	}
	if m.toast != "" {
		t.Errorf("toast should be cleared, got %q", m.toast)
	}
	if m.pushedProvider != "" {
		t.Errorf("pushedProvider should be cleared, got %q", m.pushedProvider)
	}
	snap := m.history.Snapshot()
	if len(snap) != 1 || snap[0].Role != RoleSystem || snap[0].Text != "Attached to session new" {
		t.Errorf("post-switch history = %+v, want single Note row", snap)
	}
	if cmd == nil {
		t.Errorf("expected non-nil listener Cmd batch")
	}
}

// TestApplySwitchTarget_KeepsNilFields — SwitchTarget with only
// Agent set MUST leave the other opts fields untouched.
func TestApplySwitchTarget_KeepsNilFields(t *testing.T) {
	origMem := []MemoryFile{{Path: "AGENTS.md"}}
	origSkills := []SkillInfo{{Name: "verify"}}
	origPrompter := NewPrompter()
	origBranding := Branding{Wordmark: "before"}
	old := &bareAgent{id: "old"}

	m := NewModel(Options{
		Agent:    old,
		Memory:   origMem,
		Skills:   origSkills,
		Prompter: origPrompter,
		Branding: origBranding,
	})
	m.viewport.SetWidth(80)

	m.applySwitchTarget(&SwitchTarget{Agent: &bareAgent{id: "new"}})

	if got := m.opts.Memory; &got[0] != &origMem[0] {
		// Slice header comparison — different backing arrays would
		// indicate a replacement. Direct slice equality is via
		// pointer to first element.
		t.Errorf("Memory should be unchanged (same backing array)")
	}
	if got := m.opts.Skills; &got[0] != &origSkills[0] {
		t.Errorf("Skills should be unchanged")
	}
	if m.opts.Prompter != origPrompter {
		t.Errorf("Prompter should be unchanged")
	}
	if m.opts.Branding.Wordmark != "before" {
		t.Errorf("Branding should be unchanged, got %q", m.opts.Branding.Wordmark)
	}
}

// TestApplySwitchTarget_ReplacesNonNilFields — non-nil / non-zero
// SwitchTarget fields MUST overwrite Options.
func TestApplySwitchTarget_ReplacesNonNilFields(t *testing.T) {
	m := NewModel(Options{
		Agent:    &bareAgent{id: "old"},
		Memory:   []MemoryFile{{Path: "before.md"}},
		Branding: Branding{Wordmark: "before"},
	})
	m.viewport.SetWidth(80)

	newMem := []MemoryFile{{Path: "after.md"}}
	newBranding := &Branding{Wordmark: "after"}
	m.applySwitchTarget(&SwitchTarget{
		Agent:    &bareAgent{id: "new"},
		Memory:   newMem,
		Branding: newBranding,
	})
	if len(m.opts.Memory) != 1 || m.opts.Memory[0].Path != "after.md" {
		t.Errorf("Memory = %+v, want [after.md]", m.opts.Memory)
	}
	if m.opts.Branding.Wordmark != "after" {
		t.Errorf("Branding.Wordmark = %q, want 'after'", m.opts.Branding.Wordmark)
	}
}

// TestStaleTerminalMsg_Dropped — after sessionGen bumps, a
// straggler turnDoneMsg carrying the old gen MUST NOT trigger
// finalizeTurn / append rows to the new session.
func TestStaleTerminalMsg_Dropped(t *testing.T) {
	m := NewModel(Options{Agent: &bareAgent{}})
	m.viewport.SetWidth(80)
	m.sessionGen = 5
	// Start in streaming state so a real turnDoneMsg would finalize.
	m.state = stateStreaming
	m.inProgressText = "should not be committed"

	_, _ = m.Update(turnDoneMsg{gen: 4, elapsed: 0})
	if m.history.Len() != 0 {
		t.Errorf("stale turnDoneMsg should not mutate history, got %d entries", m.history.Len())
	}
	if m.state != stateStreaming {
		t.Errorf("stale turnDoneMsg should not flip state to idle")
	}

	// A matching-gen msg still triggers finalize.
	out, _ := m.Update(turnDoneMsg{gen: 5, elapsed: 0})
	got := out.(Model)
	if got.state != stateIdle {
		t.Errorf("matching-gen turnDoneMsg should trigger finalize, state = %v", got.state)
	}
}

// TestStaleStreamChunk_Dropped — a straggler streamChunkMsg with
// an old gen MUST NOT bleed text into the new session buffer.
func TestStaleStreamChunk_Dropped(t *testing.T) {
	m := NewModel(Options{Agent: &bareAgent{}})
	m.viewport.SetWidth(80)
	m.sessionGen = 3

	_, _ = m.Update(streamChunkMsg{gen: 2, text: "leaked", partial: true})
	if m.inProgressText != "" {
		t.Errorf("stale chunk leaked into inProgressText: %q", m.inProgressText)
	}

	// Matching-gen chunk accumulates as expected.
	out, _ := m.Update(streamChunkMsg{gen: 3, text: "kept", partial: true})
	got := out.(Model)
	if got.inProgressText != "kept" {
		t.Errorf("matching-gen chunk lost, inProgressText = %q", got.inProgressText)
	}
}

// TestApplySlashResult_SwitchTo — a SlashResult carrying SwitchTo
// MUST trigger a swap through applySwitchTarget.
func TestApplySlashResult_SwitchTo(t *testing.T) {
	old := &bareAgent{id: "old"}
	fresh := &bareAgent{id: "new"}
	m := NewModel(Options{Agent: old})
	m.viewport.SetWidth(80)
	m.history.Append(Message{Role: RoleUser, Text: "prior"})
	beforeGen := m.sessionGen

	out, cmd := m.applySlashResult("switch", SlashResult{
		SystemMessage: "swapped",
		SwitchTo:      &SwitchTarget{Agent: fresh, Note: "at new"},
	}, nil)
	got := out.(Model)

	if got.opts.Agent != Agent(fresh) {
		t.Errorf("Agent not swapped: %v", got.opts.Agent)
	}
	if got.sessionGen != beforeGen+1 {
		t.Errorf("sessionGen not bumped: %d → %d", beforeGen, got.sessionGen)
	}
	// History was wiped by the switch; only the post-switch Note
	// row remains.
	snap := got.history.Snapshot()
	if len(snap) != 1 || snap[0].Text != "at new" {
		t.Errorf("history after switch = %+v, want single Note row", snap)
	}
	if cmd == nil {
		t.Errorf("expected listener Cmd batch, got nil")
	}
}

// TestApplySlashResult_SwitchTo_NilAgent — SwitchTo with nil Agent
// MUST NOT swap; instead emit a RoleError row.
func TestApplySlashResult_SwitchTo_NilAgent(t *testing.T) {
	old := &bareAgent{id: "old"}
	m := NewModel(Options{Agent: old})
	m.viewport.SetWidth(80)

	out, cmd := m.applySlashResult("switch", SlashResult{
		SwitchTo: &SwitchTarget{Agent: nil},
	}, nil)
	got := out.(Model)

	if got.opts.Agent != Agent(old) {
		t.Errorf("Agent should not have swapped on nil-Agent SwitchTo")
	}
	if cmd != nil {
		t.Errorf("expected nil Cmd on rejected switch, got %T", cmd)
	}
	snap := got.history.Snapshot()
	if len(snap) != 1 || snap[0].Role != RoleError {
		t.Errorf("expected single RoleError row, got %+v", snap)
	}
	if !strings.Contains(snap[0].Text, "nil Agent") {
		t.Errorf("error text = %q, want 'nil Agent'", snap[0].Text)
	}
}

// TestSwitchBuiltin_OpensPicker — bare `/switch` opens the session
// picker dialog when the Agent implements SessionSwitcher.
func TestSwitchBuiltin_OpensPicker(t *testing.T) {
	agent := &switchAgent{
		id: "cur",
		sessions: []SessionInfo{
			{ID: "cur", Display: "current", Current: true},
			{ID: "b", Display: "other"},
		},
	}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	handled, out, _ := m.dispatchBuiltinSlash("switch", "")
	if !handled {
		t.Fatalf("expected /switch to be handled by builtin dispatcher")
	}
	got := out.(Model)
	if !got.overlayStack.hasID(sessionPickerDialogID) {
		t.Errorf("expected session picker dialog opened")
	}
	if len(agent.switchCalls) != 0 {
		t.Errorf("no-arg /switch should not call SwitchToSession, got %v", agent.switchCalls)
	}
}

// TestSwitchBuiltin_Direct — `/switch <id>` calls SwitchToSession
// and applies the returned target in one step.
func TestSwitchBuiltin_Direct(t *testing.T) {
	next := &bareAgent{id: "next-b"}
	agent := &switchAgent{
		id:             "cur",
		sessions:       []SessionInfo{{ID: "b"}},
		nextAgent:      next,
		nextTargetNote: "Attached to b",
	}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)
	beforeGen := m.sessionGen

	got, cmd := submitSwitch(t, m, "/switch b")
	if len(agent.switchCalls) != 1 || agent.switchCalls[0] != "b" {
		t.Errorf("switchCalls = %v, want [b]", agent.switchCalls)
	}
	if got.opts.Agent != Agent(next) {
		t.Errorf("Agent not swapped to next")
	}
	if got.sessionGen != beforeGen+1 {
		t.Errorf("sessionGen not bumped")
	}
	if cmd == nil {
		t.Errorf("expected listener Cmd batch")
	}
	snap := got.history.Snapshot()
	if len(snap) != 1 || !strings.Contains(snap[0].Text, "Attached to b") {
		t.Errorf("expected Note row, got %+v", snap)
	}
}

// TestSwitchBuiltin_DirectError — SwitchToSession error surfaces
// as a RoleError row; current session is unchanged.
func TestSwitchBuiltin_DirectError(t *testing.T) {
	agent := &switchAgent{
		id:        "cur",
		switchErr: errors.New("not found"),
	}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	got, _ := submitSwitch(t, m, "/switch bogus")
	if got.opts.Agent != Agent(agent) {
		t.Errorf("Agent should not have swapped on error")
	}
	// The acknowledgement row, then the failure.
	snap := got.history.Snapshot()
	if len(snap) != 2 || snap[1].Role != RoleError || !strings.Contains(snap[1].Text, "not found") {
		t.Errorf("expected an ack row then a RoleError with 'not found', got %+v", snap)
	}
}

// TestSwitchBuiltin_FallsThroughWhenNoCapability — an Agent that
// doesn't implement SessionSwitcher must let /switch fall through
// (handled=false) so a host-provided SlashProvider can pick it up.
func TestSwitchBuiltin_FallsThroughWhenNoCapability(t *testing.T) {
	agent := &bareAgent{id: "no-caps"}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	handled, _, _ := m.dispatchBuiltinSlash("switch", "b")
	if handled {
		t.Errorf("expected handled=false when Agent lacks SessionSwitcher")
	}
}

// TestSwitchBuiltin_SessAlias — /sess normalizes to /switch.
func TestSwitchBuiltin_SessAlias(t *testing.T) {
	agent := &switchAgent{
		id:       "cur",
		sessions: []SessionInfo{{ID: "cur", Current: true}},
	}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	handled, out, _ := m.dispatchBuiltinSlash("sess", "")
	if !handled {
		t.Fatalf("expected /sess (alias) to be handled")
	}
	got := out.(Model)
	if !got.overlayStack.hasID(sessionPickerDialogID) {
		t.Errorf("expected session picker opened via /sess alias")
	}
}

// TestSessionPickerDialog_EnterCommits — Enter on a non-current row
// dispatches SwitchToSession off the Update loop (issue #114) and
// stays open showing progress; the reply attaches the target and
// closes the dialog from Update.
func TestSessionPickerQuestion_EnterCommits(t *testing.T) {
	next := &bareAgent{id: "next"}
	agent := &switchAgent{
		id: "cur",
		sessions: []SessionInfo{
			{ID: "cur", Display: "current", Current: true},
			{ID: "other", Display: "other-session"},
		},
		nextAgent: next,
	}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	q := readySessionPicker(&m)
	q.idx = 1 // point at "other"
	ans, cmd := q.Key(keyMsgFromStroke("enter"))
	if ans != nil {
		t.Errorf("enter answered %#v; the host's reply is the answer", ans)
	}
	if cmd == nil {
		t.Fatalf("expected the off-loop SwitchToSession Cmd")
	}
	if len(agent.switchCalls) != 0 {
		t.Errorf("SwitchToSession ran inline: %v", agent.switchCalls)
	}

	// Enter names the session; Update resolves the switcher and makes
	// the call, and its reply is what ends the question.
	for _, msg := range drainBatch(t, cmd) {
		out, follow := m.Update(msg)
		m = out.(Model)
		for _, reply := range drainBatch(t, follow) {
			out, _ = m.Update(reply)
			m = out.(Model)
		}
	}
	if m.opts.Agent != Agent(next) {
		t.Errorf("Agent not swapped: %v", m.opts.Agent)
	}
	if len(agent.switchCalls) != 1 || agent.switchCalls[0] != "other" {
		t.Errorf("switchCalls = %v, want [other]", agent.switchCalls)
	}
	if m.overlayStack.hasID(sessionPickerDialogID) {
		t.Errorf("picker should close once the switch lands")
	}
}

// Enter on the attached row, Esc, and the wrapping cursor are all
// widget behaviour with no Model in them, so they moved out to
// question_external_test.go with the migration (#164 stage 3) — the
// in-package half of the picker's coverage is in
// question_sessionpicker_test.go.

// TestApplySwitchTarget_RedetectsLiveMode — swapping to a LiveAgent
// flips m.liveMode and returns a live-stream spawn Cmd; swapping
// back to a bare Agent flips it off.
func TestApplySwitchTarget_RedetectsLiveMode(t *testing.T) {
	m := NewModel(Options{Agent: &bareAgent{id: "one"}})
	m.viewport.SetWidth(80)
	if m.liveMode {
		t.Fatalf("bareAgent should not set liveMode")
	}

	// Swap to a LiveAgent → liveMode must flip on and the
	// listener batch must include a live-stream spawn Cmd
	// (we can't easily assert Cmd identity, but non-nil is
	// enough).
	live := newLiveAgentStub()
	cmd := m.applySwitchTarget(&SwitchTarget{Agent: live})
	if !m.liveMode {
		t.Errorf("expected liveMode true after swap to LiveAgent")
	}
	if cmd == nil {
		t.Errorf("expected non-nil Cmd (listener batch + live-stream spawn)")
	}

	// Swap back to a bareAgent → liveMode must flip off.
	m.applySwitchTarget(&SwitchTarget{Agent: &bareAgent{id: "three"}})
	if m.liveMode {
		t.Errorf("expected liveMode false after swap back to bareAgent")
	}
}

// TestApplySwitchTarget_AnswersAPendingPrompt pins issue #208: a
// prompt that is on screen when the session changes must be answered
// on the way out, not merely cleared.
//
// The assertion is on the blocked host call returning, not on the
// model field going nil — the field was always cleared, and clearing
// it is exactly the bug. Each arm runs the host call on its own
// goroutine, hands the request to Update through the same message the
// listener produces, and then requires the call to come back with the
// decision the switch is supposed to send. The ctx is never cancelled
// here, so nothing but a real dispatch can unblock it; that is what
// makes the test independent of whether a host happens to derive its
// tool ctx from the turn ctx.
func TestApplySwitchTarget_AnswersAPendingPrompt(t *testing.T) {
	t.Run("permission", func(t *testing.T) {
		p := NewPrompter()
		m := NewModel(Options{Agent: &bareAgent{id: "old"}, Prompter: p})
		m.viewport.SetWidth(80)

		decided := make(chan PermissionDecision, 1)
		go func() {
			d, _ := p.AskApproval(context.Background(), PermissionRequest{ToolName: "bash"})
			decided <- d
		}()
		req, ok := p.nextRequest(context.Background())
		if !ok {
			t.Fatal("nextRequest returned !ok with a pending request")
		}
		out, _ := m.Update(permissionRequestMsg{req: req})
		m = out.(Model)
		if m.openPermission() == nil {
			t.Fatal("the permission question was not seeded; the arm proves nothing")
		}

		m.applySwitchTarget(&SwitchTarget{Agent: &bareAgent{id: "new"}})

		select {
		case got := <-decided:
			if got != DecisionDeny {
				t.Errorf("decision = %v, want Deny", got)
			}
		case <-time.After(time.Second):
			t.Fatal("AskApproval still blocked 1s after the switch")
		}
		if m.openPermission() != nil {
			t.Errorf("the permission question should be off the stack after the switch")
		}
	})

	t.Run("elicit", func(t *testing.T) {
		e := NewElicitor()
		m := NewModel(Options{Agent: &bareAgent{id: "old"}, Elicitor: e})
		m.viewport.SetWidth(80)

		answered := make(chan ElicitResult, 1)
		go func() {
			r, _ := e.Elicit(context.Background(), "srv", ElicitRequest{
				Mode:   ElicitFormMode,
				Title:  "credentials",
				Fields: []ElicitField{{Name: "token", Description: "API token"}},
			})
			answered <- r
		}()
		flow, ok := e.(*elicitor).nextRequest(context.Background())
		if !ok {
			t.Fatal("nextRequest returned !ok with a pending request")
		}
		out, _ := m.Update(elicitRequestMsg{serverName: flow.serverName, req: flow.req})
		m = out.(Model)
		if m.openElicit() == nil {
			t.Fatal("the elicit form is not open; the arm proves nothing")
		}

		m.applySwitchTarget(&SwitchTarget{Agent: &bareAgent{id: "new"}})

		select {
		case got := <-answered:
			if got.Action != ElicitActionCancel {
				t.Errorf("action = %v, want Cancel", got.Action)
			}
		case <-time.After(time.Second):
			t.Fatal("Elicit still blocked 1s after the switch")
		}
		if m.openElicit() != nil {
			t.Errorf("the elicit form should be closed after the switch")
		}
	})
}
