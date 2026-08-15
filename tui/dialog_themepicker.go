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
	"charm.land/lipgloss/v2"
)

const themePickerDialogID = "theme-picker"

// themePickerDialog renders the BuiltinThemes() list with a
// cursor + "(current)" marker. originalName captures the theme
// in effect when the picker opened so esc can restore it.
type themePickerDialog struct {
	idx          int
	originalName string
	// off is the first visible row: the builtin registry already
	// outgrows a short terminal, so the list windows around the
	// cursor rather than painting past the bottom edge.
	off int

	// filter is the type-to-filter row (issue #117). This picker read
	// BuiltinThemes() straight from HandleKey and Render and had no
	// rows() accessor at all — the filter brought it into line with
	// the other two.
	filter pickerFilter
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
	return &themePickerDialog{idx: idx, originalName: currentName, filter: newPickerFilter()}
}

func (d *themePickerDialog) ID() string { return themePickerDialogID }

// rows returns the registry narrowed by the filter row, ranked by the
// shared classifier (rank.go).
func (d *themePickerDialog) rows() []BuiltinTheme {
	themes := BuiltinThemes()
	filter := d.filter.value()
	if filter == "" {
		return themes
	}
	keys := make([]string, len(themes))
	for i, bt := range themes {
		keys[i] = bt.Name
	}
	idx := rankNames(keys, filter)
	out := make([]BuiltinTheme, len(idx))
	for i, at := range idx {
		out[i] = themes[at]
	}
	return out
}

// HandleKey satisfies Dialog for callers holding only a normalized
// stroke; HandleKeyMsg is the real handler, because a stroke string
// drops Key.Text and cannot carry a paste.
func (d *themePickerDialog) HandleKey(stroke string, m *Model) DialogAction {
	return d.HandleKeyMsg(keyMsgFromStroke(stroke), m)
}

// HandleKeyMsg drives the list on navigation strokes and feeds
// everything else to the filter.
//
// Filtering deliberately does NOT preview. The live preview is bound
// to cursor MOVEMENT — which is what the "↑↓ preview" footer promises
// — and repainting the whole chat in a new palette on every keystroke
// of a filter would be a strobe, not a preview. Enter still applies
// whatever row is highlighted, so no theme becomes unreachable.
func (d *themePickerDialog) HandleKeyMsg(msg tea.KeyPressMsg, m *Model) DialogAction {
	stroke := msg.String()
	if stroke == "esc" {
		// Restore the theme that was active when the picker
		// opened. applyNamedTheme tolerates an empty string ("")
		// which falls back to the auto / per-provider path —
		// exactly what we want when the operator never had an
		// explicit pick before opening the picker.
		m.applyNamedTheme(d.originalName)
		return DialogAction{Consumed: true, Close: true}
	}
	if len(BuiltinThemes()) == 0 {
		return DialogAction{Consumed: true, Close: true}
	}
	if !pickerNavStroke(stroke) {
		cmd, changed := d.filter.handleKeyMsg(msg)
		if changed {
			d.idx, d.off = 0, 0
		}
		return DialogAction{Consumed: true, Cmd: cmd}
	}

	themes := d.rows()
	if len(themes) == 0 {
		// The filter matched no theme; stay open so it can be edited.
		return DialogAction{Consumed: true}
	}
	d.idx = clampIndex(d.idx, len(themes))
	switch stroke {
	case "up", "ctrl+p":
		d.idx = stepIndex(d.idx, -1, len(themes))
		m.applyNamedTheme(themes[d.idx].Name)
		return DialogAction{Consumed: true}
	case "down", "ctrl+n":
		d.idx = stepIndex(d.idx, 1, len(themes))
		m.applyNamedTheme(themes[d.idx].Name)
		return DialogAction{Consumed: true}
	case "enter":
		pick := themes[d.idx]
		m.applyNamedTheme(pick.Name)
		m.history.Append(Message{Role: RoleSystem, Text: "/theme: switched to " + pick.Name})
		m.refreshViewport()
		// Also emit ThemeChangedMsg — hosts can use either the
		// callback OR the msg observation pattern (or both).
		name := pick.Name
		// Callback persistence path (mirrors PersistModelChoice in
		// dialog_modelpicker.go), off the Update goroutine because it
		// is host code writing to the host's config (issue #137). A
		// failure lands later as persistDoneMsg and doesn't block the
		// switch — the operator's session reflects the pick either way.
		cmd := tea.Batch(
			func() tea.Msg { return ThemeChangedMsg{Name: name} },
			persistChoiceCmd(m.sessionGen, "/theme", m.opts.PersistThemeChoice, name),
		)
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

	all := BuiltinThemes()
	body := ""
	switch {
	case len(all) == 0:
		body = m.styles.Muted.Render("(no themes registered)")
	default:
		themes := d.rows()
		filter := d.filter.value()
		// The filter row is the first body row; its cost is the +1 on
		// modalChromeRows below.
		lines := []string{d.filter.render(width, len(themes), len(all), m.styles)}
		if len(themes) == 0 {
			lines = append(lines, m.styles.Muted.Render("no themes match "+quoteFilter(filter)))
			body = strings.Join(lines, "\n")
			break
		}
		d.idx = clampIndex(d.idx, len(themes))
		rows := make([]string, 0, len(themes))
		for i, bt := range themes {
			base := lipgloss.NewStyle()
			marker := "  "
			if i == d.idx {
				marker, base = "> ", m.styles.Accent
			}
			row := base.Render(marker) + highlightSpan(bt.Name, filter, base)
			if strings.EqualFold(bt.Name, m.themeName) {
				row += "  " + m.styles.Muted.Render("(current)")
			}
			if bt.Description != "" {
				row += "  " + m.styles.Muted.Render(bt.Description)
			}
			rows = append(rows, row)
		}
		view := modalBodyHeight(m.height, modalChromeRows+1)
		d.off = listWindow(d.off, d.idx, len(rows), view)
		lines = append(lines, scrollView(m.styles, rows, nonNeg(width-4), view, d.off)...)
		body = strings.Join(lines, "\n")
	}
	footer := "type to filter " + GlyphSeparator + " ↑↓ preview " +
		GlyphSeparator + " enter accept " + GlyphSeparator + " esc cancel"
	return RenderContext{
		Title:  "Choose a Theme",
		Body:   body,
		Footer: footer,
		Width:  width,
		Styles: m.styles,
	}.Render()
}

// DialogCursor implements cursorDialog so the filter row owns the
// terminal caret. Nil only when the registry is empty — every other
// state of this picker renders the filter row.
func (d *themePickerDialog) DialogCursor(_ int, _ *Model) *tea.Cursor {
	if len(BuiltinThemes()) == 0 {
		return nil
	}
	return filterRowCursor(d.filter.cursor())
}
