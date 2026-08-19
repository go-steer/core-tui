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

// The Asker end to end: a real asker, a real model, and the answer
// arriving on the channel a host's Ask call is parked on (issue #255).
// question_ask_test.go owns the widgets; this file owns the wiring
// between them and the host.

package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// askReply is both halves of what Ask handed back. The error matters
// on the refusal path, where it IS the answer.
type askReply struct {
	result AskResult
	err    error
}

// askFlowFor puts req to a real asker wired into a real model, the way
// a host's ask-the-user tool would, and returns the model parked on the
// question plus the channel Ask answers on. The committing keys are
// still inside modalInputGrace — call pastGraceFor before pressing one.
func askFlowFor(t *testing.T, req AskRequest) (model, chan askReply) {
	t.Helper()
	m, replies, _ := askFlowRaw(t, req)
	if m.openAsk() == nil {
		t.Fatal("setup: askRequestMsg did not open the question")
	}
	return m, replies
}

// askFlowRaw is askFlowFor without the assertion that a modal opened,
// for the refusal tests, where not opening one is the behaviour under
// test. It also hands back the Cmd the branch returned, which is where
// the re-armed listener and the render kick live.
func askFlowRaw(t *testing.T, req AskRequest) (model, chan askReply, tea.Cmd) {
	t.Helper()
	a := NewAsker().(*asker)
	replies := make(chan askReply, 1)
	go func() {
		r, err := a.Ask(context.Background(), req)
		replies <- askReply{result: r, err: err}
	}()
	if _, ok := a.nextRequest(context.Background()); !ok {
		t.Fatal("setup: nextRequest returned !ok with a pending request")
	}

	m := newModel(Options{Asker: a})
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = out.(model)
	out, cmd := m.Update(askRequestMsg{req: req})
	return out.(model), replies, cmd
}

// awaitAsk reads what reached the host, or fails.
func awaitAsk(t *testing.T, replies chan askReply) askReply {
	t.Helper()
	select {
	case r := <-replies:
		return r
	case <-time.After(time.Second):
		t.Fatal("no result reached the host")
		return askReply{}
	}
}

// The whole path: an agent asks, a modal opens, the operator picks a
// row, and the ID they picked comes back on the call that is parked
// waiting for it.
func TestAsk_TheOperatorsChoiceReachesTheHost(t *testing.T) {
	m, replies := askFlowFor(t, twoChoices(AskChoice))
	m = pastGraceFor(m, askDialogID)

	out, _ := m.Update(keyPress("down"))
	m = out.(model)
	out, _ = m.Update(keyPress("enter"))
	m = out.(model)

	if m.openAsk() != nil {
		t.Error("the question stayed open after it was answered")
	}
	got := awaitAsk(t, replies)
	if got.err != nil {
		t.Fatalf("an answered question carried err = %v", got.err)
	}
	if got.result.Action != AskAnswered {
		t.Errorf("action = %v, want AskAnswered", got.result.Action)
	}
	if strings.Join(got.result.ChoiceIDs, ",") != "b" {
		t.Errorf("ChoiceIDs = %v, want the row the operator was on", got.result.ChoiceIDs)
	}
}

// Text arrives trimmed, on the same trip.
func TestAsk_TypedTextReachesTheHost(t *testing.T) {
	m, replies := askFlowFor(t, AskRequest{Kind: AskText, Prompt: "Name?"})
	m = pastGraceFor(m, askDialogID)

	for _, r := range "ada " {
		out, _ := m.Update(keyPress(string(r)))
		m = out.(model)
	}
	out, _ := m.Update(keyPress("enter"))
	m = out.(model)
	if m.openAsk() != nil {
		t.Error("the question is still up after it was answered")
	}

	if got := awaitAsk(t, replies); got.result.Text != "ada" {
		t.Errorf("Text = %q, want the typed value, trimmed", got.result.Text)
	}
}

// Decline and cancel are different answers — "I read this and I am
// saying no" against "I dismissed it without deciding" — and an agent
// may act on the difference. Both are reachable and they must not
// collapse into each other.
func TestAsk_DeclineAndCancelAreDifferentAnswers(t *testing.T) {
	cases := []struct {
		stroke string
		want   AskAction
	}{
		{"ctrl+d", AskDeclined},
		{"esc", AskCancelled},
	}
	for _, tc := range cases {
		t.Run(tc.stroke, func(t *testing.T) {
			m, replies := askFlowFor(t, twoChoices(AskChoice))
			m = pastGraceFor(m, askDialogID)

			out, _ := m.Update(keyPress(tc.stroke))
			m = out.(model)
			if m.openAsk() != nil {
				t.Fatalf("%s left the question open", tc.stroke)
			}
			if got := awaitAsk(t, replies).result.Action; got != tc.want {
				t.Errorf("%s reached the host as %v, want %v", tc.stroke, got, tc.want)
			}
		})
	}
}

// The keys the modal advertises are the keys it honors — on both
// surfaces that name them, the modal's own footer and the status
// footer hint. The legends are built per kind, so the promise is only
// as good as the surface that made it.
func TestAskModal_AdvertisesTheKeysItHonors(t *testing.T) {
	cases := []struct {
		name string
		req  AskRequest
		key  string
	}{
		{"choice", twoChoices(AskChoice), "ctrl+d decline"},
		{"multi", twoChoices(AskMultiChoice), "space toggle"},
		{"confirm", AskRequest{Kind: AskConfirm, Prompt: "Ship it?"}, "y yes"},
		{"text", AskRequest{Kind: AskText, Prompt: "Name?"}, "enter submit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := askFlowFor(t, tc.req)
			// The U+00A0 keyLegend binds a phrase with is a line-break
			// detail, not part of what was promised.
			plain := func(s string) string { return unbindLegend(ansi.Strip(s)) }
			if got := plain(m.overlayStack.render(m.width, &m)); !strings.Contains(got, tc.key) {
				t.Errorf("the modal footer does not offer %q:\n%s", tc.key, got)
			}
			if got := plain(m.footerHint()); !strings.Contains(got, tc.key) {
				t.Errorf("the footer hint does not offer %q: %s", tc.key, got)
			}
		})
	}
}

// R-PROMPT-1's refusal path, which is issue #209's lesson applied
// before the bug can be shipped a second time: a question this TUI
// cannot draw is refused with an error to the agent AND a row to the
// operator, and never as a decline nobody made.
func TestAsk_UnrenderableIsRefusedAndRecorded(t *testing.T) {
	cases := []struct {
		name   string
		req    AskRequest
		reason string
	}{
		{"chooser with no choices", AskRequest{Kind: AskChoice, Title: "pick"},
			"the question has no choices"},
		{"multi-select with no choices", AskRequest{Kind: AskMultiChoice},
			"the question has no choices"},
		{"kind this TUI does not render", AskRequest{Kind: AskKind(42)},
			"the question kind is not one this TUI renders"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, replies, cmd := askFlowRaw(t, tc.req)

			if m.openAsk() != nil {
				t.Error("a modal opened for a question the TUI cannot draw")
			}
			got := awaitAsk(t, replies)
			if got.result.Action == AskDeclined {
				t.Error("the TUI declined on the operator's behalf — nobody was asked")
			}
			if got.result.Action != AskCancelled {
				t.Errorf("action = %v, want AskCancelled", got.result.Action)
			}
			if !errors.Is(got.err, ErrAskUnsupported) {
				t.Errorf("err = %v, want it to match ErrAskUnsupported", got.err)
			}
			if !strings.Contains(fmt.Sprint(got.err), tc.reason) {
				t.Errorf("the error does not say which part could not be drawn: %v", got.err)
			}

			// The operator's half. It must not say the TUI declined:
			// an operator reading that has been told a refusal went out
			// over their name.
			last := lastSystemRow(t, m)
			if !strings.Contains(last, tc.reason) {
				t.Errorf("the system row does not give the reason: %s", last)
			}
			if strings.Contains(strings.ToLower(last), "declined") {
				t.Errorf("the row claims a decline the operator never made: %s", last)
			}
			if cmd == nil {
				t.Fatal("the refusal branch returned no Cmd — the listener is not re-armed")
			}
		})
	}
}

// AskLongText with no editor configured is the one unrenderable case
// that depends on the environment rather than on the request, so it
// gets its own run with the variables cleared.
func TestAsk_LongTextWithoutAnEditorIsRefused(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")

	m, replies, _ := askFlowRaw(t, AskRequest{Kind: AskLongText, Prompt: "Write it up"})
	if m.openAsk() != nil {
		t.Error("a modal opened with nothing to open the buffer in")
	}
	got := awaitAsk(t, replies)
	if !errors.Is(got.err, ErrAskUnsupported) {
		t.Errorf("err = %v, want it to match ErrAskUnsupported", got.err)
	}
	// The operator's row and the agent's error come out of one
	// function, so they cannot describe different failures. That
	// property is invisible in either caller alone.
	if !strings.Contains(lastSystemRow(t, m), "$VISUAL") {
		t.Errorf("the row does not say what is missing: %s", lastSystemRow(t, m))
	}
	if !strings.Contains(fmt.Sprint(got.err), "$VISUAL") {
		t.Errorf("the error does not say what is missing: %v", got.err)
	}
}

// The editor round trip: the buffer the operator saved arrives as the
// answer, through overlay.resolve rather than through a keystroke.
func TestAskEditor_SavedBufferAnswersTheQuestion(t *testing.T) {
	t.Setenv("VISUAL", "true")
	m, replies := askFlowFor(t, AskRequest{Kind: AskLongText, Prompt: "Write it up"})

	out, _ := m.Update(askEditorDoneMsg{text: "the long answer"})
	m = out.(model)
	if m.openAsk() != nil {
		t.Error("the question stayed open after the editor answered it")
	}
	got := awaitAsk(t, replies)
	if got.result.Action != AskAnswered || got.result.Text != "the long answer" {
		t.Errorf("host got %+v, want the saved buffer as an answer", got.result)
	}
}

// An editor that failed, or one quit without saving, is not an answer.
// The question stays open and says what happened — inventing an answer
// out of a crashed child process is the same class of mistake as
// declining on the operator's behalf.
func TestAskEditor_AFailedRoundTripLeavesTheQuestionOpen(t *testing.T) {
	t.Setenv("VISUAL", "true")
	cases := []struct {
		name string
		msg  askEditorDoneMsg
		want string
	}{
		{"editor failed to run", askEditorDoneMsg{err: errors.New("exit status 127")}, "127"},
		{"quit without writing", askEditorDoneMsg{}, "empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, replies := askFlowFor(t, AskRequest{Kind: AskLongText, Prompt: "Write it up"})

			out, _ := m.Update(tc.msg)
			m = out.(model)

			q, ok := m.openAsk().(*askEditorQuestion)
			if !ok {
				t.Fatalf("the question is %T, want it still open", m.openAsk())
			}
			if !strings.Contains(q.note, tc.want) {
				t.Errorf("the modal says %q, want it to mention %q", q.note, tc.want)
			}
			if !strings.Contains(ansi.Strip(q.Body(q.Width(100), 24, m.styles)), tc.want) {
				t.Error("the note does not reach the frame the operator reads")
			}
			select {
			case got := <-replies:
				t.Errorf("a failed editor answered the agent anyway: %+v", got)
			default:
			}
		})
	}
}

// A session switch has to answer the question, not forget it: the
// host's Ask call is parked on a channel whose only writer is about to
// be replaced. Cancelled, because the operator switched away instead
// of answering.
func TestAsk_SessionSwitchCancelsTheOpenQuestion(t *testing.T) {
	m, replies := askFlowFor(t, twoChoices(AskChoice))

	m.applySwitchTarget(&SwitchTarget{Agent: &bareAgent{id: "next"}})

	if m.openAsk() != nil {
		t.Error("the question survived the session switch")
	}
	if got := awaitAsk(t, replies).result.Action; got != AskCancelled {
		t.Errorf("the switch answered %v, want AskCancelled", got)
	}
}

// The switch must not leave two listeners on one channel. The
// superseded resolver returns nil on purpose and step 8 re-arms once —
// the failure this pins is invisible at runtime, because two consumers
// take alternate requests and the ones that reach the parked listener
// are never seen again.
func TestAsk_SessionSwitchReArmsTheListenerExactlyOnce(t *testing.T) {
	m, _ := askFlowFor(t, twoChoices(AskChoice))
	if cmd := askResolver(dismissed{Reason: dismissSuperseded}, &m); cmd != nil {
		t.Error("the superseded resolver re-armed a listener step 8 also re-arms")
	}
	fresh := NewAsker()
	m.applySwitchTarget(&SwitchTarget{Agent: &bareAgent{id: "next"}, Asker: fresh})
	if m.opts.Asker != fresh {
		t.Error("SwitchTarget.Asker did not replace the outgoing one")
	}
}

// A cancelled ctx unblocks the caller even though the modal is still
// on screen: the agent gave up, and the operator's eventual keystroke
// finds a flow with nobody on the other end, which dispatchResult
// absorbs. Pinned because the alternative — Ask parked forever on a
// question nobody will answer — is the failure this whole channel
// shape exists to avoid.
func TestAsk_ContextCancellationUnblocksTheCaller(t *testing.T) {
	a := NewAsker().(*asker)
	ctx, cancel := context.WithCancel(context.Background())
	replies := make(chan askReply, 1)
	go func() {
		r, err := a.Ask(ctx, twoChoices(AskChoice))
		replies <- askReply{result: r, err: err}
	}()
	if _, ok := a.nextRequest(context.Background()); !ok {
		t.Fatal("setup: nextRequest returned !ok with a pending request")
	}
	cancel()

	got := awaitAsk(t, replies)
	if !errors.Is(got.err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", got.err)
	}
	if got.result.Action != AskCancelled {
		t.Errorf("action = %v, want the inert AskCancelled", got.result.Action)
	}
	// And the late answer goes nowhere rather than panicking on a
	// channel with no reader.
	a.dispatchResult(AskResult{Action: AskAnswered}, nil)
}
