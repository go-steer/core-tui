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
func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.Key()
	// Modal-close: Esc closes any open overlay before any other handler.
	if m.overlay != overlayNone && key.Code == tea.KeyEscape {
		m.overlay = overlayNone
		return m, nil
	}

	// Quit on Ctrl+C / Ctrl+D regardless of focus.
	if key.Mod&tea.ModCtrl != 0 && (key.Code == 'c' || key.Code == 'd') {
		m.quitting = true
		return m, tea.Quit
	}

	// Ctrl+B toggles status layout.
	if key.Mod&tea.ModCtrl != 0 && key.Code == 'b' {
		if m.statusLayout == StatusHeader {
			m.statusLayout = StatusSidebar
		} else {
			m.statusLayout = StatusHeader
		}
		m.resize()
		m.refreshViewport()
		return m, nil
	}

	// Shift+Tab cycles the permission mode chip (R-PERM-6/7). Only
	// effective when the host wired the chip.
	if key.Code == tea.KeyTab && key.Mod&tea.ModShift != 0 && m.permissionModeWired() {
		m.permMode = m.permMode.Next()
		_ = m.opts.PermissionMode.Set(m.permMode)
		if m.opts.PermissionMode.Persist != nil {
			_ = m.opts.PermissionMode.Persist(m.permMode)
		}
		return m, nil
	}

	// Ctrl+P / Ctrl+G / Ctrl+Y / Ctrl+E open the four sample modals.
	// Hardcoded content — this is a visual preview, not a real form
	// dispatch yet.
	if key.Mod&tea.ModCtrl != 0 {
		switch key.Code {
		case 'p':
			m.overlay = overlayPalette
			return m, nil
		case 'g':
			m.overlay = overlayModelPicker
			return m, nil
		case 'y':
			m.overlay = overlayPermission
			return m, nil
		case 'e':
			m.overlay = overlayElicit
			return m, nil
		}
	}

	// Forward to the input field for everything else.
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}
