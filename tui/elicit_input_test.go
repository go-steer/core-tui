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
	"testing"
	"unicode/utf8"
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
