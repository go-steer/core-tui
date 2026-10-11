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
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// tasksFocusModel is a sized model on a SubagentReporter host whose
// bar is showing subs, so enter on a row has an overlay to open.
func tasksFocusModel(t *testing.T, subs ...SubagentInfo) (*model, *time.Time) {
	t.Helper()
	turns := make(map[string][]SubagentEvent, len(subs))
	for _, s := range subs {
		turns[s.Name] = []SubagentEvent{{Seq: 1, Text: s.Name + " says hi"}}
	}
	m := newModel(Options{
		Agent:        &subagentAgent{roster: subs, turns: turns},
		StatusLayout: StatusHeader,
	})
	m.styles = newStylesWithTheme(true, goldenTheme())
	now := tasksEpoch
	m.now = func() time.Time { return now }
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return landRoster(t, out.(*model), subs...), &now
}

func twoRunning() []SubagentInfo {
	return []SubagentInfo{
		{Name: "reviewer", Status: "running", StartedAt: tasksEpoch},
		{Name: "prober", Status: "running", StartedAt: tasksEpoch.Add(time.Second)},
	}
}

// markedRow is the bar row carrying the selection bar, or "" for none.
func markedRow(m *model) string {
	for _, row := range barText(m) {
		if strings.HasPrefix(row, glyphSelectBar) {
			return row
		}
	}
	return ""
}

func footerText(m *model) string {
	return ansi.Strip(m.renderFooter(m.chromeWidth()))
}

func TestTasksFocus_DownFromEmptyComposerEntersBar(t *testing.T) {
	m, _ := tasksFocusModel(t, twoRunning()...)
	if !strings.Contains(footerText(m), "↓ subagents") {
		t.Errorf("footer = %q, want the ↓ hint while the bar has rows and the input is empty", footerText(m))
	}

	m = press(m, "down")
	if m.focus != focusTasks {
		t.Fatalf("focus = %v after ↓ from an empty composer, want the tasks bar", m.focus)
	}
	if m.input.Focused() {
		t.Error("composer still focused: typed text would land in a prompt nobody is looking at")
	}
	if row := markedRow(m); !strings.Contains(row, "reviewer") {
		t.Errorf("marked row = %q, want the first row (reviewer)", row)
	}
	if got := footerText(m); !strings.Contains(got, "Subagents") || !strings.Contains(got, "enter open") {
		t.Errorf("footer = %q, want the bar's legend", got)
	}
}

func TestTasksFocus_DownStaysInComposer(t *testing.T) {
	t.Run("input has text", func(t *testing.T) {
		m, _ := tasksFocusModel(t, twoRunning()...)
		m.input.SetValue("half a thought")
		if strings.Contains(footerText(m), "↓ subagents") {
			t.Errorf("footer = %q advertises ↓ while it would move the caret instead", footerText(m))
		}
		if m = press(m, "down"); m.focus != focusInput {
			t.Errorf("focus = %v, want ↓ left to the composer while composing", m.focus)
		}
	})
	t.Run("no bar", func(t *testing.T) {
		m, _ := tasksFocusModel(t)
		if strings.Contains(footerText(m), "↓ subagents") {
			t.Errorf("footer = %q advertises ↓ with no bar to enter", footerText(m))
		}
		if m = press(m, "down"); m.focus != focusInput {
			t.Errorf("focus = %v, want ↓ inert with no bar", m.focus)
		}
	})
}

func TestTasksFocus_NavigateAndLeave(t *testing.T) {
	m, _ := tasksFocusModel(t, twoRunning()...)
	m = press(m, "down")
	m = press(m, "j")
	if row := markedRow(m); !strings.Contains(row, "prober") {
		t.Fatalf("marked row = %q after j, want prober", row)
	}
	m = press(m, "down")
	if row := markedRow(m); !strings.Contains(row, "prober") {
		t.Errorf("marked row = %q after ↓ on the last row, want it to stay put", row)
	}
	m = press(m, "k")
	m = press(m, "up")
	if m.focus != focusInput || !m.input.Focused() {
		t.Errorf("↑ off the top row left focus = %v, want the composer back", m.focus)
	}
	if row := markedRow(m); row != "" {
		t.Errorf("marked row = %q with the composer focused, want no marker", row)
	}

	for _, key := range []string{"tab", "esc"} {
		m = press(m, "down")
		if m = press(m, key); m.focus != focusInput {
			t.Errorf("%s from the bar left focus = %v, want the composer", key, m.focus)
		}
	}
}

// The "+ N more" row stands for several subagents, so it is not a
// row the cursor can open.
func TestTasksFocus_MoreRowIsNotSelectable(t *testing.T) {
	subs := twoRunning()
	subs = append(subs,
		SubagentInfo{Name: "third", Status: "running", StartedAt: tasksEpoch.Add(2 * time.Second)},
		SubagentInfo{Name: "fourth", Status: "running", StartedAt: tasksEpoch.Add(3 * time.Second)})
	m, _ := tasksFocusModel(t, subs...)
	m = press(m, "down")
	m = press(m, "down")
	m = press(m, "down")
	if row := markedRow(m); !strings.Contains(row, "prober") {
		t.Errorf("marked row = %q, want the cursor held on the last named row (prober)", row)
	}
}

func TestTasksFocus_EnterOpensLiveOverlay(t *testing.T) {
	m, _ := tasksFocusModel(t, twoRunning()...)
	m = press(m, "down")
	m = press(m, "down")
	m, cmd := pressCmd(m, "enter")

	d, ok := m.overlayStack.get(subagentDialogID).(*subagentDialog)
	if !ok {
		t.Fatal("enter on a bar row opened no subagent overlay")
	}
	if d.name != "prober" {
		t.Errorf("overlay opened on %q, want the selected row (prober)", d.name)
	}
	if cmd == nil {
		t.Error("enter returned no Cmd: the overlay's first fetch and live tail never start")
	}
	if m.focus != focusTasks {
		t.Errorf("focus = %v under the overlay, want the bar kept for the next pick", m.focus)
	}

	m = press(m, "esc")
	if m.overlayStack.hasID(subagentDialogID) {
		t.Fatal("esc left the overlay open")
	}
	if m.focus != focusTasks {
		t.Errorf("closing the overlay left focus = %v, want the bar it was opened from", m.focus)
	}
}

// A roster tick can move the selected subagent (it finished, so it
// drops below the in-flight rows) or take it off the bar entirely.
func TestTasksFocus_SelectionFollowsRoster(t *testing.T) {
	m, now := tasksFocusModel(t, twoRunning()...)
	m = press(m, "down")

	*now = tasksEpoch.Add(time.Second)
	m = landRoster(t, m,
		SubagentInfo{Name: "reviewer", Status: "done", LastReport: "ok"},
		SubagentInfo{Name: "prober", Status: "running", StartedAt: tasksEpoch.Add(time.Second)})
	if row := markedRow(m); !strings.Contains(row, "reviewer") {
		t.Errorf("marked row = %q, want the cursor to follow reviewer to its lingering row", row)
	}

	*now = tasksEpoch.Add(time.Second + tasksLinger)
	m = landRoster(t, m,
		SubagentInfo{Name: "reviewer", Status: "done"},
		SubagentInfo{Name: "prober", Status: "running", StartedAt: tasksEpoch.Add(time.Second)})
	if row := markedRow(m); !strings.Contains(row, "prober") {
		t.Errorf("marked row = %q after reviewer left the bar, want the row that took its place", row)
	}

	m = landRoster(t, m, SubagentInfo{Name: "prober", Status: "done"})
	*now = now.Add(tasksLinger)
	m = landRoster(t, m, SubagentInfo{Name: "prober", Status: "done"})
	if m.focus != focusInput || !m.input.Focused() {
		t.Errorf("bar emptied under the cursor but focus = %v, want the composer back", m.focus)
	}
}
