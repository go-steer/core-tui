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

// The model picker, as a question (issue #164, stage 3).
//
// The theme picker (question_themepicker.go) went first because it is
// synchronous: the keystroke that picks a row is also the moment the
// answer exists. This one is the other shape, and it is the shape the
// session picker, the permission prompt and the elicit form all share.
//
// Enter here does not answer the question. It starts a host call —
// ModelSwapper.SwitchModel, off the Update goroutine — and the picker
// stays on screen showing "switching to <id>…" until the reply lands,
// because a list that freezes for as long as the host takes reads as a
// hang. The answer therefore arrives from Update, not from Key, and
// overlay.resolve is the seam that lets it.
//
// What moved off the widget. It no longer type-asserts m.opts.Agent to
// ModelSwapper, no longer reads m.displayModelName() while rendering,
// no longer appends to m.history, and no longer builds a host Cmd out
// of m.sessionGen. Those were the four reasons it needed a *Model, and
// they are the four reasons it could not be tested without one.
//
// What deliberately did NOT move: attaching the new Agent, announcing
// the switch and persisting the choice all stay in applyModelSwitch.
// See modelPickerResolver for why.

package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const modelPickerDialogID = "model-picker"

// modelPickerWidth is the picker's preferred total width. The text
// input dialog matches it (defaultTextInputWidth) so a prompt opened
// from a picker does not visibly jump in size.
const modelPickerWidth = 64

// modelSwitchRequestedMsg is Enter, on its way to a host call.
//
// The picker cannot build the SwitchModel Cmd itself: that needs the
// live ModelSwapper and the live sessionGen, and both can change while
// the picker is open — SwitchModel replaces m.opts.Agent, and a session
// switch bumps the generation without closing the overlay stack.
// Capturing either when the picker opened would call the outgoing agent
// or stamp a reply the generation guard then drops on the floor. So the
// widget names the model and the Update loop, which holds both, makes
// the call.
type modelSwitchRequestedMsg struct{ ID string }

// modelPickerQuestion renders the available-models list and answers
// with the model the operator switched to.
//
// The list is a SNAPSHOT (issue #114): AvailableModels() is pulled once
// when the picker opens, off the Update goroutine, and installed by
// applyModels. Neither Key nor Body ever calls the host.
type modelPickerQuestion struct {
	// wired is whether the agent implemented ModelSwapper when the
	// picker opened. It is a constructor argument rather than a live
	// type assertion because the assertion needs m.opts.Agent, and
	// needing that from Body is what put a host call inside View()
	// before #114.
	//
	// Read only to decide what to render and whether there is anything
	// to ask: the SwitchModel call itself re-resolves the swapper in
	// Update, so a stale true here costs a no-op, not a wrong agent.
	wired bool

	// loaded flips when applyModels installs the snapshot. Until then
	// the body renders a loading line, so the picker opens instantly no
	// matter how slow the host is.
	loaded bool

	// models is the snapshot itself — the full, unfiltered list exactly
	// as the host returned it.
	models []ModelInfo

	// current is the active model as of the moment the snapshot landed.
	// It is what "(current)" marks. Passed in rather than read live for
	// the same reason as wired.
	current string

	// idx is the cursor, an index into rows() rather than into models —
	// see rows().
	idx int

	// off is the first visible row: a host advertising more models than
	// the terminal has rows would otherwise paint the tail of the list
	// past the bottom edge with no way to reach it.
	off int

	// filter is the type-to-filter row (issue #117).
	filter pickerFilter

	// switching is the model ID of an in-flight SwitchModel, or "".
	// While it is set the question is committed but not yet answered:
	// every keystroke except esc is swallowed, so the operator cannot
	// queue a second switch against a list that is about to be
	// replaced.
	switching string

	// fail is why the last SwitchModel came back unusable, shown under
	// the list (issue #245). Set by the Update-side reply handler, which
	// is the only place that knows; the transcript row it pairs with is
	// written by applyModelSwitch and says the same thing.
	fail pickerFailure
}

func newModelPickerQuestion(wired bool) *modelPickerQuestion {
	return &modelPickerQuestion{wired: wired, filter: newPickerFilter()}
}

// openModelPicker asks the model picker (singleton) and returns the Cmd
// that pulls AvailableModels() off the Update goroutine. Nil Cmd when
// the picker is already open or the capability is unwired — an unwired
// picker still opens, because "this agent cannot swap models" is worth
// saying on the surface the operator asked for.
func (m *Model) openModelPicker() tea.Cmd {
	if m.overlayStack.hasID(modelPickerDialogID) {
		return nil
	}
	_, wired := m.opts.Agent.(ModelSwapper)
	q := newModelPickerQuestion(wired)
	m.overlayStack.ask(q, askOperator, modelPickerResolver(q))
	if !wired {
		return nil
	}
	return m.availableModelsCmd()
}

// modelPickerOn returns the picker currently asked on o, or nil when
// none is open — the reply-routing check every async arm in Update
// starts with, since a host reply can land against a picker the
// operator escaped out of or replaced.
//
// A free function over *overlay rather than a method on it: which
// questions exist is the pickers' business, and a general
// questionAt(id) with one caller would be a seam invented for a
// second caller that does not exist yet.
func modelPickerOn(o *overlay) *modelPickerQuestion {
	aq := o.asked(modelPickerDialogID)
	if aq == nil {
		return nil
	}
	q, _ := aq.q.(*modelPickerQuestion)
	return q
}

// modelPickerResolver is the other half of the picker.
//
// It closes over the question, which the theme picker's resolver does
// not need to. That is not a widget reaching into a model — it is the
// open site handing the resolver the same heap object the overlay
// holds — and it buys the one distinction the answer cannot carry: two
// different unrenderable states. "This agent has no ModelSwapper" and
// "the host advertised an empty list" are both questions that could not
// be put to the operator, and only the second is worth a transcript
// row. Encoding that as a fifth dismissReason would put a fact about
// one picker into the vocabulary every question shares.
//
// The chosen arm is deliberately thin, and this is the honest reason: a
// model pick is not itself the change. SwitchModel is a host call whose
// reply carries the new Agent, and `/model <id>` reaches that same
// reply with no picker in the loop at all — so attaching the agent,
// announcing the switch and persisting the choice live in
// applyModelSwitch, which both paths share. Duplicating them here would
// mean the two ways to change model could drift. What the answer buys
// is the pop: the picker closes BECAUSE the switch landed, through the
// exactly-once latch, rather than being closed by whichever handler
// noticed first.
func modelPickerResolver(q *modelPickerQuestion) resolver {
	return func(a answer, m *Model) tea.Cmd {
		switch a := a.(type) {
		case chosen:
			return nil
		case dismissed:
			if a.Reason == dismissUnrenderable && q.wired && q.loaded && len(q.models) == 0 {
				m.history.Append(Message{Role: RoleSystem, Text: "/model: no models available"})
				m.refreshViewport()
			}
			return nil
		case declined, selected, text, fields, decision:
			// Shapes a single-select list cannot produce. Named rather
			// than defaulted so that a variant added to the sealed set
			// is a lint failure here instead of silently routing to "do
			// nothing".
			return nil
		}
		return nil
	}
}

func (q *modelPickerQuestion) ID() string { return modelPickerDialogID }

func (q *modelPickerQuestion) Title() string { return "Choose a Model" }

func (q *modelPickerQuestion) Footer() string {
	return keyLegend("type to filter", "↑↓ choose", "enter accept", "esc cancel")
}

func (q *modelPickerQuestion) Width(avail int) int {
	width := modelPickerWidth
	if avail > 0 && width > avail-4 {
		width = avail - 4
	}
	return max(width, 30)
}

// rows returns the list the cursor indexes into and Body paints: the
// snapshot narrowed by the filter row, ranked best-first (rank.go).
// Issue #114 built this accessor as the seam for exactly this, and it
// held — cursor wrap, window scroll and Enter all index rows() rather
// than q.models, so none of them needed to change when the list started
// shrinking under them.
func (q *modelPickerQuestion) rows() []ModelInfo {
	filter := q.filter.value()
	if filter == "" {
		return q.models
	}
	keys := make([]string, len(q.models))
	for i, mi := range q.models {
		keys[i] = pickerKey(mi.Display, mi.ID)
	}
	idx := rankNames(keys, filter)
	out := make([]ModelInfo, len(idx))
	for i, at := range idx {
		out[i] = q.models[at]
	}
	return out
}

// applyModels installs the open-time snapshot, from Update's
// modelsLoadedMsg handler.
//
// This is also where the cursor gets seeded: issue #110 wants it on the
// CURRENT model rather than row 0, and the list only exists from this
// point on — the constructor has nothing to seed against.
//
// current is the caller's m.displayModelName(), passed in rather than
// read here so that the question stays free of the app model.
func (q *modelPickerQuestion) applyModels(models []ModelInfo, current string) {
	q.models = models
	q.current = current
	q.loaded = true
	q.idx = indexOfModel(models, current)
	q.off = 0
}

// indexOfModel finds the row for the active model, matching on the same
// predicate Body uses to paint its "(current)" tag — ID first, then the
// display name, since a host may advertise either as the thing the
// operator recognises. Returns 0 when nothing matches, which covers an
// unset model and a host whose current model is not in its own list.
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

// Key drives the list on navigation strokes and feeds everything else
// to the filter.
func (q *modelPickerQuestion) Key(msg tea.KeyPressMsg) (answer, tea.Cmd) {
	stroke := msg.String()
	if stroke == "esc" {
		// Esc closes even mid-switch: the host call is already
		// committed, so its reply still applies the agent — the
		// operator is just declining to watch. The reply then finds no
		// question to resolve and does the announcing itself.
		return dismissed{Reason: dismissEscape}, nil
	}
	if q.switching != "" {
		return nil, nil
	}
	if !q.wired {
		// Nothing to ask: the agent cannot swap models at all.
		return dismissed{Reason: dismissUnrenderable}, nil
	}
	if !q.loaded {
		// Snapshot has not landed. There is nothing to move, and the
		// keystroke is consumed anyway so it cannot leak to the
		// textarea behind the modal.
		return nil, nil
	}
	if len(q.models) == 0 {
		// The HOST advertised nothing — say so and close, on any key.
		// Distinct from the filter matching nothing, below.
		return dismissed{Reason: dismissUnrenderable}, nil
	}
	if !pickerNavStroke(stroke) {
		cmd, changed := q.filter.handleKeyMsg(msg)
		if changed {
			// The list under the cursor was just replaced. Land on the
			// best match rather than trying to keep hold of a row that
			// may not be in the new list at all.
			q.idx, q.off = 0, 0
			// The failure named a model that may not even be in the
			// narrowed list — drop it rather than leave it pointing at
			// nothing on screen.
			q.fail.clear()
		}
		return nil, cmd
	}

	rows := q.rows()
	if len(rows) == 0 {
		// The FILTER matched nothing. Stay open with the row on screen:
		// the operator's next keystroke is a backspace.
		return nil, nil
	}
	q.idx = clampIndex(q.idx, len(rows))
	// Every stroke from here moves the cursor or commits a new switch,
	// so whatever the last one failed at is no longer what the operator
	// is looking at.
	q.fail.clear()
	switch stroke {
	case "up", "ctrl+p":
		q.idx = stepIndex(q.idx, -1, len(rows))
		return nil, nil
	case "down", "ctrl+n":
		q.idx = stepIndex(q.idx, 1, len(rows))
		return nil, nil
	case "enter":
		// Committed, not answered. The answer is the host's reply.
		q.switching = rows[q.idx].ID
		return nil, requestModelSwitchCmd(q.switching)
	}
	// A navigation stroke this picker has no use for. Consumed, as
	// every keystroke into an open modal is, but nothing changes.
	return nil, nil
}

func requestModelSwitchCmd(id string) tea.Cmd {
	return func() tea.Msg { return modelSwitchRequestedMsg{ID: id} }
}

// filtering reports whether the filter row is on screen, which is also
// the condition for the picker owning the terminal caret. Only the
// loaded-list state has one: a loading line, an in-flight switch, an
// unwired agent and a host that advertised nothing are all states with
// nothing to narrow.
func (q *modelPickerQuestion) filtering() bool {
	return q.wired && q.loaded && q.switching == "" && len(q.models) > 0
}

func (q *modelPickerQuestion) Body(width, termHeight int, st styleSet) string {
	switch {
	case !q.wired:
		return st.Muted.Render("agent does not implement ModelSwapper")
	case q.switching != "":
		return st.Muted.Render("switching to " + q.switching + "…")
	case !q.loaded:
		return st.Muted.Render("loading models…")
	case len(q.models) == 0:
		return st.Muted.Render("(no models advertised by the agent)")
	}

	models := q.rows()
	filter := q.filter.value()
	// The filter row is the FIRST body row, always — including when it
	// matched nothing, since it is the thing the operator has to edit to
	// get back. Its row is paid for with modalChromeRows+1 below, and
	// the failure row, which is always the LAST, adds its own.
	lines := []string{q.filter.render(width, len(models), len(q.models), st)}
	if len(models) == 0 {
		lines = append(lines, st.Muted.Render("no models match "+quoteFilter(filter)))
		return q.fail.appendTo(lines, width, st)
	}
	q.idx = clampIndex(q.idx, len(models))
	rows := make([]string, 0, len(models))
	for i, mi := range models {
		disp := mi.Display
		if disp == "" {
			disp = mi.ID
		}
		base := lipgloss.NewStyle()
		marker := "  "
		if i == q.idx {
			marker, base = "> ", st.Accent
		}
		row := base.Render(marker) + highlightSpan(disp, filter, base)
		if mi.ID != disp {
			row += st.Muted.Render("  (") +
				highlightSpan(mi.ID, filter, st.Muted) +
				st.Muted.Render(")")
		}
		if mi.ID == q.current || disp == q.current {
			row += "  " + st.Muted.Render("(current)")
		}
		if mi.Description != "" {
			row += "  " + st.Muted.Render(mi.Description)
		}
		rows = append(rows, row)
	}
	view := modalBodyHeight(termHeight, modalChromeRows+1+q.fail.rows())
	q.off = listWindow(q.off, q.idx, len(rows), view)
	lines = append(lines, scrollView(st, rows, modalBodyWidth(width), view, q.off)...)
	return q.fail.appendTo(lines, width, st)
}

// Cursor implements cursorQuestion. Without it the picker would take
// input with no hardware caret and no IME anchor, which is #105 / #123
// regressed on the surface most likely to see CJK.
func (q *modelPickerQuestion) Cursor(_ int) *tea.Cursor {
	if !q.filtering() {
		return nil
	}
	return filterRowCursor(q.filter.cursor())
}

// applyModelSwitch is the Update-side half of the Enter path, shared
// with `/model <id>`, which has no picker at all: attach the new agent
// (or report the failure), announce it, persist, and re-resolve the
// theme.
//
// Returns the Cmd that runs Options.PersistModelChoice — host code that
// writes the pick to the host's config, so it does not belong on the
// Update goroutine either (issue #137). Nil when unwired; the failure
// row arrives later as persistDoneMsg.
func (m *Model) applyModelSwitch(msg modelSwitchedMsg) tea.Cmd {
	if reason := modelSwitchFailure(msg); reason != "" {
		m.history.Append(Message{Role: RoleError, Text: "/model: " + reason})
		m.refreshViewport()
		return nil
	}
	m.opts.Agent = msg.agent
	m.history.Append(Message{Role: RoleSystem, Text: "/model: switched to " + msg.id})
	// Refresh the theme so per-provider palettes (when AutoProviderTheme
	// is on) track the freshly-selected model's provider. No-op when
	// AutoProviderTheme is off — resolveStyles returns the same
	// defaultTheme.
	m.refreshTheme()
	m.refreshViewport()
	return persistChoiceCmd(m.sessionGen, "/model", m.opts.PersistModelChoice, msg.id)
}

// modelSwitchFailure is why a SwitchModel reply is unusable, or "" when
// it is fine.
//
// One function rather than a condition spelled out at each site,
// because the reason now has two readers that must not drift: the
// transcript row applyModelSwitch appends — which is all `/model <id>`
// gets, and all it needs, since that path has no modal in the way — and
// the inline row the picker shows over the top of it (issue #245). The
// operator on the picker path reads both, so the two saying different
// things about the same failure would be worse than the silence this
// replaced.
func modelSwitchFailure(msg modelSwitchedMsg) string {
	switch {
	case msg.err != nil:
		return "switch failed: " + msg.err.Error()
	case msg.agent == nil:
		return "ModelSwapper returned nil Agent"
	}
	return ""
}
