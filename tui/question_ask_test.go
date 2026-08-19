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
	"fmt"
	"strings"
	"testing"
)

// twoChoices is the request every list test starts from.
func twoChoices(kind AskKind) AskRequest {
	return AskRequest{
		Kind:   kind,
		Title:  "pick",
		Prompt: "Which one?",
		Choices: []AskOption{
			{ID: "a", Label: "Alpha", Description: "the first"},
			{ID: "b", Label: "Beta"},
		},
	}
}

// typeIntoAsk feeds word to a text question one rune at a time and
// fails if any of it ended the question.
func typeIntoAsk(t *testing.T, q *askTextQuestion, word string) {
	t.Helper()
	for _, r := range word {
		if ans, _ := q.Key(keyMsgFromStroke(string(r))); ans != nil {
			t.Fatalf("typing %q answered the question with %T", string(r), ans)
		}
	}
}

// Each kind gets the surface built for it. Asserted because
// newAskQuestion's default arm returns nil on purpose — a kind with no
// surface must be screened out by supportedAsk rather than rendered as
// something else — and a mis-routed kind would otherwise show up only
// as the wrong modal at runtime.
func TestNewAskQuestion_RoutesEachKindToItsSurface(t *testing.T) {
	t.Setenv("VISUAL", "true") // so AskLongText is renderable here
	cases := []struct {
		kind AskKind
		want string
	}{
		{AskChoice, "*tui.askListQuestion"},
		{AskMultiChoice, "*tui.askListQuestion"},
		{AskConfirm, "*tui.askListQuestion"},
		{AskText, "*tui.askTextQuestion"},
		{AskLongText, "*tui.askEditorQuestion"},
	}
	for _, tc := range cases {
		q := newAskQuestion(twoChoices(tc.kind))
		if got := fmt.Sprintf("%T", q); got != tc.want {
			t.Errorf("kind %v built %s, want %s", tc.kind, got, tc.want)
		}
	}
	if q := newAskQuestion(AskRequest{Kind: AskKind(99)}); q != nil {
		t.Errorf("an unknown kind built %T; it must be refused, not guessed", q)
	}
}

// Which answers re-arm the ask listener, and which must not. Same
// property, and the same failure mode, as the elicit resolver's: two
// armed listeners on one channel are two consumers taking alternate
// requests, and the ones that go to the parked listener are never seen
// again.
func TestAskResolver_ReArmsOnlyWhenTheFlowIsFree(t *testing.T) {
	cases := []struct {
		name   string
		ans    answer
		reArms bool
	}{
		{"chose a row", chosen{ID: "a"}, true},
		{"submitted a selection", selected{IDs: []string{"a"}}, true},
		{"submitted text", text{Value: "ada"}, true},
		{"declined", declined{}, true},
		{"escaped", dismissed{Reason: dismissEscape}, true},
		{"unrenderable", dismissed{Reason: dismissUnrenderable}, true},
		{"superseded by a session switch", dismissed{Reason: dismissSuperseded}, false},
		{"shutdown", dismissed{Reason: dismissShutdown}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(Options{Asker: NewAsker()})
			cmd := askResolver(tc.ans, &m)
			if got := cmd != nil; got != tc.reArms {
				t.Errorf("re-armed = %v, want %v", got, tc.reArms)
			}
		})
	}
}

// The single-select drives to an answer with no model, no Update and
// no tea.Program — the property stage 3 of #164 bought, spent here by
// the first surface built after it.
func TestAskChoice_DrivesToAnAnswerWithoutAModel(t *testing.T) {
	q := newAskListQuestion(twoChoices(AskChoice))

	body := q.Body(q.Width(100), 24, styleSet{})
	for _, want := range []string{"Which one?", "Alpha", "the first", "Beta"} {
		if !strings.Contains(body, want) {
			t.Errorf("the body does not show %q:\n%s", want, body)
		}
	}

	q.Key(keyMsgFromStroke("down"))
	ans, _ := q.Key(keyMsgFromStroke("enter"))
	c, ok := ans.(chosen)
	if !ok {
		t.Fatalf("enter answered %T, want chosen", ans)
	}
	if c.ID != "b" || c.Index != 1 {
		t.Errorf("chose %+v, want the row under the cursor (b, 1)", c)
	}
}

// Space ticks, enter submits, and the IDs come back in list order
// rather than in the order they were ticked — the only order both
// sides of the interface can see.
func TestAskMultiChoice_SubmitsTickedRowsInListOrder(t *testing.T) {
	req := twoChoices(AskMultiChoice)
	req.Choices = append(req.Choices, AskOption{ID: "c", Label: "Gamma"})
	q := newAskListQuestion(req)

	q.Key(keyMsgFromStroke("end"))   // c
	q.Key(keyMsgFromStroke("space")) // tick it first
	q.Key(keyMsgFromStroke("home"))  // a
	q.Key(keyMsgFromStroke("space"))

	if body := q.Body(q.Width(100), 24, styleSet{}); !strings.Contains(body, "[✓]") {
		t.Errorf("a ticked row draws no tick:\n%s", body)
	}

	ans, _ := q.Key(keyMsgFromStroke("enter"))
	s, ok := ans.(selected)
	if !ok {
		t.Fatalf("enter answered %T, want selected", ans)
	}
	if strings.Join(s.IDs, ",") != "a,c" {
		t.Errorf("submitted %v, want [a c] in list order", s.IDs)
	}
	if len(s.Indexes) != 2 || s.Indexes[0] != 0 || s.Indexes[1] != 2 {
		t.Errorf("submitted indexes %v, want [0 2]", s.Indexes)
	}
}

// A multi-select with nothing ticked is an answer: the operator read
// the list and none of it applied. Pinned because the tempting
// alternative — treating it as a decline — would put a word in their
// mouth that ctrl+d already says explicitly.
func TestAskMultiChoice_EmptySelectionIsAnAnswer(t *testing.T) {
	q := newAskListQuestion(twoChoices(AskMultiChoice))
	ans, _ := q.Key(keyMsgFromStroke("enter"))
	s, ok := ans.(selected)
	if !ok {
		t.Fatalf("enter answered %T, want selected", ans)
	}
	if len(s.IDs) != 0 {
		t.Errorf("submitted %v, want nothing ticked", s.IDs)
	}

	// The resolver's half of the same claim: Answered, not Declined.
	if got := askActionFor(t, s); got != AskAnswered {
		t.Errorf("an empty selection reached the host as %v, want AskAnswered", got)
	}
}

// The confirm's accelerators, and the answer they produce. "no" is an
// ANSWER — the question was put and the operator answered it — and it
// must not arrive as the decline that ctrl+d means.
func TestAskConfirm_YesAndNoAreBothAnswers(t *testing.T) {
	for _, tc := range []struct{ stroke, id string }{{"y", confirmYesID}, {"n", confirmNoID}} {
		q := newAskListQuestion(AskRequest{Kind: AskConfirm, Prompt: "Ship it?"})
		ans, _ := q.Key(keyMsgFromStroke(tc.stroke))
		c, ok := ans.(chosen)
		if !ok {
			t.Fatalf("%q answered %T, want chosen", tc.stroke, ans)
		}
		if c.ID != tc.id {
			t.Errorf("%q chose %q, want %q", tc.stroke, c.ID, tc.id)
		}
		if got := askActionFor(t, c); got != AskAnswered {
			t.Errorf("%q reached the host as %v, want AskAnswered", tc.stroke, got)
		}
	}
}

// Both options are on screen and the cursor moves between them
// sideways, which is the whole reason the confirm renders as a row
// rather than as a two-entry list.
func TestAskConfirm_RendersBothOptionsAndMovesSideways(t *testing.T) {
	q := newAskListQuestion(AskRequest{Kind: AskConfirm, Prompt: "Ship it?"})
	body := q.Body(q.Width(100), 24, styleSet{})
	if !strings.Contains(body, "Yes") || !strings.Contains(body, "No") {
		t.Errorf("the confirm row does not show both options:\n%s", body)
	}
	if strings.Count(body, "\n") != 2 {
		t.Errorf("the confirm drew %d body rows, want prompt + blank + row:\n%s",
			strings.Count(body, "\n")+1, body)
	}
	q.Key(keyMsgFromStroke("right"))
	ans, _ := q.Key(keyMsgFromStroke("enter"))
	if c, _ := ans.(chosen); c.ID != confirmNoID {
		t.Errorf("right then enter chose %q, want %q", c.ID, confirmNoID)
	}
}

// The one-line input: type, submit, and the value arrives trimmed.
func TestAskText_SubmitsTheTrimmedValue(t *testing.T) {
	q := newAskTextQuestion(AskRequest{Kind: AskText, Prompt: "Name?", Initial: "  ada  "})
	typeIntoAsk(t, q, "!")
	ans, _ := q.Key(keyMsgFromStroke("enter"))
	tx, ok := ans.(text)
	if !ok {
		t.Fatalf("enter answered %T, want text", ans)
	}
	if tx.Value != "ada  !" {
		t.Errorf("submitted %q, want the seeded value plus the typed rune, trimmed", tx.Value)
	}
}

// The caret sits inside the input box, which is below however many
// rows the prompt wrapped to. The arithmetic lives in two places by
// necessity — Body composes, Cursor measures — so this is the
// assertion that keeps them agreeing.
func TestAskText_CaretFollowsTheWrappedPrompt(t *testing.T) {
	const width = 60
	short := newAskTextQuestion(AskRequest{Kind: AskText, Prompt: "Name?"})
	long := newAskTextQuestion(AskRequest{
		Kind:   AskText,
		Prompt: strings.Repeat("a long question that has to wrap somewhere ", 4),
	})
	for _, q := range []*askTextQuestion{short, long} {
		q.Body(width, 24, styleSet{})
	}

	shortY, longY := short.Cursor(width).Y, long.Cursor(width).Y
	if shortY >= longY {
		t.Errorf("caret rows: short prompt %d, wrapped prompt %d — the wrapped one must sit lower",
			shortY, longY)
	}
	// And the row it claims is the row the input actually renders on.
	rows := strings.Split(long.Body(width, 24, styleSet{}), "\n")
	if want := longY - modalBodyTop; want < 0 || want >= len(rows) {
		t.Fatalf("caret row %d is outside the %d-row body", want, len(rows))
	}
}

// Every surface refuses on esc and declines on ctrl+d, and the two
// stay distinct all the way to the host. The keys are per-surface code
// — three Key methods — so the table is the only thing that would
// catch one of them growing an opinion of its own.
func TestAskSurfaces_EscapeAndDeclineAreUniform(t *testing.T) {
	t.Setenv("VISUAL", "true")
	for _, kind := range []AskKind{AskChoice, AskMultiChoice, AskConfirm, AskText, AskLongText} {
		q := newAskQuestion(twoChoices(kind))

		esc, _ := q.Key(keyMsgFromStroke("esc"))
		if d, ok := esc.(dismissed); !ok || d.Reason != dismissEscape {
			t.Errorf("kind %v answered esc with %#v, want dismissed{dismissEscape}", kind, esc)
		}
		if got := askActionFor(t, esc); got != AskCancelled {
			t.Errorf("kind %v: esc reached the host as %v, want AskCancelled", kind, got)
		}

		dec, _ := q.Key(keyMsgFromStroke("ctrl+d"))
		if _, ok := dec.(declined); !ok {
			t.Errorf("kind %v answered ctrl+d with %#v, want declined", kind, dec)
		}
		if got := askActionFor(t, dec); got != AskDeclined {
			t.Errorf("kind %v: ctrl+d reached the host as %v, want AskDeclined", kind, got)
		}
	}
}

// The grace window holds the keys that answer and nothing else
// (issue #95). A keystroke already in the terminal buffer when the
// question appears must not spend the operator's answer, but it must
// also not be swallowed on its way into a text field they were
// already typing in.
func TestAskSurfaces_OnlyAnsweringKeysAreHeld(t *testing.T) {
	t.Setenv("VISUAL", "true")
	held := map[AskKind][]string{
		AskChoice:      {"enter", "ctrl+d"},
		AskMultiChoice: {"enter", "ctrl+d"},
		AskConfirm:     {"enter", "ctrl+d", "y", "n"},
		AskText:        {"enter", "ctrl+d"},
		AskLongText:    {"enter", "ctrl+d"},
	}
	live := []string{"up", "down", "space", "esc", "a"}
	for kind, keys := range held {
		g, ok := newAskQuestion(twoChoices(kind)).(gracedQuestion)
		if !ok {
			t.Fatalf("kind %v is not graced; an agent-opened question must be", kind)
		}
		for _, k := range keys {
			if !g.Commits(keyMsgFromStroke(k)) {
				t.Errorf("kind %v does not hold %q, which answers the agent", kind, k)
			}
		}
		for _, k := range live {
			if kind == AskConfirm && (k == "y" || k == "n") {
				continue
			}
			if g.Commits(keyMsgFromStroke(k)) {
				t.Errorf("kind %v holds %q, which answers nothing", kind, k)
			}
		}
	}
}

// A chooser with no choices is screened out before it opens, and the
// arm inside the widget is the backstop. It reports unrenderable
// rather than escape: the operator did not dismiss anything.
func TestAskList_EmptyChoicesAreUnrenderableNotEscaped(t *testing.T) {
	if supportedAsk(AskRequest{Kind: AskChoice}) {
		t.Error("a chooser with no choices passed the screen")
	}
	q := newAskListQuestion(AskRequest{Kind: AskChoice})
	ans, _ := q.Key(keyMsgFromStroke("enter"))
	d, ok := ans.(dismissed)
	if !ok || d.Reason != dismissUnrenderable {
		t.Errorf("enter on an empty chooser answered %#v, want dismissed{dismissUnrenderable}", ans)
	}
}

// The title names the sub-agent that asked, so an operator with
// several in flight can tell which one is waiting on them.
func TestAskTitle_NamesTheAskingAgent(t *testing.T) {
	if got := askTitle(AskRequest{}); got != "Agent question" {
		t.Errorf("a title-less request is headed %q, want a default", got)
	}
	got := askTitle(AskRequest{Title: "pick one", Source: "reviewer"})
	if !strings.Contains(got, "reviewer") || !strings.Contains(got, "pick one") {
		t.Errorf("title = %q, want it to name both the sub-agent and the question", got)
	}
}

// $VISUAL wins over $EDITOR — the convention is that VISUAL is the
// full-screen editor and EDITOR the line-oriented fallback, and a long
// answer is the full-screen case. A value with arguments splits into
// argv rather than reaching a shell.
func TestEditorArgv_PrefersVisualAndSplitsArguments(t *testing.T) {
	t.Setenv("EDITOR", "ed")
	t.Setenv("VISUAL", "code -w")
	if got := strings.Join(editorArgv(), "|"); got != "code|-w" {
		t.Errorf("editorArgv = %q, want code|-w", got)
	}
	t.Setenv("VISUAL", "   ")
	if got := strings.Join(editorArgv(), "|"); got != "ed" {
		t.Errorf("a blank VISUAL should fall through to EDITOR, got %q", got)
	}
	t.Setenv("EDITOR", "")
	if got := editorArgv(); got != nil {
		t.Errorf("editorArgv = %v with neither set, want nil", got)
	}
}

// Every way the launch can fail before the editor runs comes back as a
// message rather than as silence: the question is open and the
// operator is waiting on it, so a launch that never happened has to
// say so or the modal reads as hung.
func TestAskEditorCmd_ReportsAFailureToLaunch(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	msg := askEditorCmd("draft")()
	done, ok := msg.(askEditorDoneMsg)
	if !ok {
		t.Fatalf("a launch with no editor produced %T, want askEditorDoneMsg", msg)
	}
	if done.err == nil {
		t.Error("the failure carries no reason")
	}
}

// The preview shows the head of the buffer and says how much it is not
// showing. The modal is a staging area, not a viewer.
func TestAskEditor_PreviewElidesTheTail(t *testing.T) {
	q := newAskEditorQuestion(AskRequest{Kind: AskLongText, Initial: "one\ntwo"})
	if body := q.Body(72, 24, styleSet{}); !strings.Contains(body, "one") ||
		!strings.Contains(body, "two") {
		t.Errorf("a short draft is not shown whole:\n%s", body)
	}

	long := make([]string, 0, 20)
	for i := range 20 {
		long = append(long, fmt.Sprintf("line %d", i))
	}
	q = newAskEditorQuestion(AskRequest{Kind: AskLongText, Initial: strings.Join(long, "\n")})
	body := q.Body(72, 24, styleSet{})
	if !strings.Contains(body, "line 0") {
		t.Errorf("the preview does not start at the top:\n%s", body)
	}
	if strings.Contains(body, "line 19") {
		t.Errorf("the preview shows the whole 20-line draft:\n%s", body)
	}
	if !strings.Contains(body, "12 more lines") {
		t.Errorf("the preview does not say what it is hiding:\n%s", body)
	}
}

// askActionFor runs the resolver over an answer and reports what
// reached the host, which is the only place the widget's vocabulary
// and the interface's vocabulary meet.
func askActionFor(t *testing.T, ans answer) AskAction {
	t.Helper()
	a := NewAsker().(*asker)
	flow := askFlow{response: make(chan askResponse, 1)}
	a.pending = &flow
	m := newModel(Options{Asker: a})
	askResolver(ans, &m)
	select {
	case r := <-flow.response:
		return r.result.Action
	default:
		t.Fatalf("%T reached the host as nothing at all", ans)
		return AskCancelled
	}
}
