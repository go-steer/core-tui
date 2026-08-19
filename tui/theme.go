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

// Theme defines the semantic color tokens that drive every styled
// surface in the TUI (agentic-tui skill §10.A). Components reference
// roles (Primary, Accent, Success, Error, FgBase, BgElevated, etc.)
// instead of concrete colors, so swapping themes is a one-line
// change and color audits become grep-friendly.
//
// Per-provider themes (anthropicTheme, geminiTheme, openAITheme)
// expose subtle palette shifts so each LLM provider has visual
// identity in the status surface — same brand language, different
// accent / secondary hues. themeForProvider picks based on the
// adapter's StatusReporter.Status().Provider.

package tui

import (
	"fmt"
	"image/color"
	"math"
	"strings"

	"charm.land/lipgloss/v2"
)

// Theme is the bundle of semantic color tokens. ~15 fields drive
// every styled component in the TUI. New themes are typically
// ~15 lines of `lipgloss.Color("#...")` plus the Builtin/Per-
// Provider constructor.
type Theme struct {
	Name string

	// Brand: drive the wordmark, accents, and primary highlights.
	Primary   color.Color // wordmark + brand cursor block
	Secondary color.Color // agent identity, model-picker focus
	Accent    color.Color // section heads, palette selection

	// Semantic signal: drive feedback rows + chip/badge states.
	Success color.Color // tool success, confirmation
	Warning color.Color // permission warn, rate-limit notices
	Error   color.Color // error rows, denied permissions
	Info    color.Color // system rows, hints

	// Foreground hierarchy: most→least prominent text.
	FgBase   color.Color // assistant / default text
	FgMuted  color.Color // hints, labels, separators
	FgSubtle color.Color // backgrounded text, disabled

	// Background tiers: rarely used but available for surfaces
	// that need a tinted backdrop (code fences, dialog body).
	BgBase     color.Color // chat backdrop (usually nil = terminal default)
	BgElevated color.Color // dialog / modal body
	BgOverlay  color.Color // tooltip / floater

	// OnPrimary is the foreground for text painted ON a Primary
	// fill rather than on the terminal background — today the
	// inverted H1 banner in assistant markdown, the one surface
	// that reverses out of a brand color. Primary alone cannot
	// answer "what is legible on top of this?", so the token has
	// to exist; but it is DERIVED, not required — a nil OnPrimary
	// is filled in by newStylesWithTheme from a luminance check on
	// Primary, so no builtin and no host theme has to set it.
	OnPrimary color.Color

	// ChromaStyleName names the Chroma style used for BOTH inline
	// syntax highlighting (diff bodies, tool-result previews) and
	// Glamour code fences in assistant markdown. Those two used to
	// be unrelated — inline was pinned to a package-level
	// styles.Get("github") and fences to Glamour's bundled "charm"
	// sub-config — so a code fence and the diff under it painted
	// from two different palettes. Naming one style per theme is
	// what unifies them and what makes /theme reach the code.
	// Empty is filled in with defaultChromaStyleName; an unknown
	// name degrades through Chroma's own fallback, never a panic.
	ChromaStyleName string

	// Borders + rules.
	BorderActive color.Color // focused input / open dialog
	BorderQuiet  color.Color // sidebar dividers, message rules

	// Diff surfaces: dim tints used as backgrounds behind + / -
	// lines in inline tool-display diffs. Foreground stays
	// Success / Error; the bg makes the change region scannable
	// at a glance the way `git diff --color` and GitHub render.
	// The *GutterBg variants are a step deeper than the code
	// bg so the line-number column reads as a distinct "rail"
	// next to the change region (pattern from docs/diffview.md §1).
	DiffAddBg       color.Color
	DiffDelBg       color.Color
	DiffAddGutterBg color.Color
	DiffDelGutterBg color.Color

	// WordmarkSequence, when non-nil, causes the brand wordmark
	// to render with one color per rune from this slice (cycling
	// when the wordmark is longer than the sequence). The Google
	// theme uses this to mimic the iconic B-R-Y-B-G-R logo
	// sequence — the single visual signature no palette
	// distribution alone can produce. Nil falls back to the
	// single-color Primary render (styleSet.Wordmark style), which
	// is what every other theme should keep doing.
	WordmarkSequence []color.Color

	// PromptGlyph overrides the textarea's left-edge prompt rail
	// for themes whose identity has a distinctive glyph (e.g. GKE
	// uses ⎈ , the Unicode helm-symbol, since GKE is Kubernetes).
	// Empty (zero value) keeps the house default "▎ " — every
	// theme that doesn't have a glyph identity should leave this
	// empty. The glyph picks up the active prompt color from
	// styleSet.Focused.Prompt, so foreground color is theme-
	// controlled regardless.
	PromptGlyph string
}

// defaultPromptGlyph is the house textarea prompt rail used by
// every theme that doesn't set Theme.PromptGlyph. A thin vertical
// half-block + space gives a 2-cell-wide focus marker that
// doesn't shift the textarea's column position on theme swap.
const defaultPromptGlyph = "▎ "

// defaultChromaStyleName is the Chroma style a theme gets when it
// leaves ChromaStyleName empty. github's foreground-only palette
// reads on both light and dark terminals, which is why it was the
// hardcoded package default before themes could pick — keeping it
// as the fallback means a host theme written against the older
// Theme struct renders exactly as it did.
const defaultChromaStyleName = "github"

// chromaFor picks a theme's Chroma style by terminal polarity. The
// bundled styles are foreground-only through lipglossFormatter, so
// a dark-terminal style dropped on a white background is a real
// legibility loss, not just a taste one — every builtin that names
// a dark style names its light counterpart too (or falls back to
// defaultChromaStyleName, which reads on both).
func chromaFor(dark bool, darkName, lightName string) string {
	if dark {
		return darkName
	}
	return lightName
}

// normalizeTheme fills in the derived tokens a Theme is allowed to
// leave zero, and is the single funnel every Theme crosses on its
// way to a styleSet bundle (see newStylesWithTheme, which calls it).
//
// It deliberately does NOT live in defaultTheme. There are four
// independent ways a Theme value comes into existence and only two
// of them route through defaultTheme — ThemeByName (via the
// registry's Build closures) and the provider/brand constructors.
// The other two do not: a bare `Theme{...}` composite literal handed
// straight to newStylesWithTheme, and the same shape in the golden
// corpus. The first of those was a host's until #257 unexported the
// constructor; it is in-package now, but the hazard is unchanged and
// so is the argument, because a caller that builds a Theme without
// crossing defaultTheme skips every derivation either way.
// Normalizing at the styleSet boundary
// instead means the twelve builtins need no edits, host themes keep
// working, and the two Branding override sites (newStyles and
// model.resolveStyles) that mutate Primary and then call
// newStylesWithTheme re-derive OnPrimary for free.
func normalizeTheme(t Theme) Theme {
	if t.ChromaStyleName == "" {
		t.ChromaStyleName = defaultChromaStyleName
	}
	if t.OnPrimary == nil {
		t.OnPrimary = contrastOn(t.Primary)
	}
	return t
}

// onPrimaryLight / onPrimaryDark are the two ends contrastOn picks
// between. Near-white and near-black rather than pure #FFFFFF /
// #000000: the pure ends read as a harsh cut-out against a
// saturated brand fill on most terminal palettes.
var (
	onPrimaryLight = lipgloss.Color("#FAFAFA")
	onPrimaryDark  = lipgloss.Color("#0A0A0A")
)

// contrastOn returns the more legible of near-white / near-black
// for text drawn on top of bg, using the WCAG relative-luminance
// midpoint. A nil bg (a Theme that never set Primary) answers
// near-white, which is the safe read on the dark terminals that
// dominate.
func contrastOn(bg color.Color) color.Color {
	if bg == nil {
		return onPrimaryLight
	}
	if relativeLuminance(bg) > 0.5 {
		return onPrimaryDark
	}
	return onPrimaryLight
}

// relativeLuminance returns the WCAG 2.x relative luminance of c in
// [0,1]. Alpha is ignored — every color in a Theme is opaque.
func relativeLuminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	lin := func(v uint32) float64 {
		s := float64(v) / 0xFFFF
		if s <= 0.04045 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

// mixColors blends a toward b by ratio (0 = all a, 1 = all b) in
// sRGB space and returns the result as a lipgloss hex color. Used
// to derive the markdown heading ramp from Accent → FgMuted so
// heading depth stays visible without six more Theme fields.
//
// sRGB rather than a perceptual space on purpose: the inputs are
// already hand-picked brand colors, the outputs are five
// intermediate heading tints, and a gamma-correct blend would buy
// accuracy nobody can see at terminal color depth for a chunk of
// arithmetic and a dependency.
func mixColors(a, b color.Color, ratio float64) color.Color {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	}
	if ratio < 0 {
		ratio = 0
	} else if ratio > 1 {
		ratio = 1
	}
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	blend := func(x, y uint32) uint8 {
		v := (float64(x)*(1-ratio) + float64(y)*ratio) / 0x101
		return uint8(math.Round(math.Min(math.Max(v, 0), 255)))
	}
	return color.RGBA{R: blend(ar, br), G: blend(ag, bg), B: blend(ab, bb), A: 0xFF}
}

// hexColor renders c as "#RRGGBB" for the config surfaces that take
// a color string rather than a color.Color — Glamour's
// ansi.StylePrimitive.Color is a *string, so every theme token that
// reaches the markdown renderer goes through here. Returns "" for
// nil so callers can leave the corresponding Glamour field unset
// and inherit rather than paint a bogus black.
func hexColor(c color.Color) string {
	if c == nil {
		return ""
	}
	r, g, b, _ := c.RGBA()
	// color.Color.RGBA returns alpha-premultiplied values in
	// [0, 0xFFFF], so the high byte is exactly the 8-bit channel;
	// the mask makes that explicit for the overflow linter.
	return fmt.Sprintf("#%02X%02X%02X", (r>>8)&0xFF, (g>>8)&0xFF, (b>>8)&0xFF)
}

// defaultTheme returns the canonical "core-tui" palette — the
// purple-pink Dracula-adjacent identity used by the visual-
// preview slice and inherited by core-agent's launchTUIv2 today.
// `dark` flips the foreground hierarchy so light terminals stay
// readable.
func defaultTheme(dark bool) Theme {
	t := Theme{
		Name:         "default",
		Primary:      brandViolet,
		Secondary:    brandPink,
		Accent:       brandViolet,
		Success:      lipgloss.Color("#5FD787"),
		Warning:      lipgloss.Color("#FFD75F"),
		Error:        lipgloss.Color("#FF5F5F"),
		Info:         lipgloss.Color("#A8A8A8"),
		BorderActive: brandViolet,
		BorderQuiet:  lipgloss.Color("#3A3A3A"),
		// Dracula is the palette this theme is already named after;
		// it has no light counterpart in Chroma's bundle, so light
		// terminals keep the house github default.
		ChromaStyleName: chromaFor(dark, "dracula", defaultChromaStyleName),
	}
	if dark {
		t.FgBase = lipgloss.Color("#D0D0D0")
		t.FgMuted = lipgloss.Color("#9A9A9A")
		t.FgSubtle = lipgloss.Color("#6C6C6C")
		t.BgElevated = lipgloss.Color("#1E1E1E")
		t.BgOverlay = lipgloss.Color("#2A2A2A")
		t.DiffAddBg = lipgloss.Color("#1B2D1B")
		t.DiffDelBg = lipgloss.Color("#3A1E1E")
		t.DiffAddGutterBg = lipgloss.Color("#102010")
		t.DiffDelGutterBg = lipgloss.Color("#2A1010")
	} else {
		t.FgBase = lipgloss.Color("#1E1E1E")
		t.FgMuted = lipgloss.Color("#5F5F5F")
		t.FgSubtle = lipgloss.Color("#9E9E9E")
		t.BgElevated = lipgloss.Color("#F0F0F0")
		t.BgOverlay = lipgloss.Color("#E5E5E5")
		t.BorderQuiet = lipgloss.Color("#D7D7D7")
		t.DiffAddBg = lipgloss.Color("#E6FFE6")
		t.DiffDelBg = lipgloss.Color("#FFE6E6")
		t.DiffAddGutterBg = lipgloss.Color("#C8F0C8")
		t.DiffDelGutterBg = lipgloss.Color("#F0C8C8")
	}
	return t
}

// anthropicTheme tints toward Claude's warm orange identity. Used
// when the host's StatusReporter reports Provider == "anthropic".
func anthropicTheme(dark bool) Theme {
	t := defaultTheme(dark)
	t.Name = "anthropic"
	// Gruvbox is the warm orange/brown end of Chroma's bundle —
	// the closest match to the clay identity, in both polarities.
	t.ChromaStyleName = chromaFor(dark, "gruvbox", "gruvbox-light")
	t.Primary = lipgloss.Color("#D97757") // Claude clay
	t.Secondary = lipgloss.Color("#F0B27A")
	t.Accent = lipgloss.Color("#D97757")
	t.BorderActive = lipgloss.Color("#D97757")
	return t
}

// geminiTheme tints toward Google's blue/teal palette. Used for
// Provider == "gemini" / "vertex".
func geminiTheme(dark bool) Theme {
	t := defaultTheme(dark)
	t.Name = "gemini"
	t.ChromaStyleName = chromaFor(dark, "tokyonight-night", "tokyonight-day")
	t.Primary = lipgloss.Color("#4285F4")
	t.Secondary = lipgloss.Color("#5FD7FF")
	t.Accent = lipgloss.Color("#4285F4")
	t.BorderActive = lipgloss.Color("#4285F4")
	return t
}

// openAITheme tints toward OpenAI's green identity. Used for
// Provider == "openai".
func openAITheme(dark bool) Theme {
	t := defaultTheme(dark)
	t.Name = "openai"
	// Monokai's green function names + teal keywords are the
	// nearest bundled read on the OpenAI green.
	t.ChromaStyleName = chromaFor(dark, "monokai", "monokailight")
	t.Primary = lipgloss.Color("#10A37F")
	t.Secondary = lipgloss.Color("#4ECCA3")
	t.Accent = lipgloss.Color("#10A37F")
	t.BorderActive = lipgloss.Color("#10A37F")
	return t
}

// googleTheme paints the surface in Google's full brand palette
// from the 15-color Google News set, distributing all five logo
// hues across the decorative + signal slots so the surface reads
// as a Google product (Search / Maps / Drive chrome) rather than
// a single-hue blue tint:
//
//   - Primary deep blue (#174EA6) stamps brand identity on the
//     wordmark (authoritative; mirrors the dark blue used in the
//     Google logo).
//   - BorderActive Medium blue (#4285F4) frames focused inputs in
//     a lighter blue so the focus ring sits visually distinct
//     from the deeper-blue identity above.
//   - Accent yellow (#FBBC04) makes section heads + palette
//     selection pop — yellow is high-contrast against the
//     blue/red base.
//   - Warning brand orange (#E37400) keeps Warning visually
//     separated from Accent yellow so a yellow header doesn't
//     read as "warning" at a glance.
//
// Success green and Error red stay on the medium-tone brand
// variants for readable foreground text on dark backdrops.
// Diff bgs flip with dark/light so light terminals stay readable.
func googleTheme(dark bool) Theme {
	t := defaultTheme(dark)
	t.Name = "google"
	// GitHub's own palettes: the neutral, unmistakably
	// engineering-surface read that suits the Google chrome.
	t.ChromaStyleName = chromaFor(dark, "github-dark", "github")
	t.Primary = lipgloss.Color("#174EA6")      // deep blue — wordmark
	t.Secondary = lipgloss.Color("#EA4335")    // brand red — agent identity
	t.Accent = lipgloss.Color("#F9AB00")       // brand amber — softer than bright yellow on bold modal/tool heads
	t.Success = lipgloss.Color("#34A853")      // brand green
	t.Warning = lipgloss.Color("#E37400")      // brand orange (distinct from Accent yellow)
	t.Error = lipgloss.Color("#EA4335")        // brand red
	t.Info = lipgloss.Color("#9AA0A6")         // brand grey
	t.BorderActive = lipgloss.Color("#34A853") // brand green — focus ring on textarea + dialogs
	// The signature Google "Google" sequence: B-R-Y-B-G-R. The
	// wordmark renderer cycles this slice over the chars of the
	// configured wordmark — pattern-matches the Google logo even
	// for wordmarks of different lengths than "Google".
	t.WordmarkSequence = []color.Color{
		lipgloss.Color("#4285F4"), // B — medium blue
		lipgloss.Color("#EA4335"), // R — medium red
		lipgloss.Color("#FBBC04"), // Y — brand yellow (stays bright here; this is the iconic spot for yellow)
		lipgloss.Color("#4285F4"), // B — medium blue
		lipgloss.Color("#34A853"), // G — brand green
		lipgloss.Color("#EA4335"), // R — medium red
	}
	if dark {
		// BorderQuiet + DiffAddGutterBg shift green-ward so
		// dividers / status separators / addition-line gutters
		// carry a faint green wash without colliding with the
		// Success foreground color.
		t.BorderQuiet = lipgloss.Color("#3D5246")
		t.DiffAddBg = lipgloss.Color("#0C2415")
		t.DiffDelBg = lipgloss.Color("#2C0E0D")
		t.DiffAddGutterBg = lipgloss.Color("#0F2818")
		t.DiffDelGutterBg = lipgloss.Color("#180807")
	} else {
		t.BorderQuiet = lipgloss.Color("#C5DCCD")
		t.DiffAddBg = lipgloss.Color("#CEEAD6") // brand light green
		t.DiffDelBg = lipgloss.Color("#FAD2CF") // brand light red
		t.DiffAddGutterBg = lipgloss.Color("#9FD5B0")
		t.DiffDelGutterBg = lipgloss.Color("#F0BAB6")
	}
	return t
}

// gkeTheme is the Google Kubernetes Engine variant of the Google
// theme. Two GKE-specific signatures vs plain Google:
//
//  1. Wordmark cycles R-B-G-Y (the GKE icon's clockwise quadrant
//     order: top-red, right-blue, bottom-green, left-yellow)
//     instead of Google's B-R-Y-B-G-R logo letter order. Anyone
//     who's seen the GKE hexagonal icon will recognize the
//     sequence.
//  2. Prompt glyph is ⎈ (U+2388 HELM SYMBOL), the Unicode K8s
//     logo character. Replaces the house ▎ prompt rail so every
//     input row carries the Kubernetes signature.
//
// Everything else (chrome, signal colors, focus ring, diff bgs)
// inherits from googleTheme — GKE IS a Google product, the brand
// chrome should match.
func gkeTheme(dark bool) Theme {
	t := googleTheme(dark)
	t.Name = "gke"
	// R-B-G-Y, clockwise from top of the GKE hexagonal icon.
	t.WordmarkSequence = []color.Color{
		lipgloss.Color("#EA4335"), // R — top
		lipgloss.Color("#4285F4"), // B — right
		lipgloss.Color("#34A853"), // G — bottom
		lipgloss.Color("#FBBC04"), // Y — left
	}
	t.PromptGlyph = "⎈ " // Unicode helm symbol — the Kubernetes logo glyph
	return t
}

// gopherTheme paints the surface in the Go brand palette from the
// Go Brand Book (Gopher Blue → Aqua gradient, Fuchsia / Yellow
// secondaries). Source: cogo-wasm2/docs/color-palette.md.
func gopherTheme(dark bool) Theme {
	t := defaultTheme(dark)
	t.Name = "gopher"
	// Nord / solarized-light are the cool blue-teal families that
	// sit on the Gopher Blue → Aqua gradient.
	t.ChromaStyleName = chromaFor(dark, "nord", "solarized-light")
	t.Primary = lipgloss.Color("#00ADD8")   // Gopher Blue
	t.Secondary = lipgloss.Color("#5DC9E2") // Light Blue — pairs with gradient
	t.Accent = lipgloss.Color("#00A29C")    // Aqua — far end of gradient
	t.Success = lipgloss.Color("#00A29C")   // Aqua reads as teal/green success
	t.Warning = lipgloss.Color("#FDDD00")   // brand yellow
	t.Error = lipgloss.Color("#CE3262")     // Fuchsia
	t.Info = lipgloss.Color("#555759")      // Slate
	t.BorderActive = lipgloss.Color("#00ADD8")
	if dark {
		t.BorderQuiet = lipgloss.Color("#555759") // Slate
		t.DiffAddBg = lipgloss.Color("#0A2826")
		t.DiffDelBg = lipgloss.Color("#2D1018")
		t.DiffAddGutterBg = lipgloss.Color("#051716")
		t.DiffDelGutterBg = lipgloss.Color("#19080E")
	} else {
		t.BorderQuiet = lipgloss.Color("#DBD9D6") // Cool Gray
		t.DiffAddBg = lipgloss.Color("#D7F0EE")
		t.DiffDelBg = lipgloss.Color("#F5D5DF")
		t.DiffAddGutterBg = lipgloss.Color("#BDE2DF")
		t.DiffDelGutterBg = lipgloss.Color("#E8BCCA")
	}
	// Official Go gradient — Gopher Blue → Aqua, 6 stops of
	// linear RGB interpolation between #00ADD8 and #00A29C.
	// Brand-book literal: "Go gradient combines the spectrum of
	// Go Blue and Aqua colors." Subtle but authentic; pattern-
	// matches Go's brand chrome (pkg.go.dev, golang.org logo
	// backgrounds) for anyone who's seen them.
	t.WordmarkSequence = []color.Color{
		lipgloss.Color("#00ADD8"), // Gopher Blue
		lipgloss.Color("#00AACC"),
		lipgloss.Color("#00A8C0"),
		lipgloss.Color("#00A6B4"),
		lipgloss.Color("#00A4A8"),
		lipgloss.Color("#00A29C"), // Aqua
	}
	return t
}

// matrixTheme paints the surface in green-on-black phosphor —
// terminal-hacker aesthetic. The wordmark cycles 6 shades of
// green light-to-dim, mimicking the "rain head" → trailing
// tail look from the films. Error stays CRT-red so failures
// pop hard against the monochromatic green base.
func matrixTheme(dark bool) Theme {
	t := defaultTheme(dark)
	t.Name = "matrix"
	// swapoff is Chroma's monochrome-terminal style: near-white
	// keywords, cyan strings, teal comments on black. The narrow
	// hue set is what keeps a code fence reading as phosphor
	// rather than as a syntax rainbow parked in the middle of the
	// green chrome.
	t.ChromaStyleName = chromaFor(dark, "swapoff", defaultChromaStyleName)
	t.Primary = lipgloss.Color("#00FF41")   // bright matrix green
	t.Secondary = lipgloss.Color("#39FF14") // lime
	t.Accent = lipgloss.Color("#7FFF7F")    // pale green
	t.Success = lipgloss.Color("#00FF41")
	t.Warning = lipgloss.Color("#FFB000") // amber (CRT warning)
	t.Error = lipgloss.Color("#FF0033")   // CRT red — only non-green hue
	t.Info = lipgloss.Color("#5A8F5A")    // dim green-grey
	t.BorderActive = lipgloss.Color("#00FF41")
	if dark {
		t.BorderQuiet = lipgloss.Color("#003B00")
		t.BgElevated = lipgloss.Color("#020F02") // very dark green-tinted black
		t.BgOverlay = lipgloss.Color("#041604")
	}
	// Rain effect — bright leading char fading to dim tail.
	t.WordmarkSequence = []color.Color{
		lipgloss.Color("#9FFF9F"), // brightest leading head
		lipgloss.Color("#39FF14"),
		lipgloss.Color("#00FF41"),
		lipgloss.Color("#00C82E"),
		lipgloss.Color("#008F11"),
		lipgloss.Color("#005700"), // tail
	}
	return t
}

// prideTheme paints a neutral violet chrome with a full
// rainbow-flag wordmark. The 6 flag colors R-O-Y-G-B-V land
// on the wordmark; body text stays calm so the rainbow is a
// signature, not a constant assault.
func prideTheme(dark bool) Theme {
	t := defaultTheme(dark)
	t.Name = "pride"
	// Catppuccin spreads the widest pastel hue set of any bundled
	// style — the closest a code fence gets to the flag without
	// becoming unreadable.
	t.ChromaStyleName = chromaFor(dark, "catppuccin-mocha", "catppuccin-latte")
	t.Primary = lipgloss.Color("#5A189A")   // deep violet — wordmark base
	t.Secondary = lipgloss.Color("#7B2CBF") // violet — agent identity
	t.Accent = lipgloss.Color("#FFED00")    // flag yellow
	t.Success = lipgloss.Color("#008026")   // flag green
	t.Warning = lipgloss.Color("#FF8C00")   // flag orange
	t.Error = lipgloss.Color("#E40303")     // flag red
	t.Info = lipgloss.Color("#9A8FB8")      // lavender grey
	t.BorderActive = lipgloss.Color("#7B2CBF")
	if dark {
		t.BorderQuiet = lipgloss.Color("#3D2E5E")
	}
	// 6-stripe rainbow flag — R-O-Y-G-B-V, the canonical order.
	t.WordmarkSequence = []color.Color{
		lipgloss.Color("#E40303"), // red
		lipgloss.Color("#FF8C00"), // orange
		lipgloss.Color("#FFED00"), // yellow
		lipgloss.Color("#008026"), // green
		lipgloss.Color("#004DFF"), // blue
		lipgloss.Color("#750787"), // violet
	}
	return t
}

// cyberpunkTheme paints a deep-magenta chrome with neon
// yellow/cyan/magenta accents. The wordmark cycles Y-C-M-Y-C-M
// for an arcade-marquee feel. Loud on purpose — this is the
// "I'm hacking the planet" theme.
func cyberpunkTheme(dark bool) Theme {
	t := defaultTheme(dark)
	t.Name = "cyberpunk"
	// witchhazel is neon mint / cyan / lilac on deep purple —
	// the arcade-marquee palette this theme is built around.
	t.ChromaStyleName = chromaFor(dark, "witchhazel", defaultChromaStyleName)
	t.Primary = lipgloss.Color("#7B007B")   // deep magenta — wordmark base
	t.Secondary = lipgloss.Color("#00FFD0") // hot cyan
	t.Accent = lipgloss.Color("#FCEE0A")    // neon yellow
	t.Success = lipgloss.Color("#00FFD0")   // cyan = "online"
	t.Warning = lipgloss.Color("#FCEE0A")
	t.Error = lipgloss.Color("#FF003C")
	t.Info = lipgloss.Color("#5A189A")
	t.BorderActive = lipgloss.Color("#FF00A0") // hot magenta focus ring
	if dark {
		t.BorderQuiet = lipgloss.Color("#1A0F2E") // near-black purple
		t.BgElevated = lipgloss.Color("#0D0517")
		t.BgOverlay = lipgloss.Color("#1A0F2E")
	}
	// Y-C-M arcade cycle.
	t.WordmarkSequence = []color.Color{
		lipgloss.Color("#FCEE0A"), // yellow
		lipgloss.Color("#00FFD0"), // cyan
		lipgloss.Color("#FF00A0"), // magenta
	}
	return t
}

// vaporwaveTheme paints a pink/purple/cyan synthwave palette.
// The wordmark gradients pink→cyan across 6 stops for that
// 80s-Miami-poolside-screensaver feel. Less aggressive than
// Cyberpunk; same chromatic family but softer.
func vaporwaveTheme(dark bool) Theme {
	t := defaultTheme(dark)
	t.Name = "vaporwave"
	// base16-snazzy: hot-pink keywords, mint strings, sky-blue
	// functions — synthwave, straight out of the bundle.
	t.ChromaStyleName = chromaFor(dark, "base16-snazzy", "rose-pine-dawn")
	t.Primary = lipgloss.Color("#FF71CE")   // hot pink
	t.Secondary = lipgloss.Color("#01CDFE") // cyan
	t.Accent = lipgloss.Color("#B967FF")    // purple
	t.Success = lipgloss.Color("#05FFA1")   // mint
	t.Warning = lipgloss.Color("#FFFB96")   // peach
	t.Error = lipgloss.Color("#FF5C8A")     // pink-red
	t.Info = lipgloss.Color("#9D7AB8")      // lavender
	t.BorderActive = lipgloss.Color("#B967FF")
	if dark {
		t.BorderQuiet = lipgloss.Color("#3D2E5E")
		t.BgElevated = lipgloss.Color("#1A0F2E")
		t.BgOverlay = lipgloss.Color("#2D1A4A")
	}
	// Smooth gradient pink → cyan (6 stops).
	t.WordmarkSequence = []color.Color{
		lipgloss.Color("#FF71CE"), // hot pink
		lipgloss.Color("#FF8FCE"),
		lipgloss.Color("#C880DB"),
		lipgloss.Color("#9F80EB"),
		lipgloss.Color("#5BA9F8"),
		lipgloss.Color("#01CDFE"), // cyan
	}
	return t
}

// christmasTheme — red + green + gold festive. Wordmark
// alternates R-G-R-G-R-G. Use in December (or whenever). Doubles
// as a perfectly-themed diff palette (red = remove, green = add
// matches the chrome).
func christmasTheme(dark bool) Theme {
	t := defaultTheme(dark)
	t.Name = "christmas"
	// rrt is red keywords, green comments and gold functions on
	// black — accidentally the exact festive triad.
	t.ChromaStyleName = chromaFor(dark, "rrt", defaultChromaStyleName)
	t.Primary = lipgloss.Color("#C8102E")   // holiday red
	t.Secondary = lipgloss.Color("#0E5F40") // holiday green
	t.Accent = lipgloss.Color("#D4AF37")    // gold
	t.Success = lipgloss.Color("#0E5F40")   // pine
	t.Warning = lipgloss.Color("#D4AF37")   // gold
	t.Error = lipgloss.Color("#C8102E")     // cardinal red
	t.Info = lipgloss.Color("#C0C0C0")      // silver
	t.BorderActive = lipgloss.Color("#C8102E")
	if dark {
		t.BorderQuiet = lipgloss.Color("#3D2A1A") // warm wood tone
	}
	// R-G-R-G-R-G alternating — pure festive.
	t.WordmarkSequence = []color.Color{
		lipgloss.Color("#C8102E"), // red
		lipgloss.Color("#0E5F40"), // green
	}
	return t
}

// BuiltinTheme describes one entry in the built-in theme registry —
// the seed list that the /theme picker iterates and that
// ThemeByName resolves against. Hosts that want to advertise a
// custom theme today can apply it via Options.Branding overrides;
// a future PR could extend this to accept host-registered themes.
type BuiltinTheme struct {
	// Name is the canonical lower-case slug ("default", "google",
	// "gopher", "anthropic", "gemini", "openai"). Matched case-
	// insensitively by ThemeByName + /theme <name>.
	Name string
	// Description is a one-line palette summary shown in the
	// picker's row (muted style).
	Description string
	// Build is the constructor — called with the current dark
	// flag every time the theme is applied, so /theme transitions
	// pick up the correct foreground hierarchy without a restart.
	Build func(dark bool) Theme
}

// BuiltinThemes returns the seed registry in display order. The
// picker shows them in this exact order, grouped:
//
//   - "default" first as the neutral baseline.
//   - Brand themes (google / gopher) — operator-facing identities.
//   - Per-provider variants (anthropic / gemini / openai) — auto-
//     applied by themeForProvider; available manually too.
//   - Fun / show-off themes leveraging the multicolor wordmark
//     (matrix / pride / cyberpunk / vaporwave / christmas) — pure
//     personality, last so the "serious" set is scannable first.
func BuiltinThemes() []BuiltinTheme {
	return []BuiltinTheme{
		{Name: "default", Description: "house violet/pink (Dracula-adjacent)", Build: defaultTheme},
		{Name: "google", Description: "Google News palette — blue / red / green / yellow + multicolor wordmark", Build: googleTheme},
		{Name: "gke", Description: "Google Kubernetes Engine — R-B-G-Y icon-quadrant wordmark + ⎈ helm prompt", Build: gkeTheme},
		{Name: "gopher", Description: "Go brand book — Gopher Blue → Aqua gradient wordmark, Fuchsia + brand yellow", Build: gopherTheme},
		{Name: "anthropic", Description: "Claude clay — warm orange", Build: anthropicTheme},
		{Name: "gemini", Description: "Gemini blue / teal", Build: geminiTheme},
		{Name: "openai", Description: "OpenAI green", Build: openAITheme},
		{Name: "matrix", Description: "green-on-black phosphor — wordmark cycles 6 shades of green", Build: matrixTheme},
		{Name: "pride", Description: "calm violet chrome + full rainbow-flag wordmark (R-O-Y-G-B-V)", Build: prideTheme},
		{Name: "cyberpunk", Description: "neon yellow / cyan / magenta — wordmark cycles Y-C-M", Build: cyberpunkTheme},
		{Name: "vaporwave", Description: "synthwave pink → cyan gradient wordmark, lavender chrome", Build: vaporwaveTheme},
		{Name: "christmas", Description: "red + green + gold — wordmark alternates R-G", Build: christmasTheme},
	}
}

// ThemeByName resolves a case-insensitive name against the
// builtin registry and returns the constructed Theme. Unknown
// names fall back to defaultTheme so a stale persisted name or
// a typo in /theme <name> never strands the operator on a
// half-painted UI.
func ThemeByName(name string, dark bool) Theme {
	for _, bt := range BuiltinThemes() {
		if strings.EqualFold(bt.Name, name) {
			return bt.Build(dark)
		}
	}
	return defaultTheme(dark)
}

// themeForProvider returns the per-provider theme variant for
// the given provider tag, or defaultTheme on empty / unknown.
// The provider string is matched case-insensitively and tolerates
// vendor suffixes ("anthropic-vertex" → anthropic).
func themeForProvider(provider string, dark bool) Theme {
	switch {
	case provider == "":
		return defaultTheme(dark)
	case containsCI(provider, "anthropic"):
		return anthropicTheme(dark)
	case containsCI(provider, "gemini"), containsCI(provider, "vertex"):
		return geminiTheme(dark)
	case containsCI(provider, "openai"):
		return openAITheme(dark)
	default:
		return defaultTheme(dark)
	}
}

// containsCI is a case-insensitive substring helper (no
// strings import needed in callers; the test is tiny so the
// allocation is fine).
func containsCI(s, sub string) bool {
	if len(sub) == 0 || len(s) < len(sub) {
		return len(sub) == 0
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			a, b := s[i+j], sub[j]
			if a >= 'A' && a <= 'Z' {
				a += 32
			}
			if b >= 'A' && b <= 'Z' {
				b += 32
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
