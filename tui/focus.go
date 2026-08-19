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

// Keyboard focus (issue #151) and the transcript keymap it unlocks
// (issue #155).
//
// Until this landed, every keystroke the modal cascade didn't claim
// was forwarded to BOTH the textarea and the chat viewport, so the
// transcript could only be driven by keys nobody would ever type
// into a prompt. That is why the viewport's KeyMap is stripped to
// four non-letter chords in NewModel, why the arrows belong to
// prompt-history recall, and why four separate affordances — line
// scrolling (#155), select-and-expand (#152), copy (#153) and
// horizontal scroll (#154) — were each blocked on inventing yet
// another chord.
//
// One region owns the keyboard at a time. When that region is the
// transcript the composer is blurred and unfed, which frees the
// entire letter and arrow space in one move: this file's keymap is
// the whole of #155, and #152-#154 extend the same switch rather
// than negotiating for chords.
//
// Deliberately NOT a general focus-ring abstraction. There are two
// regions today and a sidebar is the only plausible third, so the
// enum is the whole mechanism; cycleFocus is where a third entry
// would go.

package tui

import tea "charm.land/bubbletea/v2"

// focusTarget names the region that currently owns the keyboard.
//
// focusInput is the zero value on purpose: a bare model{} — which
// the tests build constantly, and which NewModel starts from —
// composes into the prompt exactly as it did before this file
// existed. Focus mode is something the operator opts into.
type focusTarget int

const (
	// focusInput: keystrokes reach the composer, the arrows recall
	// prompt history, and the transcript is scrollable only by the
	// non-letter chords the viewport keeps (pgup / pgdn).
	focusInput focusTarget = iota
	// focusTranscript: the transcript takes the keymap in
	// handleTranscriptKey and the composer is blurred.
	focusTranscript
)

// setFocus moves the keyboard to a region, blurring or re-arming the
// textarea to match.
//
// Blurring is not decoration. bubbles' textarea drops every message
// it gets while blurred (textarea.go:1204), and textareaCursor
// returns nil for a blurred widget (cursor.go), so this one call
// both stops stray text from landing in the prompt and takes the
// hardware cursor off a composer that is no longer listening. The
// blink Cmd from Focus() is dropped for the reason NewModel drops
// it: the virtual cursor is off, so the terminal blinks its own.
//
// Idempotent rather than transition-guarded: it reconciles the
// widget every call, so a caller that only wants to re-arm a
// textarea it never blurred (the turn-end path) gets the same answer
// as one changing modes, and the two states can't drift apart.
func (m *model) setFocus(t focusTarget) {
	m.focus = t
	// The copy notice belongs to the mode it was made in: it is shown
	// in the focus legend, which the composer does not draw, so
	// leaving it set would resurface a stale "copied 24 lines" on the
	// next tab back in (issue #153). The pan offset goes with it for
	// the same reason and one more: it is only reachable from this
	// mode, so a composer that inherited one would be showing a
	// sideways transcript with no key bound to bring it back
	// (issue #154).
	m.clearCopyNotice()
	m.chatResetPan()
	if t == focusInput {
		_ = m.input.Focus()
		return
	}
	m.input.Blur()
	// Taking the keyboard is also what puts the cursor on an item
	// (issue #152). The transcript's keymap is a keymap over a
	// selection, so entering the mode has to leave one on screen —
	// seeding here rather than on the first arrow means the marker is
	// visible from the first frame, which is how the operator learns
	// the mode is a selection at all.
	m.chatSeedSelection()
}

// cycleFocus advances to the next region. With exactly two of them
// forward and backward are the same move, which is why tab alone
// carries this and shift+tab keeps the permission-mode chip it has
// had since R-PERM-1. Add a third region and that stops being true —
// split the binding then, not before.
func (m *model) cycleFocus() {
	if m.focus == focusInput {
		m.setFocus(focusTranscript)
		return
	}
	m.setFocus(focusInput)
}

// handleTranscriptKey runs the transcript's own keymap. Returns the
// Cmd the stroke produced (nil for all but the copy keys, which write
// the clipboard through the terminal) and whether it claimed the
// stroke at all; anything it declines falls through
// to handleKey's global switch, which is where the frame-level
// chords live (ctrl+c, ctrl+b, ctrl+g, ctrl+x, shift+tab, `?`).
// Those belong to the frame rather than to the composer, and having
// to leave focus mode to reach them would defeat the point of it.
//
// The arrows move the CURSOR, an item at a time, and shift+arrow is
// what scrolls a line (issue #152). #155 shipped the arrows as a line
// scroll because there was nothing else for them to move; now that
// there is a selection they have to drive it, because a marker the
// operator cannot move is decoration and a mode with a marker in it
// that scrolls instead has two cursors and no way to tell which one a
// key will move. Line scrolling keeps a binding rather than being
// dropped: an item taller than the window can only be read by
// scrolling within it, and pgup / pgdn alone are too coarse for that.
//
// Notably absent:
//
//   - pgup / pgdn, which are NOT claimed here. They already page the
//     chat from either focus because handleKey forwards every
//     unclaimed key to the viewport, and a second implementation of
//     paging would be one more thing to keep in step with the first.
//   - ctrl+d / ctrl+u, the viewport's half-page pair. ctrl+d quits
//     unconditionally and is far too load-bearing to shadow in a
//     mode the operator can enter by accident; a half-page keymap
//     with only one working half is worse than none.
func (m *model) handleTranscriptKey(stroke string) (tea.Cmd, bool) {
	// Whatever the last copy said, this key is the operator moving on
	// (issue #153). Cleared before the switch so a copy arm can set a
	// fresh one, and so the two copy keys in a row each report their
	// own result rather than the first one's.
	m.clearCopyNotice()

	var cmd tea.Cmd
	switch stroke {
	case "tab", "enter":
		// Two ways back here and a third upstream, because being
		// stuck in a mode that silently swallows typing is the
		// failure this feature can most easily cause. tab is the
		// toggle, enter is what fingers reach for when a composer is
		// on screen, and esc never arrives here at all — it is
		// claimed by handleKey's esc cascade, which has to order
		// "back to the composer" against the other things esc backs
		// out of.
		//
		// enter deliberately does NOT also submit whatever is in
		// the box: the operator can't see the caret from here, and
		// firing a half-written prompt is not an undoable mistake.
		m.setFocus(focusInput)
	case "up", "k":
		m.chatSelectBy(-1)
	case "down", "j":
		m.chatSelectBy(1)
	case "shift+up":
		m.chatScrollBy(-1)
	case "shift+down":
		m.chatScrollBy(1)
	case "shift+left":
		// The other axis of the same modifier: shift is "move the
		// window", unshifted is "move the cursor" (issue #154).
		m.chatPanBy(-chatPanStep)
	case "shift+right":
		m.chatPanBy(chatPanStep)
	case "space":
		m.chatToggleCollapsed()
	case "y":
		// y for yank, next to k / j / g / G. c is the narrower half
		// of the same key: the code out of the item, without the
		// prose around it (issue #153).
		cmd = m.chatCopyItem()
	case "c":
		cmd = m.chatCopyCode()
	case "home", "g":
		m.chatSelectFirst()
	case "end", "G":
		m.chatSelectLast()
	default:
		return nil, false
	}
	// Every arm above either moved the viewport or left it alone, so
	// one syncFollow covers all of them: it reads the follow intent
	// back off AtBottom rather than each arm having to assert it.
	// Scrolling up out of the tail stops following, G lands back on
	// it and resumes — the same contract ctrl+l and end already have
	// from the input side.
	m.syncFollow()
	return cmd, true
}
