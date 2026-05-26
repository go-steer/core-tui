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
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// dispatchBuiltinSlash handles every slash command the TUI owns
// itself (i.e. doesn't delegate to the agent's SlashProvider). Returns
// (handled=true, ...) when a built-in matched and was processed; the
// caller (dispatchSlash) treats handled=false as "fall through to the
// agent's SlashProvider".
//
// Each built-in either renders a system message into history or
// mutates model state (clear, quit, interrupt). Capability-checked
// built-ins (/tools, /model, /reload, /permissions, /pricing) probe
// the Agent via type assertion and degrade to a "not available"
// system message when the host hasn't wired the capability.
//
// Operator-initiated: slash commands always GotoBottom after the
// response renders so the operator sees the result even if they'd
// scrolled up reading backlog (refreshViewport alone preserves
// scroll position by design). The scroll lives in refreshAndScroll
// — each case calls it explicitly instead of refreshViewport.
func (m Model) dispatchBuiltinSlash(name, args string) (bool, tea.Model, tea.Cmd) {
	// Alias normalization so internal/tui muscle memory carries
	// over: /models→/model, /perms→/permissions, /by-the-way→/btw,
	// /sub→/subagent. /q, /exit, /int are handled in their dispatch
	// cases below.
	switch name {
	case "models":
		name = "model"
	case "perms":
		name = "permissions"
	case "by-the-way":
		name = "btw"
	case "sub":
		name = "subagent"
	}

	switch name {
	case "help", "?":
		m.history.Append(Message{Role: RoleSystem, Text: m.renderBuiltinHelp()})
		m.input.Reset()
		m.refreshAndScroll()
		return true, m, nil

	case "clear":
		// Two-step confirmation: arming sets confirmingClear so the
		// next Enter is interpreted as y/yes (wipe) or anything-else
		// (cancel). Footer hint flips while armed so the operator
		// sees what's expected.
		m.confirmingClear = true
		m.input.Reset()
		m.history.Append(Message{Role: RoleSystem, Text: "clear chat history? press enter for y/yes — anything else cancels"})
		m.refreshAndScroll()
		return true, m, nil

	case "quit", "exit", "q":
		m.input.Reset()
		return true, m, tea.Quit

	case "memory":
		m.history.Append(Message{Role: RoleSystem, Text: m.renderMemoryList(m.opts.Memory)})
		m.input.Reset()
		m.refreshAndScroll()
		return true, m, nil

	case "mcp":
		m.history.Append(Message{Role: RoleSystem, Text: m.renderMCPList(m.opts.MCPServers)})
		m.input.Reset()
		m.refreshAndScroll()
		return true, m, nil

	case "skills":
		m.history.Append(Message{Role: RoleSystem, Text: m.renderSkillList(m.opts.Skills)})
		m.input.Reset()
		m.refreshAndScroll()
		return true, m, nil

	case "stats":
		m.history.Append(Message{Role: RoleSystem, Text: m.renderStats()})
		m.input.Reset()
		m.refreshAndScroll()
		return true, m, nil

	case "mouse":
		// Mouse capture is enabled by the program harness at startup
		// (program.go); the TUI has no per-session toggle yet. Surface
		// the limitation honestly so the operator knows the slash isn't
		// silently dropped.
		m.history.Append(Message{Role: RoleSystem, Text: "/mouse: mouse-capture toggle is not yet wired — the host program controls it at startup"})
		m.input.Reset()
		m.refreshAndScroll()
		return true, m, nil

	case "interrupt", "int":
		if m.state != stateStreaming || m.cancelTurn == nil {
			m.history.Append(Message{Role: RoleSystem, Text: "/interrupt: no turn in flight"})
			m.input.Reset()
			m.refreshAndScroll()
			return true, m, nil
		}
		m.cancelTurn()
		m.input.Reset()
		return true, m, nil

	case "tools":
		lister, ok := m.opts.Agent.(ToolLister)
		if !ok {
			m.history.Append(Message{Role: RoleSystem, Text: "/tools: agent doesn't implement ToolLister"})
		} else {
			m.history.Append(Message{Role: RoleSystem, Text: m.renderToolList(lister.Tools())})
		}
		m.input.Reset()
		m.refreshAndScroll()
		return true, m, nil

	case "model":
		swapper, ok := m.opts.Agent.(ModelSwapper)
		if !ok {
			m.history.Append(Message{Role: RoleSystem, Text: "/model: agent doesn't implement ModelSwapper"})
			m.input.Reset()
			m.refreshAndScroll()
			return true, m, nil
		}
		if args == "" {
			// No-arg form opens the interactive picker dialog
			// (mirrors Ctrl+G). Singleton — re-open while
			// already showing is a no-op.
			if !m.overlayStack.HasID(modelPickerDialogID) {
				m.overlayStack.Open(newModelPickerDialog())
			}
			m.input.Reset()
			return true, m, nil
		}
		// `/model <id>` switches without opening a picker — useful for
		// scripted/replay flows and as a fallback while the picker
		// modal is still being built.
		newAgent, err := swapper.SwitchModel(args)
		if err != nil {
			m.history.Append(Message{Role: RoleError, Text: "/model: switch failed: " + err.Error()})
			m.input.Reset()
			m.refreshAndScroll()
			return true, m, nil
		}
		m.opts.Agent = newAgent
		m.history.Append(Message{Role: RoleSystem, Text: "/model: switched to " + args})
		if m.opts.PersistModelChoice != nil {
			if perr := m.opts.PersistModelChoice(args); perr != nil {
				m.history.Append(Message{Role: RoleError, Text: "/model: persist failed: " + perr.Error()})
			}
		}
		m.refreshTheme()
		m.input.Reset()
		m.refreshAndScroll()
		return true, m, nil

	case "reload":
		reloader, ok := m.opts.Agent.(Reloader)
		if !ok {
			m.history.Append(Message{Role: RoleSystem, Text: "/reload: agent doesn't implement Reloader"})
			m.input.Reset()
			m.refreshAndScroll()
			return true, m, nil
		}
		res, err := reloader.Reload(context.Background())
		if err != nil {
			m.history.Append(Message{Role: RoleError, Text: "/reload: " + err.Error()})
			m.input.Reset()
			m.refreshAndScroll()
			return true, m, nil
		}
		if res.Agent != nil {
			m.opts.Agent = res.Agent
		}
		if res.Memory != nil {
			m.opts.Memory = res.Memory
		}
		if res.MCPServers != nil {
			m.opts.MCPServers = res.MCPServers
		}
		if res.Skills != nil {
			m.opts.Skills = res.Skills
		}
		note := res.Note
		if note == "" {
			note = "/reload: reloaded"
		}
		m.history.Append(Message{Role: RoleSystem, Text: note})
		m.input.Reset()
		m.refreshAndScroll()
		return true, m, nil

	case "permissions":
		ctrl, ok := m.opts.Agent.(PermissionController)
		if !ok {
			m.history.Append(Message{Role: RoleSystem, Text: "/permissions: agent doesn't implement PermissionController"})
		} else {
			m.history.Append(Message{Role: RoleSystem, Text: renderApprovalLog(ctrl.SessionApprovals())})
		}
		m.input.Reset()
		m.refreshAndScroll()
		return true, m, nil

	case "pricing":
		ctrl, ok := m.opts.Agent.(PricingController)
		if !ok {
			m.history.Append(Message{Role: RoleSystem, Text: "/pricing: agent doesn't implement PricingController"})
			m.input.Reset()
			m.refreshAndScroll()
			return true, m, nil
		}
		text := m.handlePricing(ctrl, args)
		m.history.Append(Message{Role: RoleSystem, Text: text})
		m.input.Reset()
		m.refreshAndScroll()
		return true, m, nil

	case "subagents":
		lister, ok := m.opts.Agent.(SubagentLister)
		if !ok {
			m.history.Append(Message{Role: RoleSystem, Text: "/subagents: agent doesn't implement SubagentLister"})
		} else {
			m.history.Append(Message{Role: RoleSystem, Text: renderSubagentList(lister.Subagents())})
		}
		m.input.Reset()
		m.refreshAndScroll()
		return true, m, nil

	case "keys":
		m.history.Append(Message{Role: RoleSystem, Text: m.renderKeysDiagnostic()})
		m.input.Reset()
		m.refreshAndScroll()
		return true, m, nil

	case "resume":
		text := m.handleResume(args)
		m.history.Append(Message{Role: RoleSystem, Text: text})
		m.input.Reset()
		m.refreshAndScroll()
		return true, m, nil

	case "allow":
		text := m.handleAllowDeny(args, "allow")
		m.history.Append(Message{Role: RoleSystem, Text: text})
		m.input.Reset()
		m.refreshAndScroll()
		return true, m, nil

	case "deny":
		text := m.handleAllowDeny(args, "deny")
		m.history.Append(Message{Role: RoleSystem, Text: text})
		m.input.Reset()
		m.refreshAndScroll()
		return true, m, nil
	}

	return false, m, nil
}

// renderKeysDiagnostic prints what the TUI knows about the
// operator's terminal + its keyboard quirks: detected term
// program, capability bits, and which newline keystroke we'd
// recommend (with the observed-active one called out when the
// operator has already used one). Operators reach for this when
// shift+enter / ctrl+j don't work and they want to know what
// to try.
func (m *Model) renderKeysDiagnostic() string {
	var b strings.Builder
	b.WriteString("Terminal & keyboard diagnostic:\n\n")

	term := m.caps.TermProgram
	if term == "" {
		term = "(unknown — TERM_PROGRAM not set)"
	}
	fmt.Fprintf(&b, "  %-22s %s\n", "Terminal program:", m.itemNameStyle().Render(term))
	fmt.Fprintf(&b, "  %-22s %t\n", "True color:", m.caps.TrueColor)
	fmt.Fprintf(&b, "  %-22s %t\n", "OSC 8 hyperlinks:", m.caps.Hyperlinks)
	fmt.Fprintf(&b, "  %-22s %t\n", "OSC 52 clipboard:", m.caps.Clipboard)
	fmt.Fprintf(&b, "  %-22s %t\n", "Kitty graphics:", m.caps.KittyGraphics)

	b.WriteString("\nNewline keystroke:\n\n")
	recommended := defaultNewlineHint(m.caps.TermProgram)
	active := m.newlineHint
	if active == "" {
		active = recommended
	}
	fmt.Fprintf(&b, "  %-22s %s", "Recommended default:", m.itemNameStyle().Render(recommended))
	if active != recommended {
		b.WriteString(m.styles.Muted.Render("  (observed override)"))
	}
	b.WriteByte('\n')
	fmt.Fprintf(&b, "  %-22s %s\n", "Currently in use:", m.itemNameStyle().Render(active))

	b.WriteString("\nAll combos core-tui accepts for newline (try each if the others don't work):\n")
	for _, combo := range []string{"shift+enter", "ctrl+j", "alt+enter"} {
		marker := "  • "
		if combo == active {
			marker = "  " + m.styles.Accent.Render("▶ ")
		}
		fmt.Fprintf(&b, "%s%s\n", marker, m.itemNameStyle().Render(combo))
	}

	b.WriteString("\nWhich one works depends on the terminal:\n")
	b.WriteString("  • VS Code integrated terminal: alt+enter — requires a keybindings.json\n")
	b.WriteString("    entry binding shift+enter to send `\\u001b\\r` when terminalFocus,\n")
	b.WriteString("    e.g.:\n")
	b.WriteString("        { \"key\": \"shift+enter\", \"command\": \"workbench.action.terminal.sendSequence\",\n")
	b.WriteString("          \"args\": { \"text\": \"\\u001b\\r\" },\n")
	b.WriteString("          \"when\": \"terminalFocus\" }\n")
	b.WriteString("  • kitty / wezterm / iTerm2 with keyboard-enhancement: shift+enter works natively\n")
	b.WriteString("  • everything else (gnome-terminal, alacritty, tmux): ctrl+j\n")
	b.WriteString("\nTip: the footer hint auto-updates to the first combo you actually use.")
	return strings.TrimRight(b.String(), "\n")
}

// handleResume implements /resume:
//
//	/resume          → list recent transcripts under AgentsDir/sessions
//	/resume <path>   → load that transcript into the current model
//
// Loading replaces history wholesale + re-renders assistant
// markdown at the current viewport width (ApplyTranscript handles
// the cache reset). Doesn't restore in-flight turn / queue /
// modal state — a resumed session starts idle.
func (m *Model) handleResume(args string) string {
	args = strings.TrimSpace(args)
	if m.opts.AgentsDir == "" {
		return "/resume: no AgentsDir wired (host did not pass Options.AgentsDir)"
	}
	if args == "" {
		infos, err := ListTranscripts(m.opts.AgentsDir)
		if err != nil {
			return "/resume: list failed: " + err.Error()
		}
		if len(infos) == 0 {
			return "/resume: no saved sessions in " + m.opts.AgentsDir + "/sessions"
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Saved sessions (%d) — use /resume <path>:\n\n", len(infos))
		for i, info := range infos {
			if i >= 10 {
				fmt.Fprintf(&b, "  %s and %d older\n", GlyphTruncate, len(infos)-10)
				break
			}
			fmt.Fprintf(&b, "  %s %s  %s  %s\n",
				GlyphCollapsed,
				m.itemNameStyle().Render(info.Name),
				m.styles.Muted.Render(formatFileSize(info.Size)),
				m.styles.Muted.Render(info.ModTime.Format("2006-01-02 15:04")),
			)
		}
		return strings.TrimRight(b.String(), "\n")
	}
	// Argument is a path. Accept absolute, relative-to-cwd, or a
	// bare filename (resolved against AgentsDir/sessions).
	path := args
	if _, err := os.Stat(path); err != nil {
		path = filepath.Join(m.opts.AgentsDir, transcriptSessionsDir, args)
	}
	t, err := LoadTranscript(path)
	if err != nil {
		return "/resume: " + err.Error()
	}
	m.ApplyTranscript(t)
	return fmt.Sprintf("/resume: loaded %s (%d messages, model=%s)", filepath.Base(path), len(t.Messages), t.Model)
}

// handleAllowDeny dispatches /allow + /deny to the PermissionController
// capability. Two arg shapes:
//
//	/allow <pattern>            → AddAllowPatterns([pattern])
//	/allow bundle:<bundle-name> → AddBuiltinAllowExtra(bundle-name)
//	/deny  <pattern>            → AddDenyPatterns([pattern])
//
// bundle:<name> is allow-only because the gate has no built-in deny
// bundles. The returned text is the system-message body the caller
// renders.
func (m Model) handleAllowDeny(args, op string) string {
	ctrl, ok := m.opts.Agent.(PermissionController)
	if !ok {
		return "/" + op + ": agent doesn't implement PermissionController"
	}
	args = strings.TrimSpace(args)
	if args == "" {
		hint := "<pattern>   e.g. /" + op + " bash:git *"
		if op == "allow" {
			hint += "   or   /allow bundle:dev_tools"
		}
		return "/" + op + ": usage — /" + op + " " + hint
	}
	if op == "allow" && strings.HasPrefix(args, "bundle:") {
		name := strings.TrimPrefix(args, "bundle:")
		if name == "" {
			return "/allow bundle: empty bundle name — try /allow bundle:dev_tools"
		}
		if err := ctrl.AddBuiltinAllowExtra(name); err != nil {
			return "/allow bundle: " + err.Error()
		}
		return "/allow: enabled bundle " + name
	}
	var err error
	if op == "allow" {
		err = ctrl.AddAllowPatterns([]string{args})
	} else {
		err = ctrl.AddDenyPatterns([]string{args})
	}
	if err != nil {
		return "/" + op + ": " + err.Error()
	}
	return "/" + op + ": added " + args
}

// handlePricing parses the /pricing subcommand and dispatches.
// Three shapes:
//
//	/pricing refresh              → re-pull the upstream price table
//	/pricing set                  → open the embedded huh.Form
//	/pricing set <id> <in> <out>  → direct positional override
//
// The form path (no positional args) lets operators tab through
// validated fields; the positional path keeps scripted / replay
// flows fast. Returns the text to echo as a system message; for
// the form path returns "" because the form takes over the
// screen (its own completion handler dispatches the result).
func (m *Model) handlePricing(ctrl PricingController, args string) string {
	args = strings.TrimSpace(args)
	if args == "" || args == "help" {
		return "/pricing: usage — /pricing refresh OR /pricing set (form) OR /pricing set <model-id> <input-per-mtok> <output-per-mtok>"
	}
	sub, rest, _ := strings.Cut(args, " ")
	switch sub {
	case "refresh":
		summary, err := ctrl.Refresh(context.Background())
		if err != nil {
			return "/pricing refresh: " + err.Error()
		}
		return summary
	case "set":
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			// No positional args → open the embedded huh form.
			// updatePricingForm dispatches PricingController.Set
			// on submit, so we don't echo anything from here.
			// Form width: clamp to a comfortable max but never
			// wider than the modal can hold (border + padding eat
			// ~6 cols).
			formWidth := m.viewport.Width() - 8
			if formWidth > 72 {
				formWidth = 72
			}
			m.pendingForm = newPricingForm(m.displayModelName(), formWidth)
			return "/pricing: opening form (esc cancels)"
		}
		if len(fields) != 3 {
			return "/pricing set: want <model-id> <input-per-mtok> <output-per-mtok>, or pass nothing to open the form"
		}
		in, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			return "/pricing set: invalid input rate " + fields[1] + " — " + err.Error()
		}
		out, err := strconv.ParseFloat(fields[2], 64)
		if err != nil {
			return "/pricing set: invalid output rate " + fields[2] + " — " + err.Error()
		}
		summary, err := ctrl.Set(fields[0], in, out)
		if err != nil {
			return "/pricing set: " + err.Error()
		}
		return summary
	default:
		return "/pricing: unknown subcommand " + sub + " — try /pricing refresh or /pricing set"
	}
}

// renderBuiltinHelp produces the /help text. Lists every built-in plus
// the host-provided commands from SlashProvider.SlashCommands(). The
// rendered output is a single block — the renderer will glamour-ify
// it on the next viewport refresh.
func (m Model) renderBuiltinHelp() string {
	var b strings.Builder
	b.WriteString("Built-in commands:\n")
	b.WriteString("  /help, /?            — show this reference\n")
	b.WriteString("  /clear               — clear chat history\n")
	b.WriteString("  /quit, /exit, /q     — exit\n")
	b.WriteString("  /memory              — display loaded memory files\n")
	b.WriteString("  /mcp                 — configured MCP servers\n")
	b.WriteString("  /skills              — loaded skill bundles\n")
	b.WriteString("  /stats               — per-turn + session usage totals\n")
	b.WriteString("  /tools               — list tools and gate state\n")
	b.WriteString("  /model [<id>]        — list models or switch to <id>\n")
	b.WriteString("  /reload              — rebuild agent from disk\n")
	b.WriteString("  /permissions         — review session approvals\n")
	b.WriteString("  /pricing refresh|set — manage cost rates\n")
	b.WriteString("  /subagents           — list background subagents\n")
	b.WriteString("  /interrupt, /int     — cancel the in-flight turn\n")
	b.WriteString("  /mouse               — (placeholder)\n")

	if provider, ok := m.opts.Agent.(SlashProvider); ok {
		specs := provider.SlashCommands()
		if len(specs) > 0 {
			b.WriteString("\nAgent commands:\n")
			for _, s := range specs {
				name := "/" + s.Name
				if len(s.Aliases) > 0 {
					for _, a := range s.Aliases {
						name += ", /" + a
					}
				}
				b.WriteString("  " + padRight(name, 20) + " — " + s.Description + "\n")
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// padRight pads s with spaces on the right up to width. Used by /help
// to align the command-name column.
func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

// Style helpers — bold accent (violet) for section headings, bold
// secondary (pink) for tool / server item names. Mirrors the
// internal/tui look so operators don't see a downgrade switching
// adapters.
func (m Model) headingStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(BrandViolet).Bold(true)
}

func (m Model) itemNameStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(BrandPink).Bold(true)
}

func (m Model) renderMemoryList(files []MemoryFile) string {
	if len(files) == 0 {
		return "No memory files loaded. Drop AGENTS.md / CLAUDE.md / GEMINI.md in the project or user-home tree to surface them here."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Memory files (%d):\n\n", len(files))
	for i, f := range files {
		fmt.Fprintf(&b, "  %s %s", GlyphCollapsed, m.itemNameStyle().Render(f.Path))
		if f.Bytes > 0 || f.Truncated {
			annotation := formatFileSize(f.Bytes)
			if f.Truncated {
				annotation += ", truncated"
			}
			fmt.Fprintf(&b, "  %s", m.styles.Muted.Render("("+annotation+")"))
		}
		b.WriteByte('\n')
		if f.Excerpt != "" {
			fmt.Fprintf(&b, "      %s\n", strings.ReplaceAll(f.Excerpt, "\n", " "))
		}
		if i < len(files)-1 {
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderMCPList groups tools under their owning server (bold violet
// header + ▸ pink tool name + indented description) so /mcp shows the
// full catalog instead of just a per-server tool count. Falls back to
// the count when the server provides no per-tool detail.
func (m Model) renderMCPList(servers []MCPServerInfo) string {
	if len(servers) == 0 {
		return "No MCP servers configured. Drop a .agents/mcp.json describing servers (stdio or HTTP transport) to expose external tools to the agent."
	}
	var b strings.Builder
	b.WriteString("MCP servers:\n\n")
	for si, s := range servers {
		status := "connected"
		if !s.Connected {
			status = "disconnected"
		}
		fmt.Fprintf(&b, "  %s — %s", m.headingStyle().Render(s.Name), status)
		if s.Transport != "" {
			fmt.Fprintf(&b, " (%s)", s.Transport)
		}
		if s.URL != "" {
			fmt.Fprintf(&b, " %s", s.URL)
		}
		b.WriteByte('\n')

		switch {
		case !s.Connected:
			// Skip tool list for disconnected servers.
		case len(s.Tools) == 0 && s.ToolCount == 0:
			b.WriteString("      (server exposes no tools, or enumeration failed)\n")
		case len(s.Tools) > 0:
			tools := make([]MCPToolInfo, len(s.Tools))
			copy(tools, s.Tools)
			sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
			b.WriteByte('\n')
			for i, t := range tools {
				fmt.Fprintf(&b, "    %s %s\n", GlyphCollapsed, m.itemNameStyle().Render(t.Name))
				if t.Description != "" {
					fmt.Fprintf(&b, "        %s\n", strings.ReplaceAll(t.Description, "\n", " "))
				}
				if i < len(tools)-1 {
					b.WriteByte('\n')
				}
			}
		default:
			// Server reported ToolCount but no per-tool details.
			fmt.Fprintf(&b, "      %d tool(s)\n", s.ToolCount)
		}
		if si < len(servers)-1 {
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m Model) renderSkillList(skills []SkillInfo) string {
	if len(skills) == 0 {
		return "No skills discovered. Drop SKILL.md bundles under .agents/skills/<name>/ to expose them to the agent."
	}
	sorted := make([]SkillInfo, len(skills))
	copy(sorted, skills)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	var b strings.Builder
	fmt.Fprintf(&b, "Skills (%d):\n\n", len(sorted))
	for i, s := range sorted {
		fmt.Fprintf(&b, "  %s %s", GlyphCollapsed, m.itemNameStyle().Render(s.Name))
		if s.Source != "" && s.Source != "local" {
			fmt.Fprintf(&b, " [%s]", s.Source)
		}
		b.WriteByte('\n')
		if s.Description != "" {
			fmt.Fprintf(&b, "      %s\n", strings.ReplaceAll(s.Description, "\n", " "))
		}
		if i < len(sorted)-1 {
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderStats expands internal/tui's /stats layout: turns +
// session duration + per-direction tokens + cost + context fill +
// model name. Each value falls back to "(unknown)" / "(unset)"
// rather than zero so the operator can tell "we don't know" from
// "the value is genuinely zero."
func (m Model) renderStats() string {
	tracker := m.opts.UsageTracker
	if tracker == nil {
		return "/stats: no UsageTracker wired (host did not pass Options.UsageTracker)"
	}
	totals := tracker.SessionTotals()
	cost := tracker.SessionCostUSD()
	last, lastCost := tracker.LastTurn()
	winUsed := tracker.ContextWindowUsed()
	winSize := tracker.ContextWindowSize()
	turns := tracker.SessionTurns()
	dur := tracker.SessionDuration()

	var b strings.Builder
	b.WriteString("Session stats:\n")
	if turns > 0 {
		fmt.Fprintf(&b, "  Turns:      %d\n", turns)
	}
	if dur > 0 {
		fmt.Fprintf(&b, "  Duration:   %s\n", dur.Round(time.Second))
	}
	fmt.Fprintf(&b, "  Tokens:     %d in / %d out\n", totals.InputTokens, totals.OutputTokens)
	fmt.Fprintf(&b, "  Cost:       $%.4f\n", cost)
	if winSize > 0 {
		fmt.Fprintf(&b, "  Context:    %d / %d tokens (%d%%)\n", winUsed, winSize, (winUsed*100)/winSize)
	} else {
		b.WriteString("  Context:    (unknown)\n")
	}
	fmt.Fprintf(&b, "  Last turn:  %d in / %d out · $%.4f\n", last.InputTokens, last.OutputTokens, lastCost)
	if model := m.displayModelName(); model != "" {
		fmt.Fprintf(&b, "  Model:      %s\n", model)
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderToolList renders the agent's tool catalog in alphabetical
// order: bold pink name on its own line with a ▸ marker, source +
// gate annotation in muted brackets next to it, indented description
// underneath, blank line between entries — matches internal/tui's
// /tools layout so the catalog is scannable.
func (m Model) renderToolList(tools []ToolInfo) string {
	if len(tools) == 0 {
		return "Agent has no tools registered."
	}
	sorted := make([]ToolInfo, len(tools))
	copy(sorted, tools)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	var b strings.Builder
	fmt.Fprintf(&b, "Tools (%d):\n\n", len(sorted))
	for i, t := range sorted {
		fmt.Fprintf(&b, "  %s %s", GlyphCollapsed, m.itemNameStyle().Render(t.Name))
		annotation := ""
		if t.Source != "" {
			annotation = t.Source
		}
		if t.GateState != "" {
			if annotation != "" {
				annotation += ", "
			}
			annotation += t.GateState
		}
		if annotation != "" {
			fmt.Fprintf(&b, "  %s", m.styles.Muted.Render("["+annotation+"]"))
		}
		b.WriteByte('\n')
		if t.Description != "" {
			fmt.Fprintf(&b, "      %s\n", strings.ReplaceAll(t.Description, "\n", " "))
		}
		if i < len(sorted)-1 {
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderApprovalLog(logs []ApprovalLog) string {
	if len(logs) == 0 {
		return "/permissions: no approvals recorded this session"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "/permissions: %d decision(s) this session\n", len(logs))
	for _, l := range logs {
		fmt.Fprintf(&b, "  • %s — %s [%s]\n", l.Tool, l.Key, l.Decision)
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderSubagentList(subs []SubagentInfo) string {
	if len(subs) == 0 {
		return "/subagents: none running"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "/subagents: %d subagent(s)\n", len(subs))
	for _, s := range subs {
		line := fmt.Sprintf("  • %s [%s]", s.Name, s.Status)
		if !s.StartedAt.IsZero() {
			line += " — started " + s.StartedAt.Format("15:04:05")
		}
		if s.LastReport != "" {
			line += " — " + truncate(s.LastReport, 60)
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}
