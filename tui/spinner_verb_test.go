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

// Spinner-verb punctuation normalization (issue #141).
//
// GlyphTruncate is the single authority for the trailing affordance
// on the thinking line, so a verb that punctuates itself has to be
// normalized before the append or the line reads "Thinking...…".
// The pools are host-overridable, so the property is asserted on
// the rendered line — not on the built-in data — and once more with
// a host-supplied pool to pin that the two paths agree.

package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// spinnerVerbCases is the punctuation corpus. want is the verb text
// expected on screen BEFORE the appended GlyphTruncate.
var spinnerVerbCases = []struct {
	name string
	in   string
	want string
}{
	{"ascii ellipsis", "Thinking...", "Thinking"},
	{"glyph ellipsis", "Thinking" + GlyphTruncate, "Thinking"},
	{"unpunctuated", "Thinking", "Thinking"},
	{"mixed run", "Thinking..." + GlyphTruncate, "Thinking"},
	{"repeated ascii", "Thinking......", "Thinking"},
	{"mid-phrase ellipsis survives", "Wait... then more", "Wait... then more"},
	{"empty", "", ""},
	{"all punctuation", "...", ""},
	{"space before the dots", "Thinking ...", "Thinking"},
	{"trailing space only", "Thinking  ", "Thinking"},
	{"bare period is not an ellipsis", "Done.", "Done."},
}

func TestTrimTrailingEllipsis(t *testing.T) {
	for _, tc := range spinnerVerbCases {
		t.Run(tc.name, func(t *testing.T) {
			got := trimTrailingEllipsis(tc.in)
			if got != tc.want {
				t.Errorf("trimTrailingEllipsis(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if again := trimTrailingEllipsis(got); again != got {
				t.Errorf("not idempotent: trimTrailingEllipsis(%q) = %q", got, again)
			}
		})
	}
}

// TestSpinnerLine_EndsInExactlyOneEllipsis is the defect stated as a
// property of the frame: whatever the host puts in the pool, the
// rendered line carries one "…" and no ASCII dot run in front of it.
func TestSpinnerLine_EndsInExactlyOneEllipsis(t *testing.T) {
	for _, tc := range spinnerVerbCases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewModel(Options{
				Agent:           stubAgent{},
				ThinkingPhrases: []string{tc.in},
			})
			m.viewport.SetWidth(80)
			m = m.submitTurn("go")

			line := ansi.Strip(m.renderSpinnerLine())
			assertOneTrailingEllipsis(t, line, tc.want)
		})
	}
}

// TestSpinnerLine_WorkingPoolIsNormalizedToo covers the other pool:
// the tool-active branch of renderSpinnerLine reads WorkingPhrases,
// and a fix that only touched the thinking path would leave the
// double punctuation reachable through a tool call.
func TestSpinnerLine_WorkingPoolIsNormalizedToo(t *testing.T) {
	m := NewModel(Options{
		Agent:           stubAgent{},
		ThinkingPhrases: []string{"Thinking..."},
		WorkingPhrases:  []string{"Running tools..."},
	})
	m.viewport.SetWidth(80)
	m = m.submitTurn("go")
	m.toolActive = true

	assertOneTrailingEllipsis(t, ansi.Strip(m.renderSpinnerLine()), "Running tools")
}

// TestSpinnerLine_DefaultPoolsAreNormalized walks every built-in
// entry rather than sampling one: the pools are hand-maintained
// prose, so the guard has to be against the whole list.
func TestSpinnerLine_DefaultPoolsAreNormalized(t *testing.T) {
	m := NewModel(Options{Agent: stubAgent{}})
	m.viewport.SetWidth(80)
	m = m.submitTurn("go")

	for _, toolActive := range []bool{false, true} {
		m.toolActive = toolActive
		pool := m.thinkingPhrases()
		if toolActive {
			pool = m.workingPhrases()
		}
		for i, verb := range pool {
			// spinnerFrame counts glyph frames, and the verb only
			// advances every spinnerFramesPerVerb of them (issue
			// #162), so selecting pool entry i means landing on the
			// first frame of its window rather than on frame i.
			m.spinnerFrame = i * spinnerFramesPerVerb
			line := ansi.Strip(m.renderSpinnerLine())
			assertOneTrailingEllipsis(t, line, trimTrailingEllipsis(verb))
		}
	}
}

// assertOneTrailingEllipsis checks the shape of an ANSI-stripped
// spinner line: the normalized verb, then exactly one GlyphTruncate,
// with no ASCII dot run in between.
func assertOneTrailingEllipsis(t *testing.T, line, wantVerb string) {
	t.Helper()
	if line == "" {
		t.Fatal("renderSpinnerLine() = \"\", want a spinner line")
	}
	idx := strings.Index(line, wantVerb+GlyphTruncate)
	if idx < 0 {
		t.Fatalf("spinner line = %q, want it to carry %q", line, wantVerb+GlyphTruncate)
	}
	if n := strings.Count(line, GlyphTruncate); n != 1 {
		t.Errorf("spinner line = %q has %d %q, want exactly 1", line, n, GlyphTruncate)
	}
	// Nothing between the verb text and the glyph, and no ASCII run
	// immediately before it either.
	before := line[:idx+len(wantVerb)]
	if strings.HasSuffix(before, "..") || strings.HasSuffix(before, ". ") {
		t.Errorf("spinner line = %q double-punctuates ahead of %q — issue #141", line, GlyphTruncate)
	}
}
