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

// Tests for the two measurement helpers every renderer leans on
// (issue #210): wordWrap, which has to bound a line it cannot find a
// breakpoint in, and renderedWidth, which has to price a tab the way
// lipgloss will draw it rather than the way ansi.StringWidth counts
// it.

package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// TestWordWrap_BoundsEveryLine pins the property the function's name
// has always implied and did not hold: no emitted line is wider than
// the budget.
//
// The cases that matter are the ones with no space and no hyphen in
// them. ansi.Wordwrap honours the breakpoints and then gives up, so
// each of these used to come back as one line at its natural width —
// a bound that declines to bind for exactly the inputs a bound is for.
// A file path and a URL are the everyday ones; CJK is the one that
// cannot be fixed by choosing better breakpoints, since it has none.
func TestWordWrap_BoundsEveryLine(t *testing.T) {
	cases := []struct {
		name string
		s    string
	}{
		{"path", "/very/long/path/to/some/file/deep/in/a/tree/main.go"},
		{"url", "https://example.invalid/a/b/c?query=1&other=2&third=3"},
		{"blob", strings.Repeat("QUJDREVG", 12)},
		{"cjk", strings.Repeat("日本語のテキスト", 6)},
		{"hyphen-then-run", "a-b-c-" + strings.Repeat("d", 60)},
		{"prose", "hello world this is a sentence long enough to wrap twice"},
		{"styled-run", "\x1b[31m" + strings.Repeat("z", 60) + "\x1b[0m"},
		{"multiline", strings.Repeat("m", 40) + "\n" + strings.Repeat("n", 40)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, width := range []int{8, 20, 40} {
				got := wordWrap(tc.s, width)
				for i, line := range strings.Split(got, "\n") {
					if w := ansi.StringWidth(line); w > width {
						t.Errorf("width %d: line %d is %d cells: %q",
							width, i, w, ansi.Strip(line))
					}
				}
				// A bound that drops content is not a bound, it is a
				// truncation — and truncation here would silently eat
				// a file path rather than wrapping it.
				if visible(got) != visible(tc.s) {
					t.Errorf("width %d: content changed\n got %q\nwant %q",
						width, visible(got), visible(tc.s))
				}
			}
		})
	}
}

// visible reduces s to the glyphs a reader would see, dropping the
// styling and all whitespace. Comparing two of these answers "did the
// wrapper lose any content" without asserting anything about where it
// chose to break, which is the wrapper's business and differs per
// input. It cannot see a lost space; it can see a lost path segment,
// and that is the failure worth pinning.
func visible(s string) string {
	return strings.Join(strings.Fields(ansi.Strip(s)), "")
}

// TestWordWrap_LeavesFittingProseAlone guards the other half: the
// hard-break pass must be a no-op on every line the word-preserving
// pass already fitted, or this "fix" would start breaking words mid-
// token in ordinary assistant prose.
func TestWordWrap_LeavesFittingProseAlone(t *testing.T) {
	const prose = "the quick brown fox jumps over the lazy dog and keeps going"
	for _, width := range []int{12, 20, 40, 80} {
		want := ansi.Wordwrap(prose, width, " -")
		if got := wordWrap(prose, width); got != want {
			t.Errorf("width %d: hard-break pass altered fitting prose\n got %q\nwant %q",
				width, got, want)
		}
	}
}

// TestRenderedWidth_PricesATabTheWayLipglossDrawsIt pins the helper
// against lipgloss itself rather than against a hardcoded number, so
// it fails if lipgloss ever retunes its tab width instead of quietly
// agreeing with a stale constant.
func TestRenderedWidth_PricesATabTheWayLipglossDrawsIt(t *testing.T) {
	cases := []string{
		"no tabs here",
		"col1\tcol2",
		"\tleading",
		"trailing\t",
		"a\tb\tc\td",
		"日本\tご",
	}
	for _, s := range cases {
		want := lipgloss.Width(lipgloss.NewStyle().Render(s))
		if got := renderedWidth(s); got != want {
			t.Errorf("renderedWidth(%q) = %d, lipgloss renders it at %d", s, got, want)
		}
	}
}

// TestRenderMessage_UserCardWithATabStaysInTheColumn is the defect
// this came from. The card measures each line and pads it to the
// column, then hands line+pad to Render — which expands the tabs the
// measurement priced at zero, so a single tab put the card four cells
// past the chat column. The overrun was invisible because chatCutLine
// trims it on the way to the frame; the card just lost its right edge.
//
// The pad is all this pins. Wrapping still prices a tab at zero, so a
// line with enough tabs in it wraps late and overruns anyway — a turn
// of strings.Repeat("a\t", 20) still draws at 93 cells here. That is
// issue #217 and a wider change than this; the cases below are ones
// the wrap does not reach, so they isolate the arithmetic under test.
func TestRenderMessage_UserCardWithATabStaysInTheColumn(t *testing.T) {
	const width = 40
	m := NewModel(Options{})
	m.viewport.SetWidth(width)

	for _, text := range []string{
		"col1\tcol2",
		"\tindented",
		"a\tb\tc\td\te",
		"no tabs at all",
	} {
		out := m.renderMessage(Message{Role: RoleUser, Text: text})
		for i, line := range strings.Split(out, "\n") {
			if w := lipgloss.Width(line); w > width {
				t.Errorf("text %q line %d is %d cells, column is %d: %q",
					text, i, w, width, ansi.Strip(line))
			}
		}
	}
}
