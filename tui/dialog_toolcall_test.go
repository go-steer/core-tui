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
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// modelWithTools builds a minimally-plausible Model that has the
// three RoleTool entries in history so the dialog has something to
// walk through. Only fields the dialog reads are populated — the
// rest stay zero-valued.
func modelWithTools(t *testing.T) *Model {
	t.Helper()
	m := Model{}
	m.styles = NewStyles(true, Branding{})
	m.width = 120
	m.height = 40
	m.history.Append(Message{
		Role:            RoleTool,
		ToolName:        "read_file",
		ToolCallID:      "call-1",
		ToolArgsMap:     map[string]any{"path": "a.go"},
		ToolResponseMap: map[string]any{"content": "package a\n"},
	})
	m.history.Append(Message{
		Role:        RoleTool,
		ToolName:    "bash",
		ToolCallID:  "call-2",
		ToolArgsMap: map[string]any{"command": "ls"},
		// Result hasn't arrived — response nil, error empty.
	})
	m.history.Append(Message{
		Role:        RoleTool,
		ToolName:    "grep",
		ToolCallID:  "call-3",
		ToolArgsMap: map[string]any{"pattern": "TODO"},
		ToolError:   "regex compile failed",
	})
	return &m
}

func TestToolCallDialog_OpensOnMostRecent(t *testing.T) {
	m := modelWithTools(t)
	tools := collectToolCalls(m.history.Snapshot())
	d := newToolCallDialog(len(tools))
	if d.idx != len(tools)-1 {
		t.Fatalf("expected idx to start on the most recent tool (%d), got %d",
			len(tools)-1, d.idx)
	}
	out := d.Render(m.width, m)
	if !strings.Contains(out, "3/3") {
		t.Errorf("expected header to show '3/3' on the newest tool, got:\n%s", out)
	}
	if !strings.Contains(out, "grep") {
		t.Errorf("expected tool name 'grep' on the most-recent row, got:\n%s", out)
	}
}

func TestToolCallDialog_LeftRightWalk(t *testing.T) {
	m := modelWithTools(t)
	d := newToolCallDialog(len(collectToolCalls(m.history.Snapshot())))
	act := d.HandleKey("left", m)
	if !act.Consumed || act.Close {
		t.Fatalf("left should be consumed and not close, got %+v", act)
	}
	out := d.Render(m.width, m)
	if !strings.Contains(out, "2/3") || !strings.Contains(out, "bash") {
		t.Errorf("expected to walk back to bash (2/3), got:\n%s", out)
	}
	// One more left → read_file.
	d.HandleKey("left", m)
	out = d.Render(m.width, m)
	if !strings.Contains(out, "1/3") || !strings.Contains(out, "read_file") {
		t.Errorf("expected to walk back to read_file (1/3), got:\n%s", out)
	}
	// Another left at the boundary is a no-op (clamped).
	d.HandleKey("left", m)
	if d.idx != 0 {
		t.Errorf("expected idx clamped to 0 at boundary, got %d", d.idx)
	}
	// Right walks forward.
	d.HandleKey("right", m)
	if d.idx != 1 {
		t.Errorf("expected right to advance to idx=1, got %d", d.idx)
	}
}

func TestToolCallDialog_EscCloses(t *testing.T) {
	m := modelWithTools(t)
	d := newToolCallDialog(len(collectToolCalls(m.history.Snapshot())))
	act := d.HandleKey("esc", m)
	if !act.Close || !act.Consumed {
		t.Errorf("esc should close and consume, got %+v", act)
	}
}

func TestToolCallDialog_HomeEndJump(t *testing.T) {
	m := modelWithTools(t)
	d := newToolCallDialog(len(collectToolCalls(m.history.Snapshot())))
	d.HandleKey("home", m)
	if d.idx != 0 {
		t.Errorf("home should jump to first tool, got idx=%d", d.idx)
	}
	d.HandleKey("end", m)
	if d.idx != 2 {
		t.Errorf("end should jump to last tool, got idx=%d", d.idx)
	}
}

func TestToolCallDialog_ScrollClampsAtTop(t *testing.T) {
	m := modelWithTools(t)
	d := newToolCallDialog(len(collectToolCalls(m.history.Snapshot())))
	d.HandleKey("up", m)
	if d.scroll != 0 {
		t.Errorf("scroll should stay at 0 when already at top, got %d", d.scroll)
	}
	d.HandleKey("down", m)
	if d.scroll != 1 {
		t.Errorf("down should increment scroll, got %d", d.scroll)
	}
}

func TestToolCallDialog_RendersPendingBadge(t *testing.T) {
	m := modelWithTools(t)
	d := newToolCallDialog(len(collectToolCalls(m.history.Snapshot())))
	// Walk back to the bash entry (no response, no error) and
	// verify the header marks it as pending.
	d.HandleKey("left", m)
	out := d.Render(m.width, m)
	if !strings.Contains(out, "(pending)") {
		t.Errorf("expected '(pending)' badge on tool row without result, got:\n%s", out)
	}
}

func TestToolCallDialog_RendersFailedBadge(t *testing.T) {
	m := modelWithTools(t)
	d := newToolCallDialog(len(collectToolCalls(m.history.Snapshot())))
	// Newest is grep with an error; header should show ✘ failed.
	out := d.Render(m.width, m)
	if !strings.Contains(out, "failed") {
		t.Errorf("expected 'failed' badge on errored tool, got:\n%s", out)
	}
	if !strings.Contains(out, "regex compile failed") {
		t.Errorf("expected error message in body, got:\n%s", out)
	}
}

func TestToolCallDialog_EmptyHistoryClosesOnKey(t *testing.T) {
	m := Model{}
	m.styles = NewStyles(true, Branding{})
	m.width = 100
	m.height = 30
	d := newToolCallDialog(0)
	// Any key should close cleanly when there's nothing to show.
	act := d.HandleKey("left", &m)
	if !act.Close {
		t.Errorf("HandleKey on empty history should close the dialog, got %+v", act)
	}
	// Render on empty history should be safe and produce a
	// user-visible "no tool calls" message rather than panic.
	out := d.Render(m.width, &m)
	if !strings.Contains(out, "no tool calls") {
		t.Errorf("expected empty-state hint, got:\n%s", out)
	}
}

func TestCollectToolCalls_FiltersNonToolRoles(t *testing.T) {
	snap := []Message{
		{Role: RoleUser, Text: "hi"},
		{Role: RoleAssistant, Text: "hey"},
		{Role: RoleTool, ToolName: "read_file"},
		{Role: RoleSystem, Text: "ok"},
		{Role: RoleTool, ToolName: "bash"},
	}
	got := collectToolCalls(snap)
	if len(got) != 2 {
		t.Fatalf("expected 2 tool rows, got %d", len(got))
	}
	if got[0].ToolName != "read_file" || got[1].ToolName != "bash" {
		t.Errorf("order not preserved, got %v", []string{got[0].ToolName, got[1].ToolName})
	}
}

// ---------------------------------------------------------------
// Opening on the selected row (issue #233)
// ---------------------------------------------------------------

// interleavedToolModel puts non-tool rows BETWEEN the tool rows on
// purpose. With a transcript of nothing but tool calls the history
// index and the tool-list index are the same number, so a seeding
// bug that used selIdx directly would pass every assertion; here the
// three tool calls sit at history 1, 3 and 5 and map to tool indices
// 0, 1 and 2.
func interleavedToolModel(t *testing.T) Model {
	t.Helper()
	m := NewModel(Options{Agent: &bareAgent{id: "a"}})
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = out.(Model)
	m.history.Append(Message{Role: RoleUser, Text: "read it"})
	m.history.Append(Message{Role: RoleTool, ToolName: "read_file", ToolCallID: "call-1"})
	m.history.Append(Message{Role: RoleAssistant, Text: "done", Rendered: "done"})
	m.history.Append(Message{Role: RoleTool, ToolName: "bash", ToolCallID: "call-2"})
	m.history.Append(Message{Role: RoleAssistant, Text: "and now", Rendered: "and now"})
	m.history.Append(Message{Role: RoleTool, ToolName: "grep", ToolCallID: "call-3"})
	m.refreshViewport()
	return m
}

// openToolCallOverlay presses ctrl+x through the real Update path and
// hands back the dialog it opened.
func openToolCallOverlay(t *testing.T, m Model) *toolCallDialog {
	t.Helper()
	m = press(m, "ctrl+x")
	d, ok := m.overlayStack.Get(toolCallDialogID).(*toolCallDialog)
	if !ok {
		t.Fatal("ctrl+x did not open the tool-call overlay")
	}
	return d
}

// The overlay predates the transcript cursor and opened on the newest
// call regardless of what the operator had selected — so picking a
// row and pressing ctrl+x meant walking back to it with ←, counting
// tool calls on the way. Issue #233.
func TestToolCallOverlay_OpensOnTheSelectedRow(t *testing.T) {
	m := interleavedToolModel(t)
	m.setFocus(focusTranscript)
	m.selIdx = 1 // the read_file row: history 1, tool 0

	d := openToolCallOverlay(t, m)
	if d.idx != 0 {
		t.Fatalf("overlay opened on tool %d, want 0 (the selected read_file row)", d.idx)
	}
	if out := d.Render(m.width, &m); !strings.Contains(out, "1/3") || !strings.Contains(out, "read_file") {
		t.Errorf("expected the header to show read_file as 1/3, got:\n%s", out)
	}

	// The middle row too, so the assertion above isn't satisfied by
	// "always opens on the first call".
	m.selIdx = 3 // bash: history 3, tool 1
	if d := openToolCallOverlay(t, m); d.idx != 1 {
		t.Errorf("overlay opened on tool %d, want 1 (the selected bash row)", d.idx)
	}
}

// The marker is only drawn while the transcript holds the keyboard
// (chatRowMarked), so from the composer selIdx is a position the
// operator cannot see. Aiming the overlay with it would move the
// binding's target for reasons invisible from where they are sitting.
func TestToolCallOverlay_ComposerFocusStillOpensOnTheNewest(t *testing.T) {
	m := interleavedToolModel(t)
	m.selIdx = 1 // a stale cursor from an earlier visit to focus mode
	if m.focus != focusInput {
		t.Fatalf("setup: expected composer focus, got %d", m.focus)
	}
	if d := openToolCallOverlay(t, m); d.idx != 2 {
		t.Errorf("overlay opened on tool %d, want 2 (most recent) — an unseen cursor aimed it", d.idx)
	}
}

// Focus mode with the cursor on prose. There is no tool call to seed
// from, and the fallback is the behaviour the binding has always had.
func TestToolCallOverlay_NonToolSelectionFallsBackToTheNewest(t *testing.T) {
	m := interleavedToolModel(t)
	m.setFocus(focusTranscript)
	m.selIdx = 2 // the assistant row between two tool calls

	if d := openToolCallOverlay(t, m); d.idx != 2 {
		t.Errorf("overlay opened on tool %d, want 2 (most recent)", d.idx)
	}
}

func TestIndexOfToolCall_ReportsAbsenceRatherThanZero(t *testing.T) {
	tools := []Message{{ID: 7}, {ID: 9}}
	if got := indexOfToolCall(tools, 9); got != 1 {
		t.Errorf("indexOfToolCall(9) = %d, want 1", got)
	}
	// -1 rather than 0: a caller that got 0 for "not here" would
	// silently open on the first tool call instead of falling back.
	if got := indexOfToolCall(tools, 8); got != -1 {
		t.Errorf("indexOfToolCall(8) = %d, want -1 for a row that is not a tool call", got)
	}
	if got := indexOfToolCall(nil, 7); got != -1 {
		t.Errorf("indexOfToolCall on an empty list = %d, want -1", got)
	}
}
