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
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// findInitialPromptCmd walks the tea.Cmd tree returned by Init and
// returns the first initialPromptMsg it finds (or nil). Init emits a
// tea.Batch that mixes long-lived listener cmds (blocking on
// channels) with the one-shot initialPromptMsg cmd; we invoke each
// child in a goroutine and race a short timeout so the blocking
// listeners don't stall the test.
func findInitialPromptCmd(t *testing.T, cmd tea.Cmd) *initialPromptMsg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		if ip, ok := msg.(initialPromptMsg); ok {
			return &ip
		}
		return nil
	}
	found := make(chan initialPromptMsg, len(batch))
	for _, child := range batch {
		if child == nil {
			continue
		}
		go func(c tea.Cmd) {
			out := c()
			if ip, ok := out.(initialPromptMsg); ok {
				found <- ip
			}
		}(child)
	}
	select {
	case ip := <-found:
		return &ip
	case <-time.After(200 * time.Millisecond):
		return nil
	}
}

func TestInit_InitialPrompt_EmitsMsgWhenSet(t *testing.T) {
	m := NewModel(Options{
		Agent:         stubAgent{},
		InitialPrompt: "audit this repo for races",
	})
	got := findInitialPromptCmd(t, m.Init())
	if got == nil {
		t.Fatal("Init did not emit initialPromptMsg for a non-empty InitialPrompt")
	}
	if got.text != "audit this repo for races" {
		t.Errorf("initialPromptMsg.text = %q, want %q", got.text, "audit this repo for races")
	}
}

func TestInit_InitialPrompt_SkippedWhenEmpty(t *testing.T) {
	m := NewModel(Options{Agent: stubAgent{}})
	if got := findInitialPromptCmd(t, m.Init()); got != nil {
		t.Errorf("Init should not emit initialPromptMsg when InitialPrompt is empty, got %+v", got)
	}
}

func TestUpdate_InitialPromptMsg_SubmitsTurn(t *testing.T) {
	m := NewModel(Options{Agent: stubAgent{}})
	m.viewport.SetWidth(80)

	next, _ := m.Update(initialPromptMsg{text: "hello world"})
	nm := next.(Model)

	entries := nm.history.Snapshot()
	if len(entries) == 0 {
		t.Fatal("expected a history entry after initialPromptMsg")
	}
	// submitTurn appends a RoleUser row with the raw text.
	first := entries[0]
	if first.Role != RoleUser {
		t.Errorf("expected first history entry Role=RoleUser, got %v", first.Role)
	}
	if first.Text != "hello world" {
		t.Errorf("expected first entry Text=%q, got %q", "hello world", first.Text)
	}
	if nm.state != stateStreaming {
		t.Errorf("expected state=stateStreaming after seed submit, got %v", nm.state)
	}
}

func TestUpdate_InitialPromptMsg_EmptyIsNoOp(t *testing.T) {
	m := NewModel(Options{Agent: stubAgent{}})
	m.viewport.SetWidth(80)

	next, cmd := m.Update(initialPromptMsg{text: "   "})
	nm := next.(Model)

	if entries := nm.history.Snapshot(); len(entries) != 0 {
		t.Errorf("empty InitialPrompt should not append history, got %d entries", len(entries))
	}
	if cmd != nil {
		t.Errorf("empty InitialPrompt should not return a Cmd, got %T", cmd())
	}
}

func TestUpdate_InitialPromptMsg_SlashCommandRejected(t *testing.T) {
	m := NewModel(Options{Agent: stubAgent{}})
	m.viewport.SetWidth(80)

	next, _ := m.Update(initialPromptMsg{text: "/help"})
	nm := next.(Model)

	entries := nm.history.Snapshot()
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 history entry (the error row), got %d", len(entries))
	}
	if entries[0].Role != RoleError {
		t.Errorf("expected slash rejection to append RoleError, got %v", entries[0].Role)
	}
	if nm.state == stateStreaming {
		t.Errorf("slash-prefixed InitialPrompt should not have started a turn")
	}
}
