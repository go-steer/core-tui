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
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// focusedModel is followModel already switched to transcript focus,
// parked mid-transcript so a scroll key can prove itself in either
// direction.
func focusedModel(t *testing.T, rows int) Model {
	t.Helper()
	m := followModel(t, 100, 40, rows)
	m.setFocus(focusTranscript)
	m.chatSetYOffset(m.chatYOffset() / 2)
	m.syncFollow()
	return m
}

// press feeds one stroke through the real Update path.
func press(m Model, stroke string) Model {
	out, _ := m.Update(keyPress(stroke))
	return out.(Model)
}

// Tab is the whole entry point to the mode, and blurring the
// composer is what the mode is FOR: bubbles drops every message a
// blurred textarea gets, so this is also the assertion that typed
// text can't reach a prompt the operator isn't looking at.
func TestFocus_TabMovesTheKeyboardAndBlursTheComposer(t *testing.T) {
	m := followModel(t, 100, 40, 200)
	if m.focus != focusInput {
		t.Fatalf("setup: fresh model started at focus=%d, want the composer", m.focus)
	}
	if !m.input.Focused() {
		t.Fatal("setup: fresh model's textarea is not focused")
	}

	m = press(m, "tab")
	if m.focus != focusTranscript {
		t.Error("tab did not move the keyboard to the transcript")
	}
	if m.input.Focused() {
		t.Error("transcript holds the keyboard but the textarea is still focused")
	}

	m = press(m, "tab")
	if m.focus != focusInput {
		t.Error("tab did not bring the keyboard back to the composer")
	}
	if !m.input.Focused() {
		t.Error("composer holds the keyboard but the textarea is still blurred")
	}
}

// The point of a focus model is that the letter and arrow space
// stops belonging to the prompt (#151). Typing while the transcript
// has focus must leave the draft alone — including the letters the
// transcript's own keymap uses.
func TestFocus_TranscriptKeepsTypedTextOutOfTheComposer(t *testing.T) {
	m := focusedModel(t, 200)
	m.input.SetValue("half-written prompt")

	for _, stroke := range []string{"j", "k", "g", "x", "?"} {
		m = press(m, stroke)
	}
	if got := m.input.Value(); got != "half-written prompt" {
		t.Errorf("keystrokes leaked into the composer: %q", got)
	}
}

// Issue #155. The arrows and j/k scroll by a line, g/G and home/end
// jump, and G re-arms follow — the same contract ctrl+l and end
// already had from the composer side.
func TestFocus_TranscriptScrollKeys(t *testing.T) {
	cases := []struct {
		stroke string
		want   func(before, after int) bool
		desc   string
		follow bool
	}{
		{stroke: "up", desc: "one line up", want: func(b, a int) bool { return a == b-1 }},
		{stroke: "k", desc: "one line up", want: func(b, a int) bool { return a == b-1 }},
		{stroke: "down", desc: "one line down", want: func(b, a int) bool { return a == b+1 }},
		{stroke: "j", desc: "one line down", want: func(b, a int) bool { return a == b+1 }},
		{stroke: "home", desc: "top", want: func(_, a int) bool { return a == 0 }},
		{stroke: "g", desc: "top", want: func(_, a int) bool { return a == 0 }},
		{stroke: "end", desc: "bottom", want: func(b, a int) bool { return a > b }, follow: true},
		{stroke: "G", desc: "bottom", want: func(b, a int) bool { return a > b }, follow: true},
	}
	for _, tc := range cases {
		t.Run(tc.stroke, func(t *testing.T) {
			m := focusedModel(t, 200)
			before := m.chatYOffset()
			m = press(m, tc.stroke)
			after := m.chatYOffset()
			if !tc.want(before, after) {
				t.Errorf("%q should scroll to %s: YOffset %d → %d", tc.stroke, tc.desc, before, after)
			}
			if m.follow != tc.follow {
				t.Errorf("%q left follow=%v, want %v", tc.stroke, m.follow, tc.follow)
			}
			if m.focus != focusTranscript {
				t.Errorf("%q dropped out of transcript focus", tc.stroke)
			}
		})
	}
}

// The composer keeps the arrows: prompt-history recall is what they
// meant before the focus model and what they still mean where a
// prompt is being composed (#155's "history recall keeps the arrows
// while the input has focus").
func TestFocus_ComposerKeepsTheArrowsForHistoryRecall(t *testing.T) {
	m := followModel(t, 100, 40, 200)
	m.recordPrompt("earlier prompt")
	before := m.chatYOffset()

	m = press(m, "up")
	if got := m.input.Value(); got != "earlier prompt" {
		t.Errorf("up in the composer recalled %q, want the prompt history entry", got)
	}
	if got := m.chatYOffset(); got != before {
		t.Errorf("up in the composer scrolled the transcript: YOffset %d → %d", before, got)
	}
}

// Both of the in-mode exits, plus the assertion that enter does NOT
// also submit: the caret is invisible from here and firing a
// half-written prompt is not undoable.
func TestFocus_EnterAndTabReturnToTheComposerWithoutSubmitting(t *testing.T) {
	for _, stroke := range []string{"enter", "tab", "esc"} {
		t.Run(stroke, func(t *testing.T) {
			m := focusedModel(t, 200)
			m.input.SetValue("half-written prompt")

			m = press(m, stroke)
			if m.focus != focusInput {
				t.Errorf("%q did not return the keyboard to the composer", stroke)
			}
			if !m.input.Focused() {
				t.Errorf("%q returned focus but left the textarea blurred", stroke)
			}
			if got := m.input.Value(); got != "half-written prompt" {
				t.Errorf("%q disturbed the draft: %q", stroke, got)
			}
			if m.state == stateStreaming {
				t.Errorf("%q submitted the draft", stroke)
			}
		})
	}
}

// Esc's cascade order (issue #151): focus is the innermost thing the
// operator is inside of, so the first press backs out of it and the
// second reaches the interrupt. The cost is real and deliberate —
// this test is the record of the trade.
func TestFocus_EscReturnsTheKeyboardBeforeInterrupting(t *testing.T) {
	cancelled := false
	m := focusedModel(t, 200)
	m.state = stateStreaming
	m.cancelTurn = func() { cancelled = true }

	m = press(m, "esc")
	if cancelled {
		t.Error("the first esc interrupted the turn instead of returning the keyboard")
	}
	if m.focus != focusInput {
		t.Error("the first esc did not return the keyboard to the composer")
	}

	m = press(m, "esc")
	if !cancelled {
		t.Error("the second esc did not interrupt the turn")
	}
}

// A bracketed paste never goes through handleKey, and a blurred
// textarea drops everything — so without the focus grab in the
// PasteMsg arm the paste vanishes silently.
func TestFocus_PasteTakesTheKeyboardBack(t *testing.T) {
	m := focusedModel(t, 200)

	out, _ := m.Update(tea.PasteMsg{Content: "pasted text"})
	m = out.(Model)

	if m.focus != focusInput {
		t.Error("a paste left the keyboard on the transcript")
	}
	if got := m.input.Value(); !strings.Contains(got, "pasted text") {
		t.Errorf("the paste did not reach the composer: %q", got)
	}
}

// The frame-level chords are the frame's, not the composer's:
// needing to leave focus mode to press ctrl+g would defeat the mode.
func TestFocus_GlobalChordsStillFireFromTranscriptFocus(t *testing.T) {
	t.Run("ctrl+b", func(t *testing.T) {
		m := focusedModel(t, 200)
		before := m.statusLayout
		m = press(m, "ctrl+b")
		if m.statusLayout == before {
			t.Error("ctrl+b did not toggle the status layout from transcript focus")
		}
		if m.focus != focusTranscript {
			t.Error("ctrl+b dropped the focus mode")
		}
	})
	t.Run("?", func(t *testing.T) {
		// `?` normally defers to a non-empty draft. In focus mode
		// there is no typing to protect, so the key the legend
		// advertises has to work regardless.
		m := focusedModel(t, 200)
		m.input.SetValue("half-written prompt")
		m = press(m, "?")
		if !m.helpOpen {
			t.Error("? did not open the help panel from transcript focus")
		}
	})
	t.Run("ctrl+l", func(t *testing.T) {
		m := focusedModel(t, 200)
		m = press(m, "ctrl+l")
		if m.chatYOffset() != 0 || m.follow {
			t.Errorf("ctrl+l left YOffset=%d follow=%v", m.chatYOffset(), m.follow)
		}
	})
}

// A mode that swallows typing has to be visible without pressing a
// key to find out. Three signals: the legend names the mode, the
// composer's top border drops to the quiet token, and the hardware
// cursor goes away with the textarea's focus.
func TestFocus_ModeIsVisibleInTheFrame(t *testing.T) {
	composer := followModel(t, 100, 40, 20)
	transcript := focusedModel(t, 20)

	if hint := transcript.footerHint(); !strings.Contains(hint, "Transcript") {
		t.Errorf("footer legend does not name the mode: %q", hint)
	}
	if hint := composer.footerHint(); strings.Contains(hint, "Transcript") {
		t.Errorf("composer footer names the mode it is not in: %q", hint)
	}

	// The rule is the same glyph run either way — what changes is
	// the style, so compare the raw bytes and the stripped ones.
	cBox, tBox := composer.renderInputBox(), transcript.renderInputBox()
	if ansi.Strip(cBox) != ansi.Strip(tBox) {
		t.Error("the focus indicator changed the composer's text, not just its color")
	}
	if cBox == tBox {
		t.Error("the composer's top border looks identical in both focus states")
	}

	if transcript.textareaCursor(inputOrigin{}) != nil {
		t.Error("the hardware cursor is still on a composer that is not listening")
	}
	if composer.textareaCursor(inputOrigin{}) == nil {
		t.Error("the composer lost its cursor while it holds the keyboard")
	}
}

// A turn ending is not a reason to yank the keyboard out of a
// transcript the operator deliberately moved it to. A session
// switch is: the transcript they focused is gone.
func TestFocus_SurvivesTurnEndAndResetsOnSessionSwitch(t *testing.T) {
	t.Run("turn end keeps it", func(t *testing.T) {
		m := focusedModel(t, 20)
		m.state = stateStreaming
		m.finalizeTurn(time.Second, "")
		if m.focus != focusTranscript {
			t.Error("the end of a turn stole the keyboard back from the transcript")
		}
		if m.input.Focused() {
			t.Error("the end of a turn re-armed a textarea that should stay blurred")
		}
	})
	t.Run("session switch resets it", func(t *testing.T) {
		m := focusedModel(t, 20)
		m.applySwitchTarget(&SwitchTarget{Agent: &bareAgent{id: "b"}})
		if m.focus != focusInput {
			t.Error("a session switch left the keyboard parked on the old transcript")
		}
		if !m.input.Focused() {
			t.Error("a session switch returned focus but left the textarea blurred")
		}
	})
}

// The sibling of TestHelpPanel_NavigationKeysAreAllBound: the panel
// must not advertise a focus-mode key that does nothing. Every row
// under "Transcript focus" has to either move the viewport or move
// the keyboard.
func TestHelpPanel_TranscriptFocusKeysAreAllBound(t *testing.T) {
	m := focusedModel(t, 200)
	m.helpOpen = true

	keys := helpPanelSectionKeys(t, m, "Transcript focus")
	if len(keys) == 0 {
		t.Fatal("no Transcript focus keys parsed out of the help panel")
	}

	// The panel spells the arrows as glyphs; the keymap spells them
	// as strokes. Parenthesised prose ("(esc" from the enter row)
	// is not a binding.
	glyphs := map[string]string{"↑": "up", "↓": "down"}
	for _, key := range keys {
		stroke, ok := glyphs[key]
		if !ok {
			stroke = key
		}
		if strings.ContainsAny(stroke, "()") {
			continue
		}
		t.Run(stroke, func(t *testing.T) {
			probe := focusedModel(t, 200)
			before := probe.chatYOffset()

			probe = press(probe, stroke)
			if probe.chatYOffset() == before && probe.focus == focusTranscript {
				t.Errorf("the help panel advertises %q under Transcript focus but it moved neither the viewport (YOffset=%d) nor the keyboard", stroke, before)
			}
		})
	}
}
