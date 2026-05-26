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

	"charm.land/lipgloss/v2"
)

// Brand colors, fixed across light/dark backgrounds (style.md §1.1).
// Hosts override AccentColor / SecondaryColor / CursorColor through
// Options.Branding; the slate and cyan derive deterministically from
// the brand line.
var (
	BrandViolet     = lipgloss.Color("#BD93F9")
	BrandPink       = lipgloss.Color("#FF79C6")
	BrandPinkBright = lipgloss.Color("#FFB6E1")
	BrandSlate      = lipgloss.Color("#6272A4")
	BrandCyan       = lipgloss.Color("#5FD7FF")
)

// Glyph vocabulary (style.md §2). One anchor glyph per row, ever.
const (
	GlyphModel       = "◇"
	// GlyphTool is the inline marker for completed tool calls.
	// Single-cell, text-class (not emoji-class) so terminals
	// render it in the foreground color we asked for instead of
	// the system emoji default.
	GlyphTool        = "›"
	// GlyphToolActive is the inline marker for the in-flight
	// tool call (the most recent RoleTool that hasn't been
	// followed by any text yet). Solid right-pointer reads as
	// "currently running."
	GlyphToolActive  = "▶"
	GlyphToolPending = "○"
	GlyphToolDone    = "✓"
	GlyphToolFail    = "✗"
	GlyphCollapsed   = "▸"
	GlyphExpanded    = "▾"
	GlyphWarn        = "⚠"
	GlyphUserPrompt  = "❯"
	GlyphTruncate    = "…"
	GlyphSeparator   = "·"
	GlyphCursor      = "█"
	GlyphRule        = "─"
	GlyphColumn      = "│"
)

// Styles bundles every resolved lipgloss style for the current
// terminal background. NewStyles picks the variant for light vs dark
// from BackgroundColorMsg.IsDark() at startup (R-MD-2). Theme is
// the semantic-token bundle every per-field style derives from
// (agentic-tui skill §10).
type Styles struct {
	Dark  bool
	Theme Theme

	UserPrefix    lipgloss.Style
	UserText      lipgloss.Style
	AssistantText lipgloss.Style
	SystemText    lipgloss.Style
	ErrorText     lipgloss.Style
	ToolHead      lipgloss.Style
	ToolBody      lipgloss.Style

	Wordmark         lipgloss.Style
	AgentIdentity    lipgloss.Style
	Accent           lipgloss.Style
	Muted            lipgloss.Style
	Rule             lipgloss.Style
	Border           lipgloss.Style
	SidebarDivider   lipgloss.Style
	SidebarHeading   lipgloss.Style
	InputBorderTop   lipgloss.Style
	InputPlaceholder lipgloss.Style
	Footer           lipgloss.Style

	PermissionChip lipgloss.Style
	PermissionWarn lipgloss.Style

	ModalBorder lipgloss.Style
	ModalTitle  lipgloss.Style
	ModalFooter lipgloss.Style
}

// NewStyles assembles the style bundle for the given background
// brightness, applying any Branding overrides on top of the
// DefaultTheme. Hosts that want per-provider tinting should pass
// a theme via NewStylesWithTheme directly.
func NewStyles(dark bool, brand Branding) Styles {
	theme := DefaultTheme(dark)
	if brand.AccentColor != "" {
		c := lipgloss.Color(brand.AccentColor)
		theme.Primary = c
		theme.Accent = c
		theme.BorderActive = c
	}
	if brand.SecondaryColor != "" {
		theme.Secondary = lipgloss.Color(brand.SecondaryColor)
	}
	return NewStylesWithTheme(dark, theme)
}

// NewStylesWithTheme is the per-token construction path: every
// component style derives from the Theme so a palette swap is a
// one-line change (no per-field updates). UserPrefix / UserText
// keep an explicit blue tone — the user-bubble color is semantic
// to the operator's voice and shouldn't shift with provider.
func NewStylesWithTheme(dark bool, theme Theme) Styles {
	var fgUser, border color.Color
	if dark {
		fgUser = lipgloss.Color("#87AFFF")
		border = lipgloss.Color("#5F5F5F")
	} else {
		fgUser = lipgloss.Color("#0050A0")
		border = lipgloss.Color("#BCBCBC")
	}
	muted := theme.FgMuted
	return Styles{
		Dark:             dark,
		Theme:            theme,
		UserPrefix:       lipgloss.NewStyle().Foreground(fgUser).Bold(true),
		UserText:         lipgloss.NewStyle().Foreground(fgUser),
		AssistantText:    lipgloss.NewStyle().Foreground(theme.FgBase),
		SystemText:       lipgloss.NewStyle().Foreground(theme.Info).Italic(true),
		ErrorText:        lipgloss.NewStyle().Foreground(theme.Error),
		ToolHead:         lipgloss.NewStyle().Foreground(theme.Accent).Bold(true),
		ToolBody:         lipgloss.NewStyle().Foreground(muted),
		Wordmark:         lipgloss.NewStyle().Foreground(theme.Primary).Bold(true),
		AgentIdentity:    lipgloss.NewStyle().Foreground(theme.Secondary).Bold(true),
		Accent:           lipgloss.NewStyle().Foreground(theme.Accent).Bold(true),
		Muted:            lipgloss.NewStyle().Foreground(muted),
		Rule:             lipgloss.NewStyle().Foreground(theme.BorderQuiet),
		Border:           lipgloss.NewStyle().Foreground(border),
		SidebarDivider:   lipgloss.NewStyle().Foreground(border),
		SidebarHeading:   lipgloss.NewStyle().Foreground(muted).Bold(true),
		InputBorderTop:   lipgloss.NewStyle().Foreground(border),
		InputPlaceholder: lipgloss.NewStyle().Foreground(muted).Italic(true),
		Footer:           lipgloss.NewStyle().Foreground(muted),
		PermissionChip:   lipgloss.NewStyle().Foreground(theme.Accent),
		PermissionWarn:   lipgloss.NewStyle().Foreground(theme.Warning).Bold(true),
		ModalBorder:      lipgloss.NewStyle().Foreground(theme.BorderActive),
		ModalTitle:       lipgloss.NewStyle().Foreground(theme.Accent).Bold(true),
		ModalFooter:      lipgloss.NewStyle().Foreground(muted),
	}
}
