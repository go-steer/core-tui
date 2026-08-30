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

// The mouse-capture hint (R-MOUSE-3, issue #288).
//
// Enabling cell-motion mouse reporting takes away a capability the
// operator had before the TUI launched: while ?1002 is on the terminal
// never sees click-drag, so native text selection is dead. The failure
// is silent and misattributed — the operator's first drag does nothing
// and they conclude the terminal is broken, not that an application
// turned mouse tracking on. /mouse is the fix and it is undiscoverable
// at exactly the moment it is needed, because nothing suggests the TUI
// is involved. That is how it was reported downstream
// (go-steer/core-agent#859).
//
// So: a one-row hint in the toast slot for the first few seconds of the
// session, and again after each /mouse on, then gone. Transient by
// design — permanent chrome would cost a viewport row forever to solve
// a first-thirty-seconds problem.

package tui

import (
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// defaultMouseHintTTL is how long the hint stays up when the host
// doesn't override Options.MouseHintTTL. Long enough to read on the
// way past, short enough not to be chrome.
const defaultMouseHintTTL = 5 * time.Second

// mouseHintArmMsg starts (or restarts) the hint's clock. Emitted by
// Init for the session-start showing and by the /mouse handler when
// capture is switched back on.
//
// It goes through a msg rather than being seeded in newModel so the
// clock starts when the program does, not when the model is built —
// and so a test that constructs a model without running Init sees no
// hint.
type mouseHintArmMsg struct{}

// mouseHintExpiredMsg lands one TTL after an arm. It carries no state:
// the renderer decides visibility from mouseHintSetAt, and this exists
// only to force the repaint that makes the hint disappear on time.
// Without it the row would linger until the next unrelated keystroke.
type mouseHintExpiredMsg struct{}

// mouseHintTick schedules the expiry repaint.
func mouseHintTick(ttl time.Duration) tea.Cmd {
	return tea.Tick(ttl, func(time.Time) tea.Msg { return mouseHintExpiredMsg{} })
}

// armMouseHintCmd is the "should the hint show, and if so start its
// clock" decision, in one place because both callers ask it: Init for
// the session-start showing and the /mouse handler for the re-show.
// nil means no hint — capture is off, so there is nothing to explain,
// or the host opted out with a negative MouseHintTTL.
//
// Separate from Init so it is reachable in a test: Init's batch is
// mostly blocking channel listeners, and running it to see what it
// armed would hang.
func (m model) armMouseHintCmd() tea.Cmd {
	if !m.mouseCaptureOn() || m.mouseHintTTL() <= 0 {
		return nil
	}
	return func() tea.Msg { return mouseHintArmMsg{} }
}

// mouseCaptureOn reports whether cell-motion capture is currently on.
// Mirrors View's read of Options.Mouse: nil means the default, which
// is enabled.
func (m model) mouseCaptureOn() bool {
	return m.opts.Mouse == nil || *m.opts.Mouse
}

// mouseHintTTL resolves the effective TTL. Zero (the zero value, i.e.
// the host said nothing) means the default; negative turns the hint
// off entirely, which is the supported way for a host to opt out
// without also having to blank Options.MouseHint.
func (m model) mouseHintTTL() time.Duration {
	if m.opts.MouseHintTTL == 0 {
		return defaultMouseHintTTL
	}
	return m.opts.MouseHintTTL
}

// mouseSelectModifier names the modifier that bypasses mouse reporting
// so the terminal's own selection takes over, for terminals we can
// recognise. Returns "" when we can't, which is a real answer and not
// a fallback to guessing: the caller words the hint differently rather
// than claiming a key that may do nothing.
//
// Shift is the xterm-family convention and holds for every terminal
// termProgram() identifies except one. VS Code's integrated terminal
// is xterm.js, which binds Alt (Option on macOS) instead — and even
// that is conditional on the user's
// terminal.integrated.macOptionClickForcesSelection, which is why the
// hint always names /mouse as well. The bypass is the terminal's to
// offer; /mouse is ours, so it is the half we can promise.
//
// prog is a termProgram() value; goos is a runtime.GOOS value. Both
// are parameters rather than reads so the mapping is testable without
// setenv on a parallel test.
func mouseSelectModifier(prog, goos string) string {
	switch prog {
	case "":
		// No signal. Say nothing about a key we haven't identified.
		return ""
	case "vscode":
		if goos == "darwin" {
			return "Option"
		}
		return "Alt"
	default:
		return "Shift"
	}
}

// mouseHintText is the row the operator reads. Host override wins
// verbatim; otherwise it is derived from the terminal.
func (m model) mouseHintText() string {
	if m.opts.MouseHint != "" {
		return m.opts.MouseHint
	}
	if mod := mouseSelectModifier(termProgram(), runtime.GOOS); mod != "" {
		return "hold " + mod + " to select text " + GlyphSeparator + " /mouse turns capture off"
	}
	// Unrecognised terminal: name only the escape hatch we control,
	// which is true everywhere.
	return "mouse capture on " + GlyphSeparator + " /mouse turns it off and restores text selection"
}

// renderMouseHint draws the hint into the same slot as the wake toast
// — between the input box and the footer. Empty string means no row,
// and every caller (View and allocateChrome both) keys off that, so
// the budget and the frame can't disagree about whether it is there.
func (m model) renderMouseHint(width int) string {
	if width <= 0 || m.mouseHintSetAt.IsZero() {
		return ""
	}
	// Capture off: the hint is about a constraint that no longer
	// applies. Covers /mouse off during the TTL, where leaving it up
	// would advertise a problem the operator just solved.
	if !m.mouseCaptureOn() {
		return ""
	}
	ttl := m.mouseHintTTL()
	if ttl <= 0 || time.Since(m.mouseHintSetAt) > ttl {
		return ""
	}
	// One slot, one row. The wake toast is the operator's own agent
	// asking for attention; this is a first-run nicety, so it yields.
	if m.renderToast(width) != "" {
		return ""
	}
	body := "  " + m.mouseHintText()
	if w := lipgloss.Width(body); w < width {
		body += strings.Repeat(" ", width-w)
	}
	return m.styles.Muted.Render(body)
}
