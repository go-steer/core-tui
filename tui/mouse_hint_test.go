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
)

// TestMouseSelectModifier_NamesOnlyWhatItRecognises. The whole reason
// the hint is not the specced literal "Hold Shift to select text" is
// that Shift is wrong in VS Code's integrated terminal, which is
// xterm.js and binds Alt/Option. An unrecognised terminal gets no
// modifier at all rather than a guess.
func TestMouseSelectModifier_NamesOnlyWhatItRecognises(t *testing.T) {
	cases := []struct {
		prog, goos, want string
	}{
		// No signal — say nothing.
		{"", "linux", ""},
		{"", "darwin", ""},
		// xterm-family convention, which is everything else we detect.
		{"iterm.app", "darwin", "Shift"},
		{"apple_terminal", "darwin", "Shift"},
		{"kitty", "linux", "Shift"},
		{"alacritty", "linux", "Shift"},
		{"wezterm", "linux", "Shift"},
		{"ghostty", "darwin", "Shift"},
		{"tmux", "linux", "Shift"},
		// The exception, and it is platform-dependent on top.
		{"vscode", "darwin", "Option"},
		{"vscode", "linux", "Alt"},
		{"vscode", "windows", "Alt"},
	}
	for _, tc := range cases {
		if got := mouseSelectModifier(tc.prog, tc.goos); got != tc.want {
			t.Errorf("mouseSelectModifier(%q, %q) = %q, want %q", tc.prog, tc.goos, got, tc.want)
		}
	}
}

// TestMouseHintText_AlwaysNamesTheEscapeHatchWeControl. Whichever
// branch produces the text, /mouse has to be in it: the modifier is
// the terminal's to offer and may not work (VS Code gates Option-drag
// behind a user setting), while /mouse is ours and always does.
func TestMouseHintText_AlwaysNamesTheEscapeHatchWeControl(t *testing.T) {
	m := newModel(Options{Agent: &noopAgent{}})
	if got := m.mouseHintText(); !strings.Contains(got, "/mouse") {
		t.Errorf("derived hint = %q, want it to name /mouse", got)
	}
}

// TestMouseHintText_HostOverrideWins is Options.MouseHint doing what
// R-MOUSE-3 specs: a host that knows the operator's terminal better
// than the TERM_PROGRAM probe replaces the string outright.
func TestMouseHintText_HostOverrideWins(t *testing.T) {
	m := newModel(Options{Agent: &noopAgent{}, MouseHint: "hold Cmd, this is a weird terminal"})
	if got := m.mouseHintText(); got != "hold Cmd, this is a weird terminal" {
		t.Errorf("hint = %q, want the host's override verbatim", got)
	}
}

// TestMouseHint_ArmedAtStartupOnlyWhenCaptureIsOn. The hint explains
// why click-drag stopped selecting text. On a host that launched with
// Mouse=false that never happened, so there is nothing to explain.
func TestMouseHint_ArmedAtStartupOnlyWhenCaptureIsOn(t *testing.T) {
	off := false
	on := true
	cases := []struct {
		name string
		opts Options
		want bool
	}{
		{"default (capture on)", Options{Agent: &noopAgent{}}, true},
		{"explicitly on", Options{Agent: &noopAgent{}, Mouse: &on}, true},
		{"capture off", Options{Agent: &noopAgent{}, Mouse: &off}, false},
		{"negative TTL opts out", Options{Agent: &noopAgent{}, MouseHintTTL: -1}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(tc.opts)
			cmd := m.armMouseHintCmd()
			if armed := cmd != nil; armed != tc.want {
				t.Fatalf("armed the hint = %v, want %v", armed, tc.want)
			}
			if cmd == nil {
				return
			}
			if _, ok := cmd().(mouseHintArmMsg); !ok {
				t.Error("arm Cmd produced something other than a mouseHintArmMsg")
			}
		})
	}
}

// TestInit_ArmsTheMouseHint pins the wiring the test above stops
// short of: armMouseHintCmd is only useful if Init actually calls it.
// Init's batch is mostly blocking channel listeners, so this reads the
// call site rather than running the batch.
func TestInit_ArmsTheMouseHint(t *testing.T) {
	m := newModel(Options{Agent: &noopAgent{}})
	if m.Init() == nil {
		t.Fatal("Init returned no commands at all")
	}
	if m.armMouseHintCmd() == nil {
		t.Error("the default model does not arm the hint, so Init cannot either")
	}
}

// TestMouseHint_RendersThenExpires walks the lifecycle: armed by the
// msg, visible, gone once the TTL is past. The renderer decides
// visibility from the timestamp, so expiry needs no state change —
// which is exactly what makes it worth pinning.
func TestMouseHint_RendersThenExpires(t *testing.T) {
	m := newModel(Options{Agent: &noopAgent{}})
	m.width, m.height = 100, 40
	m.resize()

	if got := m.renderMouseHint(80); got != "" {
		t.Errorf("hint rendered before it was armed: %q", got)
	}

	out, cmd := m.Update(mouseHintArmMsg{})
	m = out.(model)
	if got := m.renderMouseHint(80); got == "" {
		t.Fatal("hint did not render after being armed")
	}
	if cmd == nil {
		t.Fatal("arming returned no expiry tick — the row would linger until the next keystroke")
	}

	// Walk the clock past the TTL rather than sleeping for it.
	m.mouseHintSetAt = time.Now().Add(-defaultMouseHintTTL - time.Second)
	if got := m.renderMouseHint(80); got != "" {
		t.Errorf("hint outlived its TTL: %q", got)
	}
}

// TestMouseHint_HiddenWhileCaptureOff. /mouse off during the TTL
// window solves the problem the hint describes, so the row goes with
// the same keystroke instead of advertising a constraint that has just
// been lifted.
func TestMouseHint_HiddenWhileCaptureOff(t *testing.T) {
	m := newModel(Options{Agent: &noopAgent{}})
	m.width, m.height = 100, 40
	m.resize()
	out, _ := m.Update(mouseHintArmMsg{})
	m = out.(model)
	if m.renderMouseHint(80) == "" {
		t.Fatal("precondition: hint should be up")
	}

	off := false
	m.opts.Mouse = &off
	if got := m.renderMouseHint(80); got != "" {
		t.Errorf("hint still up with capture off: %q", got)
	}
}

// TestMouseHint_NegativeTTLDisablesIt is the documented opt-out.
// Blanking MouseHint only falls back to the derived text, so there has
// to be a way to say "no hint at all".
func TestMouseHint_NegativeTTLDisablesIt(t *testing.T) {
	m := newModel(Options{Agent: &noopAgent{}, MouseHintTTL: -1})
	m.width, m.height = 100, 40
	m.resize()
	out, cmd := m.Update(mouseHintArmMsg{})
	m = out.(model)
	if cmd != nil {
		t.Error("a disabled hint should not schedule an expiry tick")
	}
	if got := m.renderMouseHint(80); got != "" {
		t.Errorf("hint rendered despite a negative TTL: %q", got)
	}
}

// TestMouseHint_YieldsToTheWakeToast. The two share one slot. A wake
// is the operator's own agent asking for attention; the hint is a
// first-run nicety, so the hint is the one that gives way.
func TestMouseHint_YieldsToTheWakeToast(t *testing.T) {
	m := newModel(Options{Agent: &noopAgent{}})
	m.width, m.height = 100, 40
	m.resize()
	out, _ := m.Update(mouseHintArmMsg{})
	m = out.(model)
	if m.renderMouseHint(80) == "" {
		t.Fatal("precondition: hint should be up")
	}

	m.toast, m.toastSetAt = "agent needs you", time.Now()
	if got := m.renderMouseHint(80); got != "" {
		t.Errorf("hint and toast both claimed the slot; hint = %q", got)
	}
}

// TestMouseHint_IsChargedToTheLayoutBudget. The hint is a chrome row.
// Unbudgeted it pushes the footer off the bottom through clipFrame for
// the first few seconds of every session — the same trap the pause
// banner fell into.
func TestMouseHint_IsChargedToTheLayoutBudget(t *testing.T) {
	m := newModel(Options{Agent: &noopAgent{}})
	m.width, m.height = 100, 40
	m.resize()
	before := m.allocateChrome(m.opts.StatusLayout, m.width)

	out, _ := m.Update(mouseHintArmMsg{})
	m = out.(model)
	after := m.allocateChrome(m.opts.StatusLayout, m.width)

	if after.mouseHint == 0 {
		t.Fatal("hint row was not charged to the budget")
	}
	if before.frameRows() != after.frameRows() {
		t.Errorf("frame changed height with the hint up: %d → %d", before.frameRows(), after.frameRows())
	}
	if after.chat >= before.chat {
		t.Errorf("hint row came from nowhere: chat %d → %d, want the row taken from the chat", before.chat, after.chat)
	}
}

// TestMouseSlash_PersistsChoiceOffLoop. The callback writes the host's
// config file, so it is disk I/O reached from a keystroke and belongs
// on a Cmd — same treatment as PersistThemeChoice and
// PersistModelChoice (issue #137).
func TestMouseSlash_PersistsChoiceOffLoop(t *testing.T) {
	var persisted []bool
	m := newModel(Options{
		Agent: &noopAgent{},
		PersistMouseChoice: func(on bool) error {
			time.Sleep(slowHostDelay)
			persisted = append(persisted, on)
			return nil
		},
	})
	m.width, m.height = 100, 40
	m.resize()
	m.input.SetValue("/mouse")

	var cmd tea.Cmd
	mustBeFast(t, "/mouse", func() {
		next, c := pressKey(m, tea.Key{Code: tea.KeyEnter})
		m, cmd = next, c
	})
	if len(persisted) != 0 {
		t.Fatalf("PersistMouseChoice ran on the Update goroutine: %v", persisted)
	}
	var sawPersist bool
	for _, msg := range runCmds(t, cmd) {
		if done, ok := msg.(persistDoneMsg); ok && done.what == "/mouse" {
			sawPersist = true
		}
	}
	if !sawPersist {
		t.Fatal("no persistDoneMsg for /mouse")
	}
	// Default is on, so the first toggle turns capture off — and that
	// is the state the operator wants to survive the restart.
	if len(persisted) != 1 || persisted[0] {
		t.Errorf("PersistMouseChoice got %v, want [false]", persisted)
	}
}

// TestMouseSlash_NilPersistIsFine — hosts that don't wire the callback
// keep today's session-local behaviour and must not trip a nil call.
func TestMouseSlash_NilPersistIsFine(t *testing.T) {
	m := newModel(Options{Agent: &noopAgent{}})
	m.width, m.height = 100, 40
	m.resize()
	m.input.SetValue("/mouse")

	next, _ := pressKey(m, tea.Key{Code: tea.KeyEnter})
	if got := lastText(next); !strings.Contains(got, "/mouse: capture off") {
		t.Errorf("toggle row = %q", got)
	}
	if next.mouseCaptureOn() {
		t.Error("capture still on after /mouse")
	}
}

// TestMouseSlash_ReArmsTheHintOnlyWhenTurningCaptureOn. R-MOUSE-3 says
// "after each /mouse on". Turning capture off needs no re-arm — the
// renderer already drops the row while capture is off.
func TestMouseSlash_ReArmsTheHintOnlyWhenTurningCaptureOn(t *testing.T) {
	armCount := func(t *testing.T, m model) int {
		t.Helper()
		m.input.SetValue("/mouse")
		_, cmd := pressKey(m, tea.Key{Code: tea.KeyEnter})
		// No persistence wired and nothing to arm collapses the batch
		// to nil, which is the "zero arms" answer rather than a fault.
		if cmd == nil {
			return 0
		}
		var n int
		for _, msg := range runCmds(t, cmd) {
			if _, ok := msg.(mouseHintArmMsg); ok {
				n++
			}
		}
		return n
	}

	on := newModel(Options{Agent: &noopAgent{}})
	on.width, on.height = 100, 40
	on.resize()
	if got := armCount(t, on); got != 0 {
		t.Errorf("toggling capture OFF armed the hint %d time(s), want 0", got)
	}

	off := false
	back := newModel(Options{Agent: &noopAgent{}, Mouse: &off})
	back.width, back.height = 100, 40
	back.resize()
	if got := armCount(t, back); got != 1 {
		t.Errorf("toggling capture ON armed the hint %d time(s), want 1", got)
	}
}
