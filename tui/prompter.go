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

// PermissionPrompter is the interface the TUI implements and the
// host wires into its permission gate. The host's gate calls
// AskApproval whenever a tool invocation needs explicit operator
// approval; the call blocks on the TUI's modal until the operator
// chooses a decision (or until ctx is cancelled).
//
// See R-PERM-1 / R-PERM-2 in requirements.md and design.md §3.5.
type PermissionPrompter interface {
	AskApproval(ctx context.Context, req PermissionRequest) (PermissionDecision, error)
}

// PermissionKind tags the request's source so the modal can pick
// the right phrasing + scope-key glue (e.g. "bash command" vs
// "file edit" vs "url fetch").
type PermissionKind int

const (
	// PermissionKindBash is a shell-command tool call (R-PERM-1's
	// "show the verbatim command" rule applies).
	PermissionKindBash PermissionKind = iota
	// PermissionKindEdit is a file-edit tool call (Detail should be
	// the rendered diff).
	PermissionKindEdit
	// PermissionKindHTTP is a network-fetch tool call (Detail
	// should be URL + method + body summary).
	PermissionKindHTTP
	// PermissionKindOther is the catch-all — any tool call that
	// doesn't fit one of the above gets generic args rendering.
	PermissionKindOther
)

// DetailKind picks the Glamour code-fence language tag the modal
// uses when rendering Detail (R-PERM-1). DetailPlain renders
// without syntax highlighting.
type DetailKind int

const (
	DetailPlain DetailKind = iota
	DetailDiff             // unified diff (red/green hunks)
	DetailShell            // bash / sh command line
	DetailHTTP             // URL + method + body
	DetailArgs             // JSON or key=value tool args
)

// PermissionRequest carries everything the modal needs to render
// the approval prompt. Hosts populate the fields they have; the
// modal renders only what's set.
type PermissionRequest struct {
	Kind     PermissionKind
	ToolName string

	// Detail is the rendered payload the operator is being asked to
	// approve (R-PERM-1). For file edits: a unified diff. For shell:
	// the verbatim command. For HTTP: URL + method + body summary.
	// For other tools: a key=value or JSON dump.
	Detail     string
	DetailKind DetailKind

	// Verb is the action extracted from the payload (e.g. "rm" from
	// "rm -rf /tmp/foo"). Empty when no verb is meaningful — the
	// modal suppresses the verb-scoped decision (R-PERM-2 "v") when
	// Verb is empty.
	Verb string

	// Source is the sub-agent name when the request originated from
	// a background agent (R-PERM-1 "originating sub-agent"). Empty
	// for the foreground agent.
	Source string

	// PersistTool / PersistKey are the host's persistence-key hint.
	// Round-tripped back via the AlwaysAllow callback (R-PERM-3) so
	// the host knows what scope to write to disk.
	PersistTool string
	PersistKey  string
}

// PermissionDecision is the operator's choice. Six values per
// R-PERM-2 — every key the modal accepts maps to one of these.
type PermissionDecision int

const (
	DecisionDeny             PermissionDecision = iota // n / esc
	DecisionAllowOnce                                  // y
	DecisionAllowSession                               // s
	DecisionAllowSessionVerb                           // v (when Verb is non-empty)
	DecisionAllowSessionTool                           // t
	DecisionAllowAlways                                // a — host persists via callback
)

// permissionDenyReasonMax is the longest deny reason, in BYTES of
// UTF-8, the prompt will submit (R-PERM-9). It is the server's limit
// on core-agent's POST /perms/respond "reason" field, so the prompt
// refuses to send anything longer rather than truncating it or
// letting the host collect a 400.
const permissionDenyReasonMax = 500

// PermissionOutcome is the operator's answer to one permission
// request, as AskApprovalDetailed returns it: the decision, plus the
// free-text reason the operator attached to a deny.
//
// Reason is non-empty only when Decision is DecisionDeny AND the
// operator chose to type one (the prompt's "r" step, R-PERM-9). It is
// already trimmed and at most 500 bytes of UTF-8. A plain deny — the
// "n" key, esc, a superseded or shut-down prompt, a cancelled ctx —
// carries an empty Reason. Hosts forward a non-empty Reason to their
// backend with the deny (core-agent: /perms/respond's "reason", on
// protocol 1.15.0 or later) and drop nothing.
type PermissionOutcome struct {
	Decision PermissionDecision
	Reason   string
}

// permissionFlow couples one PermissionRequest with the response
// channel the TUI writes the decision back on. Lives only while
// the modal is up; closed once a decision is dispatched.
type permissionFlow struct {
	req      PermissionRequest
	response chan permissionResponse
	// offerReason is true when the request came in through
	// AskApprovalDetailed — the only entry point whose caller can
	// receive a reason. A request through plain AskApproval never
	// offers the reason step: collecting text the host cannot forward
	// and then silently dropping it would tell the operator the agent
	// heard something it never did.
	offerReason bool
}

// permissionResponse carries the operator's decision back to the
// blocked AskApproval call.
type permissionResponse struct {
	decision PermissionDecision
	reason   string
	err      error
}

// Prompter is the TUI-side PermissionPrompter implementation. The
// host obtains one via tui.NewPrompter() and wires it into its
// permission gate. The Bubble Tea loop drains the request channel
// via a listener Cmd; each request becomes a permissionRequestMsg
// that Update routes to the permission modal renderer.
//
// Concurrency model: AskApproval pushes a permissionFlow onto the
// requests channel (buffered 1) and blocks on the per-flow
// response channel. When the operator picks a decision, Update
// sends the response and the AskApproval call unblocks. If the
// caller's context cancels first, AskApproval returns the ctx
// error and starts a background drainer on the response channel
// so the eventual write (if Update hasn't seen the request yet,
// or has dispatched but not yet sent) doesn't leak the goroutine.
type Prompter struct {
	requests chan permissionFlow

	mu      sync.Mutex
	pending *permissionFlow // currently-displayed request, if any
}

// NewPrompter constructs a Prompter ready to be wired into the
// host's permission gate and the TUI's Options. Returns a pointer
// so the same instance can be shared between the gate callsite
// and the TUI's Init.
func NewPrompter() *Prompter {
	return &Prompter{
		requests: make(chan permissionFlow, 1),
	}
}

// AskApproval blocks until the operator picks a decision via the
// modal, or until ctx cancels. Implements PermissionPrompter.
//
// A request made this way never offers the deny-with-reason step,
// because this signature has nowhere to return a reason. Hosts that
// can forward one call AskApprovalDetailed instead.
func (p *Prompter) AskApproval(ctx context.Context, req PermissionRequest) (PermissionDecision, error) {
	out, err := p.ask(ctx, req, false)
	return out.Decision, err
}

// AskApprovalDetailed is AskApproval for a host that can forward the
// operator's reason for a deny to its backend. It blocks the same way
// and returns the same decisions, and in addition the prompt offers
// the "r" key — deny with a one-line reason (R-PERM-9). The reason
// comes back on the returned PermissionOutcome; see its doc for when
// it is set.
//
// Opting in is per request, by which method the host calls: the TUI
// cannot know whether a host would forward a reason, so it only asks
// for one when the caller has said so by choosing this method. On a
// cancelled ctx the outcome is a reasonless DecisionDeny plus the ctx
// error, exactly as AskApproval's.
func (p *Prompter) AskApprovalDetailed(ctx context.Context, req PermissionRequest) (PermissionOutcome, error) {
	return p.ask(ctx, req, true)
}

// ask is the body both entry points share. offerReason is carried on
// the flow to the prompt, which decides from it whether "r" exists.
func (p *Prompter) ask(ctx context.Context, req PermissionRequest, offerReason bool) (PermissionOutcome, error) {
	response := make(chan permissionResponse, 1)
	flow := permissionFlow{req: req, response: response, offerReason: offerReason}
	deny := PermissionOutcome{Decision: DecisionDeny}

	// Push the request onto the queue. Block briefly if the channel
	// is full (a previous modal hasn't been drained yet); ctx
	// cancellation unblocks.
	select {
	case p.requests <- flow:
	case <-ctx.Done():
		return deny, ctx.Err()
	}

	// Block on the operator's decision.
	select {
	case r := <-response:
		return PermissionOutcome{Decision: r.decision, Reason: r.reason}, r.err
	case <-ctx.Done():
		// Drain the response in the background so the goroutine
		// that eventually sends doesn't leak (design.md §4.1).
		go func() { <-response }()
		return deny, ctx.Err()
	}
}

// nextRequest is the Bubble Tea side's blocking read. The
// permission listener Cmd calls it; on receive it stashes the
// flow as pending (so the modal renderer can find it) and
// returns the request payload for Update.
func (p *Prompter) nextRequest(ctx context.Context) (PermissionRequest, bool) {
	flow, ok := p.nextFlow(ctx)
	return flow.req, ok
}

// nextFlow is nextRequest returning the whole flow, which is what the
// listener needs: whether the request offers the reason step is a
// property of how it was asked, not of the request itself.
func (p *Prompter) nextFlow(ctx context.Context) (permissionFlow, bool) {
	select {
	case flow := <-p.requests:
		p.mu.Lock()
		p.pending = &flow
		p.mu.Unlock()
		return flow, true
	case <-ctx.Done():
		return permissionFlow{}, false
	}
}

// dispatchDecision writes the operator's pick to the pending
// flow's response channel and clears the pending slot. No-op when
// no flow is pending — defends against double-dispatch from a key
// press fired after the modal already closed.
//
// reason rides along only on a deny, and only to a flow that offered
// the reason step; anything else is dropped here so no path can hand
// a plain-AskApproval caller, or an allow, a reason.
func (p *Prompter) dispatchDecision(d PermissionDecision, reason string) {
	p.mu.Lock()
	flow := p.pending
	p.pending = nil
	p.mu.Unlock()
	if flow == nil {
		return
	}
	if d != DecisionDeny || !flow.offerReason {
		reason = ""
	}
	// response is buffered cap 1; this never blocks.
	flow.response <- permissionResponse{decision: d, reason: reason}
}
