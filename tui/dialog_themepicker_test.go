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

// Theme picker tests. Windowing is covered from dialog_scroll_test.go
// and esc-restores from dialog_sessioninput_test.go; this file is the
// dialog's own, added with type-to-filter (#117).
//
// The theme picker is the odd one of the three: it had no rows()
// accessor at all, reading BuiltinThemes() directly from both
// HandleKey and Render, and it previews the focused theme live. Both
// of those interact with a filter.

package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func openThemePickerFixture(t *testing.T) (Model, *themePickerDialog) {
	t.Helper()
	m := NewModel(Options{Agent: &bareAgent{id: "theme"}})
	m.styles = NewStylesWithTheme(true, goldenTheme())
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = out.(Model)
	m.applyNamedTheme("default")
	d := newThemePickerDialog(m.themeName)
	m.overlayStack.Open(d)
	return m, d
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
	m, d := openThemePickerFixture(t)
	if got := len(d.rows()); got != len(BuiltinThemes()) {
		t.Fatalf("precondition: unfiltered picker has %d rows, want %d",
			got, len(BuiltinThemes()))
	}

	typeIntoPicker(&m, "matrix")
	assertNameOrder(t, themeNames(d.rows()), []string{"matrix"})

	// Case folding, and a filter that matches several names.
	d.filter = newPickerFilter()
	typeIntoPicker(&m, "GE")
	got := themeNames(d.rows())
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
	m, d := openThemePickerFixture(t)
	before := m.themeName

	typeIntoPicker(&m, "matrix")
	if m.themeName != before {
		t.Errorf("typing a filter previewed %q; only ↑↓ should preview", m.themeName)
	}
	if got := themeNames(d.rows()); len(got) != 1 || got[0] != "matrix" {
		t.Fatalf("filter did not narrow to matrix: %v", got)
	}

	// ↑↓ still previews, over the FILTERED list.
	d.HandleKey("down", &m)
	if m.themeName != "matrix" {
		t.Errorf("down previewed %q, want the only filtered row matrix", m.themeName)
	}
}

// TestThemePicker_EnterCommitsTheFilteredRow: the cursor indexes
// rows(), so Enter must apply the highlighted theme and not the one
// at that index of the unfiltered registry.
func TestThemePicker_EnterCommitsTheFilteredRow(t *testing.T) {
	var persisted []string
	m, d := openThemePickerFixture(t)
	m.opts.PersistThemeChoice = func(name string) error {
		persisted = append(persisted, name)
		return nil
	}

	typeIntoPicker(&m, "cyber")
	if got := themeNames(d.rows()); len(got) != 1 || got[0] != "cyberpunk" {
		t.Fatalf("filter matched %v, want just cyberpunk", got)
	}
	act := d.HandleKey("enter", &m)
	if !act.Consumed || !act.Close {
		t.Errorf("enter = %+v, want Consumed and Close", act)
	}
	if m.themeName != "cyberpunk" {
		t.Errorf("theme = %q, want cyberpunk", m.themeName)
	}
	if len(persisted) != 1 || persisted[0] != "cyberpunk" {
		t.Errorf("PersistThemeChoice got %v, want [cyberpunk]", persisted)
	}
	if act.Cmd == nil {
		t.Fatal("expected a ThemeChangedMsg Cmd")
	}
	if msg, ok := act.Cmd().(ThemeChangedMsg); !ok || msg.Name != "cyberpunk" {
		t.Errorf("Cmd emitted %#v, want ThemeChangedMsg{cyberpunk}", act.Cmd())
	}
}

// TestThemePicker_EscRestoresAfterFiltering: esc still puts back the
// theme that was live when the picker opened, whatever previewing and
// filtering happened in between.
func TestThemePicker_EscRestoresAfterFiltering(t *testing.T) {
	m, d := openThemePickerFixture(t)
	original := m.themeName

	typeIntoPicker(&m, "vapor")
	d.HandleKey("down", &m) // preview the filtered row
	if m.themeName == original {
		t.Fatalf("precondition: the preview should have changed the theme")
	}
	act := d.HandleKey("esc", &m)
	if !act.Consumed || !act.Close {
		t.Errorf("esc = %+v, want Consumed and Close", act)
	}
	if m.themeName != original {
		t.Errorf("esc left theme = %q, want the pre-picker %q", m.themeName, original)
	}
}

// TestThemePicker_FilterMatchingNothingStaysOpen: no registry entry
// matches, so the picker holds the row on screen rather than closing
// or previewing something arbitrary.
func TestThemePicker_FilterMatchingNothingStaysOpen(t *testing.T) {
	m, d := openThemePickerFixture(t)
	before := m.themeName
	typeIntoPicker(&m, "zzz")
	if got := len(d.rows()); got != 0 {
		t.Fatalf("filter %q matched %d rows, want none", "zzz", got)
	}
	for _, stroke := range []string{"down", "up", "enter"} {
		if act := d.HandleKey(stroke, &m); !act.Consumed || act.Close {
			t.Errorf("%q on an empty filter result = %+v, want Consumed and not Close", stroke, act)
		}
	}
	if m.themeName != before {
		t.Errorf("an empty filter result changed the theme to %q", m.themeName)
	}
	body := ansi.Strip(d.Render(100, &m))
	if !strings.Contains(body, "no themes match") {
		t.Errorf("empty-result body does not say so:\n%s", body)
	}
}

// TestThemePicker_ShrinkingListNeverPanics: the old cursor arithmetic
// here was (idx ± 1 + len) % len with len = len(BuiltinThemes()),
// which could not be zero. It can now.
func TestThemePicker_ShrinkingListNeverPanics(t *testing.T) {
	m, d := openThemePickerFixture(t)
	for _, r := range "gemzz" {
		typeIntoPicker(&m, string(r))
		for _, stroke := range []string{"down", "down", "up", "enter"} {
			d.HandleKey(stroke, &m)
		}
		if n := len(d.rows()); n > 0 && (d.idx < 0 || d.idx >= n) {
			t.Fatalf("cursor %d is outside the %d filtered rows", d.idx, n)
		}
		d.Render(100, &m)
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
	d := newThemePickerDialog("default")
	typeIntoFilter(&d.filter, "e")
	rendered := ansi.Strip(d.Render(80, &m))
	rows := d.rows()
	if len(rows) < 2 {
		t.Fatalf("filter %q matched %d rows; the test needs several", "e", len(rows))
	}
	for _, bt := range rows {
		if !strings.Contains(rendered, bt.Name) {
			t.Errorf("theme %q missing from an unsized render:\n%s", bt.Name, rendered)
		}
	}
}
