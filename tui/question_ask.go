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

// The agent's question, as a question (R-PROMPT-1, issue #255).
//
// Three surfaces cover the five AskKinds: a list (single-select,
// multi-select and the yes/no row, which is a two-option list drawn
// side by side), a one-line input, and the editor-backed long form in
// question_ask_editor.go. They share the seam, the grace window, the
// esc cascade and the teardown with every other question, so what is
// here is only the parts that differ — which is the whole argument
// §7.7 of the design makes about what a "shared base" is worth.
//
// Everything the answer MEANS lives in askResolver, next to the flow
// it writes back to. The widgets below decide "which row" and "what
// text" and nothing else; none of them has ever seen a *Model.

package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const askDialogID = "ask"

// askModalWidth is the preferred total width, before the clamp to
// whatever the terminal can spare. Matches the elicit form: both are
// agent-opened modals carrying a sentence of prose, and two different
// widths for that would read as two different mechanisms.
const askModalWidth = 72

// confirmYesID / confirmNoID are the ChoiceIDs an AskConfirm answers
// with. Fixed rather than taken from the request: a host that wants
// its own IDs has AskChoice, and a yes/no whose "yes" is spelled
// differently per caller is a yes/no every caller has to unpack.
const (
	confirmYesID = "yes"
	confirmNoID  = "no"
)

// askSurface is the shape the three ask questions share beyond
// question itself: each can say what its own keys are, so the footer
// hint in view.go cannot promise a key the open surface does not take.
//
// It is not an extension point. It exists because openAsk has three
// possible concrete types and the one thing every caller of it wants
// is the legend.
type askSurface interface {
	question
	legend() string
}

// newAskQuestion builds the surface for req's kind. Only ever called
// for a request supportedAsk has already passed, which is what lets
// the default arm be a nil rather than a fallback modal: a kind with
// no surface is refused before it gets here, and inventing a shape for
// an unknown one is how a host learns a request "worked" when nobody
// saw it.
func newAskQuestion(req AskRequest) askSurface {
	switch req.Kind {
	case AskChoice, AskMultiChoice, AskConfirm:
		return newAskListQuestion(req)
	case AskText:
		return newAskTextQuestion(req)
	case AskLongText:
		return newAskEditorQuestion(req)
	}
	return nil
}

// askResolver writes the operator's answer back to the ask flow that
// is parked on it.
//
// Whether it re-arms the listener follows elicitResolver exactly, and
// for the same reason: an answer frees the flow, so the next request
// should be waited for; a dismissal that came from a session switch or
// from shutdown must NOT re-arm, because applySwitchTarget's step 8
// already returns a fresh listener and two armed listeners on one
// channel are two consumers taking alternate requests.
func askResolver(a answer, m *Model) tea.Cmd {
	switch a := a.(type) {
	case chosen:
		m.dispatchAsk(AskResult{Action: AskAnswered, ChoiceIDs: []string{a.ID}})
		return m.askListener()
	case selected:
		// A multi-select with nothing ticked is an answer, not a
		// decline: the operator read the list and chose none of it, and
		// an agent asking "which of these apply" can act on that.
		// ChoiceIDs is len 0 and Action is still AskAnswered.
		m.dispatchAsk(AskResult{Action: AskAnswered, ChoiceIDs: a.IDs})
		return m.askListener()
	case text:
		m.dispatchAsk(AskResult{Action: AskAnswered, Text: a.Value})
		return m.askListener()
	case declined:
		// ctrl+d. Distinct from the esc below, and distinct from a "no"
		// on a confirm, which arrives above as chosen{ID: "no"}.
		m.dispatchAsk(AskResult{Action: AskDeclined})
		return m.askListener()
	case dismissed:
		m.dispatchAsk(AskResult{Action: AskCancelled})
		switch a.Reason {
		case dismissEscape, dismissUnrenderable:
			return m.askListener()
		case dismissSuperseded, dismissShutdown:
			return nil
		}
		return nil
	case fields, decision:
		// Shapes no ask surface produces. Named rather than defaulted so
		// that a variant added to the sealed set is a lint failure here
		// instead of silently routing to "do nothing".
		return nil
	}
	return nil
}

// openAsk returns the ask question currently on the overlay stack, or
// nil when none is open. Same job, and the same caveat, as openElicit:
// it is for LOOKING at the open question — the footer legend, the
// editor round trip, the tests that drive one to completion. Ending
// the question goes through overlay.resolve so the exactly-once latch
// cannot be sidestepped.
func (m *Model) openAsk() askSurface {
	aq := m.overlayStack.asked(askDialogID)
	if aq == nil {
		return nil
	}
	q, _ := aq.q.(askSurface)
	return q
}

// askTitle is the modal header for a request, with the sub-agent name
// prefixed when one asked. Shared by all three surfaces so the header
// does not depend on which shape the agent picked.
func askTitle(req AskRequest) string {
	title := req.Title
	if title == "" {
		title = "Agent question"
	}
	if req.Source != "" {
		title = req.Source + " " + GlyphSeparator + " " + title
	}
	return title
}

// askWidth clamps the preferred width to the terminal, with the same
// floor every other modal uses.
func askWidth(avail int) int {
	width := askModalWidth
	if avail > 0 && width > avail-4 {
		width = avail - 4
	}
	return max(width, 30)
}

// promptLines renders the question's own prose above the answer
// widget, wrapped to the body width, followed by a blank row. Empty
// when the request carries no prompt — an agent that put its question
// in the title gets one blank row less, not an empty one.
func promptLines(prompt string, width int, st styleSet) []string {
	if strings.TrimSpace(prompt) == "" {
		return nil
	}
	wrapped := lipgloss.NewStyle().Width(nonNeg(width)).Render(prompt)
	return append(strings.Split(st.AssistantText.Render(wrapped), "\n"), "")
}

// askListQuestion is the single-select, the multi-select and the
// yes/no, which differ in three places: whether space toggles, whether
// the rows draw down the page or across it, and what the answer is
// called.
//
// Holding one type for the three is not economy for its own sake — it
// is what keeps "enter commits the row under the cursor" one
// implementation. The alternative was three widgets whose cursor
// arithmetic agreed by convention, which is the shape stage 1 of #164
// was written to get rid of.
type askListQuestion struct {
	req  AskRequest
	rows []AskOption

	idx int
	// off is the first visible row. A host can hand over a list longer
	// than the terminal, and a choice below the fold is a choice the
	// operator cannot make.
	off int

	// marked is the ticked set for AskMultiChoice, keyed by option ID.
	// Nil for the other two kinds, which is also what makes a stray
	// space keystroke a no-op there rather than a panic.
	marked map[string]bool
}

// newAskListQuestion seeds the rows. AskConfirm synthesizes its own
// two, so the request's Choices are ignored for that kind rather than
// merged: a confirm whose labels came from the agent is an AskChoice
// with two options, and that kind already exists.
func newAskListQuestion(req AskRequest) *askListQuestion {
	q := &askListQuestion{req: req, rows: req.Choices}
	if req.Kind == AskConfirm {
		q.rows = []AskOption{{ID: confirmYesID, Label: "Yes"}, {ID: confirmNoID, Label: "No"}}
	}
	if req.Kind == AskMultiChoice {
		q.marked = make(map[string]bool, len(q.rows))
	}
	return q
}

func (q *askListQuestion) ID() string { return askDialogID }

func (q *askListQuestion) Title() string { return askTitle(q.req) }

func (q *askListQuestion) legend() string {
	switch q.req.Kind {
	case AskConfirm:
		return keyLegend("←/→ move", "y yes", "n no", "enter accept",
			"ctrl+d decline", "esc cancel")
	case AskMultiChoice:
		return keyLegend("↑↓ move", "space toggle", "enter submit",
			"ctrl+d decline", "esc cancel")
	case AskChoice, AskText, AskLongText:
	}
	return keyLegend("↑↓ move", "enter accept", "ctrl+d decline", "esc cancel")
}

func (q *askListQuestion) Footer() string { return q.legend() }

func (q *askListQuestion) Width(avail int) int { return askWidth(avail) }

// Commits implements gracedQuestion: the keys that send something back
// to the agent, and therefore the only ones the grace window holds.
// Moving the cursor and ticking a box stay live throughout — both are
// visible and reversible, and nothing leaves the TUI until one of
// these.
func (q *askListQuestion) Commits(msg tea.KeyPressMsg) bool {
	switch msg.String() {
	case "enter", "ctrl+d":
		return true
	case "y", "n":
		// The confirm's accelerators. On the other two kinds they are
		// ordinary keystrokes that do nothing, and holding those would
		// widen the window past what it is for.
		return q.req.Kind == AskConfirm
	}
	return false
}

// Key drives the cursor and the commit keys. Everything else is
// swallowed, as every keystroke into an open modal is.
func (q *askListQuestion) Key(msg tea.KeyPressMsg) (answer, tea.Cmd) {
	stroke := msg.String()
	switch stroke {
	case "esc":
		return dismissed{Reason: dismissEscape}, nil
	case "ctrl+d":
		return declined{}, nil
	}
	if len(q.rows) == 0 {
		// Nothing to ask about. supportedAsk screens this shape out
		// before the question is opened, so this arm is what keeps that
		// a policy rather than a load-bearing invariant — and it is
		// unrenderable rather than escape, because the operator did not
		// decline anything.
		return dismissed{Reason: dismissUnrenderable}, nil
	}
	q.idx = clampIndex(q.idx, len(q.rows))

	if q.req.Kind == AskConfirm {
		switch stroke {
		case "y":
			return chosen{ID: confirmYesID, Index: 0}, nil
		case "n":
			return chosen{ID: confirmNoID, Index: 1}, nil
		case "left", "right", "tab", "shift+tab":
			// One step either way over two options is the same move, so
			// every navigation key is spelled once here rather than
			// mapped to a direction that cannot matter.
			q.idx = stepIndex(q.idx, 1, len(q.rows))
			return nil, nil
		}
	}

	switch stroke {
	case "up", "ctrl+p":
		q.idx = stepIndex(q.idx, -1, len(q.rows))
	case "down", "ctrl+n":
		q.idx = stepIndex(q.idx, 1, len(q.rows))
	case "home":
		q.idx = 0
	case "end":
		q.idx = len(q.rows) - 1
	case "space":
		if q.req.Kind == AskMultiChoice {
			id := q.rows[q.idx].ID
			if q.marked[id] {
				delete(q.marked, id)
			} else {
				q.marked[id] = true
			}
		}
	case "enter":
		if q.req.Kind == AskMultiChoice {
			return q.selection(), nil
		}
		return chosen{ID: q.rows[q.idx].ID, Index: q.idx}, nil
	}
	return nil, nil
}

// selection collects the ticked rows in LIST order rather than in
// click order, because the list order is the only one both sides can
// see. Indexes is filled beside IDs for the same reason chosen carries
// one: a resolver that already tracks positions should not have to
// re-derive them.
func (q *askListQuestion) selection() selected {
	out := selected{}
	for i, row := range q.rows {
		if q.marked[row.ID] {
			out.IDs = append(out.IDs, row.ID)
			out.Indexes = append(out.Indexes, i)
		}
	}
	return out
}

// Body renders the prompt, then the rows — across the page for a
// confirm, down it for the two lists.
func (q *askListQuestion) Body(width, termHeight int, st styleSet) string {
	body := modalBodyWidth(width)
	lines := promptLines(q.req.Prompt, body, st)

	if len(q.rows) == 0 {
		return strings.Join(append(lines, st.Muted.Render("(no choices offered)")), "\n")
	}
	q.idx = clampIndex(q.idx, len(q.rows))

	if q.req.Kind == AskConfirm {
		return strings.Join(append(lines, q.confirmRow(st)), "\n")
	}

	rows := make([]string, 0, len(q.rows))
	for i, opt := range q.rows {
		rows = append(rows, q.renderRow(opt, i == q.idx, st))
	}
	// The prompt rows are body content rather than chrome, so they come
	// out of the same budget the list windows against — otherwise a long
	// prompt would push the last choices off the bottom edge with the
	// scrollbar reporting that everything fit.
	view := modalBodyHeight(termHeight, modalChromeRows+len(lines))
	q.off = listWindow(q.off, q.idx, len(rows), view)
	return strings.Join(append(lines, scrollView(st, rows, body, view, q.off)...), "\n")
}

// renderRow draws one list row: cursor marker, tick box for a
// multi-select, label, and the dim description trailing it.
func (q *askListQuestion) renderRow(opt AskOption, focused bool, st styleSet) string {
	base := lipgloss.NewStyle()
	marker := "  "
	if focused {
		marker, base = "> ", st.Accent
	}
	row := base.Render(marker)
	if q.req.Kind == AskMultiChoice {
		box := "[ ] "
		if q.marked[opt.ID] {
			box = "[✓] "
		}
		row += base.Render(box)
	}
	row += base.Render(askLabel(opt))
	if opt.Description != "" {
		row += "  " + st.Muted.Render(opt.Description)
	}
	return row
}

// confirmRow draws the two options side by side, which is what makes a
// yes/no read as one decision rather than as a list that happens to
// have two entries.
func (q *askListQuestion) confirmRow(st styleSet) string {
	cells := make([]string, 0, len(q.rows))
	for i, opt := range q.rows {
		label := "  " + askLabel(opt) + "  "
		if i == q.idx {
			cells = append(cells, st.Accent.Render("["+label+"]"))
			continue
		}
		cells = append(cells, st.Muted.Render(" "+label+" "))
	}
	return "  " + strings.Join(cells, "  ")
}

// askLabel is what a row is called: the host's Label, falling back to
// the ID. An option with neither is the agent's mistake, and rendering
// a blank row would hide it — the ID at least names what the answer
// will say.
func askLabel(opt AskOption) string {
	if opt.Label != "" {
		return opt.Label
	}
	return opt.ID
}

var (
	_ question       = (*askListQuestion)(nil)
	_ askSurface     = (*askListQuestion)(nil)
	_ gracedQuestion = (*askListQuestion)(nil)
)
