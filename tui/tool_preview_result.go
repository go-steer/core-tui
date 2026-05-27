// Copyright 2026 The go-steer team
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Per-tool RESULT rendering (Task #8 — tool-result events).
// Renders the multi-line block that attaches under a tool row
// once the tool has completed, combining the call-time scope info
// from renderToolPreview with the result payload from the agent.
//
// Result rendering closes the gap Phase 1-3 of inline-tool-display
// couldn't: read_file shows the actual content the model received,
// bash shows stdout/stderr, write_file shows bytes written, errors
// render as a red row inline under the tool name.

package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// resultLineCap bounds how many lines of result content a single
// inline preview can carry. Matches the design's "default-render"
// threshold and the diff previewLineCap; result content can be a
// whole file's worth of bytes, so the cap is non-negotiable.
const resultLineCap = 8

// renderToolPreviewWithResult is the result-aware variant of
// renderToolPreview. Returns the call-time scope info on TOP and
// the rendered result UNDER it, joined with a newline. Either half
// may be empty independently — an unknown tool with a known result
// (or vice versa) still surfaces what we have.
//
// `err` non-empty short-circuits to a red error row under the
// existing call scope info; the structured response is ignored on
// failures.
func renderToolPreviewWithResult(name string, args, response map[string]any, err string, styles Styles) string {
	call := renderToolPreview(name, args, styles)
	result := renderToolResult(name, args, response, err, styles)
	switch {
	case call == "" && result == "":
		return ""
	case call == "":
		return result
	case result == "":
		return call
	default:
		return call + "\n" + result
	}
}

// renderToolResult routes the (name, args, response, err) tuple to
// the per-tool result builder. Returns "" when the tool isn't
// recognized or the response shape doesn't carry what the
// renderer needs.
//
// Errors short-circuit to a uniform red one-line summary so any
// failed tool — known or not — still surfaces the failure under
// its row.
func renderToolResult(name string, args, response map[string]any, err string, styles Styles) string {
	if err != "" {
		return renderResultError(err, styles)
	}
	if response == nil {
		return ""
	}
	switch name {
	case "read_file":
		return renderReadFileResult(args, response, styles)
	case "read_many_files":
		return renderReadManyFilesResult(response, styles)
	case "grep":
		return renderGrepResult(response, styles)
	case "glob":
		return renderGlobResult(response, styles)
	case "bash", "shell":
		return renderBashResult(response, styles)
	case "write_file":
		return renderWriteFileResult(response, styles)
	case "edit_file", "replace", "str_replace", "apply_patch", "patch":
		// Eager "⎿  +N -M" summary at call time already shows the
		// magnitude — a post-result "applied: …" footer would
		// duplicate it. Skip; the call→done visual flip is the
		// "result arrived" signal.
		return ""
	}
	return ""
}

// renderResultError formats a tool failure as a red error row
// under the tool name. Single line, truncated to the same byte
// cap a diff line uses so a multi-page panic trace doesn't blow
// up the preview area. Uses summaryIndent + "⎿ " so the row
// visually anchors to the call line above it, matching the
// read-preview and diff-summary conventions.
func renderResultError(err string, styles Styles) string {
	const errCap = 200
	errStyle := lipgloss.NewStyle().Foreground(styles.Theme.Error).Bold(true)
	text := strings.TrimSpace(err)
	if text == "" {
		text = "(failed)"
	}
	if len(text) > errCap {
		text = text[:errCap] + "…"
	}
	return styles.Muted.Render(summaryIndent) + errStyle.Render("✘ error: ") + styles.ErrorText.Render(text)
}

// renderReadFileResult shows the first resultLineCap lines of the
// content the tool returned, syntax-highlighted via the same
// per-line cache the diff renderer uses. Language is detected
// from the call-time `path` arg so the preview reads as code, not
// just text.
func renderReadFileResult(args, response map[string]any, styles Styles) string {
	content := stringArg(response, "content", "output", "text", "result")
	if content == "" {
		return ""
	}
	lang := ""
	if args != nil {
		lang = detectLang(stringArg(args, "path", "file", "filename"))
	}
	return renderCodeInline(content, styles, resultLineCap, lang)
}

// renderReadManyFilesResult shows a one-line summary of what came
// back. The structured shape varies across hosts — we look for an
// explicit count, then fall back to a comma-joined `files` slice
// when present, then to "n bytes" when the result only includes a
// total size.
func renderReadManyFilesResult(response map[string]any, styles Styles) string {
	const indent = "    "
	if count, ok := intArg(response, "count", "n"); ok && count > 0 {
		return indent + styles.Muted.Render(fmt.Sprintf("read %d file%s", count, plural(count)))
	}
	if files := stringSliceArg(response, "files", "paths"); len(files) > 0 {
		head := files
		if len(head) > 3 {
			head = head[:3]
		}
		body := fmt.Sprintf("read %d file%s: %s", len(files), plural(len(files)), strings.Join(head, ", "))
		if len(files) > 3 {
			body += fmt.Sprintf(", +%d more", len(files)-3)
		}
		return indent + styles.Muted.Render(body)
	}
	if bytes, ok := intArg(response, "bytes", "size"); ok && bytes > 0 {
		return indent + styles.Muted.Render(fmt.Sprintf("read %s", formatBytes(bytes)))
	}
	return ""
}

// renderGrepResult prefers the first few matches when the host
// returns them structured (`matches` slice). Falls back to a
// match-count one-liner otherwise.
func renderGrepResult(response map[string]any, styles Styles) string {
	const indent = "    "
	if matches := stringSliceArg(response, "matches", "lines", "results"); len(matches) > 0 {
		var b strings.Builder
		head := matches
		if len(head) > resultLineCap {
			head = head[:resultLineCap]
		}
		for i, m := range head {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(indent + styles.Muted.Render(truncateBytes(m)))
		}
		if len(matches) > resultLineCap {
			b.WriteString("\n" + indent + styles.Muted.Render(fmt.Sprintf("… +%d more matches", len(matches)-resultLineCap)))
		}
		return b.String()
	}
	if count, ok := intArg(response, "count", "matches_count"); ok {
		return indent + styles.Muted.Render(fmt.Sprintf("%d match%s", count, pluralES(count)))
	}
	return ""
}

// renderGlobResult shows the first few paths matched, or a
// "no matches" muted line when the result indicates none.
func renderGlobResult(response map[string]any, styles Styles) string {
	const indent = "    "
	paths := stringSliceArg(response, "paths", "files", "matches", "results")
	if len(paths) == 0 {
		if count, ok := intArg(response, "count"); ok && count == 0 {
			return indent + styles.Muted.Render("no matches")
		}
		return ""
	}
	head := paths
	if len(head) > resultLineCap {
		head = head[:resultLineCap]
	}
	var b strings.Builder
	for i, p := range head {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(indent + styles.Muted.Render(p))
	}
	if len(paths) > resultLineCap {
		b.WriteString("\n" + indent + styles.Muted.Render(fmt.Sprintf("… +%d more paths", len(paths)-resultLineCap)))
	}
	return b.String()
}

// renderBashResult shows the first lines of stdout (and, if non-
// empty, a single muted summary line for stderr). Exit code lands
// in a footer when non-zero — successful shells typically don't
// need to surface "exit 0" inline.
func renderBashResult(response map[string]any, styles Styles) string {
	const indent = "    "
	var b strings.Builder
	stdout := stringArg(response, "stdout", "output", "result")
	if stdout != "" {
		b.WriteString(renderCodeInline(stdout, styles, resultLineCap, ""))
	}
	stderr := stringArg(response, "stderr")
	if strings.TrimSpace(stderr) != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		errStyle := lipgloss.NewStyle().Foreground(styles.Theme.Warning)
		first := firstLine(stderr)
		b.WriteString(indent + errStyle.Render("stderr: ") + styles.Muted.Render(truncateBytes(first)))
	}
	if exit, ok := intArg(response, "exit_code", "exit", "returncode"); ok && exit != 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		warnStyle := lipgloss.NewStyle().Foreground(styles.Theme.Warning).Bold(true)
		b.WriteString(indent + warnStyle.Render(fmt.Sprintf("exit %d", exit)))
	}
	return b.String()
}

// renderWriteFileResult shows the number of bytes / lines the
// write reported. The TUI never reads the actual file content
// back from disk for this preview — we trust what the agent's
// tool reported.
func renderWriteFileResult(response map[string]any, styles Styles) string {
	const indent = "    "
	parts := []string{}
	if bytes, ok := intArg(response, "bytes_written", "bytes", "size"); ok && bytes > 0 {
		parts = append(parts, fmt.Sprintf("wrote %s", formatBytes(bytes)))
	}
	if lines, ok := intArg(response, "lines_written", "lines"); ok && lines > 0 {
		parts = append(parts, fmt.Sprintf("%d line%s", lines, plural(lines)))
	}
	if len(parts) == 0 {
		return ""
	}
	return indent + styles.Muted.Render(strings.Join(parts, " · "))
}

// renderCodeInline renders raw content (not a diff) as a capped,
// optionally syntax-highlighted block. Same indent + truncation
// behavior as renderDiffInline so result content lines up under
// the tool name visually.
//
// Unlike the diff renderer, there's no gutter, no +/- glyph, no
// bg tint — the content is informational, not a change.
func renderCodeInline(content string, styles Styles, maxLines int, lang string) string {
	if content == "" {
		return ""
	}
	const indent = "    "
	ctxStyle := lipgloss.NewStyle().Foreground(styles.Theme.FgMuted)
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	out := make([]string, 0, len(lines)+1)
	truncatedAt := -1
	for i, line := range lines {
		if maxLines > 0 && len(out) >= maxLines {
			truncatedAt = i
			break
		}
		body := truncateBytes(line)
		if lang != "" {
			body = highlightLine(body, lang, nil)
		} else {
			body = ctxStyle.Render(body)
		}
		out = append(out, indent+body)
	}
	if truncatedAt > 0 {
		remaining := len(lines) - truncatedAt
		out = append(out, indent+styles.Muted.Render(fmt.Sprintf("… +%d more lines", remaining)))
	}
	return strings.Join(out, "\n")
}

// formatBytes renders a byte count with a 1024-based suffix —
// 512 → "512B", 1500 → "1.5K", 1.5MB → "1.5M". Single-digit
// precision keeps the inline preview tight.
func formatBytes(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1fK", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/(1024*1024))
	}
}

// firstLine returns text up to the first newline, never the rest.
// Used to keep stderr summaries one row tall even when the
// payload itself is multi-line.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// plural returns "s" for n != 1, "" otherwise. Tiny helper to
// keep result messages grammatical without per-builder repetition.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// pluralES returns "es" for n != 1, "" otherwise — for words that
// pluralize with "es" (match → matches).
func pluralES(n int) string {
	if n == 1 {
		return ""
	}
	return "es"
}
