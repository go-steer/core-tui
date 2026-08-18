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

// Action-row text input — the dialog the session picker stacks on top
// of itself when a SessionInfo carries a SessionInput (issue #56),
// and the off-loop plumbing behind the host closure it commits with
// (issue #194).
//
// It was a bare newTextInputDialog whose submit closure called
// SessionInput.Submit inline and returned the result in the same
// frame. That closure is host code with the same contract as
// SwitchToSession — the motivating row is "+ Attach to endpoint…",
// so the call is a dial against an address the operator just typed
// and may well have typed wrong. Running it from HandleKey put a
// full connect timeout on the Update goroutine: no repaint, no keys,
// and no Ctrl+C, since interrupt handling lives in the same Update.
//
// So the commit is now two stages, the shape #183 established for
// `/switch <id>`: Enter marks the dialog in-flight and hands back a
// Cmd, the Cmd calls Submit on its own goroutine, and
// sessionInputSubmittedMsg brings the SwitchTarget back to Update to
// be applied — through applySessionSwitch, the same tail the picker's
// own Enter already used.
//
// The dialog is a wrapper rather than a configured primitive because
// "in flight" is not something textInputConfig can express: the box
// has to stop accepting keys, stop claiming the caret, and say what
// it is waiting for, and all three are states the generic dialog has
// no concept of.

package tui

import (
	tea "charm.land/bubbletea/v2"
)

// sessionInputDialogID is the text-input dialog the picker stacks
// on top of itself for SessionInfo action rows (issue #56).
const sessionInputDialogID = "session-input"

// sessionInputDialog is the action row's text input plus the state
// the primitive underneath it has no concept of: whether the row's
// own Submit closure is currently in the air.
//
// The embedded *textInputDialog owns everything about being a text
// input — the widget, validation, the caret, the chrome — and every
// method not overridden below is its. The overrides exist only to
// interpose the in-flight gate.
type sessionInputDialog struct {
	*textInputDialog

	// row is the action row this dialog was opened for. Held whole
	// rather than just its Submit closure: the ID goes out with the
	// request and comes back on the reply, which is how a reply is
	// matched to the dialog still waiting for it.
	row SessionInfo

	// inflight is set by the Enter that dispatched Submit and
	// cleared when the reply lands or the operator dismisses the
	// dialog. A bool and not "value != ''" because a row that sets
	// no Validate accepts the empty string, and an empty submitted
	// value must not read as idle.
	inflight bool

	// value is what was handed to Submit. Rendered in the in-flight
	// body, and checked against the reply: dismissing an in-flight
	// dialog and opening a fresh one from the PICKER doesn't bump
	// any counter — Ctrl+G is not a slash dispatch — so gen and seq
	// can both still match when the first reply lands on the second
	// dialog. The value is what tells the two apart.
	value string
}

// newSessionInputDialog builds the text-input dialog for a
// SessionInfo action row. Title falls back through the row's display
// name to the primitive's own default, so a host that fills in
// neither still gets a labelled modal.
func newSessionInputDialog(row SessionInfo) *sessionInputDialog {
	in := row.Input
	title := in.Title
	if title == "" {
		title = row.Display
	}
	if title == "" {
		title = "Enter a Value"
	}
	d := &sessionInputDialog{row: row}
	d.textInputDialog = newTextInputDialog(textInputConfig{
		ID:          sessionInputDialogID,
		Title:       title,
		Prompt:      in.Prompt,
		Placeholder: in.Placeholder,
		Initial:     in.Initial,
		Validate:    in.Validate,
		Footer:      "enter attach " + GlyphSeparator + " esc back",
		Submit:      d.submit,
	})
	return d
}

// submit is the textInputConfig.Submit closure, so it runs on the
// Update goroutine and must not touch the host itself. Its whole job
// is to arm the in-flight state and hand back the Cmd that does.
//
// The gen/seq stamp is read HERE, at the keystroke that commits,
// rather than when the dialog was opened: the guard's question is
// "has the operator moved on since they pressed Enter", and the
// dialog can sit open for as long as it takes to type a URL.
func (d *sessionInputDialog) submit(value string, m *Model) DialogAction {
	in := d.row.Input
	if in.Submit == nil {
		// A malformed row — Input set, Submit nil. This is a nil
		// check and not a host call, so it is still answered in the
		// dispatching frame, and it closes both dialogs the way
		// every other terminal outcome here does.
		cmd := m.endSessionPicker(dismissed{Reason: dismissSuperseded})
		m.history.Append(Message{Role: RoleError, Text: "/switch: session row " + d.row.ID + " has no Submit closure"})
		m.refreshViewport()
		// Close pops the FRONT dialog, which is this one: the picker
		// underneath went through endSessionPicker, by ID.
		return DialogAction{Consumed: true, Close: true, Cmd: cmd}
	}
	d.inflight, d.value = true, value
	// Stay open. The operator gets the in-flight body as their
	// acknowledgement, and the dialogs close together when the
	// reply is applied — closing now would leave the attach
	// happening behind an empty screen with nothing to escape.
	return DialogAction{
		Consumed: true,
		Cmd:      sessionInputSubmitCmd(in.Submit, m.sessionGen, m.slashSeq, d.row.ID, value),
	}
}

// HandleKey satisfies Dialog for callers holding only a normalized
// stroke. Restated rather than inherited: the embedded
// textInputDialog.HandleKey delegates to its OWN HandleKeyMsg, which
// would route around the in-flight gate below.
func (d *sessionInputDialog) HandleKey(stroke string, m *Model) DialogAction {
	return d.HandleKeyMsg(keyMsgFromStroke(stroke), m)
}

// HandleKeyMsg gates the primitive on the in-flight state.
//
// Esc still closes, and that is deliberate: an unresponsive endpoint
// taking the keyboard with it is the entire defect this dialog was
// rewritten to fix, so the one key that means "let me out" cannot be
// the one key that is swallowed. SessionInput.Submit takes no
// context, so there is nothing to cancel — the goroutine runs to
// completion and its reply is discarded (see
// applySessionInputSubmit). Everything else is swallowed while the
// call is out, most of all a second Enter: the picker swallows keys
// under an in-flight SwitchToSession for the same reason, and here a
// second Enter would put a second dial on the wire.
func (d *sessionInputDialog) HandleKeyMsg(msg tea.KeyPressMsg, m *Model) DialogAction {
	if !d.inflight {
		return d.textInputDialog.HandleKeyMsg(msg, m)
	}
	if msg.String() == "esc" {
		d.inflight = false
		return DialogAction{Consumed: true, Close: true}
	}
	return DialogAction{Consumed: true}
}

// Render swaps the typed body for a progress line while Submit is
// out, in the chrome and at the width the typed frame would have
// used so the modal doesn't jump on Enter. Same wording as the
// picker's in-flight SwitchToSession, because from the operator's
// side it is the same wait.
func (d *sessionInputDialog) Render(totalWidth int, m *Model) string {
	if !d.inflight {
		return d.textInputDialog.Render(totalWidth, m)
	}
	note := "attaching…"
	if d.value != "" {
		note = "attaching to " + d.value + "…"
	}
	return RenderContext{
		Title:  d.cfg.Title,
		Body:   m.styles.Muted.Render(note),
		Footer: "esc cancel",
		Width:  d.dialogWidth(totalWidth),
		Height: m.height,
		Styles: m.styles,
	}.Render()
}

// DialogCursor drops the caret while the call is out — the input box
// isn't on screen to put it in, and a caret parked on a progress line
// would advertise a surface that no longer takes keys.
func (d *sessionInputDialog) DialogCursor(width int, m *Model) *tea.Cursor {
	if d.inflight {
		return nil
	}
	return d.textInputDialog.DialogCursor(width, m)
}

// applySessionInputSubmit is the Update-side half of the action row's
// commit. Called only after the gen AND seq guards have passed, so
// the reply is known to belong to the session and the slash dispatch
// the operator is still looking at.
//
// A reply whose dialog is GONE is discarded outright — no attach, no
// error row, nothing. Esc on an in-flight dialog is an explicit
// cancel, and the operator who pressed it has moved on; landing a
// switch afterwards would swap their whole session out from under
// them, seconds later, with no keystroke to blame it on. Hosts should
// therefore treat SessionInput.Submit as a call whose RESULT may be
// dropped: if it opened something (a connection, a lease), the drop
// is the host's to clean up, exactly as it would be for a
// SwitchTarget the TUI failed to attach.
//
// The same check is what stops a reply from landing on the WRONG
// dialog — see the value field's comment for the picker sequence
// where gen and seq alone are not enough.
func (m *Model) applySessionInputSubmit(msg sessionInputSubmittedMsg) tea.Cmd {
	d, ok := m.overlayStack.Get(sessionInputDialogID).(*sessionInputDialog)
	if !ok || !d.inflight || d.row.ID != msg.rowID || d.value != msg.value {
		return nil
	}
	d.inflight = false
	// Both dialogs go, on every outcome: the text input that asked
	// the question and the picker underneath it. Failures close both
	// too — an error row in the transcript is the report, and leaving
	// the modal up would bury it. That is the one place this path
	// deliberately differs from the picker's own Enter, where a failed
	// attach leaves the list up: there the operator is still looking at
	// somewhere else to go, here they typed an address that did not
	// work and the report is the point.
	m.overlayStack.Close(sessionInputDialogID)
	attached := msg.err == nil && msg.target.Agent != nil
	pickerCmd := m.endSessionPicker(sessionInputAnswer(msg.rowID, attached))
	if msg.err == nil && msg.target.Agent == nil {
		// Checked here rather than left to applySessionSwitch only
		// so the row names the closure that actually misbehaved;
		// blaming SessionSwitcher would point at code the host
		// never ran.
		m.history.Append(Message{Role: RoleError, Text: "/switch: SessionInput.Submit returned nil Agent"})
		m.refreshViewport()
		return pickerCmd
	}
	// Everything else is the SwitchToSession tail verbatim — the
	// error row, the attach, the listener batch — so the two ways of
	// arriving at a new session cannot drift apart.
	return tea.Batch(pickerCmd, m.applySessionSwitch(sessionSwitchedMsg{
		gen:    msg.gen,
		id:     msg.rowID,
		target: msg.target,
		err:    msg.err,
	}))
}

// sessionInputAnswer is what the action row's outcome means to the
// PICKER underneath it, which is a different question from what it
// means to the operator.
//
// A successful attach is the picker's answer arriving by proxy: the
// operator picked that row, the address they typed is how it got
// resolved, so chosen carries the row ID exactly as an ordinary Enter
// would have. A failure is not an answer at all — nobody chose a
// session — and it is not the operator dismissing the list either, so
// esc is the wrong reason. It is dismissSuperseded: the input took over
// as the surface asking the question, and it is what tore the picker
// down.
func sessionInputAnswer(rowID string, ok bool) answer {
	if ok {
		return chosen{ID: rowID}
	}
	return dismissed{Reason: dismissSuperseded}
}

// endSessionPicker answers whatever session picker is open underneath
// and pops it, returning the Cmd its resolver scheduled.
//
// Through Overlay.resolve rather than Overlay.Close because the picker
// is a question now (#164 stage 3): closing it by ID would pop it with
// its resolver never run, which is the "torn down and nobody was told"
// hole the whole design exists to remove. A no-op when the input was
// opened by `/switch <id>` naming an action row, which has no picker
// under it at all.
func (m *Model) endSessionPicker(ans answer) tea.Cmd {
	return m.overlayStack.resolve(sessionPickerDialogID, ans, m)
}
