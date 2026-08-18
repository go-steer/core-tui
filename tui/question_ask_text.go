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

// AskText — the one-line answer (R-PROMPT-1, issue #255).
//
// It is a bubbles textinput in the modal chrome, which is what
// textInputDialog already is (dialog_textinput.go, issue #56). This is
// not that widget with a resolver bolted on, and the reason is
// §7.4-versus-§13 of the design: the text input stayed a dialog
// because its one in-repo caller needs a Submit that returns a Cmd and
// keeps the modal open while a host dial is in flight. An agent's
// question has neither property — it commits once and closes — so
// wrapping the dialog would mean carrying its Submit / Validate /
// in-flight machinery to reach a text box and a caret.
//
// What IS shared is the part worth sharing: textInputStyles, so the
// two typed surfaces cannot drift apart, and textInputCursor, so the
// caret lands where bubbles' own Cursor() does not (issue #125).

package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// askTextQuestion is one free-text value, answered with enter.
type askTextQuestion struct {
	req   AskRequest
	input textinput.Model

	// styled* caches which palette the input's styles were built for,
	// so Body rebuilds them on an actual theme flip rather than every
	// frame. Same guard, same reason, as textInputDialog.syncStyles —
	// /theme can swap the palette while this modal is open.
	styled      bool
	styledDark  bool
	styledTheme string
}

func newAskTextQuestion(req AskRequest) *askTextQuestion {
	ti := textinput.New()
	// The question's own prompt renders as a body line above the box,
	// so the widget's inline prompt would be a second marker. A thin
	// rail instead, marking where typing lands.
	ti.Prompt = "▎ "
	ti.Placeholder = req.Placeholder
	if req.Initial != "" {
		ti.SetValue(req.Initial)
		ti.CursorEnd()
	}
	// Real cursor, not a painted block (issue #105).
	ti.SetVirtualCursor(false)
	_ = ti.Focus()
	return &askTextQuestion{req: req, input: ti}
}

func (q *askTextQuestion) ID() string { return askDialogID }

func (q *askTextQuestion) Title() string { return askTitle(q.req) }

func (q *askTextQuestion) legend() string {
	return keyLegend("enter submit", "ctrl+d decline", "esc cancel")
}

func (q *askTextQuestion) Footer() string { return q.legend() }

func (q *askTextQuestion) Width(avail int) int { return askWidth(avail) }

// Commits implements gracedQuestion. Typing stays live inside the
// window and only the two keys that send something back are held —
// which is the same split the elicit form draws, and for the same
// reason: a window that swallowed the operator's typing would trade
// the grant it protects for the sentence they were writing.
func (q *askTextQuestion) Commits(msg tea.KeyPressMsg) bool {
	switch msg.String() {
	case "enter", "ctrl+d":
		return true
	}
	return false
}

func (q *askTextQuestion) Key(msg tea.KeyPressMsg) (answer, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return dismissed{Reason: dismissEscape}, nil
	case "ctrl+d":
		return declined{}, nil
	case "enter":
		// Trimmed here rather than in the resolver: `text` is documented
		// as already trimmed, and an empty answer is still an answer —
		// an agent asking "anything else?" can act on "no".
		return text{Value: strings.TrimSpace(q.input.Value())}, nil
	}
	var cmd tea.Cmd
	q.input, cmd = q.input.Update(msg)
	return nil, cmd
}

func (q *askTextQuestion) Body(width, _ int, st styleSet) string {
	q.syncStyles(st)
	q.input.SetWidth(nonNeg(modalInnerWidth(width) - lipgloss.Width(q.input.Prompt)))
	// modalInnerWidth, not modalBodyWidth: this body never windows, so
	// there is no scrollbar column to leave room for.
	lines := promptLines(q.req.Prompt, modalInnerWidth(width), st)
	return strings.Join(append(lines, q.input.View()), "\n")
}

// Cursor implements cursorQuestion. The box sits under however many
// rows the prompt wrapped to, which is re-derived from promptLines
// rather than remembered from Body — the same rule the elicit form's
// caret follows, and for the same reason: a second copy of the
// arithmetic is how the caret and the renderer drift apart. The zero
// styleSet is safe because only the row COUNT is read and no style can
// introduce a newline.
func (q *askTextQuestion) Cursor(width int) *tea.Cursor {
	c := textInputCursor(q.input, q.input.Prompt)
	if c == nil {
		return nil // blurred
	}
	c.X += modalContentX
	c.Y += modalBodyTop + len(promptLines(q.req.Prompt, modalInnerWidth(width), styleSet{}))
	return c
}

func (q *askTextQuestion) syncStyles(s styleSet) {
	if q.styled && q.styledDark == s.Dark && q.styledTheme == s.Theme.Name {
		return
	}
	q.styled = true
	q.styledDark = s.Dark
	q.styledTheme = s.Theme.Name
	q.input.SetStyles(textInputStyles(s))
}

var (
	_ question       = (*askTextQuestion)(nil)
	_ askSurface     = (*askTextQuestion)(nil)
	_ cursorQuestion = (*askTextQuestion)(nil)
	_ gracedQuestion = (*askTextQuestion)(nil)
)
