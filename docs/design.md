# core-tui Design

This document specifies how `core-tui` is structured, what its plug-in
surface looks like, and how the two named hosts (cogo, core-agent)
satisfy it. It is the design counterpart to `requirements.md`.

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
   ≤ 50-line adapter plus a `tui.Run(ctx, opts)` call.

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
    ├── local/          minimal: in-process echo "agent" → smoke testing
    ├── permissions/    fake tool calls exercising the modal
    ├── cogo/           cogo adapter sketch (uses cogo's agent pkg)
    └── core-agent/     core-agent adapter sketch (local + attach)
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

## 3. The plug-in surface

This section is normative — it is the only stable API hosts may rely
on. Everything else in `package tui` is subject to change.

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

The TUI feature-detects each via type assertion. Each interface
matches one user-visible feature and is documented as such.

```go
// Interruptible lets the TUI proactively cancel a turn beyond just
// cancelling the ctx — useful when the agent wraps its own
// cancellation semantics (core-agent's Agent.Interrupt for the
// attach-mode path). Optional; ctx cancellation always works.
type Interruptible interface {
    Interrupt() bool
}

// StatusReporter feeds the header bar.
type StatusReporter interface {
    Status() Status
}
type Status struct {
    ModelName string
    State     string // "idle" | "running" | "deferred" | ...
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
}

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

// PermissionController backs /permissions, /allow, /deny, persistence
// of allow-always decisions.
type PermissionController interface {
    SessionApprovals() []ApprovalLog
    AddAllowPatterns(patterns []string) error
    AddDenyPatterns(patterns []string) error
    AddBuiltinAllowExtra(bundleName string) error
    AlwaysAllow(req PermissionRequest) error
    Snapshot() PermissionSnapshot // for /permissions list
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
// the host has pre-built the conversation context. The TUI's
// default submit path still uses Run(ctx, prompt); RunWithContents
// is invoked by host-supplied affordances.
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

// SlashProvider lets an agent advertise its own slash commands. The
// TUI queries SlashCommands at startup and after Reload, merges the
// entries into /help and the palette under an agent-scoped section,
// and routes invocations back via InvokeSlash. Names that collide
// with built-ins are skipped (built-in wins) and a system warning
// is logged. Entries that collide with Options.Commands resolve to
// the host extension; the agent entry is shadowed.
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
    SystemMessage string      // optional line rendered in chat after the call
    ModalAnswer   *SideAnswer // optional /btw-style modal (R-CMD-5)
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

`MentionProvider` is not a capability on Agent — it's a host-side
configuration item delivered through `Options.MentionProviders`. The
TUI merges entries from every provider into the `@` palette under
section headers (R-AT-4). Multiple providers can share the prefix
namespace as long as their `Prefix` differs (e.g. one provider serves
`@sym:`, another `@git:`).

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

// UserPrompter is implemented by the TUI. Hosts pass the value
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
   `PricingController`, `ToolLister`, `SubagentLister`, `Interruptible`,
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
starting point.

### 6.1 cogo (Gemini-only, local-only)

cogo today has the TUI under `internal/tui`. Migration:

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

### 6.2 core-agent (multi-provider, local + attach)

core-agent's setup mirrors cogo but adds:

- `PricingController` adapter (wraps the existing `internal/pricing`
  package).
- `PermissionController` adapter (wraps `permissions.Gate`).
- `ToolLister` adapter.
- `SubagentLister` adapter (over the `BackgroundAgentManager`).
- `Interruptible` adapter (wraps `Agent.Interrupt`).
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
  shutdown.
- **Capability tests** — a `mockagent` package implements `Agent` +
  every capability; tests assert that each slash command's
  "available" and "not available" paths render correctly when the
  capability is present / absent.
- **Adapter examples** — `examples/cogo` and `examples/core-agent`
  build a one-file adapter against a fake of each host's agent.go;
  failing to compile after a refactor is a CI signal that the
  interface broke.

## 8. Compatibility & versioning

- v0.x — pre-1.0; treat all surface as breakable except the items in
  §3 (Agent + Event + capability interfaces + Options field names).
  Field additions to Options are non-breaking by Go-module rules
  (struct literal with explicit field names is the documented usage).
- v1.0 — declared once both cogo and core-agent are migrated and
  green for one minor release.
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

Suggested order, not normative:

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
6. Write `examples/local` (smoke), `examples/permissions` (modal
   exercise), `examples/cogo` and `examples/core-agent` adapter
   sketches.
7. Open migration PRs against cogo + core-agent.
