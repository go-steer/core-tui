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

// TestSplitAtSafeBoundary pins the incremental-render boundary logic:
// stable prefix is everything up to the latest \n\n outside an open
// code fence; trailing is the in-flight chunk.
func TestSplitAtSafeBoundary(t *testing.T) {
	cases := []struct {
		name         string
		input        string
		wantStable   string
		wantTrailing string
	}{
		{
			name:         "no boundary yet",
			input:        "Just one paragraph, no double-newline yet",
			wantStable:   "",
			wantTrailing: "Just one paragraph, no double-newline yet",
		},
		{
			name:         "one boundary",
			input:        "First para.\n\nSecond para in progress",
			wantStable:   "First para.\n\n",
			wantTrailing: "Second para in progress",
		},
		{
			name:         "multiple boundaries — picks latest",
			input:        "Para 1.\n\nPara 2.\n\nPara 3 streaming",
			wantStable:   "Para 1.\n\nPara 2.\n\n",
			wantTrailing: "Para 3 streaming",
		},
		{
			name:         "open code fence — boundary inside fence is unsafe",
			input:        "Header\n\n```go\nfunc x() {\n\nstill inside fence",
			wantStable:   "Header\n\n",
			wantTrailing: "```go\nfunc x() {\n\nstill inside fence",
		},
		{
			name:         "closed code fence — boundary after close is safe",
			input:        "Header\n\n```\ncode\n```\n\nNext para streaming",
			wantStable:   "Header\n\n```\ncode\n```\n\n",
			wantTrailing: "Next para streaming",
		},
		{
			name:         "only boundary is inside open fence — no safe split",
			input:        "```go\nfunc foo() {\n\nstill open",
			wantStable:   "",
			wantTrailing: "```go\nfunc foo() {\n\nstill open",
		},
		{
			name:         "boundary at end (trailing empty)",
			input:        "Para complete.\n\n",
			wantStable:   "Para complete.\n\n",
			wantTrailing: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotStable, gotTrailing := splitAtSafeBoundary(tc.input)
			if gotStable != tc.wantStable {
				t.Errorf("stable = %q, want %q", gotStable, tc.wantStable)
			}
			if gotTrailing != tc.wantTrailing {
				t.Errorf("trailing = %q, want %q", gotTrailing, tc.wantTrailing)
			}
		})
	}
}

// --- theme-derived markdown (issue #116) -----------------------

// goldenMarkdownDoc exercises every element the theme now drives:
// a heading, body prose, an inline code span and a fenced block.
const goldenMarkdownDoc = "## Section\n\nBody with `inline` code.\n\n```go\nfunc main() {}\n```\n"

// TestTuiStyleConfig_DerivesFromTheme — the config used to take only
// a `dark bool`, which collapsed twelve themes into two markdown
// styles. Two themes at the same polarity must now produce different
// configs at every slot the operator actually looks at.
func TestTuiStyleConfig_DerivesFromTheme(t *testing.T) {
	a := tuiStyleConfig(MatrixTheme(true), true)
	b := tuiStyleConfig(ChristmasTheme(true), true)

	differs := func(name string, x, y *string) {
		t.Helper()
		if x == nil || y == nil {
			t.Errorf("%s: unset on one of the two themes (a=%v b=%v)", name, x, y)
			return
		}
		if *x == *y {
			t.Errorf("%s: both themes rendered %q — the slot is not theme-derived", name, *x)
		}
	}
	differs("H1.BackgroundColor", a.H1.BackgroundColor, b.H1.BackgroundColor)
	differs("H2.Color", a.H2.Color, b.H2.Color)
	differs("Code.Color", a.Code.Color, b.Code.Color)
	differs("HorizontalRule.Color", a.HorizontalRule.Color, b.HorizontalRule.Color)

	// Document takes FgBase, which Matrix and Christmas both inherit
	// from DefaultTheme — so this one is pinned to the token rather
	// than compared across themes. Glamour's own "252" would be the
	// regression.
	if a.Document.Color == nil || *a.Document.Color != hexColor(MatrixTheme(true).FgBase) {
		t.Errorf("Document.Color = %v, want the theme FgBase %s", a.Document.Color, hexColor(MatrixTheme(true).FgBase))
	}
	// cfg.Text is the innermost cascade level; setting a color there
	// wins over every enclosing block and repaints headings in body
	// color. It must stay unset.
	if a.Text.Color != nil {
		t.Errorf("Text.Color = %q, want unset — it out-cascades the heading colors", *a.Text.Color)
	}

	if a.CodeBlock.Theme == b.CodeBlock.Theme {
		t.Errorf("CodeBlock.Theme: both themes named %q", a.CodeBlock.Theme)
	}
	// Glamour registers CodeBlock.Chroma globally under one fixed
	// style name and skips the registration when that name is
	// already taken, so leaving the bundled sub-config in place
	// would let the FIRST theme to build a renderer own code-fence
	// colors for every theme after it.
	if a.CodeBlock.Chroma != nil || b.CodeBlock.Chroma != nil {
		t.Error("CodeBlock.Chroma must be nil so CodeBlock.Theme (the per-theme Chroma style) is what drives fences")
	}
	// H2-H6 keep losing the literal "##" prefix the bundled styles
	// leak into the viewport.
	if a.H2.Prefix != "" || a.H3.Prefix != "" || a.H6.Prefix != "" {
		t.Error("heading prefixes leaked back into the config — raw '##' will reach the viewport")
	}
}

// TestHeadingColor_RampsAccentToMuted — heading depth is derived
// from the Accent→FgMuted ramp rather than from five more Theme
// tokens, so the ramp has to actually be monotonic and anchored.
func TestHeadingColor_RampsAccentToMuted(t *testing.T) {
	theme := MatrixTheme(true)
	if got := headingColor(theme, 2); got != theme.Accent {
		t.Errorf("H2 = %v, want the theme Accent %v", got, theme.Accent)
	}
	if got, want := hexColor(headingColor(theme, 6)), hexColor(theme.FgMuted); got != want {
		t.Errorf("H6 = %s, want the theme FgMuted %s", got, want)
	}
	seen := map[string]int{}
	for level := 2; level <= 6; level++ {
		seen[hexColor(headingColor(theme, level))]++
	}
	if len(seen) != 5 {
		t.Errorf("heading ramp collapsed: %d distinct colors across H2-H6, want 5 (%v)", len(seen), seen)
	}
}

// TestRenderMarkdown_ThemeChangesHeadingsAndFences is the
// user-visible claim in issue #116: switching themes has to move the
// assistant text, not just the chrome around it.
func TestRenderMarkdown_ThemeChangesHeadingsAndFences(t *testing.T) {
	matrix := newMarkdownRenderer(MatrixTheme(true), true, 60).renderMarkdown(goldenMarkdownDoc)
	christmas := newMarkdownRenderer(ChristmasTheme(true), true, 60).renderMarkdown(goldenMarkdownDoc)
	if matrix == christmas {
		t.Fatal("two themes rendered byte-identical markdown")
	}
	pick := func(out, needle string) string {
		t.Helper()
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, needle) {
				return line
			}
		}
		t.Fatalf("no line containing %q in:\n%q", needle, out)
		return ""
	}
	if a, b := pick(matrix, "Section"), pick(christmas, "Section"); a == b {
		t.Errorf("heading did not re-theme:\n%q", a)
	}
	if a, b := pick(matrix, "func"), pick(christmas, "func"); a == b {
		t.Errorf("code fence did not re-theme:\n%q", a)
	}
	// The old hardcoded cool-blue heading indices must be gone from
	// every theme's output.
	for _, out := range []string{matrix, christmas} {
		for _, idx := range []string{"\x1b[38;5;75", "\x1b[38;5;39", "\x1b[38;5;147", "\x1b[38;5;110"} {
			if strings.Contains(out, idx) {
				t.Errorf("hardcoded heading index %q survived in themed output", idx)
			}
		}
	}
}

// TestRenderMarkdown_LightVariantDiffers — the dark flag still picks
// the bundled structural base, so a theme must not render the same
// bytes on a light terminal as on a dark one.
func TestRenderMarkdown_LightVariantDiffers(t *testing.T) {
	dark := newMarkdownRenderer(GopherTheme(true), true, 60).renderMarkdown(goldenMarkdownDoc)
	light := newMarkdownRenderer(GopherTheme(false), false, 60).renderMarkdown(goldenMarkdownDoc)
	if dark == light {
		t.Error("light and dark rendered identical markdown")
	}
}
