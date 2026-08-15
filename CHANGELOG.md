# Changelog

All notable changes to `core-tui` are recorded here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Stability promise

The public API of `core-tui` is the exported surface of `github.com/go-steer/core-tui/tui`, and within it the plug-in surface [`docs/design.md`](./docs/design.md) §3 describes — as shipped today:

- the `Agent` interface and the `Event` type it streams,
- the optional capability interfaces a host may implement (`ModelSwapper`, `Reloader`, `PermissionController`, `PricingController`, `ToolLister`, `SubagentLister`, `SubagentEventReader`, `SessionSwitcher`, `RemoteInterrupter`, `StatusReporter`, `SlashProvider`, `LiveAgent`, `InjectableAgent`, …),
- the `Options` struct's field names, and
- the TUI-implemented `PermissionPrompter` / `Elicitor` pair.

Adding a field to `Options` is non-breaking by Go-module rules — the documented usage is a struct literal with explicit field names — so hosts should keep writing it that way.

Everything else exported from `tui` (render helpers, `Overlay` and the `Dialog` stack, `History`, `Styles`, `Theme` internals) is **not** covered by that promise pre-1.0, even though it is exported. Reducing that gap is [the v1.0 milestone's](https://github.com/go-steer/core-tui/milestone/1) exported-surface audit.

`tui/testagent` is a test fixture. It moves with the library and promises nothing.

Pre-1.0, breaking changes are possible at any minor version (`v0.X`). When we make one it is called out in this file under **Changed** or **Removed**, with the upgrade action spelled out. Patch versions (`v0.X.Y`) are bug fixes only.

The wire protocol in [`docs/sse-event-stream-protocol.md`](./docs/sse-event-stream-protocol.md) versions independently of the library; its own version is noted in the release that changes it.

---

## [Unreleased]

### Fixed

- **Auto-follow survives a resize mid-stream** ([#93](https://github.com/go-steer/core-tui/issues/93)). Following the live tail was inferred from `viewport.AtBottom()` at repaint time, and the resize handler applied the new height *before* that sample — shrinking the terminal by a row (or typing enough to wrap the textarea onto a second line) made a pinned viewport report "not at bottom", so the re-pin was skipped and the rest of the turn streamed away below the visible region with no indication. Following is now tracked as explicit state on the model: scrolling up releases it, scrolling back to the bottom re-arms it, operator-initiated jumps (submit, slash output, session switch, `ctrl+l`) set it outright, and geometry changes leave it alone.
- **The elicitation form accepts non-ASCII input** ([#91](https://github.com/go-steer/core-tui/issues/91)). Two byte-vs-rune defects made the form ASCII-only: the append guard measured `len(stroke)` in bytes, so every printable character with a multi-byte encoding (`é`, `ü`, `日`, `😀`) was silently dropped — typing `café` yielded `caf` — and backspace sliced one *byte* off the value, leaving invalid UTF-8 in both the rendered frame and the `ElicitResult` handed to the host. Both sites are rune-aware now, and the append guard additionally rejects unprintable runes.
- **Truncated diffs no longer advertise a keybinding that doesn't exist** ([#94](https://github.com/go-steer/core-tui/issues/94)). The marker under a clipped diff read `… +N more lines · ctrl+o to expand (todo)`. `ctrl+o` is bound nowhere in the package, so the one key the transcript named was the one key that did nothing — and the note-to-self shipped to operators. The marker is now just `… +N more lines`. Row-level diff expansion needs a message cursor first; when it lands, the affordance comes back with a key behind it.
- **The scrollbar thumb no longer vanishes at the bottom of long content** ([#92](https://github.com/go-steer/core-tui/issues/92)). `Scrollbar` computed one more thumb position than it drew rows, so at maximum scroll the thumb landed one row past the end of the track: clipped short for a multi-row thumb, and gone entirely for the one-row thumb any content much longer than the viewport produces. The thumb now rests flush on the final row at maximum offset.

---

## [0.19.0] — 2026-08-14

A bug-fix release with one small piece of new plug-in surface: modal windows scroll now.

### Fixed

- **Every modal scrolls** ([#75](https://github.com/go-steer/core-tui/pull/75)). A modal body taller than the terminal was clipped, and the mouse wheel never reached a modal at all — `tea.MouseWheelMsg` fell through `Update` to the chat viewport *behind* the overlay, so the wheel scrolled something the operator couldn't see. Every modal surface now scrolls with `↑`/`↓`, `PgUp`/`PgDn`, `Home`/`End` and the wheel: the `/btw` side answer, the permission overlay, the elicitation form, the tool-call and subagent detail overlays, and the theme / model / session pickers. A body that overflows grows a scrollbar column and an `↑↓ scroll` footer hint; one that fits shows neither. Four deliberate refinements: the **inline** permission layout keeps handing the wheel to the chat (that *is* the surface showing the prompt — only the centered `PermissionOverlay` layout captures it); **pickers move one row per wheel tick**, not the three-row wheel step, because a nudge that jumps three models past the one you wanted is worse than no wheel at all; the elicitation form **follows focus only when focus moves**, so scrolling away to read something else isn't yanked back on the next repaint; and the subagent log keeps its follow-the-tail pin, with wheel-up releasing it exactly as `↑` does and `End` re-pinning. No bare `j`/`k` bindings anywhere — those belong to the field being typed into.
- Two latent bugs fell out of the same work. `PgDn` had **never** worked in the tool-call or subagent overlays — bubbletea renders `KeyPgDown` as `"pgdown"`, not the `"pgdn"` they matched on. And `/btw` rendered at roughly double height, because it wrapped its markdown to the ~100-column chat viewport and then rendered that into a 72-column modal frame, re-wrapping every line.

### Added

- **`ScrollDialog`** — an opt-in extension to the `Dialog` surface for dialogs whose body scrolls by lines rather than by cursor rows (`ScrollBy(delta int, m *Model)`). Dialogs that don't implement it get one synthesized cursor step per wheel tick instead.
- **`Overlay.HandleWheel(delta int, m *Model)`** — routes a wheel gesture to the front-most dialog.

Both are additive; existing `Dialog` implementations are untouched.

### Security

- Toolchain floor raised to **go1.26.6** ([#74](https://github.com/go-steer/core-tui/pull/74)) for two standard-library advisories reachable from code we call: [GO-2026-6218](https://pkg.go.dev/vuln/GO-2026-6218) (`net/url`, via glamour's link resolution) and [GO-2026-6088](https://pkg.go.dev/vuln/GO-2026-6088) (`encoding/xml`, via chroma's lexer init). Hosts on an older 1.26.x fetch 1.26.6 via `GOTOOLCHAIN=auto` on their next build.

### Docs

- `docs/requirements.md` gains **R-MOUSE-4** (the wheel belongs to the front-most modal, with the inline-permission exception and the one-step picker rule) and a new §3.22, **R-SCROLL-1..4**.

## [0.18.0] — 2026-08-13

Two features: operators can now see what a subagent is *doing*, and dialogs can collect typed input.

### Added

- **Subagent turn drill-down** ([#70](https://github.com/go-steer/core-tui/issues/70), [#71](https://github.com/go-steer/core-tui/issues/71)). New optional capability `SubagentEventReader` — a cursored, paged read of one subagent's turn log — with two surfaces over it. The **`/subagents <name>` overlay** shows the untruncated `LastReport` (the list still clips it at 60 columns) above a scrollable log of that subagent's turns, live-tailed once a second while open, pinned to the newest turn until you scroll up (`G` re-pins); turn lines are transcript-shaped (prose, `› bash · kubectl get pods`, `  ↳ 3 pods running`). The **inline live tail** grows a block of the newest turns under a *sync* subagent's in-flight tool row, collapsing to one line of counts plus `/subagents <name>` when the result lands — previously a two-minute sync subagent rendered as a spinner and nothing else, because subagent turns are deliberately kept off the parent's event stream. Name resolution understands the declared-name → instance case: `cluster` finds `cluster-1`, `cluster-probe` is left alone (only a `-<digits>` suffix is an instance counter), and an ambiguous query asks rather than guessing; a name that resolves to nothing reports the names that *would* have worked instead of painting a convincing empty log. Both surfaces poll off the render path with a bounded context and a generation guard, so a slow or wedged host can't block `View()` or paint a retired session's turns into a new one. Server side is [go-steer/core-agent#722](https://github.com/go-steer/core-agent/pull/722).
- **Text-input `Dialog` primitive + session-picker action rows** ([#56](https://github.com/go-steer/core-tui/issues/56)). `NewTextInputDialog(TextInputConfig)` is a reusable `Dialog` over `bubbles/v2` textinput with title, prompt, placeholder, char limit, synchronous `Validate` (errors render inline and keep the modal open), and a `Submit` hook returning a `DialogAction`. A new opt-in `KeyMsgDialog` extension (`HandleKeyMsg(tea.KeyPressMsg, *Model)`) exists because the normalized-stroke `HandleKey` path is lossy for a real editing widget; `Overlay` prefers the extension and falls back, so existing dialogs are untouched. `tea.PasteMsg` now routes to the front dialog — a paste into an open modal used to land in the chat textarea behind it. `SessionInfo.Input *SessionInput` turns a picker row into an action row: selecting it (or naming its ID via `/switch <id>`) opens the text input, and `Submit` returns a `SwitchTarget` that feeds the existing switch lifecycle — no new capability interface, no magic IDs. `examples/local` demonstrates the flow with an "attach to endpoint" row.

### Fixed

- `esc` now routes through `Overlay.HandleKeyMsg` before popping the stack, which makes the theme picker's restore-on-cancel branch reachable again ([#56](https://github.com/go-steer/core-tui/issues/56)).
- `/subagents` no longer clips a subagent's report with nowhere to read the rest ([#70](https://github.com/go-steer/core-tui/issues/70)).

### Docs

- `docs/design.md` §3.3 documents `SubagentEventReader` and its paging / not-found contract; `docs/requirements.md` gains R-SUB-2 and R-SUB-3.

## [0.17.0] — 2026-08-11

Hang-proof TUI event loop. Attach-mode `core-agent-tui` could wedge after sitting idle — no typing, dead Ctrl+C, and a `kill -QUIT` left the terminal garbled. Root cause: bubbletea runs Update and View on a single event-loop goroutine, so anything that blocks there freezes the whole TUI, and SIGINT/SIGTERM only arrive as messages the frozen loop can't read.

### Changed

- ⚠️ **`StatusReporter` / `UsageTracker` implementations must now be safe for concurrent, non-blocking reads.** The TUI pulls them off its event loop.
- **Non-blocking `View()`** ([#69](https://github.com/go-steer/core-tui/pull/69)). The status-header helpers (`displayModelName`, `displayProvider`, `usageSummaryOneLine`/`Stacked`) called the host's `StatusReporter`/`UsageTracker` on every paint, so a slow or wedged host froze the loop. They now read a `hostSnapshot` cache refreshed **off** the event loop via a self-perpetuating, generation-guarded cycle (kicked in `Init`, re-armed on a tick, restarted in `applySwitchTarget`). Push-mode overlays (`currentModel`/`pushedProvider`) still win where fresher, and `AutoProviderTheme` re-resolves when the cached provider changes. Precedence is otherwise unchanged; the only new behavior is a brief placeholder in the pre-first-refresh window — correct when the host is exactly the slow case this fixes.

### Added

- **SIGQUIT (`Ctrl-\`) wedge-recovery hatch.** A dedicated signal goroutine, independent of the event loop, dumps every goroutine's stack, restores the terminal (termios + ANSI resets for alt-screen, cursor, bracketed paste, mouse, SGR), and exits `128+SIGQUIT`. This replaces the Go runtime's default SIGQUIT handler, which dumps and then exits **without** restoring the TTY. No-op when stdin isn't a terminal; Windows gets a stub via build tags. Net effect: `Ctrl-\` is a reliable "unwedge and hand my shell back" key.

### Security

- `golang.org/x/text` v0.24.0 → v0.39.0 ([GO-2026-5970](https://pkg.go.dev/vuln/GO-2026-5970)).

### Docs

- SSE event-stream protocol **v1.4.0** ([#68](https://github.com/go-steer/core-tui/pull/68)) — `capabilities.{features, slash_commands, agent, caller_id}`, status-update merge semantics, and slash-response conventions.

### Tests

- `TestRenderHelpers_NeverCallHost` uses a hostile host whose every capability method fails the test, proving the render helpers never reach the host (cold and warm). Plus snapshot adopt/re-arm + stale-generation drop, and escape-hatch reset-sequence + not-a-terminal guards.

## [0.16.1] — 2026-07-18

### Fixed

- **Fast attach to long remote sessions** ([#67](https://github.com/go-steer/core-tui/pull/67)). Attaching to a remote session with a long history rendered oldest-to-newest and took many seconds before the tail was visible. The bottleneck was per-event `refreshViewport`: each incoming SSE event walked the whole history, concatenated every rendered message, and called `viewport.SetContent` on the ever-growing buffer — O(N) per event, **O(N²)** over catch-up, even at a 100% `listCache` hit rate. Event-driven refreshes are now coalesced: handlers flip a dirty flag instead of refreshing synchronously, and a single ~1 ms `tea.Tick` fires one `refreshViewport` for every event that landed in the window. State mutation stays synchronous (the `sessionGen` stale-drop guards continue to hold) — only the paint is deferred. Total work over an N-event catch-up drops to **O(N × batch-size)**. Applies to `streamChunkMsg`, `toolCallMsg`, `toolResultMsg`, `usageMsg`, `statusUpdateMsg`, `usageUpdateMsg`, `inboxStateMsg`, `turnSummaryMsg`, `spinnerTickMsg`, `liveStreamStartedMsg`, `liveStreamErrMsg`, and `liveStreamEndedMsg`; user-input paths and dialog handlers keep refreshing immediately.
- `WindowSizeMsg` short-circuits when the dimensions haven't changed (bubbletea can emit initial + terminal-negotiated sizes back-to-back), and skips `rerenderHistoryMarkdown` on height-only resizes since Glamour wrapping is width-pinned.

The per-turn full-text Glamour render on commit remains a smaller constant (~10 ms × M turns) and was left for a later release.

## [0.16.0] — 2026-07-18

### Changed

- The `LiveAgent` + `InjectableAgent` banner no longer overpromises operator agency ([#66](https://github.com/go-steer/core-tui/pull/66)). Was `"Live session — your messages drive the agent; events stream as they happen."`; now `"Attached to live session — events stream below; type to send a message."` Field-observed on the 2026-07-18 demo drive: an operator attached to a k8s-triage daemon saw "your messages drive the agent" while a k8s-event-watcher pushed incident injects autonomously and drove most turns. This refines [#50](https://github.com/go-steer/core-tui/issues/50)'s binary observer-vs-live split to acknowledge the hybrid shape — autonomous producer *and* operator injection both active — that dominates real alerting recipes. The pure-observer banner (`LiveAgent` only, no `Inject`) is unchanged; that host really is read-only.

## [0.15.0] — 2026-07-17

### Added

- **`RemoteInterrupter`** ([#65](https://github.com/go-steer/core-tui/pull/65)) — optional capability with a single `Interrupt(ctx context.Context) error`, mirroring the `LiveAgent` / `InboxDrainer` / `WakeRequester` pattern, so `/interrupt` works in observer / `LiveAgent` mode against a remote daemon. Closes the gap where the slash reported "no turn in flight" while the daemon's autonomous turn was actively streaming tool calls into the same view. `/interrupt` becomes tiered: local cancel wins when a turn is in flight locally (existing behavior preserved), otherwise the `Agent` is type-asserted to `RemoteInterrupter` and dispatched on a bounded goroutine with a 5 s deadline (placeholder row lands synchronously; the follow-up row lands via `remoteInterruptDoneMsg` — success as `RoleSystem`, failure as `RoleError` with the propagated error), and hosts implementing neither path keep the original fallback.

## [0.14.0] — 2026-07-17

### Added

- **Digest-wrap savings rendering** ([#64](https://github.com/go-steer/core-tui/pull/64)) — the consumer side of [go-steer/core-agent#290](https://github.com/go-steer/core-agent/pull/290) phase 4. Every wrapped tool row gets an inline chip (`[12k→2.1k tok · struct]`, or `agentic` for the LLM subagent path) placed after the `[2.4s]` latency chip so the two read left-to-right in wall-clock-then-cost order; the same compact form appears in the tool-call detail dialog header. Projected onto the wire via `ToolResult.Savings` + `Message.ToolSavings` for downstream consumers. Deferred: the session-level cumulative `/stats` block, which needs a `SessionDigestSavingsTracker` capability plus adapter wiring.

### Docs

- SSE event-stream protocol **v1.3.0** — §2.7 documents the `savings` object shape end-to-end (types, agentic-only fields, passthrough emission convention, example payloads). Fully backward-compatible: pre-v1.3.0 servers omit the sidecar, pre-v1.3.0 clients ignore it.

## [0.13.0] — 2026-07-17

### Added

- `Options.InitialPrompt` — seed an interactive session with a first prompt, submitted as though the operator had typed it ([#63](https://github.com/go-steer/core-tui/pull/63)).

## [0.12.0] — 2026-07-16

### Added

- **Tool-call detail overlay + verbose mode** ([#61](https://github.com/go-steer/core-tui/issues/61)) — two surfaces for seeing what a tool call actually did without dropping to the SSE stream. **`Ctrl+X`** opens an expand-single overlay on the most-recent tool call; `←`/`→` (or `PgUp`/`PgDn`) walk to older / newer calls, `↑`/`↓` scroll long bodies, `Esc` closes, and the header shows `3/8 · read_file · id abc123` plus a `(pending)` / `✘ failed` badge. It reuses the existing `Overlay`/`Dialog` stack — no new interaction mode, no transcript cursor. **`Options.ToolDetailVerbose`** appends the raw args + response detail block inline under every tool row's compact preview; off by default so the transcript stays readable, and hosts flip it on for CI / debug runs (`examples/local` exposes it as `-verbose-tools`). Both share `renderToolDetail` — pretty-JSON args + response with a per-line byte cap (4 KB) and per-section line cap (400) so a 20k-token YAML response can't blow up the transcript or the modal; the error case suppresses the response section and shows the full error text.
- **Per-tool-call latency** ([#62](https://github.com/go-steer/core-tui/issues/62)) — consumes the `latency_ms` sidecar that [go-steer/core-agent#278](https://github.com/go-steer/core-agent/pull/278) emits on `tool-result` payloads, as an inline `[2.4s]` muted badge on compact tool-result rows and a chip in the `Ctrl+X` overlay header. `formatLatency` scales the unit so the number stays legible (`450ms` sub-second, `2.4s` under 10 s, `2m 5s` above a minute). Zero suppresses badge and chip end-to-end, so pre-v1.2.0 servers render exactly as before.
- New zero-value-safe fields: `Options.ToolDetailVerbose`, `Message.ToolResponseMap`, `Message.ToolError`, `Message.ToolLatencyMs`, `ToolResult.LatencyMs`.

### Changed

- ⚠️ **`History.SetToolResult` gained a required `latencyMs int64` parameter** (3 args → 4). Only affects hosts that call `SetToolResult` directly; the built-in `applyToolResult` path is updated internally.

### Docs

- SSE event-stream protocol **v1.2.0** — new §2.7 `tool-result` formally documents the pre-existing event type and specifies the optional `latency_ms` sidecar. The changelog entry records **why the sidecar rides the response map rather than `CustomMetadata`**: ADK's `tool.Run` has no write access to `session.Event.CustomMetadata`, since the tool-result event is constructed after `Run` returns, so the response payload is the only sidecar channel that transports the value without per-adapter plumbing. Future sidecars should ride the same channel for the same reason.

## [0.11.0] — 2026-07-16

### Added

- **Inline tool display.** Tool rows now render previews of what the agent's tools did to the filesystem. **Edit tools** (`apply_patch`, `patch`, `edit_file`, `replace`, `str_replace`) get a colored unified diff with a tiered-background gutter and line-number margins, a per-line byte cap (`…` on pathological minified lines), Chroma syntax highlighting on the `+`/`-` bodies, and eager `⎿  +N -M` totals anchored to the tree glyph as soon as the diff is known. **Read tools** (`read_file`, `read_many_files`, `grep`, `glob`) get a muted one-line scope summary — `L10-L42 · go` (or `full · go` / `L5+`), `7 files · a.go, b.go, c.go, +4 more`, `pattern: "TODO" · path: lib/`. Design in `docs/inline-tool-display-design.md`. Rendering-only: no adapter-facing API change.
- **`PermanentStreamError`** — a new public interface (`error` + `PermanentStreamErr() bool`) letting adapters wrap their own typed errors for zero-heuristic classification on the live-stream path.

### Fixed

- **Live-stream quality pass** ([#59](https://github.com/go-steer/core-tui/pull/59)), three operator-reported issues. [#50](https://github.com/go-steer/core-tui/issues/50) — hosts satisfying both `LiveAgent` and `InjectableAgent` (concretely: core-agent's remote TUI) no longer see the misleading `Attached as observer — agent runs autonomously` row; they get a banner that's accurate for hosts where typing actually feeds the running stream. [#51](https://github.com/go-steer/core-tui/issues/51) — `liveStreamErrMsg` now distinguishes retryable errors (network blip, brief 5xx) from permanent ones (HTTP 404 / 401 / 403 — session gone, auth revoked); permanent conditions surface a distinct `session unavailable: … — relaunch to start a fresh session` row, flip `liveDisconnected`, and stop draining, fixing the "loop 6 messages/sec forever after daemon restart" symptom. A substring fallback (`status 404` / `401` / `403`) covers today's core-agent adapter with no code change. [#49](https://github.com/go-steer/core-tui/issues/49) — regression test only; the URL-truncation symptom was fixed in v0.10.x by `8bfafcc` before the issue was filed, and the wrap behavior is now pinned.

## [0.10.2] — 2026-07-15

### Fixed

- **Observer-mode per-turn footer** ([#57](https://github.com/go-steer/core-tui/issues/57), shipped via [#58](https://github.com/go-steer/core-tui/pull/58)). `LiveAgent` sessions never rendered the per-turn `└ ◇ Model · N in · N out · $X · Ns` footer: those fields are stamped only by `finalizeTurn`, which fires only from `turnDoneMsg`, and observer mode has no `turnDoneMsg` — its commit path is `applyStreamChunk` on the final non-partial chunk, which appended a bare `Message`, so the footer render short-circuited to `""`. Three-site back-annotation now converges on a stamped footer under both stream-order-relative-to-turn-complete orderings: the `applyStreamChunk` LiveAgent commit stamps the `current*` fields onto the fresh `Message`; the `turnSummaryMsg` handler back-annotates the tail via the new `History.StampLatestAssistantFooter` (tokens + model + latency); and the `usageUpdateMsg` handler back-annotates with authoritative cost from the new `UsageUpdate.LastTurn`. Guardrails: `StampLatestAssistantFooter` only fills currently-zero fields so it can't clobber `finalizeTurn`'s canonical stamp (chat mode unaffected), it bails cleanly when the tail isn't `RoleAssistant` (an autonomous tool call landing after the assistant text), and it bumps `Message.Version` so the lazy-render cache re-renders the row.

### Docs

- SSE event-stream protocol **v1.1.1** — optional `usage-update.last_turn` object (`tokens_in` / `tokens_in_cached` / `tokens_out` / `cost_usd` / `model`). It lives on `usage-update` rather than `turn-complete` because v1.1.0's "cost is optional on turn-complete" convention exists precisely for servers that compute cost out-of-band; `usage-update` fires *after* pricing has run, so it's the natural home for authoritative per-turn cost. Fully backward-compatible.

## [0.10.1] — 2026-07-14

### Fixed

- `/switch` is discoverable in the slash palette ([#55](https://github.com/go-steer/core-tui/pull/55)). v0.10.0 shipped the built-in with a dispatcher, a `/help` entry, and a `Dialog` picker, but no row in `builtinSlashItems()` — so operators typing `/` and scanning for the new command couldn't find it, and `/switch` was reachable only by typing the name blind. The palette entry now sits alongside `/subagents` and `/theme`, the `/sess` alias is surfaced in the Display column, and a test pins the row's presence. Reported by @mastersingh24 immediately after bumping core-agent to v0.10.0.

## [0.10.0] — 2026-07-14

### Added

- **Mid-session `Agent` switch** ([#48](https://github.com/go-steer/core-tui/issues/48), [#53](https://github.com/go-steer/core-tui/issues/53), shipped via [#54](https://github.com/go-steer/core-tui/pull/54)). `SlashResult.SwitchTo *SwitchTarget` lets any sync / async / preamble slash request a mid-run detach + attach alongside its normal `SystemMessage` / `ModalAnswer` output — one hook in `applySlashResult` covers all three dispatch paths. A new `SessionSwitcher` capability backs the `/switch` built-in (alias `/sess`), which opens a `Dialog` picker at parity with `/model`; `/switch <id>` direct-jumps, and hosts that don't implement the capability fall through so a host-provided `AsyncSlashProvider` `switch` still works. **Straggler protection**: a `sessionGen` counter stamped by `startAgentTurn` / `startLiveStream` / `emitEvent` and checked in every affected `Update` case, so a late `turnDoneMsg` / `streamChunkMsg` from the outgoing session can't leak `(interrupted)` rows or bleed chunks into the incoming session's buffer.

  Lifecycle contract, documented in `SwitchTarget`'s godoc and `docs/requirements.md` R-SWITCH-1..8: server-side sessions are unaffected (a remote daemon observes a dropped reader and keeps ticking per its own reattach policy — detach, not kill); the old `Agent` handle belongs to the host (core-tui drops its reference and does **not** call `Close`/`Detach`, so hosts needing teardown do it inside `SwitchToSession()` before returning the `SwitchTarget`); and the `cancel(ctx)` here releases *local* resources only (SSE socket, in-process model call).

### Security

- goldmark v1.7.8 → v1.7.17 ([GO-2026-5320](https://pkg.go.dev/vuln/GO-2026-5320), XSS in the HTML renderer — the terminal path is unaffected, but the call graph flagged it).

### Changed

- `go` directive 1.25.8 → 1.26.3, to align with core-agent.

## [0.9.1] — 2026-06-09

### Fixed

- **`/stats` renders the per-model breakdown for remote/attach operators** ([#46](https://github.com/go-steer/core-tui/pull/46)). v0.9.0 added the data side of [#38](https://github.com/go-steer/core-tui/issues/38) — the SSE `usage-update` event populates `m.sessionUsage.ByModel` — but `renderStats` only knew how to read the breakdown from the pull-path `SessionByModelTracker` capability, so remote/attach operators on `--agentic-tools` deploys saw the aggregate-only layout while the per-model data sat there unrendered. Precedence: the push path wins when `m.sessionUsage.ByModel` has more than one entry (the daemon's tracker is the source of truth), the pull path is the fallback, and either source with ≤1 entry is skipped since it would just restate `SessionTotals`. Pure render change — no new public API.

## [0.9.0] — 2026-06-09

### Added

- **SSE event-stream subscriber (push-mode consumer)** ([#40](https://github.com/go-steer/core-tui/issues/40), via [#43](https://github.com/go-steer/core-tui/pull/43)). `tui.Event` gains five optional payload fields matching the v1.1.0 wire spec; host adapters that subscribe to a server's SSE stream populate them, and core-tui applies the payload to model state and renders it. New exported types: `StatusUpdate` + the `TurnState*` constants (session-level model / provider / perm-mode / turn-state / context-pct changes), `UsageUpdate` + `UsageByModel` (cumulative totals + per-model breakdown), `InboxEvent` + `InboxState*` constants, `TurnSummary` (per-turn tokens + cost + latency + model, with `CostUSD` optional per spec v1.1.0), and `TurnError` + its kind constants (`config_error`, `auth_error`, `model_not_found`, `rate_limited`, `transient_network`, `unknown`). `Message` gains an optional `TurnError` field driving the structured push-mode error row. JSON tags mirror the spec's snake_case exactly, so adapters can unmarshal raw SSE data blocks straight into these structs. Push wins over the legacy path for `displayProvider()` when non-empty; `currentModel` / `currentUsage` / `currentCost` are last-writer-wins, and the per-turn footer renders identically from either source. Pairs with [core-agent#117](https://github.com/go-steer/core-agent/pull/117) (server-side emission).
- **Structured `turn-error` render contract** — any `RoleError` message with a non-nil `TurnError` renders a block with the kind + code in the error color and the hint + retryable marker in muted (`⚠ model_not_found · NOT_FOUND` / `hint: …` / `↻ retryable`). Legacy `RoleError` rows keep the simple `⚠ <text>` form.
- **Transcript v2 schema** ([#44](https://github.com/go-steer/core-tui/issues/44), [#45](https://github.com/go-steer/core-tui/pull/45)) — every tool row in a saved transcript (`<AgentsDir>/sessions/*.json`) now preserves its structured tool-call data instead of persisting as an empty placeholder. `TranscriptSchemaVersion` goes 1 → 2 and `TranscriptMsg` gains four `omitempty` fields (`tool_name`, `tool_args`, `tool_preview`, `tool_call_id`), populated by `buildTranscript` for `RoleTool` messages and restored by `ApplyTranscript` on `/resume`. Bidirectional back-compat: v1 files load under v2 readers with the tool fields empty (matching what v1 actually persisted), and v2 files load under v1 readers because `json.Unmarshal` drops unknown fields.

### Fixed

- `/tools` descriptions no longer overflow the wrap column.

### Docs

- SSE event-stream protocol **v1.1.0** ([#42](https://github.com/go-steer/core-tui/pull/42)) — `turn-complete.cost_usd` demoted from required to optional (servers that compute cost out-of-band emit it on the following `usage-update`), and legacy sub-types may now be advertised in `capabilities.event_types`.

## [0.8.0] — 2026-06-07

### Added

- **`Notifier` capability** ([#39](https://github.com/go-steer/core-tui/pull/39)) — hosts push framework-initiated chat rows that don't ride the agent event stream (reconnect notices, shutdown warnings, multi-attach signals, version-mismatch errors), instead of faking them through the agent stream and conflating "the agent said X" with "the framework noticed X". Surface: `Options.Notifier *Notifier` (nil preserves current behavior), `NewNotifier()`, `(*Notifier).Notify(text string)` — callable from any goroutine, empty text is a no-op, non-blocking when the buffer is full — plus the new `RoleNotice` role constant and `Styles.NoticeText`. Notices render as a `◇` glyph in the muted `theme.Info` color, **non-italic**, so operators glance-distinguish them from `RoleSystem`'s italic `ℹ`; they interleave with agent stream chunks at arrival time and `/clear` wipes them like everything else. Backpressure is a size-16 buffered channel with drop-and-coalesce: past capacity `Notify` increments a dropped counter and the next successful enqueue appends `(+N dropped)` to the row, so the operator sees coalesced loss rather than silence. `Notify` after TUI exit drops silently via a closed-flag guard, so hosts needn't track TUI lifecycle. `RoleNotice` serializes to `"notice"` in transcripts and round-trips through `roleFromString`.
- **`examples/notifier-smoke/`** — a standalone binary exercising the full `Notifier` contract: a producer goroutine pushing one realistic notice every ~7 s from a 10-item rotation, plus a 25-notice burst every ~50 s to demonstrate the `(+N dropped)` coalescence marker.

### Docs

- **SSE event-stream wire protocol v1.0.0** ([#41](https://github.com/go-steer/core-tui/pull/41)) — `docs/sse-event-stream-protocol.md` ships as a **specification only**, no client subscriber yet, so core-agent's server-side work could target a stable contract while the consumer side was built. Six event types: `capabilities` (handshake), `status-update`, `usage-update` (with per-model breakdown), `inbox` (queued/dequeued), `turn-complete` (per-turn cost), `turn-error` (structured error). SemVer evolution rules: additive is MINOR/PATCH; rename / remove / retype is MAJOR.

### Upgrading

- `RoleNotice` was added to the `Role` enum. Downstream code that switches on `Role` exhaustively should add a case — most renderers already have a default branch.

## [0.7.0] — 2026-06-06

First release with formal release notes.

### Added

- **`/theme` picker with live preview on cursor** — `↑`/`↓` applies the focused theme so you can see the palette without committing, `enter` commits, `esc` restores the original. `/theme <name>` switches directly without opening the picker (unknown names fall back to `default` with a recoverable system message and skip persistence); `/themes` is an alias.
- **Eight new themes** alongside the original four (`default`, `anthropic`, `gemini`, `openai`): `google`, `gke`, `gopher`, `matrix`, `pride`, `cyberpunk`, `vaporwave`, `christmas`. Signatures rather than palette transcriptions — `google`'s wordmark cycles B-R-Y-B-G-R in logo letter order, `gke`'s cycles the icon's clockwise quadrants and its textarea prompt is `⎈` (Unicode HELM SYMBOL), `gopher` fades Gopher Blue → Aqua from the Go brand book, `pride` cycles the flag over calm violet chrome so it reads as a signature rather than an assault.
- Two reusable mechanisms behind those themes, available to any future one: **multicolor wordmarks** via `Theme.WordmarkSequence []color.Color` (nil keeps the existing single-color path) and **themed textarea prompt glyphs** via `Theme.PromptGlyph` (empty keeps the house default `▎ `, exported as `DefaultPromptGlyph`).
- Host surface: `Options.InitialThemeName` (case-insensitive; unknown names fall back to `DefaultTheme`), `Options.PersistThemeChoice func(string) error` (mirrors `PersistModelChoice`), `ThemeChangedMsg{Name string}` for hosts with custom `Update` wrappers, `BuiltinThemes() []BuiltinTheme` for hosts building their own picker, and `ThemeByName(name, dark) Theme`. Minimum integration to get the picker is **zero lines**; persisting across restarts is two.

### Changed

- ⚠️ **`Overlay.HandleKey`'s signature changed** from `(consumed bool)` to `(consumed bool, cmd tea.Cmd)` so dialogs can emit msgs. `Overlay` is exported but is internal modal plumbing; if you call it directly, append a `_` to ignore the new return.
- `Options.Branding.AccentColor` still overrides whichever theme is active — same precedence as before.

## [0.6.9] — 2026-06-01

### Fixed

- The `liveStream*Msg` handlers re-arm the event listener ([#28](https://github.com/go-steer/core-tui/issues/28)) — the third and last of the quiet-window paint bugs.

## [0.6.8] — 2026-06-01

### Fixed

- The render kick extends to `streamChunkMsg` and its sibling chat-content msgs ([#26](https://github.com/go-steer/core-tui/issues/26)).

## [0.6.7] — 2026-06-01

### Fixed

- Kick a paint after listener msgs that land in a quiet window ([#24](https://github.com/go-steer/core-tui/issues/24)) — without a concurrent tick, a msg arriving between spinner frames updated state that nothing repainted.

## [0.6.6] — 2026-05-31

### Added

- **`LiveAgent` capability** ([#22](https://github.com/go-steer/core-tui/issues/22)) — observer-mode hosts stream a daemon's events into the chat without owning the turn.

## [0.6.5] — 2026-05-29

### Fixed

- The toast bypasses its render-time TTL while an async slash is in flight, so a long-running command's indicator doesn't vanish mid-dispatch.

## [0.6.4] — 2026-05-28

### Added

- **`SessionByModelTracker`** ([#18](https://github.com/go-steer/core-tui/issues/18)) — optional capability backing the `/stats` per-model cost breakdown.

## [0.6.3] — 2026-05-28

### Added

- **`AsyncSlashProviderWithPreamble`** ([#16](https://github.com/go-steer/core-tui/issues/16)) — an async slash command can land a chat-visible dispatch row immediately, before its result arrives.

## [0.6.2] — 2026-05-27

### Changed

- The banner drops the cursor block and gains a `Branding.AgentIdentity` segment.

## [0.6.1] — 2026-05-27

### Added

- Async slash commands get an in-flight indicator, `Esc` cancellation, and a refusal when one is already running ([#13](https://github.com/go-steer/core-tui/issues/13)).

## [0.6.0] — 2026-05-27

### Added

- **`AutoContinueFromInbox` injection mode** ([#9](https://github.com/go-steer/core-tui/issues/9)) — for hosts whose runner is opaque and can't accept a mid-turn inject, the TUI queues the text and submits it as the next turn instead.
- Async slash dispatch, a quieter wake toast, and a longer queue TTL.

## [0.5.0] — 2026-05-27

### Added

- `Options.ForceTheme` and the mouse hooks are exported; `/mouse` becomes a real toggle rather than a status report.

## [0.4.1] — 2026-05-27

### Fixed

- Read previews treat zero-valued range bounds as `full` instead of rendering `L0-L0`.

## [0.4.0] — 2026-05-27

### Added

- Eager `⎿  +N -M` diff totals anchored to the tree glyph, so the count appears as soon as the diff is known rather than when the result lands.

## [0.3.1] — 2026-05-26

### Fixed

- `/clear` followed by a bare Enter actually clears the history.

## [0.3.0] — 2026-05-26

### Added

- `ToolResult` events are wired end-to-end through the agent → TUI flow.

## [0.2.0] — 2026-05-26

### Added

- **Inline tool display, phases 1–3** — unified diff previews for `apply_patch` / `edit_file`, read previews with a per-line syntax cache, background-tinted diff lines with a line-number gutter and per-line byte cap, then a tiered gutter background plus a lexer cache.

## [0.1.0] — 2026-05-26

Initial release: `package tui` extracted from the duplicated `internal/tui` trees in [`go-steer/cogo`](https://github.com/go-steer/cogo) and [`go-steer/core-agent`](https://github.com/go-steer/core-agent), generalized so neither host is favored.

### Added

- **The plug-in surface** — the `Agent` interface and `Event` type, the §3.3 optional capability interfaces, `Options`, and the TUI-implemented `PermissionPrompter` / `Elicitor` pair. Capabilities are feature-detected by type assertion, and a missing one degrades to a "not available in this host" message rather than an error.
- **The chat loop** — streaming assistant tokens rendered incrementally through Glamour, inline tool-call rows, the per-turn footer, prompt history, `/clear` with confirmation, transcript save/`/resume`, and the `Esc`-interrupts-turn / `Ctrl+C`-quits split.
- **Prompt queueing during streaming** (R-CHAT-10) with the four-state queue panel, plus `InjectableAgent` + `MidTurnInjectionMode` (R-CHAT-11), `WakeRequester` + its toast banner (R-WAKE-1), `ContentRunner` + the `Content` type (R-CHAT-12), and `SlashResult.ModalAnswer` + the side-answer modal (R-CMD-5).
- **Live `/` and `@` palettes** (R-PAL-1/2), a real model picker and file palette, real `/mcp` `/tools` `/skills`, `/stats` with cost, `/allow` and `/deny`, `Options.PersistModelChoice` (R-MOD-3), the status header/sidebar with a persisted layout choice, the bottom-anchored help panel on `?`, and the 3-tier color context indicator.
- **Performance and terminal-fidelity work** — incremental Glamour streaming, an auto-growing textarea, hanging-indent word wrap that preserves source leading whitespace, and the viewport's `h`/`j`/`k`/`l`/arrow bindings disabled with `xOffset` pinned to 0 so a wide line can't shift the whole chat sideways.
- **The docs that govern all of it** — `docs/requirements.md`, `docs/design.md`, `docs/decisions.md`, `docs/style.md`, `docs/ui-references.md`, and `MIGRATION.md`.

[Unreleased]: https://github.com/go-steer/core-tui/compare/v0.19.0...HEAD
[0.19.0]: https://github.com/go-steer/core-tui/compare/v0.18.0...v0.19.0
[0.18.0]: https://github.com/go-steer/core-tui/compare/v0.17.0...v0.18.0
[0.17.0]: https://github.com/go-steer/core-tui/compare/v0.16.1...v0.17.0
[0.16.1]: https://github.com/go-steer/core-tui/compare/v0.16.0...v0.16.1
[0.16.0]: https://github.com/go-steer/core-tui/compare/v0.15.0...v0.16.0
[0.15.0]: https://github.com/go-steer/core-tui/compare/v0.14.0...v0.15.0
[0.14.0]: https://github.com/go-steer/core-tui/compare/v0.13.0...v0.14.0
[0.13.0]: https://github.com/go-steer/core-tui/compare/v0.12.0...v0.13.0
[0.12.0]: https://github.com/go-steer/core-tui/compare/v0.11.0...v0.12.0
[0.11.0]: https://github.com/go-steer/core-tui/compare/v0.10.2...v0.11.0
[0.10.2]: https://github.com/go-steer/core-tui/compare/v0.10.1...v0.10.2
[0.10.1]: https://github.com/go-steer/core-tui/compare/v0.10.0...v0.10.1
[0.10.0]: https://github.com/go-steer/core-tui/compare/v0.9.1...v0.10.0
[0.9.1]: https://github.com/go-steer/core-tui/compare/v0.9.0...v0.9.1
[0.9.0]: https://github.com/go-steer/core-tui/compare/v0.8.0...v0.9.0
[0.8.0]: https://github.com/go-steer/core-tui/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/go-steer/core-tui/compare/v0.6.9...v0.7.0
[0.6.9]: https://github.com/go-steer/core-tui/compare/v0.6.8...v0.6.9
[0.6.8]: https://github.com/go-steer/core-tui/compare/v0.6.7...v0.6.8
[0.6.7]: https://github.com/go-steer/core-tui/compare/v0.6.6...v0.6.7
[0.6.6]: https://github.com/go-steer/core-tui/compare/v0.6.5...v0.6.6
[0.6.5]: https://github.com/go-steer/core-tui/compare/v0.6.4...v0.6.5
[0.6.4]: https://github.com/go-steer/core-tui/compare/v0.6.3...v0.6.4
[0.6.3]: https://github.com/go-steer/core-tui/compare/v0.6.2...v0.6.3
[0.6.2]: https://github.com/go-steer/core-tui/compare/v0.6.1...v0.6.2
[0.6.1]: https://github.com/go-steer/core-tui/compare/v0.6.0...v0.6.1
[0.6.0]: https://github.com/go-steer/core-tui/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/go-steer/core-tui/compare/v0.4.1...v0.5.0
[0.4.1]: https://github.com/go-steer/core-tui/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/go-steer/core-tui/compare/v0.3.1...v0.4.0
[0.3.1]: https://github.com/go-steer/core-tui/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/go-steer/core-tui/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/go-steer/core-tui/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/go-steer/core-tui/releases/tag/v0.1.0
