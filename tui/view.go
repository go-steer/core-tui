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
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// sidebarWidth is the fixed-column count of the StatusSidebar panel
// (style.md §5).
const sidebarWidth = 32

// View composes the full TUI. Returns a tea.View with AltScreen on
// and the brand cursor block. Layout is governed by m.statusLayout
// (R-USE-2).
func (m Model) View() tea.View {
	if m.width == 0 || m.height == 0 {
		return tea.NewView("")
	}

	chat := m.viewport.View()
	input := m.renderInputBox()
	footer := m.renderFooter()

	var body string
	switch m.statusLayout {
	case StatusSidebar:
		left := lipgloss.JoinVertical(lipgloss.Left,
			chat,
			input,
			footer,
		)
		sidebar := m.renderSidebar()
		divider := strings.Repeat(GlyphColumn+"\n", lipgloss.Height(left))
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			left,
			m.styles.SidebarDivider.Render(strings.TrimRight(divider, "\n")),
			sidebar,
		)
	default:
		header := m.renderHeader()
		body = lipgloss.JoinVertical(lipgloss.Left,
			header,
			chat,
			input,
			footer,
		)
	}

	// Overlay any active modal centered over the body.
	if m.overlay != overlayNone {
		modal := m.renderOverlay()
		body = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal)
	}

	v := tea.NewView(body)
	v.AltScreen = true
	v.BackgroundColor = nil // respect the terminal's own background
	return v
}

// resize recomputes the viewport and textarea dimensions from the
// current width / height + status layout. Called after WindowSizeMsg
// and after the user toggles StatusHeader <-> StatusSidebar.
func (m *Model) resize() {
	if m.width == 0 || m.height == 0 {
		return
	}

	chatWidth := m.width
	if m.statusLayout == StatusSidebar {
		// Reserve sidebar + one column for the divider + one space of
		// padding on each side of the divider.
		chatWidth = m.width - sidebarWidth - 3
		if chatWidth < 20 {
			chatWidth = 20
		}
	}

	const inputHeight = 3
	const footerHeight = 1
	headerHeight := 0
	if m.statusLayout == StatusHeader {
		headerHeight = 2 // status line + a blank row
	}
	chatHeight := m.height - headerHeight - inputHeight - footerHeight - 2 // 2 = input top border + per-turn rule
	if chatHeight < 3 {
		chatHeight = 3
	}

	m.viewport.SetWidth(chatWidth)
	m.viewport.SetHeight(chatHeight)
	m.input.SetWidth(chatWidth - 2) // leave room for the input border
	m.input.SetHeight(inputHeight)
}

// refreshViewport rebuilds the viewport's content from history. Called
// after any change that affects rendered text (resize, style change,
// new message).
func (m *Model) refreshViewport() {
	if m.width == 0 {
		return
	}
	var b strings.Builder
	entries := m.history.Snapshot()
	rule := m.styles.Rule.Render(strings.Repeat(GlyphRule, m.viewport.Width()))

	for i, msg := range entries {
		if i > 0 {
			// Per-turn rule between user turns and inside the same
			// turn for tool/system rows. style.md §3 keeps the rule
			// rare — only between role transitions to user.
			if msg.Role == RoleUser {
				b.WriteString("\n")
				b.WriteString(rule)
				b.WriteString("\n\n")
			} else {
				b.WriteString("\n\n")
			}
		}
		b.WriteString(m.renderMessage(msg))
	}

	if m.history.Len() == 0 {
		hint := m.opts.Branding.EmptyStateHint
		if hint == "" {
			hint = "Ask me anything to get started."
		}
		b.WriteString(m.styles.SystemText.Render(hint))
	}

	m.viewport.SetContent(b.String())
	m.viewport.GotoBottom()
}

// renderMessage renders a single Message row with the correct glyph
// + style for its Role (style.md §2 + §4).
func (m Model) renderMessage(msg Message) string {
	switch msg.Role {
	case RoleUser:
		prefix := m.styles.UserPrefix.Render(GlyphUserPrompt)
		return prefix + " " + m.styles.UserText.Render(msg.Display())
	case RoleAssistant:
		return m.styles.AssistantText.Render(msg.Display())
	case RoleSystem:
		return m.styles.SystemText.Render("ℹ  " + msg.Display())
	case RoleError:
		return m.styles.ErrorText.Render(GlyphWarn + "  " + msg.Display())
	case RoleTool:
		head := m.styles.ToolHead.Render(GlyphTool + " " + msg.ToolName)
		if msg.ToolArgs != "" {
			return head + " " + m.styles.ToolBody.Render(msg.ToolArgs)
		}
		return head
	}
	return msg.Display()
}

// renderHeader renders the StatusHeader layout's top line — status
// row + a blank spacer row beneath it.
func (m Model) renderHeader() string {
	status := m.renderStatusLine()
	return status + "\n"
}

// renderStatusLine renders the one-line status used in StatusHeader
// (style.md §7.2).
func (m Model) renderStatusLine() string {
	parts := []string{
		m.styles.Wordmark.Render(m.wordmark()),
		m.sep(),
		m.styles.AgentIdentity.Render(GlyphModel + " " + "Claude Sonnet 4"),
	}
	if m.permissionModeWired() {
		parts = append(parts,
			m.sep(),
			m.renderPermissionChip(),
		)
	}
	parts = append(parts,
		m.sep(),
		m.styles.Muted.Render("9% (19.3K) "+GlyphSeparator+" $0.04"),
	)
	return strings.Join(parts, "")
}

// renderSidebar renders the StatusSidebar layout's right-hand panel
// (style.md §7.2).
func (m Model) renderSidebar() string {
	header := lipgloss.JoinVertical(lipgloss.Left,
		"  "+m.styles.AgentIdentity.Render(GlyphModel+" Claude Sonnet 4"),
		"    "+m.styles.Muted.Render(m.permMode.String()+" "+GlyphSeparator+" 9% (19.3K) "+GlyphSeparator+" $0.04"),
	)
	modified := m.sidebarSection("modified files",
		"cmd/foo/main.go     +12 -3",
		"pkg/bar/bar_test.go  +5",
	)
	sub := m.sidebarSection("subagents", "none")
	return lipgloss.JoinVertical(lipgloss.Left, header, "", modified, "", sub)
}

// sidebarSection renders a `─ heading ─` section with body rows.
func (m Model) sidebarSection(heading string, rows ...string) string {
	hr := strings.Repeat(GlyphRule, sidebarWidth-len(heading)-4)
	if hr == "" {
		hr = "─"
	}
	head := "  " + m.styles.SidebarHeading.Render(GlyphRule+" "+heading+" ") + m.styles.Rule.Render(hr)
	body := make([]string, 0, len(rows)+1)
	body = append(body, head)
	for _, r := range rows {
		body = append(body, "    "+m.styles.Muted.Render(r))
	}
	return lipgloss.JoinVertical(lipgloss.Left, body...)
}

// renderPermissionChip renders the permission-mode chip (R-PERM-6).
// The bypassPermissions state uses the warning style.
func (m Model) renderPermissionChip() string {
	if m.permMode == PermissionModeBypass {
		return m.styles.PermissionWarn.Render(m.permMode.String())
	}
	return m.styles.PermissionChip.Render(m.permMode.String())
}

// renderInputBox renders the textarea with a thin top border
// (style.md §5).
func (m Model) renderInputBox() string {
	width := m.viewport.Width()
	if width <= 0 {
		width = m.width
	}
	top := m.styles.InputBorderTop.Render(strings.Repeat(GlyphRule, width))
	return top + "\n" + m.input.View()
}

// renderFooter renders the bottom keymap legend (style.md §7.1).
func (m Model) renderFooter() string {
	hint := m.opts.Branding.FooterHint
	if hint == "" {
		hint = "ctrl+c quit " + GlyphSeparator + " ctrl+b toggle layout " + GlyphSeparator +
			" shift+tab cycle perm-mode " + GlyphSeparator + " ctrl+p palette " + GlyphSeparator +
			" ctrl+g model " + GlyphSeparator + " ctrl+y permission " + GlyphSeparator + " ctrl+e elicit"
	}
	return m.styles.Footer.Render(hint)
}

// renderOverlay renders the currently active modal. Content is
// hardcoded sample content for the visual-preview slice; a later
// slice replaces these with real huh-backed forms (per D26).
func (m Model) renderOverlay() string {
	var title, body, footer string
	switch m.overlay {
	case overlayPalette:
		title = "Commands"
		body = strings.Join([]string{
			"> ",
			"",
			m.styles.Accent.Render("> /model") + m.styles.Muted.Render("        switch the active model"),
			"  /clear      " + m.styles.Muted.Render("clear chat history"),
			"  /help       " + m.styles.Muted.Render("show command reference"),
			"  /reload     " + m.styles.Muted.Render("re-read .agents/ from disk"),
			"  /quit       " + m.styles.Muted.Render("exit"),
		}, "\n")
		footer = "↑↓ choose " + GlyphSeparator + " enter select " + GlyphSeparator + " esc cancel"
	case overlayModelPicker:
		title = "Choose a Model"
		body = strings.Join([]string{
			m.styles.Muted.Render("Anthropic"),
			"  " + m.styles.Accent.Render("> Claude Opus 4.7"),
			"  Claude Sonnet 4.6",
			"  Claude Haiku 4.5",
			"",
			m.styles.Muted.Render("Google"),
			"  Gemini 3.5 Flash",
			"  Gemini 3.5 Pro",
		}, "\n")
		footer = "↑↓ choose " + GlyphSeparator + " enter accept " + GlyphSeparator + " esc cancel"
	case overlayPermission:
		title = "Permission Required"
		body = strings.Join([]string{
			m.styles.Muted.Render("Tool ") + m.styles.Accent.Render("Write"),
			m.styles.Muted.Render("File ") + "internal/auth/session.go",
			"",
			m.styles.SystemText.Render("Diff (preview):"),
			m.styles.ErrorText.Render("- if user.Email == \"\" {"),
			m.styles.SystemText.Render("+ if user.Email == \"\" || !strings.Contains(user.Email, \"@\") {"),
			"",
			"  [" + m.styles.Accent.Render("Allow once") + "]  [Allow for session]  [Allow always]  [Deny]",
		}, "\n")
		footer = "1/2/3/4 choose " + GlyphSeparator + " esc cancel"
	case overlayElicit:
		title = "MCP server requests input"
		body = strings.Join([]string{
			m.styles.Muted.Render("Server ") + m.styles.Accent.Render("github"),
			"",
			fmt.Sprintf("  %-14s %s", "Repository:", m.styles.Accent.Render("go-steer/core-tui")),
			fmt.Sprintf("  %-14s %s", "Branch:", "main"),
			fmt.Sprintf("  %-14s %s", "Confirm:", "[•] yes  [ ] no"),
		}, "\n")
		footer = "tab next field " + GlyphSeparator + " enter submit " + GlyphSeparator + " esc decline"
	}

	width := 64
	if width > m.width-4 {
		width = m.width - 4
	}

	titleBar := m.styles.ModalTitle.Render(title)
	rule := m.styles.ModalBorder.Render(strings.Repeat(GlyphRule, width-len(title)-3))
	titleLine := titleBar + " " + rule
	footerRule := m.styles.ModalBorder.Render(strings.Repeat(GlyphRule, width-2))
	footerLine := m.styles.ModalFooter.Render(footer)

	content := lipgloss.JoinVertical(lipgloss.Left,
		titleLine,
		"",
		body,
		"",
		footerRule,
		footerLine,
	)
	return m.styles.ModalBorder.Padding(0, 1).Width(width).Render(content)
}

// sep returns the dim ` · ` separator used in status assembly.
func (m Model) sep() string {
	return m.styles.Muted.Render(" " + GlyphSeparator + " ")
}
