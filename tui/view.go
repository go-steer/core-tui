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
	"github.com/charmbracelet/x/ansi"
)

// sidebarWidth is the fixed-column count of the StatusSidebar panel
// (style.md §5).
const sidebarWidth = 32

// sidebarMinChatWidth is the minimum chat-column count we'll accept
// before forcing a fallback to StatusHeader layout. Below this we
// can't fit useful chat content next to a 32-col sidebar.
const sidebarMinChatWidth = 40

// wordWrap word-wraps s at width cols, preserving ANSI escapes from
// any prior lipgloss styling. Width <= 0 returns s unchanged.
func wordWrap(s string, width int) string {
	if width <= 0 {
		return s
	}
	return ansi.Wordwrap(s, width, " -")
}

// effectiveLayout returns the layout we'll actually render — falls
// back from StatusSidebar to StatusHeader when the terminal is too
// narrow to fit both the sidebar and a useful chat column.
func (m Model) effectiveLayout() StatusLayout {
	if m.statusLayout == StatusSidebar &&
		m.width-sidebarWidth-3 < sidebarMinChatWidth {
		return StatusHeader
	}
	return m.statusLayout
}

// View composes the full TUI. Returns a tea.View with AltScreen on
// and the brand cursor block. Layout is governed by m.statusLayout
// (R-USE-2).
func (m Model) View() tea.View {
	if m.width == 0 || m.height == 0 {
		return tea.NewView("")
	}

	chat := m.viewport.View()
	input := m.renderInputBox()

	var body string
	switch m.effectiveLayout() {
	case StatusSidebar:
		// Footer wraps to the chat column, NOT to m.width — otherwise
		// the left block grows wider than the chat column and the
		// sidebar gets pushed off the right edge of the terminal.
		chatWidth := m.width - sidebarWidth - 3
		footer := m.renderFooter(chatWidth)
		help := m.renderHelpPanel(chatWidth)
		pal := m.renderPalette(chatWidth)
		// Force `left` to exactly chatWidth wide so the sidebar lands
		// at column chatWidth + divider regardless of how short the
		// individual rows are.
		leftParts := []string{chat}
		if help != "" {
			leftParts = append(leftParts, help)
		}
		if pal != "" {
			leftParts = append(leftParts, pal)
		}
		leftParts = append(leftParts, input, footer)
		left := lipgloss.NewStyle().Width(chatWidth).Render(
			lipgloss.JoinVertical(lipgloss.Left, leftParts...),
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
		footer := m.renderFooter(m.width)
		help := m.renderHelpPanel(m.width)
		pal := m.renderPalette(m.width)
		parts := []string{header, chat}
		if help != "" {
			parts = append(parts, help)
		}
		if pal != "" {
			parts = append(parts, pal)
		}
		parts = append(parts, input, footer)
		body = lipgloss.JoinVertical(lipgloss.Left, parts...)
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

	layout := m.effectiveLayout()
	chatWidth := m.width
	if layout == StatusSidebar {
		chatWidth = m.width - sidebarWidth - 3
	}

	const inputHeight = 3
	const footerHeight = 1
	headerHeight := 0
	if layout == StatusHeader {
		headerHeight = 2 // status line + a blank row
	}
	// Allow the footer to wrap onto a second line when the terminal
	// is narrow; reserve up to 2 rows for it. Footer wrap width must
	// match the column it'll render in — chatWidth in sidebar mode,
	// m.width in header mode.
	footerRows := footerHeight
	footerWidth := m.width
	if layout == StatusSidebar {
		footerWidth = chatWidth
	}
	if footerWidth > 0 {
		footerRows = lipgloss.Height(m.renderFooter(footerWidth))
		if footerRows < 1 {
			footerRows = 1
		}
	}
	helpRows := 0
	if m.helpOpen {
		helpRows = lipgloss.Height(m.renderHelpPanel(footerWidth))
	}
	palRows := 0
	if m.palette != nil {
		palRows = lipgloss.Height(m.renderPalette(footerWidth))
	}
	chatHeight := m.height - headerHeight - inputHeight - footerRows - helpRows - palRows - 2 // 2 = input top border + spacer
	if chatHeight < 3 {
		chatHeight = 3
	}

	if chatWidth < 1 {
		chatWidth = 1
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
// + style for its Role (style.md §2 + §4). Output is word-wrapped to
// the viewport width so narrow terminals don't run text off-screen.
func (m Model) renderMessage(msg Message) string {
	width := m.viewport.Width()
	switch msg.Role {
	case RoleUser:
		prefix := m.styles.UserPrefix.Render(GlyphUserPrompt)
		body := wordWrap(msg.Display(), width-2)
		return prefix + " " + m.styles.UserText.Render(body)
	case RoleAssistant:
		body := m.styles.AssistantText.Render(wordWrap(msg.Display(), width))
		if footer := m.renderTurnFooter(msg); footer != "" {
			return body + "\n" + footer
		}
		return body
	case RoleSystem:
		return m.styles.SystemText.Render(wordWrap("ℹ  "+msg.Display(), width))
	case RoleError:
		return m.styles.ErrorText.Render(wordWrap(GlyphWarn+"  "+msg.Display(), width))
	case RoleTool:
		head := m.styles.ToolHead.Render(GlyphTool + " " + msg.ToolName)
		if msg.ToolArgs != "" {
			args := wordWrap(msg.ToolArgs, width-lipgloss.Width(head)-1)
			return head + " " + m.styles.ToolBody.Render(args)
		}
		return head
	}
	return wordWrap(msg.Display(), width)
}

// renderHeader renders the StatusHeader layout's top line — status
// row + a blank spacer row beneath it.
func (m Model) renderHeader() string {
	status := m.renderStatusLine()
	return status + "\n"
}

// renderStatusLine renders the one-line status used in StatusHeader
// (style.md §7.2). Format intentionally puts the spend metrics in a
// human-readable form: `15.2K in · 4.1K out · $0.04 · 9% ctx` rather
// than the bare "9% (19.3K)" which conflated context-fill % with
// total tokens.
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
		m.styles.Muted.Render(fmt.Sprintf(
			"15.2K in %s 4.1K out %s $0.04 %s 19.3K / 200K",
			GlyphSeparator, GlyphSeparator, GlyphSeparator,
		)),
	)
	return strings.Join(parts, "")
}

// renderSidebar renders the StatusSidebar layout's right-hand panel
// (style.md §7.2). Stacks the model + mode + spend metrics in a
// readable vertical layout — separate input/output tokens, context-
// window %, cumulative cost, instead of the prior conflated form.
func (m Model) renderSidebar() string {
	header := lipgloss.JoinVertical(lipgloss.Left,
		"  "+m.styles.AgentIdentity.Render(GlyphModel+" Claude Sonnet 4"),
		"    "+m.styles.Muted.Render(m.permMode.String()),
		"    "+m.styles.Muted.Render("15.2K in "+GlyphSeparator+" 4.1K out"),
		"    "+m.styles.Muted.Render("$0.04 "+GlyphSeparator+" 19.3K / 200K"),
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

// renderFooter renders the bottom keymap legend (style.md §7.1)
// wrapped to width. Pass chatWidth in StatusSidebar mode and m.width
// in StatusHeader mode — wrapping to the wrong width can push the
// right-side panels off-screen.
//
// Only the four essential keys are surfaced by default. Everything
// else (modal shortcuts, layout / mode cycling, newline insertion)
// is discoverable via `?`, mirroring how Antigravity and Claude Code
// keep their footer terse.
func (m Model) renderFooter(width int) string {
	hint := m.opts.Branding.FooterHint
	if hint == "" {
		sep := " " + GlyphSeparator + " "
		hint = "enter submit" + sep + "shift+enter newline" + sep + "ctrl+c quit" + sep + "? for more"
	}
	if width > 0 {
		hint = wordWrap(hint, width)
	}
	return m.styles.Footer.Render(hint)
}

// renderOverlay renders the currently active modal. Content is
// hardcoded sample content for the visual-preview slice; a later
// slice replaces these with real huh-backed forms (per D26).
func (m Model) renderOverlay() string {
	var title, body, footer string
	switch m.overlay {
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

// renderTurnFooter emits the per-turn assistant footer (R-USE-1)
// when the message carries Usage / Model / Elapsed metadata. Empty
// string when no metadata is present so seeded or mid-stream
// messages don't get an empty stub.
func (m Model) renderTurnFooter(msg Message) string {
	if msg.Usage == nil && msg.Model == "" && msg.Elapsed == 0 {
		return ""
	}
	parts := []string{}
	if msg.Model != "" {
		parts = append(parts, GlyphModel+" "+msg.Model)
	}
	if msg.Usage != nil {
		parts = append(parts,
			fmt.Sprintf("%s in", humanTokens(msg.Usage.InputTokens)),
			fmt.Sprintf("%s out", humanTokens(msg.Usage.OutputTokens)),
		)
	}
	if msg.CostUSD > 0 {
		parts = append(parts, fmt.Sprintf("$%.4f", msg.CostUSD))
	}
	if msg.Elapsed > 0 {
		parts = append(parts, msg.Elapsed.Round(100_000_000).String())
	}
	return m.styles.Muted.Italic(true).Render(strings.Join(parts, " "+GlyphSeparator+" "))
}

// renderHelpPanel renders the bottom-anchored stacked help panel when
// m.helpOpen is true. Returns empty string when closed so callers can
// conditionally include it without branching on `if helpOpen` in the
// View() composition. Width sets the column width — pass chatWidth in
// sidebar mode and m.width in header mode.
func (m Model) renderHelpPanel(width int) string {
	if !m.helpOpen || width <= 0 {
		return ""
	}
	sections := []struct {
		title string
		keys  [][2]string
	}{
		{"Input", [][2]string{
			{"enter", "submit"},
			{"shift+enter / ctrl+j", "newline"},
			{"?", "toggle this menu"},
		}},
		{"Palettes (live filter)", [][2]string{
			{"/ (at start)", "slash command palette"},
			{"@ (anywhere)", "project file palette"},
			{"↑ / ↓", "navigate palette"},
			{"tab", "complete prefix"},
			{"enter", "insert selection"},
			{"esc", "close palette"},
		}},
		{"Navigation", [][2]string{
			{"pgup / pgdn", "scroll chat"},
			{"home / end", "top / bottom"},
		}},
		{"Layout & mode", [][2]string{
			{"ctrl+b", "toggle header / sidebar"},
			{"shift+tab", "cycle permission mode"},
		}},
		{"Demo modals (visual preview)", [][2]string{
			{"ctrl+g", "model picker"},
			{"ctrl+y", "permission modal"},
			{"ctrl+e", "MCP elicitation"},
			{"esc", "close any modal"},
		}},
		{"Interrupt / quit", [][2]string{
			{"esc", "interrupt in-flight turn"},
			{"ctrl+c, ctrl+d", "exit"},
		}},
	}

	const keyCol = 24
	rule := m.styles.Rule.Render(strings.Repeat(GlyphRule, width))
	title := m.styles.Accent.Render("Help") + "  " +
		m.styles.Muted.Render("(? to close)")

	lines := []string{rule, title}
	for i, sec := range sections {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "  "+m.styles.SidebarHeading.Render(sec.title))
		for _, kv := range sec.keys {
			key := kv[0]
			pad := keyCol - lipgloss.Width(key)
			if pad < 1 {
				pad = 1
			}
			row := "    " + m.styles.AssistantText.Bold(true).Render(key) +
				strings.Repeat(" ", pad) + m.styles.Muted.Render(kv[1])
			lines = append(lines, row)
		}
	}
	lines = append(lines, rule)
	return strings.Join(lines, "\n")
}

// humanTokens formats an integer token count as a short string
// (4096 → "4.1K", 1_234_567 → "1.2M"). Used in both status and per-
// turn footer (R-USE-1 / R-USE-2).
func humanTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
