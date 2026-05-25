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
// panel (R-CHAT-10). Text holds the verbatim prompt; State tracks the
// lifecycle; Err carries the failure reason when State == QueueFailed;
// Created stamps when the entry was enqueued so the TTL cull knows
// when to drop terminal-state entries.
type QueueEntry struct {
	Text    string
	State   QueueState
	Err     string
	Created time.Time
}

// cullTTL is how long QueueDone / QueueFailed entries linger in the
// panel before the next render drops them. Matches core-agent's
// queue.go convention so operators get the same fade-out cadence.
const cullTTL = 2 * time.Second

// terminalState reports whether s is a leaf state (Done or Failed)
// and therefore subject to the cull TTL.
func (s QueueState) terminalState() bool {
	return s == QueueDone || s == QueueFailed
}
