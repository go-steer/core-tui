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

// Dialog overlay stack (agentic-tui skill §9). Replaces the ad-hoc
// modal precedence cascade with a Dialog interface + Overlay
// container so adding a new modal is one type + one Open() call
// instead of two new fields + a new case in the Esc cascade + a
// new case in renderTUI's z-order switch.
//
// Permission and Elicit modals still use their inline state in
// Model (pendingPermission / pendingElicit) because they're tied
// to the channel-based Prompter / Elicitor lifecycle that needs
// special dispatch semantics. New modals (model picker today;
// settings / debug panels future) ride the Overlay.

package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Dialog is the contract for any modal that wants to ride the
// Overlay stack. Each method is keystroke-driven; the front-most
// dialog gets every key until it returns DialogActionClose.
type Dialog interface {
	// ID is a stable identifier (e.g. "model-picker", "settings")
	// so Overlay.Close(id) can target a specific dialog regardless
	// of z-order.
	ID() string

	// HandleKey is invoked for every keystroke the front-most
	// dialog receives. Returns the action the Overlay should
	// take (consume + render; close + pop; etc.).
	HandleKey(stroke string, m *Model) DialogAction

	// Render returns the styled string for the dialog body at
	// the given total terminal width. The Overlay wraps the
	// result in chrome via RenderContext.
	Render(width int, m *Model) string
}

// DialogAction is the return shape of HandleKey. Composite so
// dialogs can signal "consume key" + "close me" + "emit a Cmd"
// (e.g. ThemeChangedMsg from the theme picker) in one go.
type DialogAction struct {
	// Consumed reports whether the dialog handled the key. When
	// false, the Overlay lets the key fall through to the rest of
	// the handleKey switch (e.g. for Ctrl+C which always quits).
	Consumed bool
	// Close pops THIS dialog off the stack after the current
	// frame renders. Pair with Consumed=true to also stop the
	// key from falling through.
	Close bool
	// Cmd is an optional tea.Cmd to dispatch alongside the
	// state mutation — used by dialogs that need to notify the
	// host of a commit (e.g. ThemeChangedMsg). Nil for the
	// common case where the dialog just mutates Model.
	Cmd tea.Cmd
}

// Overlay is the modal z-order stack. Open() pushes onto the top;
// HandleKey routes only to the front; Render iterates in stack
// order so later opens render on top. Empty stack = no modal.
type Overlay struct {
	dialogs []Dialog
}

// Open pushes a new dialog onto the top of the stack. No
// dedup — opening "model-picker" twice stacks twice; callers
// that want singletons check HasID() first.
func (o *Overlay) Open(d Dialog) {
	o.dialogs = append(o.dialogs, d)
}

// Close removes the dialog with id from the stack (any
// position). No-op when not present.
func (o *Overlay) Close(id string) {
	out := o.dialogs[:0]
	for _, d := range o.dialogs {
		if d.ID() == id {
			continue
		}
		out = append(out, d)
	}
	o.dialogs = out
}

// CloseFront pops the front-most dialog. No-op on empty stack.
func (o *Overlay) CloseFront() {
	if len(o.dialogs) == 0 {
		return
	}
	o.dialogs = o.dialogs[:len(o.dialogs)-1]
}

// HasDialogs reports whether anything is open.
func (o *Overlay) HasDialogs() bool { return len(o.dialogs) > 0 }

// HasID reports whether a dialog with id is on the stack
// (useful for singleton checks before Open).
func (o *Overlay) HasID(id string) bool {
	for _, d := range o.dialogs {
		if d.ID() == id {
			return true
		}
	}
	return false
}

// Front returns the front-most dialog, or nil on empty stack.
func (o *Overlay) Front() Dialog {
	if len(o.dialogs) == 0 {
		return nil
	}
	return o.dialogs[len(o.dialogs)-1]
}

// HandleKey routes the keystroke to the front-most dialog and
// applies the returned action. Returns Consumed so the caller
// (handleKey) can decide whether to fall through, plus an
// optional Cmd for dialogs that need to emit a msg (e.g. the
// theme picker firing ThemeChangedMsg on commit).
func (o *Overlay) HandleKey(stroke string, m *Model) (consumed bool, cmd tea.Cmd) {
	front := o.Front()
	if front == nil {
		return false, nil
	}
	act := front.HandleKey(stroke, m)
	if act.Close {
		o.CloseFront()
	}
	return act.Consumed, act.Cmd
}

// Render iterates the stack and returns the front-most dialog's
// styled string wrapped in modal chrome. Empty stack returns "".
// Today we only render the FRONT (no layered painting); future
// translucent overlays would draw deeper dialogs first.
func (o *Overlay) Render(width int, m *Model) string {
	front := o.Front()
	if front == nil {
		return ""
	}
	return front.Render(width, m)
}

// RenderContext assembles a dialog body with consistent chrome:
// title bar, body, footer. Mirrors agentic-tui skill §9.C —
// every dialog inherits identical border / title styling without
// duplicating the lipgloss boilerplate.
type RenderContext struct {
	Title  string
	Body   string
	Footer string
	Width  int

	Styles Styles
}

// Render returns the framed dialog as a styled string. Title
// renders bold-accent with a horizontal rule continuing to the
// right edge; body sits in the middle with a single blank line
// above and below; footer renders muted at the bottom with its
// own rule.
func (rc RenderContext) Render() string {
	width := rc.Width
	if width < 30 {
		width = 30
	}
	titleBar := rc.Styles.ModalTitle.Render(rc.Title)
	titleRule := rc.Styles.ModalBorder.Render(strings.Repeat(GlyphRule, nonNeg(width-lipgloss.Width(titleBar)-3)))
	titleLine := titleBar + " " + titleRule

	footerRule := rc.Styles.ModalBorder.Render(strings.Repeat(GlyphRule, nonNeg(width-2)))
	footerLine := rc.Styles.ModalFooter.Render(rc.Footer)

	content := lipgloss.JoinVertical(lipgloss.Left,
		titleLine,
		"",
		rc.Body,
		"",
		footerRule,
		footerLine,
	)
	return rc.Styles.ModalBorder.Padding(0, 1).Width(width).Render(content)
}

// Scrollbar renders a vertical scrollbar character column of
// `height` rows showing thumb position relative to (contentSize,
// viewportSize, offset). Returns "" when content fits in viewport
// or when height <= 0. Lifted from agentic-tui skill §9.F so
// any dialog with overflowing content can frame a consistent
// scroll indicator without writing the math twice.
func Scrollbar(s Styles, height, contentSize, viewportSize, offset int) string {
	if height <= 0 || contentSize <= viewportSize {
		return ""
	}
	thumbSize := max(1, height*viewportSize/contentSize)
	maxOffset := contentSize - viewportSize
	trackSpace := height - thumbSize + 1
	thumbPos := 0
	if trackSpace > 0 && maxOffset > 0 {
		thumbPos = min(trackSpace, offset*trackSpace/maxOffset)
	}
	var sb strings.Builder
	thumb := s.Accent.Render("█")
	track := s.Muted.Render("│")
	for i := 0; i < height; i++ {
		if i > 0 {
			sb.WriteByte('\n')
		}
		if i >= thumbPos && i < thumbPos+thumbSize {
			sb.WriteString(thumb)
		} else {
			sb.WriteString(track)
		}
	}
	return sb.String()
}
