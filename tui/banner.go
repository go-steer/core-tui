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

// Startup wordmark banner (issue #165).
//
// The empty state was a single italic line of hint text. This draws
// the brand wordmark above it in a five-row block face and wipes it
// in once, left to right, over about a second — after which it sits
// perfectly still until the first message replaces it.
//
// Three properties are load-bearing, in the order they were argued:
//
//   - It never delays the first usable frame, and it never *becomes*
//     the first usable frame. The banner is a pure function of a frame
//     counter that the constructor seeds to *finished* unless the wipe
//     is genuinely going to run (initialBannerFrame). Nothing waits on
//     it; if the tick chain never gets a turn, what is on screen is
//     the settled wordmark rather than a half-drawn one.
//   - It is one-shot. A continuously animated banner would repaint the
//     whole chat window forever on a screen where nothing is
//     happening; the issue makes that objection itself. bannerFrames
//     ticks in, then the chain stops arming and the cost goes to zero.
//   - It degrades rather than disappears. No truecolour, NO_COLOR, a
//     wordmark with a rune the face does not carry, a window too
//     narrow or too short — every one of those falls back to the plain
//     styled wordmark line, and only a host opt-out removes it
//     entirely.
//
// The face is deliberately hand-drawn ASCII art rather than a font
// dependency. It is 45 glyphs at 5x5, which is small enough to read
// as art in the source — the whole point of writing it with '#' and
// ' ' instead of packed bits is that a reviewer can see the letter.

package tui

import (
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Banner geometry. Each glyph is a fixed 5x5 cell box with a
// one-column gutter after it, so a wordmark of n runes occupies
// n*(bannerGlyphWidth+bannerGlyphGap)-bannerGlyphGap cells. Fixed
// width rather than proportional because the wipe below advances by
// column, and a proportional face would make the sweep speed depend
// on which letters the host happens to have configured.
const (
	bannerGlyphRows  = 5
	bannerGlyphWidth = 5
	bannerGlyphGap   = 1
)

// bannerOn is the lit cell in the source art below; bannerCell is
// what it renders as. Kept separate so the art stays legible as art
// — a grid of '#' shows the letter, a grid of '█' shows a smear.
const (
	bannerOn   = '#'
	bannerCell = "█"
)

// bannerFrames is how many ticks the wipe takes end to end, and
// bannerCadence is how long each one lasts. At the spinner's frame
// cadence that is 1.2s — long enough to register as a deliberate
// entrance, short enough that an operator who types immediately never
// notices it happened. The wipe is over before a first token could
// plausibly arrive.
//
// Sharing spinnerFrameCadence is not laziness: it means the banner and
// the spinner cannot beat against each other at the one moment both
// are on screen (a turn submitted mid-wipe), and it means issue #162's
// argument about what a tick actually costs — a chat-tail rebuild and
// a repaint, not a timer — was already made and does not need making
// twice.
const (
	bannerFrames  = 12
	bannerCadence = spinnerFrameCadence
)

// bannerEdgeCells is how wide the bright leading edge of the wipe is.
// Three cells is a highlight; much more and it stops reading as an
// edge and starts reading as a second colour.
const bannerEdgeCells = 3

// bannerMinRows is the vertical room the banner insists on before it
// will draw: its own five rows, the blank line under it, and the hint
// line, plus two rows of slack so the empty state never fills the
// window to the last cell. Below that the hint renders alone, which
// is what every release before this one did.
const bannerMinRows = bannerGlyphRows + 4

// bannerFace is the block face, one entry per supported rune, five
// strings of exactly bannerGlyphWidth characters each. A rune absent
// from this map is what makes renderBanner decline — a wordmark is a
// brand, and half of one rendered large with the rest silently
// dropped is worse than the plain line.
//
// Lowercase is folded to uppercase before lookup, so only the
// uppercase forms are carried. The punctuation set is what actually
// turns up in product names: the hyphen and underscore of a package
// name, the dot of a domain, and the slash, plus and colon that
// namespaced names use.
var bannerFace = map[rune][bannerGlyphRows]string{
	'A': {" ### ", "#   #", "#####", "#   #", "#   #"},
	'B': {"#### ", "#   #", "#### ", "#   #", "#### "},
	'C': {" ####", "#    ", "#    ", "#    ", " ####"},
	'D': {"#### ", "#   #", "#   #", "#   #", "#### "},
	'E': {"#####", "#    ", "#### ", "#    ", "#####"},
	'F': {"#####", "#    ", "#### ", "#    ", "#    "},
	'G': {" ####", "#    ", "#  ##", "#   #", " ####"},
	'H': {"#   #", "#   #", "#####", "#   #", "#   #"},
	'I': {"#####", "  #  ", "  #  ", "  #  ", "#####"},
	'J': {"#####", "   # ", "   # ", "#  # ", " ##  "},
	'K': {"#   #", "#  # ", "###  ", "#  # ", "#   #"},
	'L': {"#    ", "#    ", "#    ", "#    ", "#####"},
	'M': {"#   #", "## ##", "# # #", "#   #", "#   #"},
	'N': {"#   #", "##  #", "# # #", "#  ##", "#   #"},
	'O': {" ### ", "#   #", "#   #", "#   #", " ### "},
	'P': {"#### ", "#   #", "#### ", "#    ", "#    "},
	'Q': {" ### ", "#   #", "#   #", "#  # ", " ## #"},
	'R': {"#### ", "#   #", "#### ", "#  # ", "#   #"},
	'S': {" ####", "#    ", " ### ", "    #", "#### "},
	'T': {"#####", "  #  ", "  #  ", "  #  ", "  #  "},
	'U': {"#   #", "#   #", "#   #", "#   #", " ### "},
	'V': {"#   #", "#   #", "#   #", " # # ", "  #  "},
	'W': {"#   #", "#   #", "# # #", "## ##", "#   #"},
	'X': {"#   #", " # # ", "  #  ", " # # ", "#   #"},
	'Y': {"#   #", " # # ", "  #  ", "  #  ", "  #  "},
	'Z': {"#####", "   # ", "  #  ", " #   ", "#####"},
	'0': {" ### ", "#  ##", "# # #", "##  #", " ### "},
	'1': {"  #  ", " ##  ", "  #  ", "  #  ", "#####"},
	'2': {" ### ", "#   #", "   # ", "  #  ", "#####"},
	'3': {"#### ", "    #", " ### ", "    #", "#### "},
	'4': {"#   #", "#   #", "#####", "    #", "    #"},
	'5': {"#####", "#    ", "#### ", "    #", "#### "},
	'6': {" ####", "#    ", "#### ", "#   #", " ### "},
	'7': {"#####", "   # ", "  #  ", " #   ", " #   "},
	'8': {" ### ", "#   #", " ### ", "#   #", " ### "},
	'9': {" ### ", "#   #", " ####", "    #", "#### "},
	' ': {"     ", "     ", "     ", "     ", "     "},
	'-': {"     ", "     ", " ### ", "     ", "     "},
	'_': {"     ", "     ", "     ", "     ", "#####"},
	'.': {"     ", "     ", "     ", "     ", "  #  "},
	':': {"     ", "  #  ", "     ", "  #  ", "     "},
	'+': {"     ", "  #  ", " ### ", "  #  ", "     "},
	'/': {"    #", "   # ", "  #  ", " #   ", "#    "},
	'!': {"  #  ", "  #  ", "  #  ", "     ", "  #  "},
}

// bannerRune is one glyph of the wordmark placed on the banner's
// column grid: the art, plus the column its box starts at and the
// colour it settles to.
type bannerRune struct {
	art   [bannerGlyphRows]string
	col   int
	color color.Color
}

// bannerLayout is the wordmark resolved against the face and the
// theme — everything about the banner that does not depend on the
// animation phase. Computed per render rather than cached: it is a
// handful of map lookups over a wordmark that is never long, and it
// only runs while the transcript is empty.
type bannerLayout struct {
	runes []bannerRune
	width int
}

// layoutBanner resolves text against the block face. ok is false when
// any rune is missing from the face, which is the signal to fall back
// to the plain wordmark line.
//
// Colours come from the same place Styles.RenderWordmark takes them,
// and for the same reason: a theme that defines a WordmarkSequence has
// said something specific about how its brand is coloured, and the
// banner is the largest possible place to honour it. The Google theme
// gets its logo sequence at five rows tall rather than one.
func layoutBanner(text string, styles Styles) (bannerLayout, bool) {
	runes := []rune(strings.ToUpper(text))
	if len(runes) == 0 {
		return bannerLayout{}, false
	}
	seq := styles.Theme.WordmarkSequence
	out := make([]bannerRune, 0, len(runes))
	col := 0
	for i, r := range runes {
		art, found := bannerFace[r]
		if !found {
			return bannerLayout{}, false
		}
		c := styles.Theme.Primary
		if len(seq) > 0 {
			c = seq[i%len(seq)]
		}
		out = append(out, bannerRune{art: art, col: col, color: c})
		col += bannerGlyphWidth + bannerGlyphGap
	}
	// The trailing gutter is not part of the wordmark.
	return bannerLayout{runes: out, width: col - bannerGlyphGap}, true
}

// bannerZone classifies a column against the wipe's leading edge.
type bannerZone int

const (
	bannerLit bannerZone = iota
	bannerEdge
	bannerUnlit
)

// bannerZoneAt reports how the column at x should be painted at the
// given frame. frame >= bannerFrames — and any frame at all once the
// animation has been skipped — puts every column in bannerLit, which
// is the settled wordmark.
//
// The sweep runs one edge-width past the right-hand column so the
// highlight leaves the wordmark rather than stalling on its last
// letter.
func bannerZoneAt(x, width, frame int) bannerZone {
	if frame >= bannerFrames {
		return bannerLit
	}
	// +bannerEdgeCells so the edge exits; +1 so the final frame of the
	// chain still has somewhere to be, rather than aliasing onto the
	// settled state a tick early.
	span := width + bannerEdgeCells + 1
	head := span * (frame + 1) / bannerFrames
	switch {
	case x >= head:
		return bannerUnlit
	case x >= head-bannerEdgeCells:
		return bannerEdge
	default:
		return bannerLit
	}
}

// bannerStyle resolves a cell's zone to the style it paints in.
// settled is the glyph's own colour — the wordmark sequence entry or
// the theme Primary — so the state the banner ends in is exactly the
// state Styles.RenderWordmark would have produced at one row tall.
func bannerStyle(styles Styles, settled color.Color, zone bannerZone) lipgloss.Style {
	switch zone {
	case bannerEdge:
		return lipgloss.NewStyle().Foreground(styles.Theme.Accent).Bold(true)
	case bannerUnlit:
		return lipgloss.NewStyle().Foreground(styles.Theme.FgMuted)
	default:
		return lipgloss.NewStyle().Foreground(settled).Bold(true)
	}
}

// renderBannerBlock paints the layout at a phase. Cells are emitted in
// runs of a single style rather than one style per cell: a five-row
// banner is a few hundred cells, and per-cell SGR would put a kilobyte
// of escapes into every frame of a decorative element.
//
// Runs are keyed on (lit, zone) rather than on the resolved style,
// because two lipgloss.Style values cannot be compared for equality —
// and the pair is what the style is derived from anyway.
func renderBannerBlock(layout bannerLayout, styles Styles, frame int, plain bool) string {
	var b strings.Builder
	for row := 0; row < bannerGlyphRows; row++ {
		if row > 0 {
			b.WriteByte('\n')
		}
		for _, g := range layout.runes {
			line := g.art[row]
			runStart, runOn, runZone := 0, false, bannerLit
			flush := func(end int) {
				if end <= runStart {
					return
				}
				n := end - runStart
				if !runOn {
					b.WriteString(strings.Repeat(" ", n))
					return
				}
				cells := strings.Repeat(bannerCell, n)
				if plain {
					b.WriteString(cells)
					return
				}
				b.WriteString(bannerStyle(styles, g.color, runZone).Render(cells))
			}
			for i := 0; i < bannerGlyphWidth; i++ {
				on := line[i] == bannerOn
				zone := bannerZoneAt(g.col+i, layout.width, frame)
				if i == 0 {
					runOn, runZone = on, zone
					continue
				}
				if on != runOn || zone != runZone {
					flush(i)
					runStart, runOn, runZone = i, on, zone
				}
			}
			flush(bannerGlyphWidth)
			b.WriteString(strings.Repeat(" ", bannerGlyphGap))
		}
	}
	// Every row carries the trailing gutter of its last glyph; strip
	// it so the block measures exactly layout.width and the caller can
	// centre it without a phantom column.
	rows := strings.Split(b.String(), "\n")
	for i, r := range rows {
		rows[i] = strings.TrimRight(r, " ")
	}
	return strings.Join(rows, "\n")
}

// renderBanner returns the startup wordmark for the empty state, or ""
// when the banner should not draw at all — in which case the caller
// renders the hint on its own, exactly as it did before this existed.
//
// width and height are the chat window's, not the terminal's: the
// banner lives inside the transcript and must fit the room the
// transcript actually has.
func (m Model) renderBanner(width, height int) string {
	if m.opts.Branding.DisableBanner {
		return ""
	}
	if height < bannerMinRows {
		return ""
	}
	layout, ok := layoutBanner(m.wordmark(), m.styles)
	if !ok || layout.width > width {
		return ""
	}
	frame := m.bannerFrame
	if m.caps.ReducedMotion {
		// Belt and braces with initialBannerFrame below. That seeds
		// the settled frame at construction, which covers the normal
		// case; this covers a host that overrides Model.caps after
		// NewModel returns, as terminal_caps.go invites it to. Without
		// it that host would get the unlit silhouette and no chain to
		// light it.
		frame = bannerFrames
	}
	// NO_COLOR is a request for text, not for monochrome escapes:
	// paint the block with no styling rather than with a colourless
	// style, so the output is bytes a pipe can read.
	return renderBannerBlock(layout, m.styles, frame, m.caps.NoColor)
}

// initialBannerFrame seeds Model.bannerFrame at construction.
//
// The banner starts *finished* unless it is actually going to be
// animated. That ordering is the whole of the "never delay the first
// usable frame" requirement: whatever reaches the terminal first is a
// complete wordmark, and the wipe is an improvement layered on top by
// a tick chain that may never get to run. Seeding at zero and hoping
// for a tick would mean a host with no animation — reduced motion, a
// harness that renders one frame, a golden test — shipped the unlit
// silhouette as its final answer.
func initialBannerFrame(caps terminalCapabilities, brand Branding) int {
	if brand.DisableBanner || caps.ReducedMotion {
		return bannerFrames
	}
	return 0
}

// bannerTick schedules the next frame of the wipe.
func bannerTick() tea.Cmd {
	return tea.Tick(bannerCadence, func(time.Time) tea.Msg {
		return bannerTickMsg{}
	})
}

// armBanner returns the Cmd that advances the wipe, or nil when there
// is nothing left to advance. Every arming site goes through here so
// the gate cannot be forgotten at one of them — the same discipline
// armSpinner enforces for issue #112, for the same reason.
//
// No generation stamp, unlike the spinner: this chain is armed exactly
// once, from Init, and re-armed only by its own handler. There is no
// second chain for a stale tick to belong to, and the frame counter is
// monotonic, so a duplicate tick would at worst finish the wipe early.
func (m Model) armBanner() tea.Cmd {
	if !m.bannerAnimates() {
		return nil
	}
	return bannerTick()
}

// bannerAnimates reports whether the banner has an animation left to
// run — the gate on both arming the first tick and re-arming the
// chain. False means whatever is on screen is already the settled
// wordmark, which is why nothing needs to schedule a repaint to
// "finish" it.
func (m Model) bannerAnimates() bool {
	if m.opts.Branding.DisableBanner || m.caps.ReducedMotion {
		return false
	}
	if m.bannerFrame >= bannerFrames {
		return false
	}
	// Nothing to animate once the transcript has content: the banner
	// is gone from the tail and a tick would repaint for nobody.
	return m.history.Len() == 0
}
