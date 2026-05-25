# UI references

Black-box observations from other agentic TUIs we've watched, kept here
as a source of design inspiration. Each entry is **observation only** —
patterns we noticed by running or watching the product, not code we've
read or lifted. Mixing this channel with source-reading would create
cleanroom-hygiene problems, so keep the rule strict: anything in this
file came from screenshots, GIFs, recordings, or live use, not from the
upstream source tree.

When an observation is worth acting on, link it from the relevant
section of [`requirements.md`](./requirements.md) or
[`design.md`](./design.md) and note the action there. This doc remains
the catalog; the design docs remain the spec.

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

- **Tool calls inline** with `✓` glyph + verb + path + one-line
  summary, then indented body (file content with line numbers, diffs
  with `@@ -777,7 +777,7 @@` ribbons, bash output).
- **Truncation token** `…(961 lines)` for elided file content inside
  a tool result.
- **`Thought for 2s` cognition markers** sit between tool calls,
  in muted text. Distinct from our spec's rotating "thinking…"
  indicator (R-CHAT-3) — Crush uses it as a between-step beat.
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

### Patterns we explicitly reject

- The garbled-hex streaming indicator. We render Glamour partials
  live (R-CHAT-4) instead.
- Crush's three-decision permission modal. Our six (R-PERM-2) are
  more expressive (allow-session-verb / allow-session-tool /
  allow-always with persistence).
- The hash-stripe brand texture. Our R-BRAND-1 keeps branding
  pluggable via `Options.Branding`; baking in a single house style
  defeats the purpose.
