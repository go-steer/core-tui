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
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Issue #303 — a host /cmd reply pins the tail, the same way
// slash_builtin.go's header promises for the built-ins. The stubs
// these tests drive (slashAgent, asyncSlashAgent) live in
// slash_test.go alongside the dispatch tests they were written for.

// scrolledUpModel builds a model with enough backlog to scroll in,
// sized to a real window, and leaves the operator ten lines up from
// the tail with follow dropped.
//
// That last part is the whole precondition: from the tail follow is
// still armed and refreshViewport re-pins on its own, so the bug is
// invisible there. It only shows once the operator has scrolled up to
// read something — which is also the moment a slash reply landing
// off-screen costs the most.
func scrolledUpModel(t *testing.T, agent Agent) model {
	t.Helper()
	m := newModel(Options{Agent: agent})
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = out.(model)
	for i := 0; i < 20; i++ {
		q := "what does this function do?"
		m.history.Append(Message{Role: RoleUser, Text: q, Rendered: q})
		a := strings.Repeat("It reads the config, validates it, and returns a handle. ", 4)
		m.history.Append(Message{Role: RoleAssistant, Text: a, Rendered: a})
	}
	m.follow = true
	m.refreshViewport()

	m.chatScrollBy(-10)
	m.syncFollow()
	if m.chatAtBottom() {
		t.Fatal("precondition: still pinned to the tail after scrolling up")
	}
	return m
}

// longReply is tall enough that landing it off-screen is what the
// operator actually reported — core-agent's /usage table.
func longReply() string {
	return strings.TrimRight(strings.Repeat("cache hit 91% · $0.0021 in / $0.0140 out\n", 20), "\n")
}

// TestApplySlashResult_SystemMessagePinsTail is the issue #303
// regression. Before the fix the reply was appended and the window
// never moved, so the operator saw nothing happen at all.
func TestApplySlashResult_SystemMessagePinsTail(t *testing.T) {
	agent := &slashAgent{
		specs: []SlashCommandSpec{{Name: "usage"}},
		res:   SlashResult{SystemMessage: longReply()},
	}
	m := scrolledUpModel(t, agent)

	got, _ := submitSlash(t, m, "/usage")
	if !got.chatAtBottom() {
		t.Error("host /cmd reply did not scroll into view — the operator sees nothing happen")
	}
	if !got.follow {
		t.Error("follow not re-armed, so the next repaint would drift off the tail again")
	}
}

// TestApplySlashResult_ErrorPinsTail — a failure the operator asked
// for is the row they most need to see.
func TestApplySlashResult_ErrorPinsTail(t *testing.T) {
	agent := &slashAgent{
		specs: []SlashCommandSpec{{Name: "usage"}},
		err:   errors.New("daemon unreachable"),
	}
	m := scrolledUpModel(t, agent)

	got, _ := submitSlash(t, m, "/usage")
	if !got.chatAtBottom() {
		t.Error("host /cmd error row did not scroll into view")
	}
}

// TestApplySlashResult_ModalAnswerOnlyKeepsScroll is the other half of
// the rule. A ModalAnswer with no SystemMessage puts nothing in the
// transcript, so there is nothing at the tail to show; moving the chat
// behind an open modal would be a jump nothing on screen accounts for.
func TestApplySlashResult_ModalAnswerOnlyKeepsScroll(t *testing.T) {
	agent := &slashAgent{
		specs: []SlashCommandSpec{{Name: "btw"}},
		res:   SlashResult{ModalAnswer: &SideAnswer{Question: "q?", Answer: "a."}},
	}
	m := scrolledUpModel(t, agent)
	wantIdx, wantLine := m.viewport.Offset()

	got, _ := submitSlash(t, m, "/btw hello")
	if idx, line := got.viewport.Offset(); idx != wantIdx || line != wantLine {
		t.Errorf("chat scrolled to (%d, %d) behind the modal, want it left at (%d, %d)",
			idx, line, wantIdx, wantLine)
	}
	if got.chatAtBottom() {
		t.Error("chat jumped to the tail for a reply that never entered the transcript")
	}
}

// TestApplySlashDispatch_AsyncPreamblePinsTail — the preamble (issue
// #16) exists to be read while the host works. Off-screen it is worth
// less than the toast it supplements.
func TestApplySlashDispatch_AsyncPreamblePinsTail(t *testing.T) {
	agent := &asyncSlashAgent{
		specs:    []SlashCommandSpec{{Name: "compact"}},
		preamble: "compacting…",
		out:      make(chan SlashResultOrErr, 1),
	}
	m := scrolledUpModel(t, agent)

	got, _ := submitSlash(t, m, "/compact")
	if !got.chatAtBottom() {
		t.Error("async preamble did not scroll into view")
	}
}

// TestApplySlashDispatch_UnknownCommandPinsTail — an answer the
// operator can't see reads as a command that silently did nothing,
// and the command gets retyped.
func TestApplySlashDispatch_UnknownCommandPinsTail(t *testing.T) {
	agent := &slashAgent{specs: []SlashCommandSpec{{Name: "usage"}}}
	m := scrolledUpModel(t, agent)

	got, _ := submitSlash(t, m, "/nope")
	if !got.chatAtBottom() {
		t.Error("unknown-command row did not scroll into view")
	}
}

// TestDispatchSlash_NoSlashSurfacePinsTail — the same row, reached on
// the early return in dispatchSlash rather than through the match Cmd,
// for a host with no slash surface at all.
func TestDispatchSlash_NoSlashSurfacePinsTail(t *testing.T) {
	m := scrolledUpModel(t, &bareAgent{id: "bare"})

	out, cmd := m.dispatchSlash("/usage")
	got := out.(model)
	if cmd != nil {
		t.Errorf("nothing to ask, but got a follow-up %T", cmd)
	}
	if !got.chatAtBottom() {
		t.Error("no-slash-surface row did not scroll into view")
	}
}
