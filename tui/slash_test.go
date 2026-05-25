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
	"testing"
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
