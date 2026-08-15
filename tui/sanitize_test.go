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

// Tests for the untrusted-content funnel (#107 CRLF + tab-width
// accounting, #108 control-character and ANSI escaping).
//
// The table in hostilePayloads is the contract. Every renderer that
// takes model-, file- or host-derived text is driven over it, and
// the assertion is the same for all of them: after rendering, no
// payload byte survives that could move the cursor, clear the
// screen, retitle the window, or repaint what came before.

package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// hostilePayload is one adversarial input plus the escape-set
// decision it pins down.
type hostilePayload struct {
	name string
	in   string
	// want is the exact sanitizeContent output. Spelling it out
	// rather than asserting "contains no escapes" is what makes the
	// TAB and LF exemptions reviewable.
	want string
	why  string
}

var hostilePayloads = []hostilePayload{
	{
		name: "crlf",
		in:   "line one\r\nline two",
		// sanitizeContent does NOT collapse CRLF — that happens at
		// the split sites, before the per-line cap can slice the CR
		// off the end of a long line. Left alone here, the CR is
		// just another control character and gets escaped.
		want: "line one\\x0d\nline two",
		why:  "CR is escaped, not silently dropped, when normalization has not run",
	},
	{
		name: "lone_cr",
		in:   "before\rafter",
		want: "before\\x0dafter",
		why:  "a bare CR parks the cursor at column 0 mid-row",
	},
	{
		name: "csi_clear_screen",
		in:   "a\x1b[2Jb",
		want: "ab",
		why:  "ESC[2J erases the display",
	},
	{
		name: "csi_cursor_home",
		in:   "a\x1b[1;1Hb",
		want: "ab",
		why:  "cursor positioning lets content draw outside its own row",
	},
	{
		name: "osc_set_title",
		in:   "a\x1b]0;pwned\x07b",
		want: "ab",
		why:  "OSC 0 retitles the operator's terminal window",
	},
	{
		name: "osc_st_terminated",
		in:   "a\x1b]0;pwned\x1b\\b",
		want: "ab",
		why:  "the ST-terminated OSC form must strip too, not just the BEL form",
	},
	{
		name: "sgr_color",
		in:   "a\x1b[31mred\x1b[0mb",
		want: "aredb",
		why:  "SGR is stripped, not escaped — colored bash output is common and its text is what matters",
	},
	{
		name: "backspace",
		in:   "a\x08b",
		want: "a\\x08b",
		why:  "BS rewrites the character to its left, which forges text",
	},
	{
		name: "nul",
		in:   "a\x00b",
		want: "a\\x00b",
		why:  "NUL is a C0 control like any other",
	},
	{
		name: "del",
		in:   "a\x7fb",
		want: "a\\x7fb",
		why:  "DEL sits outside the C0 block and is missed by a naive < 0x20 test",
	},
	{
		name: "bare_esc",
		in:   "a\x1bb",
		// ansi.Strip reads ESC + 'b' as a two-byte Fe escape and eats
		// both. Losing the trailing 'b' is cosmetic damage to an
		// already-malformed payload; what matters is that no ESC
		// reaches the terminal.
		want: "a",
		why:  "an ESC must not reach the terminal whether or not it introduced a valid sequence",
	},
	{
		name: "trailing_esc",
		in:   "ab\x1b",
		want: "ab",
		why:  "ansi.Strip removes an ESC unconditionally, including one with nothing after it",
	},
	{
		name: "c1_csi_rune",
		in:   "a\u009b31mb",
		want: "a\\x9b31mb",
		why:  "U+009B is the 8-bit CSI; a UTF-8 terminal decodes it as one",
	},
	{
		name: "invalid_utf8",
		in:   "a\xffb",
		// Strip drops 0xFF outright. The escape branch for
		// undecodable bytes is still load-bearing \u2014 see
		// TestSanitizeContent_TruncatedRune.
		want: "ab",
		why:  "an undecodable byte never reaches the terminal",
	},
	{
		name: "tab_exempt",
		in:   "\tindented",
		want: "\tindented",
		why:  "TAB is EXEMPT — lipgloss expands it at width 4 and the golden corpus holds zero tab bytes",
	},
	{
		name: "newline_exempt",
		in:   "one\ntwo",
		want: "one\ntwo",
		why:  "LF is the line separator; escaping it would collapse every multi-line body",
	},
	{
		name: "plain_ascii",
		in:   `func main() {` + "\n\t" + `fmt.Println("hi")`,
		want: `func main() {` + "\n\t" + `fmt.Println("hi")`,
		why:  "the identity case — this is why the golden corpus does not churn",
	},
}

func TestSanitizeContent_HostilePayloads(t *testing.T) {
	for _, tc := range hostilePayloads {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeContent(tc.in)
			if got != tc.want {
				t.Errorf("sanitizeContent(%q) = %q, want %q\n(%s)", tc.in, got, tc.want, tc.why)
			}
		})
	}
}

// TestSanitizeContent_Idempotent guards the double-application risk:
// two renderers in the same call chain (or a future third funnel)
// must not turn `\x08` into `\\x08`.
func TestSanitizeContent_Idempotent(t *testing.T) {
	for _, tc := range hostilePayloads {
		t.Run(tc.name, func(t *testing.T) {
			once := sanitizeContent(tc.in)
			if twice := sanitizeContent(once); twice != once {
				t.Errorf("sanitizeContent not idempotent for %q: %q then %q", tc.in, once, twice)
			}
		})
	}
}

// TestSanitizeContent_RawByteC1 covers the 8-bit CSI introducer as a
// raw byte rather than a UTF-8 rune. ansi.Strip consumes it as the
// start of a real sequence, which is the outcome we want; the test
// exists so a Strip behavior change surfaces here rather than as a
// live escape.
func TestSanitizeContent_RawByteC1(t *testing.T) {
	got := sanitizeContent("a\x9b31mb")
	if strings.ContainsRune(got, 0x9b) || strings.Contains(got, "\u009b") {
		t.Errorf("raw 8-bit CSI survived sanitization: %q", got)
	}
}

// TestSanitizeContent_TruncatedRune exercises the undecodable-byte
// branch with an input ansi.Strip leaves alone: a lead byte with its
// continuation bytes cut off, which is what a byte-sliced UTF-8
// payload looks like.
func TestSanitizeContent_TruncatedRune(t *testing.T) {
	got := sanitizeContent("caf\xc3")
	if got != "caf\\xc3" {
		t.Errorf("sanitizeContent(truncated rune) = %q, want %q", got, "caf\\xc3")
	}
	if !utf8.ValidString(got) {
		t.Errorf("sanitized output is not valid UTF-8: %q", got)
	}
}

func TestNormalizeNewlines(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a\r\nb", "a\nb"},
		{"a\r\nb\r\n", "a\nb\n"},
		{"a\nb", "a\nb"},
		{"a\rb", "a\rb"},       // lone CR is not a line ending
		{"a\n\r\nb", "a\n\nb"}, // blank CRLF line
		{"", ""},
	}
	for _, tc := range cases {
		if got := normalizeNewlines(tc.in); got != tc.want {
			t.Errorf("normalizeNewlines(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestTruncateCells_TabAccounting is the #107 tab half: the cap is
// cells, not bytes, and a TAB costs contentTabWidth of them. A
// 300-tab line is well under a 200-BYTE cap and four times over a
// 200-CELL one.
func TestTruncateCells_TabAccounting(t *testing.T) {
	line := strings.Repeat("\t", 300)
	got := sanitizeLine(line)

	wantTabs := perLineByteCap / contentTabWidth
	want := strings.Repeat("\t", wantTabs) + GlyphTruncate
	if got != want {
		t.Fatalf("sanitizeLine(300 tabs) kept %d tabs, want %d", strings.Count(got, "\t"), wantTabs)
	}

	cells := strings.Count(got, "\t") * contentTabWidth
	if cells > perLineByteCap {
		t.Errorf("truncated line still costs %d cells, cap is %d", cells, perLineByteCap)
	}
}

// TestTruncateCells_ASCIIUnchanged pins the pre-existing behavior for
// the tabless case: the cap must land on exactly the same byte it
// used to, or every long-line golden moves.
func TestTruncateCells_ASCIIUnchanged(t *testing.T) {
	atCap := strings.Repeat("x", perLineByteCap)
	if got := sanitizeLine(atCap); got != atCap {
		t.Errorf("a line exactly at the cap was truncated: len %d", len(got))
	}
	overCap := strings.Repeat("x", perLineByteCap+1)
	if got, want := sanitizeLine(overCap), atCap+GlyphTruncate; got != want {
		t.Errorf("cap moved: got %d x's, want %d", strings.Count(got, "x"), perLineByteCap)
	}
}

// TestTruncateCells_RuneBoundary checks the cut never lands inside a
// multi-byte rune — otherwise truncation would itself manufacture
// the invalid bytes the escape pass exists to catch.
func TestTruncateCells_RuneBoundary(t *testing.T) {
	// 100 three-byte runes = 300 bytes; the cap falls mid-rune if
	// the budget is spent byte-wise.
	got := truncateCells(strings.Repeat("あ", 100), perLineByteCap)
	body := strings.TrimSuffix(got, GlyphTruncate)
	if len(body)%3 != 0 {
		t.Errorf("cut landed mid-rune: %d bytes of body", len(body))
	}
	if strings.ContainsRune(body, '�') {
		t.Errorf("truncation produced a replacement char: %q", body)
	}
}

// TestComputeUnifiedDiff_CRLF is the #107 headline bug: a CRLF file
// against an LF replacement string is not "every line changed".
func TestComputeUnifiedDiff_CRLF(t *testing.T) {
	crlf := "package main\r\n\r\nfunc main() {}\r\n"
	lf := "package main\n\nfunc main() {}\n"

	if got := computeUnifiedDiff("main.go", crlf, lf); got != "" {
		t.Errorf("CRLF vs LF of identical content produced a diff:\n%s", got)
	}

	// A real one-line edit on a CRLF file must still diff as one line.
	edited := "package main\r\n\r\nfunc main() { println(1) }\r\n"
	diff := computeUnifiedDiff("main.go", crlf, edited)
	added, removed := countDiffStats(diff)
	if added != 1 || removed != 1 {
		t.Errorf("one-line edit on a CRLF file diffed as +%d -%d, want +1 -1:\n%s", added, removed, diff)
	}
	if strings.Contains(diff, "\r") {
		t.Errorf("diff carries a CR: %q", diff)
	}
}

// TestRenderDiffInline_CRLFNoStrayCR renders a CRLF-carrying diff and
// checks no CR reaches the styled span. The CR mattered here more
// than anywhere else: it sits INSIDE the background-tinted region, so
// the cursor jumps to column 0 with the tint still active.
func TestRenderDiffInline_CRLFNoStrayCR(t *testing.T) {
	pinChromaStyle(t)
	diff := "--- a/x.go\r\n+++ b/x.go\r\n@@ -1,1 +1,1 @@\r\n-old line\r\n+new line\r\n"
	got := renderDiffInline(diff, goldenStyles(), 0, "Go")
	if strings.Contains(got, "\r") {
		t.Errorf("rendered diff carries a raw CR: %q", got)
	}
	if strings.Contains(got, `\x0d`) {
		t.Errorf("rendered diff escaped a CR that normalization should have removed: %q", got)
	}
}

// --- renderer-level assertions ---------------------------------

// dangerousLiterals are byte sequences that must never appear in
// rendered output regardless of what the payload contained. Checked
// against the raw bytes, before any stripping, because the whole
// point is what the terminal receives.
var dangerousLiterals = []struct{ name, seq string }{
	{"erase display", "\x1b[2J"},
	{"erase line", "\x1b[2K"},
	{"cursor home", "\x1b[1;1H"},
	{"cursor home short", "\x1b[H"},
	{"OSC introducer", "\x1b]"},
	{"DCS introducer", "\x1bP"},
	{"8-bit CSI", "\x9b"},
	{"carriage return", "\r"},
	{"backspace", "\x08"},
	{"NUL", "\x00"},
	{"DEL", "\x7f"},
}

// assertRenderSafe is the shared assertion for every renderer driven
// over the hostile table. Two prongs, because either alone has a
// blind spot:
//
//   - The raw output must contain none of dangerousLiterals. This
//     catches a payload sequence that survived intact.
//   - With ALL escape sequences stripped — lipgloss's own styling
//     included — nothing but printable text, TAB and LF may remain.
//     This catches lone control characters, which the first prong
//     only spot-checks.
func assertRenderSafe(t *testing.T, what, got string) {
	t.Helper()
	for _, d := range dangerousLiterals {
		if strings.Contains(got, d.seq) {
			t.Errorf("%s: rendered output contains %s (%q): %q", what, d.name, d.seq, got)
		}
	}
	for i, r := range ansi.Strip(got) {
		if r == '\t' || r == '\n' {
			continue
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			t.Errorf("%s: control character %#U survived at offset %d in %q", what, r, i, got)
		}
	}
}

// TestRenderersEscapeHostileContent drives every untrusted-content
// entry point over the hostile table. These are the eight paths
// #108 enumerates: diff bodies, code/result bodies, grep matches,
// glob paths, bash stderr, the compact error row, the tool-detail
// blocks, and the rune-based arg hint.
func TestRenderersEscapeHostileContent(t *testing.T) {
	pinChromaStyle(t)
	s := goldenStyles()

	for _, tc := range hostilePayloads {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.in

			assertRenderSafe(t, "renderDiffInline",
				renderDiffInline("--- a/f\n+++ b/f\n@@ -1,1 +1,1 @@\n-"+p+"\n+"+p+"\n "+p+"\n", s, 0, "Go"))
			assertRenderSafe(t, "renderDiffInline/nolang",
				renderDiffInline("@@ -1,1 +1,1 @@\n+"+p+"\n", s, 0, ""))
			assertRenderSafe(t, "renderCodeInline", renderCodeInline(p, s, 0, "Go"))
			assertRenderSafe(t, "renderCodeInline/nolang", renderCodeInline(p, s, 0, ""))
			assertRenderSafe(t, "renderResultError", renderResultError(p, s))
			assertRenderSafe(t, "trimToolArg", trimToolArg(p, 80))

			assertRenderSafe(t, "read_file result", renderToolPreviewWithResult(
				"read_file", map[string]any{"path": "x.go"}, map[string]any{"content": p}, "", s))
			assertRenderSafe(t, "bash result", renderToolPreviewWithResult(
				"bash", map[string]any{"command": p}, map[string]any{"stdout": p, "stderr": p}, "", s))
			assertRenderSafe(t, "bash error", renderToolPreviewWithResult(
				"bash", map[string]any{"command": p}, nil, p, s))
			assertRenderSafe(t, "grep result", renderToolPreviewWithResult(
				"grep", map[string]any{"pattern": p, "path": p}, map[string]any{"matches": []any{p, p}}, "", s))
			assertRenderSafe(t, "glob result", renderToolPreviewWithResult(
				"glob", map[string]any{"pattern": p}, map[string]any{"paths": []any{p}}, "", s))
			assertRenderSafe(t, "read_many_files result", renderToolPreviewWithResult(
				"read_many_files", map[string]any{"paths": []any{p}}, map[string]any{"files": []any{p}}, "", s))
			assertRenderSafe(t, "edit_file preview", renderToolPreview(
				"edit_file", map[string]any{"path": "x.go", "old_string": p, "new_string": p + "!"}, s))
			assertRenderSafe(t, "apply_patch preview", renderToolPreview(
				"apply_patch", map[string]any{"path": "x.go", "patch": "@@ -1,1 +1,1 @@\n+" + p + "\n"}, s))

			assertRenderSafe(t, "renderToolDetail",
				renderToolDetail(map[string]any{"arg": p}, map[string]any{"out": p}, "", s))
			assertRenderSafe(t, "renderToolDetail/error",
				renderToolDetail(map[string]any{"arg": p}, nil, p, s))
		})
	}
}

// TestSanitizeLine_BoundsExpandedEscapes checks the cap is applied
// AFTER escaping, not before: 200 backspaces expand to 800 cells of
// `\x08`, and the row must still fit the cap.
func TestSanitizeLine_BoundsExpandedEscapes(t *testing.T) {
	got := sanitizeLine(strings.Repeat("\x08", perLineByteCap))
	if n := len(strings.TrimSuffix(got, GlyphTruncate)); n > perLineByteCap {
		t.Errorf("escaped output is %d cells, cap is %d", n, perLineByteCap)
	}
	if !strings.HasSuffix(got, GlyphTruncate) {
		t.Errorf("expected a truncation marker, got %q", got)
	}
}
