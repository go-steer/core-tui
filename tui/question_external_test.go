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
// than the coverage. There is no Model in it, no theme, no terminal
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
// Cmd is its own machinery and never touches a Model, so running it
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
// observable at all while the widget wrote it into a *Model.
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
