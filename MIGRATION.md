# Migrating to core-tui

This document is the guide for migrating the two named hosts —
[`go-steer/cogo`](https://github.com/go-steer/cogo) and
[`go-steer/core-agent`](https://github.com/go-steer/core-agent) — from
their in-tree `internal/tui` packages to depending on `core-tui`. It's
also the reference for any third-party host writing an adapter against
core-tui's stable surface.

It is **not** a migration *commit log* — the actual cogo and
core-agent migration PRs live in those repos. This doc enumerates
what each host's adapter needs to do, maps every host feature to a
core-tui surface, surfaces the gaps that don't have a clean mapping,
and provides concrete adapter sketches.

The stable surface itself is documented in [`docs/design.md`](./docs/design.md) §3.
This guide consumes that contract — it doesn't redefine it.

---

## 1. The adapter contract (recap)

Every host adapter does the same four things (`docs/design.md` §6.0):

1. **Implement `tui.Agent`** — a single-method interface wrapping the
   host's native agent so its event stream translates into
   `tui.Event`.
2. **Implement zero or more capability interfaces** from §3.3
   (`ModelSwapper`, `Reloader`, `PermissionController`,
   `PricingController`, `ToolLister`, `SubagentLister`,
   `Interruptible`, `StatusReporter`, `SlashProvider`). Each lights
   up the corresponding slash command or UI affordance; missing ones
   degrade to a "not available in this host" message.
3. **Wire the TUI-implemented interfaces** (`PermissionPrompter`,
   `Elicitor`, `UserPrompter`) into the host's permission gate / MCP
   servers / agent before the first `Agent.Run`.
4. **Construct `Options` and call `tui.Run(ctx, opts)`.** Field
   additions to `Options` are non-breaking, so adapters compiled
   against an older `core-tui` keep working.

Adapters live in a `cmd/<host>-tui` main package (or fold into the
host's existing entrypoint).

---

## 2. cogo

### 2.1 Source-tree summary

cogo today owns `internal/tui/` (≈ 30 files, single package, mirror
of core-agent's pre-extraction TUI). The TUI is driven by:

- **`internal/agent.Agent`** — wraps an ADK runner. Streams
  `*session.Event` values out of `Run(ctx, prompt)`.
- **`program.go` callbacks** — `rebuildAgent(modelID)` for model
  swap, `reloadFromDisk()` for `/reload`, plus the
  `permissions.Gate` for ask/allow/deny semantics.
- **No subagents, no pricing, no runtime tool enumeration.**

### 2.2 Capability map

| core-tui surface | cogo source | Adapter task |
|---|---|---|
| `tui.Agent.Run` | `agent.Agent.Run(ctx, prompt) iter.Seq2[*session.Event, error]` | ~30-line translator: walk `*session.Event` parts, emit `tui.Event{Text/Partial/ToolCalls/Usage}`. |
| `ModelSwapper` | `program.go` `rebuildAgent(modelID) (*agent.Agent, error)` callback | Wrap the callback; cache the model list (or hardcode from `availableModels()`) so `AvailableModels()` returns synchronously. |
| `Reloader` | `program.go` `reloadFromDisk() (reloadResult, error)` | Wrap; map `reloadResult` → `ReloadResult` (Agent + Memory + MCPServers + Skills + Note). |
| `PermissionController` | `permissions.Gate` methods (`SessionApprovals`, `AddAllow/Deny/BuiltinAllowExtra`, `AlwaysAllow`) | Implement on a thin wrapper over `*Gate`. |
| `StatusReporter` | `Model.cfg.Model.Name` + agent's `Run` state | Trivial: return `Status{ModelName: cfg.Model.Name, State: "idle"/"running"}`. |
| `PricingController` | (not present) | Skip. `/pricing` degrades to "not available". |
| `ToolLister` | (not present — tools baked into the agent at startup) | Skip for v1. `/tools` palette stays "not available" until cogo adds tool introspection. |
| `SubagentLister` | (not present) | Skip. cogo has no subagents. |
| `Interruptible` | (not present — cogo uses `ctx` cancellation only) | Skip. `Esc`-interrupt still works via the context the TUI passes into `Agent.Run`. |
| `SlashProvider` | (no agent-defined commands) | Not needed — every cogo slash command maps to a core-tui built-in once the corresponding capability is wired. |
| `PermissionPrompter` (TUI-provided) | cogo's `gate.Prompter` | Construct via `tui.NewPrompter()`, hand to `gate.SetPrompter` before `agent.Run`. |
| `Elicitor` (TUI-provided) | cogo's MCP server elicit callback | Construct via `tui.NewElicitor()`, register with each MCP server's `Connect` call. |
| `Options.PathScope` | `permissions.PathScope` | Pass through. |
| `Options.AgentsDir` | `cogo`'s `.agents/` discovery | Pass through. |
| `Options.UsageTracker` | `internal/usage.Tracker` | Implement `tui.UsageTracker` on `*usage.Tracker`. |
| `Options.PersistModelChoice` | `program.go` model-choice save | Wrap. |
| `Options.PersistStatusLayout` | (not present today; new) | Optional — wire a small `.agents/config.json` writer. |
| `Options.MentionProviders` | (not needed) | Built-in file provider covers cogo's `@file` UX. |

### 2.3 cogo adapter sketch

```go
// cogo/cmd/cogo-tui/main.go
package main

import (
    "context"
    "iter"

    "github.com/go-steer/core-tui/tui"

    "github.com/go-steer/cogo/internal/agent"
    "github.com/go-steer/cogo/internal/config"
    "github.com/go-steer/cogo/internal/permissions"
    "github.com/go-steer/cogo/internal/usage"
)

// cogoAgent adapts *agent.Agent to tui.Agent.
type cogoAgent struct{ inner *agent.Agent }

func (a *cogoAgent) Run(ctx context.Context, prompt string) iter.Seq2[tui.Event, error] {
    return func(yield func(tui.Event, error) bool) {
        for ev, err := range a.inner.Run(ctx, prompt) {
            if err != nil {
                yield(tui.Event{}, err)
                return
            }
            te := tui.Event{Partial: ev.Partial}
            if ev.UsageMetadata != nil {
                te.Usage = &tui.Usage{
                    InputTokens:  int(ev.UsageMetadata.PromptTokenCount),
                    OutputTokens: int(ev.UsageMetadata.CandidatesTokenCount),
                }
            }
            if ev.Content != nil {
                for _, p := range ev.Content.Parts {
                    if p.FunctionCall != nil {
                        te.ToolCalls = append(te.ToolCalls, tui.ToolCall{
                            ID:   p.FunctionCall.ID,
                            Name: p.FunctionCall.Name,
                            Args: p.FunctionCall.Args,
                        })
                    }
                    if p.Text != "" {
                        te.Text += p.Text
                    }
                }
            }
            if !yield(te, nil) {
                return
            }
        }
    }
}

// Capability adapters elided for brevity — each implements the
// corresponding core-tui interface in 5-20 lines by delegating to
// cogo's existing types (rebuildAgent, reloadFromDisk, gate, etc.).

func main() {
    ctx := context.Background()
    cfg := config.Load()
    gate := permissions.NewGate(cfg)
    inner := agent.New(cfg, gate)
    tracker := usage.NewTracker()

    prompter := tui.NewPrompter()
    gate.SetPrompter(prompter)

    opts := tui.Options{
        Agent: &cogoAgentWithCapabilities{
            cogoAgent: cogoAgent{inner: inner},
            cfg:       cfg,
            gate:      gate,
        },
        Prompter:     prompter,
        PathScope:    cfg.PathScope,
        AgentsDir:    cfg.AgentsDir,
        Memory:       cfg.Memory,
        MCPServers:   cfg.MCPServers,
        Skills:       cfg.Skills,
        UsageTracker: tracker,
        StatusLayout: cfg.StatusLayout,
        PermissionMode: tui.PermissionModeWiring{
            Initial: tui.PermissionModeDefault,
            Set:     func(m tui.PermissionMode) error { return gate.SetMode(m.String()) },
            Persist: func(m tui.PermissionMode) error { return cfg.SaveMode(m.String()) },
        },
        PersistModelChoice:  func(id string) error { return cfg.SaveModel(id) },
        PersistStatusLayout: func(l tui.StatusLayout) error { return cfg.SaveStatusLayout(l) },
    }
    if err := tui.Run(ctx, opts); err != nil {
        // exit code handling
    }
}
```

### 2.4 LOC budget

~150 lines across the main + capability adapters. The Run translator
is the largest single piece (~30 lines); the rest are 5-line interface
satisfactions.

---

## 3. core-agent (local in-process mode)

### 3.1 Source-tree summary

core-agent has grown substantially since the earlier audit. The
agent's surface now includes a host-side **inbox** (mid-turn message
injection), the `/btw` side-question path (with a modal Glamour-
rendered answer that doesn't land in history), `/subagent` (direct
background-agent spawn), a queue-panel state machine that tracks
each operator-typed-during-streaming entry through queued → in-flight
→ done | failed, and a wake-signal channel for background agents
that need the operator's attention.

Exhaustive list of `agent.Agent` exported methods:

```go
// Streaming + turn lifecycle.
Run(ctx, prompt) iter.Seq2[*session.Event, error]
RunWithContents(ctx, contents) iter.Seq2[*session.Event, error]  // alt: structured contents
Interrupt() bool

// Side questions (/btw).
AskSideQuestion(ctx, q) (string, error)

// Inbox — mid-turn message injection. Operator-typed-during-
// streaming entries are Inject()ed; turn-end DrainInbox()s and
// auto-continues with the drained messages as the next prompt.
Inject(message) error
DrainInbox() []string
InboxArrived() <-chan struct{}
PendingInboxCount() int

// Wake — background agents request operator attention.
WakeRequested() <-chan struct{}
RequestWake()

// Identity + observability.
AppName() string
AgentName() string
ModelName() string
UserID() string
SessionID() string
SessionService() session.Service
EventLog() *eventlog.Handle

// Tools + subagents (local view).
Tools() []tool.Tool
BackgroundManager() *BackgroundAgentManager

// Attach mirrors (local agent serves attach RPCs too).
AttachStatus() attach.StatusInfo
AttachTools() []attach.ToolInfo
AttachAgents() []attach.AgentInfo
AttachInterrupt() bool
```

Other notable host pieces:

- **`internal/pricing`** — `RefreshPricing(ctx)` + `SetPricing(modelID, in, out)` callbacks for `/pricing refresh` and `/pricing set`. `ModelConfig.Pricing` is now a per-model map (was a single struct), so overrides survive `/model` switches.
- **`internal/config`** — new sections: `PricingFileConfig{Refresh, Source}`, `PathScopeConfig.Allow []string`, `URLScopeConfig{Allow, Deny, MaxBodyBytes, TimeoutSeconds, Headers}`, `AgentConfig.DisplayName` (maps to `Options.Branding.Wordmark`).
- **`internal/attachclient.Client`** — RPCs grew: `Tools`, `Agents` (new), `Status`, `Inject`, `Wake` (new), `Interrupt`, `Stream(ctx, sid, since)` for SSE replay-on-reconnect.

### 3.2 Capability map

| core-tui surface | core-agent source | Adapter task |
|---|---|---|
| `tui.Agent.Run` | `agent.Agent.Run` | Same translator as cogo. |
| `ModelSwapper` | `rebuildAgent(modelID)` callback | Wrap; **cache the model list** at startup (or query a registry) so `AvailableModels()` returns synchronously — core-agent's callback is lazy. |
| `Reloader` | `reloadFromDisk()` callback | Wrap; map `reloadResult` → `ReloadResult`. |
| `PermissionController` | `permissions.Gate` (same shape as cogo's) | Wrap on `*Gate`. |
| `PricingController` | `internal/pricing.RefreshPricing` + `SetPricing` | Wrap; surface the 5-layer precedence (config / project / user-manual / external / builtin / longest-prefix) inside `Refresh`. core-agent's per-model `ModelConfig.Pricing` map is the host's storage; core-tui doesn't see it. |
| `ToolLister` | `agent.Agent.Tools() []tool.Tool` (or `AttachTools()` in attach mode) | Wrap; map `tool.Tool` → `tui.ToolInfo`. |
| `SubagentLister` | `agent.Agent.BackgroundManager().Subagents()` (or `AttachAgents()` in attach mode) | Wrap; map entries → `tui.SubagentInfo` (`Name, Status, LastReport, StartedAt`). |
| `Interruptible` | `agent.Agent.Interrupt() bool` (or `AttachInterrupt()` in attach mode) | One-line wrapper. |
| `StatusReporter` | `agent.Agent.AttachStatus()` (returns `attach.StatusInfo`) | Wrap; map `StatusInfo.State` to the core-tui state string. |
| `SlashProvider` | `/subagent` flag parser + `/btw` invocation | **Needed.** See §3.3 below — `/subagent` is pure SlashProvider; `/btw` needs the resolution from §5. |
| `PermissionPrompter` (TUI-provided) | `gate.SetPrompter` | Wire. |
| `Elicitor` (TUI-provided) | each MCP server's elicit callback | Wire per server. |
| `UserPrompter` (TUI-provided) | (not used today — core-agent has no `ask_question` tool) | Optional. Wire if a future tool needs it. |
| `Options.UsageTracker` | `internal/usage.Tracker` | Same as cogo. |
| `Options.MentionProviders` | (not needed) | Built-in file provider is sufficient. |
| `Options.Branding.Wordmark` | `AgentConfig.DisplayName` | Pass through. |
| `Options.PersistModelChoice` / `Options.PersistStatusLayout` / `Options.PermissionMode.Persist` | core-agent's config save | Wrap. |

### 3.3 Slash commands the adapter owns via SlashProvider

core-agent has two slash commands that aren't built-in to core-tui
and need a `SlashProvider` adapter:

- **`/subagent <goal> --name=X --tools=Y --max-turns=10 …`** — flag
  parser produces a `BackgroundSpec`, calls
  `agent.BackgroundManager().Spawn(ctx, "", spec)`. The adapter
  parses the flag tail; core-tui passes the raw `args` string
  through. Returned `SlashResult.SystemMessage` confirms the spawn
  ("subagent X started, branch=Y, watching via /subagents").
- **`/btw <question>`** — see §5.1 for the resolution. This one
  needs more than `SlashProvider` provides today.

### 3.3 core-agent local adapter sketch

The shape mirrors the cogo sketch in §2.3, with these additional
capability adapters:

```go
type coreAgentCaps struct {
    cogoAgent
    inner        *agent.Agent
    pricing      *pricing.Manager
}

// Interruptible
func (c *coreAgentCaps) Interrupt() bool { return c.inner.Interrupt() }

// ToolLister
func (c *coreAgentCaps) Tools() []tui.ToolInfo {
    raw := c.inner.Tools()
    out := make([]tui.ToolInfo, 0, len(raw))
    for _, t := range raw {
        out = append(out, tui.ToolInfo{
            Name:        t.Name(),
            Description: t.Description(),
            Source:      t.Source(),
            GateState:   c.gate.StateFor(t.Name()),
        })
    }
    return out
}

// SubagentLister
func (c *coreAgentCaps) Subagents() []tui.SubagentInfo {
    mgr := c.inner.BackgroundManager()
    if mgr == nil {
        return nil
    }
    var out []tui.SubagentInfo
    for _, a := range mgr.Subagents() {
        out = append(out, tui.SubagentInfo{
            Name:       a.Name,
            Status:     a.Status,
            LastReport: a.LastReport,
            StartedAt:  a.StartedAt,
        })
    }
    return out
}

// PricingController
func (c *coreAgentCaps) Refresh(ctx context.Context) (string, error) {
    return c.pricing.Refresh(ctx)
}
func (c *coreAgentCaps) Set(modelID string, in, out float64) (string, error) {
    return c.pricing.Set(modelID, in, out)
}

// SlashProvider for /btw
func (c *coreAgentCaps) SlashCommands() []tui.SlashCommandSpec {
    return []tui.SlashCommandSpec{
        {Name: "btw", Description: "ask a side question (no tools, doesn't land in history)"},
    }
}
func (c *coreAgentCaps) InvokeSlash(ctx context.Context, name, args string) (tui.SlashResult, error) {
    if name != "btw" {
        return tui.SlashResult{}, fmt.Errorf("unknown command: %s", name)
    }
    answer, err := c.inner.AskSideQuestion(ctx, args)
    if err != nil {
        return tui.SlashResult{}, err
    }
    return tui.SlashResult{SystemMessage: answer}, nil
}
```

### 3.4 LOC budget

~400 lines across the main + capability adapters.

---

## 4. core-agent (attach mode)

### 4.1 What's different

Attach mode points the TUI at a remote core-agent running over HTTP +
SSE instead of in-process. Everything the user sees stays the same;
the adapter just changes how `Agent.Run` works.

- **Transport:** `attachclient.Client` provides RPC methods (Tools,
  Agents, Status, Interrupt, Inject) over short-lived HTTP requests
  plus an SSE stream (`Stream(ctx, sessionPath, since int64)`) that
  delivers `attach.Frame{Seq int64, Event *session.Event}`.
- **Replay-on-reconnect:** the client tracks the last seen
  `Frame.Seq` and re-subscribes with `?since=N` on transport failure.
- **Auth:** bearer token via request headers, set in
  `Client.auth(req)`.

### 4.2 Capability map

| core-tui surface | attach-mode source | Notes |
|---|---|---|
| `tui.Agent.Run` | `Client.Stream(ctx, sid, since)` | Translator subscribes to the SSE stream, converts each `Frame.Event` to a `tui.Event`. On EOF, re-subscribes with the last seen `Seq`. |
| `Interruptible` | `Client.Interrupt(ctx, sid)` | One round-trip; returns a bool. |
| `ToolLister` | `Client.Tools(ctx, sid)` | One round-trip; cache for the session unless `/reload` fires. |
| `SubagentLister` | `Client.Agents(ctx, sid)` | One round-trip per `/subagents` open. |
| `StatusReporter` | `Client.Status(ctx, sid)` | Lightweight poll. |
| `ModelSwapper` / `Reloader` / `PermissionController` / `PricingController` | (not yet RPCs in attach API) | **Defer.** Attach-mode `/model`, `/reload`, `/permissions`, `/pricing` degrade to "not available in attach mode" until the attach API adds the matching RPCs. |
| `SlashProvider` for `/btw` | (no attach RPC for `AskSideQuestion`) | Defer. `/btw` is local-mode only until the attach server exposes a side-question RPC. |

### 4.3 Attach-specific lifecycle concerns

These are adapter responsibilities, not core-tui's:

- **Reconnection.** Wrap the SSE subscription in a loop that
  retries with exponential backoff and resumes from the last
  `Frame.Seq`. core-tui sees one continuous event stream.
- **Auth refresh.** Bearer token rotation happens at the adapter
  layer. core-tui never sees the token.
- **Inject vs Queue.** `Client.Inject(ctx, sid, message)` feeds a
  message into the *running* turn — distinct from core-tui's
  prompt-queueing (R-CHAT-10) which buffers prompts for the *next*
  turn. core-tui has no slot for inject today; bind it to a custom
  keybinding in the adapter if your operators need it.

### 4.4 attach-mode adapter sketch

```go
type attachAgent struct {
    client *attachclient.Client
    sid    string
}

func (a *attachAgent) Run(ctx context.Context, prompt string) iter.Seq2[tui.Event, error] {
    return func(yield func(tui.Event, error) bool) {
        // Inject the prompt into the remote agent.
        if err := a.client.Inject(ctx, a.sid, prompt); err != nil {
            yield(tui.Event{}, err)
            return
        }
        // Subscribe to the SSE stream, resuming from the last
        // observed sequence on reconnection.
        var sinceSeq int64
        for ctx.Err() == nil {
            for frame := range a.client.Stream(ctx, a.sid, sinceSeq) {
                sinceSeq = frame.Seq
                te := translateEvent(frame.Event)
                if !yield(te, nil) {
                    return
                }
            }
            // Stream returned — either turn ended cleanly or the
            // transport dropped. The Client distinguishes the two
            // via a final state event. Reconnect with backoff on
            // transport failure; return on clean end.
            if isCleanEnd(sinceSeq, a.client) {
                return
            }
            time.Sleep(backoff(sinceSeq))
        }
    }
}

func (a *attachAgent) Interrupt() bool {
    resp, _ := a.client.Interrupt(context.Background(), a.sid)
    return resp.Cancelled
}

func (a *attachAgent) Tools() []tui.ToolInfo {
    raw, _ := a.client.Tools(context.Background(), a.sid)
    out := make([]tui.ToolInfo, 0, len(raw))
    for _, t := range raw {
        out = append(out, tui.ToolInfo{Name: t.Name, Description: t.Description, Source: t.Source})
    }
    return out
}

// SubagentLister + StatusReporter follow the same shape.
```

---

## 5. Gaps surfaced by the audits

### 5.1 Real semantic gaps (resolved: all five become core-tui PRs)

These are genuine core-tui surface gaps surfaced by core-agent's
expanded TUI. The core-agent
[`core-tui-adapter-design.md`](https://github.com/go-steer/core-agent/blob/main/docs/core-tui-adapter-design.md)
resolved all five as "spec it" — each becomes a focused PR in
core-tui that lands before the core-agent adapter PR.

| Gap | What it is | Resolution |
|---|---|---|
| **`/btw` modal-rendered answer** | core-agent renders the side-question answer in a transient modal that doesn't land in chat history, with Glamour markdown applied. `SlashProvider.SlashResult.SystemMessage` is a single string and lands as a permanent `RoleSystem` row. | **Spec it (PR 1).** Extend `SlashResult` with an optional `ModalAnswer *SideAnswer` field carrying `Question / Answer / Err`; core-tui renders a dismissable Glamour modal when non-nil. |
| **Queue-panel state machine** | core-agent's queue tracks each entry through `queued → in-flight → done / failed` with per-entry error display + 2s fade for Done entries. core-tui's `[]string` queue is flat. | **Spec it (PR 2).** Promote `Model.queue` from `[]string` to `[]QueueEntry{Text, State, Err, Created}`; state glyphs (⏳ ↻ ✓ ✗); TTL-based culling. |
| **Mid-turn inbox injection** | `agent.Inject(message)` feeds a message INTO the currently-streaming turn. core-agent's TUI does this on every operator-typed-during-streaming entry; the agent drains the inbox on turn-end and auto-continues. R-CHAT-10 queueing is for the NEXT turn — different semantics. | **Spec it (PR 3).** Add `InjectableAgent` capability + `Options.MidTurnInjectionMode` enum (default `QueueForNext` preserves R-CHAT-10; opt-in `InjectIntoCurrent` routes queue Enter through `Inject`). |
| **Wake signal (`WakeRequested()`)** | Background agents can request operator attention via a channel. core-agent's TUI doesn't surface this in UI yet — channel exists, no visible affordance. | **Spec it (PR 4).** Add `WakeRequester` capability with `WakeRequested() <-chan struct{}`; render a transient toast banner on each signal (per the Crush toast pattern in [ui-references.md](./docs/ui-references.md)). New `R-WAKE-1` requirement. |
| **`RunWithContents` (structured prompts)** | core-agent has an alternate `Run` that takes structured `[]Content` instead of a string. Used for retry with synthesized context. | **Spec it (PR 5).** Add `RunWithContents` as an optional method on `tui.Agent` (feature-detected via type assertion). |

### 5.2 Adapter-responsibility gaps (no spec change needed)

| Gap | Hosts affected | Where it lands |
|---|---|---|
| Runtime tool introspection (`Tools()`) | cogo | cogo limitation, not core-tui's. When cogo adds `Agent.Tools()`, the adapter wires `ToolLister` in one PR. |
| Lazy model list | core-agent | Adapter responsibility — cache at startup so `ModelSwapper.AvailableModels()` returns synchronously. |
| `PricingFileConfig` + `URLScopeConfig` storage | core-agent | Adapter loads from `internal/config`; core-tui doesn't see it. |
| `AgentConfig.DisplayName` | core-agent | Pass through to `Options.Branding.Wordmark`. |
| Per-model pricing map (`ModelConfig.Pricing` as map) | core-agent | Adapter's `PricingController.Set` writes into the map; core-tui doesn't see it. |
| Durable event-log / resume | core-agent local | Out of scope per [D20](./docs/decisions.md#d20-resume--replay) (v2). |
| Attach reconnection lifecycle | core-agent attach | Adapter responsibility per [D11](./docs/decisions.md#d11-attach-mode-remote-agent-over-http-unix-socket). |
| Attach auth token refresh | core-agent attach | Adapter responsibility. |

The §5.1 gaps are the ones that need a design decision **before** the
adapter is written. Each one is "spec it now" or "punt and adapter
works around" — the right call depends on how much core-agent's
operators rely on the feature today. The §5.2 gaps are pure adapter
responsibilities with no core-tui spec change implied.

---

## 6. Per-host migration checklist

### cogo

- [ ] Add `cmd/cogo-tui/main.go` (or fold into existing `cmd/cogo`).
- [ ] Implement `tui.Agent` translator (~30 lines).
- [ ] Implement `ModelSwapper`, `Reloader`, `PermissionController`,
      `StatusReporter` (~5-15 lines each).
- [ ] Wire `tui.NewPrompter()` into `gate.SetPrompter`.
- [ ] Wire `tui.NewElicitor()` into each MCP server.
- [ ] Construct `Options` (PathScope, AgentsDir, Memory, MCPServers,
      Skills, UsageTracker, PersistModelChoice).
- [ ] Delete `internal/tui/`.
- [ ] Smoke-test: `/help`, `/quit`, `/model`, `/reload`,
      `/permissions`, `/clear`, `/mouse` all behave as they did
      pre-migration; per-turn footer renders; streaming + Glamour
      live render works; queue panel appears when typing ahead.
- [ ] CI passes.

### core-agent (local)

- [ ] Add `cmd/core-agent-tui/main.go`.
- [ ] Same translator + capability adapters as cogo, plus:
      `Interruptible`, `ToolLister`, `SubagentLister`,
      `PricingController`, `SlashProvider` for `/btw`.
- [ ] Wire `Prompter`, `Elicitor`.
- [ ] Construct `Options`.
- [ ] Delete `internal/tui/`.
- [ ] Smoke-test: every command from the cogo list, plus `/tools`,
      `/subagents`, `/interrupt`, `/pricing refresh`, `/pricing set`,
      `/btw <question>`.
- [ ] CI passes.

### core-agent (attach)

- [ ] Add `cmd/core-agent-tui-attach/main.go`.
- [ ] Implement the `attachAgent.Run` translator with reconnection
      + `since` replay.
- [ ] Implement `Interruptible`, `ToolLister`, `SubagentLister`,
      `StatusReporter` (each one round-trip to the attach client).
- [ ] Decide whether to bind `Client.Inject` to a custom key; if so,
      register a `SlashProvider` entry for it.
- [ ] Decline `ModelSwapper`, `Reloader`, `PermissionController`,
      `PricingController`, `SlashProvider /btw` — these degrade
      gracefully to "not available in attach mode" until the attach
      API adds the matching RPCs.
- [ ] Smoke-test against a running core-agent + transport failures
      (kill server, restart, confirm the SSE reconnects + resumes).
- [ ] CI passes.

---

## 7. FAQ

**Will the migration break existing cogo / core-agent users?**
No. The TUI's user-visible behavior is the superset of what the v1
`internal/tui` packages did — every slash command, modal, and key
binding either lifts as-is or comes with documented additions
(palette, queueing, per-turn footer, `?`-help panel, sidebar layout).

**Can hosts iterate on the adapter without re-vendoring core-tui?**
Yes — capability interfaces are detected by type assertion. A host
can add a new capability adapter in a single PR without touching
core-tui.

**Will the attach-mode adapter feel different from local mode?**
A handful of commands (`/model`, `/reload`, `/permissions`,
`/pricing`, `/btw`) degrade to "not available in attach mode" until
the attach API exposes their RPCs. Everything else — streaming,
Glamour, queueing, palette, modals, help panel — is identical.

**How do third-party hosts use this guide?**
The capability map and adapter sketches generalize. Replace
"cogo" / "core-agent" with your own agent types and follow the same
four-step adapter contract from §1.
