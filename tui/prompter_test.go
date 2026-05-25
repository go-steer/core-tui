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
	"testing"
	"time"
)

// TestPrompter_RoundTripsDecision pins the basic channel pattern —
// AskApproval blocks until dispatchDecision fires, and returns the
// dispatched value.
func TestPrompter_RoundTripsDecision(t *testing.T) {
	p := NewPrompter()
	resultCh := make(chan PermissionDecision, 1)
	go func() {
		d, _ := p.AskApproval(context.Background(), PermissionRequest{ToolName: "bash"})
		resultCh <- d
	}()
	// Drain the request and dispatch.
	_, ok := p.nextRequest(context.Background())
	if !ok {
		t.Fatal("nextRequest returned !ok with a pending request")
	}
	p.dispatchDecision(DecisionAllowSession)

	select {
	case got := <-resultCh:
		if got != DecisionAllowSession {
			t.Errorf("decision = %v, want AllowSession", got)
		}
	case <-time.After(time.Second):
		t.Fatal("AskApproval didn't return within 1s")
	}
}

// TestPrompter_ContextCancelDuringPush pins R-PERM-1 cancellation
// before the TUI has read the request: AskApproval returns the ctx
// error + DecisionDeny.
func TestPrompter_ContextCancelDuringPush(t *testing.T) {
	p := NewPrompter()
	// Fill the buffer so the next push blocks.
	p.requests <- permissionFlow{} // intentionally a noise flow

	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan error, 1)
	go func() {
		_, err := p.AskApproval(ctx, PermissionRequest{ToolName: "x"})
		resultCh <- err
	}()
	cancel()
	select {
	case err := <-resultCh:
		if err == nil {
			t.Error("expected ctx cancellation error, got nil")
		}
	case <-time.After(time.Second):
		t.Fatal("AskApproval didn't unblock after cancel")
	}
}

// TestPrompter_DispatchWithNoPendingIsNoOp pins that a stray
// dispatchDecision (e.g. a late keypress after the modal already
// closed) doesn't panic and doesn't write to a stale channel.
func TestPrompter_DispatchWithNoPendingIsNoOp(t *testing.T) {
	p := NewPrompter()
	// No call has been pending — dispatching should be silently no-op.
	p.dispatchDecision(DecisionAllowOnce)
}

// TestElicitor_RoundTripsResult pins the elicitor's basic channel
// pattern — same shape as the Prompter test.
func TestElicitor_RoundTripsResult(t *testing.T) {
	e := NewElicitor().(*elicitor)
	resultCh := make(chan ElicitAction, 1)
	go func() {
		r, _ := e.Elicit(context.Background(), "srv",
			ElicitRequest{Mode: ElicitURLMode, URL: "https://example.com"})
		resultCh <- r.Action
	}()
	flow, ok := e.nextRequest(context.Background())
	if !ok {
		t.Fatal("nextRequest returned !ok with a pending request")
	}
	if flow.serverName != "srv" {
		t.Errorf("serverName = %q, want srv", flow.serverName)
	}
	e.dispatchResult(ElicitResult{Action: ElicitActionSubmit})

	select {
	case got := <-resultCh:
		if got != ElicitActionSubmit {
			t.Errorf("action = %v, want Submit", got)
		}
	case <-time.After(time.Second):
		t.Fatal("Elicit didn't return within 1s")
	}
}

// TestElicitor_UnsupportedSchemaDeclines pins R-ELIC-3: an empty
// form (or empty URL in URL mode) auto-declines server-side instead
// of opening a broken modal.
func TestElicitor_UnsupportedSchemaDeclines(t *testing.T) {
	e := NewElicitor()
	r, err := e.Elicit(context.Background(), "srv", ElicitRequest{Mode: ElicitFormMode})
	if err != nil {
		t.Fatalf("Elicit err: %v", err)
	}
	if r.Action != ElicitActionDecline {
		t.Errorf("action = %v, want Decline for empty form", r.Action)
	}
}
