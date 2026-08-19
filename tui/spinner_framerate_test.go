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

// Spinner frame rate vs verb rotation (issue #162).
//
// The glyph and the verb were indexed by the same counter and that
// counter advanced once every spinnerCadence, so the Braille cycle ran
// at 0.33 Hz and read as frozen. Splitting the two rates is a one-line
// change and a one-line regression: putting the glyph back on the verb
// index is invisible to every other test in the package, because every
// other test looks at one frame at a time. These look at a run of
// consecutive frames, which is the only way to see motion.

package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// spinnerParts renders the thinking line at a given frame and splits
// it into the leading glyph and the text after it. Works on the
// stripped line because the glyph and the verb carry different styles
// and the boundary between them is a cell, not a byte.
func spinnerParts(t *testing.T, m *model, frame int) (glyph, rest string) {
	t.Helper()
	m.spinnerFrame = frame
	line := ansi.Strip(m.renderSpinnerLine())
	if line == "" {
		t.Fatalf("frame %d rendered no spinner line", frame)
	}
	r := []rune(line)
	return string(r[0]), strings.TrimSpace(string(r[1:]))
}

func TestSpinnerFrameRate_GlyphAdvancesOnEveryFrame(t *testing.T) {
	m := newModel(Options{Agent: stubAgent{}})
	m.viewport.SetWidth(80)
	m = m.submitTurn("go")

	prev, _ := spinnerParts(t, &m, 0)
	for frame := 1; frame < len(brailleSpinnerGlyphs); frame++ {
		glyph, _ := spinnerParts(t, &m, frame)
		if glyph == prev {
			t.Fatalf("frame %d drew the same glyph as frame %d (%q) — the animation is indexed "+
				"by something slower than the tick", frame, frame-1, glyph)
		}
		prev = glyph
	}
}

func TestSpinnerFrameRate_VerbHoldsForOneCadence(t *testing.T) {
	m := newModel(Options{Agent: stubAgent{}})
	m.viewport.SetWidth(80)
	m = m.submitTurn("go")

	_, want := spinnerParts(t, &m, 0)
	for frame := 1; frame < spinnerFramesPerVerb; frame++ {
		if _, got := spinnerParts(t, &m, frame); got != want {
			t.Fatalf("verb changed at frame %d of %d: %q → %q — the phrase is rotating at the "+
				"animation rate and will strobe", frame, spinnerFramesPerVerb, want, got)
		}
	}
	if _, got := spinnerParts(t, &m, spinnerFramesPerVerb); got == want {
		// Only meaningful with more than one phrase in the pool, which
		// the built-in pool has; a single-entry host pool would
		// legitimately repeat.
		if len(m.thinkingPhrases()) > 1 {
			t.Errorf("verb did not rotate at frame %d — it is stuck on %q", spinnerFramesPerVerb, want)
		}
	}
}

// TestSpinnerCadences_DivideEvenly guards the derivation itself. If
// someone sets spinnerFrameCadence to a value spinnerCadence is not a
// multiple of, integer division silently rounds and the phrase period
// drifts away from the 3 s R-CHAT-3 asks for — with no visible symptom
// beyond phrases that feel slightly wrong.
func TestSpinnerCadences_DivideEvenly(t *testing.T) {
	if spinnerFramesPerVerb < 2 {
		t.Fatalf("spinnerFramesPerVerb = %d: the glyph is not turning faster than the verb, "+
			"which is the whole point of splitting them", spinnerFramesPerVerb)
	}
	if got := spinnerFrameCadence * time.Duration(spinnerFramesPerVerb); got != spinnerCadence {
		t.Errorf("%d frames of %s is %s, but the verb period is %s — the two cadences do not "+
			"divide evenly and the phrase rotation has drifted",
			spinnerFramesPerVerb, spinnerFrameCadence, got, spinnerCadence)
	}
}
