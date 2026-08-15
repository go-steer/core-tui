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
// Replaced the original enum-driven overlay + picker-index field on
// Model with a self-contained dialog that owns its selection state.
// (The enum path was deleted outright in the v1.0 pre-freeze sweep,
// core-tui #79.)
//
// Permission / elicit / sideAnswer modals stay inline this PR
// because they're tied to channel-based Prompter / Elicitor /
// SlashProvider lifecycles. They can be migrated to Dialog in a
// future PR once the lifecycle is decoupled.

package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const modelPickerDialogID = "model-picker"

// modelPickerDialog renders the available-models list with cursor
// + "(current)" marker, dispatches SwitchModel on Enter, persists
// via PersistModelChoice when wired.
//
// The list is a SNAPSHOT (issue #114). AvailableModels() used to be
// re-pulled on every keystroke from HandleKey and again from Render
// — i.e. from View(), which host_snapshot.go documents as never
// touching the host. Now it is pulled exactly once, off the Update
// goroutine, by the availableModelsCmd the open site returns; the
// reply lands as modelsLoadedMsg and Update calls applyModels.
// HandleKey and Render both read the snapshot and never call the
// host.
type modelPickerDialog struct {
	// loaded flips when modelsLoadedMsg installs the snapshot. Until
	// then the body renders a loading line, so the dialog opens
	// instantly no matter how slow the host is.
	//
	// There is deliberately no third "unwired" state: whether the
	// agent implements ModelSwapper is a type assertion, which costs
	// nothing and is safe from View() — only the LIST is a host call
	// and only the list is snapshotted.
	loaded bool

	// models is the snapshot itself — the full, unfiltered list
	// exactly as the host returned it. Never re-pulled while the
	// dialog is open.
	models []ModelInfo

	// idx is the cursor, an index into rows() (see rows() for why
	// that indirection exists).
	idx int

	// off is the first visible row — a host advertising more models
	// than the terminal has rows would otherwise paint the tail of
	// the list past the bottom edge with no way to reach it.
	off int

	// switching is the model ID of an in-flight SwitchModel, or "".
	// The dialog stays open showing "switching to <id>…" until
	// modelSwitchedMsg lands, so the operator sees the host working
	// instead of a frozen list.
	switching string
}

// newModelPickerDialog constructs a fresh picker focused on the first
// row, with no snapshot yet. Prefer Model.openModelPicker, which
// pairs the Open with the Cmd that fills it; the Overlay container
// owns lifecycle.
func newModelPickerDialog() *modelPickerDialog {
	return &modelPickerDialog{idx: 0}
}

// openModelPicker pushes the model picker (singleton) and returns the
// Cmd that pulls AvailableModels() off the Update goroutine. Nil Cmd
// when the picker is already open or the capability is unwired.
func (m *Model) openModelPicker() tea.Cmd {
	if m.overlayStack.HasID(modelPickerDialogID) {
		return nil
	}
	m.overlayStack.Open(newModelPickerDialog())
	return m.availableModelsCmd()
}

func (d *modelPickerDialog) ID() string { return modelPickerDialogID }

// rows returns the list the cursor indexes into and Render paints.
// Today that is the whole snapshot. It exists as a seam: type-to-
// filter (issue #117) narrows the snapshot here and every other part
// of the dialog — cursor wrap, window scroll, Enter — keeps working
// unchanged, because none of them touch d.models directly.
func (d *modelPickerDialog) rows() []ModelInfo { return d.models }

// applyModels installs the open-time snapshot. Called from Update's
// modelsLoadedMsg handler, which addresses the dialog through
// overlayStack.Get so a modal stacked on top since the open still
// routes the reply here.
//
// This is also where the cursor gets seeded: issue #110 wants it on
// the CURRENT model rather than row 0, and the list only exists from
// this point on — the constructor has nothing to seed against.
//
// current is the caller's m.displayModelName(). It is passed in rather
// than read here because a dialog must not reach into the Model for
// state it can be handed; Render already resolves it the same way.
func (d *modelPickerDialog) applyModels(models []ModelInfo, current string) {
	d.models = models
	d.loaded = true
	d.idx = indexOfModel(models, current)
	d.off = 0
}

// indexOfModel finds the row for the active model, matching on the
// same predicate Render uses to paint its "(current)" tag — ID first,
// then the display name, since a host may advertise either as the
// thing the operator recognises. Returns 0 when nothing matches, which
// covers an unset model and a host whose current model is not in its
// own list.
func indexOfModel(models []ModelInfo, current string) int {
	if current == "" {
		return 0
	}
	for i, mi := range models {
		disp := mi.Display
		if disp == "" {
			disp = mi.ID
		}
		if mi.ID == current || disp == current {
			return i
		}
	}
	return 0
}

func (d *modelPickerDialog) HandleKey(stroke string, m *Model) DialogAction {
	if stroke == "esc" {
		// Esc always closes, including mid-switch: the host call is
		// already committed, so its reply still applies the agent —
		// the operator is just declining to watch.
		return DialogAction{Consumed: true, Close: true}
	}
	if d.switching != "" {
		// A SwitchModel is in flight. Swallow everything so the
		// operator can't queue a second switch against a list that
		// is about to be replaced.
		return DialogAction{Consumed: true}
	}
	swapper, wired := m.opts.Agent.(ModelSwapper)
	if !wired {
		// Agent doesn't support model swapping — close cleanly.
		return DialogAction{Consumed: true, Close: true}
	}
	if !d.loaded {
		// Snapshot hasn't landed. Consume so keys don't leak to the
		// textarea behind the modal, but there is nothing to move.
		return DialogAction{Consumed: true}
	}
	rows := d.rows()
	if len(rows) == 0 {
		m.history.Append(Message{Role: RoleSystem, Text: "/model: no models available"})
		m.refreshViewport()
		return DialogAction{Consumed: true, Close: true}
	}
	if d.idx >= len(rows) {
		d.idx = len(rows) - 1
	}
	if d.idx < 0 {
		d.idx = 0
	}
	switch stroke {
	case "up", "ctrl+p":
		d.idx = (d.idx - 1 + len(rows)) % len(rows)
		return DialogAction{Consumed: true}
	case "down", "ctrl+n":
		d.idx = (d.idx + 1) % len(rows)
		return DialogAction{Consumed: true}
	case "enter":
		pick := rows[d.idx]
		// Stay open + mark in flight; the reply arrives as
		// modelSwitchedMsg and closes us from Update.
		d.switching = pick.ID
		return DialogAction{Consumed: true, Cmd: switchModelCmd(swapper, m.sessionGen, pick.ID)}
	}
	// Unhandled key — consume so it doesn't leak to the textarea
	// behind the modal, but don't close.
	return DialogAction{Consumed: true}
}

// applyModelSwitch is the Update-side half of the Enter path: attach
// the new agent (or report the failure), persist, and re-resolve the
// theme. Called only after the sessionGen guard has passed, because
// it replaces m.opts.Agent.
func (m *Model) applyModelSwitch(msg modelSwitchedMsg) {
	if msg.err != nil {
		m.history.Append(Message{Role: RoleError, Text: "/model: switch failed: " + msg.err.Error()})
		m.refreshViewport()
		return
	}
	if msg.agent == nil {
		m.history.Append(Message{Role: RoleError, Text: "/model: ModelSwapper returned nil Agent"})
		m.refreshViewport()
		return
	}
	m.opts.Agent = msg.agent
	m.history.Append(Message{Role: RoleSystem, Text: "/model: switched to " + msg.id})
	if m.opts.PersistModelChoice != nil {
		if perr := m.opts.PersistModelChoice(msg.id); perr != nil {
			m.history.Append(Message{Role: RoleError, Text: "/model: persist failed: " + perr.Error()})
		}
	}
	// Refresh the theme so per-provider palettes (when
	// AutoProviderTheme is on) track the freshly-selected
	// model's provider. No-op when AutoProviderTheme is off
	// — resolveStyles returns the same DefaultTheme.
	m.refreshTheme()
	m.refreshViewport()
}

func (d *modelPickerDialog) Render(totalWidth int, m *Model) string {
	width := 64
	if totalWidth > 0 && width > totalWidth-4 {
		width = totalWidth - 4
	}
	if width < 30 {
		width = 30
	}

	// Snapshot-only render (issue #114): View() must never call the
	// host, so everything below reads d.models / d.load.
	_, wired := m.opts.Agent.(ModelSwapper)
	body := ""
	switch {
	case !wired:
		body = m.styles.Muted.Render("agent does not implement ModelSwapper")
	case d.switching != "":
		body = m.styles.Muted.Render("switching to " + d.switching + "…")
	case !d.loaded:
		body = m.styles.Muted.Render("loading models…")
	default:
		models := d.rows()
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
			view := modalBodyHeight(m.height, modalChromeRows)
			d.off = listWindow(d.off, d.idx, len(rows), view)
			body = strings.Join(scrollView(m.styles, rows, nonNeg(width-4), view, d.off), "\n")
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
