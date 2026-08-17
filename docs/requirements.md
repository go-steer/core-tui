# core-tui Requirements

## 1. Purpose

`core-tui` is a standalone, reusable Bubble Tea TUI for agentic
assistants. It is the union of the TUI features currently embedded in
two sibling projects:

- [`github.com/go-steer/cogo`](https://github.com/go-steer/cogo) —
  Gemini-only conversational coding agent
- [`github.com/go-steer/core-agent`](https://github.com/go-steer/core-agent)
  — multi-provider agent runtime with attach-mode + autonomous loops

Both shipped copies of essentially the same TUI under `internal/tui/`.
The duplication was real (the two trees differed by ~3 files and a
handful of slash commands). core-tui consolidates them into one
library and generalizes the agent-side seam so no single host is
favored.

**core-agent is the reference host**: it has completed the migration,
tracks each release, and exercises the full capability surface. cogo
is no longer a gating consumer — its TUI is the historical source of
much of this behavior, and its adapter material in
[`MIGRATION.md`](../MIGRATION.md) §2 is kept as a worked example for
third-party hosts, but nothing in this document is blocked on cogo
adopting core-tui.

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
  conforming agent. core-agent is the reference host; cogo is a
  historical one (see §1).
- **Agent** — the host-supplied object that core-tui drives via the
  `tui.Agent` interface. May be in-process or a transparent HTTP
  client to a remote agent (core-agent's attach mode).
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
- **R-CHAT-3a** The verb line carries an elapsed-time suffix once the
  turn is older than one spinner cadence — `Thinking... 12s`, then
  `1m04s`, then `1h02m` — so an operator can tell a long turn from a
  wedged one. Coarse and fixed-width on purpose: no sub-second
  component, a zero-padded minor unit so the line does not shift a
  column between ticks, and minutes rather than seconds past the
  hour. It repaints on the existing spinner cadence; no separate
  timer is scheduled for it.
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
| `/interrupt`, `/int` | Cancel in-flight turn | none for a locally-driven turn (ctx cancellation); `RemoteInterrupter` for a turn the TUI has no local context for |
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
- **R-PERM-8** The six decision keys are inert for a short grace
  period (~300ms) after the modal appears, and keystrokes that arrive
  inside it are routed to the prompt instead. A permission request
  lands asynchronously, so without the window whatever the operator
  was typing answers a modal they have not seen yet — and two of the
  six decisions widen the agent's authority beyond the current call.
  `esc` is exempt: it denies, which is the fail-safe direction. The
  elicitation modal applies the same window to the keys that dispatch
  a result (see R-ELIC-4).

### 3.8 Model picker (must)

- **R-MOD-1** `/model` opens a list of model IDs returned by the
  `ModelSwapper.AvailableModels()` method. ↑/↓ + Enter to switch;
  type to filter (§3.24).
- **R-MOD-2** `/model <id>` switches without opening the picker.
- **R-MOD-3** A successful switch is persisted via
  `Options.PersistModelChoice` if non-nil.
- **R-MOD-4** Switch errors are non-fatal: the prior model stays
  active, a system error message is rendered, and input re-enables.

### 3.8a Session switch (must) — issues #48 / #53

- **R-SWITCH-1** `/switch` opens a picker of sessions returned by
  `SessionSwitcher.Sessions()`; the currently-attached row is marked
  `(current)`. ↑/↓ + Enter attach; Esc cancels without swap; type to
  filter (§3.24). `/sess` is an alias.
- **R-SWITCH-2** `/switch <id>` attaches directly, no picker.
- **R-SWITCH-3** Any `SlashProvider` / `AsyncSlashProvider` return
  value MAY populate `SlashResult.SwitchTo` to trigger the same
  detach + attach.
- **R-SWITCH-4** Applying a switch: wipe history, reset streaming /
  modal / queue state, cancel LOCAL contexts on turn / async slash /
  live stream (releases sockets / halts in-process model calls), swap
  non-nil `SwitchTarget` fields onto `Options`, re-detect `LiveAgent`
  and spawn a fresh drain if applicable, re-issue every listener.
- **R-SWITCH-5** Server-side session lifecycle is NOT core-tui's
  concern. A remote daemon observes a dropped reader and keeps the
  session running per its own policy — operators can `/switch` back
  and see continued transcript.
- **R-SWITCH-6** The outgoing `Agent` handle is the host's
  responsibility. Core-tui drops its reference and does not call
  `Close` / `Detach` on it. Hosts that need teardown do it inside
  `SwitchToSession()` before returning.
- **R-SWITCH-7** Straggler msgs from the outgoing session (a
  buffered `streamChunkMsg`, a late `turnDoneMsg`) MUST NOT leak
  content or state changes into the incoming session. A session-
  generation counter stamped by the emitter + guarded by Update
  drops them.
- **R-SWITCH-8** Switch errors (`SwitchToSession` returned err,
  `SwitchTarget.Agent == nil`) are non-fatal — a `RoleError` row
  is rendered and the current session stays attached.

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
- **R-ELIC-4** The keys that dispatch a result — URL-mode `a`/Enter
  accept and `n` decline, form-mode Enter submit — observe the same
  grace period as the permission modal (R-PERM-8), for the same
  reason: the form is opened by a server, not by the operator. Field
  editing and navigation stay live throughout; they are visible and
  reversible, and nothing leaves the TUI until a result is
  dispatched.

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
- **R-MD-1a** The Glamour style config is built from the active
  `Theme`, not from the dark/light flag alone: body text takes
  `FgBase`, H1 reverses `OnPrimary` out of a `Primary` fill, H2–H6
  walk a derived `Accent`→`FgMuted` ramp, and code fences are
  highlighted with the theme's own Chroma style
  (`Theme.ChromaStyleName`) — the same style the inline diff and
  tool-preview highlighter uses, so the two surfaces agree.
  Switching themes must repaint the assistant text, not just the
  chrome around it.
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
- **R-MOUSE-4** While a modal is on screen the wheel scrolls *that
  modal*, not the chat behind it. Exception: the inline permission
  layout renders inside the chat viewport, so the wheel keeps
  scrolling the chat — that is the surface showing the prompt. On a
  cursor list (the pickers) one wheel tick moves the selection by one
  row, not by the three-row wheel step.

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
- **R-TR-2** Transcript schema is versioned (v2 as of 2026-06-09;
  v1 still loads). Contains: started_at, model name, messages, and
  usage totals. Each message carries `{role, text}` (role lowercased)
  plus — when `role == "tool"` — optional `tool_name`, `tool_args`,
  `tool_preview`, `tool_call_id` fields preserving the structured
  call/result data the renderer assembles tool rows from. v1 readers
  ignore the v2 tool fields cleanly (unknown JSON fields are
  silently dropped); v2 readers handle v1 tool rows by leaving the
  new fields empty (no data to recover).
- **R-TR-3** Transcript save failures are non-fatal and reported to
  stderr after the alt-screen is torn down.

### 3.15 Status bar / footer (should)

- **R-FOOT-1** Footer shows a hint (e.g. "Enter to submit · /help")
  while idle and a spinner-styled "thinking…" indicator while
  streaming.

### 3.16 Sub-agent awareness (should)

- **R-SUB-1** If the agent implements `SubagentReporter`, expose a
  `/subagents` slash command listing names + statuses + last reports.
  No driving / scheduling — read-only awareness.
- **R-SUB-2** `/subagents <name>` opens a detail overlay: the
  untruncated `LastReport` above a scrollable log of that subagent's
  turns, live-tailed while it stays open. An unresolvable name reports
  the names that would have resolved rather than rendering an empty
  log. Available whenever R-SUB-1 is — one capability serves both, so
  a host cannot offer the roster without the drill-down it points at.
- **R-SUB-3** While a SYNC subagent's tool call is in flight, its
  tool row carries a live preview of the newest turns, so the
  operator sees progress instead of a spinner. The block collapses
  to a one-line summary (turn / tool-call counts, and where to read
  the rest) when the result lands.

### 3.17 Reload (should)

- **R-RELOAD-1** `/reload` invokes the host's `Reloader` capability;
  on success, the new agent / memory / MCP / skills replace the live
  ones atomically and a system message confirms.
- **R-RELOAD-2** Reload failures leave the prior agent intact.

### 3.18 Pricing controls (should)

- **R-PRICE-1** `/pricing refresh` and `/pricing set` call
  `PricingController` methods that return human-readable summary lines
  for the chat.

- **R-CHAT-12** *(withdrawn in v0.21.0, issue #77.)* It reserved an
  optional `ContentRunner` capability for driving turns from a
  structured `[]Content` slice instead of a prompt string. Nothing in
  `package tui` ever asserted it and no host ever implemented it: the
  submit path is `Agent.Run(ctx, prompt)`, and a capability the
  library never calls is a type two hosts would have had to agree on
  privately anyway. A structured-prompt entry path can be specified
  again when something concrete needs one.
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

- **R-PROMPT-1** (⚠️ specified, not shipped as of v0.19.0 — neither
  `UserPrompter` nor `NewUserPrompter()` exists in `package tui`.
  Whether to build it or drop this requirement is part of the
  exported-surface audit, issue #78.)
  When the host wires the TUI-supplied `UserPrompter`
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

### 3.20 Wake signals (should)

- **R-WAKE-1** When the host's agent implements the optional
  `WakeRequester` capability (`WakeRequested() <-chan struct{}`),
  the TUI subscribes to the channel at startup. Each receive
  triggers a transient toast banner rendered between the input box
  and the footer, in the warn color, prefixed with `⚠  `. The
  toast clears after `toastTTL` (~4 s); a fresh wake during the TTL
  window restarts the timer. The interface makes no promise about
  coalescing — rapid back-to-back wakes render multiple toasts in
  sequence. Hosts that close the wake channel cleanly stop the
  subscription without a goroutine leak. Without the capability,
  no toast affordance renders and the banner row collapses to zero
  height.

### 3.21 System clipboard (should)

- **R-CLIP-1** A bound key copies the selected transcript item — its
  source text, not the frame it was drawn into — to the system
  clipboard via OSC 52 (Operating System Command sequence 52). As
  shipped the keys are `y` (the whole item) and `c` (just its fenced
  code blocks) in transcript focus mode. OSC 52 works across iTerm2
  (behind a preference), GNOME Terminal, kitty, alacritty, Windows
  Terminal, plus `tmux`/`screen` with the standard `set-clipboard
  on`/`alternate-screen on` settings, and over SSH without local
  clipboard tooling — which is why it is the default: it reaches the
  clipboard of the machine the operator is sitting at rather than the
  one the process runs on. No host configuration required; works out
  of the box wherever the terminal allows it.
- **R-CLIP-2** The escape has no acknowledgement, so the TUI does not
  claim a result it cannot observe: the copy notice names the
  mechanism (`copied 24 lines · osc52`) whenever OSC 52 is the only
  write, and the help panel says the terminal has to allow clipboard
  writes. Terminals that decline the escape outright — Terminal.app,
  iTerm2 with the preference off, some remote and web terminals — are
  common enough that a silent no-op has to be diagnosable from the UI.
- **R-CLIP-3** `Options.ClipboardWriter` lets a host also write the
  clipboard of the machine the *process* runs on, called in addition
  to the escape rather than instead of it — the two target different
  machines, so neither is a fallback for the other.
  `tui.SystemClipboardWriter()` supplies one out of `os/exec`
  (`pbcopy` / `wl-copy` / `xclip` / `xsel` / `clip.exe`) and returns
  nil — a no-op, indistinguishable from leaving the field unset —
  where there is no clipboard to write to, including a Unix box with
  no display. core-tui takes no clipboard library dependency: as a
  library that would put windowing-system bindings in every host's
  module graph. A host writer's error reaches the operator in the
  notice, and its success is the only thing that lets the notice drop
  the `· osc52` qualifier.

### 3.22 Modal scrolling (must)

- **R-SCROLL-1** Every modal surface whose body can outgrow the
  terminal scrolls: the `/btw` side answer, the permission overlay,
  the elicitation form, the tool-call and subagent detail overlays,
  and the theme / model / session pickers. A modal never renders
  taller than the terminal and lets the terminal clip the overflow.
- **R-SCROLL-2** The scroll vocabulary is `↑`/`↓`, `PgUp`/`PgDn`,
  `Home`/`End`, plus the mouse wheel (R-MOUSE-4). Modals that accept
  typed input bind arrows and page keys only — never bare `j`/`k`,
  which belong to the field being typed into.
- **R-SCROLL-3** A scrollable body that overflows shows a scrollbar
  column on its right edge and a `↑↓ scroll` hint in the modal
  footer; a body that fits shows neither.
- **R-SCROLL-4** Modals with a focused row (the elicitation form)
  keep that row on screen when focus moves, but do not yank the view
  back to it on unrelated repaints. Modals that tail live content
  (the subagent log) stay pinned to the newest line until the user
  scrolls up, and `End` re-pins.

### 3.23 Hardware cursor (must)

- **R-CUR-1** The terminal's real cursor sits on the text surface
  that currently owns input, at the caret position within it, and
  text surfaces do not paint a caret glyph of their own. This is
  what anchors an IME's candidate window, what lets the user's
  configured cursor shape and blink apply, and what assistive
  technology follows.
- **R-CUR-2** When a modal owns input the cursor goes with it — the
  elicitation form's focused field, a text-input dialog, a picker's
  filter row (§3.24). A modal that takes no typed text (a permission
  decision, the `/btw` viewer, a read-only detail overlay) and an
  unfocused input leave the cursor unset, so the terminal hides it
  rather than showing it on an unrelated cell.
- **R-CUR-3** The cursor position is always inside the rendered
  frame. A surface clipped out of the frame yields no cursor at all
  rather than a stale position.
- **R-CUR-4** The caret column is measured in terminal CELLS, not in
  runes or bytes, on every surface — a value containing double-width
  runes places the caret on the glyph it belongs to. Where an
  upstream widget gets this wrong the library corrects it locally.

### 3.24 Picker filtering (must) — issue #117

- **R-FILT-1** The model, session and theme pickers each carry a
  one-line filter input as the first row of their body. Typing
  narrows the list as the operator types; the arrow keys, Enter and
  Esc keep their meanings and everything else is filter text.
- **R-FILT-2** Filtering uses the same ranking as the command
  palette (R-PAL-3): case-insensitive, four tiers — exact basename,
  basename prefix, whole path segment, substring anywhere — tiebroken
  by shorter name. Matching is on contiguous substrings, so the match
  highlight on a row is a single span.
- **R-FILT-3** The filter row shows a `matched/total` count once a
  filter is active. A filter that matches nothing renders a "no
  matches" line and leaves the picker open with the filter row still
  editable; it is not the same state as a host that advertised an
  empty list, which still reports that in the chat and closes.
- **R-FILT-4** The selection stays inside the filtered list at all
  times. Narrowing the list moves the cursor to the best match, and
  Enter commits the highlighted row of the FILTERED list.

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
  interactions. Target ≥ 70% statement coverage in `package tui`,
  enforced by `dev/tools/verify-coverage` in the required `test` job
  rather than left as an aspiration here. The floor is the stated 70%
  and not the current number: a floor pinned to today's coverage is a
  ratchet, and a ratchet makes every later PR that moves a statement
  into an untested branch inherit someone else's backlog. `examples/`
  and `tui/testagent` are excluded from the measurement — the first is
  wiring sketches, the second is a fixture, and averaging either in
  measures the shape of the repo rather than the coverage of the
  library.
- **N-COMPAT** The exported surface of `package tui` is the product —
  hosts consume it as a Go module, so a removed field or a changed
  signature breaks their build at `go get`. `dev/tools/verify-apidiff`
  diffs the module's exported API against the last release tag on every
  PR, reports compatible additions, and fails on incompatible changes
  that `dev/api-breaks.txt` does not acknowledge. Pre-1.0 a break is
  permitted at any minor version but must be written down in that file
  and in the CHANGELOG by the PR that makes it; post-1.0 it means a new
  major version and a `/v2` module path. The check is not in the required
  set on `main`: it depends on a tag being fetchable, and that failure
  mode should not be able to redden unrelated PRs. See "Changing the
  exported API" in CONTRIBUTING.md.
- **N-DOC** Every exported type and function has a doc comment.
- **N-A11Y** Screen-reader friendliness is not a goal of v1; document
  this limit in the README.
- **N-PORTABLE** Must work on Linux + macOS terminals (iTerm,
  Terminal.app, kitty, alacritty, GNOME terminal, tmux, screen).
  Both are built and unit-tested in CI — `test` on ubuntu and
  `test (macos)` on darwin. Neither drives a real terminal
  emulator, so the named emulators above remain a manual-verification
  claim; what CI proves is that the code compiles and its tests pass
  on both kernels. Windows is best-effort (Bubble Tea supports it; we
  don't test it in CI).

## 5. Integration requirements

- **I-IFACE** The agent plug-in interface is documented as the
  primary stable surface of the library. See `design.md` §3.
- **I-CORE-AGENT** A wiring example must show core-agent's agent type
  satisfying the interface with a small adapter, plus an example
  showing the `attachclient` flavor (remote agent over HTTP)
  satisfying the same interface.
- **I-MIGRATE** A `MIGRATION.md` describes how a host drops its
  in-tree `internal/tui/` in favor of core-tui. core-agent's
  migration is the worked case; the cogo material in §2 is retained
  as a second example for third-party hosts, not as a deliverable.

`I-COGO` (a `cmd/cogo-tui` wiring example) was removed once cogo
stopped being a gating consumer — see §1 and `design.md` §8.

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
3. A headless smoke test drives the permission round-trip end to end:
   a scripted tool call trips the gate, the modal renders, and the
   decision unblocks the turn — across the allow, deny, and
   allow-always branches and both permission layouts. (Not met — the
   smoke harness is issue #81, which owns the fixture. This criterion
   originally called for a separate `examples/permissions/` binary;
   that was dropped because `examples/local` already round-trips a
   real prompt through `tui.NewPrompter()` on `ctrl+y`, and a
   machine-checked test is a stronger promise than a binary someone
   has to run and eyeball.)
4. `/model`, `/reload`, `/pricing refresh` all surface "not
   available" cleanly when their capabilities aren't wired.
5. core-agent builds against the current release with its
   `internal/tui` removed and passes its existing test suite. (Met as
   of core-tui v0.18.0 — core-agent's `internal/tui/` is gone and
   `cmd/core-agent-tui` is the shipped surface.)
