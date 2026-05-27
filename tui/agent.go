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

	// ToolResults lists tool completions delivered alongside or after
	// the corresponding ToolCalls. ID matches the call's ID so the
	// TUI can attach the result to the right tool row. A populated
	// Error string indicates failure; a populated Response carries
	// the structured payload (per-tool shape — `content` for
	// read_file, `stdout`/`stderr` for bash, `bytes_written` for
	// write_file, etc.). The renderer picks the relevant keys.
	ToolResults []ToolResult

	// Usage carries token counts. The TUI snapshots the most recent
	// non-nil value and reports it at turn end.
	Usage *Usage

	// CostUSD is the dollar cost for THIS event's usage (typically
	// the final per-turn cost when the agent emits its usage event).
	// 0 suppresses the per-turn footer's "$X" segment. The TUI also
	// snapshots the most recent positive value and reports it at
	// turn end alongside Usage / Model.
	CostUSD float64

	// Model is the resolved model identifier for THIS event. Adapters
	// populate it on the usage event so the per-turn footer ("◇ X
	// · in · out · $X · 4s") and status sidebar can reflect the live
	// agent. Empty events leave m.currentModel unchanged.
	Model string
}

// ToolCall describes a single tool invocation.
type ToolCall struct {
	ID   string
	Name string
	Args map[string]any
}

// ToolResult describes a single tool completion. ID correlates
// with the originating ToolCall.ID. Error is non-empty iff the
// tool failed; Response carries the per-tool structured payload
// when the call succeeded. The TUI uses Name + Response in the
// per-tool result renderer (renderToolResult) — adapters should
// preserve the tool's native shape rather than pre-flattening.
type ToolResult struct {
	ID       string
	Name     string
	Response map[string]any
	Error    string
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

// InboxDrainer is an optional capability for hosts whose agent
// queues operator-injected messages in an internal inbox that's
// distinct from the per-turn prompt. Combined with InjectableAgent
// it gives core-tui the ability to drive an auto-continue loop on
// hosts whose runner is opaque (ADK, anywhere the iterator-shaped
// runner owns its own loop and doesn't expose mid-turn hooks).
//
// DrainInbox returns the currently queued messages AND removes
// them from the inbox in one call — semantics matter, since the
// TUI then formats + submits them as a synthetic turn. A nil /
// empty return is the signal "nothing to auto-continue, idle."
//
// PendingInboxCount is a non-destructive peek used for sizing /
// UI hints; it may return a coarse upper bound if the host can't
// precisely count without mutating state.
//
// See issue #9 and Options.MidTurnInjectionMode ==
// AutoContinueFromInbox.
type InboxDrainer interface {
	DrainInbox() []string
	PendingInboxCount() int
}

// WakeRequester is an optional capability: hosts whose agent
// emits "I need the operator's attention" signals (typically from
// background sub-agents reporting completion or asking for input)
// implement it. WakeRequested returns a receive-only channel; each
// receive triggers a transient toast banner in the TUI.
//
// The TUI subscribes once at startup via a goroutine that ranges
// over the channel; the host owns channel lifecycle (closing the
// channel is fine — the goroutine exits cleanly). The interface
// makes no promise about coalescing: rapid back-to-back wakes will
// render multiple toasts in sequence.
//
// See R-WAKE-1 in requirements.md and design.md §3.3.
type WakeRequester interface {
	WakeRequested() <-chan struct{}
}

// Content is a neutral structured-prompt fragment for ContentRunner
// (R-CHAT-12). Adapters translate their host's native content
// representation (ADK Content, anthropic Message, etc.) into / out
// of this shape so the TUI stays framework-agnostic.
//
// Role is one of "user" / "assistant" / "system" / "tool". Text is
// the primary payload — structured parts (tool calls, function
// responses, image refs) ride alongside in Parts. Both fields may
// be set; a renderer or downstream agent decides precedence.
type Content struct {
	Role  string
	Text  string
	Parts []ContentPart
}

// ContentPart is one named-kind fragment within a Content (tool
// call, tool response, image, etc.). Kind is a host-defined string
// — adapters agree with their backend on the vocabulary. Data is
// the raw payload, typed as `any` so adapters can pass through
// structured values without forcing a serialization here.
type ContentPart struct {
	Kind string
	Data any
}

// ContentRunner is an optional Agent capability: when implemented,
// adapters can drive turns from a structured `[]Content` slice
// instead of a single prompt string. Used by retry / replay flows
// where the host has already constructed the conversation context
// programmatically.
//
// Detected via type assertion; the TUI's default submit flow still
// uses Agent.Run(ctx, prompt) until a host wires a UI affordance
// that invokes RunWithContents.
//
// See R-CHAT-12 in requirements.md and design.md §3.3.
type ContentRunner interface {
	RunWithContents(ctx context.Context, contents []Content) iter.Seq2[Event, error]
}
