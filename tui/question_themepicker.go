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

// The theme picker, as a question (issue #164, stage 1).
//
// It is the conversion the plan picked to go first: it is the
// smallest of the modals, it had the most effects per line of any of
// them — six of them inside a list widget's Enter arm — and its
// host-visible message has no consumer, so a mistake here is cheap.
//
// What moved. The widget below decides one thing, "which row", and
// says so by returning chosen{}. Applying the theme, appending the
// transcript row, announcing ThemeChangedMsg and scheduling the
// host's persistence callback are all in themePickerResolver, next to
// the rule they are the counterpart of: esc puts back the theme that
// was live when the picker opened. Those two rules were sixty lines
// apart before and are now adjacent.
//
// The live preview is the one thing that could not simply move. It is
// an effect the widget has to cause BEFORE it has an answer, and the
// widget has no *Model to cause it with — Model.Update has a value
// receiver, so any *Model the picker held would be a dead per-Update
// copy by the second keystroke. So cursor movement returns a
// themePreviewMsg for the Update loop to apply. See its declaration
// for why that is guarded rather than applied blind.

package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const themePickerDialogID = "theme-picker"

// themePickerWidth is the picker's preferred total width, clamped
// down to whatever the terminal can spare.
const themePickerWidth = 72

// themePreviewMsg applies a theme the operator has moved the cursor
// onto but not committed. Package-private and never surfaced to a
// host: the ThemeChangedMsg a host observes is still emitted once, on
// commit, by the resolver.
//
// Update applies it only while the picker is still open. The message
// is delivered asynchronously, so an operator who arrows and then
// immediately escapes can have the preview land AFTER the resolver
// has already put the original theme back — and without the guard
// that stale preview would win, leaving the terminal in a theme the
// operator explicitly cancelled.
type themePreviewMsg struct{ Name string }

// themePickerQuestion renders a theme list with a cursor, a
// "(current)" marker and a type-to-filter row, and answers with the
// row the operator committed.
//
// The list is a constructor argument rather than a BuiltinThemes()
// call inside the widget. That is what makes the question drivable
// from an external test, and it is also what fixes a smaller thing:
// the predecessor read the registry separately from HandleKey and
// from Render, so the two could in principle disagree about what row
// the cursor was on.
type themePickerQuestion struct {
	themes []BuiltinTheme
	// current is the theme that was live when the picker opened. It
	// is what "(current)" marks and what the resolver restores on
	// esc; it deliberately does not follow the preview, so the row
	// the operator started on stays findable while they walk the
	// list.
	current string

	idx int
	// off is the first visible row: the builtin registry already
	// outgrows a short terminal, so the list windows around the
	// cursor rather than painting past the bottom edge.
	off int

	filter pickerFilter
}

// newThemePickerQuestion focuses the row matching current so the
// operator's first arrow-press visibly moves off the active row
// rather than landing on whatever happens to be entry 0.
func newThemePickerQuestion(themes []BuiltinTheme, current string) *themePickerQuestion {
	idx := 0
	for i, bt := range themes {
		if strings.EqualFold(bt.Name, current) {
			idx = i
			break
		}
	}
	return &themePickerQuestion{
		themes:  themes,
		current: current,
		idx:     idx,
		filter:  newPickerFilter(),
	}
}

// themePickerResolver is the other half of the picker: everything the
// widget used to do to a *Model once it knew which row won.
//
// prev is the theme that was live when the picker opened.
// applyNamedTheme tolerates an empty string — it falls back to the
// auto / per-provider path, which is exactly right when the operator
// never had an explicit pick before opening the picker.
func themePickerResolver(prev string) resolver {
	return func(a answer, m *Model) tea.Cmd {
		switch a := a.(type) {
		case chosen:
			m.applyNamedTheme(a.ID)
			m.history.Append(Message{Role: RoleSystem, Text: "/theme: switched to " + a.ID})
			m.refreshViewport()
			// Both notification paths fire: hosts can use the
			// ThemeChangedMsg observation OR the persistence
			// callback, or both. The callback runs off the Update
			// goroutine because it is host code writing to the host's
			// config (#137); a failure lands later as persistDoneMsg
			// and does not block the switch.
			return tea.Batch(
				func() tea.Msg { return ThemeChangedMsg{Name: a.ID} },
				persistChoiceCmd(m.sessionGen, "/theme", m.opts.PersistThemeChoice, a.ID),
			)
		case dismissed:
			// Undo whatever the live preview applied on the way here.
			m.applyNamedTheme(prev)
			return nil
		case declined, selected, text, fields, decision:
			// Shapes a single-select list cannot produce. Named
			// rather than defaulted so that a variant added to the
			// sealed set is a lint failure here instead of silently
			// routing to "do nothing".
			return nil
		}
		return nil
	}
}

func (q *themePickerQuestion) ID() string { return themePickerDialogID }

func (q *themePickerQuestion) Title() string { return "Choose a Theme" }

func (q *themePickerQuestion) Footer() string {
	return keyLegend("type to filter", "↑↓ preview", "enter accept", "esc cancel")
}

func (q *themePickerQuestion) Width(avail int) int {
	width := themePickerWidth
	if avail > 0 && width > avail-4 {
		width = avail - 4
	}
	return max(width, 30)
}

// rows returns the registry narrowed by the filter row, ranked by the
// shared classifier (rank.go).
func (q *themePickerQuestion) rows() []BuiltinTheme {
	filter := q.filter.value()
	if filter == "" {
		return q.themes
	}
	keys := make([]string, len(q.themes))
	for i, bt := range q.themes {
		keys[i] = bt.Name
	}
	idx := rankNames(keys, filter)
	out := make([]BuiltinTheme, len(idx))
	for i, at := range idx {
		out[i] = q.themes[at]
	}
	return out
}

// Key drives the list on navigation strokes and feeds everything else
// to the filter.
//
// Filtering deliberately does NOT preview. The live preview is bound
// to cursor MOVEMENT — which is what the "↑↓ preview" footer promises
// — and repainting the whole chat in a new palette on every keystroke
// of a filter would be a strobe, not a preview. Enter still applies
// whatever row is highlighted, so no theme becomes unreachable.
func (q *themePickerQuestion) Key(msg tea.KeyPressMsg) (answer, tea.Cmd) {
	stroke := msg.String()
	if stroke == "esc" {
		return dismissed{Reason: dismissEscape}, nil
	}
	if len(q.themes) == 0 {
		// Nothing to ask about. Unrenderable rather than escape: the
		// operator did not decline anything, the question could not
		// be put to them at all.
		return dismissed{Reason: dismissUnrenderable}, nil
	}
	if !pickerNavStroke(stroke) {
		cmd, changed := q.filter.handleKeyMsg(msg)
		if changed {
			q.idx, q.off = 0, 0
		}
		return nil, cmd
	}

	rows := q.rows()
	if len(rows) == 0 {
		// The filter matched nothing; stay open so it can be edited.
		return nil, nil
	}
	q.idx = clampIndex(q.idx, len(rows))
	switch stroke {
	case "up", "ctrl+p":
		q.idx = stepIndex(q.idx, -1, len(rows))
		return nil, previewThemeCmd(rows[q.idx].Name)
	case "down", "ctrl+n":
		q.idx = stepIndex(q.idx, 1, len(rows))
		return nil, previewThemeCmd(rows[q.idx].Name)
	case "enter":
		return chosen{ID: rows[q.idx].Name, Index: q.idx}, nil
	}
	// A navigation stroke this picker has no use for. Consumed, as
	// every keystroke into an open modal is, but nothing changes.
	return nil, nil
}

func previewThemeCmd(name string) tea.Cmd {
	return func() tea.Msg { return themePreviewMsg{Name: name} }
}

func (q *themePickerQuestion) Body(width, termHeight int, st styleSet) string {
	if len(q.themes) == 0 {
		return st.Muted.Render("(no themes registered)")
	}
	rows := q.rows()
	filter := q.filter.value()
	// The filter row is the first body row; its cost is the +1 on
	// modalChromeRows below.
	lines := []string{q.filter.render(width, len(rows), len(q.themes), st)}
	if len(rows) == 0 {
		lines = append(lines, st.Muted.Render("no themes match "+quoteFilter(filter)))
		return strings.Join(lines, "\n")
	}
	q.idx = clampIndex(q.idx, len(rows))
	body := make([]string, 0, len(rows))
	for i, bt := range rows {
		base := lipgloss.NewStyle()
		marker := "  "
		if i == q.idx {
			marker, base = "> ", st.Accent
		}
		row := base.Render(marker) + highlightSpan(bt.Name, filter, base)
		if strings.EqualFold(bt.Name, q.current) {
			row += "  " + st.Muted.Render("(current)")
		}
		if bt.Description != "" {
			row += "  " + st.Muted.Render(bt.Description)
		}
		body = append(body, row)
	}
	view := modalBodyHeight(termHeight, modalChromeRows+1)
	q.off = listWindow(q.off, q.idx, len(body), view)
	lines = append(lines, scrollView(st, body, modalBodyWidth(width), view, q.off)...)
	return strings.Join(lines, "\n")
}

// Cursor implements cursorQuestion so the filter row owns the
// terminal caret. Nil only when the registry is empty — every other
// state of this picker renders the filter row.
func (q *themePickerQuestion) Cursor(_ int) *tea.Cursor {
	if len(q.themes) == 0 {
		return nil
	}
	return filterRowCursor(q.filter.cursor())
}
