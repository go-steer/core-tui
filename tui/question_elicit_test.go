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

	"github.com/charmbracelet/x/ansi"
)

// A space in a typed field is a space. The stroke normalizes to
// "space", which the form's own switch claimed for the boolean toggle
// and then dropped on the floor for every other field type — five
// runes never pass the single-rune printable guard — so an elicit
// asking for a full name or a commit message could not take one.
//
// The predecessor had the same hole and its test did not see it,
// because the test fed the handler a raw " " that nothing in the key
// path ever produces. This one goes through keyMsgFromStroke, which
// normalizes exactly as Update does.
func TestElicitForm_SpaceTypesIntoATextField(t *testing.T) {
	q := elicitStringForm(t)
	typeIntoForm(t, q, "ada lovelace")
	if got, _ := q.values["who"].(string); got != "ada lovelace" {
		t.Errorf("field holds %q, want %q", got, "ada lovelace")
	}
}

// Space still toggles a boolean, which is the binding it was claimed
// for and the reason the rewrite above is a fallthrough rather than a
// replacement.
func TestElicitForm_SpaceStillTogglesABoolean(t *testing.T) {
	q := newElicitQuestion("srv", ElicitRequest{
		Fields: []ElicitField{{Name: "confirm", Type: ElicitFieldBoolean}},
	})
	q.Key(keyMsgFromStroke("space"))
	if on, _ := q.values["confirm"].(bool); !on {
		t.Fatal("space did not toggle the boolean on")
	}
	q.Key(keyMsgFromStroke("space"))
	if on, _ := q.values["confirm"].(bool); on {
		t.Error("space did not toggle the boolean back off")
	}
}

// Which dismissals re-arm the elicit listener, and which must not.
//
// This is the one thing the migration could get wrong invisibly. An
// answer the operator gave frees the flow, so the next request has to
// be waited for. A dismissal that came from a session switch must NOT
// re-arm, because applySwitchTarget's step 8 already returns a fresh
// listener for the new session — two armed listeners are two
// consumers of one channel, and requests would land in the modal on
// alternate turns with no error anywhere to explain the ones that
// vanished.
func TestElicitResolver_ReArmsOnlyWhenTheFlowIsFree(t *testing.T) {
	cases := []struct {
		name   string
		ans    answer
		reArms bool
	}{
		{"submitted", fields{Values: map[string]any{"user": "ada"}}, true},
		{"declined", declined{}, true},
		{"escaped", dismissed{Reason: dismissEscape}, true},
		{"unrenderable", dismissed{Reason: dismissUnrenderable}, true},
		{"superseded by a session switch", dismissed{Reason: dismissSuperseded}, false},
		{"shutdown", dismissed{Reason: dismissShutdown}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(Options{Elicitor: NewElicitor()})
			cmd := elicitResolver(tc.ans, &m)
			if got := cmd != nil; got != tc.reArms {
				t.Errorf("re-armed = %v, want %v", got, tc.reArms)
			}
		})
	}
}

// URL mode accepts with no values, which is the same ElicitResult the
// host received before the migration. Pinned because the mapping is
// the one place a shape was reused rather than added: "the operator
// said yes" is one answer whether or not there was anything to fill
// in, and a second variant meaning submit would have to be handled by
// every resolver in the package forever.
func TestElicitURLMode_AcceptCarriesNoValues(t *testing.T) {
	q := newElicitQuestion("srv", ElicitRequest{
		Mode: ElicitURLMode, Title: "open", URL: "https://example.com",
	})
	ans, _ := q.Key(keyMsgFromStroke("enter"))
	f, ok := ans.(fields)
	if !ok {
		t.Fatalf("enter answered %T, want fields", ans)
	}
	if f.Values != nil {
		t.Errorf("URL accept carried %v; there are no fields to carry", f.Values)
	}
}

// The footer's scroll hint reports on a measurement Body takes, so it
// is right only because askedQuestion.Render evaluates Body before
// Footer inside one composite literal. Swapping those two lines to
// read more nicely would leave the hint reporting the previous
// frame's geometry — silently correct on every frame but the first
// and the one where the terminal resizes.
//
// Asserted on the composed frame rather than on Footer() directly:
// calling Footer() by hand after Body() would pass whichever order
// Render used.
func TestElicitForm_ScrollHintReachesTheFooter(t *testing.T) {
	m := newModel(Options{Agent: &bareAgent{id: "a"}})
	m.width, m.height = 100, 24
	m.resize()
	fields := make([]ElicitField, 40)
	for i := range fields {
		fields[i] = ElicitField{Name: "field" + strconv.Itoa(i), Type: ElicitFieldString}
	}
	q := newElicitQuestion("srv", ElicitRequest{Title: "big form", Fields: fields})
	m.overlayStack.ask(q, askAgent, nil)

	// First frame, before anything has measured anything.
	got := ansi.Strip(m.overlayStack.render(m.width, &m))
	if !strings.Contains(got, scrollHint(true)) {
		t.Errorf("a 40-field form in 24 rows renders no scroll hint:\n%s", got)
	}

	// And a form that fits does not claim to scroll.
	short := newElicitQuestion("srv", ElicitRequest{
		Title:  "small form",
		Fields: []ElicitField{{Name: "who", Type: ElicitFieldString}},
	})
	var m2 model
	m2.styles, m2.width, m2.height = newStyles(true, Branding{}), 100, 24
	m2.overlayStack.ask(short, askAgent, nil)
	if got := ansi.Strip(m2.overlayStack.render(m2.width, &m2)); strings.Contains(got, scrollHint(true)) {
		t.Errorf("a one-field form claims to scroll:\n%s", got)
	}
}

// The whole point of the stage: the form is a state machine over
// keystrokes plus a renderer, and neither half needs an app model.
// This drives one to a complete answer with no NewModel, no Update
// and no tea.Program — which the predecessor could not have done at
// any price, because its state lived on model and its renderer read
// model.styles.
func TestElicitQuestion_DrivesToAnAnswerWithoutAModel(t *testing.T) {
	q := newElicitQuestion("srv", ElicitRequest{
		Title: "creds",
		Fields: []ElicitField{
			{Name: "user", Type: ElicitFieldString, Required: true},
			{Name: "save", Type: ElicitFieldBoolean},
		},
	})

	// Enter with the required field empty refuses and lands on it.
	if ans, _ := q.Key(keyMsgFromStroke("enter")); ans != nil {
		t.Fatalf("enter submitted an empty required field, answering %T", ans)
	}
	if q.idx != 0 {
		t.Errorf("the refusal left the cursor on field %d, want the missing one (0)", q.idx)
	}

	typeIntoForm(t, q, "ada")
	q.Key(keyMsgFromStroke("tab"))
	q.Key(keyMsgFromStroke("space"))

	// It renders against the zero styleSet, unstyled and unassisted.
	body := q.Body(q.Width(100), 24, styleSet{})
	for _, want := range []string{"user*:", "ada", "save:", "[✓]"} {
		if !strings.Contains(body, want) {
			t.Errorf("the body does not show %q:\n%s", want, body)
		}
	}

	ans, _ := q.Key(keyMsgFromStroke("enter"))
	f, ok := ans.(fields)
	if !ok {
		t.Fatalf("enter answered %T, want fields", ans)
	}
	if got, _ := f.Values["user"].(string); got != "ada" {
		t.Errorf("submitted user = %q, want \"ada\"", got)
	}
	if on, _ := f.Values["save"].(bool); !on {
		t.Error("submitted save = false, want the toggle the operator set")
	}
}
