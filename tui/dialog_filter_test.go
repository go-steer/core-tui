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

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// typeIntoFilter feeds text to a filter row one grapheme at a time,
// the way Overlay.HandleKeyMsg does.
func typeIntoFilter(f *pickerFilter, text string) {
	for _, r := range text {
		f.handleKeyMsg(tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)}))
	}
}

// TestPickerNavStroke pins the split between what the LIST owns and
// what the filter input owns. Getting this wrong in either direction
// is a live bug: a nav key routed to the input types a control
// sequence into the filter, and a printable routed to the list is
// #117 unfixed.
func TestPickerNavStroke(t *testing.T) {
	nav := []string{"up", "down", "ctrl+p", "ctrl+n", "enter", "esc"}
	input := []string{
		"a", "Z", "9", "-", "/", " ", "日", "😀",
		"backspace", "delete", "left", "right", "home", "end",
		"ctrl+u", "ctrl+w", "ctrl+a", "alt+b", "pgup", "pgdown", "tab",
	}
	for _, stroke := range nav {
		if !pickerNavStroke(stroke) {
			t.Errorf("%q should drive the list, not the filter input", stroke)
		}
	}
	for _, stroke := range input {
		if pickerNavStroke(stroke) {
			t.Errorf("%q should reach the filter input, not the list", stroke)
		}
	}
}

// TestPickerFilter_ValueTrimsWhitespace: a trailing space the
// operator did not mean to type would otherwise match nothing at all,
// with no visible reason why.
func TestPickerFilter_ValueTrimsWhitespace(t *testing.T) {
	f := newPickerFilter()
	typeIntoFilter(&f, "  gpt  ")
	if got := f.value(); got != "gpt" {
		t.Errorf("value() = %q, want %q", got, "gpt")
	}
	if !f.active() {
		t.Error("a filter with text in it should be active")
	}

	blank := newPickerFilter()
	typeIntoFilter(&blank, "   ")
	if blank.active() {
		t.Errorf("a whitespace-only filter should not be active (value %q)", blank.value())
	}
}

// TestPickerFilter_HandleKeyMsgReportsChange pins the signal the
// pickers re-clamp their cursor on. A key the widget ignores must not
// claim the list changed, or every arrow-adjacent keystroke would
// reset the selection to row 0.
func TestPickerFilter_HandleKeyMsgReportsChange(t *testing.T) {
	f := newPickerFilter()
	if _, changed := f.handleKeyMsg(tea.KeyPressMsg(tea.Key{Code: 'g', Text: "g"})); !changed {
		t.Error("typing a printable should report a change")
	}
	if _, changed := f.handleKeyMsg(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace})); !changed {
		t.Error("backspace over a non-empty value should report a change")
	}
	if _, changed := f.handleKeyMsg(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace})); changed {
		t.Error("backspace on an empty value reported a change")
	}
	if _, changed := f.handleKeyMsg(tea.KeyPressMsg(tea.Key{Code: tea.KeyF1})); changed {
		t.Error("a key the widget ignores reported a change")
	}
}

// TestPickerFilter_RenderFitsTheDialog is the geometry the +1 on
// modalChromeRows pays for: the filter is ONE row, and it never
// overflows the dialog's inner width — including when the match
// count is beside it and when the value carries wide runes.
func TestPickerFilter_RenderFitsTheDialog(t *testing.T) {
	s := NewStylesWithTheme(true, goldenTheme())
	for _, width := range []int{30, 36, 64, 72, 120} {
		for _, value := range []string{"", "gpt", "日本語のモデル", strings.Repeat("x", 200)} {
			f := newPickerFilter()
			typeIntoFilter(&f, value)
			row := f.render(width, 3, 40, s)
			if h := lipgloss.Height(row); h != 1 {
				t.Errorf("width %d value %q: filter row is %d rows, want exactly 1",
					width, value, h)
			}
			if got := ansi.StringWidth(row); got > width-2 {
				t.Errorf("width %d value %q: filter row is %d cells, want <= %d",
					width, value, got, width-2)
			}
		}
	}
}

// TestPickerFilter_RenderShowsTheMatchCount: the palette has told
// operators how many rows matched since v0.1.0; the pickers never
// had it. Only once a filter is active — "40/40" on an untouched
// picker is noise.
func TestPickerFilter_RenderShowsTheMatchCount(t *testing.T) {
	s := NewStylesWithTheme(true, goldenTheme())

	idle := newPickerFilter()
	if got := ansi.Strip(idle.render(64, 40, 40, s)); strings.Contains(got, "40/40") {
		t.Errorf("idle filter row shows a count: %q", got)
	}
	if got := ansi.Strip(idle.render(64, 40, 40, s)); !strings.Contains(got, filterPlaceholder) {
		t.Errorf("idle filter row is missing the %q affordance: %q", filterPlaceholder, got)
	}

	active := newPickerFilter()
	typeIntoFilter(&active, "gpt")
	got := ansi.Strip(active.render(64, 3, 40, s))
	if !strings.Contains(got, "3/40") {
		t.Errorf("active filter row = %q, want it to carry the 3/40 count", got)
	}
	if !strings.Contains(got, "gpt") {
		t.Errorf("active filter row = %q, want it to show the typed value", got)
	}
}

// TestFilterRowCursor pins the chrome offsets, which are the same for
// all three pickers: the box edge plus one column of RenderContext
// padding across, and the box edge plus a title line plus a blank row
// above the body the filter heads.
//
// Spelled as literals rather than as modalContentX / modalBodyTop. The
// golden corpus captures tea.View.Content and not tea.View.Cursor, so
// nothing else in the package would notice the caret drifting off the
// character the operator is typing — and an assertion written in terms
// of the constants it guards would drift with them in silence.
func TestFilterRowCursor(t *testing.T) {
	if got := filterRowCursor(nil); got != nil {
		t.Errorf("a nil widget cursor must stay nil, got (%d,%d)", got.X, got.Y)
	}
	got := filterRowCursor(tea.NewCursor(4, 0))
	if got == nil || got.X != 6 || got.Y != 3 {
		t.Fatalf("filterRowCursor(4,0) = %v, want (6,3)", got)
	}
}

// TestHighlightSpan checks the visible half of the matcher: the span
// is emphasised, the surrounding text is not, and the plain text is
// preserved exactly so the row's width does not change.
func TestHighlightSpan(t *testing.T) {
	base := lipgloss.NewStyle()
	cases := []struct {
		name   string
		text   string
		filter string
		marked bool
	}{
		{name: "match", text: "openai/gpt-4o", filter: "gpt", marked: true},
		{name: "wide-runes", text: "日本語モデル", filter: "モデル", marked: true},
		{name: "no-match", text: "claude-opus", filter: "gpt"},
		{name: "empty-filter", text: "claude-opus", filter: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := highlightSpan(tc.text, tc.filter, base)
			if plain := ansi.Strip(got); plain != tc.text {
				t.Errorf("highlight changed the text: %q, want %q", plain, tc.text)
			}
			// ansi.Strip removing something means an escape was
			// emitted, i.e. part of the row is styled differently
			// from the rest.
			styled := len(got) != len(tc.text)
			if styled != tc.marked {
				t.Errorf("styled = %v, want %v (rendered %q)", styled, tc.marked, got)
			}
		})
	}
}

// TestPickerKey pins what the ranker matches a row against. A host
// that advertises "GPT-4o" for "openai/gpt-4o" prints both on the
// row, so typing either has to find it.
func TestPickerKey(t *testing.T) {
	cases := []struct{ display, id, want string }{
		{display: "", id: "openai/gpt-4o", want: "openai/gpt-4o"},
		{display: "GPT-4o", id: "", want: "GPT-4o"},
		{display: "gpt-4o", id: "gpt-4o", want: "gpt-4o"},
		{display: "GPT-4o", id: "openai/gpt-4o", want: "GPT-4o openai/gpt-4o"},
	}
	for _, tc := range cases {
		if got := pickerKey(tc.display, tc.id); got != tc.want {
			t.Errorf("pickerKey(%q, %q) = %q, want %q", tc.display, tc.id, got, tc.want)
		}
	}
}

// TestStepIndex_SurvivesAnEmptyList is the panic guard. The model and
// theme pickers moved their cursor with (idx ± 1 + len) % len, which
// is a division by zero the instant the list is empty — safe only
// because before the filter a picker's list could not shrink under
// it. It can now, between one keystroke and the next.
func TestStepIndex_SurvivesAnEmptyList(t *testing.T) {
	for _, delta := range []int{-1, 1, -7, 7} {
		if got := stepIndex(0, delta, 0); got != 0 {
			t.Errorf("stepIndex(0, %d, 0) = %d, want 0", delta, got)
		}
		if got := stepIndex(9, delta, 0); got != 0 {
			t.Errorf("stepIndex(9, %d, 0) = %d, want 0 (stale index, empty list)", delta, got)
		}
	}
}

// TestStepIndex_WrapsAndClamps: wraparound at both ends, and a stale
// index left over from a longer list is pulled into range before it
// moves rather than wrapping from a position that no longer exists.
func TestStepIndex_WrapsAndClamps(t *testing.T) {
	cases := []struct{ idx, delta, n, want int }{
		{idx: 0, delta: 1, n: 3, want: 1},
		{idx: 2, delta: 1, n: 3, want: 0},
		{idx: 0, delta: -1, n: 3, want: 2},
		{idx: 1, delta: -1, n: 3, want: 0},
		{idx: 0, delta: 1, n: 1, want: 0},
		{idx: 0, delta: -1, n: 1, want: 0},
		// Stale index from a list of 40, now filtered to 3.
		{idx: 37, delta: 1, n: 3, want: 0},
		{idx: 37, delta: -1, n: 3, want: 1},
		{idx: -5, delta: 1, n: 3, want: 1},
	}
	for _, tc := range cases {
		if got := stepIndex(tc.idx, tc.delta, tc.n); got != tc.want {
			t.Errorf("stepIndex(%d, %d, %d) = %d, want %d",
				tc.idx, tc.delta, tc.n, got, tc.want)
		}
	}
}

// TestClampIndex covers the same edges for the non-moving clamp.
func TestClampIndex(t *testing.T) {
	cases := []struct{ idx, n, want int }{
		{idx: 0, n: 0, want: 0},
		{idx: 5, n: 0, want: 0},
		{idx: -1, n: 4, want: 0},
		{idx: 0, n: 4, want: 0},
		{idx: 3, n: 4, want: 3},
		{idx: 4, n: 4, want: 3},
		{idx: 99, n: 4, want: 3},
	}
	for _, tc := range cases {
		if got := clampIndex(tc.idx, tc.n); got != tc.want {
			t.Errorf("clampIndex(%d, %d) = %d, want %d", tc.idx, tc.n, got, tc.want)
		}
	}
}
