# core-tui visual style

The house style for `core-tui`. Implementation MUST follow this doc;
hosts MAY override per-axis via [`Options.Branding`](./design.md#34-required-from-host-tui--host-callbacks)
and the related fields enumerated below.

This is the visual rule book that companions [`design.md`](./design.md)
(the engineering spec) and [`requirements.md`](./requirements.md) (the
behavior spec). `R-BRAND-1` cross-references this file as the source
of truth for defaults.

The goal is **lean and sleek without losing functionality**. Concretely:

- Restrained color palette (≤ 8 hues across the whole TUI).
- Glyph-forward visual hierarchy — Unicode anchors instead of ASCII
  decoration.
- Generous vertical whitespace; sparse horizontal whitespace.
- Borders are rare. Section separation is by spacing + dim color, not
  boxes.
- Modals are bordered but un-scrimmed — the chat shows through.

---

## 1. Color palette

### 1.1 Brand colors (fixed, not adaptive)

The wordmark and accent line are brand identity — they read the same
on every terminal background. Lifted from core-agent's existing palette
(Dracula family).

| Token             | Hex         | Use                                                        |
|-------------------|-------------|------------------------------------------------------------|
| `BrandViolet`     | `#BD93F9`   | Wordmark, primary brand accents.                           |
| `BrandPink`       | `#FF79C6`   | Agent identity (DisplayName / model name), secondary brand. |
| `BrandPinkBright` | `#FFB6E1`   | Cursor glyph, micro-pings, "just-changed" callouts.        |
| `BrandSlate`      | `#6272A4`   | Muted separator, dim divider runs.                         |
| `BrandCyan`       | `#5FD7FF`   | Spinner, secondary action accent.                          |

Hosts override the entire set via `Options.Branding.AccentColor`
(replaces `BrandViolet`) and `Options.Branding.SecondaryColor`
(replaces `BrandPink`). The slate/cyan/bright-pink derive from those
by `lipgloss/v2`'s `Lighten` / `Darken` so a brand override doesn't
leave orphaned siblings.

### 1.2 Functional colors (adaptive)

Functional colors switch on terminal background. Lip Gloss v2 dropped
`AdaptiveColor`, so the TUI reads `tea.BackgroundColorMsg` once at
startup (per [R-MD-2](./requirements.md#311-markdown-rendering-must))
and selects the right variant for each token.

| Token       | Light bg  | Dark bg   | Use                                              |
|-------------|-----------|-----------|--------------------------------------------------|
| `FgMuted`   | `#6C6C6C` | `#9A9A9A` | Secondary text (hints, footer keys, timestamps). |
| `FgAccent`  | `#005F87` | `#5FAFFF` | Active selection, in-focus chip.                 |
| `FgUser`    | `#0050A0` | `#87AFFF` | User prompt prefix + user-message body.          |
| `FgAssist`  | `#1E1E1E` | `#D0D0D0` | Assistant message body. **The default text color.** |
| `FgSystem`  | `#5F5F5F` | `#A8A8A8` | System / informational lines.                    |
| `FgError`   | `#AF0000` | `#FF5F5F` | Error lines, denied permissions.                 |
| `FgWarn`    | `#AF8700` | `#FFD75F` | Warnings, `bypassPermissions` chip background.   |
| `FgSuccess` | `#005F00` | `#5FD75F` | Confirmations, diff additions.                   |
| `BorderDim` | `#BCBCBC` | `#5F5F5F` | Modal borders, sidebar divider line.             |
| `RuleDim`   | `#D7D7D7` | `#3A3A3A` | Thin section separator (`▁` run or `─` rule).    |

**Color discipline:** any rendered span uses **exactly one** functional
color plus optional weight (bold / italic / dim). Never combine two
foreground colors on one span. Background tints are reserved for the
permission-mode chip and the toast banner.

---

## 2. Glyph vocabulary

Default to plain Unicode glyphs that ship with every modern terminal
font (no Nerd-Font dependency). Each glyph is a visual anchor with a
fixed meaning across the TUI.

| Glyph | Code point | Meaning                                              |
|-------|------------|------------------------------------------------------|
| `◇`   | `U+25C7`   | Model identity (precedes model name in status).      |
| `●`   | `U+25CF`   | Tool call (head of a tool-call summary line).        |
| `○`   | `U+25CB`   | Tool call awaiting / pending.                        |
| `✓`   | `U+2713`   | Tool call completed (replaces `●` after success).    |
| `✗`   | `U+2717`   | Tool call failed / denied.                           |
| `▸`   | `U+25B8`   | Collapsible / expandable section (collapsed state).  |
| `▾`   | `U+25BE`   | Collapsible / expandable section (expanded state).   |
| `⚠`   | `U+26A0`   | Warning row (system color: `FgWarn`).                |
| `❯`   | `U+276F`   | User prompt prefix (input line, history recall).     |
| `…`   | `U+2026`   | Truncation marker (`…(N lines)`).                    |
| `·`   | `U+00B7`   | Separator dot in footer keymap (`esc cancel · …`).   |
| `█`   | `U+2588`   | Cursor block (bright pink, persistent blink).         |
| `─`   | `U+2500`   | Horizontal rule (full width, `RuleDim`).              |
| `│`   | `U+2502`   | Sidebar divider column.                              |

**Rules:**
- A row gets at most one anchor glyph at the start; never two anchors
  on the same row.
- Tool-call glyphs are state-bearing — they re-render in place when
  state changes (`●` → `✓` or `✗`).
- ASCII fallbacks are not needed; if the terminal can't render Unicode
  arrows we have bigger problems than aesthetics.

---

## 3. Spacing rhythm

The single most impactful "leanness" lever in a TUI. Density is the
enemy.

- **Within a message body** — paragraphs separated by a single blank
  line. Markdown rendering handles this; preserve Glamour's spacing.
- **Between role-tagged messages** in the same turn — a single blank
  line.
- **Between turns** — two blank lines (one blank + a `RuleDim` `─`
  full-width rule + one blank). The rule is the only horizontal line
  in the chat surface; it earns its place by being the only one.
- **Inline tool calls** sit flush with surrounding prose (no extra
  blank) — they read as a continuation of the same message.
- **Input box** — a single blank line above; status surface flush.
- **No trailing whitespace** on any rendered line.
- **No horizontal padding inside the chat area.** The viewport is
  edge-to-edge; only modals + the sidebar carry inner padding.

---

## 4. Typography & weight

- **Default**: regular weight, `FgAssist`.
- **Bold**: action / command names (`Read`, `Bash`, `Multi-Edit`),
  the active palette row, modal titles, model name in status.
- **Italic**: empty-state placeholders, "(interrupted)" notices,
  per-turn elapsed-time footer (`◇ Claude Sonnet 4 · 45s`).
- **Dim** (faded foreground or `FgMuted`): footer keymap, secondary
  hints, palette descriptions, truncation tokens.
- **Strikethrough**, **underline**: reserved for the palette filter's
  matching-substring highlight (`Kimi K2` with `Kimi` underlined).
- **No `ALL CAPS`.** Lowercase for keymap labels (`esc cancel · tab
  focus chat`); first-letter capital for headings (`Permission
  required`, not `PERMISSION REQUIRED`).

---

## 5. Borders & section separation

The "lean" win is mostly about ripping out borders that were never
load-bearing.

- **Chat area**: no borders. Sections separated by spacing + the
  per-turn `─` rule (§3).
- **Sidebar** (when `StatusLayout = StatusSidebar`): no surrounding
  border. The boundary with the chat is a single `│` column in
  `BorderDim`, padded by one space on each side. Section headings
  inside the sidebar are dim-color, bold, with a single-space
  indent and a thin `─` underline that runs to the end of the panel.
- **Modals**: rounded-corner border in `BorderDim`. Inner padding
  is one row top/bottom, two columns left/right. **No scrim** — the
  chat shows through where the modal doesn't.
- **Input box**: thin top border only (`─` in `BorderDim`); no left/
  right/bottom. It feels grounded without being boxed.
- **Footer / status line**: no border. Sits flush at the bottom of
  the viewport.

---

## 6. Modal composition

Pattern, applied uniformly across permission modal, model picker,
permissions review picker, MCP elicit form, command palette:

```
╭─ Title ───────────────────────────────────────────────────────╮
│                                                               │
│  Body content. One row of padding above and below.            │
│                                                               │
│  Action affordance / fields / list / diff. Width = min(80,    │
│  viewport - 4).                                               │
│                                                               │
│  ──────────────────────────────────────────────────────────   │
│  esc cancel · ↑↓ choose · enter confirm                       │
╰───────────────────────────────────────────────────────────────╯
```

- **Header**: title bold in `BrandViolet`; the `─` run after the
  title fills the remaining width to the closing `╮`. No icon, no
  hash-stripe brand texture (cf. Crush — too house-specific).
- **Footer**: keymap legend dim in `FgMuted`, preceded by a thin
  `─` separator that's flush with the inner padding.
- **Width**: `min(80, viewport - 4)`. Centered horizontally.
- **Vertical placement**: anchored to the top third of the viewport
  when possible (operator's eyes are usually in that band when
  reading chat); never centers in tall viewports because the eye
  has to track downward.

---

## 7. Footer & status format

### 7.1 Footer keymap legend

Single line, dim `FgMuted`, separator `·`, no leading symbol:

```
esc cancel · tab focus chat · ctrl+p commands · shift+enter newline
```

- Order: most-likely action first, least-likely last.
- Key names lowercase (`ctrl+p`, not `Ctrl+P`).
- Action labels lowercase, present-tense imperative (`cancel`, not
  `Cancel` or `Cancelling`).
- Context-sensitive: when a modal is open the legend swaps to
  modal-specific keys.

### 7.2 Status surface

Two layouts (per [R-USE-2](./requirements.md#310-usage-tracking--display-must)).

**StatusHeader** — one line, flush above the chat:

```
◇ Claude Sonnet 4 · default · 9% (19.3K) · $0.04
```

- Each section separated by ` · ` (space-dot-space) in `FgMuted`.
- Model name in `BrandPink`, bold.
- Permission mode in `FgAccent` (regular) or `FgWarn` (when
  `bypassPermissions`).
- Context % + tokens + cost in `FgMuted` regular.

**StatusSidebar** — fixed 32-column right-hand panel:

```
│  ◇ Claude Sonnet 4
│    default · 9% (19.3K) · $0.04
│
│  ─ modified files ───────
│    cmd/foo/main.go     +12 -3
│    pkg/bar/bar_test.go  +5
│
│  ─ subagents ────────────
│    none
```

- Single `│` divider column on the left, `BorderDim`.
- Section headings dim-bold with a thin underline.
- Width fixed at 32 columns; collapsible via `Ctrl+B`.

---

## 8. Branding overrides

Per `R-BRAND-1`, hosts override these specific fields. Everything else
in this doc is house style and not overridable (so brand-line consumers
stay visually consistent across hosts):

| `Options.Branding` field | Style token replaced                |
|--------------------------|-------------------------------------|
| `Wordmark`               | Replaces the literal `core-tui`.    |
| `AccentColor`            | `BrandViolet`.                      |
| `SecondaryColor`         | `BrandPink`.                        |
| `CursorColor`            | `BrandPinkBright`.                  |
| `EmptyStateHint`         | Italic placeholder in empty chat.   |
| `FooterHint`             | Right-of-keymap idle hint.          |
| `InputPlaceholder`       | Greyed text inside the input box.   |

Glyphs, spacing, border policy, typography rules, and modal layout are
**not** overridable. A host that wants its own modal aesthetic builds
its own modal — not via `Options`.

---

## 9. What this doc deliberately leaves out

- **Specific Glamour styles** — covered by `R-MD-1` / `R-MD-4` and
  the markdown renderer's own theme files.
- **Animation timing** — covered by `R-CHAT-3` (spinner cadence) and
  per-feature requirements.
- **Component implementation** — covered by `design.md` §2.
- **Sound / bell** — out of scope for v1.

---

## 10. Compliance check (lint-able)

When implementation lands, the following are mechanically checkable
and should appear in `dev/tools/`:

- No `lipgloss.Color("#…")` literals outside `tui/style.go` (forces
  every color through the named tokens above).
- No `lipgloss.Border()` calls in chat-rendering files (`view.go`,
  `messages_history.go`, etc.) — borders are reserved for modals +
  input + sidebar divider, which live in their own files.
- No string literals in chat output that contain double-blank-line
  sequences (`\n\n\n`) — that's the per-turn rule's job.
- All anchor glyphs in §2 used in their fixed meaning only (a `✓`
  may appear inside Glamour-rendered markdown, but not as the head
  of a non-tool-call row).

These aren't enforceable until source exists; recorded here so the
first round of CI linters can pick them up.
