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

// Package tui is the core-tui Bubble Tea TUI library. See
// docs/requirements.md and docs/design.md for the stable surface.
package tui

import (
	"context"
	"iter"
)

// Agent is the minimum interface a host must supply. Run executes one
// turn against prompt and returns an iterator of Events the TUI drains
// in a goroutine. Cancel the context to abort mid-turn. Multi-turn
// state is the agent's concern; the TUI calls Run once per submission.
type Agent interface {
	Run(ctx context.Context, prompt string) iter.Seq2[Event, error]
}

// Event is the neutral representation of one agent event. A single
// Event typically carries ONE of: streamed text, a tool call, or a
// usage update.
type Event struct {
	// Text is the chunk produced by the model when Partial=true, or
	// the committed full text when Partial=false. The TUI accumulates
	// partials into the in-progress assistant message and Glamour-
	// renders the accumulated text on every update.
	Text    string
	Partial bool

	// ToolCalls lists tool invocations the model issued in this event.
	// ID is the stable function-call ID used for deduping across
	// partial + committed echoes.
	ToolCalls []ToolCall

	// Usage carries token counts. The TUI snapshots the most recent
	// non-nil value and reports it at turn end.
	Usage *Usage
}

// ToolCall describes a single tool invocation.
type ToolCall struct {
	ID   string
	Name string
	Args map[string]any
}

// Usage carries token counts for a turn.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// InjectableAgent is an optional capability: hosts whose agent
// supports mid-turn message injection (feeding a message INTO the
// currently-streaming turn's context, distinct from queueing for
// the next turn) implement it on their Agent type. The TUI checks
// the capability with a type assertion when
// Options.MidTurnInjectionMode == InjectIntoCurrent — without the
// capability, the mode silently falls back to QueueForNext (no
// runtime error).
//
// See R-CHAT-11 in requirements.md and design.md §3.3.
type InjectableAgent interface {
	Inject(message string) error
}
