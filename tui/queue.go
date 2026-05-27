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

import "time"

// QueueState is the lifecycle of one operator-typed-during-streaming
// entry (R-CHAT-10). Each entry transitions:
//
//	Queued → InFlight → Done   (clean turn end)
//	                 → Failed  (turn error or interrupt)
//
// Done and Failed entries linger in the panel for cullTTL so the
// operator sees the result, then cull on the next render.
type QueueState int

const (
	QueueQueued   QueueState = iota // typed during streaming; waiting for the running turn to finish
	QueueInFlight                   // drained from the queue, currently the streaming turn
	QueueDone                       // turn finished cleanly
	QueueFailed                     // turn errored or was interrupted
)

// String returns the lowercase state label used in tests + logs.
func (s QueueState) String() string {
	switch s {
	case QueueInFlight:
		return "in-flight"
	case QueueDone:
		return "done"
	case QueueFailed:
		return "failed"
	default:
		return "queued"
	}
}

// QueueEntry is one row in the operator-typed-during-streaming queue
// panel (R-CHAT-10 / R-CHAT-11). Text holds the verbatim prompt;
// State tracks the lifecycle; Err carries the failure reason when
// State == QueueFailed; Created stamps when the entry was enqueued
// (or transitioned to terminal state) so the TTL cull knows when to
// drop it; Injected is true for entries routed through
// InjectableAgent.Inject (`InjectIntoCurrent` mode) so the renderer
// can label them distinctly from queue-drained entries.
type QueueEntry struct {
	Text     string
	State    QueueState
	Err      string
	Created  time.Time
	Injected bool
}

// cullTTL is how long QueueDone / QueueFailed entries linger in the
// panel before the next render drops them. Bumped from 2s → 8s
// (issue #8) — on fast-tier models the original two seconds had
// the entry gone before the operator's eyes could verify the
// queue actually processed anything. 8 seconds is comfortably above
// average reading speed while staying short enough that the panel
// doesn't clutter on a long working session.
const cullTTL = 8 * time.Second

// terminalState reports whether s is a leaf state (Done or Failed)
// and therefore subject to the cull TTL.
func (s QueueState) terminalState() bool {
	return s == QueueDone || s == QueueFailed
}

// hasPendingQueueEntry reports whether the model's queue holds any
// non-terminal entry (QueueQueued or QueueInFlight). Used by the
// wakeMsg handler to tell apart "operator just typed during streaming"
// from "subagent / external alert arrived" — the former produces a
// queue entry the operator can already see in the panel and doesn't
// need a redundant system-message about an inbox alert.
func (m *Model) hasPendingQueueEntry() bool {
	for _, e := range m.queue {
		if !e.State.terminalState() {
			return true
		}
	}
	return false
}
