# core-tui Requirements

## 1. Purpose

`core-tui` is a standalone, reusable Bubble Tea TUI for agentic
assistants. It is the union of the TUI features currently embedded in
two sibling projects:

- [`github.com/go-steer/cogo`](https://github.com/go-steer/cogo) —
  Gemini-only conversational coding agent
- [`github.com/go-steer/core-agent`](https://github.com/go-steer/core-agent)
  — multi-provider agent runtime with attach-mode + autonomous loops

Both ship copies of essentially the same TUI under `internal/tui/`.
The duplication is real (the two trees differ by ~3 files and a
handful of slash commands). core-tui consolidates them into one
library, generalizes the agent-side seam so neither host is favored,
and serves as the single TUI both projects depend on going forward.

The non-goals are equally important:

- core-tui does NOT define an agent loop.
- core-tui does NOT decide which LLM provider, tool registry,
  permission gate, MCP transport, skill bundle, memory file
  convention, model catalog, or pricing source the host uses.
- core-tui does NOT do headless I/O.

The integration point between core-tui and a host agent is a small,
documented Go interface set (see `design.md` for the shape).

## 2. Glossary

- **Host** — a Go program that imports core-tui and supplies a
  conforming agent. cogo and core-agent are the two named hosts.
- **Agent** — the host-supplied object that core-tui drives via the
  `tui.Agent` interface. May be in-process (cogo) or a transparent
  HTTP client to a remote agent (core-agent's attach mode).
- **Turn** — one user-prompt-to-completion cycle; the agent's `Run`
  method returns an iterator of events for one turn.
- **Capability** — an optional method-set the agent may implement to
  light up an extra TUI feature (model swap, pricing, reload, etc.).
- **Slash command** — a user-typed line beginning with `/` that the
  TUI handles locally rather than forwarding to the agent.

## 3. Functional Requirements

### 3.1 Core chat loop (must)

- **R-CHAT-1** Accept multi-line user input via a textarea widget;
  Enter submits, Shift-Enter / Ctrl-J inserts newline.
- **R-CHAT-2** Display the conversation in a scrollable viewport with
  role-tagged styling (user, assistant, system, error, tool).
- **R-CHAT-3** While a turn is in flight, disable the input and show
  a spinner with a state-aware verb line in the chat. The verb pool
  is chosen by inferred activity:
  - **Model active** (assistant tokens streaming, no outstanding tool
    call): rotate `Options.ThinkingPhrases` (defaults: "Considering",
    "Drafting", "Reasoning", …) on a 3-sec cadence.
  - **Tool active** (a `ToolCall` event was emitted and no follow-up
    `Text` has resumed yet): rotate `Options.WorkingPhrases` (defaults:
    "Reading file", "Running command", "Searching", …). When the host
    registers a `ToolSummarizer` for the tool name in flight, its
    present-continuous string takes precedence over the rotation.
  The TUI infers which state to render from the event sequence — no
  new `Event` field is required. See [`ui-references.md`](./ui-references.md)
  for the Antigravity `Loading…`/`Working…` and Claude Code
  task-aware-spinner references.
- **R-CHAT-4** Stream partial assistant tokens into the in-progress
  assistant message as they arrive, rendering them through Glamour on
  each update so the user sees formatted markdown while the turn is
  still in flight. On turn completion, cache the final Glamour-rendered
  view alongside the raw text so subsequent re-renders skip the
  Glamour pass.
- **R-CHAT-5** Show one-line tool-call summaries inline with assistant
  prose so the user sees actions interleaved with text. Dedupe by
  function call ID so partial/committed events don't double-render.
- **R-CHAT-6** `Esc` interrupts an in-flight turn via context
  cancellation and emits an "(interrupted)" notice; when no turn is
  running, `Esc` cascades through (modal → help panel → palette →
  no-op) so it never accidentally quits. `Ctrl+C` always quits.
  This split mirrors Claude Code, Crush, and Antigravity — keeps
  "stop this turn" and "exit the app" on distinct keys so users
  never have to think about which Ctrl+C this one is.
- **R-CHAT-7** Auto-scroll the viewport to bottom when new content
  arrives **only if the user was already at the bottom**. Preserve
  scroll position when the user has scrolled up.
- **R-CHAT-8** A bound key (default `Ctrl+E`) suspends the TUI via
  `tea.ExecProcess`, opens the focused code block / system message /
  diff payload in `$EDITOR` (falling back to `vi` when unset), and
  resumes the TUI cleanly on editor exit. When the focused content
  was an editable payload (e.g. a permission-modal diff), the saved
  buffer replaces the original; otherwise the editor session is
  read-only and the buffer is discarded. Degrades to a system
  warning if no editor can be resolved.
- **R-CHAT-10** While a turn is in flight the input stays editable —
  the operator can type the next prompt without waiting. Pressing
  `Enter` during streaming appends the typed text to a per-session
  prompt queue rather than submitting immediately; the input clears
  and is ready for the next one. Each queue entry transitions
  through a four-state lifecycle:
  - **`QueueQueued`** (○) — typed during streaming, waiting for the
    running turn to finish.
  - **`QueueInFlight`** (●) — drained from the queue, currently the
    streaming turn. Rendered in the accent color so the operator can
    track what's running.
  - **`QueueDone`** (✓) — turn finished cleanly. Lingers in the
    panel for `cullTTL` (~2 s) before falling off so the operator
    sees the result.
  - **`QueueFailed`** (✗) — turn errored or was interrupted. Same
    lingering cull TTL as Done; carries the truncated error string
    as a dim tail (`(rate limit exceeded)`).

  The queue panel renders between the in-progress message and the
  input box with a header showing total entry count and pending
  count (`queue (3 entries, 2 pending)`). State glyphs come from the
  tool-state palette (style.md §2) so the panel reads consistently
  with the rest of the TUI. Up to 4 entries render directly; older
  entries collapse into a `… N earlier entries` truncation hint at
  the top.

  On turn completion (clean, error, or interrupted) the next
  `Queued` entry auto-starts as a fresh turn — `markInFlightTerminal`
  flips the current entry's state first, then `maybeDrainQueue` scans
  for the next `Queued` one. `Esc` while streaming interrupts the
  active turn (R-CHAT-6) and the entry flips to `Failed`; the queue
  is not cleared. A `/clearqueue` affordance is a later slice; for
  now the cull TTL is the only way entries leave the panel.
- **R-CHAT-9** The TUI supports two render modes selected by
  `Options.RenderMode`:
  - **`RenderAltScreen`** (default) — full alt-screen takeover. The
    viewport is the only chat surface; the terminal's native
    scrollback is empty during the session. Matches v1 cogo /
    core-agent behavior and every TUI in [`ui-references.md`](./ui-references.md)
    except Antigravity inline mode.
  - **`RenderInline`** — hybrid scrollback. Committed turn blocks are
    emitted into the terminal's native scrollback via `tea.Println`;
    `View()` keeps only the live input row + the in-progress
    assistant message in the active viewport. The user can scroll
    back through prior turns with the terminal's own scrollback
    (mouse wheel, `tmux` copy-mode, `screen -h`, etc.) instead of an
    in-app viewport. On resize the TUI debounces the event, flushes
    any pending output, and resynchronizes. Borrowed from the
    Antigravity CLI; see [`ui-references.md`](./ui-references.md).

### 3.2 Prompt history (must)

- **R-HIST-1** Shell-style ↑/↓ when the textarea is empty recalls
  prior user prompts (per-session, in-memory).

### 3.3 Slash commands (must)

The TUI must ship the following built-in slash commands, with help
listed in `/help`:

| Command | Purpose | Required capability |
|---|---|---|
| `/help`, `/?` | Show command help + keyboard shortcuts | — |
| `/clear` | Clear chat history (in-memory) | — |
| `/quit`, `/exit`, `/q` | Exit | — |
| `/memory` | Display loaded memory files | — (display-only) |
| `/stats` | Display per-turn + session usage totals | — |
| `/mcp` | Display configured MCP servers | — (display-only) |
| `/skills` | Display loaded skill bundles | — (display-only) |
| `/tools` | List tools the agent has registered | `ToolLister` |
| `/model` | Pick a model interactively or `/model <id>` to switch | `ModelSwapper` |
| `/reload` | Re-read `.agents/` from disk and rebuild agent | `Reloader` |
| `/permissions` | Open interactive review of session approvals | `PermissionController` |
| `/permissions list` | Print current allow / deny / bundle config | `PermissionController` |
| `/allow <pattern>` | Add allowlist pattern (live + persisted) | `PermissionController` |
| `/allow bundle:<name>` | Enable a permission bundle (live + persisted) | `PermissionController` |
| `/deny <pattern>` | Add denylist pattern (live + persisted) | `PermissionController` |
| `/pricing refresh` | Force-refresh upstream pricing catalog | `PricingController` |
| `/pricing set <model> <in/M> <out/M>` | Manual per-model rate override | `PricingController` |
| `/interrupt`, `/int` | Cancel in-flight turn | `Interruptible` |
| `/mouse [on|off]` | Toggle mouse capture | — |

- **R-CMD-1** Commands whose capability is missing must respond with a
  one-line "not available in this host" message rather than failing
  silently.
- **R-CMD-2** Aliases must round-trip through `/help`.
- **R-CMD-3** Hosts may register additional slash commands via
  `Options.Commands`; host commands appear in `/help` and the palette
  under a separate section header.
- **R-CMD-4** Agents may advertise their own slash commands via a
  `SlashProvider` capability. The TUI queries the agent at startup
  (and after `/reload`) for the command list, merges them into `/help`
  and the palette under an agent-scoped section header, and dispatches
  invocations back to the agent via the same capability. Agent
  commands must not collide with built-in names; on collision the
  built-in wins and a system warning is logged.
- **R-CMD-5** `SlashProvider.InvokeSlash` returns a `SlashResult`
  whose `ModalAnswer *SideAnswer` field, when non-nil, renders as a
  **transient Glamour-formatted modal overlay** (question + answer,
  or error state) that the operator dismisses with `Esc`, `Enter`,
  or `Space`. The answer is **not** persisted to chat history — used
  by `/btw`-style side-question commands where the answer should
  display once and disappear. Modal-answer composition uses the
  same border + footer compositor as the elicit / model-picker
  modals (style.md §6) so the visual language stays uniform. When
  `SystemMessage` is also set, it renders as a chat row after the
  modal is dismissed.

### 3.4 Command palette (must)

- **R-PAL-1** Typing `/` opens a slash-command palette showing
  matching commands with hint text; ↑/↓ to navigate; Tab to complete
  without submit; Enter to insert (still requires Enter to submit
  unless the command was disambiguated to one).
- **R-PAL-2** Typing `@` opens a project-file palette restricted to
  the configured path scope (see §3.6).
- **R-PAL-3** Palette filters case-insensitively; prefix matches
  ranked above substring matches; directories above files; max 8
  rows visible at once.
- **R-PAL-4** Palette respects a documented exclude list (`.git`,
  `node_modules`, `vendor`, `dist`, `build`, `.next`, `.cache`,
  `target`, `.venv`, `__pycache__`, `.idea`, `.vscode`, `.terraform`,
  `.agents/sessions`, `.agents/logs`) and caps results at 200 entries.

### 3.5 `@file` expansion (must)

- **R-AT-1** `@path/to/file` tokens in submitted input are read and
  their contents inlined into the prompt before it's handed to the
  agent.
- **R-AT-2** `@`-tokens that resolve outside the configured path scope
  emit a system warning but still inline.
- **R-AT-3** The expanded prompt (after `@` substitution) is what
  gets sent to the agent; the unexpanded form is what's saved to the
  prompt-history recall.
- **R-AT-4** Hosts may register additional mention sources beyond
  files via `Options.MentionProviders` (e.g. symbols from a code
  index, git refs, web URLs, terminal command outputs, lint
  problems). Each provider supplies a `Prefix` (the trigger after
  `@`, e.g. `sym:`, `git:`, `url:`), a `Lookup(query)` returning
  ranked matches with display + insert + expand callbacks, and an
  optional `SectionHeader` for grouping in the palette. The TUI
  merges entries from every registered provider into the `@` palette
  under provider-scoped section headers (mirrors the `SlashProvider`
  pattern in R-CMD-4). The built-in file provider always runs first;
  hosts cannot disable it. Borrowed from the Antigravity CLI's
  multi-modal `@`-typeahead; see [`ui-references.md`](./ui-references.md).

### 3.6 Path scope (must)

- **R-SCOPE-1** The host supplies a `PathScope` (a list of roots)
  through `Options`. The TUI uses this for:
  - filtering `@file` results;
  - warning when the user inlines a file outside scope.
- **R-SCOPE-2** Path scope is display+enforcement metadata only —
  actual file system permissions are the agent/host's concern.

### 3.7 Permissions UX (must)

- **R-PERM-1** When the host's permission gate invokes the
  TUI-supplied `PermissionPrompter`, the TUI must render a blocking
  modal showing: tool name, the originating sub-agent name when
  present, and the **full payload** the agent is asking permission
  to execute. "Full payload" means:
  - **File edits** — the full diff (red/green hunks) of what will be
    written, not just the target path.
  - **Shell commands** — the verbatim command line about to run, with
    the shell that will run it identified.
  - **Network calls** — the full URL + method + body summary.
  - **Other tools** — the structured tool args (JSON or a key-value
    list).
  Hosts populate the payload via `PermissionRequest.Detail` (the
  rendered text) and `PermissionRequest.DetailKind` (the styling hint
  — `diff` / `shell` / `http` / `args` / `plain`). The TUI picks the
  appropriate Glamour language tag from `DetailKind` so syntax
  colors line up. Both Crush and Claude Code converged on this; see
  [`ui-references.md`](./ui-references.md).
- **R-PERM-2** The modal supports six decisions: `y` allow-once,
  `n`/`esc` deny, `s` allow-session, `v` allow-session-verb (suppress
  if no verb is extractable), `t` allow-session-tool, `a`
  allow-always (persisted).
- **R-PERM-3** Allow-always invokes a host callback to persist; if
  the host didn't wire one, fall back to allow-session and log a
  system message.
- **R-PERM-4** `/permissions` opens a non-blocking review picker
  populated from the session approval log (sourced from a
  `PermissionController` capability). Toggleable recommendations;
  Space to toggle, Enter to persist, Esc to cancel.
- **R-PERM-5** `/allow`, `/allow bundle:<name>`, `/deny` apply
  changes to the live gate **and** persist in one operation —
  `/reload` must not be required for the new rule to take effect.
- **R-PERM-6** The TUI exposes a **permission-mode indicator** in
  the status surface (header or sidebar — see R-USE-2) with four
  states: `default` (every tool call asks), `acceptEdits` (file-edit
  tools auto-allow; everything else still asks), `plan` (no tool
  calls execute; the agent is restricted to planning + read-only
  tools), and `bypassPermissions` (every tool call auto-allows —
  destructive mode, the chip renders with a warning style).
  `Shift+Tab` cycles through the four states. When the host doesn't
  wire `Options.PermissionMode` the chip is hidden and `Shift+Tab`
  has no effect.
- **R-PERM-7** Mode changes invoke `Options.PermissionMode.Set(mode)`
  so the host can apply the change to its gate. If
  `Options.PermissionMode.Persist(mode)` is non-nil it is also called
  so the host can write the choice to a settings file. Initial mode
  is read from `Options.PermissionMode.Initial`. Borrowed from
  Claude Code; see [`ui-references.md`](./ui-references.md).

### 3.8 Model picker (must)

- **R-MOD-1** `/model` opens a list of model IDs returned by the
  `ModelSwapper.AvailableModels()` method. ↑/↓ + Enter to switch.
- **R-MOD-2** `/model <id>` switches without opening the picker.
- **R-MOD-3** A successful switch is persisted via
  `Options.PersistModelChoice` if non-nil.
- **R-MOD-4** Switch errors are non-fatal: the prior model stays
  active, a system error message is rendered, and input re-enables.

### 3.9 MCP elicitation modal (must)

- **R-ELIC-1** When an MCP server requests user input via
  `Elicitor.Elicit`, render a modal. Two modes:
  - **Form mode** for primitive-property JSON schemas: render one
    field per property; supported types are `string`, `enum`, `number`,
    `integer`, `boolean`.
  - **URL mode** when the request is a URL action: `o` opens in
    browser, `a`/Enter accepts, `n` declines, `Esc` cancels.
- **R-ELIC-2** Form fields support Tab/Shift-Tab navigation,
  Space/arrows for enums and booleans, Enter to submit with
  validation, Esc to decline.
- **R-ELIC-3** Schemas with nested objects or unsupported types are
  declined automatically with a "schema unsupported" system message.

### 3.10 Usage tracking & display (must)

- **R-USE-1** Per-turn (input tokens, output tokens, cost) and
  session totals must be visible in `/stats`.
- **R-USE-2** A persistent status surface displays the current model,
  the current permission mode (per R-PERM-6), session totals (input
  tokens, output tokens, cost), and context-window utilization
  rendered as **`used / limit`** (e.g. `19.3K / 200K`) — absolute
  numbers are more legible than a bare `%` and reveal the model's
  context size at a glance. Layout is configurable via
  `Options.StatusLayout` with two presets:
  - **`StatusHeader`** (default) — a single status line above the
    chat. Minimal terminal-real-estate cost; matches the v1 source
    TUIs, Antigravity, and Claude Code.
  - **`StatusSidebar`** — a fixed-width right-hand column carrying
    the status plus any host-supplied auxiliary blocks (modified
    files, LSPs, MCPs, sub-agents). Matches Crush. See
    [`ui-references.md` (Crush §Layout)](./ui-references.md#charmbraceletcrush).

  `Ctrl+B` toggles between layouts at runtime. When the host wires
  `Options.PersistStatusLayout`, the TUI invokes it on every toggle
  so the choice can survive restarts (the host reads the persisted
  value back into `Options.StatusLayout` on next launch). Without
  the callback the toggle stays session-local.
- **R-USE-3** Pricing values come from `Options.UsageTracker`; the
  TUI does not own pricing tables.

### 3.11 Markdown rendering (must)

- **R-MD-1** Final assistant messages are rendered via Glamour with
  custom heading styles (bold H2–H6 with color) and code-fence
  borders.
- **R-MD-2** Light/dark terminal background is sourced from Bubble Tea
  v2's `tea.BackgroundColorMsg` (delivered during program startup;
  Bubble Tea v2 owns terminal I/O so we no longer pre-query stdin) and
  cached for the rest of the session. If the terminal later reports a
  changed background, the cache updates and subsequent renders pick up
  the new value.
- **R-MD-3** Streaming partial text is rendered through Glamour on
  every update so the user sees formatted markdown as the turn
  unfolds. The renderer must tolerate half-formed input (e.g. an
  unclosed code fence): if a Glamour pass fails, fall back to the
  raw text for that frame rather than dropping the chunk.
- **R-MD-4** Hosts can override the Glamour style via
  `Options.MarkdownStyle`.

### 3.12 Mouse support (must)

- **R-MOUSE-1** Mouse-wheel scrolling of the viewport works when
  capture is enabled.
- **R-MOUSE-2** Default is ON; `Options.MouseDefault` overrides;
  `/mouse [on|off]` toggles at runtime; help text mentions Shift-to-
  select.
- **R-MOUSE-3** When mouse capture is enabled, the TUI surfaces an
  auto-expiring overlay hint at the bottom of the viewport reading
  `Hold Shift to select text` for the first few seconds of the
  session (and after each `/mouse on` toggle). The hint fades on a
  short timer (~5s) so users discover the modifier without permanent
  chrome. Hint text and timeout are overridable via
  `Options.MouseHint` + `Options.MouseHintTTL`. Borrowed from the
  Antigravity CLI; see [`ui-references.md`](./ui-references.md).

### 3.13 Branding (must)

- **R-BRAND-1** Header wordmark, accent color, secondary color, cursor
  color, empty-state hint, footer hint, and textarea placeholder are
  overridable via `Options.Branding`. Defaults are neutral
  ("core-tui") and rooted in the house visual style — color palette,
  glyph vocabulary, spacing rhythm, typography rules, border policy,
  and modal composition are all specified in [`style.md`](./style.md)
  and are NOT overridable. Hosts that need a different modal
  aesthetic build their own modal rather than reshaping the
  defaults.

### 3.14 Transcript persistence (must)

- **R-TR-1** On clean exit, if `Options.AgentsDir` is non-empty, write
  a transcript to `<AgentsDir>/sessions/<RFC3339>.json` atomically
  (temp + rename).
- **R-TR-2** Transcript schema is versioned (v1), contains: started_at,
  model name, messages `[{role, text}]` (role lowercased), and usage
  totals.
- **R-TR-3** Transcript save failures are non-fatal and reported to
  stderr after the alt-screen is torn down.

### 3.15 Status bar / footer (should)

- **R-FOOT-1** Footer shows a hint (e.g. "Enter to submit · /help")
  while idle and a spinner-styled "thinking…" indicator while
  streaming.

### 3.16 Sub-agent awareness (should)

- **R-SUB-1** If the agent implements `SubagentLister`, expose a
  `/subagents` slash command listing names + statuses + last reports.
  No driving / scheduling — read-only awareness.

### 3.17 Reload (should)

- **R-RELOAD-1** `/reload` invokes the host's `Reloader` capability;
  on success, the new agent / memory / MCP / skills replace the live
  ones atomically and a system message confirms.
- **R-RELOAD-2** Reload failures leave the prior agent intact.

### 3.18 Pricing controls (should)

- **R-PRICE-1** `/pricing refresh` and `/pricing set` call
  `PricingController` methods that return human-readable summary lines
  for the chat.

- **R-CHAT-11** Operator-typed-during-streaming prompts route by
  `Options.MidTurnInjectionMode`:
  - **`QueueForNext`** (default) — the entry buffers as a `Queued`
    queue row per R-CHAT-10 and drains on the next turn-end.
  - **`InjectIntoCurrent`** — the entry is fed into the running
    turn's context via the agent's `InjectableAgent.Inject` method.
    The queue row renders immediately as `Done` with a dim
    `(injected)` suffix so the operator sees what was injected;
    `cullTTL` drops it ~2 s later. When the agent doesn't satisfy
    `InjectableAgent`, this mode silently degrades to
    `QueueForNext` (no runtime error — type-assertion check).
  Hosts with an inbox-style runtime (e.g. core-agent's
  `agent.Inject` + `DrainInbox`) opt into `InjectIntoCurrent` to
  preserve the mid-turn-context UX. Hosts without an inbox keep
  the default and the queue stays buffer-only.

### 3.19 Agent-driven prompts (should)

- **R-PROMPT-1** When the host wires the TUI-supplied `UserPrompter`
  into its agent, the agent may call `AskUser` mid-turn to elicit a
  structured multiple-choice answer from the operator. The TUI
  renders a blocking modal listing the choices (label + optional
  dim description per row); ↑↓ to navigate, Enter to confirm, Esc to
  cancel. On confirm, the agent receives the selected choice's
  `Value`; on cancel, the agent receives a cancellation sentinel and
  decides whether to retry or abort the turn. Distinct from MCP
  elicitation (R-ELIC-1) which is server-initiated and form-shaped —
  this is the **agent itself** asking a discrete question of the
  user. Borrowed from the Antigravity CLI's `ask_question` tool; see
  [`ui-references.md`](./ui-references.md).

### 3.20 System clipboard (should)

- **R-CLIP-1** A bound key (default `Ctrl+Y`) copies the focused
  content — a rendered code block, a tool-call payload, a system
  message, or the in-flight assistant reply — to the system
  clipboard via OSC 52 (Operating System Command sequence 52). OSC 52
  works across iTerm2, GNOME Terminal, kitty, alacritty, Windows
  Terminal, plus `tmux`/`screen` with the standard `set-clipboard
  on`/`alternate-screen on` settings, and over SSH without local
  clipboard tooling. When the terminal rejects the escape (rare on
  modern terminals) the TUI emits a system message naming the
  fallback (`pbcopy`/`xclip`/`wl-copy`) it tried if any were
  resolvable, or instructions for the user otherwise. No host
  configuration required; works out of the box.

## 4. Non-functional Requirements

- **N-LANG** Go ≥ 1.23 (for `iter.Seq2`). No cgo.
- **N-DEPS** Direct dependencies limited to the Charm v2 set:
  `charm.land/bubbletea/v2`, `charm.land/bubbles/v2`,
  `charm.land/lipgloss/v2`, `charm.land/glamour/v2`, and
  `charm.land/huh/v2` (used for the form-style modals — see
  [decisions.md D26](./decisions.md#d26-form--picker-widget-primitives)).
  `muesli/reflow` is no longer a direct dependency; Lip Gloss v2's
  wrapping primitives cover its role. No transitive coupling to
  Google ADK, MCP SDK, or any agent framework. (Hosts may pull those
  in.)
- **N-PERF** TUI must remain responsive on a 200-message history;
  re-render budget < 16 ms per keystroke on a baseline laptop. Long
  histories must not allocate the entire snapshot on each keystroke.
- **N-LICENSE** Apache-2.0.
- **N-TEST** Mirror existing test density — direct `Update()` table
  tests plus headless `tea.Program` smoke tests for modal
  interactions. Target ≥ 70% statement coverage in `package tui`.
- **N-DOC** Every exported type and function has a doc comment.
- **N-A11Y** Screen-reader friendliness is not a goal of v1; document
  this limit in the README.
- **N-PORTABLE** Must work on Linux + macOS terminals (iTerm,
  Terminal.app, kitty, alacritty, GNOME terminal, tmux, screen).
  Windows is best-effort (Bubble Tea supports it; we don't test it
  in CI).

## 5. Integration requirements

- **I-IFACE** The agent plug-in interface is documented as the
  primary stable surface of the library. See `design.md` §3.
- **I-COGO** A wiring example must show cogo's existing `internal/agent.Agent`
  satisfying the interface with a < 50-line adapter and a
  `cmd/cogo-tui` example.
- **I-CORE-AGENT** Same as above for core-agent, plus an example
  showing the `attachclient` flavor (remote agent over HTTP)
  satisfying the same interface.
- **I-MIGRATE** A `MIGRATION.md` (deliverable with v1) describes how
  cogo and core-agent each drop their `internal/tui/` in favor of
  core-tui.

## 6. Out of scope (v1)

- Resume / replay of prior sessions (eventlog playback).
- Driving autonomous loops or scheduling sub-agents from the TUI.
- Built-in attach client (relies on host-supplied agent — see
  decision D11).
- Headless / non-interactive REPL mode.
- OTEL traces from TUI code.
- Windows-specific tooling.

## 7. Acceptance criteria

A user-visible smoke checklist for v1:

1. `go test ./...` passes.
2. The bundled `examples/local/` binary starts, accepts input,
   streams a response, handles `/help`, `/quit`.
3. The bundled `examples/permissions/` binary triggers the permission
   modal on a fake tool call and round-trips a decision.
4. `/model`, `/reload`, `/pricing refresh` all surface "not
   available" cleanly when their capabilities aren't wired.
5. A cogo branch and a core-agent branch each successfully replace
   `internal/tui` with core-tui imports and pass their existing test
   suites (smoke; full migration is downstream work).
