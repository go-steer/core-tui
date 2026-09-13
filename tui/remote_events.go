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

// Remote-event payload types — the consumer-side surface for the
// SSE event-stream protocol (issue #40, spec v1.1.0 at
// docs/sse-event-stream-protocol.md). Hosts populate the matching
// optional fields on tui.Event (StatusUpdate, UsageUpdate, Inbox,
// TurnComplete, TurnError) when they consume push-mode SSE events
// from a server; core-tui's Update loop applies them to model
// state.
//
// JSON tags mirror the spec's snake_case payload field names
// exactly so host adapters can `json.Unmarshal` raw SSE data
// blocks directly into these structs without a translation layer.

package tui

import "time"

// StatusUpdate matches the spec §2.2 status-update payload. Used
// for session-level state changes — turn boundaries, model swap,
// permission mode change, provider tag change.
//
// Merge semantics: when a host populates Event.StatusUpdate, the
// consumer applies fields field-by-field — absent / zero-valued
// optional fields leave the existing state unchanged. TurnState is
// always present on every emission per spec. Optional fields use
// pointer types where the zero value would conflict with a
// meaningful empty / zero state (e.g. ContextPct = 0 means
// "fresh context", not "unknown").
type StatusUpdate struct {
	Model      string `json:"model,omitempty"`
	Provider   string `json:"provider,omitempty"`
	PermMode   string `json:"perm_mode,omitempty"`
	TurnState  string `json:"turn_state"`
	ContextPct *int   `json:"context_pct,omitempty"`
}

// Turn-state values from spec §2.2. Hosts MAY emit unknown values
// (forward-compat); consumers tolerate them by treating as the
// no-op idle state.
const (
	TurnStateIdle               = "idle"
	TurnStateStreaming          = "streaming"
	TurnStateAwaitingPermission = "awaiting_permission"
	TurnStateAwaitingElicit     = "awaiting_elicit"
)

// UsageUpdate matches the spec §2.3 usage-update payload — the
// cumulative session totals plus optional per-model breakdown. The
// per-model breakdown is the data side of #38 (the rendering side
// in /stats reads from a parallel local field that this update
// snapshots into).
//
// LastTurn (spec v1.1.1 addition, issue #57) carries authoritative
// per-turn tokens + cost for the just-completed turn. Optional —
// pre-v1.1.1 servers omit it; consumers back-annotate the tail
// assistant Message's footer when present so observer-mode
// (LiveAgent) sessions render the per-turn footer without needing
// finalizeTurn (which only fires on turnDoneMsg from the per-turn
// Run path).
type UsageUpdate struct {
	TokensInTotal  int                     `json:"tokens_in_total"`
	TokensOutTotal int                     `json:"tokens_out_total"`
	CostUSDTotal   float64                 `json:"cost_usd_total"`
	TurnsTotal     int                     `json:"turns_total"`
	ByModel        map[string]UsageByModel `json:"by_model,omitempty"`
	LastTurn       *UsageLastTurn          `json:"last_turn,omitempty"`
}

// UsageLastTurn is the per-turn payload attached to UsageUpdate.
// Cost is authoritative (server-side pricing layer, includes
// cache-discount + operator overrides). TokensInCached is optional —
// servers with cache-attribution wired (core-agent post-#248)
// populate it; older servers omit and consumers ignore.
//
// Issue #57 / spec v1.1.1.
type UsageLastTurn struct {
	TokensIn       int     `json:"tokens_in"`
	TokensInCached int     `json:"tokens_in_cached,omitempty"`
	TokensOut      int     `json:"tokens_out"`
	CostUSD        float64 `json:"cost_usd"`
	Model          string  `json:"model,omitempty"`
}

// UsageByModel is one entry in UsageUpdate.ByModel — per-model
// token counts, cost, and turn count for the cost-routing pitch
// of --agentic-tools (primary vs small model).
type UsageByModel struct {
	TokensIn  int     `json:"tokens_in"`
	TokensOut int     `json:"tokens_out"`
	CostUSD   float64 `json:"cost_usd"`
	Turns     int     `json:"turns"`
}

// InboxEvent matches the spec §2.4 inbox payload — operator-typed
// prompt transitioning between inbox states. The PromptID
// correlates queued/dequeued pairs and threads through to the
// matching TurnSummary / TurnError for the same prompt.
type InboxEvent struct {
	State    string    `json:"state"`
	PromptID string    `json:"prompt_id"`
	QueuedAt time.Time `json:"queued_at,omitempty"`
}

// Inbox state values from spec §2.4. Servers MAY emit unknown
// values for future states (e.g. "injected"); consumers MUST
// tolerate them (treat as no-op).
const (
	InboxStateQueued   = "queued"
	InboxStateDequeued = "dequeued"
)

// PauseEvent matches the spec §2.8 pause payload (v1.5.0) — the
// session's pause gate closing or opening. Consumers render a banner
// from it and switch the input line into "what do you want me to do
// instead?" mode. See pause.go for the capability side.
//
// Interrupted is set only on a paused event and says whether a turn
// was actually cancelled on the way in. Mode is set only on a resumed
// event and echoes the disposition the operator chose, so a second
// client watching the stream can render what happened rather than
// just that something did.
type PauseEvent struct {
	State       string    `json:"state"`
	Reason      string    `json:"reason,omitempty"`
	Interrupted bool      `json:"interrupted,omitempty"`
	Mode        string    `json:"mode,omitempty"`
	At          time.Time `json:"at"`
}

// Pause state values from spec §2.8. Hosts MAY emit unknown values;
// consumers MUST tolerate them (treat as no-op).
const (
	PauseStatePaused  = "paused"
	PauseStateResumed = "resumed"
)

// TurnSummary matches the spec §2.5 turn-complete payload —
// per-turn tokens + cost + latency + model. CostUSD is OPTIONAL
// in spec v1.1.0: servers that compute cost out-of-band (e.g.
// core-agent's pkg/agent doesn't know about internal/pricing)
// emit 0 here and rely on the immediately-following UsageUpdate
// to carry authoritative cost. Consumers correlate via PromptID.
type TurnSummary struct {
	PromptID  string  `json:"prompt_id"`
	Model     string  `json:"model"`
	TokensIn  int     `json:"tokens_in"`
	TokensOut int     `json:"tokens_out"`
	CostUSD   float64 `json:"cost_usd,omitempty"`
	LatencyMs int64   `json:"latency_ms"`
}

// TurnError matches the spec §2.6 turn-error payload — structured
// error info that should be surfaced inline in the chat. Consumers
// tolerate unknown Kind values by treating them as TurnErrorUnknown.
type TurnError struct {
	Kind    string `json:"kind"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`

	// Retryable carries the host's classification of the failure as
	// transient. core-tui parses it and exposes it to hosts, but
	// renders nothing for it: there is no retry action in the TUI to
	// put behind a retry affordance (issue #285, and see
	// renderTurnErrorBlock).
	Retryable bool `json:"retryable"`

	Hint string `json:"hint,omitempty"`
}

// TurnError kind constants from spec §2.6. Hosts MAY emit unknown
// values; consumers MUST treat unknown as TurnErrorUnknown.
const (
	TurnErrorConfig        = "config_error"
	TurnErrorAuth          = "auth_error"
	TurnErrorModelNotFound = "model_not_found"
	TurnErrorRateLimited   = "rate_limited"
	TurnErrorTransientNet  = "transient_network"
	TurnErrorUnknown       = "unknown"

	// TurnErrorCanceled (producer protocol 1.8.0) is a turn stopped on
	// purpose — an operator's interrupt, a shutdown, or a guardrail
	// cutting the turn short. Declared here because GuardrailTrip's
	// handling has to recognise it: a cancel a trip caused carries no
	// reason of its own, so rendering it beside the trip that explains
	// it stacks a contentless warning under a meaningful one.
	TurnErrorCanceled = "canceled"
)

// GuardrailTrip matches the spec §2.10 guardrail-trip payload
// (v1.13.0) — the watchdog or the cost ceiling deciding the session
// must stop. The agent refuses every turn from here until an operator
// resets it, which makes this the most consequential thing a host can
// be told and the reason it is worth a frame of its own.
//
// Non-terminal: it reports a session state change, not a turn's
// outcome. Through 1.12.0 a trip arrived as a TurnError, which meant
// a trip at a turn boundary produced a turn-error AND a turn-complete
// for one turn.
type GuardrailTrip struct {
	// Guardrail is GuardrailWatchdog or GuardrailCostCeiling. Hosts
	// MAY emit unknown values; consumers render the string.
	Guardrail string `json:"guardrail"`

	// Reason is the operator-facing explanation, and by convention it
	// already names the affordance that clears the halt, so it is
	// renderable verbatim rather than something to prefix advice onto.
	Reason string `json:"reason"`

	// HaltedTurn is true when the trip cut the in-flight turn short —
	// a turn-error of kind TurnErrorCanceled follows and this payload
	// is its only explanation. False means the turn was not cut: it
	// finished and turn-complete follows, or none was running.
	//
	// Always present on the wire, false included. A consumer must not
	// read absence as false; absence means a pre-1.13.0 producer,
	// which sends no guardrail-trip at all.
	HaltedTurn bool `json:"halted_turn"`
}

// Guardrail names from spec §2.10, shared with the producer's
// guardrail-reset endpoint so a host has one vocabulary.
const (
	GuardrailWatchdog    = "watchdog"
	GuardrailCostCeiling = "cost_ceiling"
)
