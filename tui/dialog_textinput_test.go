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

// Tests for the text-input Dialog primitive (issue #56): typing,
// validation-keeps-open, submit-closure wiring, the KeyMsgDialog
// key path, and bracketed-paste routing.

package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// renderPlain renders a dialog and strips styling — the virtual
// cursor block wraps the character under it in its own escape
// sequence, which splits substrings apart in the raw output.
func renderPlain(d Dialog, m *Model) string {
	return ansi.Strip(d.Render(80, m))
}

// typeInto feeds each rune of s to the dialog as a separate
// keystroke, the way a terminal delivers typing.
func typeInto(t *testing.T, d Dialog, m *Model, s string) {
	t.Helper()
	for _, r := range s {
		act := d.HandleKey(string(r), m)
		if !act.Consumed {
			t.Fatalf("typing %q was not consumed", string(r))
		}
	}
}

// TestTextInputDialog_Defaults — the zero-ish config still yields a
// usable dialog (stable ID, a title, a renderable body).
func TestTextInputDialog_Defaults(t *testing.T) {
	m := NewModel(Options{Agent: &bareAgent{id: "a"}})
	m.viewport.SetWidth(80)

	d := NewTextInputDialog(TextInputConfig{})
	if d.ID() != textInputDialogID {
		t.Errorf("ID() = %q, want %q", d.ID(), textInputDialogID)
	}
	out := renderPlain(d, &m)
	if !strings.Contains(out, "Enter a Value") {
		t.Errorf("default title missing from render:\n%s", out)
	}
	if !strings.Contains(out, "esc cancel") {
		t.Errorf("default footer missing from render:\n%s", out)
	}
}

// TestTextInputDialog_TypeAndSubmit — typed runes accumulate, Enter
// hands the TRIMMED value to Submit, and Submit's DialogAction is
// what the dialog returns verbatim.
func TestTextInputDialog_TypeAndSubmit(t *testing.T) {
	m := NewModel(Options{Agent: &bareAgent{id: "a"}})
	m.viewport.SetWidth(80)

	var got string
	var sawModel bool
	d := NewTextInputDialog(TextInputConfig{
		Title:  "Attach",
		Prompt: "Daemon URL:",
		Submit: func(v string, mm *Model) DialogAction {
			got = v
			sawModel = mm != nil
			return DialogAction{Consumed: true, Close: true}
		},
	})

	typeInto(t, d, &m, "http://h:7778 ")
	act := d.HandleKey("enter", &m)

	if got != "http://h:7778" {
		t.Errorf("Submit value = %q, want the trimmed URL", got)
	}
	if !sawModel {
		t.Errorf("Submit did not receive the Model")
	}
	if !act.Consumed || !act.Close {
		t.Errorf("HandleKey(enter) = %+v, want Submit's Consumed+Close", act)
	}
}

// TestTextInputDialog_Editing — backspace / ctrl+u / cursor keys all
// reach the widget through the stroke-string path.
func TestTextInputDialog_Editing(t *testing.T) {
	m := NewModel(Options{Agent: &bareAgent{id: "a"}})
	m.viewport.SetWidth(80)

	var got string
	d := NewTextInputDialog(TextInputConfig{
		Submit: func(v string, _ *Model) DialogAction {
			got = v
			return DialogAction{Consumed: true, Close: true}
		},
	})

	typeInto(t, d, &m, "abcd")
	d.HandleKey("backspace", &m)
	typeInto(t, d, &m, "z")
	d.HandleKey("enter", &m)
	if got != "abcz" {
		t.Errorf("after backspace + z: %q, want %q", got, "abcz")
	}

	d.HandleKey("ctrl+u", &m) // delete before cursor = whole line
	typeInto(t, d, &m, "fresh")
	d.HandleKey("enter", &m)
	if got != "fresh" {
		t.Errorf("after ctrl+u: %q, want %q", got, "fresh")
	}
}

// TestTextInputDialog_SpaceIsTyped — "space" is a NAMED stroke, not
// a printable rune; regression guard that it inserts a space rather
// than being swallowed.
func TestTextInputDialog_SpaceIsTyped(t *testing.T) {
	m := NewModel(Options{Agent: &bareAgent{id: "a"}})
	m.viewport.SetWidth(80)

	var got string
	d := NewTextInputDialog(TextInputConfig{
		Submit: func(v string, _ *Model) DialogAction {
			got = v
			return DialogAction{Consumed: true, Close: true}
		},
	})
	typeInto(t, d, &m, "a")
	d.HandleKey("space", &m)
	typeInto(t, d, &m, "b")
	d.HandleKey("enter", &m)
	if got != "a b" {
		t.Errorf("value = %q, want %q", got, "a b")
	}
}

// TestTextInputDialog_ValidateKeepsOpen — a failing Validate renders
// inline, keeps the dialog open, keeps the typed text, and never
// calls Submit. A later fix submits normally.
func TestTextInputDialog_ValidateKeepsOpen(t *testing.T) {
	m := NewModel(Options{Agent: &bareAgent{id: "a"}})
	m.viewport.SetWidth(80)

	submits := 0
	d := NewTextInputDialog(TextInputConfig{
		Validate: func(v string) string {
			if !strings.HasPrefix(v, "http") {
				return "must start with http"
			}
			return ""
		},
		Submit: func(string, *Model) DialogAction {
			submits++
			return DialogAction{Consumed: true, Close: true}
		},
	})

	typeInto(t, d, &m, "nope")
	act := d.HandleKey("enter", &m)
	if act.Close {
		t.Errorf("validation failure must NOT close the dialog: %+v", act)
	}
	if submits != 0 {
		t.Errorf("Submit called %d times despite validation failure", submits)
	}
	if out := renderPlain(d, &m); !strings.Contains(out, "must start with http") {
		t.Errorf("validation error not rendered:\n%s", out)
	}
	// The buffer survives so the operator can edit rather than retype.
	if v := d.(*textInputDialog).Value(); v != "nope" {
		t.Errorf("value after failed validation = %q, want %q", v, "nope")
	}

	// Typing clears the stale error, and a valid value submits.
	typeInto(t, d, &m, "x")
	if out := renderPlain(d, &m); strings.Contains(out, "must start with http") {
		t.Errorf("stale validation error survived an edit:\n%s", out)
	}
	d.HandleKey("ctrl+u", &m)
	typeInto(t, d, &m, "http://ok")
	if act := d.HandleKey("enter", &m); !act.Close {
		t.Errorf("valid value should submit + close, got %+v", act)
	}
	if submits != 1 {
		t.Errorf("Submit called %d times, want 1", submits)
	}
}

// TestTextInputDialog_EscCloses — esc closes without submitting.
func TestTextInputDialog_EscCloses(t *testing.T) {
	m := NewModel(Options{Agent: &bareAgent{id: "a"}})
	m.viewport.SetWidth(80)

	submits := 0
	d := NewTextInputDialog(TextInputConfig{
		Submit: func(string, *Model) DialogAction {
			submits++
			return DialogAction{Consumed: true, Close: true}
		},
	})
	typeInto(t, d, &m, "half-typed")
	act := d.HandleKey("esc", &m)
	if !act.Consumed || !act.Close {
		t.Errorf("esc = %+v, want Consumed+Close", act)
	}
	if submits != 0 {
		t.Errorf("esc must not submit")
	}
}

// TestTextInputDialog_NilSubmitCloses — a config with no Submit is
// degenerate but must not panic.
func TestTextInputDialog_NilSubmitCloses(t *testing.T) {
	m := NewModel(Options{Agent: &bareAgent{id: "a"}})
	m.viewport.SetWidth(80)

	d := NewTextInputDialog(TextInputConfig{})
	typeInto(t, d, &m, "x")
	if act := d.HandleKey("enter", &m); !act.Close {
		t.Errorf("nil Submit should close, got %+v", act)
	}
}

// TestTextInputDialog_InitialAndPlaceholder — Initial pre-fills the
// buffer; Placeholder shows only while empty.
func TestTextInputDialog_InitialAndPlaceholder(t *testing.T) {
	m := NewModel(Options{Agent: &bareAgent{id: "a"}})
	m.viewport.SetWidth(80)

	empty := NewTextInputDialog(TextInputConfig{Placeholder: "http://host:7778"})
	if out := renderPlain(empty, &m); !strings.Contains(out, "http://host:7778") {
		t.Errorf("placeholder not rendered while empty:\n%s", out)
	}

	pre := NewTextInputDialog(TextInputConfig{
		Initial:     "http://seed:1",
		Placeholder: "http://host:7778",
	})
	out := renderPlain(pre, &m)
	if !strings.Contains(out, "http://seed:1") {
		t.Errorf("Initial value not rendered:\n%s", out)
	}
	if strings.Contains(out, "http://host:7778") {
		t.Errorf("placeholder rendered despite a pre-filled value:\n%s", out)
	}
	// Cursor lands at the end, so typing appends.
	var got string
	pre.(*textInputDialog).cfg.Submit = func(v string, _ *Model) DialogAction {
		got = v
		return DialogAction{Consumed: true, Close: true}
	}
	typeInto(t, pre, &m, "2")
	pre.HandleKey("enter", &m)
	if got != "http://seed:12" {
		t.Errorf("append after Initial = %q, want %q", got, "http://seed:12")
	}
}

// TestOverlay_HandleKeyMsg_PrefersKeyMsgDialog — the Overlay routes
// the raw KeyPressMsg to a KeyMsgDialog and falls back to the
// stroke-string contract for plain Dialogs.
func TestOverlay_HandleKeyMsg_PrefersKeyMsgDialog(t *testing.T) {
	m := NewModel(Options{Agent: &bareAgent{id: "a"}})
	m.viewport.SetWidth(80)

	var got string
	d := NewTextInputDialog(TextInputConfig{
		Submit: func(v string, _ *Model) DialogAction {
			got = v
			return DialogAction{Consumed: true, Close: true}
		},
	})
	m.overlayStack.Open(d)

	// Key.Text is what a KeyMsgDialog inserts — a stroke-string
	// round trip would lose it for anything exotic.
	for _, r := range "hé∂" {
		key := tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)})
		if consumed, _ := m.overlayStack.HandleKeyMsg(key, &m); !consumed {
			t.Fatalf("KeyPressMsg %q not consumed", string(r))
		}
	}
	consumed, _ := m.overlayStack.HandleKeyMsg(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}), &m)
	if !consumed {
		t.Fatalf("enter not consumed")
	}
	if got != "hé∂" {
		t.Errorf("value = %q, want %q", got, "hé∂")
	}
	if m.overlayStack.HasDialogs() {
		t.Errorf("Close: true from Submit should have popped the dialog")
	}

	// A plain Dialog (the model picker) still works through the
	// same entry point via the HandleKey fallback.
	m.overlayStack.Open(newModelPickerDialog())
	consumed, _ = m.overlayStack.HandleKeyMsg(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}), &m)
	if !consumed {
		t.Errorf("plain Dialog should still consume via the HandleKey fallback")
	}
}

// TestTextInputDialog_PasteRoutesToDialog — bracketed paste lands in
// the open dialog, NOT in the chat textarea behind it.
func TestTextInputDialog_PasteRoutesToDialog(t *testing.T) {
	m := NewModel(Options{Agent: &bareAgent{id: "a"}})
	m.viewport.SetWidth(80)

	var got string
	d := NewTextInputDialog(TextInputConfig{
		Submit: func(v string, _ *Model) DialogAction {
			got = v
			return DialogAction{Consumed: true, Close: true}
		},
	})
	m.overlayStack.Open(d)

	out, _ := m.Update(tea.PasteMsg{Content: "http://pasted:7778"})
	m = out.(Model)
	if v := m.input.Value(); v != "" {
		t.Errorf("paste leaked into the chat textarea: %q", v)
	}

	out, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = out.(Model)
	if got != "http://pasted:7778" {
		t.Errorf("pasted value = %q, want the URL", got)
	}
}

// TestTextInputDialog_PasteFallsThroughWithoutDialog — with no
// text-input dialog open, paste keeps its old behavior (chat input).
func TestTextInputDialog_PasteFallsThroughWithoutDialog(t *testing.T) {
	m := NewModel(Options{Agent: &bareAgent{id: "a"}})
	m.viewport.SetWidth(80)

	out, _ := m.Update(tea.PasteMsg{Content: "plain text"})
	m = out.(Model)
	if v := m.input.Value(); v != "plain text" {
		t.Errorf("chat textarea = %q, want the pasted text", v)
	}
}

// TestKeyMsgFromStroke — the stroke→KeyPressMsg shim must round-trip
// through String() so both Dialog entry points agree.
func TestKeyMsgFromStroke(t *testing.T) {
	for _, stroke := range []string{
		"a", "Z", "é", "space", "enter", "esc", "tab", "backspace",
		"delete", "left", "right", "up", "down", "home", "end",
		"ctrl+u", "ctrl+a", "ctrl+w", "alt+b", "alt+backspace",
	} {
		if got := keyMsgFromStroke(stroke).String(); got != stroke {
			t.Errorf("keyMsgFromStroke(%q).String() = %q", stroke, got)
		}
	}
}
