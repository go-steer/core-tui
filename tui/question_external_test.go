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

// The decoupling criterion for issue #164, as an executable claim.
//
// docs/design-question-dialogs.md §8 states it: a test in
// package tui_test constructs each question with its option list,
// drives it with a sequence of tea.KeyPressMsg values and asserts on
// the returned answer — without calling NewModel, without
// tea.NewProgram, and passing the zero Styles to Body.
//
// That is what this file does, and the discipline is the point rather
// than the coverage. There is no model in it, no theme, no terminal
// and no goroutine; a question that grew a reach back into the app
// would stop compiling here before it stopped working anywhere else.
// Every subsequent question migrated in stages 3-6 belongs in this
// file on the same terms.
//
// The unexported family is reachable through export_test.go, which is
// compiled only under `go test`. See that file for why the bridge is
// aliases rather than wrappers.

package tui_test

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/go-steer/core-tui/tui"
)

// probeThemes is a fixed three-row registry. Deliberately not
// BuiltinThemes(): a question takes its options as an argument now,
// and a test that reached for the real registry would be asserting
// against a list other issues are free to reorder.
func probeThemes() []tui.BuiltinTheme {
	return []tui.BuiltinTheme{
		{Name: "alpha", Description: "the first one"},
		{Name: "beta", Description: "the second one"},
		{Name: "gamma", Description: "the third one"},
	}
}

func key(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code, Text: string(code)})
}

func namedKey(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code})
}

// drive feeds a question a sequence of keystrokes and returns the
// first answer it produced, or nil if it is still asking. Every Cmd
// the question scheduled comes back too, already run — a question's
// Cmd is its own machinery and never touches a model, so running it
// here is safe and is the only way to see the theme preview.
func drive(t *testing.T, q tui.Question, keys ...tea.KeyPressMsg) (tui.Answer, []tea.Msg) {
	t.Helper()
	var msgs []tea.Msg
	for _, k := range keys {
		ans, cmd := q.Key(k)
		if cmd != nil {
			if msg := cmd(); msg != nil {
				msgs = append(msgs, msg)
			}
		}
		if ans != nil {
			return ans, msgs
		}
	}
	return nil, msgs
}

// TestThemeQuestion_EnterAnswersWithTheHighlightedRow is the headline:
// the widget's whole job is now to say which row won, and it says so
// by value.
func TestThemeQuestion_EnterAnswersWithTheHighlightedRow(t *testing.T) {
	q := tui.NewThemePickerQuestion(probeThemes(), "alpha")

	ans, _ := drive(t, q, namedKey(tea.KeyDown), namedKey(tea.KeyEnter))
	got, ok := ans.(tui.Chosen)
	if !ok {
		t.Fatalf("answer is %T, want Chosen", ans)
	}
	if got.ID != "beta" || got.Index != 1 {
		t.Errorf("answer = %+v, want {ID: beta, Index: 1}", got)
	}
}

// TestThemeQuestion_OpensOnTheCurrentRow: the cursor starts on the
// active theme so the operator's first arrow visibly moves off it,
// and the match is case-insensitive because /theme accepts any
// casing.
func TestThemeQuestion_OpensOnTheCurrentRow(t *testing.T) {
	for _, current := range []string{"gamma", "GAMMA"} {
		t.Run(current, func(t *testing.T) {
			q := tui.NewThemePickerQuestion(probeThemes(), current)
			ans, _ := drive(t, q, namedKey(tea.KeyEnter))
			if got := ans.(tui.Chosen); got.ID != "gamma" {
				t.Errorf("enter on open committed %q, want gamma", got.ID)
			}
		})
	}
}

// TestThemeQuestion_EscIsDismissedNotDeclined. The two are different
// answers, and the difference is the reason a sealed set was worth
// having: esc is "no answer", not "the answer is no".
func TestThemeQuestion_EscIsDismissedNotDeclined(t *testing.T) {
	q := tui.NewThemePickerQuestion(probeThemes(), "alpha")
	ans, _ := drive(t, q, namedKey(tea.KeyEsc))
	got, ok := ans.(tui.Dismissed)
	if !ok {
		t.Fatalf("esc answered %T, want Dismissed", ans)
	}
	if got.Reason != tui.DismissEscape {
		t.Errorf("esc reason = %v, want DismissEscape", got.Reason)
	}
}

// TestThemeQuestion_EmptyRegistryIsUnrenderable — nothing can be
// asked, so the question says so rather than pretending the operator
// declined.
func TestThemeQuestion_EmptyRegistryIsUnrenderable(t *testing.T) {
	q := tui.NewThemePickerQuestion(nil, "alpha")
	ans, _ := drive(t, q, namedKey(tea.KeyDown))
	got, ok := ans.(tui.Dismissed)
	if !ok {
		t.Fatalf("answer is %T, want Dismissed", ans)
	}
	if got.Reason != tui.DismissUnrenderable {
		t.Errorf("reason = %v, want DismissUnrenderable", got.Reason)
	}
	if body := q.Body(60, 24, tui.Styles{}); !strings.Contains(body, "no themes registered") {
		t.Errorf("empty-registry body = %q", body)
	}
}

// TestThemeQuestion_CursorMovementSchedulesAPreview. The preview is
// the one effect the widget cannot simply return, so it schedules it
// — and a test can now see exactly what it scheduled, which was not
// observable at all while the widget wrote it into a *model.
func TestThemeQuestion_CursorMovementSchedulesAPreview(t *testing.T) {
	q := tui.NewThemePickerQuestion(probeThemes(), "alpha")

	ans, msgs := drive(t, q, namedKey(tea.KeyDown), namedKey(tea.KeyDown))
	if ans != nil {
		t.Fatalf("two arrows answered %#v; they should only move the cursor", ans)
	}
	want := []string{"beta", "gamma"}
	if len(msgs) != len(want) {
		t.Fatalf("two arrows scheduled %d messages, want %d: %#v", len(msgs), len(want), msgs)
	}
	for i, msg := range msgs {
		preview, ok := msg.(tui.ThemePreviewMsg)
		if !ok {
			t.Fatalf("message %d is %T, want ThemePreviewMsg", i, msg)
		}
		if preview.Name != want[i] {
			t.Errorf("preview %d = %q, want %q", i, preview.Name, want[i])
		}
	}
}

// TestThemeQuestion_FilteringDoesNotPreview: typing narrows the list
// and schedules nothing. Repainting the whole chat in a new palette
// once per letter is a strobe, not a preview.
func TestThemeQuestion_FilteringDoesNotPreview(t *testing.T) {
	q := tui.NewThemePickerQuestion(probeThemes(), "alpha")

	ans, msgs := drive(t, q, key('b'), key('e'))
	if ans != nil {
		t.Fatalf("typing answered %#v", ans)
	}
	for _, msg := range msgs {
		if _, bad := msg.(tui.ThemePreviewMsg); bad {
			t.Errorf("a filter keystroke previewed: %#v", msg)
		}
	}

	// Enter commits the filtered row, and Index is its position in
	// the FILTERED list rather than in the registry.
	ans, _ = drive(t, q, namedKey(tea.KeyEnter))
	got, ok := ans.(tui.Chosen)
	if !ok {
		t.Fatalf("answer is %T, want Chosen", ans)
	}
	if got.ID != "beta" || got.Index != 0 {
		t.Errorf("answer = %+v, want {ID: beta, Index: 0}", got)
	}
}

// TestThemeQuestion_RendersUnstyled is the "no theme" half of the
// criterion. The zero Styles is every field's zero lipgloss.Style, so
// a question that reached for a real palette instead of the one it
// was handed would come back with escape sequences in it.
func TestThemeQuestion_RendersUnstyled(t *testing.T) {
	q := tui.NewThemePickerQuestion(probeThemes(), "beta")

	body := q.Body(q.Width(100), 24, tui.Styles{})
	if body != ansi.Strip(body) {
		t.Errorf("the zero Styles still produced escape sequences:\n%q", body)
	}
	for _, bt := range probeThemes() {
		if !strings.Contains(body, bt.Name) {
			t.Errorf("theme %q missing from the body:\n%s", bt.Name, body)
		}
	}
	if !strings.Contains(body, "(current)") {
		t.Errorf("the active row is not marked:\n%s", body)
	}
	if q.Title() == "" || q.Footer() == "" {
		t.Errorf("chrome strings are empty: title %q footer %q", q.Title(), q.Footer())
	}
	if got := q.Width(20); got < 30 {
		t.Errorf("Width(20) = %d; the modal has a floor", got)
	}
}

// TestThemeQuestion_CaretFollowsTheFilter — the caret belongs to the
// filter row, and the question can say where it is without being
// composited into a frame.
func TestThemeQuestion_CaretFollowsTheFilter(t *testing.T) {
	q := tui.NewThemePickerQuestion(probeThemes(), "alpha")
	// A compile-time claim rather than a runtime assertion: the
	// question is a concrete type here, so a caret that stopped being
	// part of its contract would fail the build.
	var cq tui.CursorQuestion = q

	before := cq.Cursor(72)
	if before == nil {
		t.Fatal("the picker owns the caret even with an empty filter")
	}
	if _, msgs := drive(t, q, key('g'), key('a')); len(msgs) != 0 {
		// Belt and braces: the textinput's own Cmd may be nil, but
		// nothing here should be a preview.
		for _, msg := range msgs {
			if _, bad := msg.(tui.ThemePreviewMsg); bad {
				t.Errorf("typing previewed: %#v", msg)
			}
		}
	}
	after := cq.Cursor(72)
	if after == nil {
		t.Fatal("the caret vanished once something was typed")
	}
	if after.X <= before.X {
		t.Errorf("caret did not advance: %d -> %d", before.X, after.X)
	}
}

// ---- the model picker (stage 3) ----
//
// The theme picker above is the synchronous shape: the keystroke that
// picks a row is the moment the answer exists. This one is the other
// shape, and it is the one the session picker, the permission prompt
// and the elicit form all share — Enter starts a host call and the
// answer arrives from Update several hundred milliseconds later.
//
// That half cannot be tested here, and should not be: what belongs in
// this file is the claim that the widget itself needs no model, which
// for this picker means the list, the filter, the caret, the four
// unrenderable-or-inert states, and exactly what Enter commits to.

// probeModels is a fixed four-row snapshot. Same reasoning as
// probeThemes: a test that reached for a real host's list would be
// asserting against something the host is free to reorder. The rows
// are chosen so display names and IDs disagree, two share a prefix,
// and one has no display name at all.
func probeModels() []tui.ModelInfo {
	return []tui.ModelInfo{
		{ID: "openai/gpt-4o", Display: "GPT-4o", Description: "fast"},
		{ID: "openai/gpt-4.1", Display: "GPT-4.1"},
		{ID: "anthropic/claude-opus-5", Display: "Claude Opus 5", Description: "big"},
		{ID: "meta/llama-4"},
	}
}

// loadedModelPicker is the picker as it exists once the open-time
// AvailableModels() snapshot has landed — the state every test below
// except the loading one starts from.
func loadedModelPicker(current string) *tui.ModelPickerQuestion {
	q := tui.NewModelPickerQuestion(true)
	tui.LoadModels(q, probeModels(), current)
	return q
}

// requestedSwitch pulls the model ID out of whatever Enter scheduled,
// which is the only way the widget can name a model: it holds neither
// the ModelSwapper nor the session generation, so it asks the Update
// loop to make the call.
func requestedSwitch(t *testing.T, msgs []tea.Msg) string {
	t.Helper()
	if len(msgs) != 1 {
		t.Fatalf("scheduled %d messages, want exactly the switch request: %#v", len(msgs), msgs)
	}
	req, ok := msgs[0].(tui.ModelSwitchRequestedMsg)
	if !ok {
		t.Fatalf("scheduled a %T, want ModelSwitchRequestedMsg", msgs[0])
	}
	return req.ID
}

// TestModelQuestion_EnterCommitsWithoutAnswering is the headline
// difference from the theme picker. Enter produces no answer at all —
// the question is committed, not finished — because a list that
// froze for as long as the host takes to swap a model would read as a
// hang.
func TestModelQuestion_EnterCommitsWithoutAnswering(t *testing.T) {
	q := loadedModelPicker("openai/gpt-4o")

	ans, msgs := drive(t, q, namedKey(tea.KeyDown), namedKey(tea.KeyEnter))
	if ans != nil {
		t.Fatalf("enter answered %#v; the host's reply is the answer", ans)
	}
	if got := requestedSwitch(t, msgs); got != "openai/gpt-4.1" {
		t.Errorf("requested %q, want the highlighted row openai/gpt-4.1", got)
	}
}

// TestModelQuestion_OpensOnTheCurrentRow (#110): the cursor starts on
// the active model rather than on row 0, matched on the ID or, for a
// host that advertises a display name instead, on that.
func TestModelQuestion_OpensOnTheCurrentRow(t *testing.T) {
	for _, current := range []string{"anthropic/claude-opus-5", "Claude Opus 5"} {
		t.Run(current, func(t *testing.T) {
			q := loadedModelPicker(current)
			_, msgs := drive(t, q, namedKey(tea.KeyEnter))
			if got := requestedSwitch(t, msgs); got != "anthropic/claude-opus-5" {
				t.Errorf("enter on open committed %q, want the current row", got)
			}
		})
	}
}

// TestModelQuestion_EnterCommitsTheFilteredRow: the cursor indexes the
// FILTERED list. If Enter ever read the snapshot at that index
// instead, narrowing to one row and pressing Enter would switch to
// some other model — a silent and destructive version of the bug.
func TestModelQuestion_EnterCommitsTheFilteredRow(t *testing.T) {
	q := loadedModelPicker("openai/gpt-4o")

	ans, _ := drive(t, q, key('l'), key('l'), key('a'))
	if ans != nil {
		t.Fatalf("typing answered %#v", ans)
	}
	body := ansi.Strip(q.Body(q.Width(100), 24, tui.Styles{}))
	if !strings.Contains(body, "meta/llama-4") {
		t.Fatalf("the filter did not keep the matching row:\n%s", body)
	}
	if strings.Contains(body, "GPT-4o") {
		t.Fatalf("the filter kept a row that does not match:\n%s", body)
	}

	_, msgs := drive(t, q, namedKey(tea.KeyEnter))
	if got := requestedSwitch(t, msgs); got != "meta/llama-4" {
		t.Errorf("enter committed %q, want the only filtered row", got)
	}
}

// TestModelQuestion_FilterMatchesTheIDToo. The row prints both the
// display name and the ID, so both have to be searchable — filtering
// on the label alone would make an ID visible on screen unfindable.
func TestModelQuestion_FilterMatchesTheIDToo(t *testing.T) {
	q := loadedModelPicker("openai/gpt-4o")

	drive(t, q, key('a'), key('n'), key('t'), key('h'))
	body := ansi.Strip(q.Body(q.Width(100), 24, tui.Styles{}))
	if !strings.Contains(body, "Claude Opus 5") {
		t.Errorf("filtering on an ID substring dropped its row:\n%s", body)
	}
	if strings.Contains(body, "llama") {
		t.Errorf("the filter kept a non-matching row:\n%s", body)
	}
}

// TestModelQuestion_FilterMatchingNothingStaysOpen separates the two
// empty lists that used to be one. An empty result WITH a filter means
// the operator has typed past every match, and answering — closing the
// picker out from under them — would be absurd. The empty result with
// no filter is the host's, below.
func TestModelQuestion_FilterMatchingNothingStaysOpen(t *testing.T) {
	q := loadedModelPicker("openai/gpt-4o")

	drive(t, q, key('z'), key('z'), key('z'))
	for _, k := range []tea.KeyPressMsg{
		namedKey(tea.KeyDown), namedKey(tea.KeyUp), namedKey(tea.KeyEnter),
	} {
		ans, msgs := drive(t, q, k)
		if ans != nil {
			t.Errorf("%v on an empty filter result answered %#v", k, ans)
		}
		if len(msgs) != 0 {
			t.Errorf("%v on an empty filter result scheduled %#v", k, msgs)
		}
	}
	body := ansi.Strip(q.Body(q.Width(100), 24, tui.Styles{}))
	if !strings.Contains(body, "no models match") {
		t.Errorf("empty-result body does not say so:\n%s", body)
	}
	if !strings.Contains(body, "zzz") {
		t.Errorf("empty-result body does not echo the filter:\n%s", body)
	}
	if !strings.Contains(body, "0/4") {
		t.Errorf("empty-result body is missing the 0/4 count:\n%s", body)
	}
}

// TestModelQuestion_ShrinkingListNeverPanics walks the cursor while
// the list changes size under it. The pre-filter cursor arithmetic was
// (idx ± 1 + len) % len, which divides by zero the moment len hits
// zero, and was only safe because the list could not shrink.
func TestModelQuestion_ShrinkingListNeverPanics(t *testing.T) {
	q := loadedModelPicker("openai/gpt-4o")
	script := "gptzzz"
	for _, r := range script {
		drive(t, q, key(r), namedKey(tea.KeyDown), namedKey(tea.KeyDown), namedKey(tea.KeyUp))
		q.Body(q.Width(100), 24, tui.Styles{})
	}
	for range script {
		drive(t, q, namedKey(tea.KeyBackspace), namedKey(tea.KeyUp), namedKey(tea.KeyDown))
		q.Body(q.Width(100), 24, tui.Styles{})
	}
	// Backspacing the filter away restores the full list, in the
	// host's own order, and Enter finds a row again.
	body := ansi.Strip(q.Body(q.Width(100), 24, tui.Styles{}))
	for _, mi := range probeModels() {
		if !strings.Contains(body, mi.ID) {
			t.Errorf("model %q missing after the filter was deleted:\n%s", mi.ID, body)
		}
	}
	if _, msgs := drive(t, q, namedKey(tea.KeyEnter)); len(msgs) != 1 {
		t.Errorf("enter on the restored list scheduled %#v", msgs)
	}
}

// TestModelQuestion_TheFourInertStates. Two of them answer — there is
// nothing to ask — and two swallow the keystroke, because an open
// modal is exclusive and a stroke that leaked to the textarea behind
// it would be worse than one that did nothing.
func TestModelQuestion_TheFourInertStates(t *testing.T) {
	cases := []struct {
		name string
		q    func() *tui.ModelPickerQuestion
		// wantAnswer is true when a keystroke ends the question.
		wantAnswer bool
		wantBody   string
	}{
		{
			name:       "the agent is not a ModelSwapper",
			q:          func() *tui.ModelPickerQuestion { return tui.NewModelPickerQuestion(false) },
			wantAnswer: true,
			wantBody:   "does not implement ModelSwapper",
		},
		{
			name: "the host advertised an empty list",
			q: func() *tui.ModelPickerQuestion {
				q := tui.NewModelPickerQuestion(true)
				tui.LoadModels(q, nil, "")
				return q
			},
			wantAnswer: true,
			wantBody:   "no models advertised",
		},
		{
			name:     "the snapshot has not landed yet",
			q:        func() *tui.ModelPickerQuestion { return tui.NewModelPickerQuestion(true) },
			wantBody: "loading models",
		},
		{
			name: "a switch is in flight",
			q: func() *tui.ModelPickerQuestion {
				q := loadedModelPicker("openai/gpt-4o")
				drive(t, q, namedKey(tea.KeyEnter))
				return q
			},
			wantBody: "switching to openai/gpt-4o",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := tc.q()
			if body := q.Body(q.Width(100), 24, tui.Styles{}); !strings.Contains(body, tc.wantBody) {
				t.Errorf("body = %q, want it to mention %q", body, tc.wantBody)
			}
			// None of the four has a filter row on screen, so none of
			// them may claim the terminal caret (#105 / #123).
			if c := q.Cursor(q.Width(100)); c != nil {
				t.Errorf("claimed the caret at (%d,%d) with no filter row on screen", c.X, c.Y)
			}

			ans, _ := drive(t, q, namedKey(tea.KeyDown), key('g'), namedKey(tea.KeyEnter))
			if tc.wantAnswer {
				got, ok := ans.(tui.Dismissed)
				if !ok {
					t.Fatalf("answer is %T, want Dismissed", ans)
				}
				if got.Reason != tui.DismissUnrenderable {
					t.Errorf("reason = %v, want DismissUnrenderable", got.Reason)
				}
				return
			}
			if ans != nil {
				t.Errorf("an inert state answered %#v; it should swallow", ans)
			}
			// Esc always gets out, including mid-switch: the host call
			// is already committed and the operator is declining to
			// watch it, not cancelling it.
			ans, _ = drive(t, q, namedKey(tea.KeyEsc))
			if got, ok := ans.(tui.Dismissed); !ok || got.Reason != tui.DismissEscape {
				t.Errorf("esc answered %#v, want Dismissed{DismissEscape}", ans)
			}
		})
	}
}

// TestModelQuestion_EscIsDismissedNotDeclined — same distinction as
// the theme picker's, restated because this picker also has a
// committed-but-unanswered state esc can land in.
func TestModelQuestion_EscIsDismissedNotDeclined(t *testing.T) {
	q := loadedModelPicker("openai/gpt-4o")
	ans, _ := drive(t, q, namedKey(tea.KeyEsc))
	got, ok := ans.(tui.Dismissed)
	if !ok {
		t.Fatalf("esc answered %T, want Dismissed", ans)
	}
	if got.Reason != tui.DismissEscape {
		t.Errorf("esc reason = %v, want DismissEscape", got.Reason)
	}
}

// TestModelQuestion_RendersUnstyled is the "no theme" half of the
// criterion, and for this picker it is also the "no host" half: Body
// used to call m.displayModelName() to decide which row was current,
// which is how a host call ended up inside View().
func TestModelQuestion_RendersUnstyled(t *testing.T) {
	q := loadedModelPicker("anthropic/claude-opus-5")

	body := q.Body(q.Width(100), 24, tui.Styles{})
	if body != ansi.Strip(body) {
		t.Errorf("the zero Styles still produced escape sequences:\n%q", body)
	}
	for _, mi := range probeModels() {
		if !strings.Contains(body, mi.ID) {
			t.Errorf("model %q missing from the body:\n%s", mi.ID, body)
		}
	}
	if !strings.Contains(body, "(current)") {
		t.Errorf("the active row is not marked:\n%s", body)
	}
	if q.Title() == "" || q.Footer() == "" {
		t.Errorf("chrome strings are empty: title %q footer %q", q.Title(), q.Footer())
	}
	if got := q.Width(20); got < 30 {
		t.Errorf("Width(20) = %d; the modal has a floor", got)
	}
}

// TestModelQuestion_CaretFollowsTheFilter — the caret belongs to the
// filter row, and the question can say where it is with no frame
// around it.
func TestModelQuestion_CaretFollowsTheFilter(t *testing.T) {
	q := loadedModelPicker("openai/gpt-4o")
	// A compile-time claim rather than a runtime assertion: a caret
	// that stopped being part of the contract would fail the build.
	var cq tui.CursorQuestion = q

	before := cq.Cursor(72)
	if before == nil {
		t.Fatal("the picker owns the caret even with an empty filter")
	}
	drive(t, q, key('g'), key('p'))
	after := cq.Cursor(72)
	if after == nil {
		t.Fatal("the caret vanished once something was typed")
	}
	if after.X <= before.X {
		t.Errorf("caret did not advance: %d -> %d", before.X, after.X)
	}
}

// ---- the session picker (stage 3) ----
//
// The same asynchronous shape as the model picker above, plus the two
// things that make it the harder of the pair: a row whose Enter starts
// somebody ELSE's question, and a row whose Enter answers in the same
// keystroke. Three outcomes from one key, and which one you get is
// decided entirely from the row — no model, which is the claim.

// probeSessions is a fixed five-row snapshot covering every row kind
// the picker has to tell apart: the attached one, plain ones whose
// display names and IDs disagree, one with no display name at all, and
// an action row (issue #56).
func probeSessions() []tui.SessionInfo {
	return []tui.SessionInfo{
		{ID: "sess-001", Display: "nightly refactor", Current: true},
		{ID: "sess-002", Display: "docs sweep"},
		{ID: "sess-003", Display: "flaky test hunt", Description: "2 days ago"},
		{ID: "prod-042"},
		{ID: "+attach", Display: "+ Attach to endpoint…", Input: &tui.SessionInput{
			Prompt: "Daemon URL:",
		}},
	}
}

// loadedSessionPicker is the picker once the open-time Sessions()
// snapshot has landed.
func loadedSessionPicker() *tui.SessionPickerQuestion {
	q := tui.NewSessionPickerQuestion(true)
	tui.LoadSessions(q, probeSessions())
	return q
}

// requestedAttach pulls the session ID out of whatever Enter scheduled.
// Same reason as requestedSwitch: the widget holds neither the
// SessionSwitcher nor the generation, so naming the row is all it can
// do.
func requestedAttach(t *testing.T, msgs []tea.Msg) string {
	t.Helper()
	if len(msgs) != 1 {
		t.Fatalf("scheduled %d messages, want exactly the attach request: %#v", len(msgs), msgs)
	}
	req, ok := msgs[0].(tui.SessionSwitchRequestedMsg)
	if !ok {
		t.Fatalf("scheduled a %T, want SessionSwitchRequestedMsg", msgs[0])
	}
	return req.ID
}

// TestSessionQuestion_EnterOnAPlainRowCommitsWithoutAnswering — the
// model picker's headline, restated because attaching is the slower
// call of the two and the one most likely to look like a hang.
func TestSessionQuestion_EnterOnAPlainRowCommitsWithoutAnswering(t *testing.T) {
	q := loadedSessionPicker()

	ans, msgs := drive(t, q, namedKey(tea.KeyDown), namedKey(tea.KeyEnter))
	if ans != nil {
		t.Fatalf("enter answered %#v; the host's reply is the answer", ans)
	}
	if got := requestedAttach(t, msgs); got != "sess-002" {
		t.Errorf("requested %q, want the highlighted row sess-002", got)
	}
	// And the list says what it is waiting for rather than freezing.
	if body := q.Body(q.Width(100), 24, tui.Styles{}); !strings.Contains(body, "attaching to sess-002") {
		t.Errorf("body = %q, want the progress line", body)
	}
}

// TestSessionQuestion_OpensOnRowZero is the deliberate difference from
// the model picker, which #110 made open on the current row. A session
// list is a history and the attached session is the one row the
// operator is least likely to want, so seeding the cursor onto it would
// put it on the least useful row every time.
func TestSessionQuestion_OpensOnRowZero(t *testing.T) {
	q := tui.NewSessionPickerQuestion(true)
	// The attached session deliberately NOT first, so landing on it
	// could only be a seed.
	tui.LoadSessions(q, []tui.SessionInfo{
		{ID: "sess-002", Display: "docs sweep"},
		{ID: "sess-001", Display: "nightly refactor", Current: true},
	})
	if got := requestedAttach(t, mustSchedule(t, q, namedKey(tea.KeyEnter))); got != "sess-002" {
		t.Errorf("enter on open committed %q, want row 0", got)
	}
}

// mustSchedule drives one key and insists the question stayed open.
func mustSchedule(t *testing.T, q tui.Question, k tea.KeyPressMsg) []tea.Msg {
	t.Helper()
	ans, msgs := drive(t, q, k)
	if ans != nil {
		t.Fatalf("%v answered %#v, want the question still open", k, ans)
	}
	return msgs
}

// TestSessionQuestion_EnterOnTheAttachedRowAnswers is the third
// outcome, and the one that cannot be deferred to a host reply: there
// is no session to attach to and no call to make, so the answer exists
// the moment the key lands. Getting this wrong is destructive rather
// than merely wrong — the attach path wipes the transcript.
func TestSessionQuestion_EnterOnTheAttachedRowAnswers(t *testing.T) {
	q := loadedSessionPicker()

	ans, msgs := drive(t, q, namedKey(tea.KeyEnter))
	got, ok := ans.(tui.Chosen)
	if !ok {
		t.Fatalf("answer is %T, want Chosen", ans)
	}
	if got.ID != "sess-001" || got.Index != 0 {
		t.Errorf("answer = %+v, want the attached row sess-001@0", got)
	}
	if len(msgs) != 0 {
		t.Errorf("the attached row scheduled %#v; there is nothing to call", msgs)
	}
}

// TestSessionQuestion_EnterOnAnActionRowStartsAnotherQuestion — the
// second outcome. The row needs an address typed, which this question
// cannot ask for, so it neither answers nor dismisses: it names the row
// and leaves the Open to Update, because overlay pops the front dialog
// after Key returns and a question that pushed one would watch it be
// popped again.
func TestSessionQuestion_EnterOnAnActionRowStartsAnotherQuestion(t *testing.T) {
	q := loadedSessionPicker()

	// Filter down to it, so the row Enter reads is the FILTERED one.
	drive(t, q, key('e'), key('n'), key('d'))
	ans, msgs := drive(t, q, namedKey(tea.KeyEnter))
	if ans != nil {
		t.Fatalf("the action row answered %#v; it starts a question, it does not end one", ans)
	}
	if len(msgs) != 1 {
		t.Fatalf("scheduled %#v, want exactly the input request", msgs)
	}
	req, ok := msgs[0].(tui.SessionInputRequestedMsg)
	if !ok {
		t.Fatalf("scheduled a %T, want SessionInputRequestedMsg", msgs[0])
	}
	// The whole row, not just its ID: the title, prompt, placeholder and
	// validator the input needs all live on SessionInput.
	if req.Row.ID != "+attach" || req.Row.Input == nil || req.Row.Input.Prompt != "Daemon URL:" {
		t.Errorf("request carried %+v, want the action row whole", req.Row)
	}
}

// TestSessionQuestion_FilterMatchesTheIDToo — a session whose host
// gave it no display name is findable only by its ID, and it is on
// screen, so it has to be searchable.
func TestSessionQuestion_FilterMatchesTheIDToo(t *testing.T) {
	q := loadedSessionPicker()

	drive(t, q, key('p'), key('r'), key('o'), key('d'))
	if got := requestedAttach(t, mustSchedule(t, q, namedKey(tea.KeyEnter))); got != "prod-042" {
		t.Errorf("enter committed %q, want the ID-only row prod-042", got)
	}
}

// TestSessionQuestion_FilterMatchingNothingStaysOpen keeps the two
// empty states apart. This one is the operator's own doing and the next
// keystroke is a backspace; the host enumerating nothing is inert and
// answers, below.
func TestSessionQuestion_FilterMatchingNothingStaysOpen(t *testing.T) {
	q := loadedSessionPicker()

	drive(t, q, key('z'), key('z'), key('z'))
	for _, k := range []tea.KeyPressMsg{
		namedKey(tea.KeyDown), namedKey(tea.KeyUp), namedKey(tea.KeyEnter),
	} {
		ans, msgs := drive(t, q, k)
		if ans != nil {
			t.Errorf("%v on an empty filter result answered %#v", k, ans)
		}
		if len(msgs) != 0 {
			t.Errorf("%v on an empty filter result scheduled %#v", k, msgs)
		}
	}
	body := ansi.Strip(q.Body(q.Width(100), 24, tui.Styles{}))
	if !strings.Contains(body, "no sessions match") {
		t.Errorf("empty-result body does not say so:\n%s", body)
	}
	if !strings.Contains(body, "zzz") {
		t.Errorf("empty-result body does not echo the filter:\n%s", body)
	}
	if !strings.Contains(body, "0/5") {
		t.Errorf("empty-result body is missing the 0/5 count:\n%s", body)
	}
}

// TestSessionQuestion_CursorWrapsAndSurvivesAShrinkingList. The
// wrapping is the pre-filter behaviour, preserved; surviving the shrink
// is what the filter added, since the old (idx ± 1 + len) % len divides
// by zero the moment the list empties.
func TestSessionQuestion_CursorWrapsAndSurvivesAShrinkingList(t *testing.T) {
	q := loadedSessionPicker()

	// Down past the end wraps to the top, up from the top wraps to the
	// end — asserted through what Enter then commits to, since the
	// cursor index is not the widget's contract, the row it lands on is.
	drive(t, q, namedKey(tea.KeyUp))
	if _, msgs := drive(t, q, namedKey(tea.KeyEnter)); len(msgs) != 1 {
		t.Fatalf("up from row 0 scheduled %#v, want the action row's input request", msgs)
	}

	q = loadedSessionPicker()
	for range len(probeSessions()) + 1 {
		drive(t, q, namedKey(tea.KeyDown))
	}
	if got := requestedAttach(t, mustSchedule(t, q, namedKey(tea.KeyEnter))); got != "sess-002" {
		t.Errorf("a full lap plus one committed %q, want row 1", got)
	}

	q = loadedSessionPicker()
	script := "sesszzz"
	for _, r := range script {
		drive(t, q, key(r), namedKey(tea.KeyDown), namedKey(tea.KeyDown), namedKey(tea.KeyUp))
		q.Body(q.Width(100), 24, tui.Styles{})
	}
	for range script {
		drive(t, q, namedKey(tea.KeyBackspace), namedKey(tea.KeyUp), namedKey(tea.KeyDown))
		q.Body(q.Width(100), 24, tui.Styles{})
	}
	body := ansi.Strip(q.Body(q.Width(100), 24, tui.Styles{}))
	for _, want := range sessionLabels() {
		if !strings.Contains(body, want) {
			t.Errorf("session %q missing after the filter was deleted:\n%s", want, body)
		}
	}
}

// sessionLabels is what each probe row actually PRINTS. Not simply the
// IDs: an action row's ID is a magic token the picker deliberately
// keeps off screen, and a row with no display name falls back to its
// ID as the title.
func sessionLabels() []string {
	rows := probeSessions()
	out := make([]string, len(rows))
	for i, s := range rows {
		out[i] = s.Display
		if out[i] == "" {
			out[i] = s.ID
		}
	}
	return out
}

// TestSessionQuestion_TheFourInertStates — the model picker's four,
// with the wording this picker owns.
func TestSessionQuestion_TheFourInertStates(t *testing.T) {
	cases := []struct {
		name string
		q    func() *tui.SessionPickerQuestion
		// wantAnswer is true when a keystroke ends the question.
		wantAnswer bool
		wantBody   string
	}{
		{
			name:       "the agent is not a SessionSwitcher",
			q:          func() *tui.SessionPickerQuestion { return tui.NewSessionPickerQuestion(false) },
			wantAnswer: true,
			wantBody:   "does not implement SessionSwitcher",
		},
		{
			name: "the host enumerated nothing",
			q: func() *tui.SessionPickerQuestion {
				q := tui.NewSessionPickerQuestion(true)
				tui.LoadSessions(q, nil)
				return q
			},
			wantAnswer: true,
			wantBody:   "no sessions advertised",
		},
		{
			name:     "the snapshot has not landed yet",
			q:        func() *tui.SessionPickerQuestion { return tui.NewSessionPickerQuestion(true) },
			wantBody: "loading sessions",
		},
		{
			name: "an attach is in flight",
			q: func() *tui.SessionPickerQuestion {
				q := loadedSessionPicker()
				drive(t, q, namedKey(tea.KeyDown), namedKey(tea.KeyEnter))
				return q
			},
			wantBody: "attaching to sess-002",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := tc.q()
			if body := q.Body(q.Width(100), 24, tui.Styles{}); !strings.Contains(body, tc.wantBody) {
				t.Errorf("body = %q, want it to mention %q", body, tc.wantBody)
			}
			if c := q.Cursor(q.Width(100)); c != nil {
				t.Errorf("claimed the caret at (%d,%d) with no filter row on screen", c.X, c.Y)
			}

			ans, _ := drive(t, q, namedKey(tea.KeyDown), key('g'), namedKey(tea.KeyEnter))
			if tc.wantAnswer {
				got, ok := ans.(tui.Dismissed)
				if !ok {
					t.Fatalf("answer is %T, want Dismissed", ans)
				}
				if got.Reason != tui.DismissUnrenderable {
					t.Errorf("reason = %v, want DismissUnrenderable", got.Reason)
				}
				return
			}
			if ans != nil {
				t.Errorf("an inert state answered %#v; it should swallow", ans)
			}
			// Esc gets out mid-attach too. The call is committed and its
			// reply still applies the target — the operator is declining
			// to watch it, not cancelling it.
			ans, _ = drive(t, q, namedKey(tea.KeyEsc))
			if got, ok := ans.(tui.Dismissed); !ok || got.Reason != tui.DismissEscape {
				t.Errorf("esc answered %#v, want Dismissed{DismissEscape}", ans)
			}
		})
	}
}

// TestSessionQuestion_EscIsDismissedNotDeclined — nobody declined
// anything by escaping a list; §1.4's hole was exactly this conflation.
func TestSessionQuestion_EscIsDismissedNotDeclined(t *testing.T) {
	q := loadedSessionPicker()
	ans, _ := drive(t, q, namedKey(tea.KeyEsc))
	got, ok := ans.(tui.Dismissed)
	if !ok {
		t.Fatalf("esc answered %T, want Dismissed", ans)
	}
	if got.Reason != tui.DismissEscape {
		t.Errorf("esc reason = %v, want DismissEscape", got.Reason)
	}
}

// TestSessionQuestion_RendersUnstyled is the "no theme, no host" half.
// Body used to call m.opts.Agent.(SessionSwitcher).Sessions() to get
// the list it painted, which is one of the three documented ways a host
// call ended up inside View().
func TestSessionQuestion_RendersUnstyled(t *testing.T) {
	q := loadedSessionPicker()

	body := q.Body(q.Width(100), 24, tui.Styles{})
	if body != ansi.Strip(body) {
		t.Errorf("the zero Styles still produced escape sequences:\n%q", body)
	}
	for _, want := range sessionLabels() {
		if !strings.Contains(body, want) {
			t.Errorf("session %q missing from the body:\n%s", want, body)
		}
	}
	if !strings.Contains(body, "(current)") {
		t.Errorf("the attached row is not marked:\n%s", body)
	}
	if q.Title() == "" || q.Footer() == "" {
		t.Errorf("chrome strings are empty: title %q footer %q", q.Title(), q.Footer())
	}
	if got := q.Width(20); got < 30 {
		t.Errorf("Width(20) = %d; the modal has a floor", got)
	}
}

// TestSessionQuestion_CaretFollowsTheFilter — same contract as the
// model picker's, and the same compile-time claim.
func TestSessionQuestion_CaretFollowsTheFilter(t *testing.T) {
	q := loadedSessionPicker()
	var cq tui.CursorQuestion = q

	before := cq.Cursor(72)
	if before == nil {
		t.Fatal("the picker owns the caret even with an empty filter")
	}
	drive(t, q, key('d'), key('o'))
	after := cq.Cursor(72)
	if after == nil {
		t.Fatal("the caret vanished once something was typed")
	}
	if after.X <= before.X {
		t.Errorf("caret did not advance: %d -> %d", before.X, after.X)
	}
}

// TestAnswer_TheSetIsSealedAndTotal walks every variant through one
// switch. It is not coverage for its own sake: gochecksumtype fails
// this switch if a variant is added and not named here, so this is
// where the cost of an eighth shape gets paid first and visibly.
func TestAnswer_TheSetIsSealedAndTotal(t *testing.T) {
	cases := []struct {
		name string
		ans  tui.Answer
		want string
	}{
		{"dismissed", tui.Dismissed{Reason: tui.DismissSuperseded}, "dismissed:1"},
		{"declined", tui.Declined{}, "declined"},
		{"chosen", tui.Chosen{ID: "beta", Index: 1}, "chosen:beta@1"},
		{"selected", tui.Selected{IDs: []string{"a", "b"}, Indexes: []int{0, 2}}, "selected:a,b@0,2"},
		{"text", tui.Text{Value: "hello"}, "text:hello"},
		{"fields", tui.Fields{Values: map[string]any{"n": int64(3)}}, "fields:n=3"},
		{"decision", tui.Decision{Value: tui.DecisionAllowOnce}, "decision:1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := describeAnswer(tc.ans); got != tc.want {
				t.Errorf("describeAnswer = %q, want %q", got, tc.want)
			}
		})
	}
}

// describeAnswer reads each variant's payload back out, which is the
// part that matters: an alias whose field was renamed or retyped
// fails to compile here.
func describeAnswer(a tui.Answer) string {
	switch a := a.(type) {
	case tui.Dismissed:
		return "dismissed:" + strconv.Itoa(int(a.Reason))
	case tui.Declined:
		return "declined"
	case tui.Chosen:
		return "chosen:" + a.ID + "@" + strconv.Itoa(a.Index)
	case tui.Selected:
		idx := make([]string, len(a.Indexes))
		for i, n := range a.Indexes {
			idx[i] = strconv.Itoa(n)
		}
		return "selected:" + strings.Join(a.IDs, ",") + "@" + strings.Join(idx, ",")
	case tui.Text:
		return "text:" + a.Value
	case tui.Fields:
		parts := make([]string, 0, len(a.Values))
		for k, v := range a.Values {
			parts = append(parts, k+"="+strconv.FormatInt(v.(int64), 10))
		}
		return "fields:" + strings.Join(parts, ",")
	case tui.Decision:
		return "decision:" + strconv.Itoa(int(a.Value))
	}
	return "unreachable"
}
