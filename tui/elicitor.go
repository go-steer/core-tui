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
	"sync"
)

// Elicitor is the interface the TUI implements and the host wires
// into each MCP server's elicit hook. MCP servers call Elicit when
// they need structured operator input mid-tool-call; the call
// blocks on the TUI's modal until the operator submits, declines,
// or cancels (ctx done).
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

// ElicitAction is the operator's top-level decision.
type ElicitAction int

const (
	ElicitActionSubmit  ElicitAction = iota // form: Enter; url: a/Enter
	ElicitActionDecline                     // form: n; url: n
	ElicitActionCancel                      // both: Esc
)

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
	if !supportedElicit(req) {
		return ElicitResult{Action: ElicitActionDecline}, nil
	}
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
// (R-ELIC-3): nested objects, unsupported field types. Auto-
// declines those server-side instead of opening a broken modal.
// For form mode the host has already flattened to ElicitField; we
// trust the conversion. For URL mode the URL must be non-empty.
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

// dispatchResult writes the operator's submit / decline / cancel
// to the pending flow. No-op when no flow is pending.
func (e *elicitor) dispatchResult(r ElicitResult) {
	e.mu.Lock()
	flow := e.pending
	e.pending = nil
	e.mu.Unlock()
	if flow == nil {
		return
	}
	flow.response <- elicitResponse{result: r}
}
