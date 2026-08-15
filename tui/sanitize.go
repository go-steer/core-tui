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

// Normalization and escaping for untrusted content on its way into
// the transcript (issues #107 and #108).
//
// Three classes of bytes reach the renderers from outside the
// operator's control — tool ARGS (the model writes them), tool
// RESPONSES / file content / bash stdout+stderr (the filesystem and
// the host write them), and tool ERROR text (anything in the stack
// can write it). None of it was normalized or escaped before this
// file existed:
//
//   - A file with CRLF endings diffs as "every line changed" against
//     an LF replacement string, and each rendered line keeps a
//     trailing CR *inside* a background-tinted span, which parks the
//     cursor at column 0 in the middle of the strip.
//   - A file containing raw escape sequences emits them straight at
//     the operator's terminal: `ESC[2J` clears the screen, `ESC]0;…`
//     retitles the window, cursor-position sequences let hostile
//     content draw UI chrome that appears to come from core-tui
//     itself.
//
// The two are one pass because they are one string. `sanitizeLine`
// is the funnel: every per-line renderer of untrusted content calls
// it, and it is the only place the escape policy lives.
//
// # The escape set
//
// Removed outright: every ANSI escape sequence `ansi.Strip`
// recognizes — CSI, OSC, DCS, SOS, PM, APC — in both their 7-bit
// (`ESC [`) and 8-bit (raw `0x9B`) introducer forms. Stripping
// rather than escaping is deliberate: SGR color in captured bash
// output is common and benign-looking, and rendering it as visible
// `\x1b[31m` litter would be worse than dropping it.
//
// Escaped to a visible `\xNN`: C0 controls (0x00–0x1F), DEL (0x7F),
// the C1 range (U+0080–U+009F) when it survives as a UTF-8 rune, and
// any byte that is not valid UTF-8. `\xNN` is chosen over the
// Control Pictures block (␛, ␈) because it is pure ASCII — it cannot
// itself need escaping, it survives a font that lacks the glyphs,
// and it is greppable.
//
// Exempt: TAB (0x09) and LF (0x0A).
//
//   - LF is the line separator. Escaping it would collapse every
//     multi-line body to one row.
//   - TAB is exempt because it is already handled and already
//     harmless. Styled render paths run through lipgloss, whose
//     `tabWidthDefault` is 4, so tabs are expanded to spaces before
//     they reach the terminal — the golden corpus contains zero tab
//     bytes despite tab-indented fixtures. Escaping tab as a control
//     character would churn several chroma goldens and give the
//     operator `\x09"fmt"` where they expect indented Go. What tab
//     *did* break is width accounting, which `truncateCells` fixes
//     rather than the escape set.
package tui

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// contentTabWidth is the number of terminal cells one TAB occupies
// in rendered content. It mirrors lipgloss's unexported
// `tabWidthDefault`, which is what actually performs the expansion
// on every styled path in this package — this constant exists so the
// byte-budget arithmetic in truncateCells agrees with the renderer
// instead of counting a tab as one cell.
//
// Do NOT use this to expand tabs yourself. lipgloss already does it,
// and a second expansion at a different width is how the two get out
// of sync.
const contentTabWidth = 4

// hexDigits backs the `\xNN` escape writer.
const hexDigits = "0123456789abcdef"

// normalizeNewlines collapses CRLF to LF.
//
// Called at the `strings.Split` sites that turn a payload into
// rendered rows, and before `udiff.Unified` — deliberately NOT
// inside sanitizeLine. The CR sits at end-of-line, and a per-line
// cap can slice the body before ever reaching it, so a funnel that
// runs after the split is the wrong place to catch it.
//
// A LONE CR — one not paired with LF — is not a line ending here;
// it is a cursor-to-column-0 control and is left for sanitizeContent
// to escape.
func normalizeNewlines(s string) string {
	if !strings.Contains(s, "\r\n") {
		return s
	}
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// sanitizeContent strips ANSI escape sequences and escapes the
// remaining control characters, per the escape set documented at the
// top of this file. TAB and LF pass through untouched.
//
// It does not cap length — callers that render one row want
// sanitizeLine; callers that render a scrollable full-text block
// (the tool-detail error banner) want this.
func sanitizeContent(s string) string {
	if s == "" {
		return s
	}
	s = ansi.Strip(s)
	if isPlainASCII(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '\n' || r == '\t':
			b.WriteByte(s[i])
			i += size
		case r == utf8.RuneError && size == 1:
			// Not valid UTF-8. Escaping the raw byte keeps a
			// truncated multi-byte rune (or a bare 8-bit C1
			// introducer that Strip did not consume) from reaching
			// the terminal as-is.
			writeHexEscape(&b, rune(s[i]))
			i++
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			writeHexEscape(&b, r)
			i += size
		default:
			b.WriteString(s[i : i+size])
			i += size
		}
	}
	return b.String()
}

// sanitizeLine is THE per-line funnel for untrusted content: strip
// and escape first, then bound the result to perLineByteCap cells.
//
// Escaping before capping is the correct order — the cap exists to
// bound what the terminal is asked to draw, and escaping is what
// decides how much that is (a `\x08` costs four cells, not one).
//
// Every renderer that puts a single row of model-, file- or
// host-derived text on screen goes through here. Adding a new one?
// Call this, not truncateCells, and not the raw string.
func sanitizeLine(s string) string {
	return truncateCells(sanitizeContent(s), perLineByteCap)
}

// truncateCells shortens s to at most maxCells rendered cells,
// appending GlyphTruncate when it had to trim.
//
// The budget is bytes, not runes: the cap exists to bound terminal
// damage from pathological payloads (minified JS, encoded blobs),
// and a byte budget is the one that actually holds against them. Two
// corrections to a naive `len(s)`:
//
//   - A TAB costs contentTabWidth, because that is what lipgloss
//     expands it to. Counting it as one byte let a line of tabs
//     render four times wider than the cap allows.
//   - The cut lands on a rune boundary. A mid-rune slice used to be
//     written off as "visually messy but not catastrophic"; now that
//     the funnel escapes invalid bytes, leaving a torn rune behind
//     would just mean emitting `\xNN` litter at every truncation
//     point instead.
func truncateCells(s string, maxCells int) string {
	if maxCells <= 0 {
		return s
	}
	// No `len(s) <= maxCells` shortcut: a line of tabs is under the
	// byte cap and four times over the cell cap, which is the exact
	// accounting bug this function exists to fix.
	cells := 0
	for i := 0; i < len(s); {
		_, size := utf8.DecodeRuneInString(s[i:])
		cost := size
		if s[i] == '\t' {
			cost = contentTabWidth
		}
		if cells+cost > maxCells {
			return s[:i] + GlyphTruncate
		}
		cells += cost
		i += size
	}
	return s
}

// isPlainASCII reports whether s is printable ASCII plus TAB and LF
// — i.e. whether sanitizeContent is the identity function on it.
// This is the common case (source files, command output, JSON) and
// the whole point of the check is that it allocates nothing.
func isPlainASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x20 && c < 0x7f {
			continue
		}
		if c == '\t' || c == '\n' {
			continue
		}
		return false
	}
	return true
}

// writeHexEscape appends r as a visible two-digit `\xNN`.
//
// Callers must pass a value that fits in a byte — every call site is
// inside a branch that has already established r <= 0x9F, or is
// passing a single raw byte. The nibbles are masked out of the rune
// directly rather than going through a byte conversion so the
// arithmetic carries its own bound.
func writeHexEscape(b *strings.Builder, r rune) {
	b.WriteString(`\x`)
	b.WriteByte(hexDigits[(r>>4)&0x0f])
	b.WriteByte(hexDigits[r&0x0f])
}
