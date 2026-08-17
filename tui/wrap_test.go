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
//
// Plus expandTabs and the wrap's half of the tab problem (issue
// #217). Everything below that involves a tab measures the RENDERED
// width — lipgloss.Width(Style.Render(line)) — rather than
// ansi.StringWidth. That is not a stylistic choice: ansi.StringWidth
// prices a TAB at zero, so a test written with it agrees with the bug
// and passes on the broken code.

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
// #216 fixed the pad and left the wrap, which is why the last three
// cases are here: they are the ones long enough to need wrapping, and
// on #216's code they drew at 93, 45 and 89 cells against a column of
// 40. #217 is the wrap half, and the two halves together are what
// makes the whole table hold.
func TestRenderMessage_UserCardWithATabStaysInTheColumn(t *testing.T) {
	const width = 40
	m := NewModel(Options{})
	m.viewport.SetWidth(width)

	for _, text := range []string{
		"col1\tcol2",
		"\tindented",
		"a\tb\tc\td\te",
		"no tabs at all",
		strings.Repeat("a\t", 20),
		strings.Repeat("word\tword ", 8),
		"\tone\ttab-indented\tline\tthat\tis\tlong\tenough\tto\thave\tto\twrap\ttwice",
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

// tabWrapCases are inputs whose measured width and drawn width
// disagree by contentTabWidth-1 per tab. Each one is a shape that
// turns up in a real transcript: a pasted table, tab-separated tool
// output, unfenced indented code, and the two the issue measured.
var tabWrapCases = []struct {
	name string
	s    string
}{
	{"single-tab", "col1\tcol2"},
	{"leading-tab", "\tindented line of text that goes on for a while"},
	{"tab-dense", strings.Repeat("a\t", 20)},
	{"tab-separated-words", strings.Repeat("word\tword ", 8)},
	{"pasted-table", "name\tsize\tmodified\nmain.go\t14kB\tyesterday\nREADME.md\t2kB\tlast week"},
	{"indented-code", "func main() {\n\tif err := run(); err != nil {\n\t\tlog.Fatal(err)\n\t}\n}"},
	{"tab-then-unbreakable", "\t" + strings.Repeat("z", 50)},
}

// TestWordWrap_BoundsATabBearingLine is the wrap half of #217.
//
// wordWrap composes ansi.Wordwrap and ansi.Wrap, and both bound
// against ansi.StringWidth, which prices a TAB at zero. So the
// wrapper fitted a line to the budget and lipgloss then drew it
// contentTabWidth-1 cells wider per tab — a bound that measured one
// string and shipped a different one. The backstops downstream cut
// the excess off, so the visible symptom was a tab-bearing line
// losing its tail rather than wrapping onto the next row.
func TestWordWrap_BoundsATabBearingLine(t *testing.T) {
	for _, tc := range tabWrapCases {
		t.Run(tc.name, func(t *testing.T) {
			for _, width := range []int{8, 20, 40} {
				got := wordWrap(tc.s, width)
				for i, line := range strings.Split(got, "\n") {
					if w := drawnWidth(line); w > width {
						t.Errorf("width %d: line %d measures %d but DRAWS at %d cells: %q",
							width, i, ansi.StringWidth(line), w, ansi.Strip(line))
					}
				}
				if visible(got) != visible(tc.s) {
					t.Errorf("width %d: content changed\n got %q\nwant %q",
						width, visible(got), visible(tc.s))
				}
			}
		})
	}
}

// TestWordWrapIndent_BoundsATabBearingLine covers the other wrapper,
// which is the one the transcript actually uses for every non-
// assistant role. It has an extra failure mode of its own: it pulls
// the leading whitespace off each source line and subtracts its BYTE
// length from the budget, so a line indented with tabs was charged
// one cell per tab for an indent drawn at contentTabWidth. The
// tab-then-unbreakable and indented-code cases are the ones that
// exercise it.
func TestWordWrapIndent_BoundsATabBearingLine(t *testing.T) {
	for _, tc := range tabWrapCases {
		t.Run(tc.name, func(t *testing.T) {
			for _, width := range []int{20, 40} {
				got := wordWrapIndent(tc.s, width, "   ")
				for i, line := range strings.Split(got, "\n") {
					if w := drawnWidth(line); w > width {
						t.Errorf("width %d: line %d measures %d but DRAWS at %d cells: %q",
							width, i, ansi.StringWidth(line), w, ansi.Strip(line))
					}
				}
			}
		})
	}
}

// TestExpandTabs_IsTheExpansionLipglossWouldHaveDone is the claim the
// whole change rests on: moving the expansion earlier changes no
// bytes, because it is the same substitution lipgloss performs on the
// way out.
//
// Pinned against lipgloss rather than against four literal spaces, so
// it fails if lipgloss ever retunes tabWidthDefault or starts
// aligning to tab stops instead of substituting a fixed run — either
// of which would make expanding early wrong, and neither of which a
// hardcoded expectation would notice.
func TestExpandTabs_IsTheExpansionLipglossWouldHaveDone(t *testing.T) {
	plain := lipgloss.NewStyle()
	for _, tc := range tabWrapCases {
		t.Run(tc.name, func(t *testing.T) {
			if got, want := plain.Render(expandTabs(tc.s)), plain.Render(tc.s); got != want {
				t.Errorf("expanding before Render is not what Render does\n got %q\nwant %q", got, want)
			}
			if strings.Contains(expandTabs(tc.s), "\t") {
				t.Errorf("expandTabs left a tab in %q", tc.s)
			}
		})
	}
}

// TestExpandTabs_LeavesTabFreeInputAlone guards the fast path: this
// runs on every wrap of every row of every frame, and the
// overwhelmingly common input has no tab in it at all.
func TestExpandTabs_LeavesTabFreeInputAlone(t *testing.T) {
	const s = "the quick brown fox jumps over the lazy dog"
	if got := expandTabs(s); got != s {
		t.Errorf("expandTabs altered tab-free input: %q", got)
	}
	if n := testing.AllocsPerRun(100, func() { _ = expandTabs(s) }); n != 0 {
		t.Errorf("expandTabs allocated %v times on tab-free input; it is on the per-frame path", n)
	}
}

// TestRenderMessage_EveryRoleStaysInTheColumnWithTabs is the
// acceptance criterion stated by the issue, widened past the user
// card that #216 already covered. Each role reaches a different
// wrapper — the user card and the three annotated roles go through
// wordWrapIndent, the raw assistant path through wordWrap — and a fix
// in one is no evidence about the others.
//
// The assistant case deliberately leaves Rendered empty. A Glamour
// render has already been wrapped by Glamour at its own width, and
// Display() hands it back untouched; the raw path is the one this
// package is responsible for.
func TestRenderMessage_EveryRoleStaysInTheColumnWithTabs(t *testing.T) {
	roles := []struct {
		name string
		role Role
	}{
		{"assistant", RoleAssistant},
		{"system", RoleSystem},
		{"notice", RoleNotice},
		{"error", RoleError},
	}
	for _, r := range roles {
		for _, tc := range tabWrapCases {
			t.Run(r.name+"/"+tc.name, func(t *testing.T) {
				for _, width := range []int{40, 80} {
					m := NewModel(Options{})
					m.viewport.SetWidth(width)
					out := m.renderMessage(Message{Role: r.role, Text: tc.s})
					for i, line := range strings.Split(out, "\n") {
						if w := lipgloss.Width(line); w > width {
							t.Errorf("width %d: line %d is %d cells: %q",
								width, i, w, ansi.Strip(line))
						}
					}
				}
			})
		}
	}
}

// TestPermissionInline_StaysInTheColumnWithTabs is the modal-body arm
// of the acceptance criteria. The block wraps each source line at
// bodyWidth and then prefixes every emitted line with a two-cell
// gutter rule, so a wrap that runs long overruns by the overrun plus
// the gutter — and Detail is host-supplied text, which is exactly
// where a tab comes from. A tab-indented diff or a `bash -c` with a
// heredoc in it is the everyday case.
//
// Both detail kinds are covered because they reach different
// wrappers: the default arm goes to wordWrap, DetailShell to
// renderShellDetail's wordWrapIndent.
func TestPermissionInline_StaysInTheColumnWithTabs(t *testing.T) {
	kinds := []struct {
		name string
		kind DetailKind
	}{
		{"default", DetailKind(0)},
		{"shell", DetailShell},
	}
	for _, k := range kinds {
		for _, tc := range tabWrapCases {
			t.Run(k.name+"/"+tc.name, func(t *testing.T) {
				for _, width := range []int{40, 80} {
					m := NewModel(Options{})
					m.viewport.SetWidth(width)
					m.pendingPermission = &PermissionRequest{
						ToolName:   "bash",
						Verb:       "run",
						Detail:     tc.s,
						DetailKind: k.kind,
					}
					for i, line := range strings.Split(m.renderPermissionInline(), "\n") {
						if w := drawnWidth(line); w > width {
							t.Errorf("width %d: line %d draws at %d cells: %q",
								width, i, w, ansi.Strip(line))
						}
					}
				}
			})
		}
	}
}

// drawnWidth is the width the terminal will actually give a line:
// lipgloss.Width of the line AFTER a Render has had its way with the
// tabs. lipgloss.Width alone is ansi.StringWidth, which prices a tab
// at zero and would agree with the bug under test.
func drawnWidth(line string) int {
	return lipgloss.Width(lipgloss.NewStyle().Render(line))
}
