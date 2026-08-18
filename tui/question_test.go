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

// The overlay adapter: what happens between a question answering and
// a resolver running (issue #164 stage 1).
//
// The questions themselves are tested from outside the package
// (question_external_test.go) precisely because they need nothing
// from it. Everything here does need a *Model, which is the honest
// division: routing, exactly-once and teardown are the app's
// concerns, not the widget's.

package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// probeQuestion answers with whatever it was built with, on the first
// keystroke. Nothing else about it matters — the point is what the
// adapter does with the answer.
type probeQuestion struct {
	id  string
	ans answer
}

func (p *probeQuestion) ID() string                            { return p.id }
func (p *probeQuestion) Key(tea.KeyPressMsg) (answer, tea.Cmd) { return p.ans, nil }
func (p *probeQuestion) Title() string                         { return "probe" }
func (p *probeQuestion) Footer() string                        { return "esc cancel" }
func (p *probeQuestion) Width(int) int                         { return 40 }
func (p *probeQuestion) Body(int, int, styleSet) string        { return "probe body" }

// recordAnswers returns a resolver that appends every answer it is
// handed, so a test can count runs as well as inspect them.
func recordAnswers(into *[]answer) resolver {
	return func(a answer, _ *Model) tea.Cmd {
		*into = append(*into, a)
		return nil
	}
}

// TestOverlayAsk_KeystrokeAnswersAndPops: the whole routing contract
// in one keystroke — consumed, popped, resolved.
func TestOverlayAsk_KeystrokeAnswersAndPops(t *testing.T) {
	var got []answer
	m := Model{}
	m.overlayStack.ask(&probeQuestion{id: "probe", ans: chosen{ID: "x", Index: 3}}, askOperator, recordAnswers(&got))

	consumed, _ := m.overlayStack.handleKeyMsg(keyMsgFromStroke("enter"), &m)
	if !consumed {
		t.Error("an open question did not consume the keystroke")
	}
	if m.overlayStack.hasDialogs() {
		t.Error("an answered question stayed on the stack")
	}
	if len(got) != 1 {
		t.Fatalf("resolver ran %d times, want 1", len(got))
	}
	if want := (chosen{ID: "x", Index: 3}); got[0] != want {
		t.Errorf("resolver got %#v, want %#v", got[0], want)
	}
}

// TestOverlayAsk_StillAskingKeepsTheQuestion. A nil answer means the
// question is not finished; the keystroke is consumed anyway, because
// an open modal is exclusive.
func TestOverlayAsk_StillAskingKeepsTheQuestion(t *testing.T) {
	var got []answer
	m := Model{}
	m.overlayStack.ask(&probeQuestion{id: "probe"}, askOperator, recordAnswers(&got))

	consumed, _ := m.overlayStack.handleKeyMsg(keyMsgFromStroke("x"), &m)
	if !consumed {
		t.Error("an open question did not consume the keystroke")
	}
	if !m.overlayStack.hasID("probe") {
		t.Error("an unanswered question was popped")
	}
	if len(got) != 0 {
		t.Errorf("resolver ran before there was an answer: %#v", got)
	}
}

// TestOverlayResolveAll_TellsEveryQuestionWhy is the hole this
// mechanism exists to close: a session switch used to tear a modal
// down and leave whatever was waiting on it with no answer at all.
// Every reason the design names reaches the resolver intact.
func TestOverlayResolveAll_TellsEveryQuestionWhy(t *testing.T) {
	for _, reason := range []dismissReason{
		dismissEscape, dismissSuperseded, dismissShutdown, dismissUnrenderable,
	} {
		var first, second []answer
		m := Model{}
		m.overlayStack.ask(&probeQuestion{id: "one"}, askOperator, recordAnswers(&first))
		// A viewer with nothing to answer, in the middle of the
		// stack, to prove it is dropped rather than skipped over.
		m.overlayStack.open(newToolCallDialog(0))
		m.overlayStack.ask(&probeQuestion{id: "two"}, askOperator, recordAnswers(&second))

		m.overlayStack.resolveAll(reason, &m)

		if m.overlayStack.hasDialogs() {
			t.Errorf("reason %d: resolveAll left the stack populated", reason)
		}
		for name, got := range map[string][]answer{"one": first, "two": second} {
			if len(got) != 1 {
				t.Fatalf("reason %d: question %s resolved %d times, want 1", reason, name, len(got))
			}
			d, ok := got[0].(dismissed)
			if !ok {
				t.Fatalf("reason %d: question %s got %T, want dismissed", reason, name, got[0])
			}
			if d.Reason != reason {
				t.Errorf("question %s got reason %d, want %d", name, d.Reason, reason)
			}
		}
	}
}

// toldMsg is what a resolver schedules when it has to notify
// something outside the model that its question died.
type toldMsg struct{ id string }

// TestOverlayResolveAll_ReturnsWhatTheResolversScheduled. Teardown is
// not only a state change: the resolver for a permission prompt torn
// down by a session switch has to tell the gate, and it does that
// with a Cmd. resolveAll's caller is the only thing that can dispatch
// one, so dropping them would reintroduce the stranded-flow bug in a
// new place.
func TestOverlayResolveAll_ReturnsWhatTheResolversScheduled(t *testing.T) {
	m := Model{}
	if cmd := m.overlayStack.resolveAll(dismissShutdown, &m); cmd != nil {
		t.Errorf("resolveAll on an empty stack returned %v, want nil", cmd)
	}

	tell := func(id string) resolver {
		return func(answer, *Model) tea.Cmd {
			return func() tea.Msg { return toldMsg{id: id} }
		}
	}
	m.overlayStack.ask(&probeQuestion{id: "one"}, askOperator, tell("one"))
	m.overlayStack.ask(&probeQuestion{id: "two"}, askOperator, tell("two"))
	// A question with no resolver contributes nothing rather than a
	// nil Cmd in the batch.
	m.overlayStack.ask(&probeQuestion{id: "three"}, askOperator, nil)

	cmd := m.overlayStack.resolveAll(dismissShutdown, &m)
	if cmd == nil {
		t.Fatal("resolveAll dropped both resolvers' Cmds")
	}
	told := map[string]bool{}
	for _, msg := range drainBatch(t, cmd) {
		tm, ok := msg.(toldMsg)
		if !ok {
			t.Fatalf("resolveAll scheduled a %T", msg)
		}
		told[tm.id] = true
	}
	if len(told) != 2 || !told["one"] || !told["two"] {
		t.Errorf("resolveAll delivered %v, want both one and two", told)
	}
}

// TestOverlayResolveAll_NeverResolvesTwice. A question answered by a
// keystroke and then swept in the same frame — esc on the last modal
// while a session switch lands — must not run its resolver again. For
// the theme picker that second run would put back a theme the
// operator just committed.
func TestOverlayResolveAll_NeverResolvesTwice(t *testing.T) {
	var got []answer
	m := Model{}
	q := &probeQuestion{id: "probe", ans: chosen{ID: "kept"}}
	m.overlayStack.ask(q, askOperator, recordAnswers(&got))

	// Answer it, but put it back on the stack as if a teardown were
	// racing the pop.
	front := m.overlayStack.front().(*askedQuestion)
	m.overlayStack.handleKeyMsg(keyMsgFromStroke("enter"), &m)
	m.overlayStack.open(front)

	m.overlayStack.resolveAll(dismissSuperseded, &m)
	if len(got) != 1 {
		t.Fatalf("resolver ran %d times, want exactly 1: %#v", len(got), got)
	}
	if _, isChosen := got[0].(chosen); !isChosen {
		t.Errorf("the surviving answer is %#v, want the committed chosen", got[0])
	}
}

// TestAskedQuestion_CaretOnlyWhenTheQuestionHasOne. The adapter has
// to declare DialogCursor unconditionally — Go cannot implement an
// interface conditionally — so a question with no caret has to answer
// nil, which is what a dialog that did not implement cursorDialog at
// all used to produce.
func TestAskedQuestion_CaretOnlyWhenTheQuestionHasOne(t *testing.T) {
	m := Model{}
	m.styles = newStyles(true, Branding{})

	m.overlayStack.ask(&probeQuestion{id: "probe"}, askOperator, nil)
	if c := m.overlayStack.cursor(100, &m); c != nil {
		t.Errorf("a question with no text surface reported a caret at %+v", c)
	}

	m.overlayStack.close("probe")
	m.themeName = "default"
	askThemePicker(&m)
	if c := m.overlayStack.cursor(100, &m); c == nil {
		t.Error("the theme picker's filter row reported no caret")
	}
}

// TestAskedQuestion_WheelStepsTheListByOne. The adapter deliberately
// does not implement scrollDialog, so overlay.handleWheel falls
// through to synthesizing one up/down keystroke — which is what a
// list wants and what a scrolling body does not.
func TestAskedQuestion_WheelStepsTheListByOne(t *testing.T) {
	m := Model{}
	m.styles = newStyles(true, Branding{})
	m.themeName = "default"
	q := askThemePicker(&m)
	start := q.idx

	if consumed, _ := m.overlayStack.handleWheel(wheelScrollLines, &m); !consumed {
		t.Fatal("the question did not consume the wheel")
	}
	if q.idx != start+1 {
		t.Errorf("one wheel tick moved %d rows, want exactly 1", q.idx-start)
	}
}
