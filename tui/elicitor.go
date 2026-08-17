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

package tui

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Elicitor is the interface the TUI implements and the host wires
// into each MCP server's elicit hook. MCP servers call Elicit when
// they need structured operator input mid-tool-call; the call
// blocks on the TUI's modal until the operator submits, declines,
// or cancels (ctx done).
//
// A non-nil error means no operator was consulted: either ctx
// cancelled, or the request is one this TUI cannot draw and was
// refused before any modal opened (ErrElicitUnsupported). Both pair
// the error with ElicitActionCancel, which is a placeholder and not
// an answer — a host forwarding the result to an MCP server should
// check err first (R-ELIC-3, issue #209).
//
// See R-ELIC-1 / R-ELIC-2 / R-ELIC-3 in requirements.md and
// design.md §3.5.
type Elicitor interface {
	Elicit(ctx context.Context, serverName string, req ElicitRequest) (ElicitResult, error)
}

// ElicitMode picks between the two modal shapes the TUI supports
// (R-ELIC-1). FormMode renders one field per Schema property;
// URLMode renders an open / accept / decline action row for a
// URL-typed request.
type ElicitMode int

const (
	ElicitFormMode ElicitMode = iota
	ElicitURLMode
)

// ElicitFieldType is the primitive type for one form field.
type ElicitFieldType int

const (
	ElicitFieldString ElicitFieldType = iota
	ElicitFieldNumber
	ElicitFieldInteger
	ElicitFieldBoolean
	ElicitFieldEnum
)

// ElicitField describes one field in a form-mode elicit request.
// Adapters translate their MCP server's schema (JSON Schema or
// similar) into a slice of these.
type ElicitField struct {
	Name        string
	Description string
	Type        ElicitFieldType

	// EnumChoices populated when Type == ElicitFieldEnum. The form
	// renders these as a Select picker.
	EnumChoices []string

	// Required is honored by the modal's submit-time validation:
	// empty values for required fields block submission.
	Required bool

	// Default seeds the field value at modal open. Type-coerced to
	// the field's Type by the renderer; passing a string here is
	// always safe.
	Default any
}

// ElicitRequest carries everything the modal needs. Mode picks the
// rendering shape; the other fields populate per mode.
type ElicitRequest struct {
	Mode ElicitMode

	// Title shown in the modal header. Server name renders alongside.
	Title       string
	Description string // optional dim subtitle

	// Form-mode fields (Mode == ElicitFormMode).
	Fields []ElicitField

	// URL-mode payload (Mode == ElicitURLMode).
	URL string
}

// ElicitResult is what the operator's choice produces. Action picks
// between submit / decline / cancel; Values carries the form field
// answers when Action == ElicitActionSubmit.
type ElicitResult struct {
	Action ElicitAction
	Values map[string]any
}

// ElicitAction is the operator's top-level decision. Every value of
// it reports a keystroke an operator actually made; the TUI never
// synthesizes one on their behalf.
//
// Decline and Cancel are different answers and the server that asked
// may act on the difference: Decline is "I read this and I am saying
// no", Cancel is "I dismissed it without deciding". Both are reachable
// in both modes. Form mode declines on ctrl+d rather than on a letter
// because every printable key belongs to the focused field, which is
// why the form advertised a decline it could not produce until issue
// #209.
type ElicitAction int

const (
	ElicitActionSubmit  ElicitAction = iota // form: Enter; url: a/Enter
	ElicitActionDecline                     // form: ctrl+d; url: n
	ElicitActionCancel                      // both: Esc
)

// ErrElicitUnsupported is returned by Elicit, wrapped, when the
// request is one this TUI has no way to draw — see supportedElicit
// for the screen. The result carries ElicitActionCancel, but the
// error is the real answer and the Action is only there because the
// return type has one.
//
// It exists because the alternative was worse. The refusal used to
// travel as a bare ElicitActionDecline, so "I could not render this"
// and "the operator read it and said no" reached the server as one
// word, in the one case where no operator was consulted at all
// (issue #209). MCP's vocabulary is three actions wide — accept,
// decline, cancel — so a fourth ElicitAction would have to be
// collapsed back onto one of them at the host boundary anyway; the
// error return is already in Elicit's signature, already paired with
// Cancel for the ctx path, and is where Go puts "this could not be
// carried out".
//
// Hosts match it with errors.Is. The wrapped message names which
// part of the request could not be drawn, and is the same reason the
// operator sees in the transcript.
var ErrElicitUnsupported = errors.New("elicit request is not renderable by this TUI")

// elicitFlow couples one ElicitRequest with the response channel.
// Same pattern as permissionFlow in prompter.go.
type elicitFlow struct {
	serverName string
	req        ElicitRequest
	response   chan elicitResponse
}

type elicitResponse struct {
	result ElicitResult
	err    error
}

// Elicitor is the TUI-side Elicitor implementation. The host
// obtains one via tui.NewElicitor() and wires it into each MCP
// server's elicit callback. The Bubble Tea loop drains the request
// channel via a listener Cmd; each request becomes an
// elicitRequestMsg that Update routes to the elicit modal renderer.
//
// Concurrency model matches the Prompter (design.md §4.1):
// requests channel is buffered 1, response channel is per-flow
// buffered 1, ctx cancellation drains in the background.
type elicitor struct {
	requests chan elicitFlow

	mu      sync.Mutex
	pending *elicitFlow
}

// NewElicitor constructs an Elicitor ready to be wired into each
// MCP server's elicit callback + the TUI's Options. Returns the
// interface so callers can swap impls in tests without referring
// to the unexported concrete type.
func NewElicitor() Elicitor { return &elicitor{requests: make(chan elicitFlow, 1)} }

// Elicit blocks until the operator submits / declines / cancels
// the modal, or until ctx cancels.
func (e *elicitor) Elicit(ctx context.Context, serverName string, req ElicitRequest) (ElicitResult, error) {
	response := make(chan elicitResponse, 1)
	flow := elicitFlow{serverName: serverName, req: req, response: response}

	select {
	case e.requests <- flow:
	case <-ctx.Done():
		return ElicitResult{Action: ElicitActionCancel}, ctx.Err()
	}

	select {
	case r := <-response:
		return r.result, r.err
	case <-ctx.Done():
		go func() { <-response }()
		return ElicitResult{Action: ElicitActionCancel}, ctx.Err()
	}
}

// supportedElicit screens out schemas the modal can't render
// (R-ELIC-3): nested objects, unsupported field types. Those are
// refused automatically rather than opening a broken modal — as an
// ErrElicitUnsupported, not as an operator's decline. For form mode
// the host has already flattened to ElicitField; we trust the
// conversion. For URL mode the URL must be non-empty.
//
// The screen runs on the Bubble Tea loop, in the elicitRequestMsg
// handler, and not here in Elicit. R-ELIC-3 asks for the automatic
// decline AND for a "schema unsupported" system message, and only
// the loop can append to the transcript — declining on this
// goroutine got the first half and dropped the second, so a server
// asking for something undrawable was refused in complete silence
// and the operator had no way to know it had happened. Screening
// where the message can be written keeps the decision and the
// record of it in one place.
func supportedElicit(req ElicitRequest) bool {
	switch req.Mode {
	case ElicitFormMode:
		return len(req.Fields) > 0
	case ElicitURLMode:
		return req.URL != ""
	default:
		return false
	}
}

// elicitUnsupportedReason names the part of the request the modal
// could not draw, in one clause. It mirrors supportedElicit's own
// arms rather than reading anything the server sent, so the screen
// and the explanation cannot describe different failures — a reason
// naming a cause the screen does not test for would be worse than no
// reason at all. Both the operator's transcript row and the host's
// error are built from this one function, which is what keeps the
// two accounts of a refusal identical.
func elicitUnsupportedReason(req ElicitRequest) string {
	switch req.Mode {
	case ElicitFormMode:
		return "the form has no fields"
	case ElicitURLMode:
		return "the URL is empty"
	default:
		return "the request mode is not one this TUI renders"
	}
}

// elicitUnsupportedError is what the server gets: ErrElicitUnsupported
// so a host can match it with errors.Is, wrapping the same reason the
// operator reads.
func elicitUnsupportedError(req ElicitRequest) error {
	return fmt.Errorf("%w: %s", ErrElicitUnsupported, elicitUnsupportedReason(req))
}

// elicitUnsupportedNotice is the R-ELIC-3 system message: what was
// refused, which server asked, and which part of the request the
// modal could not draw.
//
// It says "without asking you" because that is the fact the operator
// cannot otherwise recover. A refusal row that read "declined" would
// leave them believing a decline had gone out over their name, which
// is the same words-in-your-mouth problem one layer up (issue #209).
//
// The server name is the host's own label for the connection and is
// spliced in as-is, the same way the modal title bar already renders
// it. It is omitted rather than left as an empty gap when the host
// passes nothing.
func elicitUnsupportedNotice(serverName string, req ElicitRequest) string {
	from := ""
	if serverName != "" {
		from = " from " + serverName
	}
	return "MCP elicitation" + from + ": schema unsupported (" +
		elicitUnsupportedReason(req) + ") — refused without asking you"
}

// nextRequest is the Bubble Tea side's blocking read; mirrors the
// Prompter's namesake.
func (e *elicitor) nextRequest(ctx context.Context) (elicitFlow, bool) {
	select {
	case flow := <-e.requests:
		e.mu.Lock()
		e.pending = &flow
		e.mu.Unlock()
		return flow, true
	case <-ctx.Done():
		return elicitFlow{}, false
	}
}

// dispatchResult writes the operator's submit / decline / cancel to
// the pending flow. No-op when no flow is pending.
//
// err is non-nil only when the TUI is answering on its own account
// rather than relaying an operator: today that is the
// ErrElicitUnsupported refusal, which never opens a modal at all.
// The elicitResponse.err field has carried this since the type was
// written and nothing had ever set it (issue #209).
func (e *elicitor) dispatchResult(r ElicitResult, err error) {
	e.mu.Lock()
	flow := e.pending
	e.pending = nil
	e.mu.Unlock()
	if flow == nil {
		return
	}
	flow.response <- elicitResponse{result: r, err: err}
}
