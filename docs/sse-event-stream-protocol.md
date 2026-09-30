# SSE Event-Stream Protocol

The wire-format contract between core-tui (consumer) and any server (producer — core-agent today) for the `/events` SSE stream. This document specifies what bytes the client expects to see; future clients (web TUI, IDE plugin, mobile) read the same spec.

**Status:** Phase 1 — additive-only. See [core-tui #40](https://github.com/go-steer/core-tui/issues/40) and [core-agent #115](https://github.com/go-steer/core-agent/issues/115) for the phased-rollout context.

**Protocol version:** `1.15.0`. Bumped on changes per the [Versioning](#versioning) rules below.

---

## 1. Transport

- **Endpoint:** existing SSE endpoint at `/sessions/{sid}/events` (path may differ per server; the protocol is endpoint-agnostic).
- **Encoding:** standard [Server-Sent Events](https://html.spec.whatwg.org/multipage/server-sent-events.html) — `text/event-stream`.
- **Framing:** each event is `event: <name>\ndata: <json-payload>\n\n`. The `data:` value is a single JSON document on one logical line (SSE allows multi-line `data:` continuation; this protocol does not use it).
- **Heartbeat / keepalive:** optional `: comment` lines per SSE convention. Clients MUST tolerate arbitrary comment lines.

Example event on the wire:

```
event: status-update
data: {"model":"gemini-2.5-pro","provider":"vertex","perm_mode":"default","turn_state":"idle"}

```

---

## 2. Event types

Ten event types are defined in this document. Each section specifies: when the server emits the event, the payload schema (snake_case JSON), and a representative example.

### 2.1 `capabilities`

**When emitted:** as the **first** event on every newly-opened stream, before any other event. Required.

**Purpose:** lets the client know which event types the server speaks, so the client can decide whether to subscribe to push-style state (Phase 2 `Auto` mode) or fall back to polling.

**Payload:**

| Field | Type | Required | Description |
|---|---|---|---|
| `protocol_version` | string (semver) | yes | Version the server speaks. Clients compare against the version they implement; see [Versioning](#versioning). |
| `event_types` | array of strings | yes | Names of event types the server emits on this stream. Clients MUST tolerate unknown names (forward-compat). Servers MAY also list **logical** sub-types that ride on a multiplexed event name (e.g. `stream-chunk` / `tool-call` / `tool-result` carried on the legacy `agent` wire event) so clients can detect capability without inspecting frame internals. |
| `server` | string | no | Free-form server identifier (e.g. `"core-agent/0.4.2"`). Diagnostic only. |
| `features` | object (v1.4.0+) | no | Feature-flag map derived from live runtime state. Keys map to booleans; consumers treat absent keys as "off / unknown" and unknown keys as forward-compat additions. See [Feature keys](#feature-keys) below. |
| `slash_commands` | array of strings (v1.4.0+) | no | Dynamic list of server-side slash-command names accepted at `POST /sessions/.../slash/<name>`. Derived from the agent's capability-interface presence (e.g. `CompactSlashProvider` → `"compact"`), not from a registry table. Clients use this to render only the slashes that will succeed against the connected agent. Absent = client falls back to its hardcoded list. |
| `agent` | object (v1.4.0+) | no | Producer's own identity — name, version, description, model, provider, url. Consolidates fields previously scattered across `/.well-known/agent-card.json`, `GET /sessions/.../status`, and the free-form `server` banner. Every sub-field is optional; consumers render only what's present. See [`agent`](#capabilitiesagent-v140) below. |
| `caller_id` | string (v1.4.0+) | no | The resolved caller identity after the server's auth middleware ran. Display hint for the client; the canonical source (with admin flag + auth source discriminator) is `GET /whoami`. Absent when the caller couldn't be resolved. |

#### Feature keys

Suggested initial keys advertised on `features`. Servers MAY add unknown keys; clients MUST tolerate them.

| Key | Meaning |
|---|---|
| `multi_session` | Server enforces per-session ACLs (many-session tenancy). |
| `perms_stream` | Agent supports `/perms/stream` SSE + `/perms/respond` POST for HTTP-driven permission prompts. |
| `cost_ceiling` | Agent has a per-turn or per-session cost ceiling wired. |
| `observer_mode` | Producer exposes a LiveAgent observer surface. |
| `mcp` | Agent has one or more MCP servers declared. |
| `specialists` | Agent supports `POST /slash/subagent` (subagent spawn). |
| `cross_daemon` | Server hosts the peer registry — clients can render a multi-daemon fleet picker. |
| `interrupt` | Agent supports `POST /interrupt` (ESC-to-cancel). |
| `guardrails` | Agent exposes a guardrail surface the operator can read and reset — a tripped behavioral watchdog or cost ceiling can be cleared without restarting the agent. Distinct from `cost_ceiling`, which says only whether a spend bound is armed. |
| `pause` (v1.5.0+) | Agent has a pause gate: `POST /pause` + `POST /resume` work, `POST /interrupt` parks the loop rather than only cancelling, and the session emits `pause` events (§2.8). Clients gate their "what should I do instead?" resume prompt on this — offering a steer against a producer that can't hold promises a park that never happens. |

#### `capabilities.agent` (v1.4.0+)

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | no | Human-readable agent name (e.g. `"core-agent"`, `"mast"`). Falls back to the first registrant's app name when the agent card doesn't override. |
| `version` | string | no | Agent version (typically the ldflag-injected build version, e.g. `"v2.8.0-dev"`). |
| `description` | string | no | One-line summary the agent card publishes and the model sees. |
| `model` | string | no | Active model identifier (e.g. `"gemini-3.1-pro"`). Same source as `status-update.model`. |
| `provider` | string | no | Provider routing tag (e.g. `"vertex"`, `"anthropic"`). Reserved; empty on producers that don't advertise a provider yet. |
| `url` | string | no | Canonical external URL for this agent (from `AgentCardConfig.ExternalURL`). Empty when the server derives URLs per-request from the Host header. |

Example:

```json
{
  "protocol_version": "1.15.0",
  "event_types": ["status-update", "usage-update", "inbox", "turn-complete", "turn-error", "pause", "wake", "guardrail-trip", "stream-chunk", "tool-call", "tool-result"],
  "server": "core-agent/2.9.0-dev",
  "features": {
    "multi_session": true,
    "perms_stream": true,
    "mcp": true,
    "specialists": true,
    "cross_daemon": false,
    "interrupt": true,
    "pause": true,
    "guardrails": true,
    "cost_ceiling": false,
    "observer_mode": false
  },
  "slash_commands": ["btw", "compact", "done", "replan", "subagent"],
  "agent": {
    "name": "core-agent",
    "version": "v2.9.0-dev",
    "description": "Autonomous coding assistant for the core-agent repository.",
    "model": "gemini-3.1-pro",
    "url": "https://agents.example.com/core-agent"
  },
  "caller_id": "alice@example.com"
}
```

(`stream-chunk`, `tool-call`, `tool-result` are pre-existing event types that predate this protocol document; listed here so the example reflects real server output.)

`event_types` is also how a client detects the event types added after v1.4.0. `pause`, `wake` and `guardrail-trip` are all purely additive, and a pre-v1.5.0 / pre-v1.7.0 / pre-v1.13.0 producer omits the name and never sends the frame — so a client written against the newer version degrades to silence on that surface rather than to an error.

`guardrail-trip` is the one place where degrading to silence costs something, and §2.10 spells out why: the 1.13.0 producer stopped suppressing the `canceled` turn-error that accompanies a halt, so a client that doesn't know the new frame renders a contentless cancellation where it used to render nothing at all.

Note what `event_types` does and does not tell you. It is the list of frames the **server** knows how to emit, not a claim about the **agent** behind it: a v1.5.0 server lists `pause` whether or not the agent it is serving can actually hold its loop. `features` is the runtime-capability half. So a client offering a pause control gates it on `features.pause`, and a client rendering pause *state* it receives gates that on `"pause"` in `event_types`. `wake` has no paired feature key, because there is no capability to decline — a wake either fires or it doesn't.

### 2.2 `status-update`

**When emitted:** on any state change to the session-level status surface — turn start/end, model swap (`/model` slash), permission mode change (Shift+Tab cycle), provider change. Also emitted once after the `capabilities` event on stream open, so clients have a complete state snapshot to render immediately.

**Payload:**

| Field | Type | Required | Description |
|---|---|---|---|
| `model` | string | no | Active model identifier (e.g. `"gemini-2.5-pro"`). Empty / absent = not yet known. |
| `provider` | string | no | Provider tag (`"vertex"`, `"anthropic"`, `"openai"`, etc.). Empty / absent = not yet known. |
| `perm_mode` | string | no | One of `"default"`, `"acceptEdits"`, `"plan"`, `"bypassPermissions"`. |
| `turn_state` | string | yes | One of `"idle"`, `"streaming"`, `"awaiting_permission"`, `"awaiting_elicit"`. |
| `context_pct` | integer | no | 0–100. Context-window fill. Absent if the server can't compute it. |
| `capabilities` | object (v1.4.0+) | no | Hot capability changes — same shape as the [`capabilities`](#21-capabilities) frame, applied via merge semantics. Any field the server sets updates the consumer's cached capabilities; fields the server omits stay as-is. Example use: an MCP server registers mid-session and `features.mcp` flips true, or a new slash-command provider gets wired. Servers MAY defer emitting this (v1.4.0 producers can skip it and rely on the client re-fetching); consumers MUST tolerate its absence. |

All fields are independently optional except `turn_state`. Clients applying a partial payload MUST merge into local state — fields not present in the event are unchanged. Merge extends into nested objects: `capabilities.features` merges key-by-key, so a hot update flipping `features.mcp = true` does NOT clobber previously-advertised `features.perms_stream = true`.

**The stream-open snapshot reports a turn in flight (v1.12.0+).** The `status-update` sent after `capabilities` carries `turn_state: "streaming"` whenever a turn is executing on the producer at that moment — **including when the session is also paused** (§2.8), since a park can land while the turn it interrupted is still unwinding. Before 1.12.0 the reference producer had no run-loop signal to read and seeded `"idle"` for every session, mid-turn or not, so a client attaching to a running session was told nothing was happening until the turn's own frames said otherwise. That matters more than it sounds, because typed frames are live fan-out with no replay: for a session born together with its first turn (a watcher creating one per incident, say), the snapshot is the only turn-state information an attaching client ever receives, and a turn blocked on a long tool call emits nothing else to correct it. A client MUST NOT read `"paused"` state as implying `"idle"`, or `"streaming"` as implying the gate is open — the two are independent, and the parked-but-still-finishing window is exactly where they disagree. The REST counterpart, `GET /status`'s `turn_in_flight`, shipped in the same revision and is not specified here (§7). Detection is by `protocol_version` only; a pre-1.12.0 producer's `"idle"` seed is indistinguishable on the wire from a genuinely idle session.

Example:

```json
{
  "model": "gemini-2.5-pro",
  "provider": "vertex",
  "perm_mode": "default",
  "turn_state": "streaming",
  "context_pct": 42
}
```

### 2.3 `usage-update`

**When emitted:** after each turn finalizes. May also be emitted on stream open with the cumulative session state so clients have a starting snapshot.

**Payload:**

| Field | Type | Required | Description |
|---|---|---|---|
| `tokens_in_total` | integer | yes | Cumulative session input tokens. |
| `tokens_out_total` | integer | yes | Cumulative session output tokens. |
| `cost_usd_total` | number | yes | Cumulative session cost in USD. |
| `turns_total` | integer | yes | Cumulative completed-turn count. |
| `by_model` | object | no | Per-model breakdown (see below). Absent = server doesn't bucket by model (pre–#38 servers); clients render the aggregate only. |
| `last_turn` | object | no | Authoritative per-turn tokens + cost for the just-completed turn. Added in v1.1.1 to close the observer-mode footer gap (core-tui #57). Absent on pre-v1.1.1 servers; clients degrade to using the `cost_usd`-defers-to-`usage-update` pattern below without last-turn back-annotation. |

`by_model` entries (key = model identifier):

| Field | Type | Required | Description |
|---|---|---|---|
| `tokens_in` | integer | yes | Per-model input tokens. |
| `tokens_out` | integer | yes | Per-model output tokens. |
| `cost_usd` | number | yes | Per-model cost in USD. |
| `turns` | integer | yes | Turns routed to this model. |

`last_turn` fields (v1.1.1+):

| Field | Type | Required | Description |
|---|---|---|---|
| `tokens_in` | integer | yes | Turn input tokens (matches the preceding `turn-complete.tokens_in`). |
| `tokens_in_cached` | integer | no | Cache-hit portion of `tokens_in` when server has cache attribution wired (core-agent post-#248). Absent = server doesn't distinguish. |
| `tokens_out` | integer | yes | Turn output tokens. |
| `cost_usd` | number | yes | Authoritative per-turn cost. Server-side pricing has already applied cache-discount + operator overrides. |
| `model` | string | no | Model the turn was routed to. Useful when a turn spans multiple models (subtask fan-out); typically matches `turn-complete.model` for simple turns. |

**Why `last_turn` is on `usage-update` and not `turn-complete`:** the "cost is optional on turn-complete" convention (see §2.5) exists because some server architectures compute cost out-of-band, so `turn-complete` fires before cost is known. `usage-update` fires AFTER pricing has run, so it's the natural home for authoritative per-turn cost. Clients that need per-turn footers in observer mode (LiveAgent) back-annotate the tail assistant message from `last_turn`. See core-tui issue #57 for the reference client implementation.

Example (v1.1.1 server with cache attribution + authoritative cost):

```json
{
  "tokens_in_total": 5557,
  "tokens_out_total": 123,
  "cost_usd_total": 0.0126,
  "turns_total": 2,
  "by_model": {
    "gemini-3.5-flash": {"tokens_in": 5557, "tokens_out": 123, "cost_usd": 0.0126, "turns": 2}
  },
  "last_turn": {
    "tokens_in": 2806,
    "tokens_in_cached": 2200,
    "tokens_out": 87,
    "cost_usd": 0.0058,
    "model": "gemini-3.5-flash"
  }
}
```

### 2.4 `inbox`

**When emitted:** when an operator-typed prompt transitions between inbox states — queued (server received but not yet routed to the model), dequeued (routed). Closes the regression noted in core-tui [#35](https://github.com/go-steer/core-tui/issues/35) where remote TUIs lose the "your input was received" confirmation.

**Payload:**

| Field | Type | Required | Description |
|---|---|---|---|
| `state` | string | yes | `"queued"` or `"dequeued"`. Future states (e.g. `"injected"`) MAY be added; clients MUST tolerate unknown values. |
| `prompt_id` | string | yes | Server-assigned identifier so client can correlate `queued` → `dequeued` pairs. |
| `queued_at` | string (RFC 3339) | no | Timestamp when the prompt entered the inbox. Diagnostic. |

Example:

```json
{"state": "queued", "prompt_id": "p-9c4a", "queued_at": "2026-06-07T19:42:11Z"}
```

### 2.5 `turn-complete`

**When emitted:** once per turn, immediately after the final agent output for that turn has streamed (i.e., after the last `stream-chunk` for the turn but before the next turn's events).

**Payload:**

| Field | Type | Required | Description |
|---|---|---|---|
| `prompt_id` | string | yes | The prompt that drove this turn (matches the `inbox` event's `prompt_id`). |
| `model` | string | yes | Model that completed this turn. |
| `tokens_in` | integer | yes | Turn input tokens. |
| `tokens_out` | integer | yes | Turn output tokens. |
| `cost_usd` | number | **no** | Turn cost in USD. **See note on cost below.** |
| `latency_ms` | integer | yes | Wall-clock time from turn start to last token. |

**Note on `cost_usd`** (clarified in protocol v1.1.0): some server architectures compute cost in a separate pricing module that runs asynchronously after the turn finalizes (e.g. core-agent's `pkg/agent` doesn't know about `internal/pricing`). Such servers MAY omit `cost_usd` from `turn-complete` and rely on the immediately-following `usage-update` event to carry authoritative cost — both cumulative session cost and the new turn's delta. Clients that need per-turn cost SHOULD correlate `turn-complete` with the next `usage-update` via `prompt_id` (or by ordering: `turn-complete` precedes its matching `usage-update`). Clients MUST handle absence of `cost_usd` on `turn-complete` without crashing — render `0`, `"—"`, or defer to the `usage-update`-derived value per local preference.

Servers that DO have pricing in-band SHOULD populate `cost_usd` for client convenience.

Example (server with pricing in-band):

```json
{
  "prompt_id": "p-9c4a",
  "model": "gemini-2.5-pro",
  "tokens_in": 2806,
  "tokens_out": 87,
  "cost_usd": 0.0067,
  "latency_ms": 4521
}
```

Example (server that defers cost to the following `usage-update`):

```json
{
  "prompt_id": "p-9c4a",
  "model": "gemini-2.5-pro",
  "tokens_in": 2806,
  "tokens_out": 87,
  "latency_ms": 4521
}
```

### 2.6 `turn-error`

**When emitted:** on any failure in the turn pipeline that should be surfaced to the operator — config error, auth failure, model not found, rate limit, transient network error. The contract is **"if something is wrong, tell the operator"** (per core-tui [#37](https://github.com/go-steer/core-tui/issues/37)); errors that should NOT surface (transient retries that succeeded on retry, internal scheduling) MUST NOT emit this event.

**Payload:**

| Field | Type | Required | Description |
|---|---|---|---|
| `kind` | string | yes | One of the values in the [Error kinds](#error-kinds) table below. Drives client rendering (icon, retry button visibility). |
| `code` | string | no | Upstream-specific error code (e.g. `"NOT_FOUND"`, `"429"`, `"INVALID_ARGUMENT"`). Free-form. Diagnostic. |
| `message` | string | yes | Human-readable error text. Single sentence; punctuation included. |
| `retryable` | bool | yes | True = client may surface a "retry" affordance. False = operator config fix needed. |
| `hint` | string | no | Actionable next-step hint (e.g. `"Check vertex.location and model name; some models are global-only."`). |

#### Error kinds

| Kind | Meaning | Retryable? |
|---|---|---|
| `config_error` | URL builder failure, missing required env var, malformed config | false |
| `auth_error` | ADC / credentials / IAM denied | false |
| `model_not_found` | Wrong model name, wrong location, no allowlist | false |
| `rate_limited` | Provider quota exceeded; backoff applies | true |
| `transient_network` | DNS, TCP reset, 5xx after retry budget exhausted, a model call that hit its deadline | true |
| `cost_ceiling` | A per-turn or per-session cost ceiling halted the session; the operator must reset it. See the note below — core-agent no longer puts this on the stream (v1.13.0). | false |
| `watchdog` | The behavioral watchdog halted the session on a runaway signal; the operator must reset it. Same note as `cost_ceiling`. | false |
| `canceled` (v1.8.0+) | The turn was stopped on purpose — an operator interrupt, a shutdown, or a guardrail cutting it short in flight (§2.10) | false |
| `unknown` | Catch-all for errors the server can't categorize | server's call |

Clients SHOULD render any unknown `kind` value as if it were `unknown` (forward-compat). Clients MUST NOT crash on unknown kinds.

**`canceled` (v1.8.0+).** Every cancellation is a deliberate stop, so `retryable` is false: re-running the work is the opposite of what was asked for. core-agent emits it as `code: "CANCELED"`, `message: "turn canceled"`. Before 1.8.0 the same cancel arrived as `transient_network` / `retryable: true` — the classifier saw `context.Canceled` and could not tell a killed turn from a dropped connection — so a client keying a retry affordance off `retryable` offered to re-run exactly the work an operator had just stopped (§5 shows the sequence). Note the asymmetry the revision deliberately kept: a model call that ran out of time is still `transient_network` / `retryable: true`, because nobody asked for a deadline to fire. A cancel and a timeout now sit on opposite sides of the flag. The frame carries no cause, and that is by design — since 1.13.0 a cancel may be a guardrail's doing, and the `guardrail-trip` immediately before it is what says so (§2.10).

**`cost_ceiling` and `watchdog`** predate this table: producers emitted them from go-steer/core-agent#145 and go-steer/core-agent#623 respectively without a version bump, which the additive-enum rule in §3 permits. They are listed so the table matches what a pre-1.13.0 producer actually sends. Since 1.13.0 core-agent reports the trip as `guardrail-trip` instead, and a later turn refused because the session is still halted short-circuits above the point where a turn-error is emitted — so on a 1.13.0 core-agent stream neither value appears at all. They stay in the table because an older producer sends them and another producer may.

Example:

```json
{
  "kind": "model_not_found",
  "code": "NOT_FOUND",
  "message": "Publisher Model `projects/.../locations/us-central1/publishers/google/models/gemini-3.1-pro-preview-customtools` was not found.",
  "retryable": false,
  "hint": "Check vertex.location and model name; some models are global-only."
}
```

### 2.7 `tool-result`

**When emitted:** after a tool call completes (success or failure). Predates this document (see §2.1's note on pre-existing event types listed in `capabilities.event_types`); formally documented here in v1.2.0 to specify the `latency_ms` sidecar key added in the same revision.

**Payload:** the per-tool response map, shape governed by the tool itself (`content` for read_file, `stdout`/`stderr`/`exit_code` for bash, `bytes_written` for write_file, etc.). Clients MUST tolerate unknown keys and MUST NOT crash on missing per-tool fields.

**Wall-clock sidecar (v1.2.0+):**

| Field | Type | Required | Description |
|---|---|---|---|
| `latency_ms` | integer | no | Wall-clock time (in milliseconds) from tool dispatch to result received. Servers with round-trip timing SHOULD populate this; clients render as e.g. `[2.4s]` under the tool row. Absent on pre-v1.2.0 servers; clients degrade to no badge. |

**Digest-wrap savings sidecar (v1.3.0+):**

| Field | Type | Required | Description |
|---|---|---|---|
| `savings` | object | no | Per-call reduction produced by a digest wrap layer (e.g. core-agent's MCP wrap in `pkg/mcp/digest_wrap.go`). Absent when the call did not dispatch through a wrap. Clients render as an inline `[12k→2k tok · struct]` chip on the tool row and a chip in the tool-call detail overlay header. Absent on pre-v1.3.0 servers; clients degrade to no chip. |

The `savings` object carries the following fields:

| Field | Type | Required | Description |
|---|---|---|---|
| `path` | string | yes | Router decision. One of `structural_json` (deterministic JSON pruner), `llm_fallback` (small-tier LLM subagent digested the payload), or `passthrough` (payload was under the wrap's threshold — no reduction). |
| `original_bytes` | integer | yes | Serialized byte size of the raw payload BEFORE digesting. |
| `digest_bytes` | integer | yes | Serialized byte size of the digest handed back to the model. |
| `original_tokens_est` | integer | yes | Estimated original token count. Producers SHOULD use the standard 4-char-per-token heuristic; consumers treat as approximate (±15%). |
| `digest_tokens_est` | integer | yes | Estimated digest token count. Same heuristic. |
| `subagent_model` | string | no | On `llm_fallback` only: the small-tier model ID that produced the digest. Zero-valued on structural / passthrough. |
| `subagent_input_tokens` | integer | no | On `llm_fallback` only: subagent's input tokens. |
| `subagent_output_tokens` | integer | no | On `llm_fallback` only: subagent's output tokens. |

Passthrough calls SHOULD emit the sidecar for parity (so clients can count "how many calls skipped the wrap") but the compact display renderer suppresses the chip since there's no reduction to report.

**Why the response map, not `CustomMetadata`.** The ergonomic sidecar channel would have been `session.Event.CustomMetadata`, but ADK constructs the `tool-result` event *after* `tool.Run` returns — `CustomMetadata` isn't writable from inside `Run`. The response map itself IS writable, and both the remote and embedded core-agent adapters copy the whole map through to `tui.ToolResult.Response` verbatim, so a well-known sidecar key rides both transports without any per-adapter plumbing. Both `latency_ms` (v1.2.0) and `savings` (v1.3.0) use this channel; future sidecars SHOULD do the same.

Example (success + latency):

```json
{
  "content": "package main\n\nfunc main() {}\n",
  "latency_ms": 2412
}
```

Example (failure — error rides its own channel via ADK, latency still stamped when available):

```json
{
  "error": "no such file or directory",
  "latency_ms": 47
}
```

Example (structural digest wrap + latency):

```json
{
  "digest": "...compressed payload...",
  "raw_bytes": 12345,
  "method": "structural_json",
  "call_id": "toolcall-abc",
  "latency_ms": 720,
  "savings": {
    "path": "structural_json",
    "original_bytes": 12345,
    "digest_bytes": 2100,
    "original_tokens_est": 3086,
    "digest_tokens_est": 525
  }
}
```

Example (LLM-subagent digest wrap + latency):

```json
{
  "digest": "The pod is CrashLoopBackOff because /entrypoint.sh is missing.",
  "raw_bytes": 32100,
  "method": "llm_fallback",
  "call_id": "toolcall-xyz",
  "latency_ms": 2412,
  "savings": {
    "path": "llm_fallback",
    "original_bytes": 32100,
    "digest_bytes": 210,
    "original_tokens_est": 8025,
    "digest_tokens_est": 52,
    "subagent_model": "gemini-2.5-flash",
    "subagent_input_tokens": 400,
    "subagent_output_tokens": 150
  }
}
```

### 2.8 `pause` (v1.5.0+)

**When emitted:** when the session's pause gate closes or opens. A paused session is one where no NEW turn starts until someone resumes — a different fact from "no turn is running", since an idle agent picks up the next queued prompt on its own and a paused one does not. Producers emit one frame per transition, on every path into and out of the gate: an explicit `POST /pause`, a `POST /interrupt` that parks the loop, and every `POST /resume` regardless of disposition.

Emitted for **every** transition regardless of which client caused it, and emitted by the agent rather than by the request handler, so a park triggered in-process — an embedded TUI, a library caller, a scheduler — reaches remote watchers identically to one driven over HTTP. A second client watching the same session learns that someone else parked the agent without polling.

Detection has two halves that answer different questions — see the note under §2.1. `"pause"` in `event_types` says the server speaks the frame; `features.pause` says the agent behind it can actually hold. A client that renders received pause state checks the first; a client that offers the operator a pause or resume control checks the second, because offering a park against a producer that cannot hold promises something that will not happen.

**Payload:**

| Field | Type | Required | Description |
|---|---|---|---|
| `state` | string | yes | `"paused"` or `"resumed"`. Future states MAY be added; clients MUST tolerate unknown values and treat them as no-ops rather than guessing. |
| `reason` | string | no | Human-readable cause, shown verbatim in the client's banner — `"operator interrupt"`, `"cost ceiling reached"`. Absent when the producer has nothing to add beyond the state. |
| `interrupted` | bool | no | Set only on `paused`. True when a turn was actually cancelled on the way in, false (or absent) for a plain `/pause` or an interrupt that landed while the agent was idle. "Your work was killed" and "the loop just won't start" are different situations, and it is the first thing an operator asks. |
| `mode` | string | no | Set only on `resumed`. Echoes the disposition the operator chose: `"steer"` (a new instruction was injected under interrupt framing), `"continue"` (a carry-on note was injected), or `"abandon"` (the gate opened, nothing was injected, and the agent stays quiet until something else drives it) — so a second client watching the stream can render what happened rather than just that something did. Clients MUST tolerate unknown values. |
| `at` | string (RFC 3339) | yes | Transition timestamp. Clients date the banner from it and use it to order a push against a concurrently-polled `GET /status`. |

The gate is idempotent on the producer side: a redundant park or release does not transition and therefore emits no event, so two operator surfaces racing the same click produce one frame, not two.

**Only a resume opens the gate (v1.11.0+).** A message injected into a parked session — `POST /inject`, or `POST /wake` carrying a prompt — produces its ordinary `inbox` / `queued` frame and nothing else: no `resumed`, and the session stays paused until a `POST /resume` releases it, at which point the queued message drains in the same turn as the operator's instruction. Through 1.10.0 an inject from any caller but the producer's own auto-continue opened the gate as a side effect, and that showed on the stream as a `resumed` frame with **no `mode`** — the one shape of `resumed` that did not echo an operator's disposition. The shim assumed a human at the other end of the socket, and a producer cannot check that: a machine injecting under a person's identity (an alert watcher, for instance) re-opened gates operators had deliberately shut. A client that relied on inject to un-park should send `POST /resume` with `mode: "steer"` instead; a client that sees a `resumed` without `mode` is talking to a pre-1.11.0 producer, or to an in-process caller that released the gate directly, and SHOULD render it as a plain resume.

**Consumer guidance.** The push is fast but not the only source: `GET /status` reports `state="paused"` with `paused_since` / `pause_reason` / `interrupted`, which is what lets a client attaching to an already-paused session render the banner without waiting for a transition that already happened. A client holding both SHOULD let an applied push win over a contradicting poll for a short settle window (core-tui uses two seconds) — a poll already in flight across a resume otherwise flips the banner back on for a tick — and let the server win after that, since a client that ignores the poll stays wrong forever after a missed event.

Producers advertise the whole feature through `capabilities.features.pause` (§2.1). A client that sees the flag off, or sees no `pause` in `event_types`, hides its resume affordances rather than offering an operator a button the server will reject.

Example (interrupt parked a running turn):

```json
{"state": "paused", "reason": "operator interrupt", "interrupted": true, "at": "2026-08-19T14:31:02.881Z"}
```

Example (operator steered it back to work):

```json
{"state": "resumed", "mode": "steer", "at": "2026-08-19T14:32:44.219Z"}
```

### 2.9 `wake` (v1.7.0+)

**When emitted:** when the agent's wake signal fires — something out of band decided the loop should look at the world again. Like `pause`, it is emitted from the agent rather than from a request handler, because the producer that matters most never touches one: a host draining background-manager alerts into the agent's wake entry point is the case this event exists to make visible, and putting the emit in the HTTP layer would hide it from exactly the operator who cannot see the process.

**Payload:**

| Field | Type | Required | Description |
|---|---|---|---|
| `at` | string (RFC 3339) | yes | When the signal fired. The only field. |

**The payload is deliberately just a timestamp.** A wake carries no state a consumer can render, because whatever did the waking reports itself through its own frames: an alert arrives as an `inbox` event, a subagent's work as `agent` events, the resulting turn as `status-update` / `turn-complete`. What the wake adds is "look now", and the only thing worth attaching to that is when. A `reason` field is specified **absent, not reserved** — no producer can currently fill it (the in-process signal it mirrors is a bare channel with no payload), and a field consumers learn to branch on and producers cannot populate is a promise that gets broken later.

The consequence for clients is the important half: **a wake does not mean an alert is waiting.** Client copy that asserts inbox contents on the strength of this event is wrong for every wake that isn't alert-driven. Render it as an attention signal and let the frames that follow say what happened.

Further semantics:

- **It is an edge, not a state.** There is no matching "unwake" and nothing to reconcile on reconnect. A client that missed one has missed a notification, not fallen out of sync.
- **Coalescing is not promised in either direction.** The producer's wake signal is typically a one-slot channel that drops a fire while one is already pending, so two wakes microseconds apart may produce one frame or two. Consumers must treat this as an edge, not a count, and are free to coalesce on their own side.
- **An operator-typed prompt does not produce one.** The inject that carries it already announces itself as an `inbox` frame, and a wake on top would make every prompt the operator types raise an attention notice about the operator's own typing. The one call that legitimately produces both is a wake request that carries a prompt, because it is an inject and an explicit wake request at once.

Detection is the ordinary mechanism: look for `"wake"` in the `capabilities` frame's `event_types`. A pre-v1.7.0 producer omits the name and sends no frame, so a client written against v1.7.0 degrades to no notifications rather than to an error.

Example:

```json
{"at": "2026-08-19T14:32:05.117Z"}
```

### 2.10 `guardrail-trip` (v1.13.0+)

**When emitted:** when a guardrail on the producer trips — a cost ceiling is crossed, a watchdog decides the agent is looping. One frame per trip, emitted from the agent so an in-process halt reaches remote watchers, and emitted whether or not a turn was running at the time.

**Payload:**

| Field | Type | Required | Description |
|---|---|---|---|
| `guardrail` | string | yes | Which guardrail tripped. Known values: `cost_ceiling`, `watchdog`. Clients MUST tolerate unknown values (render the name, don't branch on it). |
| `reason` | string | yes | Human-readable explanation, including the operator-reachable way to clear the halt. Producers put the reset affordance here; consumers SHOULD render it verbatim rather than appending advice of their own. |
| `halted_turn` | bool | yes | Whether the trip cut a turn short. See below — this is the field the event exists for. |

**This is not a `turn-error`, and that is the whole design.** A guardrail trip is a statement about the *session*: from here until an operator resets it, turns are refused. It outlives the turn it interrupted, and at a turn boundary it interrupts nothing. Modelling it as a turn outcome forced a choice between two wrong things — emit the trip as the turn's terminal frame and the turn's real outcome goes unreported, or emit both and the turn appears to end twice. As a non-terminal frame it is neither: it is an announcement that sits alongside whatever the turn does next. It does not participate in the terminal barrier, and it MUST NOT be counted as one by consumers reconciling turn state.

**`halted_turn` tells the consumer what follows.**

- `true` — a turn was in flight and has been cut. Exactly one turn-error of kind `canceled` follows, and it carries no reason, because a cancellation looks the same whoever caused it. Consumers SHOULD suppress that one frame: the trip already said what happened, and rendering both puts a contentless warning under a meaningful one. Suppression is one-shot and scoped to the turn — it is spent on the next turn-error whatever its kind, and the arming MUST be dropped at the turn boundary, or a halt will swallow an operator's own cancellation on a later turn.
- `false` — the trip landed at a turn boundary (a post-turn watchdog check, say). No turn was harmed; the turn's ordinary `turn-complete` follows and MUST render.

The field is always present on the wire — it is not omitted when false. A consumer that inferred `false` from absence would silently stop suppressing the cancel against a producer that changed its serialisation, which is the exact defect this field was added to close.

**Behaviour change for existing clients.** Before 1.13.0 the producer suppressed the guardrail-caused `canceled` frame itself, and surfaced the halt as a `turn-error` of kind `cost_ceiling` or `watchdog`. At 1.13.0 both of those stopped: the cancel is no longer withheld (the turn's terminal frame is not the producer's to withhold), and core-agent no longer puts `cost_ceiling` or `watchdog` on the stream at all — a turn refused by an already-tripped guardrail short-circuits above the emit site, so the refusal reaches the caller as a returned error and a metric label, not a frame. So a pre-1.13.0 client against a 1.13.0 producer both goes blind to halts *and* gains a bare `⚠ canceled` block under each one. That makes producer and consumer a matched pair for this revision in a way `pause` and `wake` were not.

Detection is the ordinary mechanism: look for `"guardrail-trip"` in the `capabilities` frame's `event_types`. A client that finds it absent, and that used to watch for the `cost_ceiling` / `watchdog` turn-error kinds, SHOULD keep that code — it is how halts are reported by every producer older than this revision.

The event name matches the durable row the producer already writes to its event log for the same trip, deliberately: one name for one occurrence, whether it is read live or replayed.

Example, a watchdog trip at a turn boundary:

```json
{
  "guardrail": "watchdog",
  "reason": "watchdog halted the agent (repeated-tool-call): looping on read_file with identical args. Clear it with /guardrail reset watchdog, or POST /sessions/{app}/{sid}/guardrails/reset.",
  "halted_turn": false
}
```

And a cost ceiling cutting a turn short, with the cancel that follows it:

```
event: guardrail-trip
data: {"guardrail":"cost_ceiling","reason":"turn cost $0.61 exceeded the $0.50 per-turn ceiling. Clear it with /guardrail reset cost_ceiling.","halted_turn":true}

event: turn-error
data: {"kind":"canceled","code":"CANCELED","message":"turn canceled","retryable":false}

```

---

## 3. Versioning

The protocol follows [SemVer](https://semver.org/) at the `protocol_version` field in the `capabilities` event.

**Additive changes are MINOR or PATCH:**
- New event types
- New OPTIONAL fields on existing events
- New enum values on existing fields (clients are already required to tolerate unknown values per §2)
- **Demoting a required field to optional**, when the demoted field carries documented fallback semantics (e.g. "value MAY be derived from another event in the stream"). Producers gain flexibility; consumers must already handle the field's absence going forward. Spec MUST document the fallback in the same revision.

**Breaking changes are MAJOR:**
- Removing event types
- Removing or renaming any field (required or optional)
- Changing a field's type or semantics
- Promoting an optional field to required
- Changing an enum value's meaning

Clients SHOULD compare the server's `protocol_version` against the highest MAJOR they implement; if `server_major > client_major`, the client MUST fall back to poll-only mode (it can't safely consume the stream). If `server_major == client_major`, the client can consume even if `server_minor > client_minor` (server is ahead; new types are ignored). A client that declares its version per §3.1 gets the same answer earlier, as a `409` on the stream request, and that `409` is **terminal**: the server's major will not change between attempts, so retrying — immediately or with backoff — can never succeed. Treat it as the poll-only fallback above, or surface it to the operator; do not feed it into a reconnect loop.

### 3.1 Version negotiation

The `capabilities` frame is the first thing on the stream, which makes it the last moment a client can learn it is talking to the wrong major — by then it has already opened a stream whose frames it may mis-parse. Producers therefore also negotiate the version on the stream request itself. Implemented by core-agent since go-steer/core-agent#413 (closing go-steer/core-agent#389), which shipped at protocol 1.4.0 without a bump; this section writes it down.

**Declaring a version.** A client MAY declare the protocol version it speaks on the `/events` request, in either of two places:

- the `?protocol=<version>` query param, for URL-only clients (curl, browsers) that cannot set a custom header, or
- the `X-Attach-Protocol-Version` request header.

When both are present the **query param wins**, and it wins by presence rather than by validity: a malformed `?protocol=` is not rescued by a well-formed header. Surrounding whitespace is ignored, and a param that is empty after trimming counts as absent, so the header is consulted instead. The value is semver-shaped — `1.13.0`, `1.13.0-rc.1` and a bare `1` all parse, and a leading `v` is tolerated — but only the major component is read.

**Declaring nothing is legal.** A request with neither is accepted unchanged, exactly as it was before negotiation existed; every pre-negotiation client depends on that, and core-tui's own examples do not declare a version today. Clients SHOULD nonetheless declare one — it converts a silent mis-render into a clean refusal on the day a major bump ships — but this document does not require it, because the reference producer accepts the absence and a MUST here would make it non-conforming.

**The server's answer:**

| Declared | Status | Notes |
|---|---|---|
| nothing | stream opens | Back-compat path. |
| same major, any minor / patch | stream opens | Additive-minor per the rules above: an older client ignores frame types it does not know, an older server omits the ones it has not got. A 1.5.0 client against a 1.13.0 producer is accepted. |
| different major | **409 Conflict** | In either direction — a client ahead of the server is refused as well as one behind it. Plain-text body naming both versions. Terminal; see above. |
| no parseable major | **400 Bad Request** | E.g. `?protocol=latest`. Also terminal: the request is malformed and resending it changes nothing. |

**The echoed header.** The server sets `X-Attach-Protocol-Version` on the response to the version it speaks, whether or not the client declared one, and on the 409 and 400 rejections as well as on the stream it opens — so a client can read the server's version before the `capabilities` frame arrives, or without a stream at all. That is the extent of the promise, and it is narrower than "every `/events` response":

- The echo is set by the stream handler, so a response rejected **before** the request reaches it carries no header — authentication failures, an unknown session (`404`), an ACL refusal, and an ambiguous session ID on the `/sessions/{sid}/events` shortcut form, which core-agent answers with a `409` of its own. A `409` without the header is therefore **not** a version mismatch, and clients MUST NOT report it as one; the header's presence is what tells the two apart.
- Errors the stream handler raises after negotiating (`412` for a session with no event log, for instance) do carry it.
- No other endpoint echoes it — not even `GET /sessions/{sid}/agents/{name}/events`, which despite its name is a one-shot JSON read of a subagent's history, not this stream. Clients MUST NOT infer a version from its absence on any other response.

---

## 4. Compatibility matrix

Outcomes for every combination of old/new client and old/new server during Phase 1 rollout:

| Client | Server | Outcome |
|---|---|---|
| Old TUI | Old server | Polling — unchanged from today. |
| Old TUI | New server | Polling — new event types arrive but are silently dropped (unknown SSE event names). Existing event types (`stream-chunk`, `tool-call`, etc.) work as before. |
| New TUI, `RemoteTransport: Poll` | Old server | Polling — same as today. New TUI doesn't try to subscribe to event-stream state. |
| New TUI, `RemoteTransport: Poll` | New server | Polling — new TUI ignores `capabilities` advertisement of push events when in Poll mode. |
| New TUI, `RemoteTransport: Push` | Old server | New TUI subscribes to event-stream state, sees no `capabilities` event (or sees one with no push event types), and SHOULD log a "push mode requested but server doesn't support it" warning and fall back to poll. |
| New TUI, `RemoteTransport: Push` | New server | Push mode. Designed-for outcome. |
| New TUI, `RemoteTransport: Auto` (Phase 2) | New server | Reads `capabilities`, sees push support, uses push. |
| New TUI, `RemoteTransport: Auto` (Phase 2) | Old server | Reads `capabilities` (missing) or sees no push event types, falls back to poll. |
| Pre-1.5.0 client | 1.5.0 server | `pause` frames arrive under an event name the client doesn't know and are dropped. The gate is still real, and that is the sharp edge of this revision: the server parks on `POST /interrupt`, the client that asked to cancel sees only a cancel, and nothing on its screen says a resume is owed. Producers MUST therefore honour `hold=false` on `/interrupt` for callers that ask for it, so a client written against 1.4.0 semantics can keep getting them. |
| Pre-1.13.0 client | 1.13.0 server | Guardrail halts go unreported and each one leaves a bare `canceled` turn-error on screen with no explanation above it. Both halves are regressions from 1.12.0, where the producer both named the halt (as a `cost_ceiling` / `watchdog` turn-error) and suppressed the cancel. There is no producer-side mitigation available — the suppression is exactly what the revision removed — so this is the one row in this table that asks operators to upgrade the client rather than asking the producer to stay compatible. |
| 1.13.0 client | Pre-1.13.0 server | `guardrail-trip` is absent from `event_types` and no frame arrives. The client's cancel-suppression never arms, which is correct: the old producer is already suppressing on its side. Halts still surface as `cost_ceiling` / `watchdog` turn-errors, so a client that kept its pre-1.13.0 handling loses nothing. |
| 1.5.0 client | Pre-1.5.0 server | `features.pause` is absent and `pause` is not in `event_types`, so the client hides its hold affordances: Esc falls back to plain cancel, and resume commands report themselves unavailable. No banner ever renders, because nothing ever reports a hold. Consumers SHOULD gate on the advertisement rather than probing `POST /pause` for a 404. |
| Pre-1.8.0 client | 1.8.0 server | Cancellations arrive as `canceled`, a kind the client doesn't know; per §2.6 it renders them as `unknown`, with `retryable: false`. The one behaviour that changes is the one the revision was for: a client that offered a retry after an interrupt stops offering it. |
| 1.8.0 client | Pre-1.8.0 server | Cancellations arrive as `transient_network` / `retryable: true` with `code: "CANCELED"`. A client that trusts `retryable` offers to re-run a turn the operator stopped; one that suppresses the offer while a `pause` frame is in play (§5) does not. |
| Client that un-parks with `POST /inject` | 1.11.0 server | The message is queued and the session stays paused: no `resumed` frame follows, and the agent is silent until someone sends `POST /resume`. Nothing is lost — the message drains with the resume — but a client written to the 1.10.0 behaviour waits on a turn that is not coming. Migrate to `POST /resume` with `mode: "steer"`, which is still one call. core-tui's held-input path already uses it. |
| 1.12.0 client | Pre-1.12.0 server | The stream-open `status-update` says `turn_state: "idle"` even when a turn is running, and stays wrong until the turn's own frames arrive. For a turn blocked on a long tool call that can be minutes, and a parked-mid-turn session shows a quiet hold over live work. There is no wire signal to detect this; a client that cares reads `protocol_version`. |
| Client declaring major N (§3.1) | Server at any other major | `409 Conflict` on the stream request, with the server's version in `X-Attach-Protocol-Version`. No stream opens and no frame is mis-parsed, which is what declaring buys. Terminal: the client falls back to poll-only or reports the skew, and does not retry. A client that declared nothing gets the stream instead and is left to the `capabilities` check in §3. |

---

## 5. Examples

A complete representative session, viewed from the client side reading the SSE stream from connection open through one operator-driven turn:

```
event: capabilities
data: {"protocol_version":"1.15.0","event_types":["status-update","usage-update","inbox","turn-complete","turn-error","pause","wake","guardrail-trip","stream-chunk","tool-call","tool-result"],"server":"core-agent/2.9.0-dev","features":{"multi_session":true,"perms_stream":true,"mcp":true,"specialists":true,"cross_daemon":false,"interrupt":true,"pause":true,"guardrails":true,"cost_ceiling":false,"observer_mode":false},"slash_commands":["btw","compact","done","replan","subagent"],"agent":{"name":"core-agent","version":"v2.9.0-dev","model":"gemini-3.1-pro"},"caller_id":"alice@example.com"}

event: status-update
data: {"model":"gemini-2.5-pro","provider":"vertex","perm_mode":"default","turn_state":"idle","context_pct":3}

event: usage-update
data: {"tokens_in_total":0,"tokens_out_total":0,"cost_usd_total":0.0,"turns_total":0}

event: inbox
data: {"state":"queued","prompt_id":"p-9c4a","queued_at":"2026-06-07T19:42:11Z"}

event: inbox
data: {"state":"dequeued","prompt_id":"p-9c4a"}

event: status-update
data: {"turn_state":"streaming"}

event: stream-chunk
data: {"prompt_id":"p-9c4a","text":"Looking at the schema..."}

event: stream-chunk
data: {"prompt_id":"p-9c4a","text":" The column needs..."}

event: turn-complete
data: {"prompt_id":"p-9c4a","model":"gemini-2.5-pro","tokens_in":2806,"tokens_out":87,"cost_usd":0.0067,"latency_ms":4521}

event: status-update
data: {"turn_state":"idle","context_pct":11}

event: usage-update
data: {"tokens_in_total":2806,"tokens_out_total":87,"cost_usd_total":0.0067,"turns_total":1,"by_model":{"gemini-2.5-pro":{"tokens_in":2806,"tokens_out":87,"cost_usd":0.0067,"turns":1}}}
```

And the same shape for an error path:

```
event: inbox
data: {"state":"queued","prompt_id":"p-9c4b"}

event: inbox
data: {"state":"dequeued","prompt_id":"p-9c4b"}

event: status-update
data: {"turn_state":"streaming"}

event: turn-error
data: {"kind":"model_not_found","code":"NOT_FOUND","message":"Publisher Model `projects/.../publishers/google/models/gemini-3.1-pro-preview-customtools` was not found.","retryable":false,"hint":"Check vertex.location and model name; some models are global-only."}

event: status-update
data: {"turn_state":"idle"}
```

Note: `turn-error` does NOT emit a `turn-complete` for the same `prompt_id` (the turn never completed). Clients track open `prompt_id`s and close them on either `turn-complete` or `turn-error`.

And an operator parking the agent mid-turn, then steering it (v1.5.0+):

```
event: status-update
data: {"turn_state":"streaming"}

event: pause
data: {"state":"paused","reason":"operator interrupt","interrupted":true,"at":"2026-08-19T14:31:02.881Z"}

event: turn-error
data: {"kind":"canceled","code":"CANCELED","message":"turn canceled","retryable":false}

event: status-update
data: {"turn_state":"idle"}

event: pause
data: {"state":"resumed","mode":"steer","at":"2026-08-19T14:32:44.219Z"}

event: inbox
data: {"state":"queued","prompt_id":"p-9c4c"}

event: status-update
data: {"turn_state":"streaming"}
```

`interrupted: true` and the terminal frame for the killed turn are two different statements, and a client needs both. The `pause` frame says *why* the agent stopped and that it will not restart on its own; the open `prompt_id` still closes the ordinary way — a cancelled turn is a failed turn as far as the turn pipeline is concerned, so it ends on `turn-error` (which carries no `prompt_id`; clients close the open turn by ordering, per the note above). Do not treat `interrupted: true` as the close, and do not depend on whether the `pause` frame arrives before or after the terminal one — they come from different points in the producer and their relative order is not promised.

The `turn-error` above is what a 1.8.0+ producer sends. A pre-1.8.0 producer sends `{"kind":"transient_network","code":"CANCELED","message":"model call canceled","retryable":true}` for the same cancel, because its classifier saw `context.Canceled` and could not tell a killed turn from a dropped connection — so a client that offers a retry affordance purely off `retryable` offers one, against an older producer, for work the operator just stopped. A client that has to work against both SHOULD suppress the affordance when a `pause` frame is in play: against a pre-1.8.0 producer that frame is the only thing on the wire that knows the difference. See §2.6.

And a wake, which is the whole of what a wake looks like (v1.7.0+):

```
event: wake
data: {"at":"2026-08-19T14:32:05.117Z"}
```

There is nothing else in it, and the client should not infer anything else from it. If a host-side alert was what fired the signal, that alert announces itself separately — as an `inbox` frame, since something had to put it where the model will read it — and the two frames are the producer's to order and pair, not this spec's. If a bare "look now" fired it, the wake is the only frame there will be. A client that renders "an alert is waiting" off this event is right in the first case and wrong in the second, which is the defect go-steer/core-agent#802 surfaced.

And a guardrail halt cutting a turn short (v1.13.0+), which is the only sequence in this document where a client is told to drop a frame it received:

```
event: guardrail-trip
data: {"guardrail":"cost_ceiling","reason":"turn cost $0.61 exceeded the $0.50 per-turn ceiling. Clear it with /guardrail reset cost_ceiling.","halted_turn":true}

event: turn-error
data: {"kind":"canceled","code":"CANCELED","message":"turn canceled","retryable":false}

event: status-update
data: {"turn_state":"idle"}
```

Compare it with the interrupt sequence further up. There the `pause` frame and the terminal frame come from different points in the producer, so their relative order is explicitly not promised and a client cannot pair them. Here the order is promised: both frames are published by the same agent on the same path, trip first. That is what makes "suppress the next `canceled`" a rule a client can follow rather than a race it has to guess at.

---

## 6. Slash-response conventions

The `POST /sessions/.../slash/<name>` endpoints (`compact`, `done`, `btw`, `subagent`, `replan`, and any future providers) return JSON response bodies whose shape is per-slash — see each capability interface's response type in the server's `pkg/attach/state.go`. Two response-body keys are **reserved** on every slash response for renderer negotiation:

| Key | Type | Since | Description |
|---|---|---|---|
| `_render` | string | reserved (v1.4.0+) | Advises the client which built-in renderer to use for the response body. Values reserved so far: `"text"` (plain-text pane), `"markdown"` (rendered inline), `"json"` (collapsible JSON tree). Consumers MUST tolerate unknown values and fall back to their default renderer. Producers MAY omit; consumers default to their per-slash convention (e.g. `/compact` → markdown summary). |
| `_schema` | string \| object | reserved (v0.3.0+ target) | Reserved for schema-driven rendering. When present, points the client at a schema (URL or inline JSON Schema) describing the response body so a generic form/table renderer can display it without per-slash knowledge. No producer emits it in v1.4.0. |

Both keys are additive to any existing per-slash response field. Producers MAY populate them at any point without a protocol bump (they're reserved on the response body, not on the wire event). Consumers that don't understand a value SHOULD fall back to their existing per-slash rendering path.

Rationale: mast-web needs to render slash responses without shipping a hardcoded renderer per producer. Reserving these keys now avoids a future producer stamping them with different semantics and creating a rendering collision.

---

## 7. Out of scope

The following are deliberately NOT specified here:

- **Authentication** — `Authorization: Bearer` and `X-Attach-Token` semantics live in deployment-specific docs (per [core-tui #34](https://github.com/go-steer/core-tui/issues/34)).
- **Endpoint paths** — the protocol is endpoint-agnostic. Server documentation specifies which path serves the SSE stream.
- **Reverse direction (client → server)** — request endpoints are specified by the producer, in [core-agent's attach HTTP reference](https://github.com/go-steer/core-agent/blob/main/docs/site/src/content/docs/reference/attach-http.md), not here. The version namespace covers the whole attach contract, so the REST surface still bumps this document's version, and such a bump is recorded in §8 without a §2 section (see 1.6.0, 1.9.0, 1.10.0, 1.14.0 and 1.15.0). The split is deliberate ([#270](https://github.com/go-steer/core-tui/issues/270)). core-tui makes none of these calls, because the host's attach client does and hands core-tui the resulting events. That reference already pins every request and response shape against the producer's conformance fixtures, headers and status codes included. A second copy here would specify a surface this repository does not implement and drift from it the first time an endpoint changed. §2.8 still names `POST /pause`, `POST /resume` and `/interrupt`'s `hold` flag, because the `pause` event is unreadable without them.
- **Event replay / persistence** — out of scope for Phase 1. A future version may add `Last-Event-ID` resume semantics.
- **TUI rendering decisions** — what each event LOOKS like in the terminal is core-tui's concern, not the wire protocol's.
- **Other producers** — only core-agent emits these events today. Other producers MUST implement this spec faithfully or pick a different stream identifier.

---

## 8. Change log

| Version | Date | Change |
|---|---|---|
| — | 2026-09-29 | **No version change — documentation only.** New §3.1 specifies version negotiation on the stream request: a client MAY declare the version it speaks via `?protocol=` or the `X-Attach-Protocol-Version` header (the query param wins), a different major is refused with a terminal `409` and an unparseable one with `400`, minor and patch skew are accepted, and declaring nothing is legal. The server echoes its own version on the same header — on responses from the stream handler only, which excludes rejections raised before it (auth, unknown session, and the shortcut form's ambiguous-ID `409`, which the header's absence distinguishes from a version refusal). §3 and §4 gain the matching client rule: the `409` is not retryable. Not a bump because nothing changed on the wire: go-steer/core-agent#413 shipped the mechanism at 1.4.0 without one, and a 1.13.1 here would name a version no producer speaks — which negotiation is exactly the mechanism that would expose. Same shape as the 1.5.0 row's backfill of `guardrails`: recorded as undocumented, not claimed as new. Closes [#273](https://github.com/go-steer/core-tui/issues/273). |
| 1.15.0 | 2026-09-30 | **MINOR — no SSE change.** REST only, following the 1.6.0 precedent. `POST /perms/respond` accepts an optional `reason` with a deny, and the model reads it in the refused call's result as `The operator's reason: "…"` — without one it has to guess what the operator objected to. A reason on any other decision is a 400; the server collapses whitespace runs and answers 400, leaving the prompt pending, for more than 500 bytes after that. A deny without a reason is unchanged, and a pre-1.15.0 producer ignores the field. The request shape is specified in [core-agent's attach HTTP reference](https://github.com/go-steer/core-agent/blob/main/docs/site/src/content/docs/reference/attach-http.md) per §7. No frame changes. Shipped in go-steer/core-agent#1174, closing go-steer/core-agent#1165. core-tui's half is the prompt's deny-with-reason step (R-PERM-9, [#344](https://github.com/go-steer/core-tui/issues/344)): the TUI makes no REST call itself, it returns the reason from `Prompter.AskApprovalDetailed` for the host's attach client to send. |
| 1.14.0 | 2026-09-15 | **MINOR — no SSE change.** REST only, following the 1.6.0 precedent. `POST /perms/respond` answers **410 Gone** for a prompt whose turn ended before the answer arrived — a guardrail cut, an operator's stop, a daemon going down — where it used to answer 404 with a body reading "already responded, cancelled, or never issued". An expired prompt has been 410 since go-steer/core-agent#647; the two 410 bodies differ, because "expired" tells an operator to answer faster or raise the approval timeout and a cancellation says that would not have helped. 404 narrows to what it can still honestly claim: an id already answered, and one the daemon never issued. A version rather than a silent fix because the status code is the whole signal: on a pre-1.14.0 producer a late out-of-band approver cannot tell a prompt a guardrail took from an id the broker never had, and "never issued" is the reading they will act on. No frame changes. Shipped in go-steer/core-agent#1089, closing go-steer/core-agent#1088. |
| 1.13.0 | 2026-09-13 | **MINOR.** New `guardrail-trip` event type (§2.10) reporting that a guardrail halted the session, with `guardrail` / `reason` / `halted_turn`. A trip was previously reported as a `turn-error` of kind `cost_ceiling` or `watchdog`, which mis-modelled it: a halt is a statement about the session (turns are refused until an operator resets it), not a turn outcome, and at a turn boundary there is no turn for it to be the outcome of. As a non-terminal frame it sits alongside the turn's real terminal frame instead of replacing or duplicating it, and it does not participate in the terminal barrier. `halted_turn` is the part consumers must implement: `true` means one `canceled` turn-error follows and SHOULD be suppressed (one-shot, dropped at the turn boundary), `false` means the turn completes normally. **This revision is not silently backward-compatible in the usual way.** The producer also stopped suppressing the guardrail-caused cancel — the turn's terminal frame is not the producer's to withhold — and stopped putting `cost_ceiling` and `watchdog` on the stream at all, since a turn refused by an already-tripped guardrail short-circuits above the emit site and survives only as a returned error and a metric label. So a pre-1.13.0 client against a 1.13.0 producer goes blind to halts and grows a bare `⚠ canceled` block under each one; see the two new rows in §4. Shipped in go-steer/core-agent#891 with core-tui's consumer half in the same release, which is the pairing that keeps the regression theoretical. Revisions 1.8.0 through 1.12.0 were missing from this table when this row landed; they were backfilled from the producer's history later, in [#322](https://github.com/go-steer/core-tui/issues/322). |
| 1.12.0 | 2026-09-03 | **MINOR.** The stream-open `status-update` reports `turn_state: "streaming"` whenever a turn is in flight, including on a session parked mid-turn (§2.2). The value was always in the vocabulary and the producer's SSE mapping always consumed it, but nothing produced it, so every snapshot seeded `"idle"` — and since typed frames have no replay, a client attaching to a session born with its first turn got no other turn-state information at all. No shape changes; a value that should have occurred now does. The REST half, `GET /status` gaining `turn_in_flight` beside `state` (with `"running"` finally reachable and `"paused"` still outranking it), is not specified here. Shipped in go-steer/core-agent#940, closing go-steer/core-agent#896. The same revision also covers go-steer/core-agent#941 (closing go-steer/core-agent#897), REST only: `POST .../agents/{name}/stop`'s `stopped` now reports whether this call stopped the subagent rather than whether the name is registered, and the 200 gains `status`. §4 gains the 1.12.0-client row. |
| 1.11.0 | 2026-08-31 | **MINOR — behaviour change, no shape change.** `POST /inject` (and `POST /wake` carrying a prompt) no longer opens a pause gate; only `POST /resume` does (§2.8). Through 1.10.0 any inject but auto-continue's released the hold as a side effect, which on the stream was a `resumed` frame with no `mode`. It assumed a human at the other end of the socket, and a machine injecting under an operator's identity re-opened gates an operator had deliberately shut. Now the inject publishes its `inbox` / `queued` frame and waits behind the gate. A minor rather than a major deliberately, as the producer records at length: no frame, field or status code changes shape, and a major would have the negotiation `409` (§3.1) lock out working clients over a change most of them cannot observe. Migration is `POST /resume {"mode":"steer","steer":"…"}` in place of `POST /inject`; core-tui's held-input path already used it and is unaffected. §4 gains the row for a client that relied on the old behaviour. Shipped in go-steer/core-agent#879, closing go-steer/core-agent#878. |
| 1.10.0 | 2026-08-20 | **MINOR — no SSE change.** REST only, following the 1.6.0 precedent, and the largest revision of that kind: five producer PRs landed under the one bump. `GET` + `PATCH /sessions/{sid}/acl` and an optional `viewers` / `contributors` body on `POST /sessions` make a session's ACL settable at all (go-steer/core-agent#831, closing go-steer/core-agent#797); a pre-1.10.0 producer answers the new paths 404, which is also what it answers an unauthorized caller, so clients read the negotiated version rather than probing. `POST /perms/respond` accepts and echoes an optional `approver` that the server checks against the caller it verified, and `GET /perms` history rows carry `by` (go-steer/core-agent#832). `POST /sessions/{sid}/title` overrides the 1.6.0 inferred title (go-steer/core-agent#833). `POST /inject` gains an optional `wake` flag — `false` queues without driving a turn — and its 200 reports `woke` (go-steer/core-agent#834). `POST /inject`, and `POST /wake` when it carries a prompt, report the `prompt_id` they assigned — the same id later carried by `inbox` and `turn-complete` frames, so no frame changes (go-steer/core-agent#841). Every response field is optional, and every request field defaults to the pre-1.10.0 behaviour. Shapes are pinned by core-agent's `rest-session-acl-v1`, `rest-session-title-v1` and `rest-inject-v2` [conformance fixtures](https://github.com/go-steer/core-agent/tree/main/pkg/attach/testdata/conformance). |
| 1.9.0 | 2026-08-20 | **MINOR — no SSE change.** REST only, following the 1.6.0 precedent. `GET /sessions/{sid}/subagents` rows carry an optional `tools`: the subagent's configured tool grant, sorted by name, in the same shape and `source` vocabulary as `GET /tools`. It lists what was configured, not the loop-control tools the runtime adds to every spawned subagent. Same additive shape and the same required fallback as 1.6.0's `title` — the key is omitted both by a pre-1.9.0 producer and for a subagent granted no tools, so a client cannot read absence as "reaches nothing". Shipped in go-steer/core-agent#828, closing go-steer/core-agent#768. |
| 1.8.0 | 2026-08-20 | **MINOR.** New `canceled` value in the `turn-error` `kind` enum (§2.6), with `retryable: false`. A cancelled turn — operator interrupt, shutdown, guardrail cut — used to be reported as `transient_network` / `retryable: true`, which told a client to offer a re-run of work an operator had just deliberately stopped. A model call that hit its deadline stays `transient_network` / retryable. No new event type, so `event_types` is unchanged; detection is by `protocol_version`. Additive under §3 — clients are already required to render an unknown kind as `unknown` — but a behaviour change for this one input, which is the point. Also backfills `cost_ceiling` and `watchdog` into the §2.6 table: producers had emitted both since before 1.8.0 without a bump, and the table is now what a pre-1.13.0 producer actually sends. Also corrects the `canceled` examples in §2.10 and §5, which showed `retryable: true`, and updates §5's interrupt sequence to show the 1.8.0+ frame, keeping the pre-1.8.0 one as the caution it was. §4 gains a row in each direction. Shipped in go-steer/core-agent#817, closing go-steer/core-agent#816. |
| 1.7.0 | 2026-08-19 | **MINOR.** New `wake` event type (§2.9) reporting that the agent's wake signal fired — an operator asked the loop to look now, or a host wired its own out-of-band trigger (a background subagent's alert) into the same signal. Payload is a single `at` timestamp and deliberately no `reason`: the thing that did the waking reports itself through its own frames, and no producer can fill a reason field today. Emitted from the agent rather than a request handler so an in-process wake reaches remote watchers. Shipped in go-steer/core-agent#814, closing go-steer/core-agent#802 — where the defect being fixed was that the remote adapter's wake method never satisfied `WakeRequester` and there was no frame for it to receive either way. Fully backward-compatible — a pre-1.7.0 producer omits `"wake"` from `event_types` and sends nothing, and a pre-1.7.0 consumer drops the unknown event name per §3. |
| 1.6.0 | 2026-08-19 | **MINOR — no SSE change.** The version namespace covers the whole attach contract, not only the event stream, and 1.6.0 is entirely on the REST side: `GET /sessions` rows carry an optional `title`, a short operator-facing label derived from the session's first prompt, so a session picker lists work rather than IDs. No frame in §2 changes. Recorded here so the numbering doesn't appear to skip and so a client negotiating a version knows what it is negotiating. Producer-side shape is pinned by [core-agent's `rest-sessions-list-v2` conformance fixture](https://github.com/go-steer/core-agent/blob/main/pkg/attach/testdata/conformance/rest-sessions-list-v2.json); endpoint semantics live in the producer's own reference doc per §7. `title` is omitted for pre-1.6.0 producers, for sessions whose first turn hasn't landed, and where titling is off, so every client needs the fall-back-to-session-ID path regardless of the version it negotiated. Shipped in go-steer/core-agent#809. |
| 1.5.0 | 2026-08-19 | **MINOR.** New `pause` event type (§2.8) reporting the session's pause gate closing and opening, with `state` / `reason` / `interrupted` / `mode` / `at`. New `pause` feature key (§2.1) advertising that the producer can hold its loop — `POST /pause` and `POST /resume` (`steer` / `continue` / `abandon`) work, and `POST /interrupt` parks by default instead of only cancelling the turn. Emitted for every transition and from the agent rather than the handler, so a park driven in-process reaches remote watchers. Also backfills the `guardrails` feature key, which producers have advertised since go-steer/core-agent#670 without a bump — feature keys are additive by the §2.1 rule, so this row records it rather than claiming it as new. §4 gains the two rows that matter: a pre-1.5.0 client against a 1.5.0 server gets a hold it cannot see, so producers MUST keep honouring `hold=false` on `/interrupt`. Shipped in go-steer/core-agent#794 (design: `docs/operator-interrupt-design.md` in that repo) and consumed by core-tui's `Pauser` capability ([#260](https://github.com/go-steer/core-tui/issues/260)). The REST half of the revision (the `/pause`, `/resume` and `/interrupt` request and response schemas, and `GET /status`'s `paused_since` / `pause_reason` / `interrupted`) is specified in [core-agent's attach HTTP reference](https://github.com/go-steer/core-agent/blob/main/docs/site/src/content/docs/reference/attach-http.md), per §7. Fully backward-compatible — a 1.4.0 consumer sees every pre-existing shape unchanged and drops the unknown event name. |
| 1.4.0 | 2026-07-20 | **MINOR.** `capabilities` frame (§2.1) extended with four optional fields — `features` (feature-flag map), `slash_commands` (dynamic list of accepted slash names), `agent` (name/version/description/model/provider/url identity block), and `caller_id` (resolved caller identity display hint). Enables backend-agnostic clients (mast-web) to render without a code change per producer. `status-update` (§2.2) gained an optional `capabilities` merge field spec'd for hot capability changes (no producer emits it in v1.4.0 — reserved). New §6 documents reserved `_render` / `_schema` keys on slash-response bodies. New `GET /whoami` endpoint (unauthenticated-safe) returns the resolved caller identity + admin + auth source. Closes go-steer/core-agent#329, sibling to go-steer/mast-web#12. Fully backward-compatible — pre-v1.4.0 servers omit the new fields, pre-v1.4.0 clients ignore them. |
| 1.3.0 | 2026-07-17 | **MINOR.** Added optional `savings` sidecar object on `tool-result` response payloads (§2.7) carrying the digest wrap's per-call byte / token reduction, router path, and (agentic path only) subagent usage. Closes go-steer/core-agent#223 Phase 4 tier 1 (per-tool inline chip) + tier 2 (detail-overlay chip). Session-level cumulative rendering (`/stats` block) tracked separately. Same response-map sidecar channel as v1.2.0's `latency_ms`. Fully backward-compatible — pre-v1.3.0 servers omit the object, pre-v1.3.0 clients ignore it. |
| 1.2.0 | 2026-07-16 | **MINOR.** Added optional `latency_ms` sidecar key on `tool-result` response payloads (§2.7 — formally documented in this revision). Closes go-steer/core-agent#277 (emit) + core-tui#60 (consume) — completes core-tui#52 tier 3 (inline `[2.4s]` per tool row + latency chip in the expand-single detail overlay). Sidecar rides the response map itself because ADK's `tool.Run` has no write access to the enclosing `session.Event.CustomMetadata`; §2.7 documents the finding for future sidecars. Fully backward-compatible — pre-v1.2.0 servers omit the field, pre-v1.2.0 clients ignore it. |
| 1.1.1 | 2026-07-15 | **PATCH.** Added optional `usage-update.last_turn` object (tokens_in / tokens_in_cached / tokens_out / cost_usd / model) carrying authoritative per-turn cost. Complements the v1.1.0 `cost_usd`-on-`turn-complete`-optional demotion so observer-mode (LiveAgent) clients have a source for per-turn footer cost. Closes core-tui #57. Fully backward-compatible — pre-v1.1.1 servers omit the field, pre-v1.1.1 clients ignore it. |
| 1.1.0 | 2026-06-07 | **MINOR.** `turn-complete.cost_usd` demoted from required → optional with documented fallback to the immediately-following `usage-update` (servers with pricing out-of-band can omit it). §2.1 `capabilities.event_types` clarified to permit listing logical sub-types that ride on multiplexed wire events. §3 evolution rules extended with the required→optional demotion clause that governed this change. |
| 1.0.0 | 2026-06-07 | Initial spec — `capabilities`, `status-update`, `usage-update`, `inbox`, `turn-complete`, `turn-error`. |
