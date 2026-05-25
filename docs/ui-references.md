# UI references

Black-box observations from other agentic TUIs we've watched, kept here
as a source of design inspiration. Each entry is **observation only** —
patterns we noticed by running or watching the product, not code we've
read or lifted. Mixing this channel with source-reading would create
cleanroom-hygiene problems, so keep the rule strict: anything in this
file came from screenshots, GIFs, recordings, public docs, or live use,
not from the upstream source tree.

When an observation is worth acting on, link it from the relevant
section of [`requirements.md`](./requirements.md) or
[`design.md`](./design.md) and note the action there. This doc remains
the catalog; the design docs remain the spec.

Entries are alphabetical. New entries belong in the right slot — no
chronological / favorite ordering.

---

## google/antigravity-cli (`agy`)

Source observed: the `agy-cli-demo.gif` linked from
[deepwiki.com/google-antigravity/antigravity-cli](https://deepwiki.com/google-antigravity/antigravity-cli)
(377 frames, 964×720). Sampled 8 frames evenly across the animation in
May 2026. Cross-checked with the
[Google Developers Blog announcement](https://developers.googleblog.com/an-important-update-transitioning-gemini-cli-to-antigravity-cli/)
and the [agentpedia deep-dive](https://agentpedia.codes/blog/antigravity-cli-deep-dive).

License posture: the CLI binary is closed-source (the Gemini-CLI
predecessor was Apache-2.0; the rewrite shipped as a closed Go
binary). Visual observation only — no source channel exists for us
to contaminate.

### Layout

- **No persistent sidebar.** Full-width chat occupies the terminal.
  Status fits into a single right-aligned bottom line (`Gemini 3.5
  Flash`) plus a left-aligned `esc to cancel` / `? for shortcuts`
  hint. The whole UI is a stack of full-width sections rather than a
  two-column composition.
- **Splash screen** (first frame): pixelated rainbow `A` logo, then
  `Antigravity CLI 1.0.0` / `Gemini 3.5 Flash` header. Empty `>`
  prompt below a horizontal divider. Very restrained — no marketing
  blurb, no animated banner.
- **Input field** sits at the bottom under a thin horizontal divider;
  always visible.

### Chat rendering

- **Cognition markers** at the top of a turn — `> Thought for 6s, 388
  tokens`. Includes both elapsed time and a token count (more
  informative than Crush's bare "Thought for 2s").
- **Tool calls** prefixed with a bullet glyph and a typed verb:
  `● Create(/Users/.../index.html) (ctrl+o to expand)`. The
  `(ctrl+o to expand)` affordance lives on the same line as the tool
  call — inline rather than in a separate footer hint.
- **Collapsible sections.** `▸ Thought Process` and similar are
  collapsed-by-default headers the user can expand.
- **Slash command output** rendered as a tree:
  `/artifact\n  └ Exited /artifact command`. The `└` glyph and the
  word `Exited` make the command boundary explicit.
- **State verbs.** `Loading...` and `Working...` appear at different
  points — distinct vocabulary signals which agent activity is in
  flight (model call vs tool execution). More informative than a
  generic "thinking" word.
- **Artifact counter** at the right edge of the in-progress message:
  `1 artifact · /artifact to review`. Both a count and the action to
  see them.

### Modals / overlays

- **File pager** is a full-screen takeover with line numbers, a thin
  vertical rule on the left, and a status line `[0%] L1  1-19/120`
  (percent through file · top line · range). Footer keymap:
  `↑/↓ scroll · pgup/pgdown page · shift+g bottom · g top · c comment
  · l hide lines · esc close`. Built-in pager rather than shelling
  out.
- **Artifact preview** — the demo cuts from the TUI to what looks
  like a rendered browser window of the built application. We can't
  tell from the GIF whether this is a terminal-embedded preview or a
  browser tab being launched, but the "artifact" concept itself —
  the agent produces a named, reviewable deliverable that the user
  can `/artifact` to inspect — is distinct from how cogo / core-agent
  surface work.

### Patterns worth borrowing

| Antigravity pattern | Where it could land in core-tui |
|---|---|
| Cognition marker includes elapsed + token count (`Thought for 6s, 388 tokens`) | Stronger version of R-CHAT-3's rotating "thinking" line. Could become a settled per-turn affordance. |
| State verbs distinguish `Loading...` (model) vs `Working...` (tool) | Currently R-CHAT-3 has one rotating phrase. Two verbs convey what's actually happening. |
| Inline `(ctrl+o to expand)` affordance per tool call | Better than a context-sensitive footer for discoverability. Could refine R-CHAT-5. |
| Artifact counter + review command on the in-progress message | We don't have artifacts as a concept; if a future agent capability surfaces structured deliverables, this is the UX. |
| Built-in pager for file output instead of shelling out | Worth considering when the assistant returns large file contents — keeps the TUI's keymap consistent. |

### Patterns to reject

- The "Gemini 3.5 Flash" status as the only persistent indicator. We
  want more — context-window %, cost, modified files — per R-USE-2.

---

## anthropic/claude-code

Source observed: the [code.claude.com docs](https://code.claude.com/docs/en/overview)
plus first-hand UX experience (this very file was authored from inside
a Claude Code session). License posture: closed source — no upstream
code to risk. This entry is restricted to **user-visible UX**: any
implementation detail observed by introspection is excluded by the
cleanroom rule.

### Layout

- **No sidebar.** Full-width terminal; the chat is a single column.
- **Input box** sits at the bottom of the screen with a `>` prefix.
  Multi-line input is supported (Shift+Enter for newline; Enter to
  submit). The input box is always visible — never replaced by an
  overlay.
- **Status line** below the input shows context-window utilization, a
  hint like `? for shortcuts`, and the active session / model.
- **Permission mode chip** in the status line — `default`, `accept
  edits`, `plan mode`, `bypass permissions` — toggled with
  `Shift+Tab`. The visible state of the chip changes both the icon
  and the prompt-edge color, so the user always sees which mode is
  active.

### Chat rendering

- **Tool calls** rendered as a single line with a glyph, the tool
  name, and a one-line summary — e.g. `⏺ Read(README.md)` or
  `⏺ Bash(go test ./...)`. Output is shown below in a collapsed
  block; `ctrl-r` toggles full output for long results.
- **Code edits** rendered as in-place diffs with red/green hunks,
  preserving line numbers from the file.
- **Markdown** rendered with bold/italic/code-fence styling.
- **Todo list** affordance: when the assistant publishes a todo list,
  it renders as a checklist with `☐` / `☑` glyphs, updating live as
  items move through `pending → in_progress → completed`. Different
  from the rest of the chat — it occupies a visually distinct block.
- **Streaming**: assistant tokens stream live with markdown rendering
  applied on each update. No garbled-hex indicator (cf. Crush).
- **System reminders**: synthetic system messages appear inline,
  visually muted, prefixed with `<system-reminder>`. Used for
  background context (recent file edits, task-tool nudges, plan-file
  status).

### Slash command palette

- Triggered by typing `/` as the first character. Opens a vertical
  list above the input, scrollable, with the matching commands
  filtered live as the user keeps typing.
- Each row shows the command name plus a brief description in muted
  text on the right.
- Built-in commands (`/help`, `/clear`, `/compact`, `/model`, etc.)
  and user-defined skills (e.g. `/review`, `/verify`) appear in the
  same list, scrolled together rather than sectioned.
- Selecting an entry inserts the command into the input; the user
  still presses Enter to submit (matches our R-PAL-1).

### File picker (`@`)

- Triggered by `@` at any position in the input. Opens a similar
  vertical list of project files filtered by typed prefix.
- Respects `.gitignore` and common exclude directories (the same
  exclude pattern we name in R-PAL-4 covers most of what's hidden).
- Selecting inserts `@path/to/file` into the prompt at the cursor
  position. The file content is read and inlined into the prompt the
  user submits (matches our R-AT-1).

### Permission prompts

- Not modal in the visual sense — appear as inline question rows
  with the options spelled out: `[1] Allow once  [2] Allow for
  session  [3] Deny`. The user types `1`/`2`/`3` or `y`/`n` to choose.
- For shell commands, the full command being approved is shown
  verbatim — matches the Crush pattern of "show the payload, not
  just the verb." Our R-PERM-1 could be tightened in the same
  direction (linked back from `decisions.md` if we adopt).
- For file edits, the diff is shown above the choices, so the user
  sees exactly what will change.
- Persistent "allow always" decisions are written to a project-local
  settings file (`.claude/settings.local.json` analogue) — matches
  our R-PERM-3 host-callback contract.

### Status indicators

- **Spinner** during model work: a single rotating glyph at the top
  of the in-progress assistant message, plus a paraphrased present-
  continuous verb ("Searching", "Editing", "Running tests"). The
  verb is task-aware — different from our generic rotating-phrases
  spec (R-CHAT-3).
- **Token / cost meter** in the status line: percent of context used,
  rendered as a small bar.
- **MCP server indicators**: when MCP servers are configured, a
  count appears in the status line; `/mcp` opens a list view.

### Patterns worth borrowing

| Claude Code pattern | Where it could land in core-tui |
|---|---|
| Permission-mode chip with `Shift+Tab` cycling | Not in our spec; would be a new requirement under §3.7. Cleaner than `/permissions` for the common case. |
| Inline permission prompts (no modal) for low-risk decisions | Our R-PERM-1 mandates a blocking modal; could be relaxed to "modal for high-risk, inline for low-risk" with the host classifying. |
| Task-aware spinner verbs (Searching / Editing / Running) | Refinement of R-CHAT-3 — the rotating phrases could be agent-classified rather than random. Would need a hook on the `Event` type. |
| Slash palette and `@` palette share the same row affordance | We already align (R-PAL-1 + R-PAL-2); the visual consistency is the point. |
| System-reminder channel separate from chat | Useful for surfacing context (recent edits, plan changes) without polluting the conversation. Could become a new requirement under §3.X. |
| Diff shown above choice in permission prompts | Same takeaway as Crush — strengthens R-PERM-1's "Detail" requirement. |

### Patterns to reject

- The lack of a sidebar means model/cost is squeezed into a single
  status line. Crush's sidebar handles this better — we leave the
  trade-off open under R-USE-2.

---

## openai/codex (`codex`)

Source observed: the [README splash image](https://github.com/openai/codex/raw/main/.github/codex-cli-splash.png),
[developers.openai.com/codex/cli](https://developers.openai.com/codex/cli),
and the [features page](https://developers.openai.com/codex/cli/features).
License posture: Apache-2.0, Rust source on GitHub — but we still
treat this as observation-only to keep the cleanroom rule
unconditional across entries. Anyone implementing core-tui must not
read the codex repo and then write equivalent code shortly after.

### Layout

- **Bordered welcome panel** on launch — rounded-corner ASCII border
  with model + working directory inside. Shows `model: gpt-5.2-codex-
  medium /model to change` and `directory: ~/path/to/repo`, plus a
  `Tip:` line at the bottom of the panel suggesting how to send
  feedback.
- **No persistent sidebar.** Full-width chat below the welcome panel.
- **Compact status footer** at the bottom (model name + slash-command
  hint) similar to Antigravity.

### Chat rendering

- **Bullet glyphs as state markers** in assistant output:
  `▸ Updated Plan`, `● Explored`, `□`/`■` for plan checklist items.
  The visual hierarchy uses glyph + indent rather than typography
  weight.
- **Plan-first turn structure.** The agent emits an "Updated Plan"
  block early, listing intended steps as a checklist; each item
  flips state as work progresses. Similar to Claude Code's todo
  list, surfaced more prominently as a first-class turn artifact.
- **Markdown** rendered with syntax-highlighted code blocks and diffs.
- **Theming** via `/theme` — saves a preferred color theme; broader
  than what our R-MD-4 specs (single `MarkdownStyle` override).
- **Image input.** The composer accepts pasted or attached image
  files (PNG/JPEG). The image is included in the model prompt
  alongside text. We don't currently spec this; relevant if a future
  agent surface wants multi-modal input.

### Slash commands and shell mode

- `/model` to switch, `/feedback` to report, `/theme` to recolor.
- **Headless mode** via `codex exec` runs the agent without the TUI —
  same agent harness, no Bubble Tea / Ink-style overlay. We
  explicitly leave headless out of scope (requirements §6); Codex's
  split-binary approach is one way to address it if we ever reverse.

### Patterns worth borrowing

| Codex pattern | Where it could land in core-tui |
|---|---|
| Bordered welcome panel with model + cwd + tip | Optional polish on first-launch view. Doesn't conflict with R-BRAND-1; could be a Branding option. |
| Plan-as-checklist surfaced first-class in the turn | Similar to Claude Code's todos. Worth a §3.X requirement if we want to expose structured plan rendering. |
| `/theme` for runtime theme switching | Broader than R-MD-4. Could become a `ThemeSwitcher` capability later. |
| Image input | Not in v1 scope, but a hint that the `Event`/`Options` shape may need to admit multi-modal input later. |

### Patterns to reject

- Headless via a second binary — we keep R-NONE (no headless) until
  there's demand. Single-binary scope is simpler.

---

## charmbracelet/crush

Source observed: official demo GIF in the project README (149 frames,
1600×900). Sampled 8 frames evenly across the animation in May 2026.

License posture: Crush is **FSL-1.1-MIT**, so its source is off-limits
for us. Visual observation of the running product is the only safe
channel; nothing in this section came from reading their code.

### Layout

- **Right-side sidebar** carrying session metadata: title, CWD, current
  model (with a `◇` glyph), thinking-mode state, context-window % +
  token count + cumulative session cost, modified-files list, LSPs,
  MCPs. Single column, no boxes — sections separated by faint
  underlines that span the panel width.
- **Sidebar collapses** to give the chat ~40% more width. When
  collapsed, the top edge becomes a thin diagonal-stripe header bar
  with the model status migrated up to it (`~/src/crush · 16% ·
  ctrl+d open`).
- **Always-present footer keymap legend** (`esc cancel · tab focus
  chat · ctrl+p commands · shift+enter newline · ctrl+c quit · ctrl+g
  more`). Reformulates per overlay — the permission modal swaps it
  for `t toggle diff mode · shift+↓↑ scroll`.

### Chat rendering

- **Tool calls inline** with `✓` + verb + path + one-line summary,
  then indented body (file content with line numbers, diffs with
  `@@ -777,7 +777,7 @@` ribbons, bash output).
- **Truncation token** `…(961 lines)` for elided file content inside
  a tool result.
- **`Thought for 2s` cognition markers** sit between tool calls, in
  muted text. Distinct from our spec's rotating "thinking…" indicator
  (R-CHAT-3) — Crush uses it as a between-step beat.
- **Streaming-token indicator** at the bottom of the in-progress
  message: garbled hex characters + the literal word "Thinking". We
  judge this **distracting**; our R-CHAT-4 (Glamour-render-on-each-
  update) is the better path. **Anti-pattern.**
- **Per-turn footer line** `◇ Claude Sonnet 4 45s` (model + elapsed).
- **Color palette**: deep purple background, magenta/pink accents,
  muted greys, green for diff additions, red for removals. Borders
  are scarce — spacing + background tints do the section-separation
  work.

### Modals

All modals layer over the chat **without a dimming scrim** — just a
bordered box; the underlying content shows through where the box
isn't.

- **Command palette** (`ctrl+p`):
  - Hash-stripe `Commands` title bar carrying the brand into the
    overlay.
  - `● System ○ User` tab filter at the top right. Interesting
    parallel to our R-CMD-3/4 split (built-in vs host vs agent
    commands) — a one-axis filter the user can flip.
  - Live filter as you type (`> s` → matches starting with `s`).
  - Per-item right-aligned shortcut hint (`Switch Session    ctrl+s`).
  - Footer keymap: `tab switch selection · ↑↓ choose · enter confirm
    · esc cancel`.

- **Permission modal**:
  - Header `Permission Required` with the same hash-stripe brand bar.
  - Shows the **entire diff** of what's about to be written, inline
    in the modal — not just the tool name + verb. Strong reinforcement
    that "show me what you're about to do" beats "ask me to trust the
    label."
  - Three buttons: `[Allow]`, `[Allow for Session]`, `[Deny]`.
    Selected one (Allow) bright magenta. **Fewer choices than our
    R-PERM-2 six-decision spec** — Crush has no verb-scope, no
    tool-scope, no always-with-persistence. We keep our six.

- **Model picker** (`/model` equivalent):
  - **Full-screen takeover** — sidebar hidden, no chat visible behind.
    Stronger modal than the floating picker our R-MOD-1 implies.
  - `Choose a Model` header, live filter (`> kimi`), grouped by
    provider (Groq / OpenRouter), matching substring underlined in
    each row.
  - Footer keymap: `↑/↓ choose · enter accept · ctrl+c quit`.

- **Toast / banner** (separate channel from in-chat system messages):
  green bar at the bottom edge — `OKAY! large model changed to
  claude-opus-4-20250514`. Used for confirmations that don't belong in
  the conversation log.

### Patterns worth borrowing (UX only, recreate from scratch)

| Crush pattern | Where it could land in core-tui |
|---|---|
| Sidebar with model + cost + LSP/MCP status, collapsible | Discussion under R-USE-2 — header bar is one option; collapsible sidebar is another. Worth a design.md addendum. |
| Per-overlay context-sensitive footer keymap | Underspecified in R-FOOT-1 — could be tightened to "footer hint reformulates per active overlay." |
| Permission modal shows the actual payload (diff for edits, command for bash), not just the verb | Strengthens R-PERM-1 — could become an explicit "Detail must include the rendered diff when the tool is an edit, the full command when the tool is bash" sub-rule. |
| Live filter + right-aligned shortcut hint in palettes | Already covered by R-PAL-3; the right-aligned shortcut is a small palette polish to add when implementing. |
| Toast/banner for confirmations | Not in current spec. Worth a new requirement under §3.15 or a new §3.X for non-chat notifications. |
| `Thought for Ns` between-step beats | Different from R-CHAT-3 rotating indicator. Could complement it; would need a new requirement or an R-CHAT-3 extension. |

### Patterns to reject

- The garbled-hex streaming indicator. We render Glamour partials
  live (R-CHAT-4) instead.
- Crush's three-decision permission modal. Our six (R-PERM-2) are
  more expressive (allow-session-verb / allow-session-tool /
  allow-always with persistence).
- The hash-stripe brand texture. Our R-BRAND-1 keeps branding
  pluggable via `Options.Branding`; baking in a single house style
  defeats the purpose.

---

## anysphere/cursor-agent (Cursor CLI)

Source observed: [cursor.com/cli](https://cursor.com/cli) marketing
page (no real CLI screenshots — only IDE-integration icons and a
landscape background) and the
[Codecademy tutorial](https://www.codecademy.com/article/getting-started-with-cursor-cli).
No demo GIF or asciinema cast is published. **This entry is documented
features only, not screenshot-verified.** If we get hands-on access
later, revise.

License posture: closed-source binary; tutorial-derived feature list.
Cursor's CLI is reportedly built with [Ink](https://github.com/vadimdemedes/ink)
(React for CLIs) — a JS/TS stack rather than Bubble Tea. We can't
copy idioms across the framework boundary even if we wanted to.

### Layout (documented)

- Single-pane interactive TUI; non-interactive `--print` mode for
  scripts and CI.
- Header / status line shows agent name, CWD, and current git branch
  (e.g. `~/anysphere/research · main`).
- Arrow-style input prompt (`→` or `>`); placeholder reads "Plan,
  search, build anything."
- Hint row below the input enumerates the input modes:
  `/ commands · @ files · ! shell`.

### Input modes (documented)

- **`/` slash commands** — `/model` switches the active model with
  options like Auto, Composer 2.5, Opus 4.7, GPT-5.5 High Fast,
  Gemini 3.1 Pro, Grok 4.3; `/compress` shrinks the context.
- **`@` file picker** — references specific files or folders inline
  in the prompt. Behavior matches our R-AT-1.
- **`!` shell mode** — executes shell commands directly from the
  CLI, with safety checks before run.

### Other affordances (documented)

- **Diff review** via `Ctrl+R`; follow-up instructions added with
  `I`.
- **Session history** — `cursor-agent ls` lists prior sessions,
  `cursor-agent resume <id>` continues a thread. The two-command
  pattern (list + resume) is heavier than core-tui's transcript-only
  approach.
- **Permission prompts** — text-based `Y` / `N` choices before
  shell-command execution. No documentation of multi-choice
  decisions (session-scope, always-allow, etc.).
- **TTY requirement** — the CLI hangs without a real PTY, which
  matters for CI integration. Workaround: run inside `tmux` and
  capture via `tmux capture-pane`.

### Patterns worth borrowing

| Cursor pattern | Where it could land in core-tui |
|---|---|
| Single explicit hint row enumerating `/ @ !` modes | Discoverable affordance — clearer than our footer keymap. Could be added to R-FOOT-1 as a permanent input-hint line. |
| Session list + resume (`ls` / `resume <id>`) | We exclude resume from v1 (D20). The two-command surface is worth bookmarking if we add it later. |
| Provider-name in `/model` (`MoonshotAI: Kimi K2`) | Our R-MOD-1 takes `ModelInfo{ID, Display, Description}` — provider grouping (or a `Provider` field) is a small refinement. |

### Patterns to reject

- The Ink/React stack — not applicable to a Go library. Mentioned
  only as context.
- `--print` non-interactive mode within the same binary — we
  explicitly exclude headless from v1 (requirements §6).
- TTY-required-or-hang behavior. We must degrade cleanly if stdout
  is not a TTY (e.g. print a helpful error rather than block).
