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

	// filter is the type-to-filter row (issue #117). It narrows
	// rows(), which is the only thing the rest of the dialog reads.
	filter pickerFilter

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
	return &modelPickerDialog{idx: 0, filter: newPickerFilter()}
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

// rows returns the list the cursor indexes into and Render paints:
// the snapshot narrowed by the filter row, ranked best-first
// (rank.go). Issue #114 built this accessor as the seam for exactly
// this, and it held — cursor wrap, window scroll and Enter all index
// rows() rather than d.models, so none of them needed to change when
// the list started shrinking under them.
func (d *modelPickerDialog) rows() []ModelInfo {
	filter := d.filter.value()
	if filter == "" {
		return d.models
	}
	keys := make([]string, len(d.models))
	for i, mi := range d.models {
		keys[i] = pickerKey(mi.Display, mi.ID)
	}
	idx := rankNames(keys, filter)
	out := make([]ModelInfo, len(idx))
	for i, at := range idx {
		out[i] = d.models[at]
	}
	return out
}

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

// HandleKey satisfies Dialog for callers holding only a normalized
// stroke. It synthesizes a KeyPressMsg and delegates, so both entry
// points behave identically — see KeyMsgDialog's godoc for why the
// picker wants the raw msg now that it owns a text input.
func (d *modelPickerDialog) HandleKey(stroke string, m *Model) DialogAction {
	return d.HandleKeyMsg(keyMsgFromStroke(stroke), m)
}

// HandleKeyMsg is the real key handler. Navigation strokes drive the
// list; everything else — every printable grapheme, backspace, the
// widget's own kill bindings, a bracketed paste — is filter input.
func (d *modelPickerDialog) HandleKeyMsg(msg tea.KeyPressMsg, m *Model) DialogAction {
	stroke := msg.String()
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
	if len(d.models) == 0 {
		// The HOST advertised nothing. Unchanged from before the
		// filter existed: say so and close, on any key. Distinct from
		// the filter matching nothing, which is handled below.
		m.history.Append(Message{Role: RoleSystem, Text: "/model: no models available"})
		m.refreshViewport()
		return DialogAction{Consumed: true, Close: true}
	}
	if !pickerNavStroke(stroke) {
		cmd, changed := d.filter.handleKeyMsg(msg)
		if changed {
			// The list under the cursor was just replaced. Land on
			// the best match rather than trying to keep hold of a row
			// that may not be in the new list at all.
			d.idx, d.off = 0, 0
		}
		return DialogAction{Consumed: true, Cmd: cmd}
	}

	rows := d.rows()
	if len(rows) == 0 {
		// The FILTER matched nothing. Stay open with the row on
		// screen: the operator's next keystroke is a backspace.
		return DialogAction{Consumed: true}
	}
	d.idx = clampIndex(d.idx, len(rows))
	switch stroke {
	case "up", "ctrl+p":
		d.idx = stepIndex(d.idx, -1, len(rows))
		return DialogAction{Consumed: true}
	case "down", "ctrl+n":
		d.idx = stepIndex(d.idx, 1, len(rows))
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

// filtering reports whether the filter row is on screen, which is
// also the condition for the picker owning the terminal caret. Only
// the loaded-list state has one: a loading line, an in-flight switch,
// an unwired agent and a host that advertised nothing are all states
// with nothing to narrow.
func (d *modelPickerDialog) filtering(m *Model) bool {
	if _, wired := m.opts.Agent.(ModelSwapper); !wired {
		return false
	}
	return d.loaded && d.switching == "" && len(d.models) > 0
}

// DialogCursor implements cursorDialog. Without it modalCursor would
// return (nil, true) for a dialog the operator is typing into, which
// is #105 / #123 regressed on the surface most likely to see CJK —
// no hardware caret means no IME anchor.
func (d *modelPickerDialog) DialogCursor(_ int, m *Model) *tea.Cursor {
	if !d.filtering(m) {
		return nil
	}
	return filterRowCursor(d.filter.cursor())
}

// applyModelSwitch is the Update-side half of the Enter path: attach
// the new agent (or report the failure), persist, and re-resolve the
// theme. Called only after the sessionGen guard has passed, because
// it replaces m.opts.Agent.
//
// Returns the Cmd that runs Options.PersistModelChoice — host code
// that writes the pick to the host's config, so it doesn't belong on
// the Update goroutine either (issue #137). Nil when unwired; the
// failure row arrives later as persistDoneMsg.
func (m *Model) applyModelSwitch(msg modelSwitchedMsg) tea.Cmd {
	if msg.err != nil {
		m.history.Append(Message{Role: RoleError, Text: "/model: switch failed: " + msg.err.Error()})
		m.refreshViewport()
		return nil
	}
	if msg.agent == nil {
		m.history.Append(Message{Role: RoleError, Text: "/model: ModelSwapper returned nil Agent"})
		m.refreshViewport()
		return nil
	}
	m.opts.Agent = msg.agent
	m.history.Append(Message{Role: RoleSystem, Text: "/model: switched to " + msg.id})
	// Refresh the theme so per-provider palettes (when
	// AutoProviderTheme is on) track the freshly-selected
	// model's provider. No-op when AutoProviderTheme is off
	// — resolveStyles returns the same DefaultTheme.
	m.refreshTheme()
	m.refreshViewport()
	return persistChoiceCmd(m.sessionGen, "/model", m.opts.PersistModelChoice, msg.id)
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
	case len(d.models) == 0:
		body = m.styles.Muted.Render("(no models advertised by the agent)")
	default:
		models := d.rows()
		filter := d.filter.value()
		// The filter row is the FIRST body row, always — including
		// when it matched nothing, since it is the thing the operator
		// has to edit to get back. Its row is paid for with
		// modalChromeRows+1 below.
		lines := []string{d.filter.render(width, len(models), len(d.models), m.styles)}
		if len(models) == 0 {
			lines = append(lines, m.styles.Muted.Render("no models match "+quoteFilter(filter)))
			body = strings.Join(lines, "\n")
			break
		}
		current := m.displayModelName()
		d.idx = clampIndex(d.idx, len(models))
		rows := make([]string, 0, len(models))
		for i, mi := range models {
			disp := mi.Display
			if disp == "" {
				disp = mi.ID
			}
			base := lipgloss.NewStyle()
			marker := "  "
			if i == d.idx {
				marker, base = "> ", m.styles.Accent
			}
			row := base.Render(marker) + highlightSpan(disp, filter, base)
			if mi.ID != disp {
				row += m.styles.Muted.Render("  (") +
					highlightSpan(mi.ID, filter, m.styles.Muted) +
					m.styles.Muted.Render(")")
			}
			if mi.ID == current || disp == current {
				row += "  " + m.styles.Muted.Render("(current)")
			}
			if mi.Description != "" {
				row += "  " + m.styles.Muted.Render(mi.Description)
			}
			rows = append(rows, row)
		}
		view := modalBodyHeight(m.height, modalChromeRows+1)
		d.off = listWindow(d.off, d.idx, len(rows), view)
		lines = append(lines, scrollView(m.styles, rows, nonNeg(width-4), view, d.off)...)
		body = strings.Join(lines, "\n")
	}
	footer := "type to filter " + GlyphSeparator + " ↑↓ choose " +
		GlyphSeparator + " enter accept " + GlyphSeparator + " esc cancel"
	return RenderContext{
		Title:  "Choose a Model",
		Body:   body,
		Footer: footer,
		Width:  width,
		Styles: m.styles,
	}.Render()
}
