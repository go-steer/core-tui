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
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// elicitStringForm returns a model parked on a one-field string form,
// ready for handleElicitKey.
func elicitStringForm(t *testing.T) *Model {
	t.Helper()
	m := NewModel(Options{Agent: &bareAgent{id: "a"}})
	m.width, m.height = 100, 24
	m.resize()
	m.pendingElicit = &ElicitRequest{
		Title:  "form",
		Fields: []ElicitField{{Name: "who", Type: ElicitFieldString}},
	}
	m.elicitValues = map[string]any{}
	return &m
}

// Issue #91: the append guard counted bytes, so every printable rune
// outside ASCII (2-4 bytes) was silently dropped.
func TestElicitKey_AppendsNonASCIIRunes(t *testing.T) {
	for _, word := range []string{"abc", "café", "über", "日本語", "😀🙂", "naïve 中"} {
		m := elicitStringForm(t)
		for _, r := range word {
			m.handleElicitKey(string(r))
		}
		got, _ := m.elicitValues["who"].(string)
		if got != word {
			t.Errorf("typed %q, field holds %q", word, got)
		}
	}
}

// Backspace sliced by byte, which cut a multi-byte encoding in half
// and left invalid UTF-8 in the value handed to the host.
func TestElicitKey_BackspaceRemovesWholeRunes(t *testing.T) {
	for _, word := range []string{"abc", "café", "日本語", "😀🙂"} {
		m := elicitStringForm(t)
		for _, r := range word {
			m.handleElicitKey(string(r))
		}
		runes := []rune(word)
		for i := len(runes); i > 0; i-- {
			got, _ := m.elicitValues["who"].(string)
			if want := string(runes[:i]); got != want {
				t.Fatalf("%q: after %d backspaces field = %q, want %q", word, len(runes)-i, got, want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("%q: field holds invalid UTF-8 %q (% x)", word, got, got)
			}
			m.handleElicitKey("backspace")
		}
		if got, _ := m.elicitValues["who"].(string); got != "" {
			t.Errorf("%q: after backspacing every rune, field = %q, want empty", word, got)
		}
		// One more on an empty field is a no-op, not a panic.
		m.handleElicitKey("backspace")
	}
}

// The rune count must still reject the named multi-rune strokes, and
// IsPrint must keep a bare control rune out of the value.
func TestElicitKey_IgnoresNamedAndControlStrokes(t *testing.T) {
	m := elicitStringForm(t)
	m.handleElicitKey("h")
	for _, stroke := range []string{"ctrl+b", "shift+f1", "delete", "\x00", "\x1b"} {
		m.handleElicitKey(stroke)
	}
	if got, _ := m.elicitValues["who"].(string); got != "h" {
		t.Errorf("named / control strokes leaked into the field: %q, want \"h\"", got)
	}
}

// A host-seeded default containing multi-byte runes survives editing:
// the value dispatched back is the same string the operator sees.
func TestElicitKey_SeededDefaultRoundTrips(t *testing.T) {
	m := elicitStringForm(t)
	m.elicitValues["who"] = "Zoë"
	m.handleElicitKey("backspace")
	if got, _ := m.elicitValues["who"].(string); got != "Zo" {
		t.Fatalf("backspace over ë left %q (% x), want \"Zo\"", got, got)
	}
	for _, r := range "ë 😀" {
		m.handleElicitKey(string(r))
	}
	got, _ := m.elicitValues["who"].(string)
	if got != "Zoë 😀" {
		t.Errorf("round-trip left %q, want %q", got, "Zoë 😀")
	}
	if !utf8.ValidString(got) {
		t.Errorf("round-trip produced invalid UTF-8: % x", got)
	}
}

// --- decline (issue #209) -------------------------------------------

// elicitFlowFor opens req on a real elicitor and returns the model
// parked on the modal plus the channel the host's Elicit call answers
// on. The commit keys are still inside modalInputGrace — call
// pastGrace before pressing one, except in the test that is about the
// window itself.
func elicitFlowFor(t *testing.T, req ElicitRequest) (Model, chan ElicitResult) {
	t.Helper()
	e := NewElicitor().(*elicitor)
	results := make(chan ElicitResult, 1)
	go func() {
		r, _ := e.Elicit(context.Background(), "srv", req)
		results <- r
	}()
	if _, ok := e.nextRequest(context.Background()); !ok {
		t.Fatal("setup: nextRequest returned !ok with a pending request")
	}

	m := NewModel(Options{Elicitor: e})
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = out.(Model)
	out, _ = m.Update(elicitRequestMsg{serverName: "srv", req: req})
	m = out.(Model)
	if m.pendingElicit == nil {
		t.Fatal("setup: elicitRequestMsg did not open the modal")
	}
	return m, results
}

// pastGrace backdates the modal's arrival so the keys that commit a
// result are live. modal_grace_test.go owns the window itself.
func pastGrace(m Model) Model {
	m.elicitShownAt = time.Now().Add(-modalInputGrace - time.Millisecond)
	return m
}

// awaitElicit reads the host's answer, or fails.
func awaitElicit(t *testing.T, results chan ElicitResult) ElicitResult {
	t.Helper()
	select {
	case r := <-results:
		return r
	case <-time.After(time.Second):
		t.Fatal("no result reached the host")
		return ElicitResult{}
	}
}

// Issue #209: form mode documented `n` as its decline key and had no
// such case — `n` is printable and types into the focused field, which
// is the only sane thing for it to do. So the form could submit and it
// could cancel, and the one answer it advertised it could not give.
func TestElicitForm_DeclinesOnCtrlD(t *testing.T) {
	m, results := elicitFlowFor(t, ElicitRequest{
		Title:  "creds",
		Fields: []ElicitField{{Name: "user", Type: ElicitFieldString}},
	})
	m = pastGrace(m)
	m = typeWord(m, "ada")

	out, cmd := m.Update(keyPress("ctrl+d"))
	m = out.(Model)
	if m.pendingElicit != nil {
		t.Fatal("ctrl+d left the modal open — the form still has no reachable decline")
	}
	if cmd == nil {
		t.Error("ctrl+d returned no Cmd, so the elicit listener was not re-armed " +
			"and the next request from this server would never arrive")
	}

	r := awaitElicit(t, results)
	if r.Action != ElicitActionDecline {
		t.Errorf("ctrl+d dispatched %v, want Decline", r.Action)
	}
	if len(r.Values) != 0 {
		t.Errorf("the decline carried field values %v — a decline agrees to nothing, "+
			"and half-typed input is not an answer the host may use", r.Values)
	}
}

// Decline and cancel are different answers — "I read this and I am
// saying no" against "I dismissed it without deciding" — and the
// server that asked may act on the difference. Both have to be
// reachable, and they must not collapse into one another.
func TestElicitForm_DeclineAndCancelAreDifferentAnswers(t *testing.T) {
	cases := []struct {
		stroke string
		want   ElicitAction
	}{
		{"ctrl+d", ElicitActionDecline},
		{"esc", ElicitActionCancel},
	}
	for _, tc := range cases {
		t.Run(tc.stroke, func(t *testing.T) {
			m, results := elicitFlowFor(t, ElicitRequest{
				Title:  "creds",
				Fields: []ElicitField{{Name: "user", Type: ElicitFieldString}},
			})
			m = pastGrace(m)

			out, _ := m.Update(keyPress(tc.stroke))
			m = out.(Model)
			if m.pendingElicit != nil {
				t.Fatalf("%s left the modal open", tc.stroke)
			}
			if got := awaitElicit(t, results).Action; got != tc.want {
				t.Errorf("%s dispatched %v, want %v", tc.stroke, got, tc.want)
			}
		})
	}
}

// A decline is a complete answer whatever the fields hold, so it does
// not run Enter's required-field validation. Enter on the same form is
// the control: it refuses and moves the cursor to what is missing.
func TestElicitForm_DeclineSkipsRequiredFieldValidation(t *testing.T) {
	req := ElicitRequest{
		Title: "creds",
		Fields: []ElicitField{
			{Name: "user", Type: ElicitFieldString},
			{Name: "token", Type: ElicitFieldString, Required: true},
		},
	}

	m, results := elicitFlowFor(t, req)
	m = pastGrace(m)
	out, _ := m.Update(keyPress("enter"))
	m = out.(Model)
	if m.pendingElicit == nil {
		t.Fatal("precondition: Enter submitted with the required field empty")
	}
	if m.elicitFieldIdx != 1 {
		t.Fatalf("precondition: Enter left the cursor on field %d, want the missing one (1)",
			m.elicitFieldIdx)
	}

	out, _ = m.Update(keyPress("ctrl+d"))
	m = out.(Model)
	if m.pendingElicit != nil {
		t.Fatal("ctrl+d was refused on a form with an empty required field — " +
			"validation gates submission, not declining")
	}
	if got := awaitElicit(t, results).Action; got != ElicitActionDecline {
		t.Errorf("action = %v, want Decline", got)
	}
}

// The decline commits a result, so it is held for modalInputGrace like
// every other key that answers the host (issue #95). A keystroke
// already in the terminal buffer when the modal appears must not be
// spent saying no on the operator's behalf.
func TestElicitForm_DeclineIsHeldDuringTheGraceWindow(t *testing.T) {
	m, results := elicitFlowFor(t, ElicitRequest{
		Title:  "creds",
		Fields: []ElicitField{{Name: "user", Type: ElicitFieldString}},
	})
	if m.elicitShownAt.IsZero() {
		t.Fatal("setup: elicitRequestMsg did not stamp elicitShownAt")
	}

	out, _ := m.Update(keyPress("ctrl+d"))
	m = out.(Model)
	if m.pendingElicit == nil {
		t.Fatal("ctrl+d declined inside the grace window")
	}
	select {
	case r := <-results:
		t.Fatalf("a result (%v) was dispatched inside the grace window", r.Action)
	default:
	}

	m = pastGrace(m)
	out, _ = m.Update(keyPress("ctrl+d"))
	m = out.(Model)
	if m.pendingElicit != nil {
		t.Fatal("ctrl+d after the grace window did not decline")
	}
	if got := awaitElicit(t, results).Action; got != ElicitActionDecline {
		t.Errorf("action = %v, want Decline", got)
	}
}

// The bug was a promise the code did not keep, so the fix is not
// finished until the keys the modal advertises are the keys that work.
// Both surfaces that name them are checked: the modal's own footer and
// the status footer hint.
func TestElicitModal_AdvertisesTheKeysItHonors(t *testing.T) {
	cases := []struct {
		name   string
		req    ElicitRequest
		stroke string
		key    string
	}{
		{
			name: "form",
			req: ElicitRequest{
				Title:  "creds",
				Fields: []ElicitField{{Name: "user", Type: ElicitFieldString}},
			},
			stroke: "ctrl+d",
			key:    "ctrl+d",
		},
		{
			name:   "url",
			req:    ElicitRequest{Mode: ElicitURLMode, Title: "open", URL: "https://example.com"},
			stroke: "n",
			key:    "n decline",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, results := elicitFlowFor(t, tc.req)
			m = pastGrace(m)

			// tc.key is the phrase as an operator reads it; the
			// U+00A0 keyLegend binds it with is a rendering detail
			// of where the line may break, not part of the promise.
			plain := func(s string) string {
				return unbindLegend(ansi.Strip(s))
			}
			if got := plain(m.renderElicitModal()); !strings.Contains(got, tc.key) {
				t.Errorf("the modal footer does not offer %q:\n%s", tc.key, got)
			}
			if got := plain(m.footerHint()); !strings.Contains(got, tc.key) {
				t.Errorf("the footer hint does not offer %q: %s", tc.key, got)
			}

			out, _ := m.Update(keyPress(tc.stroke))
			m = out.(Model)
			if got := awaitElicit(t, results).Action; got != ElicitActionDecline {
				t.Errorf("the advertised decline key %q dispatched %v, want Decline",
					tc.stroke, got)
			}
		})
	}
}

// URL mode's action row had no key test at all — the mode was only
// ever exercised as a channel-shape fixture. Its three answers are
// pinned here alongside the form's.
func TestElicitURLMode_ActionRow(t *testing.T) {
	cases := []struct {
		stroke string
		want   ElicitAction
	}{
		{"a", ElicitActionSubmit},
		{"enter", ElicitActionSubmit},
		{"n", ElicitActionDecline},
		{"esc", ElicitActionCancel},
	}
	for _, tc := range cases {
		t.Run(tc.stroke, func(t *testing.T) {
			m, results := elicitFlowFor(t,
				ElicitRequest{Mode: ElicitURLMode, Title: "open", URL: "https://example.com"})
			m = pastGrace(m)

			out, _ := m.Update(keyPress(tc.stroke))
			m = out.(Model)
			if m.pendingElicit != nil {
				t.Fatalf("%s left the modal open", tc.stroke)
			}
			if got := awaitElicit(t, results).Action; got != tc.want {
				t.Errorf("%s dispatched %v, want %v", tc.stroke, got, tc.want)
			}
		})
	}
}

// elicitReply is both halves of what Elicit handed back. The
// unsupported path is the only one that sets err, and on that path
// the error IS the answer, so a helper that returned the result alone
// — the way awaitElicit's chan ElicitResult does — would be throwing
// away the thing under test.
type elicitReply struct {
	result ElicitResult
	err    error
}

// unsupportedElicitFlow wires a real elicitor to a real model and
// hands it a request the modal cannot draw, the way an MCP server
// would. It deliberately does NOT use elicitFlowFor, which asserts a
// modal opened: not opening one is the behaviour under test.
func unsupportedElicitFlow(t *testing.T, req ElicitRequest, server string) (Model, chan elicitReply, tea.Cmd) {
	t.Helper()
	e := NewElicitor().(*elicitor)
	replies := make(chan elicitReply, 1)
	go func() {
		r, err := e.Elicit(context.Background(), server, req)
		replies <- elicitReply{result: r, err: err}
	}()
	if _, ok := e.nextRequest(context.Background()); !ok {
		t.Fatal("setup: nextRequest returned !ok with a pending request")
	}
	m := NewModel(Options{Elicitor: e})
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = out.(Model)
	out, cmd := m.Update(elicitRequestMsg{serverName: server, req: req})
	return out.(Model), replies, cmd
}

// awaitElicitReply is awaitElicit for the unsupported path, where the
// error half matters.
func awaitElicitReply(t *testing.T, replies chan elicitReply) elicitReply {
	t.Helper()
	select {
	case r := <-replies:
		return r
	case <-time.After(time.Second):
		t.Fatal("no result reached the host")
		return elicitReply{}
	}
}

// assertRefusedNotDeclined checks the half of issue #209 that is
// about who said what. A request the TUI cannot draw must not reach
// the server as ElicitActionDecline: no operator was consulted, and a
// decline is an operator's word. It arrives as an error instead, and
// the Action beside it is the inert Cancel.
func assertRefusedNotDeclined(t *testing.T, got elicitReply, reason string) {
	t.Helper()
	if got.result.Action == ElicitActionDecline {
		t.Error("the TUI declined on the operator's behalf — nobody was asked")
	}
	if got.result.Action != ElicitActionCancel {
		t.Errorf("action = %v, want Cancel", got.result.Action)
	}
	if !errors.Is(got.err, ErrElicitUnsupported) {
		t.Errorf("err = %v, want it to match ErrElicitUnsupported", got.err)
	}
	if reason != "" && !strings.Contains(fmt.Sprint(got.err), reason) {
		t.Errorf("the error does not say which part could not be drawn: %v", got.err)
	}
}

// R-ELIC-3 has two halves — refuse the undrawable schema, and tell
// the operator it happened — and only the first was built. The
// refusal was issued inside Elicit, on the server's goroutine, where
// there is no transcript to write to, so a request the TUI refused
// left no trace anywhere the operator could see it. It was also sent
// as a decline, which told the server the operator had read the
// request and said no (issue #209).
func TestElicit_UnsupportedSchemaIsRefusedAndRecorded(t *testing.T) {
	cases := []struct {
		name   string
		req    ElicitRequest
		server string
		reason string
	}{
		{
			name:   "form with no fields",
			req:    ElicitRequest{Mode: ElicitFormMode, Title: "creds"},
			server: "github",
			reason: "the form has no fields",
		},
		{
			name:   "url mode with no url",
			req:    ElicitRequest{Mode: ElicitURLMode, Title: "open"},
			server: "browser",
			reason: "the URL is empty",
		},
		{
			name:   "mode this TUI does not render",
			req:    ElicitRequest{Mode: ElicitMode(42)},
			server: "future",
			reason: "the request mode is not one this TUI renders",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, replies, cmd := unsupportedElicitFlow(t, tc.req, tc.server)

			if m.pendingElicit != nil {
				t.Error("a modal opened for a schema the modal cannot draw")
			}
			assertRefusedNotDeclined(t, awaitElicitReply(t, replies), tc.reason)

			last := lastSystemRow(t, m)
			for _, want := range []string{"schema unsupported", tc.reason, tc.server} {
				if !strings.Contains(last, want) {
					t.Errorf("the system row does not mention %q: %s", want, last)
				}
			}

			// Two Cmds have to come back from this branch, and the
			// batch is easy to get half right: the re-armed listener,
			// or a second undrawable request is never drained and the
			// server blocks forever; and the render kick, or the row
			// explaining the refusal waits for the operator's next
			// keystroke to appear (issue #24).
			if cmd == nil {
				t.Fatal("the unsupported branch returned no Cmd")
			}
			// Once. A Cmd is a call, not a value to be re-read: the
			// listener among these blocks until the next request
			// arrives, so a second invocation to format an error
			// message is a hang rather than a failure.
			first := cmd()
			batch, ok := first.(tea.BatchMsg)
			if !ok {
				t.Fatalf("want a batch of listener + render kick, got %T", first)
			}
			if len(batch) != 2 {
				t.Fatalf("batch has %d Cmds, want listener + render kick", len(batch))
			}
			// The listener Cmd blocks until the next request arrives,
			// which is the whole point of it, so the batch cannot be
			// walked synchronously. Run both and take whichever
			// answers; the one that never does is the listener.
			msgs := make(chan tea.Msg, len(batch))
			for _, c := range batch {
				go func(c tea.Cmd) { msgs <- c() }(c)
			}
			var kicked bool
			deadline := time.After(time.Second)
			for !kicked {
				select {
				case got := <-msgs:
					_, kicked = got.(forceRenderMsg)
				case <-deadline:
					t.Fatal("no render kick — the refusal row waits for a keypress")
				}
			}
		})
	}
}

// A host that labels its servers with the empty string gets a notice
// without a dangling "from" and without a double space where the name
// would have been.
func TestElicit_UnsupportedNoticeOmitsAnAbsentServerName(t *testing.T) {
	m, replies, _ := unsupportedElicitFlow(t, ElicitRequest{Mode: ElicitFormMode}, "")
	assertRefusedNotDeclined(t, awaitElicitReply(t, replies), "")
	last := lastSystemRow(t, m)
	if strings.Contains(last, "from") {
		t.Errorf("the notice names a server the host did not name: %s", last)
	}
	if strings.Contains(last, "  ") {
		t.Errorf("the notice has a gap where the server name would be: %q", last)
	}
}

// The refusal is a transcript row, so it has to survive the trip to
// the screen — a Message the renderer drops is the same silence the
// bug was.
func TestElicit_UnsupportedNoticeReachesTheFrame(t *testing.T) {
	m, replies, _ := unsupportedElicitFlow(t,
		ElicitRequest{Mode: ElicitFormMode}, "github")
	awaitElicitReply(t, replies)
	if got := ansi.Strip(m.View().Content); !strings.Contains(got, "schema unsupported") {
		t.Errorf("the refusal is not on screen:\n%s", got)
	}
}

// The operator's row must not say the TUI declined. That word is the
// bug one layer up, restated to the only person who cannot check it:
// an operator reading "declined automatically" has been told a
// decline went out over their name (issue #209).
func TestElicit_UnsupportedNoticeDoesNotClaimTheOperatorDeclined(t *testing.T) {
	m, replies, _ := unsupportedElicitFlow(t,
		ElicitRequest{Mode: ElicitFormMode}, "github")
	awaitElicitReply(t, replies)
	last := lastSystemRow(t, m)
	if strings.Contains(strings.ToLower(last), "declin") {
		t.Errorf("the refusal row credits the operator with a decline: %s", last)
	}
	if !strings.Contains(last, "without asking you") {
		t.Errorf("the refusal row does not say nobody was asked: %s", last)
	}
}

// The operator's account and the server's account of a refusal come
// out of one function, so they cannot drift into describing different
// failures. That property is the whole reason elicitUnsupportedReason
// exists, and it is invisible in either caller on its own.
func TestElicit_TheNoticeAndTheErrorAgreeOnTheReason(t *testing.T) {
	for _, req := range []ElicitRequest{
		{Mode: ElicitFormMode},
		{Mode: ElicitURLMode},
		{Mode: ElicitMode(42)},
	} {
		reason := elicitUnsupportedReason(req)
		if reason == "" {
			t.Fatalf("mode %v has no reason", req.Mode)
		}
		if got := elicitUnsupportedNotice("srv", req); !strings.Contains(got, reason) {
			t.Errorf("the operator's row omits the reason %q: %s", reason, got)
		}
		if got := fmt.Sprint(elicitUnsupportedError(req)); !strings.Contains(got, reason) {
			t.Errorf("the server's error omits the reason %q: %s", reason, got)
		}
	}
}

// A host cannot branch on an error it cannot name. errors.Is against
// the exported sentinel is the whole affordance this change adds, so
// it is asserted against the wrapped value a host actually receives
// rather than against the sentinel itself.
func TestElicit_HostsCanMatchTheUnsupportedSentinel(t *testing.T) {
	err := elicitUnsupportedError(ElicitRequest{Mode: ElicitFormMode})
	if !errors.Is(err, ErrElicitUnsupported) {
		t.Fatalf("errors.Is did not match: %v", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Error("the refusal matches context.Canceled, which is the other error Elicit returns")
	}
}

// lastSystemRow returns the text of the transcript's final RoleSystem
// message.
func lastSystemRow(t *testing.T, m Model) string {
	t.Helper()
	msgs := m.history.Snapshot()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == RoleSystem {
			return msgs[i].Text
		}
	}
	t.Fatalf("no system row in a transcript of %d messages", len(msgs))
	return ""
}
