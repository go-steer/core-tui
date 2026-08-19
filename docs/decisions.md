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

**Context:** Charm shipped the v2 line in early 2026 — Bubble Tea v2.0,
Lip Gloss v2, Bubbles v2.0, Glamour v2.0, plus the related Huh v2 form
library. Import paths moved to the vanity domain (`charm.land/<pkg>/v2`).
The release is stable, not a preview.

**Options:**

- (A) v1 (`github.com/charmbracelet/bubbletea`). Both source TUIs use
  it today; the port from each host's `internal/tui` to core-tui is
  mechanical. v2 becomes a follow-up migration later.
- (B) **v2 (`charm.land/bubbletea/v2` + matching v2 of bubbles, lipgloss,
  glamour).** Lift from the source TUIs but adapt to v2 idioms on the
  way in: the new Cursed Renderer (orders-of-magnitude lower bandwidth
  for Wish-style remote rendering, useful for attach-mode TUIs), Mode
  2026 synchronized output, progressive keyboard enhancements (key
  releases, super/hyper modifiers), I/O ownership consolidated in
  Bubble Tea (Lip Gloss is now a pure library), built-in colorprofile
  downsampling, native progress / cursor / progress-bar primitives,
  hyperlink support in Glamour.

**Recommendation: (B) v2.** The extraction is already a rewrite of
sorts — we're refactoring the seam between TUI and agent at the same
time — so paying the v2 cost now is cheaper than porting to v1 and
migrating again later. Concrete consequences captured in the rest of
this doc:

- New import paths everywhere (`charm.land/bubbletea/v2`,
  `charm.land/bubbles/v2`, `charm.land/lipgloss/v2`,
  `charm.land/glamour/v2`).
- Light/dark detection no longer needs the "query before Bubble Tea
  takes stdin" hack — v2 owns I/O and delivers
  `tea.BackgroundColorMsg`. See [R-MD-2 in requirements.md](./requirements.md#311-markdown-rendering-must).
- Lip Gloss v2 dropped `AdaptiveColor`; we either use the `compat`
  shim or pick colors explicitly off the background message. We
  default to the latter for clarity.
- Bubbles v2 swapped exported `Width`/`Height` fields on viewport,
  textinput, table, help, progress, filepicker for getter/setter
  methods. The lifted code from the source TUIs needs a mechanical
  pass to match.
- `muesli/reflow` drops out of the direct dep list — Lip Gloss v2's
  wrapping primitives cover what we used it for. If a corner case
  surfaces we can re-add it surgically.

**Tradeoff acknowledged:** The lifted code from cogo/core-agent is v1;
we'll write a one-time adaptation pass instead of a pure lift. The
upside is that we never owe a "migrate to v2" PR after v1.0 ships, and
the attach-mode TUI in particular benefits from the Cursed Renderer's
bandwidth profile.

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
  recording; subagents panel widget; live attach reconnection UX.
  (Bubble Tea v2 is in scope for v1 per [D2](#d2-bubble-tea-major-version);
  no separate migration is planned.)

---

## D25. Default mouse capture

**Question:** Default mouse-on or mouse-off?

**Options:**

- (A) Off (terminal-native text selection preserved).
- (B) **On (matches core-agent's current default).** Users hold Shift
  to select. `/mouse off` toggles. Host can override via `Options`.

**Recommendation: (B).** Matches the more recent of the two source
TUIs; the Shift-to-select convention is documented in the help text.

---

## D26. Form / picker widget primitives

**Question:** Hand-roll the modal widgets (permission modal, model
picker, permissions review picker, MCP elicitation form, future
agent-driven prompts) or build them on top of `charm.land/huh/v2`?

**Context:** v1 of both source TUIs hand-rolled every modal because
huh v1 wasn't a natural fit (Bubble Tea program ownership conflicts,
limited validation hooks). huh v2 (released alongside Bubble Tea v2
in March 2026) is purpose-built to embed inside a host Bubble Tea
program and exposes `Input`, `Select`, `MultiSelect`, `Confirm`, and
`Note` as composable fields with built-in validation, theming, and
focus management.

**Options:**

- (A) Hand-rolled. Direct port from the source TUIs. We own every
  keymap, layout decision, and validation rule.
- (B) **huh v2 (`charm.land/huh/v2`).** Use it as the implementation
  primitive for every modal that's really a form or a picker. We
  still own the *contract* (`Elicitor`, `PermissionPrompter`,
  `ModelSwapper`); the form fields underneath become huh `Field`s.
  Theming flows through Lip Gloss v2 so brand colors (`Options.Branding`)
  apply uniformly.

**Recommendation: (B) huh v2.** Concrete mappings:

| TUI surface | huh primitive |
|---|---|
| Permission modal (R-PERM-1/2) | `huh.NewSelect` with the six decisions; description text carries tool / detail / sub-agent name |
| `/permissions` review picker (R-PERM-4) | `huh.NewMultiSelect` with toggleable recommendations |
| `/model` picker (R-MOD-1) | `huh.NewSelect` over `ModelSwapper.AvailableModels()` |
| MCP elicit form (R-ELIC-1 form mode) | `huh.NewGroup` of `Input` / `Select` / `Confirm` per JSON-schema property |
| MCP elicit URL action (R-ELIC-1 URL mode) | `huh.NewConfirm` with custom "open / accept / decline" affordances |
| Future agent-driven prompts (e.g. an agent capability that asks the user a question mid-turn) | same `huh.Field` set, dispatched through a new `UserPrompter` capability |

**Tradeoff acknowledged:** huh v2 is one more direct dependency,
adding ~one transitive subtree to the import graph (already pulls in
Bubble Tea v2 / Lip Gloss v2 / Bubbles v2, which we depend on
anyway). We give up some pixel-perfect control over modal layout in
exchange for not maintaining hand-rolled focus / Tab / validation
plumbing in every modal. Hand-rolled modals stay possible for
surfaces that genuinely don't fit a form (the slash-command palette
and the file-`@`-picker, which are autocomplete UIs, not forms — they
keep their bespoke implementations).

---

## D27. Picker filtering: reuse the palette's ranker, don't add a fuzzy matcher

**Question:** The model / session / theme pickers had no
type-to-filter at all ([#117](https://github.com/go-steer/core-tui/issues/117)).
What ranks the matches — a real fuzzy matcher, or the classifier the
slash palette already uses?

**Options:**

- (A) `sahilm/fuzzy`. True subsequence matching with per-rune match
  positions, so `mgo` finds `main.go` and the highlight can pick out
  scattered runes. One more direct dependency.
- (B) **Lift `palette.filtered`'s ranker into a shared helper.** Four
  case-folded tiers — exact basename, basename prefix, whole path
  segment, substring anywhere — tiebroken by shorter name. Already
  written, already tested, already the ordering operators see when
  they type `/`.

**Recommendation: (B), lift it.** The repo has eight direct
dependencies and N-DEPS exists to keep that number honest; forty
model IDs and a hundred session names do not justify a ninth. Reuse
also means the palette and the pickers rank the same way, which is
the kind of consistency an operator notices without being able to
name it.

**Tradeoff acknowledged:** the tiers are contiguous-substring tests,
so `mgo` does not find `main.go`, and a match highlight is a single
span rather than a scatter of runes. The issue's "highlight the
matched runes" wording is fuzzy-subsequence language and was amended
to match. Whether to adopt real fuzzy matching — for the palette and
the pickers together, so they cannot drift — is a follow-up
evaluation, not something to half-implement on one surface.

---

## D28. Do not adopt a cell-buffer compositor

**Question:** Should the frame be composed into an addressable cell
buffer — a canvas that renderers draw into by coordinate, with a `Hit`
method for routing clicks — instead of the string joins plus clip pass
the library uses today?

**Context:** this was **H2** of the architecture spike
([`architecture-spike-results.md`](./architecture-spike-results.md)),
whose kill criteria were fixed in writing before any challenger code
existed, and which carried an explicit clause that **H2 may not be
reported as a performance win under any outcome**. Three
interchangeable backends were built behind one interface and one
golden suite was run against all three.

**Options:**

- (A) **`strjoin+clip` plus a ~50-line ANSI-aware overlay** — what the
  library already has in `clipFrame` and `compositeModal`.
- (B) A cell-buffer compositor: a canvas, coordinate addressing, and
  `Compositor.Hit`.

**Recommendation: (A). H2 is killed, on its own terms, on every
clause.**

| clause | what the measurement said |
|---|---|
| the cheap control passes 100% of the overflow goldens (5 widths × 3 heights × {plain, modal}) | it does — `strjoin+clip` scores 15/15 and 15/15, the same as the canvas, where bare `strjoin` scores 0/15 and 9/15 |
| a floating modal over live content is deliverable without a canvas | it is — clip + overlay leaves **16** live rows visible behind the modal against the canvas's 14. `lipgloss.Place` discards the body, which is why the control needs an overlay of its own |
| only `Compositor.Hit` can route a click into modal-local coordinates | false. The layout already computes every rectangle, and a containment test over that struct agrees exactly: same layer, same local coordinates, same z-order for a click outside the modal |
| canvas frame cost under 8 ms at 400 turns | met, and beside the point — at 100×40 with a modal the compositor is **1.27× slower and allocates 7.0× the bytes** of the control it was supposed to beat |

Then the finding nobody hypothesised, which settles this independently
of cost: **the canvas backend drops combining marks.** A combining mark
on a narrow base does not round-trip — NFD (`e` + U+0301) goes 54 runes
in and 51 out; a keycap sequence (base + U+FE0F + U+20E3) goes 47 in
and 43 out. ZWJ families, flags, CJK, precomposed U+00E9 and skin-tone
modifiers are all fine. macOS hands NFD out of the filesystem, so this
is the path an accented **filename** takes into a diff header or a file
picker. The design had predicted a width *disagreement* between
lipgloss's grapheme-cluster width and Bubble Tea's wcwidth; what is
actually there is content loss.

**Tradeoff acknowledged:** keeping `strjoin+clip` keeps the clip pass,
and a clip pass is a safety net that also hides the fall — an
oversized panel is silently legal to it, and every frame-invariant test
scores perfectly on a frame that has lost a whole panel. That is not a
reason to buy a canvas; it is a reason the panel-survival assertion
([#158](https://github.com/go-steer/core-tui/issues/158)) has to sit
beside the clip pass, which it now does. See D29.

---

## D29. What the rendering rewrite actually targets

**Question:** the spike's hypotheses were arranged around the idea that
the steady frame is the O(N) problem. Is there a rewrite at all, what
does it touch, and in what order?

**Context:** [`architecture-spike-results.md`](./architecture-spike-results.md),
six hypotheses with pre-registered kill criteria. **H6 was the negative
control** — the pre-registered chance to cancel the list and compositor
work entirely — and it was evaluated first, because firing it would
have saved a quarter of the engineering. It did not fire.

| | hypothesis | verdict | the number that decided it |
|---|---|---|---|
| **H6** | the O(N) refresh is not user-visible | **FAILS** — the rewrite case survives | p99 keystroke latency under stream: 84 ms at 400 turns, 154 ms at 1000, against a 25 ms kill gate |
| **H1** | item-addressed lazy list | **passes** | renders/frame flat at 7 from 10 to 4000 items; warm frame 18 µs / 42 KB against gates of 500 µs / 1 MB |
| **H2** | cell-buffer compositor | **killed** — see D28 | the cheap control matches it on every correctness clause at 0.79× the time and 0.14× the bytes |
| **H3** | interactive resize | **passes**, and **kills the baseline** | drag p95: baseline 62 / 239 / 143 ms against challenger 6.8 / 6.3 / 6.5 ms, gate 16 ms |
| **H4** | 20 fps spinner | **passes** | exactly 1 item render per tick at every size; 4.02% CPU against a 5% gate |
| **H5** | typed-`Action` dialogs | **passes**, including the partial-failure clause | dialog tests compile in an external package importing only the dialog package; the grace period touches 1 file |

**Recommendation: do the rewrite, but not the one the hypotheses were
arranged around, and not in the order they were numbered.**

**The target is the refresh and resize paths, not `View()`.** `View()`
is already flat at ~3.5 ms at every transcript size in *both* arms,
because it renders a pre-sliced viewport. All of the growth is in
`Update` — the linear reassembly. **A rewrite aimed at rendering would
move none of the numbers in the results document.** This is the single
most load-bearing sentence in the spike and the easiest one to lose.

The order, with where each step now stands:

- **Step 0 — hygiene, no architecture. Already landed; nothing to do.**
  The spike's three cheapest findings were each re-verified against
  `main` on 2026-08-18 and all three are already fixed, by work that
  shipped in v0.21.0 after the snapshot the spike measured.
  - **F1, stop using a word-wrapper to pad already-measured rows.** A
    width-setting lipgloss `Style` is a word-wrapper, not a padder:
    handed a row you have already measured it re-measures it, hunts for
    break points, allocates on the way, and can return *more rows than
    it was given*. Replaced with measure-truncate-pad in all three
    places it occurred —
    [#157](https://github.com/go-steer/core-tui/issues/157) (`fitRow`,
    one modal row at a time),
    [#160](https://github.com/go-steer/core-tui/issues/160)
    (`chatBlock`, the whole visible transcript) and
    [#203](https://github.com/go-steer/core-tui/issues/203)
    (`stackColumn`, the left column). The width-setting `Style`
    survives in two places on purpose: as `chatBlock`'s whole-block
    fallback for rows with tabs or open styling, and as the modal
    frame's own wrap, whose row count `modalRowCount` measures through
    the identical wrap so the two cannot disagree.
  - **F2, a clip pass hides panel loss and the tests cannot see it.**
    The assertion landed as
    [#158](https://github.com/go-steer/core-tui/issues/158):
    `assertPanelsSurvive` checks that each region's content is actually
    present in the composed frame, walked over the same grid as the fit
    invariant but as a separate test, so a failure names which
    invariant broke.
  - **F3, tabs make cell-width arithmetic wrong, silently.** A tab is
    one byte, one cell to `ansi.StringWidth`, and `contentTabWidth`
    columns to lipgloss at draw time.
    [#216](https://github.com/go-steer/core-tui/issues/216) fixed the
    pad half with `renderedWidth`, which prices the expansion, and
    [#217](https://github.com/go-steer/core-tui/issues/217) fixed the
    wrap half with `expandTabs` (`tui/view.go`), which performs it
    before the measurement is taken.
  - H6's secondary observation — an idle frame allocating 1.29 MB
    across 50,297 allocations, flagged in the spike as worth its own
    ticket — is the same story:
    [#161](https://github.com/go-steer/core-tui/issues/161) and #157
    took roughly 10× of it and #160 took the rest, leaving a static
    repaint at 1,026 allocations. No ticket is needed.
- **Step 1 — H5, typed-`Action` dialogs.**
  [#164](https://github.com/go-steer/core-tui/issues/164). Independent
  of the rendering work, mechanical, and it unblocks the picker
  backlog. Seal the interface: a bare `any` result defeats
  exhaustiveness at every call site, so a `switch` over it silently
  stops covering a variant added later. Keep the keystroke grace period
  in one file.
- **Step 2 — H1 and H3 together, as one work item.**
  [#247](https://github.com/go-steer/core-tui/issues/247). They are not
  separable: H3's win *is* the width-keyed per-item cache, which is
  H1's list. Sequencing them apart would build the drag mode on top of
  the thing it depends on. **Both halves have landed**: H1's list as
  [#161](https://github.com/go-steer/core-tui/issues/161), H3's drag
  behaviour as #247, which stops re-wrapping the visible rows on every
  event of a drag and does it once, on the leading event and again
  40 ms after the pointer stops.
  - **F4 — "the width contract belongs to the producer, so truncate on
    the cache-miss path" — is closed as already-done, and its
    prescription is wrong for this codebase.** The contract is
    enforced, but at the *draw* site (`chatCutLine`, `tui/chatlist.go`)
    rather than on the way into the render cache, and it is sited there
    because cutting on the cache-miss path is exactly what
    [#154](https://github.com/go-steer/core-tui/issues/154) had to
    undo: a cached row already cut to the window has no columns left to
    pan to. Drawing is strictly stronger — every line the frame
    contains passes through it, including the live tail and the fold
    summary, which the cache-side version had to be applied to
    separately — and it costs one cut per drawn line per frame, which
    the window bounds like everything else on that path. Recorded
    because the spike's phrasing reads as an instruction, and following
    it would re-break panning.
- **Step 3 — H4 falls out for free** once items are individually
  invalidatable. [#248](https://github.com/go-steer/core-tui/issues/248),
  **landed**. Nothing beyond a prerendered frame table, which
  `tui/spinner.go` already had, and pausing off-screen animation:
  `spinnerFrameCadence` is 50 ms, and a tick that lands while the live
  tail is scrolled out of the window advances nothing and paints
  nothing, while leaving the chain armed so the next tick after the
  operator scrolls back resumes it. "Zero renders off screen" and
  "resumes correctly" are asserted separately, because they fail in
  opposite directions.
  - **The 3000 ms verb hold stays, and the step's instruction to delete
    it is declined.** It reads the hold as a leftover of the single
    counter [#162](https://github.com/go-steer/core-tui/issues/162)
    split, but the shared counter was the bug and 3 s was never what
    caused it: a phrase has to sit still long enough to be read,
    R-CHAT-3 asks for this period by name, and R-CHAT-3a pins the
    elapsed readout's floor to it. Deleted at 20 fps the pool would
    rotate sixty times faster than it can be read — the same defect
    #162 fixed, pointed the other way. `spinnerFramesPerVerb` is
    derived from the two constants rather than written down, so the
    phrase period survived the cadence change without being touched.

**H3's fourth clause is now answered, and it does not demote H1.** The
spike never built the suppression-only arm — the Glamour pass
suppressed *without* the lazy list — so the criterion that would have
demoted H1 was never run on its own terms. #247 built it and measured
both sides against a real `tea.Program`, with events enqueued from
outside the loop and every frame charged with the oldest event not yet
on screen (`tui/dragprobe_test.go`):

| turns | baseline p95 | leading-edge suppression p95 |
|---|---|---|
| 0 | 3.4 ms | 3.2 ms |
| 10 | 17.1 ms | 7.5 ms |
| 100 | 15.4 ms | 6.6 ms |
| 400 | 15.1 ms | 6.9 ms |
| 1000 | 16.5 ms | 7.8 ms |

The reading is that neither mechanism is sufficient alone and both are
cheap. The baseline column already has the lazy list under it, which is
why it is 15 ms rather than the spike's 143 ms — the list is what makes
the per-event cost flat in transcript length, and suppression is what
takes the last ~10 ms of Glamour off the event, because a 40-row window
is still two assistant turns and a Glamour render of one is ~5 ms.
There was nothing left to *bound*; the only thing left was to not do
the work. The price is convergence after the last event, ~8 ms to
~22 ms, paid once per drag after the pointer has already stopped.

**Tradeoff acknowledged:** the spike never exercised the mouse,
selection, search or copy paths, never grew a corpus during a run (so
cache eviction was never designed), wrote every probe to `io.Discard`,
and ran on one Linux box — the ratios are the trustworthy part, not the
absolutes.
