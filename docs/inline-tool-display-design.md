# Inline Tool Display (Edits + Reads)

**Status:** design — not yet implemented. Supersedes the previously-parked
"Diff viewer + syntax cache" task (`#4`) by widening scope to cover
file-read tools as well.

**Goal:** make the operator's mental model of "what the agent just did to
my filesystem" visible in the chat flow itself, instead of forcing them
to switch context (open an editor, re-grep, scroll output panes).

---

## 1. Problem

The agent fires file-touching tools all the time:

- `edit_file` / `replace` — modify an existing file
- `write_file` — overwrite or create a file
- `apply_patch` / `patch` — apply a unified diff
- `read_file` / `read_many_files` — read content into the model's context
- `grep` / `glob` — search the filesystem

The TUI currently renders every one of these as a single inert row:

```
› edit_file · src/foo.go
› write_file · cmd/main.go
› read_file · README.md
› grep · "TODO" in lib/
```

The operator sees "a tool ran" but **not what the tool actually did**. For
edits this is the difference between "trust the agent" and "verify with
`git diff`". For reads it's the difference between "I know what
context the model was given" and "what did it just slurp?". Either way
the chat surface is selling itself short — it's renderable real estate
that's currently going to waste.

## 2. Design principles

1. **Inline, not modal.** The display belongs in the chat flow under
   the tool row that triggered it. Modal overlays interrupt; inline
   reads as part of the conversation.
2. **Tiered density.** Default to a compact, tasteful summary. Let the
   operator expand to full content when they want.
3. **Same pattern across reads + edits.** The operator shouldn't have
   to learn two visual languages. Both are "the tool did X to file Y,
   here's a preview of X".
4. **Computed from tool args.** No new event types from the agent; we
   already have everything we need at `applyToolCall` time.
5. **Cached.** Diff parsing + syntax highlighting is expensive; rides
   the existing `listCache` infrastructure.

## 3. Per-tool display

### 3.1 `apply_patch` / `patch`

The patch arg IS the unified diff. Parse + render directly.

```
› apply_patch · src/handler.go
    @@ -42,7 +42,9 @@
    -    return nil
    -  }
    +    return nil
    +  }
    +  if err := validate(req); err != nil {
    +    return fmt.Errorf("validate: %w", err)
    +  }
```

### 3.2 `edit_file` / `replace`

Args carry `old_text` + `new_text` (or `old_string` / `new_string`).
Compute unified diff between them, render same as `apply_patch`.

### 3.3 `write_file`

Args carry full `content`; old content needs filesystem read for the
diff. Two cases:

- File exists → diff vs new content
- File doesn't exist → render as "new file, N lines, K bytes" + first
  few lines as a preview block (no `+` prefixes since there's nothing
  to compare to)

### 3.4 `read_file`

Args carry `path` and optionally `start_line` / `end_line` or `offset` /
`limit`. We don't have the *content* the model received (the result
event isn't piped to the TUI), but we can show:

```
› read_file · README.md
    L1-L42 · 1.4K · md
```

That's `<lines-range> · <bytes> · <detected-language>`. Operator sees
the scope of the read at a glance.

For an unfiltered full-file read:

```
› read_file · cmd/main.go
    full · 184 lines · 5.2K · go
```

### 3.5 `read_many_files`

Args carry `paths` (list) or `pattern`. Show a folded count + the first
3 paths:

```
› read_many_files · pattern: "*.go"
    7 files · src/handler.go, src/store.go, src/router.go, +4 more
```

### 3.6 `grep` / `glob`

We don't see results (no result event); show the search scope:

```
› grep · "TODO" in lib/
    pattern: "TODO" · path: lib/ · case-insensitive
```

Result counts would require the result event — out of scope for v1.

### 3.7 Everything else

Unknown / MCP / skill tools fall through to the existing
`genericToolRenderer` (one-line `name · arg-hint`). No diff, no
preview — we don't know how to interpret them.

## 4. Data model

```go
// history.go
type Message struct {
    ...
    ToolName    string
    ToolArgs    string  // existing single-line hint
    ToolPreview string  // new — multi-line, pre-rendered
                        // (diff for edits, summary for reads)
}
```

`ToolPreview` is computed once at `applyToolCall` time:

```go
// update.go
func (m *Model) applyToolCall(msg toolCallMsg) {
    ...
    preview, err := renderToolPreview(msg.name, msg.args, m.styles)
    if err != nil {
        // Silent skip — preview is a nice-to-have, never block the
        // tool row from rendering.
        preview = ""
    }
    m.history.Append(Message{
        Role:        RoleTool,
        ToolName:    msg.name,
        ToolArgs:    hint,
        ToolPreview: preview,
    })
}
```

## 5. Rendering: tiered density

Threshold (tentative): **8 lines visible by default**.

| Preview length | Default render | Operator escape hatch |
|---|---|---|
| 0-8 lines | Full preview inline | n/a |
| 9-50 lines | First 6 lines + `… +N lines · tab to expand` | Tab on focused row (needs message-cursor; v1 = no expand, just truncate) |
| 50+ lines | First 6 lines + `… +N lines · large preview suppressed` | `/diff <message-id>` slash command to dump in a system message (v2) |

Each preview line is prefixed with the standard 4-space indent (matches
the existing `wordWrapIndent` continuation for tool rows) so the
preview visually attaches to the tool name above it.

## 6. New files

| File | Contents |
|---|---|
| `tui/diff.go` | `parseUnifiedDiff`, `computeUnifiedDiff(old, new)`, `renderDiffInline(diff, styles)`, language detection from extension |
| `tui/tool_preview.go` | `renderToolPreview(name, args, styles)` factory — routes to per-tool builders below |
| `tui/syntax_cache.go` | `xxh3`-keyed Chroma highlight cache (skill §7/§13) — used by `renderDiffInline` and `renderReadPreview` |
| `tui/tool_preview_read.go` | `renderReadPreview(args, styles)` for `read_file` / `read_many_files` / `grep` / `glob` |

## 7. Changes to existing files

| File | Change |
|---|---|
| `history.go` | `Message.ToolPreview string` field |
| `toolrender.go` | `genericToolRenderer.RenderCall` (and the bash / file variants) append `msg.ToolPreview` under the tool row when set, with 4-space indent |
| `update.go applyToolCall` | Invoke `renderToolPreview` after computing `hint`; stash on the appended `Message` |
| `view.go renderPermissionDetail` | When DetailKind is Diff and we have a parsed diff, use the new `renderDiffInline` for consistency with the inline preview (and to pick up the syntax cache) |
| `style.go` | New tokens `DiffAdd` / `DiffDel` / `DiffHunk` derived from `Theme.Success` / `Theme.Error` / `Theme.Muted` |

## 8. Performance

Per the agentic-tui skill §13:

- **Per-line syntax highlight cache** keyed on
  `xxh3(line + bg_color + lang)`. Chroma is 10-50ms per pane; the cache
  reduces redraws + scrolls to a map lookup.
- **listCache invalidation:** `ToolPreview` is computed at `applyToolCall`
  time, never mutated after. The cache caches the rendered Message; no
  extra invalidation needed.
- **Bounded preview size:** the 50-line truncation prevents a 10K-line
  diff from killing the renderer on every redraw.

## 9. Capability extensions (future)

Add to `tui/capabilities.go` (already declared in skill §4):

```go
// Expandable lets a Message toggle a collapsed/expanded preview.
// Requires a "focused message" cursor concept in the viewport,
// which doesn't exist yet — deferred to v2.
type Expandable interface {
    Expand()
    Collapse()
    Expanded() bool
}
```

When implemented, `Tab` on the focused message expands the preview.
For v1 we skip this and use the 6/50-line truncation thresholds.

## 10. Phasing

Three commits (~400 lines total):

**Phase 1 — diffs only (~200 lines)**
- `diff.go` (parse + compute + render inline)
- `Message.ToolPreview` field
- `toolrender.go` extension to render preview under the tool row
- `applyToolCall` populates `ToolPreview` for `apply_patch` / `edit_file`
- Plain colors (`Success` / `Error` / `Muted`); no syntax highlighting yet

**Phase 2 — reads + syntax cache (~150 lines)**
- `tool_preview_read.go` (read_file / read_many_files / grep / glob)
- `syntax_cache.go` (`xxh3`-keyed Chroma)
- `renderDiffInline` picks up syntax highlighting from the cache

**Phase 3 — polish (~50 lines)**
- `write_file` against disk (read existing file, diff)
- Permission modal integration (DetailKind=DetailDiff goes through `renderDiffInline`)
- 50-line truncation + "large preview suppressed" message

## 11. Out of scope

- Side-by-side split view (skill §7 `splitLine` mapping) — defer until
  there's a dedicated `/diff` review surface that needs the screen real
  estate.
- Tool result rendering generally — agent events don't deliver tool
  RESULTS as separate events today; preview-on-call (what THIS design
  handles) is the visible win without that plumbing.
- Mouse interaction (click to expand) — keyboard Tab is enough for v2.
- Streaming preview (showing the diff as the agent's tokens build it
  up) — adds substantial complexity for marginal gain.
- `/diff <message-id>` slash for dumping a large preview as a system
  message — nice-to-have; can land in v3.

## 12. Open questions

1. **`write_file` filesystem read** — operator may not want the TUI
   reading from disk for every `write_file` call (perf, surprise,
   permission). Should we gate this behind a config flag
   (`Options.DiskReadOnEditPreview`)? Default off?
2. **Sub-agent file operations** — a spawned sub-agent's tool calls
   are rendered with `from sub-agent: name` in the permission modal,
   but inline tool rows don't currently carry that signal. Preview
   inherits whatever the tool row shows.
3. **Per-language Chroma resolution** — file extension is the cheapest
   signal but unreliable for files without one. Should we fall back
   to `lexers.Analyse(content)` for diffs that include enough source
   to detect? Cheap to add later.
4. **Cap by line count or byte count?** 50 lines might still be a
   massive byte payload (long lines). Probably want both: cap at
   `min(50 lines, 8 KiB)`.

---

## Decision log

- 2026-05-26 — design captured; not yet implemented. Referenced by
  Task #4 (renamed scope) in the agent's task list. Pickup signal:
  user explicitly requests inline diffs OR the doc-refresh task
  triggers a re-prioritization.
