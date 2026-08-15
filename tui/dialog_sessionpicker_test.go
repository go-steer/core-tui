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

// Session picker tests. The dialog's Enter paths are covered from
// switch_test.go (the /switch built-in that opens it); this file is
// about the dialog itself, starting with type-to-filter (#117). A
// hundred-session project is the case that motivates the filter
// hardest, and it is the one the picker had no answer for.

package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// pickerSessions mixes the row kinds the picker renders: the attached
// row, plain rows with IDs that differ from their display names, and
// an action row (issue #56) that opens a text input instead of
// switching.
func pickerSessions() []SessionInfo {
	return []SessionInfo{
		{ID: "sess-001", Display: "nightly refactor", Current: true},
		{ID: "sess-002", Display: "docs sweep"},
		{ID: "sess-003", Display: "flaky test hunt", Description: "2 days ago"},
		{ID: "prod-042", Display: "prod incident"},
		{ID: "attach", Display: "+ Attach to endpoint…", Input: &SessionInput{
			Prompt: "Daemon URL:",
			Submit: func(string) (SwitchTarget, error) {
				return SwitchTarget{Agent: &bareAgent{id: "remote"}}, nil
			},
		}},
	}
}

func openSessionPickerFixture(t *testing.T) (Model, *sessionPickerDialog) {
	t.Helper()
	m := NewModel(Options{Agent: &switchAgent{id: "cur", sessions: pickerSessions()}})
	m.styles = NewStylesWithTheme(true, goldenTheme())
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = out.(Model)
	d := newSessionPickerDialog()
	d.applySessions(pickerSessions())
	m.overlayStack.Open(d)
	return m, d
}

func sessionIDs(sessions []SessionInfo) []string {
	out := make([]string, len(sessions))
	for i, s := range sessions {
		out[i] = s.ID
	}
	return out
}

// TestSessionPicker_TypingNarrowsTheList covers the display name, the
// ID, and the action row — all three are visible on the row, so all
// three have to be findable.
func TestSessionPicker_TypingNarrowsTheList(t *testing.T) {
	cases := []struct {
		filter string
		want   []string
	}{
		{filter: "docs", want: []string{"sess-002"}},
		// All three are tier-4 substring matches (the key is
		// "<display> <id>", and "sess-00" is inside neither the
		// display name nor a whole segment of it), so the shared
		// tiebreak orders them by key length — shortest first.
		{filter: "sess-00", want: []string{"sess-002", "sess-003", "sess-001"}},
		{filter: "prod", want: []string{"prod-042"}},
		{filter: "attach", want: []string{"attach"}},
		{filter: "zzz", want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.filter, func(t *testing.T) {
			m, d := openSessionPickerFixture(t)
			typeIntoPicker(&m, tc.filter)
			assertNameOrder(t, sessionIDs(d.rows()), tc.want)
		})
	}
}

// TestSessionPicker_EnterAttachesTheFilteredRow: the cursor indexes
// rows(), so a filtered Enter must attach to what is highlighted and
// not to the row at that index of the unfiltered snapshot.
func TestSessionPicker_EnterAttachesTheFilteredRow(t *testing.T) {
	m, d := openSessionPickerFixture(t)
	typeIntoPicker(&m, "prod")
	act := d.HandleKey("enter", &m)
	if !act.Consumed || act.Close {
		t.Errorf("enter = %+v, want Consumed and not Close", act)
	}
	if d.switching != "prod-042" {
		t.Errorf("switching = %q, want the filtered row prod-042", d.switching)
	}
	if act.Cmd == nil {
		t.Fatal("expected the off-loop SwitchToSession Cmd")
	}
	act.Cmd()
	agent := m.opts.Agent.(*switchAgent)
	if len(agent.switchCalls) != 1 || agent.switchCalls[0] != "prod-042" {
		t.Errorf("switchCalls = %v, want [prod-042]", agent.switchCalls)
	}
}

// TestSessionPicker_EnterOnAFilteredActionRow: filtering down to the
// action row and pressing Enter must still stack the text-input
// dialog on top rather than trying to attach to a session ID that
// names no session.
func TestSessionPicker_EnterOnAFilteredActionRow(t *testing.T) {
	m, d := openSessionPickerFixture(t)
	typeIntoPicker(&m, "endpoint")
	if got := sessionIDs(d.rows()); len(got) != 1 || got[0] != "attach" {
		t.Fatalf("filter matched %v, want just the action row", got)
	}
	d.HandleKey("enter", &m)
	if !m.overlayStack.HasID(sessionInputDialogID) {
		t.Error("enter on the filtered action row did not open its text input")
	}
	if !m.overlayStack.HasID(sessionPickerDialogID) {
		t.Error("the picker should stay open underneath so esc returns to the list")
	}
	if d.switching != "" {
		t.Errorf("an action row started a switch to %q", d.switching)
	}
}

// TestSessionPicker_FilterMatchingNothingStaysOpen keeps the two
// empty states apart: nothing enumerated by the host closes with a
// message, nothing matched by the filter does not.
func TestSessionPicker_FilterMatchingNothingStaysOpen(t *testing.T) {
	m, d := openSessionPickerFixture(t)
	typeIntoPicker(&m, "zzz")
	for _, stroke := range []string{"down", "up", "enter"} {
		if act := d.HandleKey(stroke, &m); !act.Consumed || act.Close {
			t.Errorf("%q on an empty filter result = %+v, want Consumed and not Close", stroke, act)
		}
	}
	if n := len(m.history.Snapshot()); n != 0 {
		t.Errorf("an empty filter result wrote %d chat messages", n)
	}
	body := ansi.Strip(d.Render(100, &m))
	if !strings.Contains(body, "no sessions match") {
		t.Errorf("empty-result body does not say so:\n%s", body)
	}
	if !strings.Contains(body, "0/5") {
		t.Errorf("empty-result body is missing the 0/5 count:\n%s", body)
	}
}

// TestSessionPicker_HostWithNoSessionsStillCloses pins the pre-filter
// behaviour for an empty enumeration.
func TestSessionPicker_HostWithNoSessionsStillCloses(t *testing.T) {
	m := NewModel(Options{Agent: &switchAgent{id: "cur"}})
	m.viewport.SetWidth(80)
	d := newSessionPickerDialog()
	d.applySessions(nil)
	m.overlayStack.Open(d)

	act := d.HandleKey("down", &m)
	if !act.Consumed || !act.Close {
		t.Errorf("down on an empty host list = %+v, want Consumed and Close", act)
	}
	snap := m.history.Snapshot()
	if len(snap) != 1 || !strings.Contains(snap[0].Text, "no sessions available") {
		t.Errorf("history = %v, want the 'no sessions available' system message", snap)
	}
}

// TestSessionPicker_ShrinkingListNeverPanics types a filter that runs
// out of matches while the arrows are pressed at every step.
func TestSessionPicker_ShrinkingListNeverPanics(t *testing.T) {
	m, d := openSessionPickerFixture(t)
	for _, r := range "sess-00xy" {
		typeIntoPicker(&m, string(r))
		for _, stroke := range []string{"down", "down", "down", "up"} {
			d.HandleKey(stroke, &m)
		}
		if n := len(d.rows()); n > 0 && (d.idx < 0 || d.idx >= n) {
			t.Fatalf("cursor %d is outside the %d filtered rows", d.idx, n)
		}
		d.Render(100, &m)
	}
}

// TestSessionPicker_CursorSitsInTheFilterRow — same #105 / #123
// requirement as the model picker's, and the same #125 cell-vs-rune
// trap in the CJK case.
func TestSessionPicker_CursorSitsInTheFilterRow(t *testing.T) {
	for _, typed := range []string{"docs", "セッション"} {
		t.Run(typed, func(t *testing.T) {
			m, _ := openSessionPickerFixture(t)
			typeIntoPicker(&m, typed)
			assertCursorFollows(t, m.View(), filterPromptRail+typed)
		})
	}
}

// TestSessionPicker_UnfilteredStatesHaveNoCaret: nothing to narrow,
// no caret.
func TestSessionPicker_UnfilteredStatesHaveNoCaret(t *testing.T) {
	t.Run("loading", func(t *testing.T) {
		m, _ := openSessionPickerFixture(t)
		d := newSessionPickerDialog()
		if c := d.DialogCursor(100, &m); c != nil {
			t.Errorf("a picker with no snapshot claimed the caret at (%d,%d)", c.X, c.Y)
		}
	})
	t.Run("switching", func(t *testing.T) {
		m, d := openSessionPickerFixture(t)
		d.switching = "prod-042"
		if c := d.DialogCursor(100, &m); c != nil {
			t.Errorf("an in-flight attach claimed the caret at (%d,%d)", c.X, c.Y)
		}
	})
	t.Run("unwired-agent", func(t *testing.T) {
		m, d := openSessionPickerFixture(t)
		m.opts.Agent = &bareAgent{id: "bare"}
		if c := d.DialogCursor(100, &m); c != nil {
			t.Errorf("an unwired agent claimed the caret at (%d,%d)", c.X, c.Y)
		}
	})
}
