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

// Model picker tests, from the app's side.
//
// What the widget answers on its own — filtering, the cursor, the
// empty states, what Enter commits to — moved to
// question_external_test.go when the picker became a question (#164
// stage 3), because a list that is a state machine over keystrokes
// needs no app model to exercise and the whole point of the migration
// is that it no longer has one.
//
// What is left here is everything that does need a Model: the
// resolver's effects, and the Update-side half of an answer that
// arrives from a host reply rather than from a keystroke.

package tui

import (
	"context"
	"errors"
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

// askModelPicker opens the picker exactly as /model does — the
// question, plus the resolver that knows what its answer means. A test
// that opened the bare widget would still pass while the pairing the
// open site makes was wrong.
func askModelPicker(m *Model, wired bool) *modelPickerQuestion {
	q := newModelPickerQuestion(wired)
	m.overlayStack.ask(q, modelPickerResolver(q))
	return q
}

// openModelPickerFixture returns a sized model with a loaded picker on
// the stack, and the picker itself.
func openModelPickerFixture(t *testing.T) (Model, *modelPickerQuestion) {
	t.Helper()
	m := NewModel(Options{Agent: &swapAgent{id: "cur", models: pickerModels()}})
	m.styles = NewStylesWithTheme(true, goldenTheme())
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = out.(Model)
	q := askModelPicker(&m, true)
	q.applyModels(pickerModels(), "openai/gpt-4o")
	return m, q
}

// typeIntoPicker drives text through the Overlay the way handleKey
// does, so the test exercises the real KeyMsgDialog routing rather
// than poking the widget.
func typeIntoPicker(m *Model, text string) {
	for _, r := range text {
		m.overlayStack.HandleKeyMsg(tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)}), m)
	}
}

// TestModelPicker_EnterDoesNotAnswer is the shape that separates this
// picker from the theme picker, seen from the stack rather than from
// the widget. Enter commits to a model but does not end the question,
// so the overlay must still be holding it when the frame settles —
// with the progress line on it, since a list that freezes for as long
// as the host takes reads as a hang.
func TestModelPicker_EnterDoesNotAnswer(t *testing.T) {
	m, q := openModelPickerFixture(t)
	typeIntoPicker(&m, "llama")

	consumed, cmd := m.overlayStack.HandleKeyMsg(keyMsgFromStroke("enter"), &m)
	if !consumed {
		t.Error("enter was not consumed")
	}
	if !m.overlayStack.HasID(modelPickerDialogID) {
		t.Fatal("enter popped the picker; the switch has not landed yet")
	}
	if q.switching != "meta/llama-4" {
		t.Errorf("switching = %q, want the filtered row's ID", q.switching)
	}
	msgs := drainBatch(t, cmd)
	if len(msgs) != 1 {
		t.Fatalf("enter scheduled %d messages, want just the switch request: %#v", len(msgs), msgs)
	}
	req, ok := msgs[0].(modelSwitchRequestedMsg)
	if !ok {
		t.Fatalf("enter scheduled a %T, want modelSwitchRequestedMsg", msgs[0])
	}
	if req.ID != "meta/llama-4" {
		t.Errorf("requested %q, want meta/llama-4", req.ID)
	}
	if body := ansi.Strip(m.overlayStack.Render(100, &m)); !strings.Contains(body, "switching to meta/llama-4") {
		t.Errorf("in-flight render missing the progress line:\n%s", body)
	}
}

// TestModelPicker_RequestReadsTheLiveSwapperAndGen. The question holds
// neither, deliberately: a switch that completed under it replaced
// m.opts.Agent, and a session switch bumps the generation without
// closing the stack. Both are resolved when the request lands, so a
// picker that has been open across either still calls the right agent.
func TestModelPicker_RequestReadsTheLiveSwapperAndGen(t *testing.T) {
	m := NewModel(Options{Agent: &swapAgent{id: "cur", models: pickerModels()}})
	m.viewport.SetWidth(80)
	q := readyModelPicker(&m)
	q.switching = "meta/llama-4"

	m.sessionGen = 42
	out, cmd := m.Update(modelSwitchRequestedMsg{ID: "meta/llama-4"})
	m = out.(Model)
	if cmd == nil {
		t.Fatal("the request scheduled no SwitchModel call")
	}
	msg, ok := cmd().(modelSwitchedMsg)
	if !ok {
		t.Fatalf("the request scheduled a %T, want modelSwitchedMsg", cmd())
	}
	if msg.gen != 42 {
		t.Errorf("SwitchModel stamped gen %d, want the live 42", msg.gen)
	}
	if msg.id != "meta/llama-4" {
		t.Errorf("SwitchModel called with %q", msg.id)
	}
}

// TestModelPicker_StaleRequestIsDropped. The request is a message, so
// it can land after esc popped the picker or after a second one
// opened. Neither may reach the host: nothing has been asked of it
// yet, so dropping the request is free, and making the call would
// switch a model nobody is looking at.
func TestModelPicker_StaleRequestIsDropped(t *testing.T) {
	agent := &swapAgent{id: "cur", models: pickerModels()}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	// No picker at all.
	if _, cmd := m.Update(modelSwitchRequestedMsg{ID: "meta/llama-4"}); cmd != nil {
		t.Error("a request with no picker open still scheduled a host call")
	}

	// A picker that is not switching to that model.
	q := readyModelPicker(&m)
	out, cmd := m.Update(modelSwitchRequestedMsg{ID: "meta/llama-4"})
	m = out.(Model)
	if cmd != nil {
		t.Error("a request the open picker did not issue still scheduled a host call")
	}
	if q.switching != "" {
		t.Errorf("a dropped request left the picker in flight to %q", q.switching)
	}
	if len(agent.switchCalls) != 0 {
		t.Errorf("SwitchModel was called anyway: %v", agent.switchCalls)
	}
}

// TestModelPicker_SwitchLandingAnswersThePicker is the async answer:
// the host's reply, not the keystroke, is what resolves the question,
// through Overlay.resolve rather than a bare Close. The distinction is
// invisible here and load-bearing in general — a Close pops the
// question with its resolver never run.
func TestModelPicker_SwitchLandingAnswersThePicker(t *testing.T) {
	next := &bareAgent{id: "next"}
	m := NewModel(Options{Agent: &swapAgent{id: "cur", models: pickerModels()}})
	m.viewport.SetWidth(80)
	q := readyModelPicker(&m)
	q.switching = "meta/llama-4"

	out, _ := m.Update(modelSwitchedMsg{gen: m.sessionGen, id: "meta/llama-4", agent: next})
	m = out.(Model)

	if m.overlayStack.HasID(modelPickerDialogID) {
		t.Error("the picker survived the switch it was waiting on")
	}
	if m.opts.Agent != Agent(next) {
		t.Errorf("Agent not attached: %v", m.opts.Agent)
	}
	snap := m.history.Snapshot()
	if len(snap) == 0 || !strings.Contains(snap[len(snap)-1].Text, "switched to meta/llama-4") {
		t.Errorf("missing the confirmation row: %+v", snap)
	}
}

// TestModelPicker_FailedSwitchKeepsTheListUp. The question was never
// answered, so the picker stays: the operator's next move is almost
// always the next model down, and closing would make them re-run
// /model to get back to a list they were already looking at. This is a
// deliberate change from the pre-question behaviour, which closed.
func TestModelPicker_FailedSwitchKeepsTheListUp(t *testing.T) {
	cases := []struct {
		name string
		msg  modelSwitchedMsg
		want string
	}{
		{"host returned an error", modelSwitchedMsg{
			id: "meta/llama-4", err: errors.New("provider unreachable"),
		}, "switch failed"},
		{"host returned a nil Agent", modelSwitchedMsg{
			id: "meta/llama-4",
		}, "nil Agent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewModel(Options{Agent: &swapAgent{id: "cur", models: pickerModels()}})
			m.viewport.SetWidth(80)
			q := readyModelPicker(&m)
			q.switching = "meta/llama-4"

			msg := tc.msg
			msg.gen = m.sessionGen
			out, _ := m.Update(msg)
			m = out.(Model)

			if !m.overlayStack.HasID(modelPickerDialogID) {
				t.Fatal("a failed switch closed the picker")
			}
			if q.switching != "" {
				t.Errorf("a failed switch left the picker stuck in flight to %q", q.switching)
			}
			snap := m.history.Snapshot()
			if len(snap) == 0 || !strings.Contains(snap[len(snap)-1].Text, tc.want) {
				t.Errorf("history = %+v, want a row mentioning %q", snap, tc.want)
			}
			// And the list is usable again rather than stuck behind the
			// in-flight guard, which swallows every stroke but esc.
			if _, cmd := m.overlayStack.HandleKeyMsg(keyMsgFromStroke("enter"), &m); cmd == nil {
				t.Error("the picker is inert after a failed switch")
			}
		})
	}
}

// TestModelPicker_FailedSwitchSaysWhyOnThePicker is the other half of
// the test above, and issue #245: keeping the list up is only the right
// call if the operator can tell that something happened. The reason
// applyModelSwitch writes goes to the transcript, which is BEHIND this
// modal — on its own that is Enter, a pause, and an unchanged list.
//
// Asserted on the rendered frame rather than on the field, because the
// field being set while the row is not drawn is exactly the bug: the
// list is windowed and the row is appended after it.
func TestModelPicker_FailedSwitchSaysWhyOnThePicker(t *testing.T) {
	cases := []struct {
		name string
		msg  modelSwitchedMsg
		want string
	}{
		{"host returned an error", modelSwitchedMsg{
			id: "meta/llama-4", err: errors.New("provider unreachable"),
		}, "switch failed: provider unreachable"},
		{"host returned a nil Agent", modelSwitchedMsg{
			id: "meta/llama-4",
		}, "ModelSwapper returned nil Agent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, q := openModelPickerFixture(t)
			q.switching = "meta/llama-4"
			msg := tc.msg
			msg.gen = m.sessionGen

			out, _ := m.Update(msg)
			m = out.(Model)

			lines := modalContentLines(m.overlayStack.Render(100, &m))
			at := lineWith(lines, tc.want)
			if at < 0 {
				t.Fatalf("the frame does not say why the switch failed:\n%s",
					strings.Join(lines, "\n"))
			}
			if !strings.Contains(lines[at], GlyphWarn) {
				t.Errorf("the reason row is missing its warn glyph: %q", lines[at])
			}
			// The list is still there under it — the row is an addition
			// to the picker, not a replacement for it.
			if last := lineWith(lines, "meta/llama-4"); last < 0 || last > at {
				t.Errorf("the reason is not under the list:\n%s", strings.Join(lines, "\n"))
			}
			// And the transcript still carries its own copy: /model <id>
			// reaches this same handler with no modal in the way.
			snap := m.history.Snapshot()
			if len(snap) == 0 || !strings.Contains(snap[len(snap)-1].Text, tc.want) {
				t.Errorf("history = %+v, want a row mentioning %q", snap, tc.want)
			}
		})
	}
}

// TestModelPicker_FailureRowClearsOnTheNextMove. The reason names a
// model, so it stops being true the moment the operator is looking at a
// different one — otherwise "provider unreachable" sits under a list
// whose cursor has moved three rows on, reading as a fact about
// whatever is selected now.
func TestModelPicker_FailureRowClearsOnTheNextMove(t *testing.T) {
	fail := func(t *testing.T) (Model, *modelPickerQuestion) {
		t.Helper()
		m, q := openModelPickerFixture(t)
		q.switching = "meta/llama-4"
		out, _ := m.Update(modelSwitchedMsg{
			gen: m.sessionGen, id: "meta/llama-4", err: errors.New("provider unreachable"),
		})
		m = out.(Model)
		if q.fail.rows() != 1 {
			t.Fatal("the failed switch left no reason to clear")
		}
		return m, q
	}

	t.Run("a cursor step", func(t *testing.T) {
		m, q := fail(t)
		m.overlayStack.HandleKeyMsg(keyMsgFromStroke("down"), &m)
		if q.fail.rows() != 0 {
			t.Error("the reason survived a cursor move")
		}
	})

	t.Run("a filter edit", func(t *testing.T) {
		m, q := fail(t)
		typeIntoPicker(&m, "g")
		if q.fail.rows() != 0 {
			t.Error("the reason survived the list being narrowed under it")
		}
	})

	t.Run("esc does not, because the picker is going away", func(t *testing.T) {
		m, q := fail(t)
		m.overlayStack.HandleKeyMsg(keyMsgFromStroke("esc"), &m)
		if m.overlayStack.HasDialogs() {
			t.Fatal("esc did not close the picker")
		}
		if q.fail.rows() != 1 {
			t.Error("esc cleared the reason on a picker nobody will render again")
		}
	})
}

// TestModelPicker_ReplyForSomeoneElsesSwitchLeavesThePicker: esc
// mid-flight pops the picker, and the operator may have re-opened a
// fresh one before the reply landed. The switch still applies — the
// host call was committed the moment it left — but the fresh picker is
// not the question it answers.
func TestModelPicker_ReplyForSomeoneElsesSwitchLeavesThePicker(t *testing.T) {
	next := &bareAgent{id: "next"}
	m := NewModel(Options{Agent: &swapAgent{id: "cur", models: pickerModels()}})
	m.viewport.SetWidth(80)
	readyModelPicker(&m) // a fresh picker, in flight to nothing

	out, _ := m.Update(modelSwitchedMsg{gen: m.sessionGen, id: "meta/llama-4", agent: next})
	m = out.(Model)

	if !m.overlayStack.HasID(modelPickerDialogID) {
		t.Error("a reply the open picker did not issue closed it anyway")
	}
	if m.opts.Agent != Agent(next) {
		t.Errorf("the committed switch was dropped: %v", m.opts.Agent)
	}
	snap := m.history.Snapshot()
	if len(snap) == 0 || !strings.Contains(snap[len(snap)-1].Text, "switched to meta/llama-4") {
		t.Errorf("the switch landed silently: %+v", snap)
	}
}

// TestModelPicker_ResolverTellsTheTwoUnrenderableStatesApart is why
// the resolver closes over its question. Both states answer
// dismissed{dismissUnrenderable} — the answer vocabulary has one shape
// for "this could not be asked" and encoding which one as a fifth
// reason would put a fact about one picker into the vocabulary every
// question shares — and only one of them is worth a transcript row.
func TestModelPicker_ResolverTellsTheTwoUnrenderableStatesApart(t *testing.T) {
	t.Run("the host advertised nothing", func(t *testing.T) {
		m := NewModel(Options{Agent: &swapAgent{id: "cur"}})
		m.viewport.SetWidth(80)
		q := askModelPicker(&m, true)
		q.applyModels(nil, "")

		m.overlayStack.HandleKeyMsg(keyMsgFromStroke("down"), &m)
		if m.overlayStack.HasDialogs() {
			t.Error("an empty host list left the picker open")
		}
		snap := m.history.Snapshot()
		if len(snap) != 1 || !strings.Contains(snap[0].Text, "no models available") {
			t.Errorf("history = %v, want the 'no models available' system message", snap)
		}
	})

	t.Run("the agent is not a ModelSwapper", func(t *testing.T) {
		m := NewModel(Options{Agent: &bareAgent{id: "bare"}})
		m.viewport.SetWidth(80)
		askModelPicker(&m, false)

		m.overlayStack.HandleKeyMsg(keyMsgFromStroke("down"), &m)
		if m.overlayStack.HasDialogs() {
			t.Error("an unwired agent left the picker open")
		}
		if snap := m.history.Snapshot(); len(snap) != 0 {
			t.Errorf("an unwired agent wrote %v; it is not a host-list problem", snap)
		}
	})
}

// TestModelPicker_EscNeverStartsASwitch — including mid-flight, where
// esc is the operator declining to watch rather than cancelling
// anything. The host call already left; what must not happen is a
// second one.
func TestModelPicker_EscNeverStartsASwitch(t *testing.T) {
	agent := &swapAgent{id: "cur", models: pickerModels()}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)
	q := readyModelPicker(&m)
	q.switching = "meta/llama-4"

	m.overlayStack.HandleKeyMsg(keyMsgFromStroke("esc"), &m)
	if m.overlayStack.HasDialogs() {
		t.Error("esc left the picker open")
	}
	if len(agent.switchCalls) != 0 {
		t.Errorf("esc called SwitchModel: %v", agent.switchCalls)
	}
	if snap := m.history.Snapshot(); len(snap) != 0 {
		t.Errorf("esc wrote %v to the transcript", snap)
	}
}

// TestModelPicker_CursorSitsInTheFilterRow is the #105 / #123
// requirement on a typed surface: modalCursor answers (nil, true) for
// a question with no caret, so without one the picker would take input
// with no hardware caret and no IME anchor. Asserted through the
// composed frame, with CJK in the value so a rune-index measurement
// fails (#125). What the question answers on its own is
// question_external_test.go's.
func TestModelPicker_CursorSitsInTheFilterRow(t *testing.T) {
	for _, typed := range []string{"gpt", "日本語", "a日b"} {
		t.Run(typed, func(t *testing.T) {
			m, _ := openModelPickerFixture(t)
			typeIntoPicker(&m, typed)
			assertCursorFollows(t, m.View(), filterPromptRail+typed)
		})
	}
}

// TestModelPicker_HighlightsTheMatchedSpan: contiguous substring
// matching means the highlight is one span, so the row's plain text is
// unchanged and only the matched run carries an escape. Needs a real
// palette, hence a Model.
func TestModelPicker_HighlightsTheMatchedSpan(t *testing.T) {
	m, _ := openModelPickerFixture(t)
	typeIntoPicker(&m, "opus")
	rendered := m.overlayStack.Render(100, &m)
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
// of the list's allowance rather than added to the modal, so the modal
// is exactly as tall as it was before the filter existed.
//
// It used to hold only from nine rows up: below that modalBodyHeight
// stopped subtracting and returned minModalBodyRows, so the modal was
// taller than the terminal whatever chrome figure it was given and
// clipFrame took the footer off the bottom. Issue #142 sheds the
// margin and the floor below modalFullscreenBelow, so the range now
// runs down to three rows — title, one body row, footer hint. Two rows
// drops the body entirely and is covered by TestModalFit_ShortTerminal.
func TestModelPicker_BodyFitsTheTerminal(t *testing.T) {
	for _, h := range []int{3, 4, 6, 8, 9, 10, 12, 13, 14, 24, 50} {
		m, _ := openModelPickerFixture(t)
		m.height = h
		rendered := ansi.Strip(m.overlayStack.Render(100, &m))
		if got := strings.Count(rendered, "\n") + 1; got > h {
			t.Errorf("height %d: picker is %d rows tall\n%s", h, got, rendered)
		}
		// The filter row is never the thing that gets windowed away:
		// it is what the operator needs to edit to get back. Probed on
		// the prompt rail as well as the placeholder because the footer
		// says "type to filter" too, and matching the phrase alone let
		// the footer stand in for the row at heights that have no body
		// at all (issue #230).
		if h >= modalEdgeRows+3 &&
			!strings.Contains(rendered, filterPromptRail+filterPlaceholder) {
			t.Errorf("height %d: filter row scrolled out of the body\n%s", h, rendered)
		}
	}
}
