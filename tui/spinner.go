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

// Pre-rendered gradient spinner frames (agentic-tui skill §7).
// The full reference implementation describes per-ID StepMsg
// routing for multiple concurrent spinners with deterministic
// frame counters. Today we use one spinner (the thinking-line in
// renderSpinnerLine), so we ship the minimum that gets us the
// visual polish — a Braille glyph cycle with color blending —
// while keeping the frame-array shape so per-ID multi-spinner
// expansion is a drop-in later.

package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// brailleSpinnerGlyphs is the 10-frame Braille cycle. Used by
// both the foreground "thinking" spinner and (future) per-
// subagent inline spinners.
var brailleSpinnerGlyphs = []string{
	"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏",
}

// spinnerFrameCache holds the pre-rendered frame strings keyed
// by (theme primary color, brightness). Lipgloss styling per
// frame is cheap but cumulative over many ticks; the cache
// makes the spinner render allocation-free after warm-up.
type spinnerFrameCache struct {
	primary color.Color
	dark    bool
	frames  []string
}

// renderBrailleFrame returns the styled spinner glyph for step.
// Color blends between the theme's Primary and Secondary on a
// 10-frame loop so the glyph reads as "alive" without distracting
// from the verb that follows it. Pre-builds + caches the frames
// the first time it's called for a given (theme, brightness).
func (m *Model) renderBrailleFrame(step int) string {
	primary := m.styles.Theme.Primary
	if m.spinnerCache == nil ||
		m.spinnerCache.primary != primary ||
		m.spinnerCache.dark != m.styles.Dark {
		m.spinnerCache = buildSpinnerFrameCache(primary, m.styles.Theme.Secondary, m.styles.Dark)
	}
	if len(m.spinnerCache.frames) == 0 {
		return ""
	}
	return m.spinnerCache.frames[step%len(m.spinnerCache.frames)]
}

// buildSpinnerFrameCache pre-renders the 10 Braille glyphs with
// a per-frame foreground color blended along a 10-step ramp
// between primary and secondary (lipgloss.Blend1D). Hits the
// allocator once per theme change.
func buildSpinnerFrameCache(primary, secondary color.Color, dark bool) *spinnerFrameCache {
	if primary == nil {
		primary = BrandViolet
	}
	if secondary == nil {
		secondary = BrandPink
	}
	ramp := lipgloss.Blend1D(len(brailleSpinnerGlyphs), primary, secondary)
	frames := make([]string, len(brailleSpinnerGlyphs))
	for i, glyph := range brailleSpinnerGlyphs {
		frames[i] = lipgloss.NewStyle().Foreground(ramp[i]).Bold(true).Render(glyph)
	}
	return &spinnerFrameCache{primary: primary, dark: dark, frames: frames}
}
