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
	"iter"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// TestEmitEvent_TextOnly pins that a Text event produces exactly one
// streamChunkMsg with the right partial flag.
func TestEmitEvent_TextOnly(t *testing.T) {
	ch := make(chan tea.Msg, 4)
	emitEvent(context.Background(), ch, 0, Event{Text: "hello", Partial: true})
	got := drain(ch)
	if len(got) != 1 {
		t.Fatalf("expected 1 msg, got %d", len(got))
	}
	chunk, ok := got[0].(streamChunkMsg)
	if !ok {
		t.Fatalf("expected streamChunkMsg, got %T", got[0])
	}
	if chunk.text != "hello" || !chunk.partial {
		t.Errorf("chunk = %+v, want {hello true}", chunk)
	}
}

// TestEmitEvent_MultiFanOut pins that a multi-field Event fans out to
// one msg per field (text + tool call + usage = 3 msgs).
func TestEmitEvent_MultiFanOut(t *testing.T) {
	ch := make(chan tea.Msg, 8)
	emitEvent(context.Background(), ch, 0, Event{
		Text:      "explaining",
		Partial:   true,
		ToolCalls: []ToolCall{{ID: "t1", Name: "Read"}},
		Usage:     &Usage{InputTokens: 100, OutputTokens: 50},
	})
	got := drain(ch)
	if len(got) != 3 {
		t.Fatalf("expected 3 msgs, got %d (%v)", len(got), got)
	}
}

// TestApplyStreamChunk_AccumulatesPartials pins that successive
// partial chunks concatenate into the in-progress buffer; a non-
// partial chunk overwrites (some agents echo the full text at end).
func TestApplyStreamChunk_AccumulatesPartials(t *testing.T) {
	m := NewModel(Options{})
	m.applyStreamChunk(streamChunkMsg{text: "hel", partial: true})
	m.applyStreamChunk(streamChunkMsg{text: "lo", partial: true})
	if m.inProgressText != "hello" {
		t.Errorf("after partials: inProgressText = %q, want %q", m.inProgressText, "hello")
	}
	m.applyStreamChunk(streamChunkMsg{text: "FULL", partial: false})
	if m.inProgressText != "FULL" {
		t.Errorf("after non-partial: inProgressText = %q, want %q", m.inProgressText, "FULL")
	}
}

// TestApplyToolCall_DedupsByID pins R-CHAT-5: the same tool call ID
// arriving twice (partial + committed echo) renders as one history
// entry, not two.
func TestApplyToolCall_DedupsByID(t *testing.T) {
	m := NewModel(Options{})
	m.applyToolCall(toolCallMsg{id: "x", name: "Read"})
	m.applyToolCall(toolCallMsg{id: "x", name: "Read"})
	got := 0
	for _, msg := range m.history.Snapshot() {
		if msg.Role == RoleTool {
			got++
		}
	}
	if got != 1 {
		t.Errorf("dedup failed: %d tool rows, want 1", got)
	}
}

// TestApplyToolCall_FlipsToolActive pins R-CHAT-3: a ToolCall flips
// the spinner into the working pool; the next stream chunk flips back.
func TestApplyToolCall_FlipsToolActive(t *testing.T) {
	m := NewModel(Options{})
	if m.toolActive {
		t.Fatalf("expected toolActive=false initially")
	}
	m.applyToolCall(toolCallMsg{id: "x", name: "Read"})
	if !m.toolActive {
		t.Errorf("toolActive=false after toolCall; want true")
	}
	m.applyStreamChunk(streamChunkMsg{text: "next", partial: true})
	if m.toolActive {
		t.Errorf("toolActive=true after stream chunk; want false")
	}
}

// TestFinalizeTurn_AppendsAssistantWithMetadata pins that turn-end
// flushes the in-progress buffer into a finalized assistant Message
// carrying Usage / Model / Elapsed / Rendered.
func TestFinalizeTurn_AppendsAssistantWithMetadata(t *testing.T) {
	m := NewModel(Options{})
	m.state = stateStreaming
	m.inProgressText = "hello"
	m.currentUsage = &Usage{InputTokens: 10, OutputTokens: 20}
	m.currentModel = "Claude Sonnet 4.6"
	m.viewport.SetWidth(80)

	m.finalizeTurn(2*time.Second, "")

	if m.state != stateIdle {
		t.Errorf("state = %v, want stateIdle", m.state)
	}
	entries := m.history.Snapshot()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	got := entries[0]
	if got.Role != RoleAssistant {
		t.Errorf("role = %v, want RoleAssistant", got.Role)
	}
	if got.Text != "hello" {
		t.Errorf("text = %q, want %q", got.Text, "hello")
	}
	if got.Usage == nil || got.Usage.InputTokens != 10 {
		t.Errorf("usage = %+v, want input=10", got.Usage)
	}
	if got.Elapsed != 2*time.Second {
		t.Errorf("elapsed = %v, want 2s", got.Elapsed)
	}
}

// TestFinalizeTurn_InterruptedNotice pins that "(interrupted)" lands
// as a system message, not as an error banner — distinct visual
// treatment per §4.2.
func TestFinalizeTurn_InterruptedNotice(t *testing.T) {
	m := NewModel(Options{})
	m.state = stateStreaming
	m.viewport.SetWidth(80)
	m.finalizeTurn(0, "(interrupted)")
	entries := m.history.Snapshot()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Role != RoleSystem {
		t.Errorf("(interrupted) role = %v, want RoleSystem", entries[0].Role)
	}
}

// TestMaybeDrainQueue_EmptyKeepsListener pins that draining an empty
// queue is a no-op + the event listener Cmd is re-issued so the
// stream loop keeps running for the next turn.
func TestMaybeDrainQueue_EmptyKeepsListener(t *testing.T) {
	m := NewModel(Options{})
	model, cmd := m.maybeDrainQueue()
	if cmd == nil {
		t.Errorf("expected non-nil eventListener Cmd")
	}
	got := model.(Model)
	if got.state != stateIdle {
		t.Errorf("state = %v, want stateIdle", got.state)
	}
	if len(got.queue) != 0 {
		t.Errorf("queue = %d, want 0", len(got.queue))
	}
}

// TestMaybeDrainQueue_PopsHeadAndStartsTurn pins R-CHAT-10: when the
// queue has a Queued entry, draining marks it InFlight and starts a
// new streaming turn. The entry stays in the queue panel (with the
// new state glyph) instead of being popped — finalizeTurn flips it
// to Done / Failed when its turn completes.
func TestMaybeDrainQueue_PopsHeadAndStartsTurn(t *testing.T) {
	m := NewModel(Options{Agent: stubAgent{}})
	m.queue = []QueueEntry{
		{Text: "first queued", State: QueueQueued},
		{Text: "second queued", State: QueueQueued},
	}
	model, cmd := m.maybeDrainQueue()
	if cmd == nil {
		t.Errorf("expected non-nil Cmd batch")
	}
	got := model.(Model)
	if got.state != stateStreaming {
		t.Errorf("state = %v, want stateStreaming", got.state)
	}
	if len(got.queue) != 2 {
		t.Fatalf("queue len after drain = %d, want 2 (entries stay, only state flips)", len(got.queue))
	}
	if got.queue[0].State != QueueInFlight {
		t.Errorf("queue[0].State = %v, want InFlight", got.queue[0].State)
	}
	if got.queue[1].State != QueueQueued {
		t.Errorf("queue[1].State = %v, want Queued", got.queue[1].State)
	}
	got.cancelTurn() // clean up the goroutine
}

// stubAgent yields no events — used to start a turn without spinning
// up real streaming for unit tests.
type stubAgent struct{}

func (stubAgent) Run(_ context.Context, _ string) iter.Seq2[Event, error] {
	return func(_ func(Event, error) bool) {}
}

// drain returns every message currently buffered in ch, draining the
// channel non-blockingly.
func drain(ch <-chan tea.Msg) []tea.Msg {
	var out []tea.Msg
	for {
		select {
		case m := <-ch:
			out = append(out, m)
		default:
			return out
		}
	}
}
