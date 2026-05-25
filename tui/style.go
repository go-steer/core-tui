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
	GlyphTool        = "⚙"
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
// from BackgroundColorMsg.IsDark() at startup (R-MD-2).
type Styles struct {
	Dark bool

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
// brightness, applying any Branding overrides.
func NewStyles(dark bool, brand Branding) Styles {
	accent := BrandViolet
	if brand.AccentColor != "" {
		accent = lipgloss.Color(brand.AccentColor)
	}
	secondary := BrandPink
	if brand.SecondaryColor != "" {
		secondary = lipgloss.Color(brand.SecondaryColor)
	}

	var (
		muted, fgUser, fgAssist, fgSystem, fgError color.Color
		border, rule                               color.Color
	)
	if dark {
		muted = lipgloss.Color("#9A9A9A")
		fgUser = lipgloss.Color("#87AFFF")
		fgAssist = lipgloss.Color("#D0D0D0")
		fgSystem = lipgloss.Color("#A8A8A8")
		fgError = lipgloss.Color("#FF5F5F")
		border = lipgloss.Color("#5F5F5F")
		rule = lipgloss.Color("#3A3A3A")
	} else {
		muted = lipgloss.Color("#6C6C6C")
		fgUser = lipgloss.Color("#0050A0")
		fgAssist = lipgloss.Color("#1E1E1E")
		fgSystem = lipgloss.Color("#5F5F5F")
		fgError = lipgloss.Color("#AF0000")
		border = lipgloss.Color("#BCBCBC")
		rule = lipgloss.Color("#D7D7D7")
	}
	warn := lipgloss.Color("#FFD75F")

	return Styles{
		Dark:             dark,
		UserPrefix:       lipgloss.NewStyle().Foreground(fgUser).Bold(true),
		UserText:         lipgloss.NewStyle().Foreground(fgUser),
		AssistantText:    lipgloss.NewStyle().Foreground(fgAssist),
		SystemText:       lipgloss.NewStyle().Foreground(fgSystem).Italic(true),
		ErrorText:        lipgloss.NewStyle().Foreground(fgError),
		ToolHead:         lipgloss.NewStyle().Foreground(accent).Bold(true),
		ToolBody:         lipgloss.NewStyle().Foreground(muted),
		Wordmark:         lipgloss.NewStyle().Foreground(accent).Bold(true),
		AgentIdentity:    lipgloss.NewStyle().Foreground(secondary).Bold(true),
		Accent:           lipgloss.NewStyle().Foreground(accent).Bold(true),
		Muted:            lipgloss.NewStyle().Foreground(muted),
		Rule:             lipgloss.NewStyle().Foreground(rule),
		Border:           lipgloss.NewStyle().Foreground(border),
		SidebarDivider:   lipgloss.NewStyle().Foreground(border),
		SidebarHeading:   lipgloss.NewStyle().Foreground(muted).Bold(true),
		InputBorderTop:   lipgloss.NewStyle().Foreground(border),
		InputPlaceholder: lipgloss.NewStyle().Foreground(muted).Italic(true),
		Footer:           lipgloss.NewStyle().Foreground(muted),
		PermissionChip:   lipgloss.NewStyle().Foreground(accent),
		PermissionWarn:   lipgloss.NewStyle().Foreground(warn).Bold(true),
		ModalBorder:      lipgloss.NewStyle().Foreground(border),
		ModalTitle:       lipgloss.NewStyle().Foreground(accent).Bold(true),
		ModalFooter:      lipgloss.NewStyle().Foreground(muted),
	}
}
