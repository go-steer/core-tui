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

	// Esc cascades through overlays (R-CHAT-6): modal → help panel →
	// palette → interrupt-in-flight (placeholder until streaming) →
	// no-op. Never quits.
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
		if m.palette != nil {
			m.palette = nil
			m.resize()
			m.refreshViewport()
			return m, nil
		}
		// Placeholder: when streaming lands, this branch will call
		// the turn's cancel func and emit an "(interrupted)" notice.
		return m, nil
	}

	// Palette dispatch — when a palette is open we consume the nav
	// keys ourselves; everything else falls through to the textarea
	// and the post-forward filter sync re-reads the input.
	if m.palette != nil {
		switch stroke {
		case "up", "ctrl+p":
			m.palette.moveCursor(-1)
			return m, nil
		case "down", "ctrl+n":
			m.palette.moveCursor(1)
			return m, nil
		case "tab":
			return m.paletteComplete(), nil
		case "enter":
			return m.paletteInsert(), nil
		}
	}

	switch stroke {
	case "ctrl+c":
		// Always quits (R-CHAT-6). Ctrl+D also quits as a familiar
		// "EOF closes input" convention.
		m.quitting = true
		return m, tea.Quit
	case "ctrl+d":
		m.quitting = true
		return m, tea.Quit

	case "ctrl+b":
		if m.statusLayout == StatusHeader {
			m.statusLayout = StatusSidebar
		} else {
			m.statusLayout = StatusHeader
		}
		if m.opts.PersistStatusLayout != nil {
			_ = m.opts.PersistStatusLayout(m.statusLayout)
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
	// Refresh palette state from the updated input — opens a new
	// palette on a fresh `/` or `@` trigger, closes the active one
	// when the trigger is deleted, or updates the filter on any
	// other keystroke.
	m.refreshPalette()
	return m, tea.Batch(taCmd, vpCmd)
}

// refreshPalette re-derives palette state from the current textarea
// content. Called from handleKey after every forwarded keystroke so
// the palette opens / closes / re-filters in lock-step with the user
// typing into the input.
//
// Triggering rules (R-PAL-1 / R-PAL-2):
//   - `/` at the very start of input opens a slash palette.
//   - `@` at a word boundary anywhere opens a file palette.
//   - Deleting the trigger char closes the active palette.
func (m *Model) refreshPalette() {
	value := m.input.Value()

	if m.palette == nil {
		if strings.HasPrefix(value, "/") {
			m.palette = newSlashPalette(0)
			m.palette.filter = value[1:]
			m.resize()
			return
		}
		if idx := lastAtTokenStart(value); idx >= 0 {
			m.palette = newFilePalette(idx)
			m.palette.filter = atFilterFrom(value, idx)
			m.resize()
			return
		}
		return
	}

	// Palette is open — verify the trigger char still sits at
	// triggerPos, otherwise close it.
	if m.palette.triggerPos >= len(value) {
		m.palette = nil
		m.resize()
		return
	}
	if string(value[m.palette.triggerPos]) != m.palette.triggerRune() {
		m.palette = nil
		m.resize()
		return
	}
	if m.palette.kind == paletteSlash {
		m.palette.filter = value[1:]
	} else {
		m.palette.filter = atFilterFrom(value, m.palette.triggerPos)
	}
	// Clamp cursor to the new filtered list.
	if n := len(m.palette.filtered()); m.palette.cursor >= n {
		if n > 0 {
			m.palette.cursor = n - 1
		} else {
			m.palette.cursor = 0
		}
	}
}

// lastAtTokenStart returns the byte index of the most recent `@` in s
// that sits at a word boundary (start of string or after whitespace),
// or -1 when no such `@` exists.
func lastAtTokenStart(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] != '@' {
			continue
		}
		if i == 0 {
			return 0
		}
		switch s[i-1] {
		case ' ', '\t', '\n':
			return i
		}
	}
	return -1
}

// atFilterFrom returns the filter text following an `@` at position
// triggerPos in s — everything after `@` up to (but not including)
// the next whitespace or end-of-string.
func atFilterFrom(s string, triggerPos int) string {
	rest := s[triggerPos+1:]
	if sp := strings.IndexAny(rest, " \t\n"); sp >= 0 {
		return rest[:sp]
	}
	return rest
}

// paletteComplete extends the input with the longest common prefix
// of the matched palette items (Tab while palette is open). Leaves
// the palette open for further filtering. Idempotent when the filter
// is already the full common prefix.
func (m Model) paletteComplete() tea.Model {
	if m.palette == nil {
		return m
	}
	extension := m.palette.completion()
	if extension == "" {
		return m
	}
	value := m.input.Value()
	tokenEnd := m.palette.triggerPos + 1 + len(m.palette.filter)
	if tokenEnd > len(value) {
		tokenEnd = len(value)
	}
	newValue := value[:m.palette.triggerPos] + m.palette.triggerRune() + extension + value[tokenEnd:]
	m.input.SetValue(newValue)
	m.refreshPalette()
	return m
}

// paletteInsert replaces the typed trigger token with the selected
// item's insert form and closes the palette (Enter while palette is
// open). Unavailable items are skipped — closing the palette
// silently — until a real slice surfaces a system-message hint.
func (m Model) paletteInsert() tea.Model {
	if m.palette == nil {
		return m
	}
	item, ok := m.palette.selected()
	if !ok || !item.Available {
		m.palette = nil
		m.resize()
		return m
	}
	value := m.input.Value()
	tokenEnd := m.palette.triggerPos + 1 + len(m.palette.filter)
	if tokenEnd > len(value) {
		tokenEnd = len(value)
	}
	insertText := item.insertText(m.palette.kind)
	if m.palette.kind == paletteFile {
		insertText += " "
	}
	newValue := value[:m.palette.triggerPos] + insertText + value[tokenEnd:]
	m.input.SetValue(newValue)
	m.palette = nil
	m.resize()
	return m
}
