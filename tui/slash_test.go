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
	"iter"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// slashAgent is a stub Agent + SlashProvider for dispatch tests.
type slashAgent struct {
	specs []SlashCommandSpec
	res   SlashResult
	err   error

	invokedName string
	invokedArgs string
}

func (s *slashAgent) Run(_ context.Context, _ string) iter.Seq2[Event, error] {
	return func(_ func(Event, error) bool) {}
}
func (s *slashAgent) SlashCommands() []SlashCommandSpec { return s.specs }
func (s *slashAgent) InvokeSlash(_ context.Context, name, args string) (SlashResult, error) {
	s.invokedName = name
	s.invokedArgs = args
	return s.res, s.err
}

// TestDispatchSlash_OpensSideAnswerModal pins R-CMD-5: a SlashResult
// with a non-nil ModalAnswer sets m.sideAnswer and doesn't add the
// answer to chat history.
func TestDispatchSlash_OpensSideAnswerModal(t *testing.T) {
	agent := &slashAgent{
		specs: []SlashCommandSpec{{Name: "btw"}},
		res:   SlashResult{ModalAnswer: &SideAnswer{Question: "q?", Answer: "a."}},
	}
	m := NewModel(Options{Agent: agent})
	m.input.SetValue("/btw what now")
	m.viewport.SetWidth(80)

	out, _ := m.dispatchSlash("/btw what now")
	got := out.(Model)
	if got.sideAnswer == nil {
		t.Fatalf("expected sideAnswer to be set")
	}
	if got.sideAnswer.Question != "q?" {
		t.Errorf("question = %q, want %q", got.sideAnswer.Question, "q?")
	}
	if got.history.Len() != 0 {
		t.Errorf("history len = %d, want 0 (modal answer must not land in chat)", got.history.Len())
	}
	if agent.invokedName != "btw" {
		t.Errorf("invoked name = %q, want %q", agent.invokedName, "btw")
	}
	if agent.invokedArgs != "what now" {
		t.Errorf("invoked args = %q, want %q", agent.invokedArgs, "what now")
	}
}

// TestDispatchSlash_AppendsSystemMessage pins that a SlashResult with
// only SystemMessage adds a RoleSystem entry to history.
func TestDispatchSlash_AppendsSystemMessage(t *testing.T) {
	agent := &slashAgent{
		specs: []SlashCommandSpec{{Name: "ping"}},
		res:   SlashResult{SystemMessage: "pong"},
	}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	out, _ := m.dispatchSlash("/ping")
	got := out.(Model)
	entries := got.history.Snapshot()
	if len(entries) != 1 {
		t.Fatalf("history len = %d, want 1", len(entries))
	}
	if entries[0].Role != RoleSystem || entries[0].Text != "pong" {
		t.Errorf("entry = %+v, want RoleSystem 'pong'", entries[0])
	}
}

// TestDispatchSlash_SurfacesError pins that an InvokeSlash error
// renders as a RoleError row (§4.2 error semantics).
func TestDispatchSlash_SurfacesError(t *testing.T) {
	agent := &slashAgent{
		specs: []SlashCommandSpec{{Name: "boom"}},
		err:   errors.New("kaboom"),
	}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	out, _ := m.dispatchSlash("/boom")
	got := out.(Model)
	entries := got.history.Snapshot()
	if len(entries) != 1 || entries[0].Role != RoleError {
		t.Fatalf("expected one RoleError row, got %+v", entries)
	}
}

// TestDispatchSlash_AliasMatches pins that aliases in SlashCommandSpec
// route to the same InvokeSlash call.
func TestDispatchSlash_AliasMatches(t *testing.T) {
	agent := &slashAgent{
		specs: []SlashCommandSpec{
			{Name: "btw", Aliases: []string{"by-the-way"}},
		},
		res: SlashResult{SystemMessage: "matched"},
	}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	out, _ := m.dispatchSlash("/by-the-way hello")
	got := out.(Model)
	if got.history.Len() != 1 {
		t.Errorf("alias dispatch failed: history len = %d, want 1", got.history.Len())
	}
	if agent.invokedName != "by-the-way" {
		t.Errorf("invoked name = %q, want %q", agent.invokedName, "by-the-way")
	}
}

// TestSlashClear_BareEnterConfirms locks in the fix for the
// /clear confirmation bug: the prompt promises "press enter for
// y/yes" but the Enter handler used to short-circuit on empty
// input before reaching the confirmingClear branch — pressing
// Enter quietly did nothing. Now an empty Enter while armed
// counts as the y/yes answer and wipes history.
func TestSlashClear_BareEnterConfirms(t *testing.T) {
	agent := &slashAgent{}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)
	// Seed some content so we can confirm the wipe.
	m.history.Append(Message{Role: RoleUser, Text: "hello"})
	m.history.Append(Message{Role: RoleAssistant, Text: "hi back"})

	// Arm the confirmation.
	out, _ := m.dispatchSlash("/clear")
	m = out.(Model)
	if !m.confirmingClear {
		t.Fatalf("expected confirmingClear=true after /clear")
	}
	// Two existing rows + the "press enter" system prompt = 3.
	if m.history.Len() != 3 {
		t.Fatalf("expected 3 history entries before confirmation, got %d", m.history.Len())
	}

	// Bare Enter (empty input) MUST wipe history.
	enter := tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	out2, _ := m.Update(enter)
	m = out2.(Model)
	if m.confirmingClear {
		t.Errorf("expected confirmingClear=false after bare-Enter confirmation")
	}
	if m.history.Len() != 0 {
		t.Errorf("expected history.Len()==0 after bare-Enter confirm, got %d", m.history.Len())
	}
}

// TestSlashClear_YesConfirms covers the typed-y/yes path so the
// existing accept words still work alongside the new bare-Enter
// shortcut.
func TestSlashClear_YesConfirms(t *testing.T) {
	agent := &slashAgent{}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)
	m.history.Append(Message{Role: RoleUser, Text: "hello"})

	out, _ := m.dispatchSlash("/clear")
	m = out.(Model)

	m.input.SetValue("yes")
	enter := tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	out2, _ := m.Update(enter)
	m = out2.(Model)
	if m.history.Len() != 0 {
		t.Errorf("expected history wiped after typed 'yes', got Len=%d", m.history.Len())
	}
}

// TestSlashClear_OtherTextCancels: any non-y/yes text disarms
// without clearing and leaves a "clear cancelled" system row.
func TestSlashClear_OtherTextCancels(t *testing.T) {
	agent := &slashAgent{}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)
	m.history.Append(Message{Role: RoleUser, Text: "hello"})

	out, _ := m.dispatchSlash("/clear")
	m = out.(Model)
	armedLen := m.history.Len()

	m.input.SetValue("nope")
	enter := tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	out2, _ := m.Update(enter)
	m = out2.(Model)

	if m.confirmingClear {
		t.Errorf("expected confirmingClear=false after cancel")
	}
	// Original entries + arming prompt + "clear cancelled" = armedLen+1.
	if m.history.Len() != armedLen+1 {
		t.Errorf("expected armedLen+1 entries (cancel row added), got %d", m.history.Len())
	}
	last := m.history.Snapshot()[m.history.Len()-1]
	if !strings.Contains(last.Text, "cancelled") {
		t.Errorf("expected 'clear cancelled' system row, got %q", last.Text)
	}
}

// asyncSlashAgent stubs SlashProvider + AsyncSlashProvider so the
// dispatch tests can exercise the non-blocking path (issue #10).
// The channel is buffered + pre-loaded so the goroutine spawned by
// invokeSlashAsync drains immediately and the test doesn't need to
// orchestrate timing.
type asyncSlashAgent struct {
	specs []SlashCommandSpec
	out   chan SlashResultOrErr
	calls int
}

func (a *asyncSlashAgent) Run(_ context.Context, _ string) iter.Seq2[Event, error] {
	return func(_ func(Event, error) bool) {}
}
func (a *asyncSlashAgent) SlashCommands() []SlashCommandSpec { return a.specs }
func (a *asyncSlashAgent) InvokeSlash(_ context.Context, _, _ string) (SlashResult, error) {
	a.calls++
	return SlashResult{}, errors.New("should not be called when AsyncSlashProvider is satisfied")
}
func (a *asyncSlashAgent) InvokeSlashAsync(_ context.Context, _, _ string) <-chan SlashResultOrErr {
	a.calls++
	return a.out
}

func TestDispatchSlash_AsyncPathReturnsCmd(t *testing.T) {
	// AsyncSlashProvider satisfied → dispatch must return a Cmd
	// instead of resolving the result synchronously. The Cmd, when
	// invoked, drains one value from the host's channel.
	ch := make(chan SlashResultOrErr, 1)
	ch <- SlashResultOrErr{Res: SlashResult{ModalAnswer: &SideAnswer{Question: "q?", Answer: "a."}}}
	agent := &asyncSlashAgent{specs: []SlashCommandSpec{{Name: "btw"}}, out: ch}
	m := NewModel(Options{Agent: agent})
	m.input.SetValue("/btw hello")
	m.viewport.SetWidth(80)

	out, cmd := m.dispatchSlash("/btw hello")
	if _, ok := out.(Model); !ok {
		t.Fatalf("expected Model, got %T", out)
	}
	if cmd == nil {
		t.Fatal("expected a Cmd for the async path, got nil")
	}
	// Run the Cmd; it should produce a slashResultMsg carrying the
	// modal answer we pre-loaded.
	msg := cmd()
	r, ok := msg.(slashResultMsg)
	if !ok {
		t.Fatalf("expected slashResultMsg, got %T", msg)
	}
	if r.res.ModalAnswer == nil || r.res.ModalAnswer.Question != "q?" {
		t.Errorf("expected pre-loaded modal answer, got %+v", r.res)
	}
}

func TestDispatchSlash_AsyncPath_RoutesThroughApplySlashResult(t *testing.T) {
	// Once the slashResultMsg lands, Update must route it through
	// applySlashResult so the modal opens / system message appends.
	ch := make(chan SlashResultOrErr, 1)
	ch <- SlashResultOrErr{Res: SlashResult{ModalAnswer: &SideAnswer{Question: "q?", Answer: "a."}}}
	agent := &asyncSlashAgent{specs: []SlashCommandSpec{{Name: "btw"}}, out: ch}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	// Hand-deliver the message (no goroutine race in the test).
	out, _ := m.Update(slashResultMsg{name: "btw", res: SlashResult{ModalAnswer: &SideAnswer{Question: "q?", Answer: "a."}}})
	got := out.(Model)
	if got.sideAnswer == nil {
		t.Fatalf("expected sideAnswer to be set after slashResultMsg")
	}
	if got.sideAnswer.Question != "q?" {
		t.Errorf("question = %q, want %q", got.sideAnswer.Question, "q?")
	}
}

func TestDispatchSlash_SyncFallbackWhenNoAsync(t *testing.T) {
	// A plain SlashProvider (no AsyncSlashProvider) still resolves
	// synchronously — backward compat with existing hosts.
	agent := &slashAgent{
		specs: []SlashCommandSpec{{Name: "btw"}},
		res:   SlashResult{SystemMessage: "ok"},
	}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	out, cmd := m.dispatchSlash("/btw ping")
	if cmd != nil {
		t.Errorf("sync path should return nil Cmd, got %T", cmd)
	}
	got := out.(Model)
	if got.history.Len() == 0 {
		t.Errorf("expected system message appended on sync path")
	}
}

// TestDispatchSlash_UnknownCommand pins that an unknown slash logs a
// helpful system row instead of failing silently or panicking.
func TestDispatchSlash_UnknownCommand(t *testing.T) {
	agent := &slashAgent{specs: []SlashCommandSpec{{Name: "known"}}}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	out, _ := m.dispatchSlash("/nope")
	got := out.(Model)
	if got.history.Len() != 1 {
		t.Fatalf("expected one system row, got %d", got.history.Len())
	}
	if got.history.Snapshot()[0].Role != RoleSystem {
		t.Errorf("unknown command should render as RoleSystem, got %v", got.history.Snapshot()[0].Role)
	}
}
