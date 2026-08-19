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
	"github.com/charmbracelet/x/ansi"
)

// panRulerWidth is comfortably wider than the window panModel builds,
// so there is always something past the right edge to pan to.
const panRulerWidth = 260

// panRuler is a column ruler: cell k is the digit k%10, so the first
// character of a drawn line says which column the window starts at.
// That makes an off-by-one in the cut visible as a wrong digit rather
// than as a subtly wrong string.
func panRuler() string {
	var b strings.Builder
	for i := range panRulerWidth {
		b.WriteByte(byte('0' + i%10))
	}
	return b.String()
}

// panModel is focusedModel's wide sibling: a transcript of rulers,
// parked mid-scroll with the keyboard on the transcript. Every row is
// one line that overruns the window, which is the only shape panning
// applies to.
func panModel(t *testing.T) model {
	t.Helper()
	m := newModel(Options{Agent: &bareAgent{id: "pan"}})
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = out.(model)
	ruler := panRuler()
	for range 60 {
		m.history.Append(Message{Role: RoleAssistant, Text: ruler, Rendered: ruler})
	}
	m.refreshViewport()
	m.chatSetYOffset(m.chatYOffset() / 2)
	m.syncFollow()
	m.setFocus(focusTranscript)
	if m.chatMaxPan() <= chatPanStep {
		t.Fatalf("setup: nothing to pan over, maxPan=%d with a %d-cell window", m.chatMaxPan(), m.viewport.Width())
	}
	return m
}

// panContent is what the frame shows of the rows underneath it: each
// drawn line with its ANSI and its selection gutter removed. Blank
// lines (the separators between rows) are dropped, so the result is
// the ruler as the operator sees it.
func panContent(t *testing.T, m model) []string {
	t.Helper()
	var out []string
	for _, ln := range strings.Split(m.chatView(), "\n") {
		runes := []rune(ansi.Strip(ln))
		if len(runes) < chatGutterWidth {
			continue
		}
		body := strings.TrimRight(string(runes[chatGutterWidth:]), " ")
		if body == "" {
			continue
		}
		out = append(out, body)
	}
	if len(out) == 0 {
		t.Fatal("the frame drew no content lines at all")
	}
	return out
}

// The whole feature in one assertion: the columns the right edge used
// to eat are reachable, and they arrive at the LEFT edge — the window
// moves over the line, the line does not shrink into the window.
func TestPan_ShiftRightRevealsWhatTheEdgeCut(t *testing.T) {
	m := panModel(t)
	w := m.viewport.Width()
	ruler := panRuler()

	if got, want := panContent(t, m)[0], ruler[:w]; got != want {
		t.Fatalf("unpanned window shows %q, want the first %d columns %q", got, w, want)
	}

	m = press(m, "shift+right")
	if m.chatX != chatPanStep {
		t.Fatalf("one shift+right left the offset at %d, want %d", m.chatX, chatPanStep)
	}
	if got, want := panContent(t, m)[0], ruler[chatPanStep:chatPanStep+w]; got != want {
		t.Errorf("panned window shows %q, want columns %d..%d %q", got, chatPanStep, chatPanStep+w, want)
	}
	m = press(m, "shift+left")
	if got, want := panContent(t, m)[0], ruler[:w]; got != want {
		t.Errorf("panning back shows %q, want the left edge again %q", got, want)
	}
}

// Both ends clamp, and the right-hand one has to land ON the last
// column rather than past it: a pan that runs off into blank space is
// how the operator loses the content they were reading.
func TestPan_ClampsAtBothEnds(t *testing.T) {
	m := panModel(t)
	w := m.viewport.Width()

	m = press(m, "shift+left")
	if m.chatX != 0 {
		t.Errorf("shift+left at the left edge moved to %d, want to stay at 0", m.chatX)
	}

	maxPan := m.chatMaxPan()
	for range panRulerWidth/chatPanStep + 2 {
		m = press(m, "shift+right")
	}
	if m.chatX != maxPan {
		t.Errorf("panning to the end stopped at %d, want the clamp at %d", m.chatX, maxPan)
	}
	last := panRuler()[panRulerWidth-1:]
	if got := panContent(t, m)[0]; !strings.HasSuffix(got, last) {
		t.Errorf("fully panned window ends %q, want it to end on the ruler's last column %q", got, last)
	}
	if got, want := len(panContent(t, m)[0]), w; got != want {
		t.Errorf("fully panned line shows %d columns, want a full window of %d — the clamp overshot into padding", got, want)
	}
}

// Panning is a way of reading one wide item. Moving the cursor means
// the operator is reading a different one, and a paragraph rendered
// from column 40 reads as a broken frame; line scrolling is the
// opposite case — a tall table read a row at a time — so it keeps the
// offset.
func TestPan_WhatResetsItAndWhatDoesNot(t *testing.T) {
	resets := map[string]func(m model) model{
		"cursor down":   func(m model) model { return press(m, "down") },
		"cursor up":     func(m model) model { return press(m, "up") },
		"jump to top":   func(m model) model { return press(m, "g") },
		"jump to end":   func(m model) model { return press(m, "G") },
		"back to input": func(m model) model { return press(m, "tab") },
		"resize": func(m model) model {
			out, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
			return out.(model)
		},
	}
	for name, move := range resets {
		t.Run(name, func(t *testing.T) {
			m := press(panModel(t), "shift+right")
			if m.chatX == 0 {
				t.Fatal("setup: the pan did not take")
			}
			if m = move(m); m.chatX != 0 {
				t.Errorf("%s left the transcript panned to column %d", name, m.chatX)
			}
		})
	}

	keeps := map[string]string{
		"line scroll down": "shift+down",
		"line scroll up":   "shift+up",
	}
	for name, stroke := range keeps {
		t.Run(name, func(t *testing.T) {
			m := press(panModel(t), "shift+right")
			if m = press(m, stroke); m.chatX != chatPanStep {
				t.Errorf("%s dropped the pan to column %d; reading a tall wide item is what this is for", name, m.chatX)
			}
		})
	}
}

// A resize that does not change the transcript's width — a taller
// terminal, a WindowSizeMsg that repeats the current size — has not
// invalidated anything, and dropping the pan under a mouse-drag's
// stream of same-width events would make the feature unusable.
func TestPan_ASameWidthResizeKeepsIt(t *testing.T) {
	m := press(panModel(t), "shift+right")
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 44})
	if m = out.(model); m.chatX != chatPanStep {
		t.Errorf("a height-only resize dropped the pan to column %d", m.chatX)
	}
}

// The gutter is frame furniture, not transcript content: the marker
// says which item the cursor is on, so it has to stay at the left
// edge no matter how far the text under it has slid (issue #152).
func TestPan_TheSelectionMarkerDoesNotSlide(t *testing.T) {
	m := press(panModel(t), "shift+right")
	var marked int
	for _, ln := range strings.Split(m.chatView(), "\n") {
		if strings.HasPrefix(ansi.Strip(ln), glyphSelectBar) {
			marked++
		}
	}
	if marked == 0 {
		t.Error("the selection marker vanished while panned; it lives in the gutter, which does not pan")
	}
}

// The frame invariant, restated for the panned case: cutting a window
// out of the middle of a line must still produce exactly the columns
// the layout handed the transcript, or the panel beside it shifts.
func TestPan_EveryLineStillFillsTheWidth(t *testing.T) {
	m := panModel(t)
	width := m.viewport.Width() + chatGutterWidth
	for _, x := range []int{0, chatPanStep, 3 * chatPanStep, m.chatMaxPan()} {
		m.chatX = x
		for i, ln := range strings.Split(m.chatView(), "\n") {
			if got := ansi.StringWidth(ln); got != width {
				t.Fatalf("panned to column %d, frame line %d is %d cells wide, want exactly %d", x, i, got, width)
			}
		}
	}
}

// Nothing wider than the window means nowhere to go. Clamping to zero
// is the answer rather than panning into blank space, because a
// transcript that slides off the left edge for no reason looks broken.
func TestPan_NothingWiderThanTheWindowDoesNotMove(t *testing.T) {
	m := focusedModel(t, 40)
	if got := m.chatMaxPan(); got != 0 {
		t.Fatalf("setup: a window of short rows reports %d columns to pan over", got)
	}
	if m = press(m, "shift+right"); m.chatX != 0 {
		t.Errorf("shift+right over narrow content panned to column %d", m.chatX)
	}
}

// A pan is invisible from the frame alone — there is no left edge to
// compare against — so the legend has to say it, and say it in
// columns so the operator knows how far back the other arrow is.
func TestPan_TheLegendSaysHowFar(t *testing.T) {
	m := panModel(t)
	if got := m.footerHint(); strings.Contains(got, "panned") {
		t.Errorf("the legend claims a pan at column zero: %q", got)
	}
	m = press(m, "shift+right")
	hint := m.footerHint()
	for _, want := range []string{"panned 8 cols", "shift+←→ pan"} {
		if !strings.Contains(hint, want) {
			t.Errorf("legend %q is missing %q while panned", hint, want)
		}
	}
}

// chatCutLine is the single place a line can reach the frame from, so
// its edge cases are the frame's edge cases. The unpanned arm must
// leave a short line completely alone — byte-identical, not
// re-rendered — because that is what keeps the goldens stable and
// what keeps a cheap frame cheap.
func TestChatCutLine(t *testing.T) {
	cases := []struct {
		name  string
		ln    string
		x     int
		width int
		want  string
	}{
		{"a short line is untouched", "abc", 0, 10, "abc"},
		{"an exact fit is untouched", "abcde", 0, 5, "abcde"},
		{"an over-wide line is cut at the edge", "abcdefgh", 0, 5, "abcde"},
		{"panning takes a window out of the middle", "abcdefgh", 2, 3, "cde"},
		{"panning past the end shows nothing", "abcdefgh", 20, 5, ""},
		{"a zero width is left to the caller", "abc", 0, 0, "abc"},
		{"a negative width is left to the caller", "abc", 4, -1, "abc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := chatCutLine(tc.ln, tc.x, tc.width); got != tc.want {
				t.Errorf("chatCutLine(%q, %d, %d) = %q, want %q", tc.ln, tc.x, tc.width, got, tc.want)
			}
		})
	}
}

// The styling has to survive the cut: a diff's colour lives in escape
// sequences before the columns the window lands on, and ansi.Cut is
// chosen over a byte slice precisely because it carries them across.
func TestChatCutLine_KeepsTheStylingAcrossTheCut(t *testing.T) {
	ln := "\x1b[31m" + "abcdefgh" + "\x1b[0m"
	got := chatCutLine(ln, 2, 3)
	if ansi.Strip(got) != "cde" {
		t.Fatalf("cut %q, want the cells c..e", ansi.Strip(got))
	}
	if !strings.Contains(got, "\x1b[31m") {
		t.Errorf("cut dropped the colour it was opened with: %q", got)
	}
}
