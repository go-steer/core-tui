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

// Theme picker tests, from the app's side. What the widget answers on
// its own, with no Model anywhere, is question_external_test.go; this
// file is the other half — that the resolver turns those answers into
// the effects the picker has always had.
//
// Windowing is covered from dialog_scroll_test.go and the esc cascade
// from dialog_sessioninput_test.go.

package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// askThemePicker opens the picker exactly as /theme does — the
// question, plus the resolver that knows what its answer means.
func askThemePicker(m *Model) *themePickerQuestion {
	q := newThemePickerQuestion(BuiltinThemes(), m.themeName)
	m.overlayStack.ask(q, askOperator, themePickerResolver(m.themeName))
	return q
}

func openThemePickerFixture(t *testing.T) (Model, *themePickerQuestion) {
	t.Helper()
	m := NewModel(Options{Agent: &bareAgent{id: "theme"}})
	m.styles = NewStylesWithTheme(true, goldenTheme())
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = out.(Model)
	m.applyNamedTheme("default")
	return m, askThemePicker(&m)
}

// pressPicker sends one stroke to the front-most overlay and applies
// everything it schedules, so the test observes what an operator sees
// once the frame settles.
//
// Settling is the part that is new. The live preview is no longer a
// write the widget makes to a *Model it was handed — it cannot be,
// since Model.Update has a value receiver and any *Model the widget
// held would be a dead per-Update copy — so it arrives as a
// themePreviewMsg the Update loop applies. Commit and restore still
// happen synchronously, inside the resolver.
func pressPicker(t *testing.T, m *Model, stroke string) (consumed bool) {
	t.Helper()
	consumed, cmd := m.overlayStack.HandleKeyMsg(keyMsgFromStroke(stroke), m)
	for _, msg := range drainBatch(t, cmd) {
		out, follow := m.Update(msg)
		*m = out.(Model)
		for _, next := range drainBatch(t, follow) {
			out, _ = m.Update(next)
			*m = out.(Model)
		}
	}
	return consumed
}

func themeNames(themes []BuiltinTheme) []string {
	out := make([]string, len(themes))
	for i, bt := range themes {
		out[i] = bt.Name
	}
	return out
}

// TestThemePicker_TypingNarrowsTheList. "g" is the interesting filter
// on this registry: several names contain it and the tiers put the
// prefix matches first.
func TestThemePicker_TypingNarrowsTheList(t *testing.T) {
	m, q := openThemePickerFixture(t)
	if got := len(q.rows()); got != len(BuiltinThemes()) {
		t.Fatalf("precondition: unfiltered picker has %d rows, want %d",
			got, len(BuiltinThemes()))
	}

	typeIntoPicker(&m, "matrix")
	assertNameOrder(t, themeNames(q.rows()), []string{"matrix"})

	// Case folding, and a filter that matches several names.
	q.filter = newPickerFilter()
	typeIntoPicker(&m, "GE")
	got := themeNames(q.rows())
	if len(got) == 0 {
		t.Fatalf("filter %q matched nothing", "GE")
	}
	for _, name := range got {
		if !strings.Contains(strings.ToLower(name), "ge") {
			t.Errorf("filter %q kept %q, which does not contain it", "GE", name)
		}
	}
	// Prefix matches rank above substring ones: "gemini" and "gke"
	// both start with the filter, "pride" does not.
	if got[0] != "gke" && got[0] != "gemini" {
		t.Errorf("first match is %q, want a prefix match (%v)", got[0], got)
	}
}

// TestThemePicker_FilteringDoesNotPreview is the deliberate
// difference from the arrow keys. Cursor movement applies the focused
// theme so the operator sees it live; doing that on every keystroke
// of a filter would repaint the whole chat once per letter, which is
// a strobe rather than a preview. The footer promises "↑↓ preview",
// and that is exactly what it does.
func TestThemePicker_FilteringDoesNotPreview(t *testing.T) {
	m, q := openThemePickerFixture(t)
	before := m.themeName

	typeIntoPicker(&m, "matrix")
	if m.themeName != before {
		t.Errorf("typing a filter previewed %q; only ↑↓ should preview", m.themeName)
	}
	if got := themeNames(q.rows()); len(got) != 1 || got[0] != "matrix" {
		t.Fatalf("filter did not narrow to matrix: %v", got)
	}

	// ↑↓ still previews, over the FILTERED list.
	pressPicker(t, &m, "down")
	if m.themeName != "matrix" {
		t.Errorf("down previewed %q, want the only filtered row matrix", m.themeName)
	}
}

// TestThemePicker_PreviewDiesWithThePicker: the preview is scheduled
// as a message rather than written straight to the model, so it can
// land after the operator has already cancelled. It must not win when
// it does — the theme the operator explicitly escaped out of would
// otherwise reappear a frame later.
func TestThemePicker_PreviewDiesWithThePicker(t *testing.T) {
	m, _ := openThemePickerFixture(t)
	original := m.themeName

	_, cmd := m.overlayStack.HandleKeyMsg(keyMsgFromStroke("down"), &m)
	stale := drainBatch(t, cmd)
	if len(stale) != 1 {
		t.Fatalf("down scheduled %d messages, want just the preview", len(stale))
	}
	if _, ok := stale[0].(themePreviewMsg); !ok {
		t.Fatalf("down scheduled %T, want themePreviewMsg", stale[0])
	}

	// Esc first: the picker closes and the resolver restores.
	pressPicker(t, &m, "esc")
	if m.themeName != original {
		t.Fatalf("esc left theme = %q, want %q", m.themeName, original)
	}

	// Now the preview arrives, too late.
	out, _ := m.Update(stale[0])
	m = out.(Model)
	if m.themeName != original {
		t.Errorf("a preview delivered after esc changed the theme to %q, want %q",
			m.themeName, original)
	}
}

// TestThemePicker_EnterCommitsTheFilteredRow: the cursor indexes
// rows(), so Enter must apply the highlighted theme and not the one
// at that index of the unfiltered registry.
func TestThemePicker_EnterCommitsTheFilteredRow(t *testing.T) {
	var persisted []string
	m, q := openThemePickerFixture(t)
	m.opts.PersistThemeChoice = func(name string) error {
		persisted = append(persisted, name)
		return nil
	}

	typeIntoPicker(&m, "cyber")
	if got := themeNames(q.rows()); len(got) != 1 || got[0] != "cyberpunk" {
		t.Fatalf("filter matched %v, want just cyberpunk", got)
	}
	consumed, cmd := m.overlayStack.HandleKeyMsg(keyMsgFromStroke("enter"), &m)
	if !consumed {
		t.Error("enter was not consumed")
	}
	if m.overlayStack.HasDialogs() {
		t.Error("enter left the picker open")
	}
	if m.themeName != "cyberpunk" {
		t.Errorf("theme = %q, want cyberpunk", m.themeName)
	}
	if cmd == nil {
		t.Fatal("expected a Cmd carrying ThemeChangedMsg + the persist call")
	}
	// Persistence is host code and runs in the Cmd now (issue #137),
	// so the callback has NOT fired at the point Enter returns.
	if len(persisted) != 0 {
		t.Errorf("PersistThemeChoice ran on the Update goroutine: %v", persisted)
	}
	var announced bool
	for _, msg := range drainBatch(t, cmd) {
		switch v := msg.(type) {
		case ThemeChangedMsg:
			announced = v.Name == "cyberpunk"
		case persistDoneMsg:
			if v.err != nil {
				t.Errorf("persistDoneMsg carried %v", v.err)
			}
		}
	}
	if !announced {
		t.Error("no ThemeChangedMsg{cyberpunk} in the Cmd batch")
	}
	if len(persisted) != 1 || persisted[0] != "cyberpunk" {
		t.Errorf("PersistThemeChoice got %v, want [cyberpunk]", persisted)
	}
}

// TestThemePicker_EscRestoresAfterFiltering: esc still puts back the
// theme that was live when the picker opened, whatever previewing and
// filtering happened in between.
func TestThemePicker_EscRestoresAfterFiltering(t *testing.T) {
	m, _ := openThemePickerFixture(t)
	original := m.themeName

	typeIntoPicker(&m, "vapor")
	pressPicker(t, &m, "down") // preview the filtered row
	if m.themeName == original {
		t.Fatalf("precondition: the preview should have changed the theme")
	}
	if !pressPicker(t, &m, "esc") {
		t.Error("esc was not consumed")
	}
	if m.overlayStack.HasDialogs() {
		t.Error("esc left the picker open")
	}
	if m.themeName != original {
		t.Errorf("esc left theme = %q, want the pre-picker %q", m.themeName, original)
	}
}

// TestThemePicker_FilterMatchingNothingStaysOpen: no registry entry
// matches, so the picker holds the row on screen rather than closing
// or previewing something arbitrary.
func TestThemePicker_FilterMatchingNothingStaysOpen(t *testing.T) {
	m, q := openThemePickerFixture(t)
	before := m.themeName
	typeIntoPicker(&m, "zzz")
	if got := len(q.rows()); got != 0 {
		t.Fatalf("filter %q matched %d rows, want none", "zzz", got)
	}
	for _, stroke := range []string{"down", "up", "enter"} {
		if consumed := pressPicker(t, &m, stroke); !consumed {
			t.Errorf("%q on an empty filter result was not consumed", stroke)
		}
		if !m.overlayStack.HasDialogs() {
			t.Fatalf("%q on an empty filter result closed the picker", stroke)
		}
	}
	if m.themeName != before {
		t.Errorf("an empty filter result changed the theme to %q", m.themeName)
	}
	body := ansi.Strip(m.overlayStack.Render(100, &m))
	if !strings.Contains(body, "no themes match") {
		t.Errorf("empty-result body does not say so:\n%s", body)
	}
}

// TestThemePicker_ShrinkingListNeverPanics: the old cursor arithmetic
// here was (idx ± 1 + len) % len with len = len(BuiltinThemes()),
// which could not be zero. It can now.
func TestThemePicker_ShrinkingListNeverPanics(t *testing.T) {
	m, q := openThemePickerFixture(t)
	for _, r := range "gemzz" {
		if !m.overlayStack.HasDialogs() {
			// An enter on a non-empty result commits and closes;
			// re-open and keep narrowing.
			q = askThemePicker(&m)
		}
		typeIntoPicker(&m, string(r))
		for _, stroke := range []string{"down", "down", "up", "enter"} {
			if m.overlayStack.HasDialogs() {
				pressPicker(t, &m, stroke)
			}
		}
		if n := len(q.rows()); n > 0 && (q.idx < 0 || q.idx >= n) {
			t.Fatalf("cursor %d is outside the %d filtered rows", q.idx, n)
		}
		q.Body(100, m.height, m.styles)
	}
}

// TestThemePicker_CursorSitsInTheFilterRow — the picker was
// TestCursor_NilWhenNothingOwnsIt's "arrow-nav-dialog" case until it
// grew somewhere to type. It has to own the caret now, or the filter
// row would take CJK with no IME anchor (#105 / #123).
func TestThemePicker_CursorSitsInTheFilterRow(t *testing.T) {
	for _, typed := range []string{"matrix", "テーマ"} {
		t.Run(typed, func(t *testing.T) {
			m, _ := openThemePickerFixture(t)
			typeIntoPicker(&m, typed)
			assertCursorFollows(t, m.View(), filterPromptRail+typed)
		})
	}
}

// TestThemePicker_UnsizedRendersEveryFilteredRow keeps the
// pre-existing unsized behaviour (no WindowSizeMsg = no windowing)
// true of the filtered list too.
func TestThemePicker_UnsizedRendersEveryFilteredRow(t *testing.T) {
	m := Model{}
	m.styles = NewStyles(true, Branding{})
	q := askThemePicker(&m)
	typeIntoFilter(&q.filter, "e")
	rendered := ansi.Strip(m.overlayStack.Render(80, &m))
	rows := q.rows()
	if len(rows) < 2 {
		t.Fatalf("filter %q matched %d rows; the test needs several", "e", len(rows))
	}
	for _, bt := range rows {
		if !strings.Contains(rendered, bt.Name) {
			t.Errorf("theme %q missing from an unsized render:\n%s", bt.Name, rendered)
		}
	}
}
