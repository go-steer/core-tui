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

// Notifier — host-initiated side channel for chat rows that don't
// belong to the agent event stream (R-NOTIFY-1, issue #30).
//
// Hosts construct one via tui.NewNotifier(), pass it on
// Options.Notifier, and call Notify(text) from any goroutine.
// The TUI drains the channel via notifyListener (mirrors the
// wake / prompt / elicit listener pattern) and appends each
// notice as a RoleNotice Message — visually distinct from
// RoleSystem so operators can tell "framework speaking" from
// "agent system response".
//
// Backpressure: small buffered channel (notifyBufferSize) + drop
// path. When the channel is full, Notify increments a dropped
// counter and the next successful enqueue carries the coalesced
// count so the operator sees "(+N dropped)" in the appended
// row. The use cases (reconnect notices, shutdown warnings, ...)
// don't justify blocking host goroutines on a full UI queue.
//
// Lifecycle: Notify after TUI exit silently drops (the listener
// goroutine returns when the channel is closed, but Notify
// callers don't have to track that — a closed channel send would
// panic, so the implementation guards with a mutex + closed flag).

package tui

import "sync"

// notifyBufferSize is the in-flight notice cap. 16 is large
// enough to absorb realistic bursts (reconnect retry chatter,
// multi-attach signaling) without blocking, but small enough
// that runaway notice loops surface as visible (+N dropped)
// markers within seconds rather than minutes.
const notifyBufferSize = 16

// noticeEnvelope is one inbound notice + the coalesced-drop
// count that should be appended to its rendered text.
type noticeEnvelope struct {
	text    string
	dropped int // count of notices dropped since the last successful enqueue
}

// Notifier is the host-facing handle for pushing notices into a
// running TUI. Construct via NewNotifier; pass on
// Options.Notifier; call Notify from any goroutine. Safe for
// concurrent use.
type Notifier struct {
	// ch carries inbound notices to the TUI's notifyListener
	// drain Cmd. Buffered (notifyBufferSize); see package
	// comment for the backpressure contract.
	ch chan noticeEnvelope

	mu      sync.Mutex
	closed  bool
	dropped int // accumulated dropped count since the last successful enqueue
}

// NewNotifier constructs a Notifier ready to be wired into
// Options.Notifier. Returns the concrete type so hosts can
// retain a typed handle for Notify calls.
func NewNotifier() *Notifier {
	return &Notifier{ch: make(chan noticeEnvelope, notifyBufferSize)}
}

// Notify pushes a chat-row notice to the TUI. Safe to call from
// any goroutine. Non-blocking: when the in-flight buffer is full
// (notifyBufferSize notices already queued), the call increments
// a dropped counter and returns immediately; the next successful
// enqueue carries the coalesced count so the operator sees
// `(+N dropped)` appended to the rendered text.
//
// Empty text is silently ignored — there's nothing to display.
//
// Calls after the TUI has exited are silently dropped (the
// Notifier's channel is closed and a guard rejects further
// sends rather than panicking). Hosts don't need to track TUI
// lifecycle.
func (n *Notifier) Notify(text string) {
	if text == "" {
		return
	}
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return
	}
	env := noticeEnvelope{text: text, dropped: n.dropped}
	select {
	case n.ch <- env:
		// Successful enqueue carries any accumulated drops; reset.
		n.dropped = 0
	default:
		// Channel full — drop this one, bump the counter; the
		// next successful enqueue surfaces the coalesced count.
		n.dropped++
	}
	n.mu.Unlock()
}

// close marks the Notifier as drained-and-done and closes the
// channel so the listener goroutine returns. Idempotent. Called
// by the TUI on exit (not exported — hosts don't manage
// lifecycle).
func (n *Notifier) close() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return
	}
	n.closed = true
	close(n.ch)
}
