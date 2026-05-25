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

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
)

// Init asks the terminal for its background color so the style bundle
// can resolve dark vs light at startup (R-MD-2), and starts the
// textarea cursor blink.
func (m Model) Init() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, textarea.Blink)
}

// Update is the Bubble Tea dispatcher. The visual-preview slice
// handles window-resize, background-color, and a small keymap; later
// slices add agent-event dispatch, modal forms, etc.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		m.refreshViewport()
		return m, nil

	case tea.BackgroundColorMsg:
		m.styles = NewStyles(msg.IsDark(), m.opts.Branding)
		m.refreshViewport()
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	// Forward unhandled messages to the input + viewport so navigation
	// keys work even when our switch above doesn't claim them.
	var (
		taCmd tea.Cmd
		vpCmd tea.Cmd
	)
	m.input, taCmd = m.input.Update(msg)
	m.viewport, vpCmd = m.viewport.Update(msg)
	return m, tea.Batch(taCmd, vpCmd)
}

// handleKey runs the keymap for the visual-preview slice. The slice
// owns these bindings (no real agent dispatch yet); follow-up slices
// will replace this with full slash routing and modal state machines.
//
// We use msg.String() (a normalized keystroke like "ctrl+b" /
// "shift+enter") for dispatch — Code+Mod bit-fiddling is brittle in
// the face of v2's keyboard-enhancement protocol.
func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	stroke := msg.String()

	// Modal-close: Esc closes any open overlay or help panel before
	// any other handler.
	if stroke == "esc" {
		if m.overlay != overlayNone {
			m.overlay = overlayNone
			return m, nil
		}
		if m.helpOpen {
			m.helpOpen = false
			m.resize()
			m.refreshViewport()
			return m, nil
		}
	}

	switch stroke {
	case "ctrl+c", "ctrl+d":
		m.quitting = true
		return m, tea.Quit

	case "ctrl+b":
		if m.statusLayout == StatusHeader {
			m.statusLayout = StatusSidebar
		} else {
			m.statusLayout = StatusHeader
		}
		m.resize()
		m.refreshViewport()
		return m, nil

	case "shift+tab":
		if m.permissionModeWired() {
			m.permMode = m.permMode.Next()
			_ = m.opts.PermissionMode.Set(m.permMode)
			if m.opts.PermissionMode.Persist != nil {
				_ = m.opts.PermissionMode.Persist(m.permMode)
			}
		}
		return m, nil

	case "ctrl+p":
		m.overlay = overlayPalette
		return m, nil
	case "ctrl+g":
		m.overlay = overlayModelPicker
		return m, nil
	case "ctrl+y":
		m.overlay = overlayPermission
		return m, nil
	case "ctrl+e":
		m.overlay = overlayElicit
		return m, nil

	case "enter":
		// Submit the textarea (R-CHAT-1: Enter submits). For the
		// visual-preview slice "submit" just appends the typed text
		// as a RoleUser message and clears the input so the operator
		// gets feedback. A real slice wires this into the agent.
		text := strings.TrimSpace(m.input.Value())
		if text != "" {
			m.history.Append(Message{Role: RoleUser, Text: text})
			m.input.Reset()
			m.refreshViewport()
		}
		return m, nil

	case "shift+enter", "ctrl+j":
		// Insert a newline (R-CHAT-1: Shift-Enter / Ctrl-J inserts
		// newline). Synthesize an Enter KeyPressMsg with no modifier
		// and forward to the textarea — that hits its InsertNewline
		// binding.
		fakeEnter := tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(fakeEnter)
		return m, cmd

	case "?":
		// Toggle the bottom-anchored stacked help panel. Only fires
		// when input is empty so users can still type `?` mid-sentence
		// without hijacking the key.
		if strings.TrimSpace(m.input.Value()) == "" {
			m.helpOpen = !m.helpOpen
			m.resize()
			m.refreshViewport()
			return m, nil
		}
	}

	// Forward unmatched keys to the input field for typing. Viewport
	// gets the message too so PgUp/PgDn/Home/End scroll the chat
	// even while the input is focused.
	var (
		taCmd tea.Cmd
		vpCmd tea.Cmd
	)
	m.input, taCmd = m.input.Update(msg)
	m.viewport, vpCmd = m.viewport.Update(msg)
	return m, tea.Batch(taCmd, vpCmd)
}
