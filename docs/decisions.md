# Design Decisions Log

This file records the design questions that came up while drafting
`requirements.md` and `design.md`, the options considered for each, and
the recommendation taken. Where the recommendation is provisional —
i.e. the user may want to override it before implementation starts —
the option is flagged **(pending user confirmation)**.

The point of this file is not to be authoritative; it is to give the
user a single place to disagree with the assumptions baked into the
requirements + design documents.

---

## D1. Implementation language

**Question:** Go, or a rewrite in another language (Rust, TypeScript)?

**Options:**

- (A) **Go.** Matches both source TUIs verbatim — direct port, no
  language-bridge cost. Bubble Tea / Lip Gloss / Glamour / Bubbles are
  the de-facto Go TUI stack and both upstream TUIs already depend on
  them.
- (B) Rust with `ratatui`. Better long-term perf and crash safety; loses
  free-of-charge porting from existing Go code.
- (C) TypeScript with `Ink` or `blessed`. Wider authoring pool; awkward
  fit for an agent that's expected to embed into a Go binary.

**Recommendation: (A) Go.** Both consumers (cogo, core-agent) are Go,
they ship the agent as a Go binary, and the entire TUI codebase can be
lifted with minimal change. A non-Go core-tui would force every
embedder into an IPC layer.

---

## D2. Bubble Tea major version

**Question:** Bubble Tea v1 or v2?

**Options:**

- (A) **v1 (`github.com/charmbracelet/bubbletea`).** Both source TUIs
  use it. Stable, mature, well-tested.
- (B) v2 (preview). Newer architecture (e.g. focused components, better
  layout primitives) but API still in flux as of early 2026; would
  require rewriting much of the existing TUI rather than porting.

**Recommendation: (A) v1.** Keep the port mechanical. v2 can be a
follow-up migration once the API stabilizes.

---

## D3. Coupling to ADK `session.Event`

**Question:** Should the TUI consume `iter.Seq2[*session.Event, error]`
directly (like both source TUIs do), or define its own neutral event
type that adapters translate into?

**Options:**

- (A) Re-export the ADK iterator. Zero translation cost. **Locks
  core-tui into Google ADK forever.** If a future agent doesn't use ADK
  it cannot plug in.
- (B) **Define a neutral `tui.Event` type.** Adapters translate
  `session.Event` → `tui.Event`. The translation is ~30 lines (lift
  from `agentcmd.go`) and isolates the only ADK coupling in the
  current TUI. Hosts that use non-ADK agents (a hypothetical
  Anthropic-SDK-native agent, e.g.) plug in by writing a one-file
  adapter rather than a fork.
- (C) Generic over event type. Too much ceremony for a TUI consumer
  surface.

**Recommendation: (B).** The TUI today only touches three fields of
`session.Event` (Content.Parts → Text / FunctionCall, UsageMetadata,
Partial). A 5-field neutral struct covers it. Both cogo and core-agent
add a 30-line adapter and the rest of core-tui is provider-agnostic.

**Tradeoff acknowledged:** This makes the agent-side surface slightly
heavier (each host writes the adapter) in exchange for the TUI staying
portable. Given the user's stated goal — "tie this TUI to **either**
cogo or core-agent" — portability is the explicit ask.

---

## D4. Shape of the Agent plug-in interface

**Question:** One big interface, or a small required core plus
feature-detected capability interfaces?

**Options:**

- (A) One `Agent` interface with every method (Run, Interrupt, Tools,
  ModelName, RebuildAgent, ReloadFromDisk, RefreshPricing, ...). Forces
  every adapter to implement (or stub) every method.
- (B) **Small required core + optional capability interfaces.** The
  core is `Run(ctx, prompt) → iter[Event]`. Every other feature is a
  separate interface (`Interruptible`, `ModelSwapper`, `Reloader`,
  `PricingController`, `StatusReporter`, `ToolLister`,
  `SubagentLister`, ...). The TUI feature-detects each capability via
  type assertion and surfaces graceful "not available" messages when a
  capability is missing. Cogo can ship a thin agent (no autonomous, no
  attach); core-agent ships the full set.
- (C) Plug-in registry. Overkill for the scope.

**Recommendation: (B).** Mirrors how Go's `io` package works
(`io.Reader` + optional `io.ReaderAt`, etc.). Lets cogo implement only
what it has and lets core-agent implement everything without forcing
cogo to grow stubs.

---

## D5. Permission gate ownership

**Question:** Does core-tui own a `permissions.Gate` and policy code, or
does the host provide one?

**Options:**

- (A) Core-tui ships its own permissions package. Forks the existing
  one. Duplicate maintenance.
- (B) **Core-tui defines a minimal `PermissionPrompter` interface (it
  RECEIVES requests from the host's gate) and exports a `Prompter`
  implementation hosts wire into their own gate.** Host owns the gate;
  core-tui owns the UI side of the prompt. Mirrors how today's
  `internal/tui/prompter.go` works.
- (C) Generic over Gate. No — gate semantics (allow/deny/bundle/scope)
  are too specific.

**Recommendation: (B).** Define `tui.PermissionRequest` and
`tui.PermissionDecision` as neutral types in core-tui. Provide
`tui.NewPrompter()` returning a callback the host wires into its gate.
Adapters convert between host-specific `permissions.PromptRequest` and
the neutral `tui.PermissionRequest`. Twelve-line translation per
adapter, but the TUI has no opinion about persistence, bundle naming,
or path scope semantics.

---

## D6. MCP elicitation ownership

**Question:** Same as D5 but for MCP elicit modals.

**Options:**

- (A) Couple to `modelcontextprotocol/go-sdk/mcp` types.
- (B) **Define a neutral `tui.ElicitRequest` (a schema + a reply
  channel) and have the host adapter translate to/from MCP SDK
  types.** Same shape as D5.

**Recommendation: (B)** for the same reason as D5. The MCP SDK is
likely stable for the near term but core-tui shouldn't pin its consumer
to a specific SDK version.

---

## D7. Pricing system

**Question:** Does core-tui own pricing tables / LiteLLM refresh?

**Options:**

- (A) Lift `internal/pricing` and the LiteLLM client into core-tui.
- (B) **Pricing lives in the host.** The TUI exposes
  `/pricing refresh` and `/pricing set` slash commands; both invoke
  optional `PricingController` capability methods on the agent. Hosts
  that don't implement `PricingController` get a graceful "pricing not
  available in this host" response.

**Recommendation: (B).** Pricing is a property of the model catalog,
which is a property of the host's model provider abstraction (multi-
provider in core-agent, Gemini-only in cogo). Forcing pricing into
core-tui would either over-fit it to one host's catalog format or
require a generic catalog API that today has only one consumer.

---

## D8. Memory file loading (AGENTS.md / CLAUDE.md / GEMINI.md)

**Question:** Does core-tui own memory file discovery and parsing?

**Options:**

- (A) Lift the `instruction` package into core-tui.
- (B) **Host loads memory and passes it to core-tui as opaque,
  display-only data** (file name + content per file). `/memory` slash
  command renders the loaded list.

**Recommendation: (B).** Same logic as D7 — memory location
conventions vary (cogo uses one path, an arbitrary future host might
use another). Core-tui only needs to *display* it.

---

## D9. MCP / Skills loading

Same as D8 — display-only in core-tui, host loads.

---

## D10. Session transcript persistence

**Question:** Does core-tui own transcript-on-exit?

**Options:**

- (A) **Yes.** It's a leaf feature (serialize history + usage to JSON),
  the schema is neutral, and both source TUIs already do it. The
  "agents dir" path is the only host-specific bit and is passed in.
- (B) Host-owned via an `OnExit` callback. Pushes boilerplate onto every
  host.

**Recommendation: (A).** Schema-versioned JSON in
`<agentsDir>/sessions/<timestamp>.json` with role strings (so
external tools can read it without depending on core-tui). The agents
dir is passed in via `Options.AgentsDir`; empty disables.

---

## D11. Attach mode (remote agent over HTTP/Unix socket)

**Question:** Core-agent has a substantial `attach/` package + a
`core-agent-tui` binary that connects to a remote agent. Should
core-tui ship an attach client?

**Options:**

- (A) Lift `internal/attachclient` into core-tui as a built-in
  alternative to local agents.
- (B) **Don't.** Attach is a *transport* concern. The
  `attachclient.Agent` already implements the same agent surface a
  local agent does — if it conforms to the core-tui `Agent` interface
  (D4), any host can wire it transparently. Core-tui never knows
  whether the agent on the other end of the iterator is in-process or
  HTTP-over-the-wire.
- (C) Ship a thin "attach to URL" entry point as a convenience while
  keeping the agent interface as the seam.

**Recommendation: (B).** core-tui treats `Agent` as a black box. The
`core-agent` repo already owns its attach client and is free to swap
it in by satisfying the same `Agent` interface. cogo can do the same
in the future if it wants. This keeps core-tui focused.

**Followup decision:** core-tui will *export* a documented `Agent`
interface and assert in the design doc that `core-agent/attachclient`
should satisfy it; the user can then ask core-agent to conform.

---

## D12. Autonomous loops / background subagents / scheduler

**Question:** core-agent has `autonomous.go`, `background.go`,
`scheduler`. These drive the agent without user input. Does core-tui
need to know about them?

**Options:**

- (A) Surface them via dedicated UI (a subagents panel, an autonomous-
  mode indicator).
- (B) **Treat them as agent-internal.** core-tui surfaces them only
  through optional capability interfaces — `SubagentLister` for a
  panel, `StatusReporter` for the header — that hosts can choose to
  implement. core-tui ships a minimal subagents panel (list + status)
  but does not drive scheduling.

**Recommendation: (B).** Driving autonomous loops belongs in the agent
package, not the TUI. The TUI's contribution is read-only awareness so
the operator can see what subagents are doing.

---

## D13. Slash-command extensibility

**Question:** Can hosts register additional slash commands?

**Options:**

- (A) Built-in commands only; hosts fork the TUI to add more.
- (B) **Built-in commands + a `CommandRegistrar` for host-added ones.**
  The host registers `(name, alias, help, handler)`; core-tui surfaces
  them in `/help`, in the palette, and routes input. Handlers receive
  the parsed args + a small `tui.CommandContext` for posting system
  messages back to the chat.

**Recommendation: (B).** Cheap and obviously useful. Cogo and
core-agent diverge on `/pricing`, `/permissions list`, `/interrupt`
today; extensibility lets each host add what it needs without
modifying core-tui.

---

## D14. Headless / non-interactive REPL

**Question:** Does core-tui also offer a headless REPL?

**Options:**

- (A) Yes — port `runner.REPL` into core-tui.
- (B) **No.** Core-tui is interactive-only. Hosts own the headless
  REPL themselves (both source repos already do).

**Recommendation: (B).** Keep the scope tight.

---

## D15. Branding & default placeholders

**Question:** Should the wordmark and empty-state hints be configurable?

**Options:**

- (A) Hard-code "core-tui" branding.
- (B) **Brand is configurable** via `Options.Branding` (wordmark,
  accent color, empty hint, footer hint, default prompt placeholder).
  core-tui ships a neutral default ("core-tui ▌") that hosts override.

**Recommendation: (B).** Cogo will want "go-steer / c[o]go" back;
core-agent will want something similar. A neutral default keeps the
test suite stable.

---

## D16. Markdown style customization

**Question:** Are Glamour styles fixed or configurable?

**Options:**

- (A) Fixed styles.
- (B) **Light/dark detection (the current behavior) plus a `MarkdownStyle`
  override** in `Options` for hosts that want their own ANSI style
  JSON.

**Recommendation: (B).** Defaults match current behavior; the override
is a single field hosts can ignore.

---

## D17. Configuration ownership

**Question:** Does core-tui read `.agents/config.json`?

**Options:**

- (A) Yes — core-tui owns config.
- (B) **No — host loads config and passes a `Config` struct** (or a
  smaller `tui.Config` that holds only what the TUI needs: model name,
  scope info for @-files, mouse-on toggle).

**Recommendation: (B).** Same logic as D7/D8 — config format is
host-specific.

---

## D18. Bubble Tea program ownership

**Question:** Who calls `tea.NewProgram` and `Run`?

**Options:**

- (A) Host constructs the program and passes it to core-tui. Lets the
  host inject custom `tea.ProgramOption`s.
- (B) **core-tui constructs and runs the program internally** via
  `tui.Run(ctx, opts) (exitCode, error)`. Hosts pass options; core-tui
  owns lifecycle (alt-screen, mouse capture, raw mode, cleanup).
- (C) Both — `Run(opts)` shorthand plus `New(opts)` returning a
  `*Model` for hosts that want full control.

**Recommendation: (C).** `Run` covers 95% of cases; `New` is the
escape hatch. Mirrors how `http.ListenAndServe` vs `http.Server` works.

---

## D19. Tool-call rendering

**Question:** How are tool invocations summarized in the chat?

**Options:**

- (A) Generic "called X" with full args dumped.
- (B) **One-line summary with tool-aware extraction:** bash shows the
  command; file_read/file_write shows the path; web_fetch shows the
  URL. Falls back to generic for unknown tools. Hosts can register
  custom summarizers via `Options.ToolSummarizers`.

**Recommendation: (B).** Matches current behavior in both source
TUIs. Custom summarizer registration handles host-added tools.

---

## D20. Resume / replay

**Question:** Does v1 support reopening an old transcript / replaying a
recording / resuming a crashed session?

**Options:**

- (A) Yes — design now.
- (B) **No — out of scope for v1.** Capture as a future-work item.
  core-agent already has eventlog/resume primitives; integration can
  follow once the v1 plug-in interface is stable.

**Recommendation: (B).** Focus v1 on feature parity with the two
source TUIs.

---

## D21. Telemetry / OTEL

**Question:** Does core-tui emit traces?

**Options:**

- (A) Yes, OTEL spans for keystrokes / turn boundaries.
- (B) **No — leave to the host.** Agent-side telemetry covers the
  interesting timeline.

**Recommendation: (B).**

---

## D22. License & module path

**Question:** What license + import path?

**Options:**

- (A) Apache-2.0, `github.com/go-steer/core-tui` — matches both source
  repos.
- (B) MIT, different path.

**Recommendation: (A).**

---

## D23. Test strategy

**Question:** How heavily do we test?

**Options:**

- (A) Light — render snapshots only.
- (B) **Mirror the existing test approach** (~30 `_test.go` files in
  each source TUI): drive `Update()` directly with crafted messages
  and assert on history / view fragments. Add Bubble Tea program-level
  smoke tests for the modal interactions.

**Recommendation: (B).** Hold the bar both source TUIs already meet.

---

## D24. Initial deliverable scope

**Question:** What's in v1 vs deferred?

**Recommendation:**

- **v1 (must):** All TUI features in the inventoried superset
  (slash commands, palette, @-files, model picker, markdown, message
  history, permissions modal & picker, MCP elicit modal, mouse,
  thinking, transcript, branding, prompt history, mouse toggle,
  `/reload`, `/pricing`, `/permissions list`, `/allow`, `/deny`,
  `/interrupt`); `Agent` interface + the core capability interfaces;
  the neutral event type; `tui.Run(ctx, opts)` entry point; light
  Branding config; one cogo-like and one core-agent-like demo wiring
  showing both hosts can satisfy the interface.
- **v2 (deferred):** Resume from transcript / eventlog; replay a
  recording; subagents panel widget; live attach reconnection UX;
  Bubble Tea v2 migration.

---

## D25. Default mouse capture

**Question:** Default mouse-on or mouse-off?

**Options:**

- (A) Off (terminal-native text selection preserved).
- (B) **On (matches core-agent's current default).** Users hold Shift
  to select. `/mouse off` toggles. Host can override via `Options`.

**Recommendation: (B).** Matches the more recent of the two source
TUIs; the Shift-to-select convention is documented in the help text.
