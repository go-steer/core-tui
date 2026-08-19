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
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// followModel returns a sized model holding a transcript several
// screens tall, pinned to the tail.
func followModel(t *testing.T, w, h, rows int) model {
	t.Helper()
	m := newModel(Options{Agent: &bareAgent{id: "a"}})
	out, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m = out.(model)
	for i := 0; i < rows; i++ {
		m.history.Append(Message{Role: RoleAssistant, Text: "line " + strconv.Itoa(i), Rendered: "line " + strconv.Itoa(i)})
	}
	m.refreshViewport()
	if !m.chatAtBottom() {
		t.Fatalf("setup: expected the viewport pinned to the tail, YOffset=%d", m.chatYOffset())
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
	m = out.(model)
	if !m.follow {
		t.Error("follow cleared by a resize; it is operator intent and must survive geometry changes")
	}
	if !m.chatAtBottom() {
		t.Errorf("viewport left off the tail by the resize itself, YOffset=%d", m.chatYOffset())
	}

	// The next chunk lands.
	m.history.Append(Message{Role: RoleAssistant, Text: "fresh chunk", Rendered: "fresh chunk"})
	m.refreshViewport()
	if !m.chatAtBottom() {
		t.Errorf("follow lost: new content appended below the visible region, YOffset=%d", m.chatYOffset())
	}

	// Growing back is equally fine.
	out, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 60})
	m = out.(model)
	m.history.Append(Message{Role: RoleAssistant, Text: "another", Rendered: "another"})
	m.refreshViewport()
	if !m.chatAtBottom() {
		t.Errorf("follow lost across a grow, YOffset=%d", m.chatYOffset())
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
		m = out.(model)
	}
	if m.input.Height() < 2 {
		t.Fatalf("setup: expected the textarea to have grown, height=%d", m.input.Height())
	}
	if !m.follow {
		t.Error("follow cleared by the textarea growing a line")
	}
	m.history.Append(Message{Role: RoleAssistant, Text: "fresh chunk", Rendered: "fresh chunk"})
	m.refreshViewport()
	if !m.chatAtBottom() {
		t.Errorf("follow lost while typing, YOffset=%d", m.chatYOffset())
	}
}

// Scrolling up is still what releases follow, and scrolling back to
// the bottom re-arms it. This is the behavior the AtBottom() sample
// used to provide and that the flag has to keep providing.
func TestFollow_ScrollUpReleasesAndBottomReArms(t *testing.T) {
	m := followModel(t, 100, 40, 200)

	out, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	m = out.(model)
	if m.follow {
		t.Fatal("PgUp did not release follow")
	}
	before := m.chatYOffset()
	m.history.Append(Message{Role: RoleAssistant, Text: "fresh chunk", Rendered: "fresh chunk"})
	m.refreshViewport()
	if m.chatYOffset() != before {
		t.Errorf("operator reading backlog was yanked to the tail: YOffset %d → %d", before, m.chatYOffset())
	}

	// Page back down to the bottom: follow re-arms and the next
	// chunk pins again.
	for i := 0; i < 10 && !m.chatAtBottom(); i++ {
		out, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}))
		m = out.(model)
	}
	if !m.follow {
		t.Fatalf("scrolling back to the bottom did not re-arm follow (AtBottom=%v)", m.chatAtBottom())
	}
	m.history.Append(Message{Role: RoleAssistant, Text: "newer", Rendered: "newer"})
	m.refreshViewport()
	if !m.chatAtBottom() {
		t.Errorf("re-armed follow did not pin the next chunk, YOffset=%d", m.chatYOffset())
	}
}

// A resize while the operator is reading backlog must not silently
// re-arm follow either — the flag has to hold in both directions.
func TestFollow_ResizeDoesNotReArmWhileScrolledUp(t *testing.T) {
	m := followModel(t, 100, 40, 200)

	out, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	m = out.(model)
	if m.follow {
		t.Fatal("setup: PgUp did not release follow")
	}
	out, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = out.(model)
	if m.follow {
		t.Error("resize re-armed follow behind the operator's back")
	}
	m.history.Append(Message{Role: RoleAssistant, Text: "fresh chunk", Rendered: "fresh chunk"})
	m.refreshViewport()
	if m.chatAtBottom() {
		t.Error("new content jumped the operator to the tail after a resize")
	}
}

// Operator-initiated jumps own the flag outright: submitting a turn
// (and any refreshAndScroll path) re-arms, ctrl+l's jump to the top
// releases.
func TestFollow_ExplicitJumpsSetTheFlag(t *testing.T) {
	m := followModel(t, 100, 40, 200)

	out, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	m = out.(model)
	if m.follow {
		t.Fatal("setup: PgUp did not release follow")
	}
	m.refreshAndScroll()
	if !m.follow {
		t.Error("refreshAndScroll jumped to the tail without re-arming follow")
	}

	out, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'l', Mod: tea.ModCtrl}))
	m = out.(model)
	if m.follow {
		t.Error("ctrl+l jumped to the top but left follow armed — the next repaint would drag the operator back down")
	}
	m.history.Append(Message{Role: RoleAssistant, Text: "fresh chunk", Rendered: "fresh chunk"})
	m.refreshViewport()
	if m.chatYOffset() != 0 {
		t.Errorf("after ctrl+l the next repaint moved the viewport to YOffset=%d, want 0", m.chatYOffset())
	}
}

// Issue #113: `end` is ctrl+l's counterpart — it jumps to the tail
// AND re-arms follow. The flag is the point: a jump that moved the
// viewport without setting m.follow would be undone by the next
// repaint, which is exactly the state PgDn-until-AtBottom already
// gets you out of.
func TestFollow_EndJumpsToTailAndReArms(t *testing.T) {
	m := followModel(t, 100, 40, 200)

	out, _ := m.Update(keyPress("pgup"))
	m = out.(model)
	if m.follow {
		t.Fatal("setup: PgUp did not release follow")
	}
	if m.chatAtBottom() {
		t.Fatal("setup: PgUp did not move the viewport off the tail")
	}

	out, cmd := m.Update(keyPress("end"))
	m = out.(model)
	if !m.follow {
		t.Error("end jumped to the tail but left follow released — the next repaint would drag the operator back off it")
	}
	if !m.chatAtBottom() {
		t.Errorf("end did not reach the tail, YOffset=%d", m.chatYOffset())
	}
	if cmd != nil {
		t.Error("end returned a command; a pure scroll should not schedule work")
	}

	// The re-armed flag survives into the next chunk.
	m.history.Append(Message{Role: RoleAssistant, Text: "fresh chunk", Rendered: "fresh chunk"})
	m.refreshViewport()
	if !m.chatAtBottom() {
		t.Errorf("follow re-armed by end did not pin the next chunk, YOffset=%d", m.chatYOffset())
	}
}

// end must not disturb the input: it is a viewport key, and the
// textarea's content has to survive the keystroke untouched.
func TestFollow_EndLeavesTheInputAlone(t *testing.T) {
	m := followModel(t, 100, 40, 200)
	m.input.SetValue("half-typed prompt")

	out, _ := m.Update(keyPress("end"))
	m = out.(model)
	if got := m.input.Value(); got != "half-typed prompt" {
		t.Errorf("end mutated the input: %q", got)
	}
}

// end is claimed for goto-bottom ONLY while the input is empty.
// "end goes to end-of-line" is too strong a convention to shadow in a
// box the operator is composing in — the more so once syncInputHeight
// grows that box to textareaMaxHeight rows. With text in the input the
// key must reach the textarea instead, leaving the transcript where it
// was and follow released.
func TestFollow_EndYieldsToTheInputWhenComposing(t *testing.T) {
	m := followModel(t, 100, 40, 200)
	m.chatGotoTop()
	m.follow = false
	m.input.SetValue("a half-typed prompt")
	before := m.chatYOffset()

	out, _ := m.Update(keyPress("end"))
	m = out.(model)

	if m.follow {
		t.Error("end re-armed follow while the operator was composing; want the textarea to own the key")
	}
	if got := m.chatYOffset(); got != before {
		t.Errorf("end scrolled the transcript while composing: YOffset %d -> %d", before, got)
	}
}

// ...and the empty-input case still reaches the chat, so the
// conditional does not quietly disable the binding.
func TestFollow_EndClaimedWhenInputEmpty(t *testing.T) {
	m := followModel(t, 100, 40, 200)
	m.chatGotoTop()
	m.follow = false
	m.input.SetValue("")

	out, _ := m.Update(keyPress("end"))
	m = out.(model)

	if !m.follow {
		t.Error("end did not re-arm follow with an empty input")
	}
	if !m.chatAtBottom() {
		t.Errorf("end did not reach the tail: YOffset=%d", m.chatYOffset())
	}
}

// Regression guard for the two shadows issue #113 explicitly refuses
// to give up: binding `end` must not tempt anyone into handing
// ctrl+d / ctrl+u back to the viewport's half-page scroll bindings.
func TestFollow_EndDoesNotUnshadowQuitOrKillLine(t *testing.T) {
	t.Run("ctrl+d still quits", func(t *testing.T) {
		m := followModel(t, 100, 40, 200)
		out, cmd := m.Update(keyPress("ctrl+d"))
		m = out.(model)
		if !m.quitting {
			t.Error("ctrl+d did not set quitting — it scrolled instead of quitting")
		}
		if cmd == nil {
			t.Fatal("ctrl+d returned no command, want tea.Quit")
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("ctrl+d command produced %T, want tea.QuitMsg", cmd())
		}
	})

	t.Run("ctrl+u still kills the line", func(t *testing.T) {
		m := followModel(t, 100, 40, 200)
		m.input.SetValue("text to kill")
		m.historyCursor = 3
		out, _ := m.Update(keyPress("ctrl+u"))
		m = out.(model)
		if got := m.input.Value(); got != "" {
			t.Errorf("ctrl+u left %q in the input — it scrolled instead of clearing", got)
		}
		if m.historyCursor != -1 {
			t.Errorf("ctrl+u left historyCursor=%d, want -1", m.historyCursor)
		}
	})
}

// The help panel must not advertise keys that do nothing. Before
// #113 the Navigation section listed "home / end", neither of which
// was bound anywhere — the same class of lie as the unbound ctrl+o
// marker fixed in #94. This walks the rendered rows and presses every
// key the section names, asserting each one actually moves the
// viewport.
func TestHelpPanel_NavigationKeysAreAllBound(t *testing.T) {
	m := followModel(t, 100, 40, 200)
	m.helpOpen = true

	keys := helpPanelSectionKeys(t, m, "Navigation")
	if len(keys) == 0 {
		t.Fatal("no Navigation keys parsed out of the help panel")
	}

	for _, stroke := range keys {
		t.Run(stroke, func(t *testing.T) {
			// Park mid-transcript so a key can prove itself by
			// moving in either direction.
			probe := followModel(t, 100, 40, 200)
			probe.chatSetYOffset(probe.chatYOffset() / 2)
			before := probe.chatYOffset()

			out, _ := probe.Update(keyPress(stroke))
			probe = out.(model)
			if probe.chatYOffset() == before {
				t.Errorf("help panel advertises %q under Navigation but it left the viewport at YOffset=%d", stroke, before)
			}
		})
	}
}

// helpPanelSectionKeys renders the help panel and returns the
// individual key strokes named in the given section, splitting rows
// like "pgup / pgdn" into their parts.
func helpPanelSectionKeys(t *testing.T, m model, section string) []string {
	t.Helper()

	panel := ansi.Strip(m.renderHelpPanel(100))
	if panel == "" {
		t.Fatal("renderHelpPanel returned empty; helpOpen not set?")
	}

	var (
		keys   []string
		inside bool
	)
	for _, line := range strings.Split(panel, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == section:
			inside = true
			continue
		case !inside:
			continue
		case trimmed == "":
			// Blank line closes the section.
			inside = false
			continue
		}
		// Rows are "    <key><padding><description>"; the key column
		// is everything up to the first run of two or more spaces.
		fields := strings.SplitN(trimmed, "  ", 2)
		for _, part := range strings.Split(fields[0], "/") {
			if part = strings.TrimSpace(part); part != "" {
				keys = append(keys, part)
			}
		}
	}
	return keys
}
