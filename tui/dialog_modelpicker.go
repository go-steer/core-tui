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

// Model picker dialog — first concrete Dialog implementation.
// Replaces the m.overlay==overlayModelPicker enum + m.modelPickerIdx
// field with a self-contained dialog that owns its selection state.
//
// Permission / elicit / sideAnswer modals stay inline this PR
// because they're tied to channel-based Prompter / Elicitor /
// SlashProvider lifecycles. They can be migrated to Dialog in a
// future PR once the lifecycle is decoupled.

package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

const modelPickerDialogID = "model-picker"

// modelPickerDialog renders the available-models list with cursor
// + "(current)" marker, dispatches SwitchModel on Enter, persists
// via PersistModelChoice when wired.
type modelPickerDialog struct {
	idx int
}

// newModelPickerDialog constructs a fresh picker focused on the
// first row. Callers OPEN the dialog via overlay.Open; the
// Overlay container owns lifecycle.
func newModelPickerDialog() *modelPickerDialog {
	return &modelPickerDialog{idx: 0}
}

func (d *modelPickerDialog) ID() string { return modelPickerDialogID }

func (d *modelPickerDialog) HandleKey(stroke string, m *Model) DialogAction {
	swapper, ok := m.opts.Agent.(ModelSwapper)
	if !ok {
		// Agent doesn't support model swapping — close cleanly.
		return DialogAction{Consumed: true, Close: true}
	}
	models := swapper.AvailableModels()
	if len(models) == 0 {
		m.history.Append(Message{Role: RoleSystem, Text: "/model: no models available"})
		m.refreshViewport()
		return DialogAction{Consumed: true, Close: true}
	}
	switch stroke {
	case "esc":
		return DialogAction{Consumed: true, Close: true}
	case "up", "ctrl+p":
		d.idx = (d.idx - 1 + len(models)) % len(models)
		return DialogAction{Consumed: true}
	case "down", "ctrl+n":
		d.idx = (d.idx + 1) % len(models)
		return DialogAction{Consumed: true}
	case "enter":
		pick := models[d.idx]
		newAgent, err := swapper.SwitchModel(pick.ID)
		if err != nil {
			m.history.Append(Message{Role: RoleError, Text: "/model: switch failed: " + err.Error()})
			m.refreshViewport()
			return DialogAction{Consumed: true, Close: true}
		}
		m.opts.Agent = newAgent
		m.history.Append(Message{Role: RoleSystem, Text: "/model: switched to " + pick.ID})
		if m.opts.PersistModelChoice != nil {
			if perr := m.opts.PersistModelChoice(pick.ID); perr != nil {
				m.history.Append(Message{Role: RoleError, Text: "/model: persist failed: " + perr.Error()})
			}
		}
		m.refreshViewport()
		return DialogAction{Consumed: true, Close: true}
	}
	// Unhandled key — consume so it doesn't leak to the textarea
	// behind the modal, but don't close.
	return DialogAction{Consumed: true}
}

func (d *modelPickerDialog) Render(totalWidth int, m *Model) string {
	width := 64
	if totalWidth > 0 && width > totalWidth-4 {
		width = totalWidth - 4
	}
	if width < 30 {
		width = 30
	}

	swapper, ok := m.opts.Agent.(ModelSwapper)
	body := ""
	if !ok {
		body = m.styles.Muted.Render("agent does not implement ModelSwapper")
	} else {
		models := swapper.AvailableModels()
		if len(models) == 0 {
			body = m.styles.Muted.Render("(no models advertised by the agent)")
		} else {
			current := m.displayModelName()
			rows := make([]string, 0, len(models))
			for i, mi := range models {
				disp := mi.Display
				if disp == "" {
					disp = mi.ID
				}
				marker := "  "
				if i == d.idx {
					marker = "> "
				}
				row := marker + disp
				if mi.ID != disp {
					row += m.styles.Muted.Render("  (" + mi.ID + ")")
				}
				if mi.ID == current || disp == current {
					row += "  " + m.styles.Muted.Render("(current)")
				}
				if mi.Description != "" {
					row += "  " + m.styles.Muted.Render(mi.Description)
				}
				if i == d.idx {
					row = m.styles.Accent.Render(row)
				}
				rows = append(rows, row)
			}
			body = strings.Join(rows, "\n")
		}
	}
	footer := "↑↓ choose " + GlyphSeparator + " enter accept " + GlyphSeparator + " esc cancel"
	return RenderContext{
		Title:  "Choose a Model",
		Body:   body,
		Footer: footer,
		Width:  width,
		Styles: m.styles,
	}.Render()
}

// Suppress unused-import lint when lipgloss isn't needed at this
// file level — RenderContext does the lipgloss work via Styles.
var _ = lipgloss.Left
