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

// Unified-diff rendering for the inline tool-display surface
// (docs/inline-tool-display-design.md §3). Phase 3 visual upgrade:
// + / - lines now get a dim BG tint (DiffAddBg / DiffDelBg) so a
// changed region scans like `git diff --color` does, and a muted
// line-number gutter prefixes every line. Long lines truncate at
// perLineByteCap with a "…" marker so a minified-JS edit doesn't
// blow up the inline area.

package tui

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"

	udiff "github.com/aymanbagabas/go-udiff"

	"charm.land/lipgloss/v2"
)

// perLineByteCap bounds how many bytes of one diff-line body are
// inlined before we truncate with "…". Matches the design's open
// Q #4 — line counts alone can't stop a 10K-character minified
// line from blowing up the preview pane.
const perLineByteCap = 200

// summaryIndent is the 4-column prefix used on single-line tool
// summary rows that attach under a tool call (read scope info,
// diff totals, error rows). The "⎿" box-drawing glyph reads as
// a tree-branch hint that "this row belongs to the call above" —
// inspired by Claude Code / Anthropic's CLI display.
//
// Width arithmetic: 2 spaces + 1-cell "⎿" + 1 space = 4 cols, so
// content after the prefix aligns with the 4-space body indent
// used by diff lines below.
const summaryIndent = "  ⎿ "

// countDiffStats walks a unified diff and counts added vs removed
// content lines. File headers (--- / +++) and hunk headers (@@) are
// skipped — only the +/- prefixed BODY lines count. Used by
// tool_preview to surface eager totals on the call line before
// the result event arrives.
func countDiffStats(diff string) (added, removed int) {
	if diff == "" {
		return 0, 0
	}
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++"):
			// file header — ignore
		case strings.HasPrefix(line, "---"):
			// file header — ignore
		case strings.HasPrefix(line, "+"):
			added++
		case strings.HasPrefix(line, "-"):
			removed++
		}
	}
	return added, removed
}

// computeUnifiedDiff returns a unified-diff string between old and
// new content with the given label as both from/to filename. Used
// by edit_file / replace tools whose args carry old_text + new_text
// but not the pre-computed diff.
func computeUnifiedDiff(label, oldText, newText string) string {
	if oldText == newText {
		return ""
	}
	return udiff.Unified(label, label, oldText, newText)
}

// renderDiffInline styles a unified-diff string for inline display
// under a tool row. Every line carries a 4-space indent + a
// 5-char right-aligned line-number gutter + the styled body so
// blocks visually attach to the tool name above them.
//
// + / - lines render with a dim Success / Error background, the
// glyph in bold Success / Error fg, and the body either through
// the per-line syntax cache (when `lang` is non-empty) or as flat
// colored text. Bodies longer than perLineByteCap truncate with
// "…" so pathological minified lines stay tame.
//
// `maxLines` caps the rendered output; lines beyond the cap are
// dropped and replaced with a "… +N more" marker. Pass 0 for
// no cap.
func renderDiffInline(diff string, styles Styles, maxLines int, lang string) string {
	if diff == "" {
		return ""
	}
	addBg := styles.Theme.DiffAddBg
	delBg := styles.Theme.DiffDelBg
	addPrefixStyle := lipgloss.NewStyle().Foreground(styles.Theme.Success).Background(addBg).Bold(true)
	delPrefixStyle := lipgloss.NewStyle().Foreground(styles.Theme.Error).Background(delBg).Bold(true)
	addBodyFallback := lipgloss.NewStyle().Foreground(styles.Theme.Success).Background(addBg)
	delBodyFallback := lipgloss.NewStyle().Foreground(styles.Theme.Error).Background(delBg)
	hunkStyle := lipgloss.NewStyle().Foreground(styles.Theme.FgMuted).Italic(true)
	ctxStyle := lipgloss.NewStyle().Foreground(styles.Theme.FgMuted)
	gutterStyle := lipgloss.NewStyle().Foreground(styles.Theme.FgSubtle)
	addGutterStyle := lipgloss.NewStyle().Foreground(styles.Theme.FgSubtle).Background(styles.Theme.DiffAddGutterBg)
	delGutterStyle := lipgloss.NewStyle().Foreground(styles.Theme.FgSubtle).Background(styles.Theme.DiffDelGutterBg)

	const indent = "    "
	const emptyGutter = "       " // 7 spaces = " NNNN │ " width
	lines := strings.Split(strings.TrimRight(diff, "\n"), "\n")
	out := make([]string, 0, len(lines)+1)
	truncatedAt := -1

	oldNo, newNo := 0, 0

	for i, line := range lines {
		// Skip the "--- old\n+++ new" file headers — they
		// duplicate the tool's path arg and read as visual
		// noise inline.
		if strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
			continue
		}
		if maxLines > 0 && len(out) >= maxLines {
			truncatedAt = i
			break
		}
		switch {
		case strings.HasPrefix(line, "@@"):
			if o, n, ok := parseHunkHeader(line); ok {
				oldNo, newNo = o, n
			}
			out = append(out, indent+gutterStyle.Render(emptyGutter)+hunkStyle.Render(line))
		case strings.HasPrefix(line, "+"):
			body := truncateBytes(line[1:])
			rendered := highlightOrFlat(body, lang, addBg, addBodyFallback)
			gutter := formatGutter(newNo)
			out = append(out, indent+addGutterStyle.Render(gutter)+addPrefixStyle.Render("+")+rendered)
			newNo++
		case strings.HasPrefix(line, "-"):
			body := truncateBytes(line[1:])
			rendered := highlightOrFlat(body, lang, delBg, delBodyFallback)
			gutter := formatGutter(oldNo)
			out = append(out, indent+delGutterStyle.Render(gutter)+delPrefixStyle.Render("-")+rendered)
			oldNo++
		default:
			body := truncateBytes(line)
			gutter := formatGutter(newNo)
			out = append(out, indent+gutterStyle.Render(gutter)+ctxStyle.Render(body))
			oldNo++
			newNo++
		}
	}
	if truncatedAt > 0 {
		remaining := len(lines) - truncatedAt
		out = append(out, indent+styles.Muted.Render("… +"+itoa(remaining)+" more lines · ctrl+o to expand (todo)"))
	}
	return strings.Join(out, "\n")
}

// highlightOrFlat returns the body either syntax-highlighted (with
// bg threaded through chroma so the tint stays continuous across
// tokens) or as a flat color-on-bg render. The two paths produce
// equivalent bg behavior; only fg differs.
func highlightOrFlat(body, lang string, bg color.Color, fallback lipgloss.Style) string {
	if lang == "" {
		return fallback.Render(body)
	}
	return highlightLine(body, lang, bg)
}

// truncateBytes shortens s to at most perLineByteCap bytes,
// appending "…" when it had to trim. Operates on raw bytes (not
// runes) because the cap exists to bound terminal damage from
// pathological payloads (minified JS, encoded blobs) — a multi-
// byte boundary split would be visually messy but not
// catastrophic, and the cap should still hold.
func truncateBytes(s string) string {
	if len(s) <= perLineByteCap {
		return s
	}
	return s[:perLineByteCap] + "…"
}

// formatGutter renders the 5-digit right-aligned line-number
// followed by " │ " (a vertical bar separator). 7 chars total so
// continuation indentation lines up.
func formatGutter(n int) string {
	return fmt.Sprintf("%5d │ ", n)
}

// parseHunkHeader pulls the starting old / new line numbers out of
// a unified-diff hunk header. Returns ok=false for malformed
// headers so the caller leaves the counters untouched.
//
// Format: `@@ -<oldStart>[,<oldCount>] +<newStart>[,<newCount>] @@`
func parseHunkHeader(line string) (oldStart, newStart int, ok bool) {
	for _, f := range strings.Fields(line) {
		switch {
		case strings.HasPrefix(f, "-") && len(f) > 1:
			n, parseOk := parseStartNum(f[1:])
			if parseOk {
				oldStart = n
			}
		case strings.HasPrefix(f, "+") && len(f) > 1:
			n, parseOk := parseStartNum(f[1:])
			if parseOk {
				newStart = n
				ok = true
			}
		}
	}
	return
}

// parseStartNum returns the first number from a "<start>[,<count>]"
// pair, or (0, false) on parse failure.
func parseStartNum(s string) (int, bool) {
	if i := strings.Index(s, ","); i >= 0 {
		s = s[:i]
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// itoa is a tiny non-strconv helper kept for callers that already
// import this package's namespace. ASCII-only, non-negative
// integers (line counts).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
