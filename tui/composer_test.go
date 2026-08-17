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
	"strings"
	"testing"
)

// TestComposer_StaysFresh is the test the composer cache is worth
// having only because of.
//
// The cache's whole claim is that the string it hands out is what the
// textarea would have drawn. Nothing about the type proves that on
// its own — it proves that every method BELOW re-renders, which is a
// different statement, and the gap between the two is exactly a
// forwarder someone adds later without noticing it mutates. So the
// assertion here is differential rather than expected-value: drive
// the model the way a session does, and after every step compare the
// cached block against a render taken from the widget on the spot. A
// mutation that skipped its re-render shows up on the step after it,
// naming the step.
//
// It runs the mutation through the Model rather than the composer,
// because the composer is not the thing that can get this wrong — the
// call site is. A method that forgets to re-render is only reachable
// through a Model that calls it.
func TestComposer_StaysFresh(t *testing.T) {
	m := benchModel(t, 3, 100, 40)

	steps := []struct {
		name string
		do   func(m *Model)
	}{
		{"initial", func(*Model) {}},
		{"type a rune", func(m *Model) { keyIntoComposer(m, "h") }},
		{"type a word", func(m *Model) {
			for _, r := range "ello there" {
				keyIntoComposer(m, string(r))
			}
		}},
		{"backspace", func(m *Model) { pressIntoComposer(m, "backspace") }},
		{"newline", func(m *Model) { pressIntoComposer(m, "shift+enter") }},
		{"second line", func(m *Model) {
			for _, r := range "and a second line" {
				keyIntoComposer(m, string(r))
			}
		}},
		{"grow to the line count", func(m *Model) { m.syncInputHeight() }},
		{"cursor home", func(m *Model) { pressIntoComposer(m, "home") }},
		{"cursor up", func(m *Model) { pressIntoComposer(m, "up") }},
		// A width change re-wraps every line, so a stale block here is
		// the most visible failure the cache could produce.
		{"narrow", func(m *Model) { *m = resizeModel(*m, 40, 24) }},
		{"widen", func(m *Model) { *m = resizeModel(*m, 160, 50) }},
		{"blur", func(m *Model) { m.input.Blur() }},
		{"focus", func(m *Model) { _ = m.input.Focus() }},
		// Placeholder path: the empty box is the state the cache is
		// there for, and it renders through different code in bubbles.
		{"clear to the placeholder", func(m *Model) { m.input.Reset() }},
		{"resize while empty", func(m *Model) { *m = resizeModel(*m, 100, 40) }},
		{"blur while empty", func(m *Model) { m.input.Blur() }},
		{"focus while empty", func(m *Model) { _ = m.input.Focus() }},
		{"set a value outright", func(m *Model) { m.input.SetValue("a programmatic prompt") }},
		// Theme and prompt: both change styling rather than content,
		// which is the class of change a content-keyed cache would
		// have missed.
		{"theme swap", func(m *Model) { m.applyNamedTheme(BuiltinThemes()[1].Name) }},
		{"theme swap back", func(m *Model) { m.applyNamedTheme(BuiltinThemes()[0].Name) }},
		{"prompt glyph", func(m *Model) { m.input.SetPrompt("⎈ ") }},
		{"long value that wraps", func(m *Model) {
			m.input.SetValue(strings.Repeat("a long prompt that has to wrap several times over. ", 6))
		}},
		{"height clamp", func(m *Model) { m.syncInputHeight() }},
		{"reset again", func(m *Model) { m.input.Reset() }},
	}

	for _, step := range steps {
		step.do(&m)
		want := m.input.ta.View()
		if got := m.input.View(); got != want {
			t.Fatalf("after %q the cached block is stale\n cached:\n%s\n fresh:\n%s",
				step.name, got, want)
		}
	}
}

// TestComposer_ZeroValueDoesNotRender pins the reason `ready` exists.
//
// A Model built as a bare literal — which a good number of the dialog
// tests do — holds an unconstructed textarea, and bubbles panics both
// rendering one and resizing one. Neither of those was reachable
// before, because nothing drew a bare Model's input box. Moving the
// render from draw time to mutation time makes every mutator a
// potential draw, and SetPrompt a potential resize, so both have to
// decline on a composer newComposer never built.
//
// The bar is what the zero Model could already survive, not more:
// SetValue and Reset panic inside bubbles itself on an unconstructed
// textarea and did so before this type existed, so they are out of
// scope. The mutators below are the ones refreshTheme reaches, which
// is the path a bare Model actually takes.
func TestComposer_ZeroValueDoesNotRender(t *testing.T) {
	var m Model
	m.styles = NewStyles(true, Branding{})
	m.input.SetPrompt("> ")
	m.input.Blur()
	m.refreshTheme()
	if got := m.input.View(); got != "" {
		t.Errorf("an unconstructed composer rendered %q; it should render nothing", got)
	}
}

// TestComposer_RendersOncePerMutation is the cost claim, asserted
// rather than benchmarked: a draw of an untouched composer must not
// reach the textarea at all. Without it the type still passes the
// freshness test above while re-rendering on every View, which is the
// behaviour it exists to remove.
func TestComposer_RendersOncePerMutation(t *testing.T) {
	m := benchModel(t, 3, 100, 40)
	first := m.input.View()
	for i := 0; i < 50; i++ {
		if got := m.input.View(); got != first {
			t.Fatalf("draw %d returned a different block from draw 0", i)
		}
	}
	allocs := testing.AllocsPerRun(20, func() { _ = m.input.View() })
	if allocs != 0 {
		t.Errorf("drawing an untouched composer allocated %.0f objects; "+
			"it should be a field read", allocs)
	}
}

// keyIntoComposer sends one stroke to the model the way the terminal
// does, so the textarea is mutated through the same path a keystroke
// takes rather than through SetValue. Both spellings go through
// keyPress; the two names are kept apart only so the step table reads
// as what an operator did.
func keyIntoComposer(m *Model, s string) { pressIntoComposer(m, s) }

func pressIntoComposer(m *Model, stroke string) {
	out, _ := m.Update(keyPress(stroke))
	*m = out.(Model)
}
