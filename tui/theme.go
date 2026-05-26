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
// Per-provider themes (AnthropicTheme, GeminiTheme, OpenAITheme)
// expose subtle palette shifts so each LLM provider has visual
// identity in the status surface — same brand language, different
// accent / secondary hues. ThemeForProvider picks based on the
// adapter's StatusReporter.Status().Provider.

package tui

import (
	"image/color"

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

	// Borders + rules.
	BorderActive color.Color // focused input / open dialog
	BorderQuiet  color.Color // sidebar dividers, message rules

	// Diff surfaces: dim tints used as backgrounds behind + / -
	// lines in inline tool-display diffs. Foreground stays
	// Success / Error; the bg makes the change region scannable
	// at a glance the way `git diff --color` and GitHub render.
	DiffAddBg color.Color
	DiffDelBg color.Color
}

// DefaultTheme returns the canonical "core-tui" palette — the
// purple-pink Dracula-adjacent identity used by the visual-
// preview slice and inherited by core-agent's launchTUIv2 today.
// `dark` flips the foreground hierarchy so light terminals stay
// readable.
func DefaultTheme(dark bool) Theme {
	t := Theme{
		Name:         "default",
		Primary:      BrandViolet,
		Secondary:    BrandPink,
		Accent:       BrandViolet,
		Success:      lipgloss.Color("#5FD787"),
		Warning:      lipgloss.Color("#FFD75F"),
		Error:        lipgloss.Color("#FF5F5F"),
		Info:         lipgloss.Color("#A8A8A8"),
		BorderActive: BrandViolet,
		BorderQuiet:  lipgloss.Color("#3A3A3A"),
	}
	if dark {
		t.FgBase = lipgloss.Color("#D0D0D0")
		t.FgMuted = lipgloss.Color("#9A9A9A")
		t.FgSubtle = lipgloss.Color("#6C6C6C")
		t.BgElevated = lipgloss.Color("#1E1E1E")
		t.BgOverlay = lipgloss.Color("#2A2A2A")
		t.DiffAddBg = lipgloss.Color("#1B2D1B")
		t.DiffDelBg = lipgloss.Color("#3A1E1E")
	} else {
		t.FgBase = lipgloss.Color("#1E1E1E")
		t.FgMuted = lipgloss.Color("#5F5F5F")
		t.FgSubtle = lipgloss.Color("#9E9E9E")
		t.BgElevated = lipgloss.Color("#F0F0F0")
		t.BgOverlay = lipgloss.Color("#E5E5E5")
		t.BorderQuiet = lipgloss.Color("#D7D7D7")
		t.DiffAddBg = lipgloss.Color("#E6FFE6")
		t.DiffDelBg = lipgloss.Color("#FFE6E6")
	}
	return t
}

// AnthropicTheme tints toward Claude's warm orange identity. Used
// when the host's StatusReporter reports Provider == "anthropic".
func AnthropicTheme(dark bool) Theme {
	t := DefaultTheme(dark)
	t.Name = "anthropic"
	t.Primary = lipgloss.Color("#D97757") // Claude clay
	t.Secondary = lipgloss.Color("#F0B27A")
	t.Accent = lipgloss.Color("#D97757")
	t.BorderActive = lipgloss.Color("#D97757")
	return t
}

// GeminiTheme tints toward Google's blue/teal palette. Used for
// Provider == "gemini" / "vertex".
func GeminiTheme(dark bool) Theme {
	t := DefaultTheme(dark)
	t.Name = "gemini"
	t.Primary = lipgloss.Color("#4285F4")
	t.Secondary = lipgloss.Color("#5FD7FF")
	t.Accent = lipgloss.Color("#4285F4")
	t.BorderActive = lipgloss.Color("#4285F4")
	return t
}

// OpenAITheme tints toward OpenAI's green identity. Used for
// Provider == "openai".
func OpenAITheme(dark bool) Theme {
	t := DefaultTheme(dark)
	t.Name = "openai"
	t.Primary = lipgloss.Color("#10A37F")
	t.Secondary = lipgloss.Color("#4ECCA3")
	t.Accent = lipgloss.Color("#10A37F")
	t.BorderActive = lipgloss.Color("#10A37F")
	return t
}

// ThemeForProvider returns the per-provider theme variant for
// the given provider tag, or DefaultTheme on empty / unknown.
// The provider string is matched case-insensitively and tolerates
// vendor suffixes ("anthropic-vertex" → anthropic).
func ThemeForProvider(provider string, dark bool) Theme {
	switch {
	case provider == "":
		return DefaultTheme(dark)
	case containsCI(provider, "anthropic"):
		return AnthropicTheme(dark)
	case containsCI(provider, "gemini"), containsCI(provider, "vertex"):
		return GeminiTheme(dark)
	case containsCI(provider, "openai"):
		return OpenAITheme(dark)
	default:
		return DefaultTheme(dark)
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
