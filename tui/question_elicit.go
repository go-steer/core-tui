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

// The MCP elicitation form, as a question (issue #164, stage 3).
//
// This is the first of the two surfaces that were never Dialogs at
// all. The form lived as five fields on Model — the request, the
// server name, the focused field index, the in-progress values and
// the grace stamp — read by a key handler in update.go, a renderer in
// view.go and a caret path in cursor.go, none of which could see the
// other two. The rule "Tab wraps at the last field" was in one file,
// "a focused row is drawn with a > marker instead of two spaces" in
// the second, and "the caret sits at the end of the value on that
// same row" in the third; the three agreed only because someone
// re-derived the arithmetic each time.
//
// They are one type here, and the three now share the state they are
// about rather than a convention. The effects — writing the result
// back to the elicitor and re-arming its listener — are in
// elicitResolver, which is the only part of this file that has ever
// seen a *Model.
//
// Two behaviours this stage brings with it, both described in
// docs/design-question-dialogs.md:
//
//   - The grace window (§9) moves out of the key handler and onto the
//     seam, as askOrigin plus the gracedQuestion extension. The form
//     is the second modal to want it and the last one that would have
//     open-coded it.
//
//   - Scrolling (§7.7) gets the scrollQuestion extension the stage-1
//     comment in question.go promised would arrive with its first
//     implementor. A form with more fields than the terminal has rows
//     is that implementor.

package tui

import (
	"fmt"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

const elicitDialogID = "elicit"

// elicitModalWidth is the form's preferred total width, before the
// clamp to whatever the terminal can spare.
const elicitModalWidth = 72

// elicitQuestion is an MCP elicitation — a per-field form, or a URL
// the server wants the operator to accept (R-ELIC-1 / R-ELIC-2).
//
// The request is held by value. It arrived over a channel from the
// host's Elicit call and nothing else may write to it, so a copy is
// the cheapest way to say so; the predecessor held a *ElicitRequest
// and used nil-ness of that pointer as "is the modal open", which is
// a job the overlay stack already does.
type elicitQuestion struct {
	req    ElicitRequest
	server string

	// idx is the focused field, moved by Tab / Shift+Tab. Meaningless
	// in URL mode, which has no fields.
	idx int

	// values is what will be sent on submit, seeded from each field's
	// Default. Go primitives only — it goes to the host verbatim as
	// ElicitResult.Values.
	values map[string]any

	// sc windows the body. Owned here rather than borrowed from
	// Model.modalScroll: the caret path has to subtract the offset the
	// render just settled on, and a question that reaches back to the
	// app model for it is exactly the coupling this stage removes.
	sc scrollState
}

// newElicitQuestion seeds the form from the request's defaults, so a
// server that proposes an answer gets it shown rather than merely
// implied.
func newElicitQuestion(serverName string, req ElicitRequest) *elicitQuestion {
	values := make(map[string]any, len(req.Fields))
	for _, f := range req.Fields {
		if f.Default != nil {
			values[f.Name] = f.Default
		}
	}
	return &elicitQuestion{req: req, server: serverName, values: values}
}

// elicitResolver writes the operator's answer back to the elicitor
// flow that is parked on it.
//
// Whether it re-arms the listener is the one decision here worth
// stating. An answer the operator gave means the flow is free and the
// next request should be waited for, so the listener is re-armed. A
// dismissal that came from the session switch or from shutdown must
// NOT re-arm: applySwitchTarget's step 8 returns a fresh
// m.elicitListener() for the new session, and a second armed listener
// on the same channel is a second consumer — one of the two would
// take a request and the other would take the next one, so requests
// would land in the modal on alternate turns.
func elicitResolver(a answer, m *Model) tea.Cmd {
	switch a := a.(type) {
	case fields:
		// Both submits arrive here. A form sends its values; URL mode
		// accepts with none, which is the same ElicitResult the host
		// got before and the reason accept is not a variant of its
		// own — "the operator said yes" is one shape whether or not
		// there was anything to fill in.
		m.dispatchElicit(ElicitResult{Action: ElicitActionSubmit, Values: a.Values})
		return m.elicitListener()
	case declined:
		// ctrl+d in a form, "n" in URL mode. Distinct from the esc
		// below: the operator read the request and answered no, and
		// MCP forwards decline and cancel differently (issue #209).
		m.dispatchElicit(ElicitResult{Action: ElicitActionDecline})
		return m.elicitListener()
	case dismissed:
		m.dispatchElicit(ElicitResult{Action: ElicitActionCancel})
		switch a.Reason {
		case dismissEscape, dismissUnrenderable:
			return m.elicitListener()
		case dismissSuperseded, dismissShutdown:
			return nil
		}
		return nil
	case chosen, selected, text, decision:
		// Shapes a form cannot produce. Named rather than defaulted so
		// that a variant added to the sealed set is a lint failure
		// here instead of silently routing to "do nothing".
		return nil
	}
	return nil
}

func (q *elicitQuestion) ID() string { return elicitDialogID }

func (q *elicitQuestion) Title() string {
	title := q.req.Title
	if title == "" {
		title = "MCP request"
	}
	if q.server != "" {
		title = q.server + " " + GlyphSeparator + " " + title
	}
	return title
}

// legend is the footer without the scroll hint — the strings the form
// is promising, independent of whether it currently overflows.
//
// Body measures its row budget against this rather than against
// Footer, because the hint is decided by that measurement: budgeting
// against a footer that grows once the body overflows would let a
// form flip between overflowing and not on alternate frames.
func (q *elicitQuestion) legend() string {
	if q.req.Mode == ElicitURLMode {
		return keyLegend("a / enter accept", "n decline", "esc cancel")
	}
	return keyLegend(
		"tab next", "shift+tab prev", "space toggle", "←/→ enum",
		"enter submit", "ctrl+d decline", "esc cancel")
}

// Footer prefixes the scroll hint when the body is windowed.
//
// It reads sc, which Body writes — and that is safe only because
// askedQuestion.Render puts Body before Footer inside one composite
// literal, where the Go spec orders function calls lexically
// left-to-right. The comment on that literal says the same thing from
// the other side; both are load-bearing.
func (q *elicitQuestion) Footer() string {
	if q.sc.overflows() {
		return scrollHint(true) + " " + GlyphSeparator + " " + q.legend()
	}
	return q.legend()
}

func (q *elicitQuestion) Width(avail int) int {
	width := elicitModalWidth
	if avail > 0 && width > avail-4 {
		width = avail - 4
	}
	return max(width, 30)
}

// Commits implements gracedQuestion: the keys that send a result back
// to the host, and therefore the only ones the grace window has any
// reason to hold. Field editing and navigation stay live throughout —
// they are visible, reversible, and nothing leaves the TUI until one
// of these.
func (q *elicitQuestion) Commits(msg tea.KeyPressMsg) bool {
	switch msg.String() {
	case "enter":
		return true
	case "a", "n":
		// URL mode's accept and decline. In a form both are ordinary
		// printable input belonging to whichever field has the
		// keyboard, which is also why form-mode decline is ctrl+d.
		return q.req.Mode == ElicitURLMode
	case "ctrl+d":
		return q.req.Mode != ElicitURLMode
	}
	return false
}

// ScrollBy implements scrollQuestion so the wheel moves the field
// list by lines. Unlike the pickers, this body is text rather than a
// selection, so three rows per tick is the right amount — there is no
// cursor to overshoot.
func (q *elicitQuestion) ScrollBy(delta int) { q.sc.by(delta) }

// Key runs the form's state machine: arrow / page keys window the
// body, Tab moves the focus, and everything else is routed by field
// type.
func (q *elicitQuestion) Key(msg tea.KeyPressMsg) (answer, tea.Cmd) {
	stroke := msg.String()
	if stroke == "esc" {
		return dismissed{Reason: dismissEscape}, nil
	}
	// Scroll first. A form with more fields than the terminal has
	// rows was unreachable below the fold, and none of the bindings
	// below want these keys: field nav is Tab / Shift+Tab, enum
	// cycling is ←/→, and printable input is single-rune.
	if q.sc.applyStroke(stroke) {
		return nil, nil
	}
	if q.req.Mode == ElicitURLMode {
		switch stroke {
		case "a", "enter":
			return fields{}, nil
		case "n":
			return declined{}, nil
		}
		// "o" would open the URL in a browser — deferred to a later
		// slice (we don't depend on os/exec yet). Everything else is
		// swallowed, as every keystroke into an open modal is.
		return nil, nil
	}
	return q.formKey(stroke)
}

// formKey is Key's form-mode half. Split out only because the two
// modes share nothing below the scroll arm and reading one should not
// mean stepping over the other.
func (q *elicitQuestion) formKey(stroke string) (answer, tea.Cmd) {
	if len(q.req.Fields) == 0 {
		// A form with no fields cannot be filled in or focused, so
		// every arm below would index out of range. It is also not
		// something the operator declined: supportedElicit screens
		// this shape out before the question is ever asked, and this
		// arm is what keeps that a policy rather than a load-bearing
		// invariant.
		if stroke == "enter" {
			return fields{}, nil
		}
		return nil, nil
	}
	switch stroke {
	case "enter":
		// Required fields are validated at submit time rather than as
		// they are typed: a form that refuses to move the focus off an
		// empty required field is a form the operator cannot read.
		for i, f := range q.req.Fields {
			if !f.Required {
				continue
			}
			if v, ok := q.values[f.Name]; !ok || isElicitEmpty(v) {
				// Land on the missing field instead of submitting, so
				// the refusal points at its own reason.
				q.idx = i
				return nil, nil
			}
		}
		return fields{Values: q.values}, nil
	case "ctrl+d":
		// Decline, not cancel: the operator read the request and
		// answered no. No validation runs — a decline is a complete
		// answer whatever the fields hold — and the values are dropped
		// rather than sent, because nothing in them was agreed to.
		return declined{}, nil
	case "tab":
		q.idx = (q.idx + 1) % len(q.req.Fields)
		return nil, nil
	case "shift+tab":
		q.idx = (q.idx - 1 + len(q.req.Fields)) % len(q.req.Fields)
		return nil, nil
	case "space":
		f := q.req.Fields[q.idx]
		if f.Type == ElicitFieldBoolean {
			cur, _ := q.values[f.Name].(bool)
			q.values[f.Name] = !cur
			return nil, nil
		}
		// In a typed field a space is a space. The stroke normalizes
		// to "space", so without this rewrite it fell past the
		// printable append below — five runes, not one — and a form
		// asking for a full name could not take one. The predecessor
		// had the same hole; its test fed the handler a raw " ", which
		// nothing in the key path ever produces.
		stroke = " "
	case "left", "right":
		f := q.req.Fields[q.idx]
		if f.Type == ElicitFieldEnum && len(f.EnumChoices) > 0 {
			delta := 1
			if stroke == "left" {
				delta = -1
			}
			idx := indexOfEnum(f.EnumChoices, q.values[f.Name])
			idx = (idx + delta + len(f.EnumChoices)) % len(f.EnumChoices)
			q.values[f.Name] = f.EnumChoices[idx]
			return nil, nil
		}
	case "backspace":
		f := q.req.Fields[q.idx]
		if f.Type == ElicitFieldString {
			cur, _ := q.values[f.Name].(string)
			if cur != "" {
				// Delete one rune, not one byte: slicing a string by
				// byte cuts a multi-byte encoding in half and leaves
				// invalid UTF-8 in the values map, which then flows
				// into the frame AND into the ElicitResult the host
				// receives.
				r := []rune(cur)
				q.values[f.Name] = string(r[:len(r)-1])
			}
			return nil, nil
		}
	}
	// Printable single-rune keystrokes append to typed fields. Count
	// runes, not bytes: every printable character outside ASCII is
	// 2-4 bytes, so a byte-length guard silently drops é / 日 / 😀 and
	// the form is unusable for anything but ASCII. Multi-rune named
	// strokes ("ctrl+b", "enter") still fail the count, and IsPrint
	// keeps a lone control rune out of the value.
	if r := []rune(stroke); len(r) == 1 && unicode.IsPrint(r[0]) {
		f := q.req.Fields[q.idx]
		switch f.Type {
		case ElicitFieldString, ElicitFieldNumber, ElicitFieldInteger:
			cur, _ := q.values[f.Name].(string)
			q.values[f.Name] = cur + stroke
		case ElicitFieldBoolean, ElicitFieldEnum:
		}
	}
	return nil, nil
}

// Body renders the URL blurb or the field list, windowed to whatever
// rows the terminal can spare after the chrome.
func (q *elicitQuestion) Body(width, termHeight int, st styleSet) string {
	var (
		lines []string
		focus = -1 // -1 = nothing to keep on screen (URL mode)
	)
	if q.req.Mode == ElicitURLMode {
		body := st.Accent.Render(q.req.URL)
		if q.req.Description != "" {
			body = st.Muted.Render(q.req.Description) + "\n\n" + body
		}
		lines = strings.Split(body, "\n")
	} else {
		lines, focus = q.formLines(modalBodyWidth(width), st)
	}

	// The offset follows the focused field while arrow / page keys and
	// the wheel move it directly — without the follow, Tab could walk
	// the focus down into the invisible part of a long form.
	chrome := modalChromeRows - 1 + wrappedRows(q.legend(), modalInnerWidth(width))
	view := modalBodyHeight(termHeight, chrome)
	q.sc.measure(len(lines), view)
	if focus >= 0 {
		q.sc.follow(focus)
	}
	return strings.Join(scrollView(st, lines, modalBodyWidth(width), view, q.sc.offset), "\n")
}

// formLines renders the fields one per terminal row and returns the
// row index the focused field starts on. The caller needs both: a
// field row is two lines when it carries a description, so "field
// index" and "row index" differ.
func (q *elicitQuestion) formLines(width int, st styleSet) (lines []string, focus int) {
	for i, f := range q.req.Fields {
		if i == q.idx {
			focus = len(lines)
		}
		lines = append(lines, strings.Split(q.renderField(f, i == q.idx, width, st), "\n")...)
	}
	return lines, focus
}

// elicitFieldRow lays out one "  label:           value" form row.
// Factored out of renderField so the caret path can measure the
// prefix — everything left of the value — with the exact padding the
// renderer applies, instead of re-deriving the column arithmetic and
// drifting from it. The focused row swaps the two-space lead for
// "> ", which is the same width.
func elicitFieldRow(label, value string) string {
	return fmt.Sprintf("  %-16s %s", label, value)
}

// renderField renders one field row, styling the focused one
// accent-bold. Width is reserved for future per-field truncation;
// unused today but kept on the signature so callers don't have to
// refactor when it lands.
func (q *elicitQuestion) renderField(f ElicitField, focused bool, _ int, st styleSet) string {
	label := f.Name
	if f.Required {
		label += "*"
	}
	row := elicitFieldRow(label+":", q.formatValue(f, st))
	rendered := st.AssistantText.Render(row)
	if focused {
		rendered = st.Accent.Render("> " + strings.TrimPrefix(row, "  "))
	}
	// Per-field description renders on the line below the value in
	// muted text, so MCP elicits with explanatory help text actually
	// surface it to the operator.
	if f.Description != "" {
		return rendered + "\n" + st.Muted.Render("    "+strings.ReplaceAll(f.Description, "\n", " "))
	}
	return rendered
}

// formatValue renders a field's current value — booleans as
// checkboxes, enums with arrow hints, strings and numbers as the
// literal value or a placeholder.
func (q *elicitQuestion) formatValue(f ElicitField, st styleSet) string {
	switch f.Type {
	case ElicitFieldBoolean:
		if on, _ := q.values[f.Name].(bool); on {
			return "[✓]"
		}
		return "[ ]"
	case ElicitFieldEnum:
		v, _ := q.values[f.Name].(string)
		if v == "" && len(f.EnumChoices) > 0 {
			v = f.EnumChoices[0]
		}
		return "‹ " + v + " ›"
	case ElicitFieldString, ElicitFieldNumber, ElicitFieldInteger:
	}
	// The typed-text field kinds, and anything outside the declared
	// set, render the literal value.
	v, _ := q.values[f.Name].(string)
	if v == "" {
		return st.Muted.Render("(empty)")
	}
	return v
}

// Cursor implements cursorQuestion for the focused field's caret.
//
// The form is not a bubbles widget — renderField prints "label:
// value" rows and formKey appends runes to the end of the focused
// value — so the caret is derived from the same row format the
// renderer uses, and always sits at the end of the typed value.
func (q *elicitQuestion) Cursor(width int) *tea.Cursor {
	if q.req.Mode == ElicitURLMode || len(q.req.Fields) == 0 {
		return nil
	}
	if q.idx < 0 || q.idx >= len(q.req.Fields) {
		return nil
	}
	f := q.req.Fields[q.idx]
	switch f.Type {
	case ElicitFieldString, ElicitFieldNumber, ElicitFieldInteger:
	case ElicitFieldBoolean, ElicitFieldEnum:
		// Booleans toggle with space and enums cycle with ←/→ —
		// neither accepts typed text, so neither owns a caret.
		return nil
	default:
		return nil
	}

	// Which body row the focused field starts on. formLines is re-run
	// rather than remembered from Body: a field row can be two lines,
	// so "field index" is not "row index", and re-deriving that rule
	// here is how the caret and the renderer would drift apart. Zero
	// styleSet because only the row COUNT is read, and no style can
	// change it — a styleSet value cannot introduce a newline.
	_, focusRow := q.formLines(modalBodyWidth(width), styleSet{})

	// Subtract the offset the render just settled on. Body runs before
	// Cursor in the same frame (see askedQuestion.Render), so sc holds
	// post-render geometry. A focused field scrolled out of the window
	// has no on-screen caret.
	if q.sc.view > 0 && q.sc.total > q.sc.view {
		focusRow -= q.sc.offset
		if focusRow < 0 || focusRow >= q.sc.view {
			return nil
		}
	}

	// Column: the focused row is the "> " marker, the padded label, a
	// space, then the value. Measured with ansi.StringWidth via
	// elicitFieldRow so a label or value carrying wide runes counts
	// terminal CELLS, not bytes or runes.
	label := f.Name
	if f.Required {
		label += "*"
	}
	typed, _ := q.values[f.Name].(string)
	x := ansi.StringWidth(elicitFieldRow(label+":", "")) + ansi.StringWidth(typed)
	return tea.NewCursor(x+modalContentX, focusRow+modalBodyTop)
}

// isElicitEmpty reports whether v is the zero value for its type —
// used by Enter's submit-time validation against required fields.
func isElicitEmpty(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case bool:
		return false // every bool is a valid choice
	default:
		return false
	}
}

// indexOfEnum returns the index of v in choices, defaulting to 0 when
// v is nil or not found. Used by enum ←/→ cycling.
func indexOfEnum(choices []string, v any) int {
	s, _ := v.(string)
	for i, c := range choices {
		if c == s {
			return i
		}
	}
	return 0
}

// openElicit returns the elicit form currently on the overlay stack,
// or nil when none is open. It is what replaced `m.pendingElicit !=
// nil` for the Model-side code that has a legitimate reason to ask —
// the footer legend, and the tests that drive a form to completion.
//
// Note what it is NOT for: ending the question. That goes through
// overlay.resolve, so the exactly-once latch cannot be sidestepped.
func (m *Model) openElicit() *elicitQuestion {
	aq := m.overlayStack.asked(elicitDialogID)
	if aq == nil {
		return nil
	}
	q, _ := aq.q.(*elicitQuestion)
	return q
}

var (
	_ question       = (*elicitQuestion)(nil)
	_ cursorQuestion = (*elicitQuestion)(nil)
	_ gracedQuestion = (*elicitQuestion)(nil)
	_ scrollQuestion = (*elicitQuestion)(nil)
)
