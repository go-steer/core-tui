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

// Session picker tests, in-package half.
//
// The split follows design-question-dialogs.md §8, the same way the
// model picker's does. Anything that needs a *Model is here: the
// resolver's effects, the three Update-side async arms, the action
// row's hand-off to its text input, and the composed frame, which
// reaches the widget through the overlay because a question renders no
// chrome of its own. Behaviour the WIDGET owns — what the filter
// matches, what Enter commits to, which states own the caret — is in
// question_external_test.go, driven with no Model at all, which is the
// property the migration was for.
//
// The /switch built-in that opens the picker is covered from
// switch_test.go.

package tui

import (
	"errors"
	"fmt"
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

// askSessionPicker puts a picker on the stack with its real resolver —
// the pairing openSessionPicker does, minus the host Cmd, so a test can
// choose what the snapshot contains.
func askSessionPicker(m *Model, wired bool) *sessionPickerQuestion {
	q := newSessionPickerQuestion(wired)
	m.overlayStack.ask(q, sessionPickerResolver(q))
	return q
}

func openSessionPickerFixture(t *testing.T) (Model, *sessionPickerQuestion) {
	t.Helper()
	return openSessionPickerSized(t, 30, pickerSessions())
}

// openSessionPickerSized is the fixture with the terminal geometry and
// the session list under the test's control — which is what the
// windowing tests need, since the window only engages once the body is
// taller than modalBodyHeight.
func openSessionPickerSized(t *testing.T, height int, sessions []SessionInfo) (Model, *sessionPickerQuestion) {
	t.Helper()
	m := NewModel(Options{Agent: &switchAgent{id: "cur", sessions: sessions}})
	m.styles = NewStylesWithTheme(true, goldenTheme())
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: height})
	m = out.(Model)
	q := askSessionPicker(&m, true)
	q.applySessions(sessions)
	return m, q
}

func sessionIDs(sessions []SessionInfo) []string {
	out := make([]string, len(sessions))
	for i, s := range sessions {
		out[i] = s.ID
	}
	return out
}

// overlayIDs names what is on the modal stack, bottom to top. The
// public surface answers "is this ID open" and "what is on top", which
// is all production code needs; a test asserting that a path opened
// exactly one dialog has to see duplicates, and duplicates are
// invisible through HasID.
func overlayIDs(o *Overlay) []string {
	out := make([]string, len(o.dialogs))
	for i, d := range o.dialogs {
		out[i] = d.ID()
	}
	return out
}

// sessionPickerLines renders the picker through the overlay and returns
// the lines inside its box edge, ANSI stripped and the right-hand
// padding removed — fitRow pads every windowed row out to the content
// width, and that padding is noise for a "which lines are on screen"
// assertion, as is the edge glyph now sitting on both ends of it.
func sessionPickerLines(m *Model) []string {
	return modalContentLines(m.overlayStack.Render(100, m))
}

// lineWith returns the index of the first line containing want, or -1.
func lineWith(lines []string, want string) int {
	for i, ln := range lines {
		if strings.Contains(ln, want) {
			return i
		}
	}
	return -1
}

// requestedSession drains the Cmd Enter handed back and returns the ID
// the picker named, failing when the batch carries nothing of the sort.
func requestedSession(t *testing.T, msgs []tea.Msg) string {
	t.Helper()
	for _, msg := range msgs {
		if req, ok := msg.(sessionSwitchRequestedMsg); ok {
			return req.ID
		}
	}
	t.Fatalf("no sessionSwitchRequestedMsg in %#v", msgs)
	return ""
}

// ---------------------------------------------------------------
// Enter is a commit, not an answer (#164 stage 3)
// ---------------------------------------------------------------

// TestSessionPicker_EnterDoesNotAnswer is the shape this picker shares
// with the model picker, seen from the stack rather than the widget:
// Enter commits to a session but does not end the question, so the
// overlay must still be holding it when the frame settles — with the
// progress line on it, since a list that freezes for as long as the
// host takes reads as a hang.
func TestSessionPicker_EnterDoesNotAnswer(t *testing.T) {
	m, q := openSessionPickerFixture(t)
	typeIntoPicker(&m, "prod")

	// Straight through the Overlay, and the reply deliberately NOT
	// pumped: what is being asserted is the state between the keystroke
	// and the host answering, which is the whole state pressPicker
	// exists to skip past.
	consumed, cmd := m.overlayStack.HandleKeyMsg(keyMsgFromStroke("enter"), &m)
	if !consumed {
		t.Error("enter was not consumed")
	}
	if cmd == nil {
		t.Fatal("enter scheduled nothing; the attach would never happen")
	}
	if !m.overlayStack.HasID(sessionPickerDialogID) {
		t.Fatal("enter popped the picker; the attach has not landed yet")
	}
	if q.switching != "prod-042" {
		t.Errorf("switching = %q, want the filtered row prod-042", q.switching)
	}
	body := ansi.Strip(m.overlayStack.Render(100, &m))
	if !strings.Contains(body, "attaching to prod-042") {
		t.Errorf("the picker is not showing progress:\n%s", body)
	}
}

// TestSessionPicker_RequestReadsTheLiveSwitcherAndGen is why Enter
// names a session instead of calling the host itself. Both the switcher
// and the generation can move while the picker is open — a /model
// switch replaces m.opts.Agent inside one session — so the request has
// to be resolved when it lands, not when the picker opened.
func TestSessionPicker_RequestReadsTheLiveSwitcherAndGen(t *testing.T) {
	m, q := openSessionPickerFixture(t)
	q.idx = 3 // prod-042

	_, cmd := m.overlayStack.HandleKeyMsg(keyMsgFromStroke("enter"), &m)
	id := requestedSession(t, drainBatch(t, cmd))

	// The agent is replaced AFTER Enter and BEFORE the request lands.
	next := &switchAgent{id: "next", sessions: pickerSessions()}
	m.opts.Agent = next

	out, follow := m.Update(sessionSwitchRequestedMsg{ID: id})
	m = out.(Model)
	drainBatch(t, follow)

	if len(next.switchCalls) != 1 || next.switchCalls[0] != "prod-042" {
		t.Errorf("the request went to the wrong agent: %v", next.switchCalls)
	}
}

// TestSessionPicker_StaleRequestIsDropped: escape after Enter and the
// request has nobody to belong to.
func TestSessionPicker_StaleRequestIsDropped(t *testing.T) {
	m, q := openSessionPickerFixture(t)
	q.idx = 3
	_, cmd := m.overlayStack.HandleKeyMsg(keyMsgFromStroke("enter"), &m)
	msgs := drainBatch(t, cmd)
	pressPicker(t, &m, "esc")

	out, _ := m.Update(sessionSwitchRequestedMsg{ID: requestedSession(t, msgs)})
	m = out.(Model)

	agent := m.opts.Agent.(*switchAgent)
	if len(agent.switchCalls) != 0 {
		t.Errorf("a request outliving its picker still called the host: %v", agent.switchCalls)
	}
}

// TestSessionPicker_AttachLandingAnswersThePicker is the other half of
// EnterDoesNotAnswer: the reply is what ends the question, through
// Overlay.resolve rather than a bare Close.
func TestSessionPicker_AttachLandingAnswersThePicker(t *testing.T) {
	m, q := openSessionPickerFixture(t)
	q.switching = "prod-042"

	// The Cmd is deliberately dropped rather than drained: a successful
	// attach batches applySwitchTarget's event listener into it, and
	// that Cmd blocks on the new agent's channel until the program ends.
	out, _ := m.Update(sessionSwitchedMsg{
		gen:    m.sessionGen,
		id:     "prod-042",
		target: SwitchTarget{Agent: &bareAgent{id: "attached"}},
	})
	m = out.(Model)

	if m.overlayStack.HasID(sessionPickerDialogID) {
		t.Error("the attach landed and the picker is still up")
	}
	if a, ok := m.opts.Agent.(*bareAgent); !ok || a.id != "attached" {
		t.Errorf("agent = %#v, want the attached one", m.opts.Agent)
	}
}

// TestSessionPicker_FailedAttachKeepsTheListUp is a deliberate
// behaviour change and it matches the model picker's: the question was
// never answered, so the picker stays on its list. The operator's next
// move after "endpoint unreachable" is almost always the next session
// down, and closing would make them re-run /switch to get back to a
// list they were already looking at.
func TestSessionPicker_FailedAttachKeepsTheListUp(t *testing.T) {
	cases := []struct {
		name string
		msg  sessionSwitchedMsg
		want string
	}{
		{
			name: "host error",
			msg:  sessionSwitchedMsg{id: "prod-042", err: errors.New("endpoint unreachable")},
			want: "endpoint unreachable",
		},
		{
			name: "nil agent",
			msg:  sessionSwitchedMsg{id: "prod-042", target: SwitchTarget{}},
			want: "nil Agent",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, q := openSessionPickerFixture(t)
			q.switching = "prod-042"
			msg := tc.msg
			msg.gen = m.sessionGen

			out, _ := m.Update(msg)
			m = out.(Model)

			if !m.overlayStack.HasID(sessionPickerDialogID) {
				t.Fatal("a failed attach closed the picker")
			}
			if q.switching != "" {
				t.Errorf("switching = %q, want the list usable again", q.switching)
			}
			snap := m.history.Snapshot()
			if len(snap) != 1 || !strings.Contains(snap[0].Text, tc.want) {
				t.Errorf("history = %v, want a row mentioning %q", snap, tc.want)
			}
			// Back on the list, not stuck on the progress line.
			body := ansi.Strip(m.overlayStack.Render(100, &m))
			if !strings.Contains(body, "prod incident") {
				t.Errorf("the list did not come back:\n%s", body)
			}
		})
	}
}

// TestSessionPicker_FailedAttachSaysWhyOnThePicker is the model
// picker's twin, and issue #245. Keeping the list up is only the right
// call if the operator can tell that something happened: the reason
// applySessionSwitch writes lands in the transcript, which is BEHIND
// this modal, so without a row on the picker itself a failed attach is
// Enter, a pause, and the same list.
func TestSessionPicker_FailedAttachSaysWhyOnThePicker(t *testing.T) {
	cases := []struct {
		name string
		msg  sessionSwitchedMsg
		want string
	}{
		{
			name: "host error",
			msg:  sessionSwitchedMsg{id: "prod-042", err: errors.New("endpoint unreachable")},
			want: "endpoint unreachable",
		},
		{
			name: "nil agent",
			msg:  sessionSwitchedMsg{id: "prod-042", target: SwitchTarget{}},
			want: "SessionSwitcher returned nil Agent",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, q := openSessionPickerFixture(t)
			q.switching = "prod-042"
			msg := tc.msg
			msg.gen = m.sessionGen

			out, _ := m.Update(msg)
			m = out.(Model)

			lines := sessionPickerLines(&m)
			at := lineWith(lines, tc.want)
			if at < 0 {
				t.Fatalf("the frame does not say why the attach failed:\n%s",
					strings.Join(lines, "\n"))
			}
			if !strings.Contains(lines[at], GlyphWarn) {
				t.Errorf("the reason row is missing its warn glyph: %q", lines[at])
			}
			if last := lineWith(lines, "prod incident"); last < 0 || last > at {
				t.Errorf("the reason is not under the list:\n%s", strings.Join(lines, "\n"))
			}
		})
	}
}

// TestSessionPicker_FailureRowComesOutOfTheList. The row has to be paid
// for. A modal composed one row taller than the height it was budgeted
// against does not overflow visibly — clipFrame takes the bottom off,
// which on this modal is the footer telling the operator how to close
// it. So the row comes out of the list window, and the composed body is
// the same height with the reason as without.
func TestSessionPicker_FailureRowComesOutOfTheList(t *testing.T) {
	const height = 24
	m, q := openSessionPickerSized(t, height, manySessions(40))
	before := sessionPickerLines(&m)

	q.fail.set("endpoint unreachable")
	after := sessionPickerLines(&m)

	if len(after) != len(before) {
		t.Errorf("the modal grew from %d rows to %d; the failure row was not "+
			"charged to the list window:\n%s", len(before), len(after), strings.Join(after, "\n"))
	}
	if lineWith(after, "endpoint unreachable") < 0 {
		t.Fatalf("the reason is not on screen at all:\n%s", strings.Join(after, "\n"))
	}
	// It came out of the LIST, not out of the chrome: the footer sits on
	// the row it always did.
	if got, want := lineWith(after, "esc cancel"), lineWith(before, "esc cancel"); got != want {
		t.Errorf("the footer moved from row %d to row %d:\n%s", want, got, strings.Join(after, "\n"))
	}
	// And it is the LAST list row that gave way, not one in the middle.
	if lineWith(after, "sess-005") >= 0 {
		t.Errorf("the list did not give up its bottom row:\n%s", strings.Join(after, "\n"))
	}
}

// TestSessionPicker_ReplyForSomeoneElsesAttachLeavesThePicker: a reply
// the current picker did not issue must not close it. Esc pops the
// picker mid-flight, and the operator can re-open a fresh one before
// the first reply lands.
func TestSessionPicker_ReplyForSomeoneElsesAttachLeavesThePicker(t *testing.T) {
	m, q := openSessionPickerFixture(t)
	q.switching = "sess-002"

	out, _ := m.Update(sessionSwitchedMsg{
		gen:    m.sessionGen,
		id:     "prod-042", // a different attach
		target: SwitchTarget{Agent: &bareAgent{id: "attached"}},
	})
	m = out.(Model)

	if !m.overlayStack.HasID(sessionPickerDialogID) {
		t.Error("a reply for another attach closed this picker")
	}
	if q.switching != "sess-002" {
		t.Errorf("switching = %q, want the picker's own attach still pending", q.switching)
	}
	// The attach itself still applies — it was committed the moment it
	// left, and nothing about the picker's fate changes that.
	if a, ok := m.opts.Agent.(*bareAgent); !ok || a.id != "attached" {
		t.Errorf("agent = %#v, want the attach applied anyway", m.opts.Agent)
	}
}

// TestSessionPicker_CurrentRowAnswersImmediately: picking the session
// you are already attached to is a real choice with nothing to carry
// out, so it ends the question in the same frame and makes no host
// call. Notably it must NOT go down the attach path, which wipes the
// transcript.
func TestSessionPicker_CurrentRowAnswersImmediately(t *testing.T) {
	m, q := openSessionPickerFixture(t)
	m.history.Append(Message{Role: RoleUser, Text: "keep me"})
	q.idx = 0 // sess-001, Current

	pressPicker(t, &m, "enter")

	if m.overlayStack.HasID(sessionPickerDialogID) {
		t.Error("picking the attached row left the picker open")
	}
	if q.switching != "" {
		t.Errorf("picking the attached row started a switch to %q", q.switching)
	}
	agent := m.opts.Agent.(*switchAgent)
	if len(agent.switchCalls) != 0 {
		t.Errorf("picking the attached row called the host: %v", agent.switchCalls)
	}
	if snap := m.history.Snapshot(); len(snap) != 1 || snap[0].Text != "keep me" {
		t.Errorf("history = %v, want the transcript untouched", snap)
	}
}

// ---------------------------------------------------------------
// The action row (issue #56)
// ---------------------------------------------------------------

// TestSessionPicker_EnterOnAFilteredActionRow: filtering down to the
// action row and pressing Enter must stack the text-input dialog on top
// rather than trying to attach to a session ID that names no session —
// and the Open has to come from Update, because Overlay pops the front
// dialog after Key returns.
func TestSessionPicker_EnterOnAFilteredActionRow(t *testing.T) {
	m, q := openSessionPickerFixture(t)
	typeIntoPicker(&m, "endpoint")
	if got := sessionIDs(q.rows()); len(got) != 1 || got[0] != "attach" {
		t.Fatalf("filter matched %v, want just the action row", got)
	}

	pressPicker(t, &m, "enter")

	if !m.overlayStack.HasID(sessionInputDialogID) {
		t.Error("enter on the filtered action row did not open its text input")
	}
	if !m.overlayStack.HasID(sessionPickerDialogID) {
		t.Error("the picker should stay open underneath so esc returns to the list")
	}
	if q.switching != "" {
		t.Errorf("an action row started a switch to %q", q.switching)
	}
}

// TestSessionPicker_ActionRowDoesNotStackTwoInputs: two Enters in
// flight are one input, the same guard applySwitchLookup uses.
func TestSessionPicker_ActionRowDoesNotStackTwoInputs(t *testing.T) {
	m, _ := openSessionPickerFixture(t)
	row := pickerSessions()[4]
	for range 2 {
		out, _ := m.Update(sessionInputRequestedMsg{Row: row})
		m = out.(Model)
	}

	var inputs int
	for _, id := range overlayIDs(&m.overlayStack) {
		if id == sessionInputDialogID {
			inputs++
		}
	}
	if inputs != 1 {
		t.Errorf("%d session-input dialogs on the stack, want 1", inputs)
	}
}

// TestSessionInput_SuccessAnswersThePickerUnderneath is the reason
// dialog_sessioninput.go goes through Overlay.resolve instead of
// Overlay.Close: the picker is a question now, and closing it by ID
// would pop it with its resolver never run.
func TestSessionInput_SuccessAnswersThePickerUnderneath(t *testing.T) {
	m, _ := openSessionPickerFixture(t)
	row := pickerSessions()[4]
	m.overlayStack.Open(newSessionInputDialog(row))
	d := m.overlayStack.Get(sessionInputDialogID).(*sessionInputDialog)
	d.inflight, d.value = true, "http://localhost:9000"

	// Cmd dropped, not drained: the attach batches an event listener
	// into it that blocks on the new agent's channel forever.
	m.applySessionInputSubmit(sessionInputSubmittedMsg{
		gen:    m.sessionGen,
		rowID:  row.ID,
		value:  d.value,
		target: SwitchTarget{Agent: &bareAgent{id: "remote"}},
	})

	if m.overlayStack.HasDialogs() {
		t.Errorf("dialogs still open: %v", overlayIDs(&m.overlayStack))
	}
	if a, ok := m.opts.Agent.(*bareAgent); !ok || a.id != "remote" {
		t.Errorf("agent = %#v, want the endpoint attached", m.opts.Agent)
	}
}

// TestSessionInput_FailureStillEndsThePickersQuestion. A failed attach
// through the action row closes both modals — the error row in the
// transcript is the report, and leaving a modal up would bury it, which
// is the one place this path differs from the picker's own Enter. The
// picker still has to be TOLD, and the answer is a dismissal rather
// than a choice: nobody picked a session.
func TestSessionInput_FailureStillEndsThePickersQuestion(t *testing.T) {
	m, _ := openSessionPickerFixture(t)
	row := pickerSessions()[4]
	m.overlayStack.Open(newSessionInputDialog(row))
	d := m.overlayStack.Get(sessionInputDialogID).(*sessionInputDialog)
	d.inflight, d.value = true, "http://nope"

	m.applySessionInputSubmit(sessionInputSubmittedMsg{
		gen:   m.sessionGen,
		rowID: row.ID,
		value: d.value,
		err:   errors.New("dial tcp: refused"),
	})

	if m.overlayStack.HasDialogs() {
		t.Errorf("dialogs still open: %v", overlayIDs(&m.overlayStack))
	}
	snap := m.history.Snapshot()
	if len(snap) != 1 || !strings.Contains(snap[0].Text, "dial tcp: refused") {
		t.Errorf("history = %v, want the failure reported", snap)
	}
}

// TestSessionInputAnswer pins the mapping itself, since it is the one
// place an outcome is translated into somebody else's answer.
func TestSessionInputAnswer(t *testing.T) {
	if got := sessionInputAnswer("attach", true); got != answer(chosen{ID: "attach"}) {
		t.Errorf("attached = %#v, want chosen{attach}", got)
	}
	if got := sessionInputAnswer("attach", false); got != answer(dismissed{Reason: dismissSuperseded}) {
		t.Errorf("failed = %#v, want dismissed{superseded}", got)
	}
}

// ---------------------------------------------------------------
// The resolver's effects
// ---------------------------------------------------------------

// TestSessionPicker_HostWithNoSessionsSaysSo pins the pre-filter
// behaviour for an empty enumeration, which is now a resolver effect
// rather than something the widget does to a *Model.
func TestSessionPicker_HostWithNoSessionsSaysSo(t *testing.T) {
	m := NewModel(Options{Agent: &switchAgent{id: "cur"}})
	m.viewport.SetWidth(80)
	q := askSessionPicker(&m, true)
	q.applySessions(nil)

	pressPicker(t, &m, "down")

	if m.overlayStack.HasID(sessionPickerDialogID) {
		t.Error("an empty host list left the picker open")
	}
	snap := m.history.Snapshot()
	if len(snap) != 1 || !strings.Contains(snap[0].Text, "no sessions available") {
		t.Errorf("history = %v, want the 'no sessions available' system message", snap)
	}
}

// TestSessionPicker_ResolverTellsTheTwoUnrenderableStatesApart: an
// agent that never claimed SessionSwitcher is not worth a transcript
// row, and a host that claimed it then enumerated nothing is. Both are
// dismissUnrenderable, which is why the resolver closes over its
// question.
func TestSessionPicker_ResolverTellsTheTwoUnrenderableStatesApart(t *testing.T) {
	t.Run("unwired agent says nothing", func(t *testing.T) {
		m := NewModel(Options{Agent: &bareAgent{id: "bare"}})
		m.viewport.SetWidth(80)
		askSessionPicker(&m, false)

		pressPicker(t, &m, "down")

		if m.overlayStack.HasID(sessionPickerDialogID) {
			t.Error("an unwired picker stayed open")
		}
		if n := len(m.history.Snapshot()); n != 0 {
			t.Errorf("an unwired agent wrote %d chat messages, want 0", n)
		}
	})
	t.Run("a loading picker is not unrenderable", func(t *testing.T) {
		m := NewModel(Options{Agent: &switchAgent{id: "cur"}})
		m.viewport.SetWidth(80)
		askSessionPicker(&m, true) // no applySessions: still loading

		pressPicker(t, &m, "down")

		if !m.overlayStack.HasID(sessionPickerDialogID) {
			t.Error("a keystroke before the snapshot landed closed the picker")
		}
		if n := len(m.history.Snapshot()); n != 0 {
			t.Errorf("a loading picker wrote %d chat messages, want 0", n)
		}
	})
}

// TestSessionPicker_FilterMatchingNothingStaysOpen keeps the two empty
// states apart: nothing enumerated by the host closes with a message,
// nothing matched by the filter does not.
func TestSessionPicker_FilterMatchingNothingStaysOpen(t *testing.T) {
	m, _ := openSessionPickerFixture(t)
	typeIntoPicker(&m, "zzz")
	for _, stroke := range []string{"down", "up", "enter"} {
		pressPicker(t, &m, stroke)
		if !m.overlayStack.HasID(sessionPickerDialogID) {
			t.Fatalf("%q on an empty filter result closed the picker", stroke)
		}
	}
	if n := len(m.history.Snapshot()); n != 0 {
		t.Errorf("an empty filter result wrote %d chat messages", n)
	}
	body := ansi.Strip(m.overlayStack.Render(100, &m))
	if !strings.Contains(body, "no sessions match") {
		t.Errorf("empty-result body does not say so:\n%s", body)
	}
	if !strings.Contains(body, "0/5") {
		t.Errorf("empty-result body is missing the 0/5 count:\n%s", body)
	}
}

// TestSessionPicker_CursorSitsInTheFilterRow — same #105 / #123
// requirement as the model picker's, and the same #125 cell-vs-rune
// trap in the CJK case. Through View() because the caret's position is
// only meaningful once the modal has been composed into a frame.
func TestSessionPicker_CursorSitsInTheFilterRow(t *testing.T) {
	for _, typed := range []string{"docs", "セッション"} {
		t.Run(typed, func(t *testing.T) {
			m, _ := openSessionPickerFixture(t)
			typeIntoPicker(&m, typed)
			assertCursorFollows(t, m.View(), filterPromptRail+typed)
		})
	}
}

// ---------------------------------------------------------------
// Two-line cells (issue #163)
// ---------------------------------------------------------------

// manySessions builds n rows big enough to overflow any window the
// tests below set up. Display and ID are deliberately unrelated
// strings, so an assertion that finds one has definitely not found
// the other, and both are fixed-width so no row's tokens are a
// substring of another row's.
func manySessions(n int) []SessionInfo {
	out := make([]SessionInfo, n)
	for i := range out {
		out[i] = SessionInfo{
			ID:      fmt.Sprintf("sess-%03d", i),
			Display: fmt.Sprintf("title-%03d", i),
		}
	}
	return out
}

// TestSessionPicker_RendersTwoLineCells is the shape of the change:
// the title on its own line, the ID and the metadata on the next one,
// and never both on the same line.
func TestSessionPicker_RendersTwoLineCells(t *testing.T) {
	m, _ := openSessionPickerFixture(t)
	lines := sessionPickerLines(&m)

	title := lineWith(lines, "flaky test hunt")
	if title < 0 {
		t.Fatalf("display name is not on screen:\n%s", strings.Join(lines, "\n"))
	}
	if strings.Contains(lines[title], "sess-003") {
		t.Errorf("the ID is still sharing the title's line: %q", lines[title])
	}
	if title+1 >= len(lines) {
		t.Fatalf("title line %d is the last line; the cell has no detail line", title)
	}
	detail := lines[title+1]
	if !strings.Contains(detail, "sess-003") {
		t.Errorf("detail line = %q, want the session ID on it", detail)
	}
	// Description is metadata, so it rides with the ID rather than
	// crowding the title.
	if !strings.Contains(detail, "2 days ago") {
		t.Errorf("detail line = %q, want the Description on it", detail)
	}

	// "(current)" is metadata too, and belongs under its title.
	cur := lineWith(lines, "nightly refactor")
	if cur < 0 {
		t.Fatal("the attached session is not on screen")
	}
	if strings.Contains(lines[cur], "(current)") {
		t.Errorf("(current) is still on the title line: %q", lines[cur])
	}
	if !strings.Contains(lines[cur+1], "(current)") {
		t.Errorf("detail line = %q, want (current) on it", lines[cur+1])
	}

	// Action rows (issue #56) have no identity to put underneath, so
	// their chevron stays on the title line with the label.
	act := lineWith(lines, "+ Attach to endpoint…")
	if act < 0 {
		t.Fatal("the action row is not on screen")
	}
	if !strings.Contains(lines[act], glyphCollapsed) {
		t.Errorf("action row = %q, want the chevron on the title line", lines[act])
	}
}

// TestSessionPicker_EmptyDisplayFallsBackToTheID covers the state
// every host is in until it starts populating Display: the title line
// must never come out blank, and the ID must not then be printed
// twice.
func TestSessionPicker_EmptyDisplayFallsBackToTheID(t *testing.T) {
	sessions := []SessionInfo{
		// Selected (idx 0 on open) and titled, so the bare row below
		// is an UNSELECTED one — its pad line is the one that has to
		// come out genuinely empty. The selected row's pad carries the
		// selection bar and is asserted separately.
		{ID: "sess-head", Display: "a real title"},
		{ID: "sess-bare"},
		{ID: "sess-desc", Description: "yesterday"},
		{ID: "sess-cur", Current: true},
	}
	m, q := openSessionPickerSized(t, 30, sessions)
	lines := sessionPickerLines(&m)

	for _, s := range sessions[1:] {
		at := lineWith(lines, s.ID)
		if at < 0 {
			t.Fatalf("%s is not on screen:\n%s", s.ID, strings.Join(lines, "\n"))
		}
		// The ID is the fallback TITLE, so it is on the first line of
		// the cell and must not be repeated on the second.
		if at+1 < len(lines) && strings.Contains(lines[at+1], s.ID) {
			t.Errorf("%s: ID printed on both lines of the cell (%q / %q)",
				s.ID, lines[at], lines[at+1])
		}
	}
	// The cell stays two lines tall whatever the host supplied: the
	// bare row pads, the others carry their metadata.
	bare := lineWith(lines, "sess-bare")
	if got := strings.TrimSpace(lines[bare+1]); got != "" {
		t.Errorf("bare session's pad line = %q, want it empty", got)
	}
	if got := lines[lineWith(lines, "sess-desc")+1]; !strings.Contains(got, "yesterday") {
		t.Errorf("detail line = %q, want the Description", got)
	}
	if got := lines[lineWith(lines, "sess-cur")+1]; !strings.Contains(got, "(current)") {
		t.Errorf("detail line = %q, want (current)", got)
	}

	// A SELECTED cell with nothing to say still draws its pad line:
	// the selection bar reaches the bottom of the cell, so the cursor
	// marks a CELL and not just its first line.
	q.Key(keyMsgFromStroke("down"))
	lines = sessionPickerLines(&m)
	if got := strings.TrimSpace(lines[lineWith(lines, "sess-bare")+1]); got != glyphSelectBar {
		t.Errorf("selected bare cell's pad line = %q, want just the selection bar", got)
	}
}

// TestSessionPicker_SelectedCellIsNeverHalfWindowed is the trap issue
// #163 names, and the reason the window arithmetic calls listWindow
// twice. listWindow holds ONE line visible; a cell is two. Hand it the
// title and the detail line clips off the bottom while scrolling down;
// hand it the detail line and the title clips off the top while
// scrolling back up. So walk the cursor all the way down and all the
// way back, asserting at every step that BOTH lines of the selected
// cell are on screen and still adjacent.
func TestSessionPicker_SelectedCellIsNeverHalfWindowed(t *testing.T) {
	sessions := manySessions(12)
	m, q := openSessionPickerSized(t, 20, sessions)

	check := func(step string) {
		t.Helper()
		lines := sessionPickerLines(&m)
		want := sessions[q.idx]
		at := lineWith(lines, want.Display)
		if at < 0 {
			t.Fatalf("%s to idx %d: title %q is not in the window:\n%s",
				step, q.idx, want.Display, strings.Join(lines, "\n"))
		}
		if at+1 >= len(lines) || !strings.Contains(lines[at+1], want.ID) {
			t.Fatalf("%s to idx %d: detail line %q is not directly under its title:\n%s",
				step, q.idx, want.ID, strings.Join(lines, "\n"))
		}
	}

	check("open")
	for range len(sessions) - 1 {
		q.Key(keyMsgFromStroke("down"))
		check("down")
	}
	if q.idx != len(sessions)-1 {
		t.Fatalf("cursor is at %d, want the last row %d", q.idx, len(sessions)-1)
	}
	// The window has to have actually engaged, or everything above is
	// vacuously true: at the bottom the first cell is off screen.
	if lines := sessionPickerLines(&m); lineWith(lines, sessions[0].Display) >= 0 {
		t.Fatalf("nothing was windowed away — the fixture is too tall to test the trap:\n%s",
			strings.Join(lines, "\n"))
	}
	for range len(sessions) - 1 {
		q.Key(keyMsgFromStroke("up"))
		check("up")
	}
	if q.idx != 0 {
		t.Fatalf("cursor is at %d, want back at the top", q.idx)
	}
	if lines := sessionPickerLines(&m); lineWith(lines, sessions[len(sessions)-1].Display) >= 0 {
		t.Fatalf("the last cell is still on screen after scrolling back to the top:\n%s",
			strings.Join(lines, "\n"))
	}
}

// TestSessionPicker_ShortTerminal walks the heights where the window
// is smaller than a cell or barely equal to one. Two-line cells halve
// how many sessions fit, so these are the sizes the change makes
// tighter, and the invariant to hold is that the modal still composes
// inside the terminal and the selection is still legible.
func TestSessionPicker_ShortTerminal(t *testing.T) {
	sessions := manySessions(8)
	for _, h := range []int{3, 4, 6, 7, 8, 9, 12, 13, 14, 20} {
		t.Run(fmt.Sprint(h), func(t *testing.T) {
			m, q := openSessionPickerSized(t, 30, sessions)
			m.height = h
			// Drive the cursor to the bottom, where a window shorter
			// than a cell has to choose which half to keep.
			for range len(sessions) - 1 {
				q.Key(keyMsgFromStroke("down"))
				m.overlayStack.Render(100, &m)
			}
			rendered := m.overlayStack.Render(100, &m)
			if got := strings.Count(rendered, "\n") + 1; got > h {
				t.Errorf("picker is %d rows tall in a %d-row terminal:\n%s",
					got, h, ansi.Strip(rendered))
			}
			lines := sessionPickerLines(&m)
			// The filter row is never windowed away — it is what the
			// operator edits to get back. Probed on the prompt rail
			// as well as the placeholder because the footer says
			// "type to filter" too, and matching the phrase alone let
			// the footer stand in for the row at heights that have no
			// body at all (issue #230).
			if h >= modalEdgeRows+3 &&
				!strings.Contains(ansi.Strip(rendered), filterPromptRail+filterPlaceholder) {
				t.Errorf("filter row scrolled out of the body:\n%s", ansi.Strip(rendered))
			}
			// A one-row window can only hold half the cell, and the
			// half worth keeping is the title — that is what the
			// ordering of the two listWindow calls buys.
			//
			// The shortest terminals are the exception, and it
			// predates this change: title + filter row + footer
			// already fills them, so fitModalContent clips the list
			// away entirely and there is no half to keep. The
			// assertion that matters at that size is the height
			// bound above. The exception was three rows until the
			// box edge claimed two more (issue #199) — it is stated
			// against modalEdgeRows so that it tracks the chrome
			// rather than having to be re-guessed.
			if h < 4+modalEdgeRows {
				return
			}
			want := sessions[q.idx]
			if lineWith(lines, want.Display) < 0 {
				t.Errorf("selected title %q is not on screen at height %d:\n%s",
					want.Display, h, strings.Join(lines, "\n"))
			}
		})
	}
}

// TestSessionPicker_NarrowTerminal: the picker floors its width at 30,
// so below that the frame stops shrinking and rows are truncated by
// fitRow instead. Stacking the cell is what makes that survivable —
// truncation now eats the ID rather than the title — and the frame
// must stay rectangular whatever it cuts.
func TestSessionPicker_NarrowTerminal(t *testing.T) {
	sessions := manySessions(8)
	for _, w := range []int{12, 20, 34, 40, 100} {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			m, q := openSessionPickerSized(t, 20, sessions)
			q.Key(keyMsgFromStroke("down"))
			rendered := ansi.Strip(m.overlayStack.Render(w, &m))
			lines := strings.Split(rendered, "\n")
			width := ansi.StringWidth(lines[0])
			for i, ln := range lines {
				if got := ansi.StringWidth(ln); got != width {
					t.Fatalf("line %d is %d cells wide, want %d (frame is ragged):\n%s",
						i, got, width, rendered)
				}
			}
			// The title is the thing being chosen, so it is the thing
			// that has to survive the narrowest frame.
			if !strings.Contains(rendered, "title-001") {
				t.Errorf("selected title was truncated away at width %d:\n%s", w, rendered)
			}
		})
	}
}
