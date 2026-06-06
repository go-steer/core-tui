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

// Theme picker dialog. Mirrors dialog_modelpicker.go with one
// twist: cursor moves *apply* the focused theme so the operator
// sees the palette live. Esc restores the original; Enter
// commits and fires a ThemeChangedMsg the host can persist.

package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

const themePickerDialogID = "theme-picker"

// themePickerDialog renders the BuiltinThemes() list with a
// cursor + "(current)" marker. originalName captures the theme
// in effect when the picker opened so esc can restore it.
type themePickerDialog struct {
	idx          int
	originalName string
}

// newThemePickerDialog focuses the row matching the currently
// active theme so the operator's first arrow-press visibly moves
// off the active row (rather than landing on a random row that
// happens to be entry 0). originalName is captured for restore-
// on-cancel.
func newThemePickerDialog(currentName string) *themePickerDialog {
	themes := BuiltinThemes()
	idx := 0
	for i, bt := range themes {
		if strings.EqualFold(bt.Name, currentName) {
			idx = i
			break
		}
	}
	return &themePickerDialog{idx: idx, originalName: currentName}
}

func (d *themePickerDialog) ID() string { return themePickerDialogID }

func (d *themePickerDialog) HandleKey(stroke string, m *Model) DialogAction {
	themes := BuiltinThemes()
	if len(themes) == 0 {
		return DialogAction{Consumed: true, Close: true}
	}
	switch stroke {
	case "esc":
		// Restore the theme that was active when the picker
		// opened. applyNamedTheme tolerates an empty string ("")
		// which falls back to the auto / per-provider path —
		// exactly what we want when the operator never had an
		// explicit pick before opening the picker.
		m.applyNamedTheme(d.originalName)
		return DialogAction{Consumed: true, Close: true}
	case "up", "ctrl+p":
		d.idx = (d.idx - 1 + len(themes)) % len(themes)
		m.applyNamedTheme(themes[d.idx].Name)
		return DialogAction{Consumed: true}
	case "down", "ctrl+n":
		d.idx = (d.idx + 1) % len(themes)
		m.applyNamedTheme(themes[d.idx].Name)
		return DialogAction{Consumed: true}
	case "enter":
		pick := themes[d.idx]
		m.applyNamedTheme(pick.Name)
		m.history.Append(Message{Role: RoleSystem, Text: "/theme: switched to " + pick.Name})
		// Callback persistence path (mirrors PersistModelChoice
		// in dialog_modelpicker.go). Error surfaces as a system
		// message but doesn't block the switch — the operator's
		// session reflects the pick either way.
		if m.opts.PersistThemeChoice != nil {
			if perr := m.opts.PersistThemeChoice(pick.Name); perr != nil {
				m.history.Append(Message{Role: RoleError, Text: "/theme: persist failed: " + perr.Error()})
			}
		}
		m.refreshViewport()
		// Also emit ThemeChangedMsg — hosts can use either the
		// callback OR the msg observation pattern (or both).
		name := pick.Name
		cmd := func() tea.Msg { return ThemeChangedMsg{Name: name} }
		return DialogAction{Consumed: true, Close: true, Cmd: cmd}
	}
	// Unknown key — consume so it doesn't leak to the textarea
	// behind the modal, but don't close.
	return DialogAction{Consumed: true}
}

func (d *themePickerDialog) Render(totalWidth int, m *Model) string {
	width := 72
	if totalWidth > 0 && width > totalWidth-4 {
		width = totalWidth - 4
	}
	if width < 30 {
		width = 30
	}

	themes := BuiltinThemes()
	body := ""
	if len(themes) == 0 {
		body = m.styles.Muted.Render("(no themes registered)")
	} else {
		rows := make([]string, 0, len(themes))
		for i, bt := range themes {
			marker := "  "
			if i == d.idx {
				marker = "> "
			}
			row := marker + bt.Name
			if strings.EqualFold(bt.Name, m.themeName) {
				row += "  " + m.styles.Muted.Render("(current)")
			}
			if bt.Description != "" {
				row += "  " + m.styles.Muted.Render(bt.Description)
			}
			if i == d.idx {
				row = m.styles.Accent.Render(row)
			}
			rows = append(rows, row)
		}
		body = strings.Join(rows, "\n")
	}
	footer := "↑↓ preview " + GlyphSeparator + " enter accept " + GlyphSeparator + " esc cancel"
	return RenderContext{
		Title:  "Choose a Theme",
		Body:   body,
		Footer: footer,
		Width:  width,
		Styles: m.styles,
	}.Render()
}
