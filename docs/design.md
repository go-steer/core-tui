# core-tui Design

This document specifies how `core-tui` is structured, what its plug-in
surface looks like, and how a host satisfies it. core-agent is the
reference host; cogo appears throughout as a second, smaller worked
example and is not a gating consumer (§8). It is the design
counterpart to `requirements.md`.

Throughout this document, the prefix `tui` refers to the package
`github.com/go-steer/core-tui/tui`. Other packages are noted with
their full path.

## 1. Goals

In order of priority:

1. **Lossless port** of the existing TUI features from cogo +
   core-agent. The user-visible behavior must be the superset; no
   regressions on either host. (See `requirements.md` §3.)
2. **Agent-framework agnostic.** Nothing in `core-tui/tui` may import
   `google.golang.org/adk`, `google.golang.org/genai`, MCP SDK types,
   or anything host-specific. Translation happens at the host's
   adapter layer.
3. **Capability composition.** The required agent surface is tiny.
   Every advanced feature (model swap, reload, pricing, permissions,
   tool listing, subagents) is an opt-in capability the TUI
   feature-detects.
4. **Embeddable.** A host should be able to drop core-tui in with a
   small adapter plus a `tui.Run(ctx, opts)` call.

   This goal used to say "a ≤ 50-line adapter". `examples/core-agent`
   (issue #82) was written partly to measure that number, and the
   measurement says it was never true: the *minimum* adapter —
   `Agent.Run` translating a host's nested event tree into
   `tui.Event`, and nothing else — is **64 non-comment lines**, and a
   reference-host-shaped adapter is **257 lines local / 380 attach**
   in the deliberately condensed sketch (core-agent's real ones are
   ~1,050 and ~1,630). The figure is recorded here as measured rather
   than aspired to. Bringing it down is what issue #77's capability
   consolidation is for; §6.1 / §6.2's per-host LOC budgets (~150 and
   ~400) were always the more honest targets.

## 2. Module layout

```
core-tui/
├── docs/
│   ├── requirements.md
│   ├── design.md
│   └── decisions.md
├── go.mod              module github.com/go-steer/core-tui
├── go.sum
├── tui/                # the library — public surface
│   ├── agent.go        Agent interface + capability interfaces + Event types
│   ├── prompter.go     PermissionPrompter + PermissionRequest/Decision
│   │                   (blocking decision modal renders via huh.Select)
│   ├── elicitor.go     Elicitor + ElicitRequest/Result
│   ├── tracker.go      UsageTracker interface (host implements; TUI reads)
│   ├── options.go      Options struct + Branding + defaults
│   ├── program.go      Run() entry point + Model construction
│   ├── model.go        bubbletea.Model implementation
│   ├── update.go       Update() dispatcher + slash routing
│   ├── view.go         View() rendering + modal compositors
│   ├── status.go       header/sidebar status surface (R-USE-2)
│                       — single file; layout switch is at render time
│   ├── keys.go         KeyMap + defaults
│   ├── commands.go     SlashAction enum + ParseSlash + Help text
│   ├── palette.go      slash/file palette state
│   ├── files.go        @file picker + expansion
│   ├── markdown.go     Glamour renderer wrapper
│   ├── messages.go     internal tea.Msg types (turnDone, streamChunk, ...)
│   ├── history.go      Role + Message + History
│   ├── styles.go       Styles + adaptive colors
│   ├── thinking.go     rotating "thinking" indicator
│   ├── branding.go     default brand + Branding helpers
│   ├── modelpicker.go  model picker overlay (huh.Select)
│   ├── permpicker.go   permissions review overlay (huh.MultiSelect)
│   ├── elicit.go       MCP-elicit modal state (huh.Group)
│   ├── transcript.go   on-exit transcript writer
│   ├── agentcmd.go     translates Agent events → tea.Msgs
│   └── *_test.go       table-driven Update() tests + smoke tests
└── examples/
    ├── local/          scripted in-process agent → visual harness
    ├── notifier-smoke/ standalone Notifier-contract exerciser
    └── core-agent/     reference-host adapter sketch — local +
        │               attach flavors; the compile-time canary (§7)
        └── fakehost/   local stand-in for core-agent's agent type +
                        attach client, so the example depends on no
                        other repo
```

### 2.1 Why one flat package

The TUI types are highly interconnected (the `Model` references the
history, palette, picker overlays, prompter, elicitor, etc.). Both
source projects keep everything in one `internal/tui` package and
neither has felt the splitting pressure. We follow suit but make the
package **public** (`tui` not `internal/tui`) so hosts can import it.

Helper modules (transcript, markdown, palette state) stay in the same
package; the design contracts that consumers should depend on are
called out explicitly in §3.

**Re-checked for 1.0 (issue #78): the decision stands.** The specific
question was whether the render-side interfaces (`Dialog`, `Item`,
`Focusable`, `ToolRenderer`, …) should move to a subpackage so the
contract is structural rather than conventional. They cannot —
`Dialog` and `Item` take `*Model` in their method signatures, so a
subpackage holding them would import `tui` while `tui` imports it, an
import cycle — and they should not, because none of them is
implementable from outside the package today, so a package named for
extension would advertise a seam that does not exist. The reasoning
and what it means for #115 is in
[`api-surface.md`](./api-surface.md) §4. What the flat package needed
was not a boundary but an enumeration, which §3 now has.

## 3. The plug-in surface

This section is normative — it is the only stable API hosts may rely
on. Everything else in `package tui` is subject to change.

**The frozen surface is 156 exported symbols**, enumerated one by one in
[`api-surface.md`](./api-surface.md) §3.1. That is every symbol below plus
its transitive closure: the argument and result types of the capability
interfaces, the SSE payload structs hanging off `Event`, and the string
vocabularies those payloads carry. The sections that follow describe the
surface; `api-surface.md` is the list, and it is the list that §8's
compatibility promise is made about.

The rest of `package tui` is 109 more exported symbols in two groups, also
classified there:

- **42 host-useful but unpromised** — the render extension points
  (`Dialog`, `Item`, `Focusable`, `ToolRenderer`, `Overlay`, …), the
  theming registry, the transcript reader, `Styles`. Each carries a
  promote-or-unexport recommendation in `api-surface.md` §3.2. Five of
  them (`Theme`, `BuiltinTheme`, `BuiltinThemes`, `ThemeByName`,
  `ThemeChangedMsg`), the seven transcript symbols, and
  `SystemClipboardWriter` are recommended for promotion into the list
  above; nothing is promoted until it is.
- **67 incidental** — the `Glyph*` and `Brand*` vocabularies, the named
  theme constructors, `History`, the queue types, `Model` / `NewModel`.
  Exported without a host-facing purpose; being narrowed before the
  freeze.

A host that finds itself naming something outside §3.1's list is either
using an unpromised symbol — file an issue asking for promotion — or has
found a gap in the list, which is a bug in this document.

Two properties of the surface are easy to miss and are promises too:

- **Untyped vocabularies.** `Options.ForceTheme`, `StatusUpdate.TurnState`,
  `TurnError.Kind`, `InboxEvent.State` and `ToolSavings.Path` are plain
  `string` fields whose legal values are exported constants
  (`ThemeAuto`, `TurnStateIdle`, `TurnErrorConfig`, `InboxStateQueued`,
  `SavingsPathAgentic`, …). Those constants are contract even though no
  type-level walk reaches them.
- **`errors.As` targets.** `SubagentNotFoundError` is returned as a bare
  `error` from `SubagentEventReader`; hosts match it with `errors.As`.
  Its identity is contract even though its name never appears in a
  signature.

### 3.1 Required: `Agent`

```go
package tui

import (
    "context"
    "iter"
)

// Agent is the bare minimum a host must supply. Run executes one turn
// against prompt and returns an iterator of Events that the TUI drains
// in a goroutine. Cancel the context to abort mid-turn.
//
// Multi-turn state (conversation history) is the agent's concern. The
// TUI calls Run once per submission and assumes state is preserved by
// the agent across calls.
type Agent interface {
    Run(ctx context.Context, prompt string) iter.Seq2[Event, error]
}
```

### 3.2 Required: `Event`

```go
// Event is the neutral representation of one ADK / agent event. Adapters
// translate their framework's native event type (Google ADK
// session.Event, an Anthropic SDK stream chunk, an MCP tool result,
// etc.) into this shape.
//
// All fields are optional. A single Event typically carries ONE of:
//   - text  → Text non-empty
//   - tool call → ToolCalls non-empty
//   - usage update → Usage non-nil
type Event struct {
    // Text is the chunk produced by the model when Partial=true,
    // or the committed full text when Partial=false. The TUI
    // accumulates partials into the in-progress assistant message
    // and Glamour-renders the accumulated text on every update so
    // formatting appears live; the final render result is cached on
    // turn end so subsequent re-renders skip the Glamour pass. When
    // a partial render fails (e.g. an unclosed code fence mid-stream)
    // the TUI falls back to raw text for that frame.
    Text    string
    Partial bool

    // ToolCalls lists tool invocations the model issued in this
    // event. ID is the stable function-call ID used for deduping
    // across partial + committed echoes (ADK emits the same call
    // twice; the TUI keeps the first).
    ToolCalls []ToolCall

    // Usage carries token counts. The TUI snapshots the most
    // recent non-nil value and reports it once at turn end.
    Usage *Usage
}

type ToolCall struct {
    ID   string         // empty allowed; non-empty enables dedupe
    Name string
    Args map[string]any
}

type Usage struct {
    InputTokens  int
    OutputTokens int
}
```

The translation in cogo's adapter looks like this (≈ 30 lines):

```go
// cogo/cmd/cogo-tui/adapter.go
func (a *cogoAgent) Run(ctx context.Context, prompt string) iter.Seq2[tui.Event, error] {
    return func(yield func(tui.Event, error) bool) {
        for ev, err := range a.inner.Run(ctx, prompt) {
            if err != nil { yield(tui.Event{}, err); return }
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
                            ID: p.FunctionCall.ID, Name: p.FunctionCall.Name, Args: p.FunctionCall.Args,
                        })
                    }
                    if p.Text != "" { te.Text += p.Text }
                }
            }
            if !yield(te, nil) { return }
        }
    }
}
```

### 3.3 Optional capability interfaces

Each interface matches one user-visible feature and is documented as
such. There are twenty, and this section declares all twenty: a
capability that does not appear here is not part of the plug-in
surface. [`api-surface.md`](./api-surface.md) §3.1 is the mechanical
list the roster is checked against, and
`TestDesignDocDeclarationsMatchSource` in `tui/design_doc_test.go`
compares every declaration below against `package tui` on each test
run, so the two can no longer disagree quietly.

Seventeen of the twenty are feature-detected on the `Agent` by type
assertion. The other three each differ in a way worth knowing:
`UsageTracker` is supplied through `Options.UsageTracker` (§3.4)
rather than found on the `Agent`, so it may live on a type of its own;
`SessionByModelTracker` is asserted on that tracker rather than on the
agent; and `ContentRunner` is asserted nowhere in `package tui` at
all, being declared so hosts can drive each other through it, which is
why #77 proposes deleting it.

Detection by type assertion is why the declarations below have to be
exact. A near-miss — the right method name with the wrong signature —
is indistinguishable from declining the capability, so the host sees
no compile error and no runtime error, just a feature that never
appears. Write `var _ tui.X = (*adapter)(nil)` for every capability
the adapter means to satisfy; `examples/core-agent` carries such a
block for exactly this reason (§7). The two places the reference host
names a capability it does not in fact satisfy — a `RequestWake()`
that is not `WakeRequester`, and a comment naming an `Interruptible`
interface that has never existed — are recorded in
[`api-audit.md`](./api-audit.md) §3.

```go
// LiveAgent is for hosts whose agent is not driven by per-turn Run
// calls — a remote-attached daemon running autonomously, an observer
// TUI watching MCP-triggered activity (issue #22). When implemented,
// the TUI ranges over Events(ctx) in one long-lived goroutine started
// at startup and paints every event, whether or not the operator
// typed. LiveAgent WINS over Run: a host satisfying both has Run
// skipped, and operator submissions route through InjectableAgent
// when that is present too. Events is called exactly once, so
// reconnection and replay are the implementation's own business.
type LiveAgent interface {
    Events(ctx context.Context) iter.Seq2[Event, error]
}

// PermanentStreamError, implemented by an error a LiveAgent yields,
// says the condition is not worth retrying — the session is gone, the
// auth was revoked. The TUI paints a terminal "session unavailable"
// row instead of looping on the reconnect path (issue #51). Errors
// that don't implement it fall back to a substring heuristic over the
// HTTP status, so adapters that already stringify one keep working.
type PermanentStreamError interface {
    error
    PermanentStreamErr() bool
}

// RemoteInterrupter lets the TUI cancel an in-flight turn it has no
// local context for — a LiveAgent observer session watching a
// daemon's autonomous turn. Without it, /interrupt short-circuits
// with "no turn in flight" on remote sessions, because the local gate
// keys off the per-turn cancel func that only operator-initiated
// turns populate. Implementations MAY block briefly on network I/O;
// the TUI calls Interrupt off the Update-loop path with a short
// deadline, and errors surface as an inline RoleError row. Optional;
// ctx cancellation of a locally-driven turn always works regardless.
type RemoteInterrupter interface {
    Interrupt(ctx context.Context) error
}

// StatusReporter feeds the header bar. Most hosts leave the model
// name and state for the TUI to derive from Options and skip this;
// implement it when the agent has richer state to surface. Status()
// is called off the event loop, so it must be safe for concurrent
// calls and must return last-known state rather than doing I/O.
type StatusReporter interface {
    Status() Status
}
type Status struct {
    ModelName string
    State     string // "idle" | "running" | "deferred" | ...
    Provider  string // "gemini" | "anthropic" | "vertex" | ... — optional
}

// UsageTracker is the read-only side of the host's usage accounting;
// it feeds the per-turn footer, /stats, the status surface and the
// totals recorded in the session transcript (R-USE-1 / R-USE-3). It
// is snapshotted at turn end rather than polled per frame. Unlike
// every other capability here it is wired through
// Options.UsageTracker rather than detected on the Agent, so it can
// live on a type of its own. Same concurrency rule as StatusReporter:
// the TUI pulls these off its event loop and expects cached values.
type UsageTracker interface {
    SessionTotals() Usage           // input + output tokens, cumulative
    SessionCostUSD() float64        // accumulated dollar spend
    LastTurn() (Usage, float64)     // most-recent turn's usage + cost
    ContextWindowSize() int         // 0 when unknown
    ContextWindowUsed() int         // 0 when unknown
    SessionTurns() int              // 0 when unknown
    SessionDuration() time.Duration // 0 when unknown
}

// SessionByModelTracker is a capability ON the UsageTracker, not on
// the Agent (issue #18): hosts that account per-model satisfy it and
// /stats grows a breakdown under the aggregate rows. The map key is
// the model name and should match what StatusReporter reports. An
// empty map or a single entry suppresses the breakdown, since one
// entry would only restate SessionTotals.
type SessionByModelTracker interface {
    SessionByModel() map[string]ModelTotals
}
type ModelTotals struct {
    Turns        int
    InputTokens  int
    OutputTokens int
    CostUSD      float64
}

// ModelSwapper backs /model.
type ModelSwapper interface {
    AvailableModels() []ModelInfo
    SwitchModel(modelID string) (Agent, error) // returns the new agent
}
type ModelInfo struct {
    ID, Display, Description string
}

// Reloader backs /reload.
type Reloader interface {
    Reload(ctx context.Context) (ReloadResult, error)
}
type ReloadResult struct {
    Agent      Agent           // replaces the live agent
    Memory     []MemoryFile    // for /memory display
    MCPServers []MCPServerInfo // for /mcp display
    Skills     []SkillInfo     // for /skills display
    Note       string          // optional system-message line
}

// SessionSwitcher backs /switch (issues #48 / #53). Hosts that
// manage multiple sessions (e.g. a remote daemon with per-caller
// bearer auth) implement it so operators can hop between sessions
// mid-run without exiting. SwitchToSession returns a SwitchTarget
// the TUI applies via a local detach + attach — see the
// SwitchTarget doc for the lifecycle contract (host owns the
// outgoing Agent; core-tui only cancels LOCAL contexts, does not
// touch server-side sessions).
type SessionSwitcher interface {
    Sessions() []SessionInfo
    SwitchToSession(id string) (SwitchTarget, error)
}
type SessionInfo struct {
    ID, Display, Description string
    Current                  bool
    // Input (issue #56) turns the row into an ACTION row: Enter
    // opens a single-line text-input Dialog stacked on the picker
    // and the row's own Submit closure produces the SwitchTarget,
    // so SwitchToSession never sees a synthetic ID. Backs the
    // "+ Attach to endpoint…" row on multi-daemon hosts.
    Input *SessionInput
}
type SessionInput struct {
    Title, Prompt, Placeholder, Initial string
    Validate func(value string) string            // "" = valid
    Submit   func(value string) (SwitchTarget, error)
}

// The primitive behind it is a general one — any Dialog can stack a
// text prompt on top of itself:
//
//   NewTextInputDialog(TextInputConfig{...}) Dialog
//
// Dialogs that own a text widget implement KeyMsgDialog (optional
// extension of Dialog) to receive the raw tea.KeyPressMsg instead
// of the normalized stroke string, and cursorDialog (also optional)
// to say where the terminal cursor belongs — a position relative to
// the dialog's own top-left cell, which View offsets by wherever it
// composited the dialog (R-CUR-1..3). Folding that into Dialog
// itself is part of the contract work in #115.

// SwitchTarget is also reachable via SlashResult.SwitchTo, so any
// SlashProvider / AsyncSlashProvider can request an Agent swap
// alongside its normal system-message / modal-answer output:
type SwitchTarget struct {
    Agent        Agent               // required
    UsageTracker UsageTracker        // nil = keep existing
    Prompter     PermissionPrompter  // nil = keep
    Elicitor     Elicitor            // nil = keep
    Notifier     *Notifier           // nil = keep
    Memory       []MemoryFile        // nil = keep; non-nil replaces
    Skills       []SkillInfo         // nil = keep; non-nil replaces
    MCPServers   []MCPServerInfo     // nil = keep; non-nil replaces
    Branding     *Branding           // nil = keep; non-nil replaces
    Note         string              // optional post-switch system row
}

// PermissionController backs /permissions, /allow and /deny.
// SessionApprovals is what the /permissions review picker lists — the
// gate's recollection of the approval-shaped decisions taken this
// session. Persisting an allow-always decision is NOT a method here:
// it arrives on the Options.AlwaysAllow callback (§3.4), because the
// decision is made in the TUI's own modal and the gate that would
// implement this interface is not necessarily the thing that owns the
// settings file.
type PermissionController interface {
    SessionApprovals() []ApprovalLog
    AddAllowPatterns(patterns []string) error
    AddDenyPatterns(patterns []string) error
    AddBuiltinAllowExtra(bundleName string) error
}
type ApprovalLog struct {
    Tool     string
    Key      string
    Decision string // "allow-once" | "allow-session" | "deny" | ...
}

// PricingController backs /pricing.
type PricingController interface {
    Refresh(ctx context.Context) (summary string, err error)
    Set(modelID string, inputPerMTok, outputPerMTok float64) (summary string, err error)
}

// ToolLister backs /tools.
type ToolLister interface {
    Tools() []ToolInfo
}
type ToolInfo struct {
    Name, Description, Source, GateState string
}

// InjectableAgent is an optional capability the TUI checks via type
// assertion when Options.MidTurnInjectionMode == InjectIntoCurrent.
// When implemented, operator-typed-during-streaming prompts route
// through Inject so they land in the running turn's context (rather
// than buffering for the next turn). Without the capability,
// MidTurnInjectionMode silently degrades to QueueForNext.
//
// See R-CHAT-11.
type InjectableAgent interface {
    Inject(message string) error
}

// InboxDrainer is for hosts whose agent queues operator-injected
// messages in an inbox distinct from the per-turn prompt. With
// InjectableAgent it is what lets the TUI drive an auto-continue loop
// over a runner it cannot reach into — the ADK case, where the
// iterator-shaped runner owns its own loop and exposes no mid-turn
// hook. DrainInbox returns the queued messages AND removes them in
// the same call, because the TUI immediately submits them as a
// synthetic turn; an empty return is "nothing to continue with".
// PendingInboxCount is the non-destructive peek used for sizing and
// hints, and may be a coarse upper bound.
//
// See issue #9 and Options.MidTurnInjectionMode ==
// AutoContinueFromInbox.
type InboxDrainer interface {
    DrainInbox() []string
    PendingInboxCount() int
}

// WakeRequester is an optional capability for hosts whose agent
// emits "I need the operator's attention" signals. The TUI
// subscribes once at startup; each receive triggers a transient
// toast banner. Hosts own channel lifecycle — closing the channel
// is fine.
//
// See R-WAKE-1.
type WakeRequester interface {
    WakeRequested() <-chan struct{}
}

// ContentRunner is an optional Agent capability for hosts that
// support structured-prompt entry — driving turns from `[]Content`
// instead of a single string. Used for retry / replay flows where
// the host has pre-built the conversation context. Nothing in
// package tui asserts it: the submit path always uses
// Run(ctx, prompt), and RunWithContents is called only by
// host-supplied affordances, which is why it is a deletion candidate
// under #77.
//
// See R-CHAT-12.
type Content struct {
    Role  string
    Text  string
    Parts []ContentPart
}
type ContentPart struct {
    Kind string
    Data any
}
type ContentRunner interface {
    RunWithContents(ctx context.Context, contents []Content) iter.Seq2[Event, error]
}

// SubagentLister backs /subagents (v1 read-only).
type SubagentLister interface {
    Subagents() []SubagentInfo
}
type SubagentInfo struct {
    Name, Status, LastReport string
    StartedAt                time.Time
}

// SubagentEventReader backs the subagent drill-down: `/subagents
// <name>` opens an overlay with the UNTRUNCATED LastReport (#70)
// above the subagent's turn log (#71), and a running sync
// subagent's tool row grows a live preview block underneath it
// that collapses to a one-line summary when the result lands.
//
// Polled, not streamed, and deliberately so: a subagent's turns are
// kept off the parent's event stream (they'd flood every attached
// chat view), so the only live view of them is a cursored re-read
// of the host's log. `since` is a seq cursor; 0 means "from the
// start"; the reply carries the cursor to resume from. Both callers
// poll once a second while their surface is open, off the render
// path with a bounded context.
//
// A name the host cannot resolve MUST return *SubagentNotFoundError
// carrying the names that would resolve — not an empty page. The
// two are different facts, and flattening them makes the TUI paint
// a convincing empty turn log for a typo (the server-side half of
// this is go-steer/core-agent#694).
type SubagentEventReader interface {
    SubagentEvents(ctx context.Context, name string, since int64) (SubagentEventPage, error)
}
type SubagentEventPage struct {
    Events    []SubagentEvent
    NextSince int64
    Truncated bool
}
type SubagentEvent struct {
    Seq         int64
    Timestamp   time.Time
    Author      string
    Text        string
    ToolCalls   []SubagentToolCall
    ToolResults []SubagentToolResult
}
type SubagentToolCall struct {
    ID   string
    Name string
    Args map[string]any
}
type SubagentToolResult struct {
    ID       string
    Name     string
    Response map[string]any
    Error    string
}
type SubagentNotFoundError struct {
    Name      string
    Available []string
}

// SlashProvider lets an agent advertise its own slash commands. The
// TUI asks SlashCommands each time the operator opens the / palette
// and again on /help — not once at startup — so a host whose catalog
// changes mid-session does not have to announce it. Invocations route
// back through InvokeSlash. /help renders the host's entries as their
// own section below the built-ins; the palette merges them in as
// ordinary rows after the built-ins it painted on the keystroke.
//
// Built-ins win, silently: dispatch tries the built-in table first
// and only an unrecognized name reaches the host, so a spec that
// reuses a built-in name is unreachable and stays visible in the
// palette anyway. Pick names the built-in catalog does not already
// have.
type SlashProvider interface {
    SlashCommands() []SlashCommandSpec
    InvokeSlash(ctx context.Context, name, args string) (SlashResult, error)
}
type SlashCommandSpec struct {
    Name        string   // bare name, no leading "/"
    Aliases     []string // optional
    Description string   // shown in /help and the palette hint
}
type SlashResult struct {
    SystemMessage string        // optional line rendered in chat after the call
    ModalAnswer   *SideAnswer   // optional /btw-style modal (R-CMD-5)
    SwitchTo      *SwitchTarget // optional mid-run Agent swap (issue #48)
}

// AsyncSlashProvider and AsyncSlashProviderWithPreamble are the
// non-blocking shapes, for hosts whose commands do network or file
// I/O (issues #10 / #16). The channel carries exactly one
// SlashResultOrErr and the ctx is cancelled when the operator hits
// Esc, at which point the eventual value is discarded. The preamble
// variant additionally returns a line to append to the chat
// synchronously at dispatch, for work slow enough that the bottom-bar
// toast is easy to miss.
//
// Both are refinements of SlashProvider rather than alternatives to
// it: dispatch asserts SlashProvider FIRST and only then looks for an
// async shape, so an adapter that implements one of these without
// also implementing InvokeSlash has every one of its commands
// silently declined. The two async shapes share a method name and
// differ in return type, so one Go type can satisfy only one of them;
// dispatch prefers the preamble variant. Collapsing all three into
// SlashProvider is #77's proposal.
type AsyncSlashProvider interface {
    SlashCommands() []SlashCommandSpec
    InvokeSlashAsync(ctx context.Context, name, args string) <-chan SlashResultOrErr
}
type AsyncSlashProviderWithPreamble interface {
    SlashCommands() []SlashCommandSpec
    InvokeSlashAsync(ctx context.Context, name, args string) (preamble string, results <-chan SlashResultOrErr)
}
type SlashResultOrErr struct {
    Res SlashResult
    Err error
}

// SideAnswer carries a /btw-style transient Q+A overlay. Renders as
// a dismissable Glamour modal; the answer does NOT land in chat
// history. When Err is non-nil the modal renders an error state in
// place of the answer body. See R-CMD-5.
type SideAnswer struct {
    Question string
    Answer   string
    Err      error
}
```

`MentionProvider` — **SPECIFIED, NOT SHIPPED as of v0.20.0.** Neither
the type nor `Options.MentionProviders` exists in `package tui`; only
the built-in file provider runs today, and `@sym:`-style prefixes are
inert text. Whether to build R-AT-4 or drop it is part of the
exported-surface audit (issue #78) — the same open question §3.5
records against `UserPrompter`.

As specified: not a capability on Agent but a host-side configuration
item delivered through `Options.MentionProviders`. The TUI merges
entries from every provider into the `@` palette under section headers
(R-AT-4). Multiple providers can share the prefix namespace as long as
their `Prefix` differs (e.g. one provider serves `@sym:`, another
`@git:`).

```go
type MentionProvider struct {
    // Prefix is the literal that follows "@" to scope the lookup
    // (e.g. "sym:", "git:", "url:"). The built-in file provider has
    // an empty Prefix and matches any "@" not claimed by a registered
    // provider.
    Prefix string

    // SectionHeader is the title shown above this provider's entries
    // in the palette (e.g. "Symbols", "Git refs").
    SectionHeader string

    // Lookup runs on each keystroke. Should return ranked matches
    // (prefix matches first, then substring) and is expected to
    // de-bounce internally if it touches anything expensive.
    Lookup func(ctx context.Context, query string) ([]MentionMatch, error)
}

type MentionMatch struct {
    // Display is the visible row in the palette.
    Display string
    // Insert is the literal that replaces the typed @-token on
    // selection (the form persisted to prompt history).
    Insert string
    // Expand is called when the user submits a prompt containing
    // Insert; the returned string is inlined in place of Insert
    // before the prompt reaches the agent. Empty Expand means the
    // Insert form is sent as-is.
    Expand func(ctx context.Context) (string, error)
}
```

### 3.4 Required-from-host (TUI → host) callbacks

These are not capability interfaces — they are concrete callbacks the
host wires into the TUI. They live on `Options` so they're explicit at
construction time.

```go
type Options struct {
    Agent  Agent          // required
    Cfg    Config         // required; subset of host config the TUI needs

    // Optional environment data, used for display + transcripts.
    AgentsDir   string
    PathScope   PathScope            // for @file + scope warnings
    Memory      []MemoryFile         // for /memory
    MCPServers  []MCPServerInfo      // for /mcp
    Skills      []SkillInfo          // for /skills
    UsageTracker UsageTracker        // for /stats + header

    // Branding.
    Branding Branding

    // Persistence callbacks.
    PersistModelChoice func(modelID string) error

    // Permission-mode wiring (R-PERM-6/7). Zero value hides the
    // permission-mode chip and disables Shift+Tab cycling.
    PermissionMode PermissionModeWiring

    // Status surface layout (R-USE-2). StatusHeader (zero value) =
    // single line above the chat; StatusSidebar = right-hand column.
    StatusLayout StatusLayout

    // PersistStatusLayout is invoked when the user toggles the
    // status layout at runtime (Ctrl+B). Nil leaves the toggle
    // session-local; non-nil lets the host write the choice to a
    // settings file so it survives restarts.
    PersistStatusLayout func(StatusLayout) error

    // Spinner verb pools (R-CHAT-3). Nil = built-in defaults.
    ThinkingPhrases []string // rotated while the model is generating
    WorkingPhrases  []string // rotated while a tool call is in flight

    // Slash-command extension.
    Commands []SlashCommand

    // Tool-summary extension. Summarizer output overrides
    // WorkingPhrases rotation for the tool it covers (R-CHAT-3).
    ToolSummarizers map[string]ToolSummarizer

    // Markdown style override (default: light/dark autodetect).
    MarkdownStyle string

    // Mouse default (on if zero-value left).
    MouseDefault MouseSetting

    // MouseHint is the auto-expiring "Hold Shift to select text"
    // overlay shown when mouse capture is on (R-MOUSE-3). Empty
    // string uses the default. Zero MouseHintTTL uses 5 seconds.
    MouseHint    string
    MouseHintTTL time.Duration

    // RenderMode picks alt-screen vs hybrid-scrollback rendering
    // (R-CHAT-9). RenderAltScreen is the default.
    RenderMode RenderMode

    // MentionProviders extends the @ palette beyond files (R-AT-4).
    // The built-in file provider always runs; this list is additive.
    MentionProviders []MentionProvider
}

type RenderMode int

const (
    RenderAltScreen RenderMode = iota
    RenderInline
)

// PermissionModeWiring backs R-PERM-6 / R-PERM-7. Set is required
// when any field is non-zero; Persist is optional.
type PermissionModeWiring struct {
    Initial PermissionMode
    Set     func(PermissionMode) error
    Persist func(PermissionMode) error
}

type PermissionMode int

const (
    PermissionModeDefault PermissionMode = iota
    PermissionModeAcceptEdits
    PermissionModePlan
    PermissionModeBypass
)

type StatusLayout int

const (
    StatusHeader StatusLayout = iota
    StatusSidebar
)
```

`Config` carries only what the TUI cares about: current model name,
`/mouse` setting, and a `path_scope` slice. Anything host-specific
(provider configs, MCP transport, allow patterns) is the host's own
struct and never crosses into the TUI.

### 3.5 The two prompter / elicitor interfaces

These are slightly different from the capability interfaces: the TUI
*implements* them and the host wires them into its gate / MCP servers.

```go
// PermissionPrompter is implemented by the TUI. Hosts pass the value
// returned by tui.NewPrompter() into their permission gate; the gate
// calls AskApproval and blocks on the channel until the user clicks.
type PermissionPrompter interface {
    AskApproval(ctx context.Context, req PermissionRequest) (PermissionDecision, error)
}

type PermissionRequest struct {
    Kind     PermissionKind
    ToolName string

    // Detail is the rendered payload the user is being asked to
    // approve (R-PERM-1). For file edits, the unified diff text;
    // for shell, the verbatim command; for HTTP, the URL + method
    // + body summary; for other tools, a key=value or JSON dump.
    Detail     string
    DetailKind DetailKind

    Verb   string // empty when no verb extractable
    Source string // empty for parent agent; subagent name otherwise

    // Persistence hint that the host's gate filled in. Round-tripped
    // back to the host on a DecisionAllowAlways via the AlwaysAllow
    // callback.
    PersistTool string
    PersistKey  string
}

// DetailKind picks the Glamour code-fence language tag the modal
// uses when rendering Detail. DetailPlain renders unstyled.
type DetailKind int

const (
    DetailPlain DetailKind = iota
    DetailDiff   // unified diff (red/green hunks)
    DetailShell  // bash / sh command line
    DetailHTTP   // URL + method + body
    DetailArgs   // JSON or key=value tool args
)

type PermissionDecision int

const (
    DecisionDeny PermissionDecision = iota
    DecisionAllowOnce
    DecisionAllowSession
    DecisionAllowSessionVerb
    DecisionAllowSessionTool
    DecisionAllowAlways
)

// Elicitor mirrors the pattern for MCP elicitation.
type Elicitor interface {
    Elicit(ctx context.Context, serverName string, req ElicitRequest) (ElicitResult, error)
}

// UserPrompter — SPECIFIED, NOT SHIPPED as of v0.20.0. Neither the
// interface nor NewUserPrompter() exists in package tui; whether to
// build it or drop R-PROMPT-1 is part of the exported-surface audit
// (issue #78). Hosts needing agent-initiated questions today route
// them through Elicitor.
//
// As specified: implemented by the TUI, with hosts passing the value
// returned by tui.NewUserPrompter() into their agent so the agent
// can call AskUser mid-turn for structured multiple-choice input
// (R-PROMPT-1). Distinct from Elicitor (MCP-server-initiated, form-
// shaped) — this is the agent itself asking a discrete question.
type UserPrompter interface {
    AskUser(ctx context.Context, req UserPromptRequest) (UserPromptResponse, error)
}

type UserPromptRequest struct {
    Question     string       // shown bold at the top of the modal
    Description  string       // optional dim subtitle below the question
    Choices      []UserChoice // ≥ 2 entries; renders as a huh.Select
    DefaultIndex int          // index of the initially highlighted choice
}

type UserChoice struct {
    Label       string // primary text on the row
    Description string // optional dim subtitle for the row
    Value       string // round-tripped back as Response.Selected on confirm
}

type UserPromptResponse struct {
    Selected  string // the chosen UserChoice.Value
    Cancelled bool   // true when the operator pressed Esc
}
```

Translation from / to the host's native types is the host adapter's
responsibility (see §6).

## 4. Lifecycle

```
                ┌───────────────┐
                │   host main   │
                └──────┬────────┘
                       │ Builds: agent, permissions.Gate, mcp servers,
                       │         skills, memory, usage tracker
                       ▼
                tui.NewPrompter()  ───┐
                                      ├─→ wired into Gate before Run
                tui.NewElicitor() ────┤   wired into each MCP server
                                      │   before Connect (so server can
                                      │   hold .Elicit closure)
                       │
                       ▼
                tui.Run(ctx, Options{
                    Agent: hostAdapter{inner: agent},
                    Prompter: prompter,   // for the host's gate
                    Elicitor: elicitor,
                    ...
                })
                       │
                       ▼
              ┌────────────────┐
              │ tea.Program    │     core-tui owns:
              │ ──────────     │     - WithAltScreen
              │ Update / View  │     - WithMouseCellMotion (when on)
              │ Loop           │     - light/dark detection
              └────────┬───────┘
                       │
       Operator types prompt + Enter
                       │
                       ▼
              startAgentTurn(ctx, p, agent, prompt)
                       │
                       │ goroutine ranges over agent.Run() iterator
                       │ and sends Events translated to tea.Msgs
                       │
                  ┌────┴────┐
                  ▼         ▼
            streamChunkMsg  toolCallMsg
            usageMsg        turnDoneMsg / turnErrMsg / turnCancelledMsg
                       │
                       ▼
              Model.Update accumulates, View renders the
                       │ in-progress message through Glamour on every
                       │ partial; on turn completion the final render
                       │ is cached so re-paints don't re-Glamour
                       ▼
              Operator quits (or Ctrl+C)
                       │
                       ▼
              transcript.Save(<AgentsDir>/sessions/<ts>.json)
              MCP servers .Close()
              p.Run() returns
                       │
                       ▼
              tui.Run returns exitCode to host
```

### 4.0 Spinner state inference (R-CHAT-3)

The TUI tracks one bit of additional state — *"is a tool call
outstanding?"* — derived purely from the existing `Event` stream:

- A `ToolCall` event flips the bit to **tool-active**.
- A subsequent `Text` event (`Partial=true` or `false`) flips it
  back to **model-active**.
- `Usage` and other non-text/non-tool events leave the bit alone.

The spinner's verb pool is chosen from this bit:
`Options.ThinkingPhrases` while model-active, `Options.WorkingPhrases`
while tool-active. If `Options.ToolSummarizers` covers the tool
named in the most recent `ToolCall`, the summarizer's
present-continuous string replaces the rotation entirely. No new
`Event` field is needed — the stream already conveys the transition.

### 4.1 Concurrency model

- One in-flight turn at a time. Multiple `Run` calls on the same Agent
  are not supported; the TUI guarantees serial calls.
- The agent goroutine never touches `Model` directly. It only calls
  `tea.Program.Send`. Bubble Tea serializes Update calls.
- The PermissionPrompter and Elicitor each hold a buffered (cap 1)
  reply channel. The TUI's Update writes the decision; the agent's
  blocking goroutine reads it. Context cancellation drops the
  blocking side and starts an async drainer on the channel to avoid
  leaks.
- **No capability method runs on the event loop** (issue #114).
  `Update` and `View` share one goroutine, so a slow host method
  called from either freezes the whole TUI — spinner, cursor blink,
  and Ctrl+C included, since those all arrive as messages the frozen
  loop cannot read. core-tui is a library and cannot assume any §3.3
  capability is fast, so every call site returns a `tea.Cmd` that
  does the work off-loop and delivers the result as a msg. `View`
  reads only snapshots (`host_snapshot.go` for the status header and
  the sidebar roster; per-dialog snapshots for the model and session
  pickers). Capability methods that take a `context.Context` are
  given a bounded one — the signature is the host saying it may be
  slow, and answering with `context.Background()` inverts the
  contract. The rule covers the `/cmd` path as well as bare keystrokes
  (issue #137): a slash command the operator asked for still can't be
  allowed to freeze the loop.
- **A reply that has been overtaken is dropped, not rendered.** Every
  off-loop reply carries the generation it left under, and `Update`
  drops it when that generation has turned over — `sessionGen` for
  anything session-scoped, `paletteSeq` for a palette fill, `slashSeq`
  for a slash dispatch. `slashSeq` exists because slash commands are
  typed back to back within one session: with the `SlashCommands()`
  name match off-loop, an "unknown command /foo" verdict can arrive
  after the operator has moved on, and a row blaming `/foo` printed
  under `/help`'s output is worse than no row at all. The same stamp
  keeps the multi-stage flows honest — `/switch <id>` enumerates
  before it switches, and a superseded enumerate must never be allowed
  to drive the switch.
- **Nor do the `Options.*` host callbacks** (issue #137).
  `PersistModelChoice`, `PersistThemeChoice`, `PersistStatusLayout`
  and `PermissionMode.Set` / `.Persist` are plain func fields rather
  than §3.3 capabilities, but they are still host code and the
  persistence ones write to the host's config file. They run in a
  `tea.Cmd` for the same reason, and their errors surface as rows
  instead of being discarded. `PermissionMode` is the sharp case:
  Shift+Tab flips the chip immediately (a bare keystroke's indicator
  must track the key), and the chip rolls back if the host declines
  the mode — a chip reading `bypassPermissions` while the gate is
  still asking is a safety claim core-tui cannot back.

### 4.2 Error semantics

- Recoverable: agent emits `turnErrMsg`; the TUI shows an Error
  message in the chat, re-enables input.
- Unrecoverable: the TUI re-renders an Error and stays interactive
  (no auto-quit). Operator can `/quit`.
- Cancellation: distinguished from errors (turnCancelledMsg →
  "(interrupted)" notice rather than error banner).

### 4.3 Render mode (R-CHAT-9)

Two strategies governed by `Options.RenderMode`:

- **`RenderAltScreen`** (default) — `tea.View.AltScreen = true`. The
  TUI owns the full terminal viewport for the duration of the
  session. Scrollback is the in-app `viewport.Model`. Matches every
  v1 source TUI and is the safe choice for short or moderately
  scrolling sessions.
- **`RenderInline`** — `tea.View.AltScreen = false`. As each turn
  commits (the assistant message reaches its final Glamour render
  and any tool calls have rendered into history), the rendered block
  is `tea.Println`-pushed into the terminal's native scrollback and
  removed from the in-app viewport. `View()` keeps only:
  - the live input row,
  - the in-progress assistant message (with its streaming spinner /
    `Thinking…` / `Working…` indicator), and
  - any active modal overlay.
  On `WindowSizeMsg` the TUI debounces (~150 ms), flushes any
  pending `Println` writes, and recomputes wrap widths so the
  scrollback stays clean across resizes. The TUI tracks the boundary
  between "in scrollback" and "in viewport" so the per-turn `─` rule
  (style.md §3) appears exactly once even when the boundary moves.

The mode is not user-toggleable at runtime — switching alters
terminal state in ways that can corrupt the scrollback. Hosts pick
the mode at construction and keep it for the session.

## 5. Slash-command routing

`commands.go` exposes:

```go
type SlashAction int

const (
    ActionNone SlashAction = iota
    ActionHelp
    ActionClear
    ActionQuit
    ActionMemory
    ActionStats
    ActionModel
    ActionMCP
    ActionSkills
    ActionTools
    ActionReload
    ActionMouse
    ActionPermissions
    ActionPermissionsList
    ActionAllow
    ActionDeny
    ActionPricing
    ActionInterrupt
    ActionHostExtension  // routed to Options.Commands
    ActionAgentExtension // routed to SlashProvider.InvokeSlash
)

func ParseSlash(input string) (action SlashAction, command, args string, isSlash bool)
```

Update dispatches built-ins inline, routes `ActionHostExtension` to
`Options.Commands`, and routes `ActionAgentExtension` to the agent's
`SlashProvider`. Resolution order when a name appears in more than one
source: built-in > host extension > agent extension. Shadowed entries
are dropped from `/help` and the palette with a one-time system warning
at startup so the operator notices the collision.

Host extensions receive a `CommandContext`:

```go
type CommandContext interface {
    PostSystem(line string)
    PostError(err error)
    Args() string
    Agent() Agent
}
```

## 6. Host adapters

§3 is the normative surface for any host. The two named hosts below
are illustrations of the same pattern, not special cases.

### 6.0 Adapter contract

A host adapter is the glue between a host's own agent types and the
neutral interfaces in §3. Every adapter does the same four things:

1. **Implement `Agent`.** Wrap the host's native agent so its event
   stream is translated to `tui.Event` (see the cogo example in §3.2).
   This is the only required interface.
2. **Implement zero or more capability interfaces** from §3.3
   (`ModelSwapper`, `Reloader`, `PermissionController`,
   `PricingController`, `ToolLister`, `SubagentLister`,
   `SubagentEventReader`, `SessionSwitcher`, `RemoteInterrupter`,
   `StatusReporter`, `SlashProvider`). Each one lights up the
   corresponding slash command or UI affordance; missing ones degrade
   to a "not available" message. Capabilities may be implemented on
   the same type as `Agent` or on separate types — the TUI feature-
   detects via type assertion.
3. **Wire the TUI-implemented interfaces** (`PermissionPrompter`,
   `Elicitor`) into the host's permission gate and MCP servers
   *before* the first `Agent.Run`. The TUI provides constructors
   (`tui.NewPrompter`, `tui.NewElicitor`); the host passes the
   returned values into its own plumbing.
4. **Construct `Options` and call `tui.Run(ctx, opts)`.** Fill in
   the fields the adapter has data for; leave the rest zero. Field
   additions to `Options` are non-breaking (see §8) so adapters
   compiled against an older `core-tui` keep working.

Adapters typically live in a `cmd/<host>-tui` main package (or fold
into the host's existing entrypoint). Anything host-specific —
provider configs, MCP transports, allow-pattern stores — stays on
the host side of the adapter and never crosses into `tui`.

A scaffold adapter, with stubs for each capability, ships as
`examples/local/` (see §11). Third-party hosts can copy it as a
starting point. `examples/core-agent/` is the fuller worked case:
the same four steps against a host-shaped agent type, in both the
in-process and attach flavors, with the capabilities each flavor
declines called out and why.

### 6.1 cogo (Gemini-only, local-only) — illustrative

Not a scheduled migration (§8). Kept as the small-host shape, where
most capabilities are declined. cogo has the TUI under
`internal/tui`. Migration would be:

1. Delete `internal/tui` entirely.
2. Add `cmd/cogo-tui/main.go` (or fold into existing `cmd/cogo`)
   containing:
   - The 30-line `Run` adapter (see §3.2).
   - Adapters for `ModelSwapper`, `Reloader` from cogo's existing
     wiring (which already supports these).
   - A `SlashProvider` adapter exposing cogo-specific commands and
     routing invocations back into cogo's command layer.
   - cogo does **not** implement `PricingController` — `/pricing`
     gracefully reports "not available."
   - cogo does **not** implement `SubagentLister`, `ToolLister`
     (initially) — those slash commands degrade similarly.
   - Call `tui.Run(ctx, opts)`.

Adapter LOC budget: ~150 lines total.

### 6.2 core-agent (multi-provider, local + attach) — the reference host

This is the migration that actually happened; see `MIGRATION.md` §3
for its shipped shape and `examples/core-agent/` for a runnable
condensation of it. core-agent's setup mirrors §6.1 but adds:

- `PricingController` adapter (wraps the existing `internal/pricing`
  package).
- `PermissionController` adapter (wraps `permissions.Gate`).
- `ToolLister` adapter.
- `SubagentLister` adapter (over the `BackgroundAgentManager`), plus
  a `SubagentEventReader` adapter over
  `GET /sessions/{id}/agents/{name}/events` for the turn drill-down.
- `RemoteInterrupter` adapter (wraps `Agent.Interrupt`), so
  `/interrupt` reaches a daemon-driven turn in attach mode.
- `SlashProvider` adapter exposing core-agent's agent-side commands
  (and, in attach mode, forwarding `InvokeSlash` to the remote agent
  over HTTP so the same command set works locally and over the wire).
- A second binary `cmd/core-agent-tui-attach/main.go` that constructs
  the agent from `internal/attachclient` instead of locally. From the
  TUI's perspective both binaries are identical — they pass an `Agent`
  that conforms to the same interface; the attach version's `Run` just
  sends HTTP requests under the hood.

Adapter LOC budget: ~400 lines (more capabilities to wire).

## 7. Test strategy

- **Unit tests** (`*_test.go` per file) — driven by direct
  `Update(msg)` invocations and asserts on `History.Snapshot()`,
  modal state, palette state. Mirrors the ~30 existing test files in
  each source TUI; we lift them.
- **Smoke tests** — headless `tea.Program` with the alt-screen
  disabled, feeding a `bytes.Buffer` for stdin. Validates startup +
  shutdown, and drives the flows that span more than one goroutine:
  chiefly the permission round-trip, where a scripted tool call trips
  the gate, the modal renders, and the decision unblocks the turn.
  Not built yet — tracked as issue #81, which owns the fixture.
- **Capability tests** — a `mockagent` package implements `Agent` +
  every capability; tests assert that each slash command's
  "available" and "not available" paths render correctly when the
  capability is present / absent.
- **Adapter example** — `examples/core-agent` builds an adapter
  against `examples/core-agent/fakehost`, a local stand-in for the
  reference host's agent type and attach client; failing to compile
  after a refactor is a CI signal that the plug-in surface broke.
  With one gating host this is the only compile-time canary we have,
  so it matters more than it did when there were two.

  Shipped for issue #82. Two flavors — `localAdapter` (in-process,
  the shape of core-agent's `cmd/core-agent/coretui_enabled.go`) and
  `attachAdapter` (HTTP + SSE, the shape of its
  `internal/coretuiremote`) — covering 18 plug-in interfaces between
  them via `var _ tui.X = ...` assertions, which is what makes the
  build fail rather than merely the behavior drift. It deliberately
  does NOT depend on the real core-agent module: core-agent depends
  on core-tui, so that import would close an ecosystem-level cycle
  and chain this repo's CI to another repo's release cadence.

  The example also carries behavioral tests, because a canary that
  only typechecks misses the half of the contract that lives in the
  doc comments — cursor paging, the `*SubagentNotFoundError`
  distinction, in-band tool errors becoming `ToolResult.Error`.

## 8. Compatibility & versioning

- v0.x — pre-1.0; treat all surface as breakable except the items in
  §3, which are enumerated symbol by symbol in
  [`api-surface.md`](./api-surface.md) §3.1 (156 of the 265 exported
  symbols). Field additions to Options are non-breaking by Go-module
  rules (struct literal with explicit field names is the documented
  usage).
- v1.0 — declared once the reference host (core-agent) has been
  migrated and green for one minor release, AND the pre-freeze work
  in the [v1.0 milestone](https://github.com/go-steer/core-tui/milestone/1)
  has landed: the capability-surface consolidation (§10 risk 1), the
  exported-surface audit, removal of the vestigial render paths, and
  an `apidiff` gate in CI so the promise is machine-checked rather
  than reviewer-checked.

  The original criterion required *both* cogo and core-agent. cogo
  is no longer a gating consumer — core-agent completed its migration
  (its `internal/tui/` is deleted and it tracks each release), while
  cogo never started one. Additional hosts adopting core-tui are
  welcome and tracked, but do not gate a release.
- Pre-1.0 changes are recorded in `CHANGELOG.md`.

## 9. What we deliberately leave out

- A built-in attach client (D11): hosts that need attach own the
  client and present it as a conforming `Agent`.
- Headless mode (D14): the host owns its REPL.
- A registry for plug-ins beyond `Options.Commands` (D13): YAGNI.
- Built-in OTEL (D21): hosts trace from the agent side.

## 10. Open risks

1. **Adapter boilerplate fatigue.** If the capability interfaces grow
   past ~10, each host's adapter becomes annoying to write. Mitigation:
   when a capability is required for "most" hosts, fold it into the
   base `Agent` interface (and accept the breaking change before v1.0).
2. **Hidden ADK assumptions in the rendering code.** Tool-call args
   are `map[string]any` which is a JSON-ish shape ADK happens to use.
   If a non-ADK adapter ever wants to render structured tool args
   (`google.golang.org/genai.Schema`), we'd revisit. v1 ships with
   JSON-shaped args by convention.
3. **MCP elicit schema drift.** core-tui's `ElicitRequest` shape today
   reflects the current MCP SDK schema flattening. If the SDK adds
   nested-object support, we need to extend the elicit modal. Out of
   scope for v1; document the constraint in the API doc.
4. **Charm v2 churn.** We target the Charm v2 line (Bubble Tea v2 /
   Bubbles v2 / Lip Gloss v2 / Glamour v2 / Huh v2) per
   [decisions.md D2 + D26](./decisions.md). v2.0 is stable but young —
   patch / minor releases may surface behavioral fixes we need to
   absorb. Mitigation: keep `tea`-touching code concentrated in
   `program.go`, `model.go`, `update.go`, `view.go`, and the modal
   files; pin minor versions in `go.mod` and bump deliberately.

## 11. Implementation plan (informational)

Historical — this was the order sketched before v0.1.0, kept for the
record. Steps 1–5 and 7 are done; what remains of step 6 is tracked
on the [v1.0 milestone](https://github.com/go-steer/core-tui/milestone/1).

1. Scaffold module + `tui` package skeleton; copy `decisions.md`,
   `requirements.md`, `design.md` into place. ✅ (this commit)
2. Lift `internal/tui` files from `core-agent` (the more recent
   superset) into `tui/`, replacing ADK / MCP imports with the
   neutral types in `agent.go` / `elicitor.go` / `prompter.go`.
3. Implement the translation layer in `agentcmd.go` and verify by
   compiling against a stub Agent.
4. Lift the existing test suite, fix imports, get to green.
5. Implement the capability feature-detection in Update + the "not
   available" message paths.
6. Write `examples/local` (visual harness) and the `examples/core-agent`
   adapter sketch. ✓ Both shipped (the adapter sketch was issue
   #82). The originally-planned `examples/permissions` binary was
   dropped: `examples/local` already round-trips a real prompt through
   `tui.NewPrompter()` on `ctrl+y`, and the coverage a separate binary
   would have added belongs in the headless smoke harness (issue #81),
   not in a program a human has to run and eyeball.
7. Open migration PRs against the host(s). ✅ core-agent migrated as
   of core-tui v0.18.0; cogo never started one and no longer gates a
   release (§8).
