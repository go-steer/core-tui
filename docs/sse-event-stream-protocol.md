# SSE Event-Stream Protocol

The wire-format contract between core-tui (consumer) and any server (producer — core-agent today) for the `/events` SSE stream. This document specifies what bytes the client expects to see; future clients (web TUI, IDE plugin, mobile) read the same spec.

**Status:** Phase 1 — additive-only. See [core-tui #40](https://github.com/go-steer/core-tui/issues/40) and [core-agent #115](https://github.com/go-steer/core-agent/issues/115) for the phased-rollout context.

**Protocol version:** `1.2.0`. Bumped on changes per the [Versioning](#versioning) rules below.

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

Six event types are defined in this protocol version. Each section specifies: when the server emits the event, the payload schema (snake_case JSON), and a representative example.

### 2.1 `capabilities`

**When emitted:** as the **first** event on every newly-opened stream, before any other event. Required.

**Purpose:** lets the client know which event types the server speaks, so the client can decide whether to subscribe to push-style state (Phase 2 `Auto` mode) or fall back to polling.

**Payload:**

| Field | Type | Required | Description |
|---|---|---|---|
| `protocol_version` | string (semver) | yes | Version the server speaks. Clients compare against the version they implement; see [Versioning](#versioning). |
| `event_types` | array of strings | yes | Names of event types the server emits on this stream. Clients MUST tolerate unknown names (forward-compat). Servers MAY also list **logical** sub-types that ride on a multiplexed event name (e.g. `stream-chunk` / `tool-call` / `tool-result` carried on the legacy `agent` wire event) so clients can detect capability without inspecting frame internals. |
| `server` | string | no | Free-form server identifier (e.g. `"core-agent/0.4.2"`). Diagnostic only. |

Example:

```json
{
  "protocol_version": "1.2.0",
  "event_types": ["status-update", "usage-update", "inbox", "turn-complete", "turn-error", "stream-chunk", "tool-call", "tool-result"],
  "server": "core-agent/0.4.2"
}
```

(`stream-chunk`, `tool-call`, `tool-result` are pre-existing event types that predate this protocol document; listed here so the example reflects real server output.)

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

All fields are independently optional except `turn_state`. Clients applying a partial payload MUST merge into local state — fields not present in the event are unchanged.

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
| `transient_network` | DNS, TCP reset, 5xx after retry budget exhausted | true |
| `unknown` | Catch-all for errors the server can't categorize | server's call |

Clients SHOULD render any unknown `kind` value as if it were `unknown` (forward-compat). Clients MUST NOT crash on unknown kinds.

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

**Why the response map, not `CustomMetadata`.** The ergonomic sidecar channel would have been `session.Event.CustomMetadata`, but ADK constructs the `tool-result` event *after* `tool.Run` returns — `CustomMetadata` isn't writable from inside `Run`. The response map itself IS writable, and both the remote and embedded core-agent adapters copy the whole map through to `tui.ToolResult.Response` verbatim, so a well-known sidecar key rides both transports without any per-adapter plumbing. Future sidecars (e.g. `tokens_delta`) SHOULD ride the same channel for the same reason.

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

Clients SHOULD compare the server's `protocol_version` against the highest MAJOR they implement; if `server_major > client_major`, the client MUST fall back to poll-only mode (it can't safely consume the stream). If `server_major == client_major`, the client can consume even if `server_minor > client_minor` (server is ahead; new types are ignored).

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

---

## 5. Examples

A complete representative session, viewed from the client side reading the SSE stream from connection open through one operator-driven turn:

```
event: capabilities
data: {"protocol_version":"1.2.0","event_types":["status-update","usage-update","inbox","turn-complete","turn-error","stream-chunk","tool-call","tool-result"],"server":"core-agent/0.4.2"}

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

---

## 6. Out of scope

The following are deliberately NOT specified here:

- **Authentication** — `Authorization: Bearer` and `X-Attach-Token` semantics live in deployment-specific docs (per [core-tui #34](https://github.com/go-steer/core-tui/issues/34)).
- **Endpoint paths** — the protocol is endpoint-agnostic. Server documentation specifies which path serves the SSE stream.
- **Reverse direction (client → server)** — existing request endpoints unchanged.
- **Event replay / persistence** — out of scope for Phase 1. A future version may add `Last-Event-ID` resume semantics.
- **TUI rendering decisions** — what each event LOOKS like in the terminal is core-tui's concern, not the wire protocol's.
- **Other producers** — only core-agent emits these events today. Other producers MUST implement this spec faithfully or pick a different stream identifier.

---

## 7. Change log

| Version | Date | Change |
|---|---|---|
| 1.2.0 | 2026-07-16 | **MINOR.** Added optional `latency_ms` sidecar key on `tool-result` response payloads (§2.7 — formally documented in this revision). Closes go-steer/core-agent#277 (emit) + core-tui#60 (consume) — completes core-tui#52 tier 3 (inline `[2.4s]` per tool row + latency chip in the expand-single detail overlay). Sidecar rides the response map itself because ADK's `tool.Run` has no write access to the enclosing `session.Event.CustomMetadata`; §2.7 documents the finding for future sidecars. Fully backward-compatible — pre-v1.2.0 servers omit the field, pre-v1.2.0 clients ignore it. |
| 1.1.1 | 2026-07-15 | **PATCH.** Added optional `usage-update.last_turn` object (tokens_in / tokens_in_cached / tokens_out / cost_usd / model) carrying authoritative per-turn cost. Complements the v1.1.0 `cost_usd`-on-`turn-complete`-optional demotion so observer-mode (LiveAgent) clients have a source for per-turn footer cost. Closes core-tui #57. Fully backward-compatible — pre-v1.1.1 servers omit the field, pre-v1.1.1 clients ignore it. |
| 1.1.0 | 2026-06-07 | **MINOR.** `turn-complete.cost_usd` demoted from required → optional with documented fallback to the immediately-following `usage-update` (servers with pricing out-of-band can omit it). §2.1 `capabilities.event_types` clarified to permit listing logical sub-types that ride on multiplexed wire events. §3 evolution rules extended with the required→optional demotion clause that governed this change. |
| 1.0.0 | 2026-06-07 | Initial spec — `capabilities`, `status-update`, `usage-update`, `inbox`, `turn-complete`, `turn-error`. |
