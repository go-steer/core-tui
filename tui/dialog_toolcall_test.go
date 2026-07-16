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
