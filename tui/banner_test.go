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

// Startup banner tests (issue #165).
//
// Two things are worth testing here and they pull in opposite
// directions. The animation has to actually animate — a wipe that
// renders the same bytes at every frame is the defect issue #162 found
// in the spinner, and it is invisible to any test that looks at one
// frame. And the banner has to stand itself down cleanly in every
// environment that cannot take it, because it is decoration: a
// decoration that breaks a frame is strictly worse than no decoration.
//
// The face-integrity test is the cheap one that pays: the art is 45
// hand-typed 5x5 grids, and a row one character short would otherwise
// surface as a wordmark that shears one column to the left halfway
// down.

package tui

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// bannerModel is an idle, empty-transcript model at a size the banner
// comfortably fits, with the corpus theme so colour assertions do not
// depend on which theme NewModel happened to seed.
func bannerModel(t *testing.T, w, h int) Model {
	t.Helper()
	m := NewModel(Options{Agent: stubAgent{}})
	m.styles = goldenStyles()
	m.caps = terminalCapabilities{}
	m.bannerFrame = 0
	out, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return out.(Model)
}

func TestBannerFace_EveryGlyphIsWellFormed(t *testing.T) {
	for r, art := range bannerFace {
		for row, line := range art {
			if len(line) != bannerGlyphWidth {
				t.Errorf("face %q row %d is %d characters, want exactly %d — a short row shears "+
					"every glyph to its right one column up the banner",
					string(r), row, len(line), bannerGlyphWidth)
			}
			for i := 0; i < len(line); i++ {
				if line[i] != bannerOn && line[i] != ' ' {
					t.Errorf("face %q row %d column %d is %q, want %q or a space",
						string(r), row, i, string(line[i]), string(bannerOn))
				}
			}
		}
	}
	// The face is looked up after strings.ToUpper, so a lowercase key
	// is unreachable art.
	for r := range bannerFace {
		if up := []rune(strings.ToUpper(string(r)))[0]; up != r {
			t.Errorf("face carries %q, which lookup can never reach — layoutBanner upper-cases first", string(r))
		}
	}
}

func TestBannerBlock_IsRectangularAndMatchesItsMeasuredWidth(t *testing.T) {
	styles := goldenStyles()
	for _, text := range []string{"core-tui", "GO-STEER", "acme.dev", "X", "a b"} {
		layout, ok := layoutBanner(text, styles)
		if !ok {
			t.Fatalf("layoutBanner(%q) declined, but every rune is in the face", text)
		}
		rows := strings.Split(renderBannerBlock(layout, styles, bannerFrames, true), "\n")
		if len(rows) != bannerGlyphRows {
			t.Fatalf("%q rendered %d rows, want %d", text, len(rows), bannerGlyphRows)
		}
		widest := 0
		for i, row := range rows {
			w := ansi.StringWidth(row)
			if w > layout.width {
				t.Errorf("%q row %d is %d cells wide, past the %d layoutBanner measured — the caller "+
					"sizes the empty state against that number", text, i, w, layout.width)
			}
			if w > widest {
				widest = w
			}
		}
		// Rows are right-trimmed, so only the widest has to reach the
		// measured width — but one of them must, or the measurement is
		// bigger than the thing it measures.
		if widest != layout.width {
			t.Errorf("%q measures %d cells but its widest row is %d", text, layout.width, widest)
		}
	}
}

// TestBanner_WipeAdvancesEveryFrame is the animation's counterpart to
// the spinner's frame-rate test: it looks at a run of consecutive
// frames, which is the only way to see motion at all.
func TestBanner_WipeAdvancesEveryFrame(t *testing.T) {
	styles := goldenStyles()
	layout, ok := layoutBanner("core-tui", styles)
	if !ok {
		t.Fatal("the default wordmark is not renderable in the block face")
	}
	prev := -1
	for frame := 0; frame <= bannerFrames; frame++ {
		lit := 0
		for x := 0; x < layout.width; x++ {
			if bannerZoneAt(x, layout.width, frame) == bannerLit {
				lit++
			}
		}
		if lit < prev {
			t.Fatalf("frame %d lit %d columns, fewer than frame %d's %d — the wipe went backwards",
				frame, lit, frame-1, prev)
		}
		if frame > 0 && frame < bannerFrames && lit == prev {
			t.Errorf("frame %d lit the same %d columns as frame %d — the wipe stalled, which is what "+
				"a counter advancing slower than the tick looks like", frame, lit, frame-1)
		}
		prev = lit
	}
	if prev != layout.width {
		t.Errorf("the wipe finished with %d of %d columns lit — the last frame is not the settled wordmark",
			prev, layout.width)
	}
}

func TestBanner_SettledFrameHasNoUnlitOrEdgeCells(t *testing.T) {
	styles := goldenStyles()
	layout, _ := layoutBanner("core-tui", styles)
	for x := 0; x < layout.width; x++ {
		if z := bannerZoneAt(x, layout.width, bannerFrames); z != bannerLit {
			t.Fatalf("column %d is zone %d at the settled frame, want bannerLit — the banner never "+
				"stops looking mid-animation", x, z)
		}
	}
	// And the settled bytes are what a one-row wordmark would have
	// used: the muted tone the wipe paints ahead of its edge must be
	// gone entirely.
	settled := renderBannerBlock(layout, styles, bannerFrames, false)
	muted := styles.Muted.Render(bannerCell)
	if strings.Contains(settled, mutedSGR(muted)) {
		t.Error("the settled banner still carries the unlit foreground — the wipe left a column behind")
	}
}

// mutedSGR pulls the leading escape sequence off a styled cell so a
// test can look for that specific foreground in a longer string
// without matching the cell character itself.
func mutedSGR(styled string) string {
	if i := strings.Index(styled, bannerCell); i > 0 {
		return styled[:i]
	}
	return styled
}

func TestBanner_DeclinesRatherThanRenderingAPartialWordmark(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Model)
		w, h   int
	}{{
		name:   "rune the face does not carry",
		mutate: func(m *Model) { m.opts.Branding.Wordmark = "core@tui" },
		w:      120, h: 40,
	}, {
		name:   "wordmark wider than the chat window",
		mutate: func(m *Model) { m.opts.Branding.Wordmark = "averylongproductname" },
		w:      80, h: 40,
	}, {
		name:   "window too short to hold it",
		mutate: func(m *Model) {},
		w:      120, h: 8,
	}, {
		name:   "host opted out",
		mutate: func(m *Model) { m.opts.Branding.DisableBanner = true },
		w:      120, h: 40,
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := bannerModel(t, tc.w, tc.h)
			tc.mutate(&m)
			out, _ := m.Update(tea.WindowSizeMsg{Width: tc.w, Height: tc.h})
			m = out.(Model)
			if got := m.renderBanner(m.viewport.Width(), m.viewport.Height()); got != "" {
				t.Errorf("renderBanner drew %d rows; it should have stood down and left the hint alone",
					strings.Count(got, "\n")+1)
			}
			// The empty state still has to say something.
			frame := ansi.Strip(m.View().Content)
			if !strings.Contains(frame, "Ask me anything to get started.") {
				t.Error("the empty-state hint went missing along with the banner")
			}
		})
	}
}

func TestBanner_ReducedMotionRendersTheSettledFrame(t *testing.T) {
	still := bannerModel(t, 120, 40)
	still.bannerFrame = bannerFrames

	reduced := bannerModel(t, 120, 40)
	reduced.caps.ReducedMotion = true
	reduced.bannerFrame = 0

	w, h := still.viewport.Width(), still.viewport.Height()
	if got, want := reduced.renderBanner(w, h), still.renderBanner(w, h); got != want {
		t.Error("a reduced-motion model at frame 0 did not render the settled wordmark — it would sit " +
			"on the unlit silhouette forever, because nothing is going to tick it forward")
	}
	if reduced.armBanner() != nil {
		t.Error("reduced motion armed the wipe anyway")
	}
	// NewModel is the normal path into that state; it must agree.
	if got := initialBannerFrame(terminalCapabilities{ReducedMotion: true}, Branding{}); got != bannerFrames {
		t.Errorf("initialBannerFrame seeded %d for reduced motion, want the settled %d", got, bannerFrames)
	}
}

func TestBanner_NoColorEmitsNoEscapes(t *testing.T) {
	m := bannerModel(t, 120, 40)
	m.caps.NoColor = true
	got := m.renderBanner(m.viewport.Width(), m.viewport.Height())
	if got == "" {
		t.Fatal("NO_COLOR suppressed the banner entirely; it should render as plain cells")
	}
	if got != ansi.Strip(got) {
		t.Errorf("NO_COLOR banner still carries escape sequences:\n%q", got)
	}
}

// TestBanner_TickChainStopsOnItsOwn is the cost assertion. A banner
// that kept ticking would repaint the whole chat window forever on a
// screen where nothing is happening, which is the objection issue #165
// raises against animating it at all.
func TestBanner_TickChainStopsOnItsOwn(t *testing.T) {
	m := bannerModel(t, 120, 40)
	if m.armBanner() == nil {
		t.Fatal("the wipe never armed, so this test is not measuring anything")
	}
	ticks := 0
	for m.armBanner() != nil {
		ticks++
		if ticks > bannerFrames*4 {
			t.Fatalf("still arming after %d ticks — the chain does not terminate", ticks)
		}
		out, _ := m.Update(bannerTickMsg{})
		m = out.(Model)
	}
	if ticks != bannerFrames {
		t.Errorf("the wipe took %d ticks, want %d — bannerFrames is not the length of the animation",
			ticks, bannerFrames)
	}
	if m.bannerFrame != bannerFrames {
		t.Errorf("chain stopped at frame %d, want %d", m.bannerFrame, bannerFrames)
	}
	// An extra tick after the chain is done must not restart it.
	out, cmd := m.Update(bannerTickMsg{})
	if cmd != nil {
		t.Error("a stray tick past the end re-armed the chain")
	}
	if out.(Model).bannerFrame != bannerFrames {
		t.Error("a stray tick past the end advanced the counter")
	}
}

func TestBanner_TickChainStopsWhenTheTranscriptFills(t *testing.T) {
	m := bannerModel(t, 120, 40)
	if m.armBanner() == nil {
		t.Fatal("the wipe never armed, so this test is not measuring anything")
	}
	m.history.Append(Message{Role: RoleUser, Text: "hello", Rendered: "hello"})
	if m.armBanner() != nil {
		t.Error("the wipe kept ticking after the first message — the banner is off screen by then and " +
			"every tick is a chat-window repaint for nobody")
	}
	m.refreshViewport()
	if strings.Contains(ansi.Strip(m.View().Content), strings.Repeat(bannerCell, 4)) {
		t.Error("the banner is still on screen with a message in the transcript")
	}
}

func TestBanner_AppearsAboveTheHintOnStartup(t *testing.T) {
	m := bannerModel(t, 120, 40)
	m.bannerFrame = bannerFrames
	m.refreshViewport()
	frame := ansi.Strip(m.View().Content)
	block := strings.Index(frame, strings.Repeat(bannerCell, 4))
	hint := strings.Index(frame, "Ask me anything to get started.")
	if block < 0 {
		t.Fatal("no banner in the startup frame")
	}
	if hint < 0 {
		t.Fatal("no empty-state hint in the startup frame")
	}
	if block > hint {
		t.Error("the banner rendered below the hint")
	}
}

// TestGolden_EmptyStateFrame pins the startup screen at the three
// corpus widths, settled.
//
// The rest of the frame corpus seeds three messages, so before this
// there was no capture of the empty state at all — the one screen
// every session begins on. 60 is the interesting width: the default
// wordmark is 47 cells and the chat window at 60 is only a little
// wider, so this is also the capture that would notice the banner
// growing.
func TestGolden_EmptyStateFrame(t *testing.T) {
	pinChromaStyle(t)
	pinCwd(t)
	for _, w := range goldenWidths {
		t.Run("width-"+strconv.Itoa(w), func(t *testing.T) {
			m := goldenEmptyModel(t, w, 24)
			assertGolden(t, "empty_frame_w"+strconv.Itoa(w), m.View().Content)
		})
	}
}

// TestGolden_BannerWipeFrame pins one mid-wipe frame, which is the
// only capture in the corpus where the leading edge's colour and the
// unlit foreground both appear. The edge wants to be in the middle of
// the wordmark rather than against either end, and bannerZoneAt puts
// it at exactly half the span on the frame below — written as an
// expression rather than as a number so the capture stays on the same
// pixels when the frame count moves, which issue #248 made it do.
func TestGolden_BannerWipeFrame(t *testing.T) {
	pinChromaStyle(t)
	pinCwd(t)
	m := goldenEmptyModel(t, 100, 24)
	m.bannerFrame = bannerFrames/2 - 1
	m.refreshViewport()
	assertGolden(t, "banner_wipe_w100", m.View().Content)
}

// goldenEmptyModel is goldenModel with no seeded history and the wipe
// settled — the screen an operator sees on startup once the animation
// is over.
func goldenEmptyModel(t *testing.T, w, h int) Model {
	t.Helper()
	m := NewModel(Options{Agent: &bareAgent{id: "golden"}})
	m.styles = goldenStyles()
	m.caps = terminalCapabilities{}
	m.newlineHint = defaultNewlineHint("")
	m.bannerFrame = bannerFrames
	out, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return out.(Model)
}
