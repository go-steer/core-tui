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

// Session picker dialog — Dialog implementation for issue #53's
// /switch built-in. Mirrors dialog_modelpicker.go's shape: cursor
// list of enumerated sessions via SessionSwitcher, Enter commits
// through applySwitchTarget, Esc closes without swap.

package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

const sessionPickerDialogID = "session-picker"

// sessionPickerDialog renders SessionSwitcher.Sessions() with a
// cursor + "(current)" marker on the attached row, dispatches
// SwitchToSession + applySwitchTarget on Enter.
type sessionPickerDialog struct {
	idx int
	// off is the first visible row. Session lists are host-supplied
	// and unbounded — a long-running endpoint can advertise dozens —
	// so the list windows around the cursor.
	off int
}

func newSessionPickerDialog() *sessionPickerDialog {
	return &sessionPickerDialog{idx: 0}
}

func (d *sessionPickerDialog) ID() string { return sessionPickerDialogID }

func (d *sessionPickerDialog) HandleKey(stroke string, m *Model) DialogAction {
	switcher, ok := m.opts.Agent.(SessionSwitcher)
	if !ok {
		// Agent doesn't support session switching — close cleanly.
		return DialogAction{Consumed: true, Close: true}
	}
	sessions := switcher.Sessions()
	if len(sessions) == 0 {
		m.history.Append(Message{Role: RoleSystem, Text: "/switch: no sessions available"})
		m.refreshViewport()
		return DialogAction{Consumed: true, Close: true}
	}
	switch stroke {
	case "esc":
		return DialogAction{Consumed: true, Close: true}
	case "up", "ctrl+p":
		d.idx = (d.idx - 1 + len(sessions)) % len(sessions)
		return DialogAction{Consumed: true}
	case "down", "ctrl+n":
		d.idx = (d.idx + 1) % len(sessions)
		return DialogAction{Consumed: true}
	case "enter":
		if d.idx >= len(sessions) {
			d.idx = len(sessions) - 1
		}
		if d.idx < 0 {
			d.idx = 0
		}
		pick := sessions[d.idx]
		// Action row (issue #56) — hand off to a text-input dialog
		// stacked on top of this one instead of switching. We stay
		// open underneath so esc on the input returns the operator
		// to the list rather than dumping them back to chat.
		if pick.Input != nil {
			m.overlayStack.Open(newSessionInputDialog(pick))
			return DialogAction{Consumed: true}
		}
		// Picking the currently-attached row is a no-op — nothing
		// to detach from. Close cleanly without wiping history.
		if pick.Current {
			return DialogAction{Consumed: true, Close: true}
		}
		tgt, err := switcher.SwitchToSession(pick.ID)
		if err != nil {
			m.history.Append(Message{Role: RoleError, Text: "/switch: " + err.Error()})
			m.refreshViewport()
			return DialogAction{Consumed: true, Close: true}
		}
		if tgt.Agent == nil {
			m.history.Append(Message{Role: RoleError, Text: "/switch: SessionSwitcher returned nil Agent"})
			m.refreshViewport()
			return DialogAction{Consumed: true, Close: true}
		}
		cmd := m.applySwitchTarget(&tgt)
		return DialogAction{Consumed: true, Close: true, Cmd: cmd}
	}
	// Unhandled key — consume so it doesn't leak to the textarea
	// behind the modal, but don't close.
	return DialogAction{Consumed: true}
}

func (d *sessionPickerDialog) Render(totalWidth int, m *Model) string {
	width := 72
	if totalWidth > 0 && width > totalWidth-4 {
		width = totalWidth - 4
	}
	if width < 30 {
		width = 30
	}

	switcher, ok := m.opts.Agent.(SessionSwitcher)
	body := ""
	if !ok {
		body = m.styles.Muted.Render("agent does not implement SessionSwitcher")
	} else {
		sessions := switcher.Sessions()
		if len(sessions) == 0 {
			body = m.styles.Muted.Render("(no sessions advertised by the agent)")
		} else {
			// Clamp cursor into range in case Sessions() shrank
			// between opens (the picker is short-lived so this
			// is defensive, not a hot path).
			if d.idx >= len(sessions) {
				d.idx = len(sessions) - 1
			}
			if d.idx < 0 {
				d.idx = 0
			}
			rows := make([]string, 0, len(sessions))
			for i, s := range sessions {
				disp := s.Display
				if disp == "" {
					disp = s.ID
				}
				marker := "  "
				if i == d.idx {
					marker = "> "
				}
				row := marker + disp
				// Action rows (issue #56) carry no session identity
				// — showing "(id)" or "(current)" next to
				// "+ Attach to endpoint…" would read as a session
				// that exists. Their affordance is the ▸ chevron.
				if s.Input != nil {
					row += "  " + m.styles.Muted.Render(GlyphCollapsed)
				} else {
					if s.ID != disp {
						row += m.styles.Muted.Render("  (" + s.ID + ")")
					}
					if s.Current {
						row += "  " + m.styles.Muted.Render("(current)")
					}
				}
				if s.Description != "" {
					row += "  " + m.styles.Muted.Render(s.Description)
				}
				if i == d.idx {
					row = m.styles.Accent.Render(row)
				}
				rows = append(rows, row)
			}
			view := modalBodyHeight(m.height, modalChromeRows)
			d.off = listWindow(d.off, d.idx, len(rows), view)
			body = strings.Join(scrollView(m.styles, rows, nonNeg(width-4), view, d.off), "\n")
		}
	}
	footer := "↑↓ choose " + GlyphSeparator + " enter attach " + GlyphSeparator + " esc cancel"
	return RenderContext{
		Title:  "Choose a Session",
		Body:   body,
		Footer: footer,
		Width:  width,
		Styles: m.styles,
	}.Render()
}

// sessionInputDialogID is the text-input dialog the picker stacks
// on top of itself for SessionInfo action rows (issue #56).
const sessionInputDialogID = "session-input"

// newSessionInputDialog builds the text-input dialog for a
// SessionInfo action row. The submit closure runs the row's own
// Submit, then closes BOTH this dialog and the picker underneath so
// a successful attach lands the operator straight in the new
// session. Failures also close both and leave a RoleError row —
// same shape as a failed SwitchToSession from the list.
func newSessionInputDialog(row SessionInfo) Dialog {
	in := row.Input
	title := in.Title
	if title == "" {
		title = row.Display
	}
	if title == "" {
		title = "Enter a Value"
	}
	return NewTextInputDialog(TextInputConfig{
		ID:          sessionInputDialogID,
		Title:       title,
		Prompt:      in.Prompt,
		Placeholder: in.Placeholder,
		Initial:     in.Initial,
		Validate:    in.Validate,
		Footer:      "enter attach " + GlyphSeparator + " esc back",
		Submit: func(value string, m *Model) DialogAction {
			// Close the picker underneath first; the Overlay pops
			// THIS dialog itself when we return Close: true.
			closeBoth := func() { m.overlayStack.Close(sessionPickerDialogID) }
			fail := func(text string) DialogAction {
				closeBoth()
				m.history.Append(Message{Role: RoleError, Text: "/switch: " + text})
				m.refreshViewport()
				return DialogAction{Consumed: true, Close: true}
			}
			if in.Submit == nil {
				return fail("session row " + row.ID + " has no Submit closure")
			}
			tgt, err := in.Submit(value)
			if err != nil {
				return fail(err.Error())
			}
			if tgt.Agent == nil {
				return fail("SessionInput.Submit returned nil Agent")
			}
			closeBoth()
			return DialogAction{Consumed: true, Close: true, Cmd: m.applySwitchTarget(&tgt)}
		},
	})
}

// Keep lipgloss import happy — RenderContext pulls it in via
// Styles but this file's direct use is limited.
var _ = lipgloss.Left
