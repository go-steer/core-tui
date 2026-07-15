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
// error info that should be surfaced inline in the chat. Kind
// drives client rendering decisions (e.g. retry affordance only
// when Retryable=true). Consumers tolerate unknown Kind values by
// treating them as TurnErrorUnknown.
type TurnError struct {
	Kind      string `json:"kind"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	Hint      string `json:"hint,omitempty"`
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
)
