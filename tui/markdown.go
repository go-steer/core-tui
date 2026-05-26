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

package tui

import (
	"strings"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
)

// markdownRenderer wraps a Glamour TermRenderer with the parameters
// the TUI tracks — dark/light background and viewport width. Held by
// Model and lazily rebuilt when either changes.
//
// R-CHAT-4 / R-MD-3: assistant text is rendered through Glamour on
// every update (including mid-stream partials). When a render fails
// — typically because the accumulated stream ends mid-code-fence —
// renderMarkdown falls back to the raw text for that frame so the
// chunk isn't dropped.
type markdownRenderer struct {
	r     *glamour.TermRenderer
	dark  bool
	width int
}

// newMarkdownRenderer builds a Glamour renderer with the project's
// chosen style + a soft word-wrap at width. Returns a no-op renderer
// on construction error so callers don't need to handle nil — any
// markdown they pass to renderMarkdown will fall through to raw text.
func newMarkdownRenderer(dark bool, width int) *markdownRenderer {
	r, _ := glamour.NewTermRenderer(
		glamour.WithStyles(tuiStyleConfig(dark)),
		glamour.WithWordWrap(width),
	)
	return &markdownRenderer{r: r, dark: dark, width: width}
}

// tuiStyleConfig starts from Glamour's bundled dark/light style and
// patches two rough edges that hit assistant streams hard:
//
//  1. H2-H6 in the bundled styles render with the literal "##"/"###"
//     prefix in the output (e.g. "## Section" stays "## Section").
//     We strip the prefix and substitute bold + color so heading
//     depth is still visible without leaking raw markdown to the
//     viewport. H1 is left alone — its inverted banner block already
//     strips the "#".
//
//  2. Code fences get static separator lines above and below so the
//     boundary of a code block is visually obvious even when syntax
//     highlighting is muted. Generic chrome — Glamour doesn't plumb
//     the language tag through to the static prefix/suffix.
//
// Lifted from internal/tui's cogoStyleConfig so behavior matches
// what core-agent operators expect.
func tuiStyleConfig(dark bool) ansi.StyleConfig {
	cfg := styles.DarkStyleConfig
	if !dark {
		cfg = styles.LightStyleConfig
	}
	for level, h := range map[int]*ansi.StyleBlock{
		2: &cfg.H2,
		3: &cfg.H3,
		4: &cfg.H4,
		5: &cfg.H5,
		6: &cfg.H6,
	} {
		h.Prefix = ""
		c := headingColor(dark, level)
		h.Color = &c
		t := true
		h.Bold = &t
	}
	cfg.CodeBlock.BlockPrefix = codeBlockTopBar
	cfg.CodeBlock.BlockSuffix = codeBlockBottomBar
	return cfg
}

// codeBlockTopBar / codeBlockBottomBar bracket fenced code blocks so
// the boundary reads as a deliberate frame rather than disappearing
// into the surrounding text.
const (
	codeBlockTopBar    = "──────── code ────────\n"
	codeBlockBottomBar = "──────────────────────"
)

// headingColor returns the 256-color index for heading level n (2-6).
// Cool-blue palette chosen so headings stay distinct from inline code
// and bold body text. Lighter shade per deeper level so the visual
// hierarchy still reads.
func headingColor(dark bool, level int) string {
	if !dark {
		switch level {
		case 2:
			return "27"
		case 3:
			return "33"
		case 4:
			return "61"
		default:
			return "67"
		}
	}
	switch level {
	case 2:
		return "75"
	case 3:
		return "39"
	case 4:
		return "147"
	default:
		return "110"
	}
}

// renderMarkdown returns the Glamour-rendered form of text, or text
// itself when Glamour returns an error (R-MD-3 fallback). Trims one
// trailing newline because Glamour adds one consistently and we
// already manage spacing via the per-turn rule.
func (mr *markdownRenderer) renderMarkdown(text string) string {
	if mr == nil || mr.r == nil || text == "" {
		return text
	}
	out, err := mr.r.Render(text)
	if err != nil {
		return text
	}
	return strings.TrimRight(out, "\n")
}

// splitAtSafeBoundary splits text into (stable, trailing) at the
// latest \n\n boundary that sits OUTSIDE an open ``` code fence.
// stable is the prefix safe to render and cache; trailing is the
// in-flight chunk to re-render on every update. If no safe boundary
// exists yet (mid-fence, first paragraph), returns ("", text) so
// the caller falls back to whole-text rendering.
//
// Used by the incremental streaming render path so long assistant
// responses don't re-parse + re-Glamour the entire accumulated
// text on every token. See renderIncremental.
func splitAtSafeBoundary(text string) (stable, trailing string) {
	for i := strings.LastIndex(text, "\n\n"); i >= 0; {
		candidate := text[:i+2]
		if !insideOpenCodeFence(candidate) {
			return candidate, text[i+2:]
		}
		// Earlier \n\n that might be outside the fence.
		next := strings.LastIndex(text[:i], "\n\n")
		if next < 0 {
			break
		}
		i = next
	}
	return "", text
}

// insideOpenCodeFence reports whether s ends inside an unclosed
// triple-backtick block (odd ``` count means open). Approximation:
// indented-code blocks aren't counted; tilde-fenced blocks (~~~)
// aren't counted. Both are rare in agent output — the stream's
// worst case for those is one extra re-render at fence close.
func insideOpenCodeFence(s string) bool {
	return strings.Count(s, "```")%2 == 1
}

// renderIncremental renders text by reusing a cached render of the
// stable prefix (everything up to the latest safe boundary) and
// only re-Glamour-ing the trailing partial. cachedPrefix /
// cachedRender hold the most recent stable cut; pass empty strings
// on first call or after a width / cache reset. Returns the glued
// result plus the new cache values so the caller can persist them.
//
// When no safe boundary exists yet (first paragraph mid-stream),
// degrades to whole-text rendering each call (cache stays empty).
func (mr *markdownRenderer) renderIncremental(text, cachedPrefix, cachedRender string) (out, newPrefix, newRender string) {
	if mr == nil || mr.r == nil || text == "" {
		return text, "", ""
	}
	stable, trailing := splitAtSafeBoundary(text)
	if stable == "" {
		// No safe boundary yet — render the whole thing as before.
		return mr.renderMarkdown(text), "", ""
	}
	stableRender := cachedRender
	if stable != cachedPrefix {
		stableRender = mr.renderMarkdown(stable)
	}
	if trailing == "" {
		return stableRender, stable, stableRender
	}
	trailingRender := mr.renderMarkdown(trailing)
	return stableRender + "\n\n" + trailingRender, stable, stableRender
}
