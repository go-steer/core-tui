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
	"strconv"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// followModel returns a sized model holding a transcript several
// screens tall, pinned to the tail.
func followModel(t *testing.T, w, h, rows int) Model {
	t.Helper()
	m := NewModel(Options{Agent: &bareAgent{id: "a"}})
	out, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m = out.(Model)
	for i := 0; i < rows; i++ {
		m.history.Append(Message{Role: RoleAssistant, Text: "line " + strconv.Itoa(i), Rendered: "line " + strconv.Itoa(i)})
	}
	m.refreshViewport()
	if !m.viewport.AtBottom() {
		t.Fatalf("setup: expected the viewport pinned to the tail, YOffset=%d", m.viewport.YOffset())
	}
	return m
}

// Issue #93: a resize mid-stream must not drop follow. The old code
// inferred follow from viewport.AtBottom() inside refreshViewport,
// sampled AFTER resize() had already applied the new height — a
// shrunken viewport reports "not at bottom" with YOffset unchanged,
// so the re-pin was skipped and the stream scrolled away underneath
// the operator for the rest of the turn.
func TestFollow_SurvivesWindowResizeMidStream(t *testing.T) {
	m := followModel(t, 100, 40, 200)

	// The terminal loses a row while the turn is streaming.
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 39})
	m = out.(Model)
	if !m.follow {
		t.Error("follow cleared by a resize; it is operator intent and must survive geometry changes")
	}
	if !m.viewport.AtBottom() {
		t.Errorf("viewport left off the tail by the resize itself, YOffset=%d", m.viewport.YOffset())
	}

	// The next chunk lands.
	m.history.Append(Message{Role: RoleAssistant, Text: "fresh chunk", Rendered: "fresh chunk"})
	m.refreshViewport()
	if !m.viewport.AtBottom() {
		t.Errorf("follow lost: new content appended below the visible region, YOffset=%d", m.viewport.YOffset())
	}

	// Growing back is equally fine.
	out, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 60})
	m = out.(Model)
	m.history.Append(Message{Role: RoleAssistant, Text: "another", Rendered: "another"})
	m.refreshViewport()
	if !m.viewport.AtBottom() {
		t.Errorf("follow lost across a grow, YOffset=%d", m.viewport.YOffset())
	}
}

// Typing enough to wrap the textarea onto a second line shrinks the
// viewport through the same resize path — the other everyday trigger
// named in the issue.
func TestFollow_SurvivesTextareaGrowth(t *testing.T) {
	m := followModel(t, 60, 40, 200)

	for i := 0; i < 200; i++ {
		key := tea.KeyPressMsg(tea.Key{Code: 'x', Text: "x"})
		out, _ := m.Update(key)
		m = out.(Model)
	}
	if m.input.Height() < 2 {
		t.Fatalf("setup: expected the textarea to have grown, height=%d", m.input.Height())
	}
	if !m.follow {
		t.Error("follow cleared by the textarea growing a line")
	}
	m.history.Append(Message{Role: RoleAssistant, Text: "fresh chunk", Rendered: "fresh chunk"})
	m.refreshViewport()
	if !m.viewport.AtBottom() {
		t.Errorf("follow lost while typing, YOffset=%d", m.viewport.YOffset())
	}
}

// Scrolling up is still what releases follow, and scrolling back to
// the bottom re-arms it. This is the behavior the AtBottom() sample
// used to provide and that the flag has to keep providing.
func TestFollow_ScrollUpReleasesAndBottomReArms(t *testing.T) {
	m := followModel(t, 100, 40, 200)

	out, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	m = out.(Model)
	if m.follow {
		t.Fatal("PgUp did not release follow")
	}
	before := m.viewport.YOffset()
	m.history.Append(Message{Role: RoleAssistant, Text: "fresh chunk", Rendered: "fresh chunk"})
	m.refreshViewport()
	if m.viewport.YOffset() != before {
		t.Errorf("operator reading backlog was yanked to the tail: YOffset %d → %d", before, m.viewport.YOffset())
	}

	// Page back down to the bottom: follow re-arms and the next
	// chunk pins again.
	for i := 0; i < 10 && !m.viewport.AtBottom(); i++ {
		out, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}))
		m = out.(Model)
	}
	if !m.follow {
		t.Fatalf("scrolling back to the bottom did not re-arm follow (AtBottom=%v)", m.viewport.AtBottom())
	}
	m.history.Append(Message{Role: RoleAssistant, Text: "newer", Rendered: "newer"})
	m.refreshViewport()
	if !m.viewport.AtBottom() {
		t.Errorf("re-armed follow did not pin the next chunk, YOffset=%d", m.viewport.YOffset())
	}
}

// A resize while the operator is reading backlog must not silently
// re-arm follow either — the flag has to hold in both directions.
func TestFollow_ResizeDoesNotReArmWhileScrolledUp(t *testing.T) {
	m := followModel(t, 100, 40, 200)

	out, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	m = out.(Model)
	if m.follow {
		t.Fatal("setup: PgUp did not release follow")
	}
	out, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = out.(Model)
	if m.follow {
		t.Error("resize re-armed follow behind the operator's back")
	}
	m.history.Append(Message{Role: RoleAssistant, Text: "fresh chunk", Rendered: "fresh chunk"})
	m.refreshViewport()
	if m.viewport.AtBottom() {
		t.Error("new content jumped the operator to the tail after a resize")
	}
}

// Operator-initiated jumps own the flag outright: submitting a turn
// (and any refreshAndScroll path) re-arms, ctrl+l's jump to the top
// releases.
func TestFollow_ExplicitJumpsSetTheFlag(t *testing.T) {
	m := followModel(t, 100, 40, 200)

	out, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	m = out.(Model)
	if m.follow {
		t.Fatal("setup: PgUp did not release follow")
	}
	m.refreshAndScroll()
	if !m.follow {
		t.Error("refreshAndScroll jumped to the tail without re-arming follow")
	}

	out, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'l', Mod: tea.ModCtrl}))
	m = out.(Model)
	if m.follow {
		t.Error("ctrl+l jumped to the top but left follow armed — the next repaint would drag the operator back down")
	}
	m.history.Append(Message{Role: RoleAssistant, Text: "fresh chunk", Rendered: "fresh chunk"})
	m.refreshViewport()
	if m.viewport.YOffset() != 0 {
		t.Errorf("after ctrl+l the next repaint moved the viewport to YOffset=%d, want 0", m.viewport.YOffset())
	}
}
