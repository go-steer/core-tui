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

// Typed answers for modal questions (issue #164, stage 1 of the plan
// in docs/design-question-dialogs.md §12).
//
// The problem this replaces. A Dialog today expresses ROUTING — was
// the key consumed, should the stack pop, here is a Cmd — and never
// the DECISION the operator made. So the decision has to be acted on
// where it is discovered, inside the widget, against a *Model the
// widget was handed for that purpose: the theme picker's Enter arm
// applied a theme, appended a transcript row, refreshed the viewport,
// built a host message and scheduled a persistence callback, all from
// inside a list widget. That widget cannot be tested without an app
// model, and the rule "esc puts the previewed theme back" sits sixty
// lines away from the rule "enter keeps it".
//
// A question returns a typed `answer` instead, and a `resolver`
// registered by whoever asked the question turns that answer into
// effects. The effects do not get smaller — they move to the one
// place that already knows what the answer means.
//
// Stage 1 introduces the vocabulary and converts exactly one caller,
// the theme picker (question_themepicker.go). Dialog and every dialog
// on it are untouched; the two live side by side on the same Overlay
// until the migrations in stage 3.

package tui

import (
	tea "charm.land/bubbletea/v2"
)

// answer is what a question dialog produces. It is a sealed
// interface: the marker method is unexported, so the complete set of
// variants is the set declared in this file.
//
// The //sumtype:decl directive is what makes the sealing real rather
// than decorative. Go type switches are never exhaustiveness-checked
// by the compiler — adding an eighth variant below would compile fine
// against every seven-arm switch in the package. The directive hands
// the set to gochecksumtype (enabled in dev/tools/.golangci.yml since
// #164 stage 0), which fails any type switch over an answer that
// misses a variant. A `default:` arm does NOT excuse a missing one;
// that is deliberate, and it is the whole reason the linter landed
// first.
//
// Variants are shapes, not domains. "The operator picked the third
// row" is a shape; "the operator granted this tool for the session"
// is a domain, and domains ride inside a shape (decision) or as the
// resolver's reading of an option ID. Adding a variant here is a real
// cost — every switch in the package has to grow an arm — and that
// cost is the point.
//
//sumtype:decl
type answer interface{ isAnswer() }

// dismissed is "no answer". Every question can produce it and every
// resolver must handle it.
//
// Reason exists because the ways a question dies are not
// interchangeable to a host: an operator pressing esc on a permission
// prompt is a deny, a session switch tearing the prompt down is a
// request the host should re-ask against the new agent, and a schema
// the TUI cannot render at all is an MCP decline the server has to be
// told about. Today those are conflated, or — in the session-switch
// case — simply dropped.
type dismissed struct{ Reason dismissReason }

type dismissReason uint8

const (
	dismissEscape       dismissReason = iota // operator pressed esc
	dismissSuperseded                        // torn down by a session switch / reload
	dismissShutdown                          // the program is quitting
	dismissUnrenderable                      // the request could not be shown at all
)

// declined is an explicit "no" that is not a dismissal. MCP
// distinguishes decline ("the user said no") from cancel ("the user
// dismissed it") and the reference host forwards both distinctly, so
// folding this into dismissed{Reason: ...} would encode an answer as
// a reason for not answering.
type declined struct{}

// chosen is exactly one option from an ordered list.
//
// ID is the stable option identity supplied by whoever built the
// list; Index is its position in the list AS FILTERED, and exists
// because three of the callers already track an index. Resolvers
// should prefer ID.
type chosen struct {
	ID    string
	Index int
}

// selected is zero or more options. Distinct from chosen rather than
// chosen-with-a-slice: a single-select resolver that has to handle
// len(IDs) == 0 and len(IDs) == 2 is a single-select resolver with
// two unreachable branches, and the permission prompt is not a place
// to have unreachable branches.
type selected struct {
	IDs     []string
	Indexes []int
}

// text is one free-text value, already trimmed.
type text struct{ Value string }

// fields is a named value per field — the shape an elicit form and
// the /pricing form produce. Values holds Go primitives (string,
// float64, int64, bool), matching what ElicitResult.Values already
// carries to the host verbatim.
type fields struct{ Values map[string]any }

// decision is a permission decision, and it is the one domain-typed
// variant. The elegant alternative — chosen{ID: "allow-session"} over
// the six labels permissionDecisionLabel already produces — was
// rejected because the mapping back needs a fallback, and the only
// safe fallback for an unrecognised permission option is
// DecisionDeny. That would install a silent security downgrade inside
// the component whose job is to be correct about permissions.
type decision struct{ Value PermissionDecision }

func (dismissed) isAnswer() {}
func (declined) isAnswer()  {}
func (chosen) isAnswer()    {}
func (selected) isAnswer()  {}
func (text) isAnswer()      {}
func (fields) isAnswer()    {}
func (decision) isAnswer()  {}

// question is a modal that asks exactly one thing and produces
// exactly one answer. Note what is absent: no *Model, no tea.Cmd
// dispatch of effects, no history, no overlay manipulation. A
// question is a pure state machine over keystrokes plus a renderer,
// which is what lets one be driven to completion from
// package tui_test with no NewModel and no tea.NewProgram.
type question interface {
	// ID is the overlay identity, as Dialog.ID today.
	ID() string

	// Key advances the question by one keystroke.
	//
	// A non-nil answer means the question is finished: the overlay
	// pops it and hands the answer to the resolver registered when it
	// was asked. A nil answer means "still asking" — the keystroke
	// was consumed either way, because an open modal is exclusive.
	// There is no Consumed here for the same reason there is no
	// dialog in the package that returns Consumed: false.
	//
	// The returned Cmd belongs to the question's own machinery — a
	// bubbles textinput's cursor blink, or a message the question
	// needs the Update loop to apply on its behalf because the
	// *Model it would need is a per-Update copy (the theme picker's
	// live preview is the one instance today). It is not an answer
	// channel and must not carry host-visible messages.
	Key(msg tea.KeyPressMsg) (answer, tea.Cmd)

	// Title and Footer are the chrome strings. The overlay owns the
	// frame; the question owns the words in it.
	Title() string
	Footer() string

	// Width is the modal's preferred TOTAL width, edge included,
	// given the columns the terminal has. The question clamps
	// itself because how wide a question wants to be is a property
	// of its content, not of the chrome around it.
	Width(avail int) int

	// Body renders the question's content to exactly width columns.
	//
	// termHeight is the terminal's height, carrying the same
	// contract as RenderContext.Height: zero means the geometry is
	// unknown and no windowing should happen. It is a parameter
	// rather than overlay-owned state — which is the one place this
	// seam departs from design-question-dialogs.md §6.1 — because a
	// question with a longer body than the terminal has to window
	// it, and the row budget depends on how many chrome rows the
	// question itself spends inside Body.
	//
	// Styles is passed by value: a question never reaches back for
	// the app's theme, and the zero Styles renders unstyled, which
	// is what makes the external test possible.
	Body(width, termHeight int, st Styles) string
}

// cursorQuestion is a question with a text caret — anything with a
// filter row or an input box. Mirrors today's cursorDialog, minus the
// *Model argument, which was the only thing keeping that interface
// tied to the app model.
type cursorQuestion interface {
	question

	// Cursor returns the caret position relative to the modal
	// block's own top-left cell, or nil when nothing owns it right
	// now. width is the modal's own width, already resolved through
	// Width.
	Cursor(width int) *tea.Cursor
}

// resolver turns an answer into effects. It runs on the Update
// goroutine, exactly once per question, and it is the ONLY place a
// question's answer meets the app model.
//
// It is registered by whoever asked the question, which is where the
// knowledge of what the answer means already lives: the /theme
// handler knows a pick means applyNamedTheme; the permission listener
// knows a decision means dispatchDecision.
//
// m is a parameter rather than something the resolver closes over,
// and that is load-bearing: Model.Update has a value receiver, so a
// *Model captured when the question was asked points at the Update
// copy that opened it and is dead by the time the answer arrives.
type resolver func(a answer, m *Model) tea.Cmd

// askedQuestion is the adapter that lets a question ride the existing
// Overlay. It is the whole of the compatibility story for stage 1:
// routing, z-order, the esc cascade, the wheel and the caret all keep
// working against Dialog, and questions get there by being wrapped
// rather than by the stack learning a second shape.
type askedQuestion struct {
	q question
	r resolver
	// resolved is the exactly-once latch. A question can be answered
	// by a keystroke and then swept by resolveAll in the same frame
	// (esc on the last modal while a session switch is landing), and
	// a resolver that runs twice would, for the theme picker, put
	// back a theme the operator just committed.
	resolved bool
}

// ask pushes a question onto the overlay stack together with the
// resolver that will receive its answer. It is Open for questions;
// Open stays for viewers (the tool-call and subagent overlays, which
// answer nothing).
//
// Unexported, unlike the rest of Overlay's methods, because its
// parameters are: an exported Ask a host cannot name the arguments of
// would be API surface with no caller. Exporting the family is
// posture B in the design, and it is not stage 1's to decide.
func (o *Overlay) ask(q question, r resolver) {
	o.Open(&askedQuestion{q: q, r: r})
}

// resolveAll answers every outstanding question with reason and
// clears the stack. Viewers are simply dropped — they have no
// resolver to tell.
//
// This is what closes the hole where a session switch tore down a
// modal and the flow waiting on it was never told anything at all.
// Stage 2 wires it to applySwitchTarget and to the quit path; stage 1
// ships the mechanism and its test.
func (o *Overlay) resolveAll(reason dismissReason, m *Model) tea.Cmd {
	var cmds []tea.Cmd
	for _, d := range o.dialogs {
		aq, ok := d.(*askedQuestion)
		if !ok {
			continue
		}
		if cmd := aq.resolve(dismissed{Reason: reason}, m); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	o.dialogs = nil
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// asked returns the askedQuestion carrying id, or nil when the dialog
// under that id is a viewer or is not open at all.
//
// It is how an Update-side handler reaches a specific open question to
// LOOK at it — a host reply checking that the picker that issued it is
// still the one on screen. Reaching one to END it goes through resolve
// instead, so the exactly-once latch cannot be sidestepped. Callers
// wanting the widget itself take .q and type-assert; the wrapper is
// what knows whether it has already been answered, and a caller that
// only ever saw the widget could not.
func (o *Overlay) asked(id string) *askedQuestion {
	aq, _ := o.Get(id).(*askedQuestion)
	return aq
}

// resolve answers the question under id, pops it, and returns whatever
// its resolver scheduled. A no-op when nothing matches or when the
// question has already been answered.
//
// This is the seam for an answer that does not come from a keystroke.
// The model picker's is the first: Enter starts a host call rather than
// ending the question, and what closes the picker is the reply landing
// in Update several hundred milliseconds later. Without this the
// Update-side handler would have to Overlay.Close the question, which
// pops it with its resolver never run — the exact "torn down and
// nobody was told" shape §1.4 of the design is about, reintroduced by
// the code meant to remove it.
//
// It is also why a resolver must not Open a dialog: resolve pops
// AFTER the resolver returns, and Overlay.HandleKeyMsg pops after it
// too, so anything a resolver pushed would be what got popped. A
// resolver that needs another modal returns a Cmd and lets Update open
// it, the way the theme picker's live preview is applied.
func (o *Overlay) resolve(id string, ans answer, m *Model) tea.Cmd {
	aq := o.asked(id)
	if aq == nil {
		return nil
	}
	cmd := aq.resolve(ans, m)
	o.Close(id)
	return cmd
}

// resolve runs the resolver at most once and returns whatever it
// scheduled.
func (a *askedQuestion) resolve(ans answer, m *Model) tea.Cmd {
	if a.resolved {
		return nil
	}
	a.resolved = true
	if a.r == nil {
		return nil
	}
	return a.r(ans, m)
}

func (a *askedQuestion) ID() string { return a.q.ID() }

// HandleKey satisfies Dialog for callers holding only a normalized
// stroke — the wheel synthesizer, and Overlay.HandleKey itself.
func (a *askedQuestion) HandleKey(stroke string, m *Model) DialogAction {
	return a.HandleKeyMsg(keyMsgFromStroke(stroke), m)
}

// HandleKeyMsg is the real handler: a question always gets the
// full-fidelity keystroke, because Key.Text and bracketed pastes are
// exactly what a filter row or an input box needs and a stroke string
// drops both.
func (a *askedQuestion) HandleKeyMsg(msg tea.KeyPressMsg, m *Model) DialogAction {
	ans, cmd := a.q.Key(msg)
	if ans == nil {
		return DialogAction{Consumed: true, Cmd: cmd}
	}
	return DialogAction{Consumed: true, Close: true, Cmd: tea.Batch(cmd, a.resolve(ans, m))}
}

func (a *askedQuestion) Render(totalWidth int, m *Model) string {
	width := a.q.Width(totalWidth)
	return RenderContext{
		Title:  a.q.Title(),
		Body:   a.q.Body(width, m.height, m.styles),
		Footer: a.q.Footer(),
		Width:  width,
		Height: m.height,
		Styles: m.styles,
	}.Render()
}

// DialogCursor implements cursorDialog. The adapter always has the
// method — Go cannot conditionally implement an interface — so a
// question with no caret answers nil here, which is the same answer
// Overlay.cursor's type assertion produced for a dialog that did not
// implement it at all.
func (a *askedQuestion) DialogCursor(width int, _ *Model) *tea.Cursor {
	cq, ok := a.q.(cursorQuestion)
	if !ok {
		return nil
	}
	return cq.Cursor(a.q.Width(width))
}

// The adapter deliberately does NOT implement ScrollDialog. Overlay
// routes a wheel tick to ScrollBy when the front dialog has it and
// otherwise synthesizes one up/down keystroke, and the second is the
// right behaviour for a list: a wheel nudge that jumps three rows
// past the one you wanted is worse than no wheel at all. A scrolling
// question — the permission prompt with a long diff — arrives in
// stage 3 and brings the scrollQuestion extension with it, since an
// interface with no implementor is not a seam, it is a guess.
var _ Dialog = (*askedQuestion)(nil)
var _ KeyMsgDialog = (*askedQuestion)(nil)
var _ cursorDialog = (*askedQuestion)(nil)
