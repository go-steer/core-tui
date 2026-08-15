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

### Added

- **A frame-invariant test harness, a golden corpus, and committed render benchmarks** ([#101](https://github.com/go-steer/core-tui/issues/101)). `package tui` carried 202 `strings.Contains` assertions and exactly five tests that called `View()` at all — a combination that proves a token is present *somewhere* in the output and says nothing about where it landed or how wide it is, which is precisely the defect class the layout work ahead is about. Three pieces close the gap. A **frame-invariant grid** drives a real `Model` across widths 40 / 80 / 100 / 120 / 200 × heights 4 / 10 / 24 / 50, in both `StatusHeader` and `StatusSidebar`, with the base chat, a seeded transcript, the permission overlay, the elicitation modal and the help panel open — 200 cells, each asserting `ansi.StringWidth(line) <= width` for every line and a total line count `<= height` — plus a resize-sequence variant that walks one model through seven successive `WindowSizeMsg`s, so cache- and geometry-carried state is exercised too. A **golden corpus** under `tui/testdata/` pins the composed frame at three widths and the seven `Model`-free renderers (`renderToolPreview`, `renderToolPreviewWithResult`, `renderToolDetail`, `renderDiffInline`, `renderCodeInline`, `renderLatencyBadge`, `renderReadPreview`) byte for byte, ANSI escapes included; `go test ./tui -update` regenerates it. Every golden renders through one explicit `Theme` literal owned by the test file and a fixed named chroma style — never `m.styles`, never the background-detected theme — so a later palette change churns zero files, and `.gitattributes` marks `*.golden -diff` so escape-heavy fixtures don't drown a review. **Benchmarks** cover `refreshViewport` (warm and cold cache), a width-changing `WindowSizeMsg` and its height-only control, and `View()` end to end, each swept over 10 / 100 / 400-turn transcripts; the width-change / height-only pair is the measurement [#104](https://github.com/go-steer/core-tui/issues/104) needs, since only a width change invalidates the lazy-render cache.
- **The terminal's real cursor is parked on whichever text surface owns input** ([#105](https://github.com/go-steer/core-tui/issues/105)). `tea.View.Cursor` was set nowhere — `grep -rn 'tea.Cursor' tui/*.go` outside the tests returned zero hits. Every text surface drew its own caret instead, a reverse-video block painted into the frame, and the hardware cursor stayed wherever the last write happened to leave it. Three things break when that happens, none of them visible to a sighted operator typing ASCII: an IME's candidate window follows the *hardware* cursor, so composing CJK or emoji opened it in the wrong place; the operator's configured cursor shape and blink were never consulted, because a painted block has neither; and assistive technology, which tracks the terminal cursor, had nothing to follow. The chat textarea and the text-input dialog now run with `bubbles`' virtual cursor off and report a real position, which `View()` translates into absolute frame coordinates. The translation is per layout — `StatusHeader` stacks a status row above the chat, `StatusSidebar` drops it and puts the input inside a `chatWidth`-wide left column — and per modal, where a centered overlay's origin is derived from its own measured size using the same gap split `lipgloss.Place` performs, never a guess. Modals that own input take the cursor with them: the text-input dialog through the new `CursorDialog` extension, and the elicitation form's focused field, whose caret follows Tab and the form's scroll window and is measured in terminal **cells** so a wide label or a `日` in the value doesn't shift it. Surfaces with no caret to offer leave it nil, so the terminal hides it rather than parking it somewhere arbitrary: a permission prompt answered with `y`/`n`, the `/btw` viewer, the arrow-nav pickers, an elicitation boolean or enum, a blurred input. The position is resolved *after* the frame-clipping post-pass ([#102](https://github.com/go-steer/core-tui/issues/102)) and clamped against it — a row that clipping removed yields no cursor at all rather than a stale one, while a column past the right edge is pulled back to the last cell, because a caret legitimately sits one cell past the final glyph. One visible change comes with it: the painted block on the first character of the input placeholder is gone, and that cell is now where the terminal draws its own caret.
- **`CursorDialog`** — an opt-in extension to the `Dialog` surface for dialogs whose body owns a text-editing widget (`DialogCursor(width int, m *Model) *tea.Cursor`, returning a position relative to the dialog's own top-left cell), plus **`Overlay.Cursor(width int, m *Model)`** which routes to the front-most dialog. Both are additive; a `Dialog` that doesn't implement the extension gets no cursor, which is the right answer for the arrow-nav pickers. It is shaped as an extension rather than a contract method deliberately — folding "where does the cursor go" into `Dialog` itself belongs with the contract work in [#115](https://github.com/go-steer/core-tui/issues/115), and when that lands the change is to move the method and delete one type assertion.
- **The thinking line says how long the turn has been running** ([#111](https://github.com/go-steer/core-tui/issues/111)). It showed a rotating verb and a moving spinner, which together answer "is anything happening" and nothing else — so the single most common question an operator has while waiting, *is this a big turn or is it wedged*, had no answer on screen. The line now carries a muted elapsed suffix: `Thinking... 12s`, then `1m04s`, then `1h02m`. Three deliberate properties in that format, all of them about a string that re-renders under an already-moving spinner. There is **no sub-second component** — `0.4s → 1.2s → 2.7s` is noise on a line whose job is a rough magnitude. The minor unit is **zero-padded**, because `1m9s → 1m10s` shifts the tail of the line a column mid-turn and `1m04s → 1m10s` does not; the width changes only at 10s, 10m and 10h, once each per turn. And the readout **coarsens as it grows**, dropping seconds past the hour where they are pure churn. It stays hidden below one spinner cadence (3s): the spinner tick is the repaint that advances the number, so anything shown sooner is a value the operator cannot watch move, and short turns never flash a `0s` on their way past. No new timer is involved — `spinnerTickMsg` already fires every 3s and already marks the viewport dirty. The origin is stamped at exactly the two sites that take a new `spinnerGen` ([#112](https://github.com/go-steer/core-tui/issues/112)): `submitTurn` for the per-turn `Run` path, and `applyStreamChunk`'s `spinnerActive` false→true flip for a LiveAgent stretch, which has no `submitTurn` to hang off. That second site is what keeps the feature from shipping its own worst bug — an unstamped origin on the LiveAgent path would have measured from the zero time and rendered a fifty-five-year turn — and *stamping* it, rather than merely suppressing the zero value, is also what makes a second stretch measure from its own start instead of the first one's. The field is zeroed wherever the animation stops (`finalizeTurn`, the LiveAgent commit, a session switch), so the invariant is "non-zero iff a spinner animation is live" and no render-path gate is load-bearing for correctness. A backwards clock — NTP step, suspend/resume — clamps to absent rather than to a negative. Tested against an injected clock rather than a golden, for the obvious reason; the golden corpus is byte-for-byte unchanged, since its model is idle and renders no spinner line at all.

### Fixed

- **The help panel is laid out to the terminal in both dimensions** ([#119](https://github.com/go-steer/core-tui/issues/119)). `renderHelpPanel` emitted the same 38 rows at every terminal size: its `width` argument reached the `<= 0` guard and the horizontal rule and nothing else, so each key row was a constant `"    " + key(padded to a hardcoded 24) + description` — 81 cells at the widest, in an 80-column terminal, and twice the terminal in a 40-column one. It also made the chrome budget wrong, because `lipgloss.Height` counts a row that visually wraps as one row. **Width** is now a derivation rather than a constant: the key column comes from the widest key actually present, capped so one 29-cell key row cannot push all 22 descriptions five columns right, and never allowed past half a narrow panel; descriptions **wrap** into the remainder with a hanging indent, and below the point where the aligned form stops being readable the panel switches to a **stacked** one — key on its own row, description full-width underneath. **Height** is pagination, on the panel's own key. `m.chrome.helpCap` is the row ceiling the budget ([#121](https://github.com/go-steer/core-tui/issues/121)) hands the panel, and laying out *to* it beats being elided *by* it, which is what the `… N more rows` marker was doing: `?` now opens the panel, walks it a page at a time, and closes it after the last page, with the title row saying which page is on screen and which key advances it. Paging on the key that opened the panel is what keeps `pgup` / `pgdn` with the chat — the panel's own Navigation section advertises them as chat scroll, and a scroll mechanism would have had to take them away and make it lie again. Pages break on section boundaries wherever they can, so a page usually comes in under the cap and the rows it does not spend go back to the chat viewport; one page always beats two, so a panel that would fit without the blank rows between sections is rendered compactly instead of split. Gated by a width × height grid asserting no rendered row exceeds its column and no page exceeds its cap, a content check that walking the pages still reaches all 22 documented keys, help-open frame goldens at 60 / 100 / 160 columns (the corpus had none — `goldenModel` never set `helpOpen`), and paged-panel cells in both the frame-invariant and the row-budget grids.
- **The input box grows again, and variable-height chrome can no longer push the frame past the terminal** ([#121](https://github.com/go-steer/core-tui/issues/121)). `syncInputHeight()` clamps the textarea to its line count between `textareaMinHeight` and `textareaMaxHeight`, and every one of its call sites follows the grow with a `resize()` — which opened with an unconditional `m.input.SetHeight(textareaMinHeight)`. The grow was computed and thrown away before it could be rendered, so the auto-growing box those doc comments describe ("multi-line paste / typed newlines grow the box visibly") survived **zero frames** and `textareaMaxHeight = 15` was unreachable dead configuration. Two adjacent gaps in the same feature go with it, both invisible while the clobber hid them: the newline arm of `handleKey`'s switch (`shift+enter` / `ctrl+j` / `alt+enter`) returns early and so never reached the reconciliation at the bottom of that function, and a bracketed paste never goes through `handleKey` at all — it falls out of `Update`'s switch to the shared forward-to-the-widgets tail. The two gestures whose entire purpose is to add rows were the two that did not grow the box, until some unrelated later keystroke reconciled the layout for them. The one-line clobber is gone, but on its own it would only have moved the damage: `resize()` had no notion of variable-height chrome yielding rows to fixed elements, so a 15-row box on a short terminal would overflow the frame exactly the way the **help panel** already did — `renderHelpPanel` emits 38 rows at every width ([#119](https://github.com/go-steer/core-tui/issues/119)), which on an 80×24 terminal composed a 48-row frame, and since `clipFrame` keeps the *first* `m.height` rows, pressing `?` clipped away the input box and the footer entirely: the operator lost the box they were typing into and the only row that says how to quit. The **palette** (`maxPaletteRows` + its own chrome) is the third instance. `resize()` now allocates the terminal's rows through an explicit chrome budget (`tui/budget.go`) instead of subtracting-and-flooring: unshrinkable chrome (footer, header, toast) is reserved off the top, then the input box's *minimum*, the chat viewport's `chatMinHeight` floor and one row per open panel, then — in priority order — the input box's *growth*, the palette, the help panel, with everything unspent going to the chat. Splitting the input box across two priorities is what resolves the conflict the issue names: a `textareaMaxHeight`-tall box wins against the palette and the help panel, but **not** against the chat floor, because watching the transcript vanish while typing is worse than typing into a box that scrolls internally past its height — which is what the textarea already does. Elements below the cut shrink rather than overflow: the palette narrows its scrolling window, and the help panel is elided with a `… N more rows` marker until #119 makes its content fit a column. Every term is still a measurement of the string `View()` will render, at the width it will render at, so `clipFrame` is back to being a safety net that does not fire — the only case left is a terminal shorter than the irreducible chrome (a 4-row pane, where the header, the input box and the footer are already taller than the screen), which the budget now reports as a number rather than discovering by accident. Two verification gaps closed with it. `assertBudgetExact` used to **skip** whenever the viewport bottomed out at `chatMinHeight` — which is exactly what the help panel causes, so every cell that would have caught the 48-row frame skipped silently; it now asserts the exhausted case as a checkable size instead. And the row-budget grid had no state in which the textarea was ever taller than three rows, because `resize()` guaranteed it could not be; it sweeps a tall textarea (alone and against the help panel) in both layouts across the full width × height matrix now. Golden-neutral: the frame corpus types nothing into the box, so its `LineCount()` is 1 and every golden is byte-identical. `BenchmarkResizeHeightOnly` and `BenchmarkResizeDrag400` are unmoved.
- **A key that jumps to the tail and resumes following the stream** ([#113](https://github.com/go-steer/core-tui/issues/113)). `ctrl+l` jumps to the top of the transcript and had no counterpart. Once following became explicit state ([#93](https://github.com/go-steer/core-tui/issues/93)), the only way back to a live stream was holding `PgDn` until `AtBottom()` re-armed the flag on its own — so an operator who had read back through a long session paid one keypress per screen to resume watching it, and on a session long enough to be worth reading back through, that is the whole cost of following in the first place. `end` now does it in one: `m.follow = true` plus `viewport.GotoBottom()`, the same pair `refreshAndScroll` uses, but without the `syncInputHeight` / `resize` that helper also runs — a jump to the tail changes no geometry, so it has no business re-resolving the layout. The binding is claimed **only while the input is empty**. bubbles' textarea binds `end` to `LineEnd`, and "end goes to end-of-line" is too strong a convention to shadow in a box the operator is composing in — the more so because `syncInputHeight` grows that box to `textareaMaxHeight` rows, where line-end is the whole point. With nothing typed there is no line to end, so the key is free and the chat takes it; with a prompt in progress it falls through to the textarea. `ctrl+e` reaches end-of-line in either state. `ctrl+d` and `ctrl+u` keep their existing shadows — recovering the viewport's half-page scroll bindings is not worth making the primary quit key scroll the transcript.

### Fixed

- **The help panel no longer advertises two keys that do nothing** ([#113](https://github.com/go-steer/core-tui/issues/113)). `renderHelpPanel`'s Navigation section listed `home / end` as "top / bottom". Neither key was bound anywhere: `grep -n '"home"\|"end"' tui/update.go` returned nothing, and bubbles v2's viewport binds neither — `handleKey`'s own fall-through comment said as much, a few lines below the switch that would have had to claim them. It is the same defect class as the truncated-diff marker naming `ctrl+o` ([#94](https://github.com/go-steer/core-tui/issues/94)): the one surface a new operator opens to learn the keymap was naming keys the package does not handle. `end` is bound now. `home` deliberately is **not** — `ctrl+l` already does exactly what it would, and a second binding for one action would spend the textarea's `LineStart` to buy nothing. So the rows name what exists instead: `ctrl+l` "jump to top (stops following the stream)" and `end` "jump to bottom (resumes following)". `ctrl+l`, the real goto-top key, was not listed at all before. A new test parses the rendered Navigation rows, presses every key they name, and asserts each one moves the viewport — so the section cannot quietly drift back out of sync with the switch that implements it.
- **CI no longer decides required checks by a race** ([#124](https://github.com/go-steer/core-tui/issues/124)). `ci.yml` carried `paths-ignore: ['**/*.md']` and a twin `ci-docs.yml` carried the inverse `paths: ['**/*.md']`, each emitting four **identically named** check runs. Because GitHub path filters match "any changed file", a PR touching both Go and Markdown satisfied both filters and ran both workflows — and since every PR here carries a CHANGELOG entry, that was the normal case rather than a corner case. Which of the two same-named results counted was then decided by whichever finished last, an ordering the old `ci-docs.yml` header admitted to relying on. Re-running the 3-second stub after a red pipeline produced four green required checks on a red commit. `ci.yml` now has no path filters and always reports; a `changes` job classifies the diff once and the four jobs short-circuit their expensive steps when it is docs-only. `ci-docs.yml` is deleted. The fast path also widened from `**/*.md` to include `docs/`, which the old filter missed entirely — `docs/site` carries `hugo.yaml`, `package.json`, its own `go.mod` and an `.scss`, so editing the published site's stylesheet used to run the whole Go pipeline. And `changes` now asserts the invariant the fast path rests on: no `//go:embed` may reference Markdown or `docs/`, since an embedded `.md` would let a docs-only PR skip the tests for a change that alters the built binary.
- **The context-fill ramp follows the active theme** ([#109](https://github.com/go-steer/core-tui/issues/109)). The `<used> / <size>` segment in the status header and in the sidebar's spend block is the one piece of chrome carrying real semantic weight — a three-tier ramp that shows the operator how close the context window is to overflowing before it bites — and it was the one piece that ignored the palette. `contextFillStyle` picked from three hex literals baked into the function (`#5FD787` / `#FFD75F` / `#FF5F5F`), so the ramp rendered identically across all twelve builtin themes and across the light and dark variant of each. Those three values are the *default* theme's `Success` / `Warning` / `Error`, which is why the defect was invisible on the house palette and worst everywhere else: on a light terminal the 85%-and-above tier is a pale coral on white — the tier the operator most needs to be able to read is the one that falls out of contrast. The three tiers now resolve `Theme.Success` / `Theme.Warning` / `Theme.Error` off the active theme, tokens that already existed, so no theme gained a field and no host action is required. Christmas gets pine / gold / cardinal, Google gets its brand green / orange / red, and the default palette renders byte for byte what it did before. The doc comment above the function had claimed a themed ramp all along; it is accurate now. Golden-neutral by construction — the frame corpus wires no `UsageTracker`, so neither usage summary reaches a golden.
- **A filename in a tool event no longer costs a full Chroma lexer sweep** ([#106](https://github.com/go-steer/core-tui/issues/106)). `detectLang` mapped a path to a lexer name by calling `lexers.Match` and nothing else — no memoization, on a function whose own upstream doc comment warns that it runs hundreds of `filepath.Match` calls per lookup. It is reached from four call sites on the tool-display path (`tool_preview.go` twice, `tool_preview_read.go`, `tool_preview_result.go`), which is to say once for every tool event that carries a filename, and the same handful of paths in a session re-paid the sweep every single time. Measured on this repo's usual CI hardware (AMD EPYC 7B12), one uncached lookup costs **4.89ms** for a path that matches a lexer and **2.42ms** for one that doesn't — a millisecond-scale stall on the UI goroutine per event, for an answer that cannot change. The result is now memoized in a `sync.Map` alongside the existing per-line `syntaxCache` and `lexerCache` in the same file, taking a hit to **19.8ns** and a miss to **19.5ns** (**247x** and **123x**), with zero allocations where the matcher's priority sort previously allocated on every hit. One deliberate divergence from the neighbouring `getLexer`, which does *not* cache its nil result: `detectLang` **does** cache the empty-string miss. `""` is the ordinary answer for the paths a session touches most — `README`, `LICENSE`, `Makefile`, extensionless notes, plain text — so leaving negative results out would have left the hotter half of the path uncached, on the branch that is the more expensive of the two to be wrong about at scale. There is no invalidation to get wrong: `detectLang` is a pure function of its label, so an entry stays correct for the life of the process, and the working set is bounded by the number of distinct paths one session touches. The render benchmarks are unmoved — `BenchmarkRefreshViewport` at 400 turns 6.90ms → 6.70ms, its cold-cache variant 19.1ms → 19.3ms, allocation counts identical — so nothing was borrowed from the repaint path to pay for this.
- **The thinking spinner can no longer end up running two tick chains at once** ([#112](https://github.com/go-steer/core-tui/issues/112)). `spinnerTick()` returned a bare `tea.Tick` carrying no identity — `spinnerTickMsg` was an empty struct — and the handler re-arms on every tick, so any path that armed while a chain was already live produced two independent chains, each re-arming the other's replacement forever. The visible symptom is the thinking/working verb pool rotating at 2x (3x after another overlap) for the rest of the session; the underlying one is `tea.Tick` accumulation that nothing ever reclaims. The existing level gate (`m.state != stateStreaming && (!m.liveMode || !m.spinnerActive)`) does not catch it, and cannot: it answers "should *a* spinner be running right now", not "which chain is this tick from", and the turn-chaining paths flip `m.state` back to `stateStreaming` well inside one 3s cadence — a queue drain or an auto-continue re-enters `submitTurn` from the same `turnDoneMsg` handler that just finalized the previous turn, so the superseded chain's next tick arrives to a streaming model, passes the gate and re-arms. Every tick now carries a `spinnerGen` stamp and the handler drops one that doesn't match, which terminates the superseded chain instead of doubling it. The generation is bumped where a new animation *begins* — `submitTurn` for a per-turn spinner, `applyStreamChunk`'s false→true `spinnerActive` flip for a LiveAgent stretch, which has no `submitTurn` to hang off — and all seven arming sites go through one `armSpinner()` helper so the stamp can't be forgotten at one of them. Deliberately a new per-turn counter rather than the existing `sessionGen`: that one only moves in `applySwitchTarget`, i.e. on a session switch, so the two overlapping turns *inside* one session that produce this bug would share a generation and the guard would be a no-op on the reported path. Same shape as the `resizeGen` stamp on `resizeReflowMsg` ([#104](https://github.com/go-steer/core-tui/issues/104)). No render path changed — the golden corpus is untouched.
- **The layout row budget is measured rather than guessed** ([#103](https://github.com/go-steer/core-tui/issues/103)). `resize()` sizes the chat viewport by subtracting the chrome around it from the terminal height, and three of its terms were wrong. The **header** was pinned at a literal `2 // status line + a blank row` while every sibling term called `lipgloss.Height` — but `renderHeader` word-wraps the status line, so on a 20-column terminal it occupies four rows and the frame came out a row taller than the terminal; the clipping post-pass from [#102](https://github.com/go-steer/core-tui/issues/102) then quietly ate the bottom of the footer, which is the row that tells a new operator how to quit. The **toast** had no term at all: `View()` slots the wake banner between the input box and the footer, so a visible toast pushed the frame over budget too, and only a spare row left over from an unexplained `- 2 // input top border + spacer` (there is no spacer) kept that from showing on a wide terminal. The **prompt-queue panel** was charged in the opposite direction — it is appended by `renderInProgress` into the viewport's *content*, not joined beside the viewport the way the footer, help panel and palette are, so subtracting it shrank the viewport by the panel's height and then spent that height drawing the panel inside the smaller viewport: a four-row queue cost the operator four rows of chat and left the frame four rows short of the terminal. Every term is now measured from the string `View()` will actually render, at the width it will render at, and only siblings of the viewport are charged. The composed frame is now exactly as tall as the terminal across the full width × height × layout matrix, so the height cap in `clipFrame` stops firing in normal use — which is what [#102](https://github.com/go-steer/core-tui/issues/102) said should happen once this landed. The new `tui/resize_budget_test.go` asserts that exactness directly, because the frame-invariant grid only checks that the frame does not *overflow* and is therefore satisfied by clipping.
- **The rendered frame is clamped to the terminal in both dimensions** ([#102](https://github.com/go-steer/core-tui/issues/102)). Nothing clamped it. Chrome is composed with `lipgloss.JoinVertical` / `JoinHorizontal` / `Place` and plain string concatenation, so overflow was possible at every join and nothing caught it at the end — the frame-invariant grid landing alongside this fix found 83 of its 200 cells overflowing, up to 82 columns of output in a 40-column terminal and 62 rows in a 24-row one. The height case was the nastier of the two, because Bubble Tea drops the **top** lines when a frame is too tall: the status header simply disappeared, with no indication anything had been cut. `View()` now ends with a clipping post-pass that truncates every line to the terminal width with `ansi.Truncate` — escape-aware, so an SGR sequence is never sliced in half the way byte-level cutting would — and caps the frame at the terminal's row count, keeping the *first* rows so the header is the part that survives. A frame that already fits comes back byte-identical; a not-yet-known geometry passes through untouched. This is a safety net, not a layout step: the row *budget* is [#103](https://github.com/go-steer/core-tui/issues/103), and once that lands the height cap should stop firing. It stays regardless — a compositor sizes its canvas to the union of its layer bounds, not to the terminal, so it would reproduce this same bug.
- **Dragging a terminal pane no longer freezes the UI** ([#104](https://github.com/go-steer/core-tui/issues/104)). A width-changing `tea.WindowSizeMsg` re-rendered *every* assistant message in the transcript through Glamour, synchronously, inside the message handler — and a pane drag emits a stream of those events, one per column crossed, so the cost was paid dozens of times over a single gesture while the UI sat unresponsive. The render memo made it worse: it pinned one width and dropped every entry the moment the width changed, so the re-assembly that followed the Glamour pass was fully cold too, on every event and again on the way back when the operator dragged the pane to a width already rendered at. The whole-history pass is now debounced behind a 120ms settle tick (generation-stamped, so a superseded drag's callback can never clobber a newer resize): a resize event reflows only the messages actually on screen — bounded by viewport height, not by session length — and the backlog warms after the drag stops, eight rows per tick rather than in one synchronous burst. Cache entries are keyed per `(message, width)` instead of behind a single width pin, so a row already rendered at the incoming width survives; rows off screen mid-drag are carried over from the previous width and retired by the warm pass before they can be scrolled into view. Measured with the repo's own `BenchmarkResizeWidthChange` (AMD EPYC 7B12), one width-changing event costs **3.63ms → 2.32ms** at 10 turns, **32.2ms → 3.97ms** at 100, and **126ms → 9.27ms** at 400 — and its allocation count, 47.3K / 463K / 1.84M before, is now 28.4K / 28.5K / 29.3K, i.e. flat in transcript length rather than linear in it. The `BenchmarkResizeHeightOnly` control (6.89ms → 6.84ms at 400 turns) and the steady-state repaint (`BenchmarkRefreshViewport`, 6.52ms → 6.60ms) are unmoved, so nothing was borrowed from the paths a resize is not on. Over a whole 30-event drag on richer turns (`tui/resize_drag_bench_test.go`, prose + list + fenced code): **8.48s → 0.72s** at 100 turns and **32.4s → 2.16s** at 400, with the deferred settle adding 0.30s and 1.19s respectively — 8.3x and 9.7x for the entire gesture. What remains per event is the transcript concat and `viewport.SetContent`, which are O(history) no matter what is on screen; removing those needs a windowed list, which this fix deliberately does not reach for. Follow state (#93) is untouched by any of it — a drag leaves an operator pinned to the tail pinned, and one reading scrollback where they were.
- **A permission prompt can no longer be answered by input the operator was already typing** ([#95](https://github.com/go-steer/core-tui/issues/95)). Permission requests arrive asynchronously and the modal was decision-live from the frame it appeared, so buffered keystrokes were consumed as the answer: typing `say` while prompts landed dispatched allow-session, allow-always and allow-once — three grants, one of them persistent, from a word aimed at the prompt. The six decision keys are now inert for 300ms after the modal appears, and keystrokes inside that window are routed to the prompt instead of being dropped. `esc` is exempt: it denies, which is the fail-safe direction. The elicitation modal gets the same window on the keys that dispatch a result (accept / decline / submit), while field editing and navigation stay live. New **R-PERM-8** / **R-ELIC-4** in `docs/requirements.md`.
- **Auto-follow survives a resize mid-stream** ([#93](https://github.com/go-steer/core-tui/issues/93)). Following the live tail was inferred from `viewport.AtBottom()` at repaint time, and the resize handler applied the new height *before* that sample — shrinking the terminal by a row (or typing enough to wrap the textarea onto a second line) made a pinned viewport report "not at bottom", so the re-pin was skipped and the rest of the turn streamed away below the visible region with no indication. Following is now tracked as explicit state on the model: scrolling up releases it, scrolling back to the bottom re-arms it, operator-initiated jumps (submit, slash output, session switch, `ctrl+l`) set it outright, and geometry changes leave it alone.
- **The elicitation form accepts non-ASCII input** ([#91](https://github.com/go-steer/core-tui/issues/91)). Two byte-vs-rune defects made the form ASCII-only: the append guard measured `len(stroke)` in bytes, so every printable character with a multi-byte encoding (`é`, `ü`, `日`, `😀`) was silently dropped — typing `café` yielded `caf` — and backspace sliced one *byte* off the value, leaving invalid UTF-8 in both the rendered frame and the `ElicitResult` handed to the host. Both sites are rune-aware now, and the append guard additionally rejects unprintable runes.
- **Truncated diffs no longer advertise a keybinding that doesn't exist** ([#94](https://github.com/go-steer/core-tui/issues/94)). The marker under a clipped diff read `… +N more lines · ctrl+o to expand (todo)`. `ctrl+o` is bound nowhere in the package, so the one key the transcript named was the one key that did nothing — and the note-to-self shipped to operators. The marker is now just `… +N more lines`. Row-level diff expansion needs a message cursor first; when it lands, the affordance comes back with a key behind it.
- **The scrollbar thumb no longer vanishes at the bottom of long content** ([#92](https://github.com/go-steer/core-tui/issues/92)). `Scrollbar` computed one more thumb position than it drew rows, so at maximum scroll the thumb landed one row past the end of the track: clipped short for a multi-row thumb, and gone entirely for the one-row thumb any content much longer than the viewport produces. The thumb now rests flush on the final row at maximum offset.

### Removed

- **The vestigial enum-driven overlay path** ([#90](https://github.com/go-steer/core-tui/pull/90), closing [#79](https://github.com/go-steer/core-tui/issues/79)). `Model` carried an unexported `overlay` enum (`overlayNone` / `overlayModelPicker`) left over from the visual-preview slice, along with the `modelPickerIdx` field, `renderOverlay`, the `case m.overlay != overlayNone` arm in `View`'s modal cascade, the matching `footerHint` case, and a second copy of the model-picker key handling in `Update`. A write-set analysis over `tui/*.go` **and** the test suite (`docs/api-audit.md` §4) found no assignment of any non-`overlayNone` value anywhere, so `m.overlay` was provably always `overlayNone` and every consumer of it was unreachable. Real picker state has lived in the `Dialog` stack since v0.1.0; `Ctrl+G` and `/model` both open `modelPickerDialog`. No exported symbol changed and **no host action is required** — this is dead-code removal ahead of the v1.0 freeze, so the paths don't become permanent maintenance obligations.

The `usageMsg` bullet on that issue was a **false positive and nothing changed for it**: `usageMsg` is not legacy, it is still fed from `Event.Usage` / `Event.Model` in `tui/agentcmd.go`, and `turnSummaryMsg` is an additional push-mode path (v0.9.0), not a replacement. Deleting it would have broken per-turn usage rendering for every pull-mode host.

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
