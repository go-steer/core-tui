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
	"image/color"
	"strings"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
)

// markdownRenderer wraps a Glamour TermRenderer with the parameters
// the TUI tracks — the active theme, dark/light background and
// viewport width. Held by Model and lazily rebuilt when the width or
// the dark flag changes; a theme change nils the field outright
// (Model.refreshTheme), because Theme carries a slice and so cannot
// be compared for equality the way dark/width can.
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

// newMarkdownRenderer builds a Glamour renderer from the theme's
// tokens + a soft word-wrap at width. Returns a no-op renderer on
// construction error so callers don't need to handle nil — any
// markdown they pass to renderMarkdown will fall through to raw text.
func newMarkdownRenderer(theme Theme, dark bool, width int) *markdownRenderer {
	r, _ := glamour.NewTermRenderer(
		glamour.WithStyles(tuiStyleConfig(theme, dark)),
		glamour.WithWordWrap(width),
		// Route Chroma syntax highlighting through Lipgloss so code
		// fences pick up the active color profile + the active theme
		// (agentic-tui skill §11.B). Without this, Chroma emits raw
		// 24-bit ANSI that fights our palette.
		glamour.WithChromaFormatter(chromaFormatterName),
	)
	return &markdownRenderer{r: r, dark: dark, width: width}
}

// tuiStyleConfig builds the Glamour style config for a theme. It
// starts from Glamour's bundled dark/light style — for the structural
// settings we have no opinion about (margins, indents, list level
// indent, block prefixes) — and then repaints every color-bearing
// element from Theme tokens.
//
// It used to take only a `dark bool`, which collapsed all twelve
// themes into exactly two markdown styles: H2-H6 got hardcoded
// 256-color indices and everything else — Document, BlockQuote, H1,
// Code, CodeBlock.Chroma, Table — was inherited verbatim from
// Glamour. Switching to Matrix or Christmas repainted the chrome and
// left the assistant text, which is most of what is on screen,
// Glamour-blue (issue #116).
//
// What the theme now drives:
//
//  1. Body text — Document takes FgBase, so assistant prose is the
//     same color as every other FgBase surface instead of Glamour's
//     "252" / "234".
//
//  2. Headings — H1 reverses OnPrimary out of a Primary fill (the
//     one surface in the TUI that paints text on a brand color, and
//     the reason OnPrimary exists as a token). H2-H6 walk a derived
//     ramp from Accent down to FgMuted, so heading depth still reads
//     without six more Theme fields. The bundled H2-H6 also render
//     the literal "##"/"###" prefix into the output; we keep
//     stripping it and substituting bold + color so heading depth is
//     visible without leaking raw markdown to the viewport.
//
//  3. Code — inline Code takes Accent on BgElevated; fenced blocks
//     get their bars in BorderQuiet and, decisively, hand Chroma the
//     theme's OWN style by name (CodeBlock.Theme) instead of
//     Glamour's bundled Chroma sub-config. That last line is what
//     unifies markdown fences with the inline diff highlighter,
//     which reads the same Theme.ChromaStyleName. Clearing
//     CodeBlock.Chroma is also a correctness fix, not just a
//     re-point: Glamour registers that sub-config globally under one
//     fixed style name and skips registration if the name is already
//     taken, so the FIRST theme to build a renderer would have
//     silently owned code-fence colors for every theme after it.
//
//  4. The remaining chrome — rules, links, list bullets, block
//     quotes, table borders — takes BorderQuiet / Accent / Secondary
//     / FgMuted, so a theme swap moves the whole surface rather than
//     the frame around it.
func tuiStyleConfig(theme Theme, dark bool) ansi.StyleConfig {
	cfg := styles.DarkStyleConfig
	if !dark {
		cfg = styles.LightStyleConfig
	}
	theme = normalizeTheme(theme)

	// Body text. Document only: cfg.Text is the innermost cascade
	// level and setting it there would win over every enclosing
	// block, repainting headings and links in body color too.
	setColor(&cfg.Document.StylePrimitive, theme.FgBase)

	// Block quote: muted + italic, indented by Glamour's "│ " token
	// which inherits the same color.
	setColor(&cfg.BlockQuote.StylePrimitive, theme.FgMuted)
	cfg.BlockQuote.Italic = boolPtr(true)

	// Headings. H1 is the inverted banner; H2-H6 walk the ramp.
	setColor(&cfg.Heading.StylePrimitive, theme.Accent)
	cfg.Heading.Bold = boolPtr(true)
	setColor(&cfg.H1.StylePrimitive, theme.OnPrimary)
	setBackground(&cfg.H1.StylePrimitive, theme.Primary)
	cfg.H1.Bold = boolPtr(true)
	for level, h := range map[int]*ansi.StyleBlock{
		2: &cfg.H2,
		3: &cfg.H3,
		4: &cfg.H4,
		5: &cfg.H5,
		6: &cfg.H6,
	} {
		h.Prefix = ""
		setColor(&h.StylePrimitive, headingColor(theme, level))
		h.Bold = boolPtr(true)
	}

	// Inline code + fenced code blocks.
	setColor(&cfg.Code.StylePrimitive, theme.Accent)
	setBackground(&cfg.Code.StylePrimitive, theme.BgElevated)
	setColor(&cfg.CodeBlock.StylePrimitive, theme.BorderQuiet)
	cfg.CodeBlock.BlockPrefix = codeBlockTopBar
	cfg.CodeBlock.BlockSuffix = codeBlockBottomBar
	// Name the theme's Chroma style and drop Glamour's bundled
	// sub-config — see the doc comment above for why the sub-config
	// is not merely redundant here but actively wrong.
	cfg.CodeBlock.Theme = theme.ChromaStyleName
	cfg.CodeBlock.Chroma = nil

	// Rules, links, lists, tables.
	setColor(&cfg.HorizontalRule, theme.BorderQuiet)
	setColor(&cfg.Item, theme.Accent)
	setColor(&cfg.Enumeration, theme.Accent)
	setColor(&cfg.Link, theme.Secondary)
	setColor(&cfg.LinkText, theme.Accent)
	setColor(&cfg.Image, theme.Secondary)
	setColor(&cfg.ImageText, theme.FgMuted)
	setColor(&cfg.Table.StylePrimitive, theme.FgBase)
	setColor(&cfg.DefinitionTerm, theme.Accent)
	setColor(&cfg.DefinitionDescription, theme.FgMuted)
	return cfg
}

// setColor points a Glamour style primitive's foreground at a theme
// token. A nil token leaves the field alone so the element inherits
// (Glamour's Color is a *string; writing a bogus "#000000" for an
// unset token would paint black rather than inherit).
func setColor(p *ansi.StylePrimitive, c color.Color) {
	if hex := hexColor(c); hex != "" {
		p.Color = &hex
	}
}

// setBackground is setColor for the background slot.
func setBackground(p *ansi.StylePrimitive, c color.Color) {
	if hex := hexColor(c); hex != "" {
		p.BackgroundColor = &hex
	}
}

// boolPtr is the *bool Glamour's tri-state style fields want.
func boolPtr(b bool) *bool { return &b }

// codeBlockTopBar / codeBlockBottomBar bracket fenced code blocks so
// the boundary reads as a deliberate frame rather than disappearing
// into the surrounding text.
const (
	codeBlockTopBar    = "──────── code ────────\n"
	codeBlockBottomBar = "──────────────────────"
)

// headingColor returns the color for heading level n (2-6) as a
// point on a derived ramp from Accent (H2) to FgMuted (H6).
//
// It used to return hardcoded 256-color indices off a cool-blue
// scale, which is why every theme's headings were Glamour-blue.
// Deriving the ramp instead of adding five "subtle accent tier"
// tokens to Theme keeps the exported surface flat — the tiers carry
// no information the endpoints don't already have, and Theme is
// being narrowed for v1.0, not grown.
func headingColor(theme Theme, level int) color.Color {
	// H2 = 0.0 (pure Accent) … H6 = 1.0 (pure FgMuted).
	ratios := map[int]float64{2: 0, 3: 0.25, 4: 0.5, 5: 0.75, 6: 1}
	ratio, ok := ratios[level]
	if !ok {
		ratio = 1
	}
	return mixColors(theme.Accent, theme.FgMuted, ratio)
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
