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

// Model picker tests. The dialog shipped in v0.1.0 with no test file
// of its own — issue #110's cursor seed got one
// (dialog_modelpicker_seed_test.go) and everything else was covered
// incidentally from host_async_test.go. Type-to-filter (#117) is the
// first behaviour with enough edges to need direct coverage: a list
// that shrinks under the cursor, an empty result that must not be
// mistaken for an empty host, and a caret on a modal that never had
// one.

package tui

import (
	"context"
	"iter"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// swapAgent is a fast ModelSwapper — slowAgent sleeps half a second
// per call, which is right for the #114 timing tests and wrong for
// anything that just needs a list.
type swapAgent struct {
	id          string
	models      []ModelInfo
	switchCalls []string
}

func (a *swapAgent) Run(_ context.Context, _ string) iter.Seq2[Event, error] {
	return func(_ func(Event, error) bool) {}
}

func (a *swapAgent) AvailableModels() []ModelInfo { return a.models }

func (a *swapAgent) SwitchModel(id string) (Agent, error) {
	a.switchCalls = append(a.switchCalls, id)
	return &swapAgent{id: id, models: a.models}, nil
}

// pickerModels is the fixture: display names and IDs that disagree,
// two rows sharing a prefix, and one row whose ID is its only name.
func pickerModels() []ModelInfo {
	return []ModelInfo{
		{ID: "openai/gpt-4o", Display: "GPT-4o", Description: "fast"},
		{ID: "openai/gpt-4.1", Display: "GPT-4.1"},
		{ID: "anthropic/claude-opus-5", Display: "Claude Opus 5", Description: "big"},
		{ID: "google/gemini-3-pro"},
		{ID: "meta/llama-4"},
	}
}

// openModelPickerFixture returns a sized model with a loaded picker
// on the stack, and the picker itself.
func openModelPickerFixture(t *testing.T) (Model, *modelPickerDialog) {
	t.Helper()
	m := NewModel(Options{Agent: &swapAgent{id: "cur", models: pickerModels()}})
	m.styles = NewStylesWithTheme(true, goldenTheme())
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = out.(Model)
	d := newModelPickerDialog()
	d.applyModels(pickerModels(), "openai/gpt-4o")
	m.overlayStack.Open(d)
	return m, d
}

// typeIntoPicker drives text through the Overlay the way handleKey
// does, so the test exercises the real KeyMsgDialog routing rather
// than poking the widget.
func typeIntoPicker(m *Model, text string) {
	for _, r := range text {
		m.overlayStack.HandleKeyMsg(tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)}), m)
	}
}

func modelIDs(models []ModelInfo) []string {
	out := make([]string, len(models))
	for i, mi := range models {
		out[i] = mi.ID
	}
	return out
}

// TestModelPicker_TypingNarrowsTheList is issue #117's headline: a
// letter used to do nothing at all, because HandleKey's catch-all
// consumed it to keep it out of the textarea behind the modal.
func TestModelPicker_TypingNarrowsTheList(t *testing.T) {
	m, d := openModelPickerFixture(t)
	if got := len(d.rows()); got != 5 {
		t.Fatalf("precondition: unfiltered picker has %d rows, want 5", got)
	}

	typeIntoPicker(&m, "gpt")
	assertNameOrder(t, modelIDs(d.rows()), []string{"openai/gpt-4o", "openai/gpt-4.1"})

	// Matching on the ID works even when the host set a Display that
	// does not contain it — both are printed on the row, so both have
	// to be searchable.
	d.filter = newPickerFilter()
	typeIntoPicker(&m, "anthropic")
	assertNameOrder(t, modelIDs(d.rows()), []string{"anthropic/claude-opus-5"})

	// And backspacing all the way out restores the full list in the
	// host's original order.
	for i := 0; i < len("anthropic"); i++ {
		m.overlayStack.HandleKeyMsg(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}), &m)
	}
	assertNameOrder(t, modelIDs(d.rows()), modelIDs(pickerModels()))
}

// TestModelPicker_EnterPicksTheFilteredRow: idx indexes rows(), not
// d.models. If Enter ever reads the snapshot directly, filtering to
// one row and pressing Enter switches to the wrong model — a silent,
// destructive version of the bug.
func TestModelPicker_EnterPicksTheFilteredRow(t *testing.T) {
	m, d := openModelPickerFixture(t)
	typeIntoPicker(&m, "llama")
	if got := len(d.rows()); got != 1 {
		t.Fatalf("filter %q matched %d rows, want 1 (%v)", "llama", got, modelIDs(d.rows()))
	}
	if d.idx != 0 {
		t.Fatalf("cursor is at %d after filtering, want 0", d.idx)
	}

	act := d.HandleKey("enter", &m)
	if !act.Consumed || act.Close {
		t.Errorf("enter = %+v, want Consumed and not Close (the switch is in flight)", act)
	}
	if d.switching != "meta/llama-4" {
		t.Errorf("switching = %q, want the filtered row's ID", d.switching)
	}
	if act.Cmd == nil {
		t.Fatal("expected the off-loop SwitchModel Cmd")
	}
	msg, _ := act.Cmd().(modelSwitchedMsg)
	if msg.id != "meta/llama-4" {
		t.Errorf("SwitchModel called with %q, want meta/llama-4", msg.id)
	}
}

// TestModelPicker_FilterMatchingNothingStaysOpen separates the two
// empty states that used to be one. An empty rows() with no filter
// means the HOST advertised nothing — say so and close. An empty
// rows() WITH a filter means the operator has typed past every match,
// and closing the dialog out from under them would be absurd.
func TestModelPicker_FilterMatchingNothingStaysOpen(t *testing.T) {
	m, d := openModelPickerFixture(t)
	typeIntoPicker(&m, "zzz")
	if got := len(d.rows()); got != 0 {
		t.Fatalf("filter %q matched %d rows, want none", "zzz", got)
	}

	for _, stroke := range []string{"down", "up", "enter"} {
		act := d.HandleKey(stroke, &m)
		if !act.Consumed || act.Close {
			t.Errorf("%q on an empty filter result = %+v, want Consumed and not Close", stroke, act)
		}
	}
	if d.switching != "" {
		t.Errorf("enter on an empty result started a switch to %q", d.switching)
	}
	if n := len(m.history.Snapshot()); n != 0 {
		t.Errorf("an empty filter result wrote %d chat messages; it is not a host problem", n)
	}

	body := ansi.Strip(d.Render(100, &m))
	if !strings.Contains(body, "no models match") {
		t.Errorf("empty-result body does not say so:\n%s", body)
	}
	if !strings.Contains(body, "zzz") {
		t.Errorf("empty-result body does not echo the filter:\n%s", body)
	}
	if !strings.Contains(body, "0/5") {
		t.Errorf("empty-result body is missing the 0/5 count:\n%s", body)
	}
}

// TestModelPicker_HostWithNoModelsStillCloses pins the behaviour the
// filter must not have swallowed: a host that advertises an empty
// list says so in the chat and closes, on any key.
func TestModelPicker_HostWithNoModelsStillCloses(t *testing.T) {
	m := NewModel(Options{Agent: &swapAgent{id: "cur"}})
	m.viewport.SetWidth(80)
	d := newModelPickerDialog()
	d.applyModels(nil, "")
	m.overlayStack.Open(d)

	act := d.HandleKey("down", &m)
	if !act.Consumed || !act.Close {
		t.Errorf("down on an empty host list = %+v, want Consumed and Close", act)
	}
	snap := m.history.Snapshot()
	if len(snap) != 1 || !strings.Contains(snap[0].Text, "no models available") {
		t.Errorf("history = %v, want the 'no models available' system message", snap)
	}
}

// TestModelPicker_ShrinkingListNeverPanics walks the cursor while the
// list changes size under it — typing, deleting, and pressing the
// arrows at every step. The old cursor arithmetic was
// (idx ± 1 + len) % len, which divides by zero the moment len hits 0,
// and was only safe because the list could not shrink.
func TestModelPicker_ShrinkingListNeverPanics(t *testing.T) {
	m, d := openModelPickerFixture(t)
	script := "gptzzz"
	for _, r := range script {
		typeIntoPicker(&m, string(r))
		for _, stroke := range []string{"down", "down", "up"} {
			d.HandleKey(stroke, &m)
		}
		if d.idx < 0 || (len(d.rows()) > 0 && d.idx >= len(d.rows())) {
			t.Fatalf("cursor %d is outside the %d filtered rows", d.idx, len(d.rows()))
		}
		d.Render(100, &m)
	}
	for i := 0; i < len(script); i++ {
		m.overlayStack.HandleKeyMsg(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}), &m)
		for _, stroke := range []string{"up", "down"} {
			d.HandleKey(stroke, &m)
		}
		d.Render(100, &m)
	}
	if got := len(d.rows()); got != 5 {
		t.Errorf("after backspacing the filter away the picker shows %d rows, want 5", got)
	}
}

// TestModelPicker_CursorSitsInTheFilterRow is the #105 / #123
// requirement on a new typed surface: modalCursor answers (nil, true)
// for a dialog that does not implement DialogCursor, so without one
// the picker would take input with no hardware caret and no IME
// anchor. Asserted through the composed frame, with CJK in the value
// so a rune-index measurement fails (#125).
func TestModelPicker_CursorSitsInTheFilterRow(t *testing.T) {
	for _, typed := range []string{"gpt", "日本語", "a日b"} {
		t.Run(typed, func(t *testing.T) {
			m, _ := openModelPickerFixture(t)
			typeIntoPicker(&m, typed)
			v := m.View()
			assertCursorFollows(t, v, filterPromptRail+typed)
		})
	}
}

// TestModelPicker_HighlightsTheMatchedSpan: contiguous substring
// matching means the highlight is one span, so the row's plain text
// is unchanged and only the matched run carries an escape.
func TestModelPicker_HighlightsTheMatchedSpan(t *testing.T) {
	m, d := openModelPickerFixture(t)
	typeIntoPicker(&m, "opus")
	rendered := d.Render(100, &m)
	if !strings.Contains(ansi.Strip(rendered), "Claude Opus 5") {
		t.Fatalf("filtered row missing from the body:\n%s", ansi.Strip(rendered))
	}
	// The span carries its own escape run, so the styled row is
	// strictly longer than the plain one at the same cell width.
	for _, line := range strings.Split(rendered, "\n") {
		if !strings.Contains(ansi.Strip(line), "Claude Opus 5") {
			continue
		}
		if len(line) == len(ansi.Strip(line)) {
			t.Errorf("matched row carries no styling at all: %q", line)
		}
		return
	}
}

// TestModelPicker_BodyFitsTheTerminal is the geometry the +1 on
// modalChromeRows buys: the filter row is an extra body row taken out
// of the list's allowance rather than added to the modal, so the
// modal is exactly as tall as it was before the filter existed.
//
// Nine rows is the floor for that being true. Below it
// modalBodyHeight stops subtracting and returns minModalBodyRows
// instead (dialog_scroll.go), at which point the modal is taller than
// the terminal whatever chrome figure it was given — clipFrame is the
// backstop there, and TestFrameInvariants_Grid drives the picker
// across heights 4 and 10 to prove it.
func TestModelPicker_BodyFitsTheTerminal(t *testing.T) {
	for _, h := range []int{9, 10, 14, 24, 50} {
		m, d := openModelPickerFixture(t)
		m.height = h
		rendered := d.Render(100, &m)
		if got := strings.Count(rendered, "\n") + 1; got > h {
			t.Errorf("height %d: picker is %d rows tall\n%s", h, got, ansi.Strip(rendered))
		}
		// The filter row is never the thing that gets windowed away:
		// it is what the operator needs to edit to get back.
		if !strings.Contains(ansi.Strip(rendered), filterPlaceholder) {
			t.Errorf("height %d: filter row scrolled out of the body\n%s", h, ansi.Strip(rendered))
		}
	}
}

// TestModelPicker_UnfilteredStatesHaveNoCaret: loading, an in-flight
// switch and an unwired agent all render something with no filter row
// on it, so none of them may claim the terminal cursor.
func TestModelPicker_UnfilteredStatesHaveNoCaret(t *testing.T) {
	loaded := func() (Model, *modelPickerDialog) { return openModelPickerFixture(t) }

	cases := []struct {
		name  string
		setup func() (Model, *modelPickerDialog)
	}{
		{
			name: "loading",
			setup: func() (Model, *modelPickerDialog) {
				m, _ := loaded()
				d := newModelPickerDialog() // no snapshot yet
				m.overlayStack = Overlay{}
				m.overlayStack.Open(d)
				return m, d
			},
		},
		{
			name: "switching",
			setup: func() (Model, *modelPickerDialog) {
				m, d := loaded()
				d.switching = "meta/llama-4"
				return m, d
			},
		},
		{
			name: "unwired-agent",
			setup: func() (Model, *modelPickerDialog) {
				m, d := loaded()
				m.opts.Agent = &bareAgent{id: "bare"}
				return m, d
			},
		},
		{
			name: "host-advertised-nothing",
			setup: func() (Model, *modelPickerDialog) {
				m, d := loaded()
				d.applyModels(nil, "")
				return m, d
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, d := tc.setup()
			if c := d.DialogCursor(100, &m); c != nil {
				t.Errorf("state %q claimed the caret at (%d,%d) with no filter row on screen",
					tc.name, c.X, c.Y)
			}
		})
	}
}
