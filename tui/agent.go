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

	// Push-mode fields (issue #40, SSE event-stream spec v1.1.0 at
	// docs/sse-event-stream-protocol.md). Host adapters that
	// consume push events from a server (currently core-agent's
	// SSE /events stream) populate exactly one of these per Event
	// to carry the corresponding spec payload through to the TUI's
	// Update loop. All optional — legacy hosts that don't consume
	// push events leave them nil and the per-turn-inferred state
	// (via Usage / Model on this struct) keeps working unchanged.
	//
	// At most one of these is non-nil per Event in normal usage
	// (one SSE wire event → one Event), though multi-population
	// is tolerated — handlers fire independently.
	StatusUpdate *StatusUpdate
	UsageUpdate  *UsageUpdate
	Inbox        *InboxEvent
	TurnComplete *TurnSummary
	TurnError    *TurnError
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

	// LatencyMs is the wall-clock time (in milliseconds) the tool
	// call took, measured from dispatch to result received. Optional
	// — 0 suppresses the inline `[2.4s]` badge and dialog chip.
	//
	// Adapters MAY populate this field directly; core-tui also
	// auto-plucks the value from Response["latency_ms"] when this
	// field is 0, because core-agent's PR #278 emits it inside the
	// response map (ADK's Tool.Run has no write access to the
	// enclosing session.Event's CustomMetadata, so the map itself is
	// the only sidecar channel). Either surface works — hosts pick
	// whichever fits their pipeline.
	//
	// Consumer side of core-tui #60 / SSE spec v1.2.0.
	LatencyMs int64

	// Savings is the digest wrap's per-call reduction — original vs.
	// digested byte / token counts, plus the router's dispatch
	// decision (structural pruner, LLM subagent, or bypassed
	// passthrough). Nil when the host didn't dispatch through a
	// digest wrap (or the response arrived pre-v1.3.0 without the
	// sidecar). Renderers show a compact inline chip on the tool row
	// and a full block in the tool-call detail overlay.
	//
	// Same auto-pluck pattern as LatencyMs: adapters MAY populate
	// this field directly; core-tui also plucks it from
	// Response["savings"] when this field is nil, because
	// core-agent's PR #290 emits the map inside the response
	// payload (same ADK constraint that shipped latency_ms there).
	//
	// Consumer side of SSE spec v1.3.0 / core-agent #223 Phase 4.
	Savings *ToolSavings
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

// LiveAgent is an optional capability for hosts whose agent isn't
// driven by per-turn Run calls — remote-attached daemons running
// autonomously, observer-mode TUIs watching MCP-server-triggered
// activity, etc. (issue #22). When implemented, core-tui spawns a
// single long-lived goroutine at startup that ranges over
// Events(ctx) and feeds the chat view from every event,
// regardless of whether the operator typed.
//
// Precedence: LiveAgent WINS over the per-turn Run path. Hosts
// satisfying both interfaces have Run silently skipped — operator
// submissions flow through InjectableAgent.Inject when available,
// otherwise the TUI logs a one-time "read-only view" system note
// and discards the typed text.
//
// Semantics (locked during PR review):
//   - ctx cancellation mid-iter: implementations stop yielding;
//     no final (zero, ctx.Err()) yield is required.
//   - Transient errors (non-nil err): core-tui surfaces them in
//     the chat as a RoleError row and KEEPS draining. The
//     iterator decides whether to keep yielding events.
//   - Iterator end (Events returns / stops yielding): core-tui
//     renders a "Disconnected — Ctrl+C to quit" system row and
//     keeps the program alive so the operator can read scrollback.
//   - Reconnect: implementation-internal. core-tui calls Events
//     exactly once at startup and trusts the iterator to handle
//     its own reconnection / replay semantics.
//   - Turn-end commit: Event{Text: ..., Partial: false} commits
//     the accumulated in-progress assistant text (matches the
//     existing Run-path convention). Hosts that forget to flush
//     a non-partial close cause a slightly-laggy commit — never
//     corruption.
//   - Spinner: active whenever the most recent partial Text
//     arrived AFTER the most recent commit Text (i.e. tokens are
//     in flight). Idle when committed and idle, even though the
//     event stream itself is "always live".
//
// See docs/remote-tui-observer-mode.md (in the core-agent repo)
// for the architectural motivation + adapter sketch.
type LiveAgent interface {
	Events(ctx context.Context) iter.Seq2[Event, error]
}

// PermanentStreamError, when implemented by an error returned from
// LiveAgent.Events, signals a condition the TUI can't recover from by
// retrying (session gone, auth revoked). Adapters wrap upstream
// HTTP 404 / 401 / 403 errors — or any locally-detected permanent
// condition — with this interface so the TUI can flip to a terminal
// "session unavailable" row instead of looping forever on the
// reconnect path (issue #51).
//
// If the interface isn't implemented, the TUI falls back to a small
// substring heuristic ("status 404" / "status 401" / "status 403") so
// existing adapters that already stringify the HTTP status keep the
// same behavior without needing an immediate update.
type PermanentStreamError interface {
	error
	PermanentStreamErr() bool
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

// RemoteInterrupter is an optional capability: hosts whose agent runs
// remotely (LiveAgent observer mode against a daemon) implement it so
// the /interrupt slash can cancel an in-flight turn even when the TUI
// has no local per-turn context to cancel.
//
// Without this capability, /interrupt short-circuits with "no turn in
// flight" on remote sessions — the local Run-path gate keys off
// `m.cancelTurn`, which is only set for operator-initiated turns
// through the per-turn iterator. Autonomous turns driven by the daemon
// (k8s-event-watcher injects, runaway tool loops, etc.) stream
// through LiveAgent but never populate cancelTurn, leaving the
// operator without a way to stop them from the TUI even when the
// daemon exposes a cancel endpoint.
//
// Implementations MAY block briefly on network I/O; the TUI calls
// Interrupt off the Update-loop path so it doesn't stall the UI. A
// short deadline via ctx is appropriate — a hung interrupt is worse
// than a failed one because it leaves the operator uncertain whether
// their input landed. Errors surface as an inline RoleError row.
//
// Same optional-capability pattern as LiveAgent / InboxDrainer /
// WakeRequester — the TUI type-asserts at slash-fire time and falls
// back to the existing "no turn in flight" message when the interface
// isn't implemented.
type RemoteInterrupter interface {
	Interrupt(ctx context.Context) error
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
