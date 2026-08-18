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

// Unit tests for the clipping post-pass (issue #102). These
// exercise clipFrame directly, independent of whatever View()
// happens to compose; the end-to-end coverage across a width x
// height x UI-state matrix lives in frame_invariant_test.go.

package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// clipStyles is a fixed style bundle for these tests so an ANSI
// assertion can't be perturbed by a palette change.
func clipStyles() styleSet { return newStylesWithTheme(true, defaultTheme(true)) }

// TestClipFrame_TruncatesWidth is the width half of the invariant.
// The styled input matters: byte-level slicing would cut an escape
// sequence in half and spill the raw bytes into the terminal, which
// is exactly why the implementation uses ansi.Truncate.
func TestClipFrame_TruncatesWidth(t *testing.T) {
	styled := clipStyles().Accent.Render(strings.Repeat("ab", 40))
	got := clipFrame(styled+"\nplain "+strings.Repeat("c", 100), 20, 10)
	for i, line := range strings.Split(got, "\n") {
		if w := ansi.StringWidth(line); w > 20 {
			t.Errorf("line %d is %d cols after clipping to 20: %q", i, w, line)
		}
	}
	if strings.Count(got, "\n") != 1 {
		t.Errorf("clipFrame changed the line count: %q", got)
	}
}

// TestClipFrame_TruncatesWideRunes guards the reason width is
// measured with ansi.StringWidth rather than len: a CJK frame is
// two columns per rune and several bytes per rune, so a byte-based
// clamp would be wrong in both directions.
func TestClipFrame_TruncatesWideRunes(t *testing.T) {
	got := clipFrame(strings.Repeat("日", 40), 10, 5)
	if w := ansi.StringWidth(got); w > 10 {
		t.Errorf("wide-rune line is %d cols after clipping to 10: %q", w, got)
	}
}

// TestClipFrame_CapsHeight pins the documented tie-break: the FIRST
// height lines survive, because the header is the row an operator
// cannot afford to lose silently.
func TestClipFrame_CapsHeight(t *testing.T) {
	got := clipFrame("one\ntwo\nthree\nfour\nfive", 80, 3)
	if got != "one\ntwo\nthree" {
		t.Errorf("expected the first 3 lines, got %q", got)
	}
}

// TestClipFrame_NonPositiveDimensions guards the arithmetic: an
// unknown terminal geometry must pass the body through untouched
// rather than clipping everything to nothing.
func TestClipFrame_NonPositiveDimensions(t *testing.T) {
	in := "one\ntwo"
	for _, tc := range []struct{ w, h int }{{0, 10}, {10, 0}, {-1, 10}, {10, -1}, {0, 0}} {
		if got := clipFrame(in, tc.w, tc.h); got != in {
			t.Errorf("clipFrame(%d,%d) mutated the body: %q", tc.w, tc.h, got)
		}
	}
}

// TestClipFrame_NoOpWhenAlreadyFits documents that the post-pass is
// a safety net, not a layout step: a frame that already fits comes
// back byte-identical, ANSI included.
func TestClipFrame_NoOpWhenAlreadyFits(t *testing.T) {
	s := clipStyles()
	in := s.Accent.Render("fits") + "\n" + s.Muted.Render("also fits")
	if got := clipFrame(in, 80, 24); got != in {
		t.Errorf("clipFrame rewrote a frame that already fits:\n got %q\nwant %q", got, in)
	}
}

// TestView_ClipsOversizedFrame is the integration point: the post-
// pass has to be wired into View, not merely available. Drives a
// real Model at a size the composition is known to overflow.
func TestView_ClipsOversizedFrame(t *testing.T) {
	m := NewModel(Options{Agent: &bareAgent{id: "clip"}})
	m.helpOpen = true // the help panel renders rows ~82 cols wide
	out, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	m = out.(Model)
	lines := strings.Split(m.View().Content, "\n")
	if len(lines) > 12 {
		t.Errorf("View emitted %d lines into a 12-row terminal", len(lines))
	}
	for i, line := range lines {
		if w := ansi.StringWidth(line); w > 40 {
			t.Errorf("View line %d is %d cols in a 40-col terminal", i, w)
		}
	}
}
