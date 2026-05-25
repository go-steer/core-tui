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
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
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
func (m Model) dispatchBuiltinSlash(name, args string) (bool, tea.Model, tea.Cmd) {
	switch name {
	case "help", "?":
		m.history.Append(Message{Role: RoleSystem, Text: m.renderBuiltinHelp()})
		m.input.Reset()
		m.refreshViewport()
		return true, m, nil

	case "clear":
		m.history.Reset()
		m.input.Reset()
		m.refreshViewport()
		return true, m, nil

	case "quit", "exit", "q":
		m.input.Reset()
		return true, m, tea.Quit

	case "memory":
		m.history.Append(Message{Role: RoleSystem, Text: renderMemoryList(m.opts.Memory)})
		m.input.Reset()
		m.refreshViewport()
		return true, m, nil

	case "mcp":
		m.history.Append(Message{Role: RoleSystem, Text: renderMCPList(m.opts.MCPServers)})
		m.input.Reset()
		m.refreshViewport()
		return true, m, nil

	case "skills":
		m.history.Append(Message{Role: RoleSystem, Text: renderSkillList(m.opts.Skills)})
		m.input.Reset()
		m.refreshViewport()
		return true, m, nil

	case "stats":
		m.history.Append(Message{Role: RoleSystem, Text: renderStats(m.opts.UsageTracker)})
		m.input.Reset()
		m.refreshViewport()
		return true, m, nil

	case "mouse":
		// Mouse capture is enabled by the program harness at startup
		// (program.go); the TUI has no per-session toggle yet. Surface
		// the limitation honestly so the operator knows the slash isn't
		// silently dropped.
		m.history.Append(Message{Role: RoleSystem, Text: "/mouse: mouse-capture toggle is not yet wired — the host program controls it at startup"})
		m.input.Reset()
		m.refreshViewport()
		return true, m, nil

	case "interrupt", "int":
		if m.state != stateStreaming || m.cancelTurn == nil {
			m.history.Append(Message{Role: RoleSystem, Text: "/interrupt: no turn in flight"})
			m.input.Reset()
			m.refreshViewport()
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
			m.history.Append(Message{Role: RoleSystem, Text: renderToolList(lister.Tools())})
		}
		m.input.Reset()
		m.refreshViewport()
		return true, m, nil

	case "model":
		swapper, ok := m.opts.Agent.(ModelSwapper)
		if !ok {
			m.history.Append(Message{Role: RoleSystem, Text: "/model: agent doesn't implement ModelSwapper"})
			m.input.Reset()
			m.refreshViewport()
			return true, m, nil
		}
		if args == "" {
			// No-arg form opens the interactive picker overlay
			// (mirrors Ctrl+G). Avoids dumping a long model list
			// into the chat when the operator wanted to choose.
			m.overlay = overlayModelPicker
			m.modelPickerIdx = 0
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
			m.refreshViewport()
			return true, m, nil
		}
		m.opts.Agent = newAgent
		m.history.Append(Message{Role: RoleSystem, Text: "/model: switched to " + args})
		if m.opts.PersistModelChoice != nil {
			if perr := m.opts.PersistModelChoice(args); perr != nil {
				m.history.Append(Message{Role: RoleError, Text: "/model: persist failed: " + perr.Error()})
			}
		}
		m.input.Reset()
		m.refreshViewport()
		return true, m, nil

	case "reload":
		reloader, ok := m.opts.Agent.(Reloader)
		if !ok {
			m.history.Append(Message{Role: RoleSystem, Text: "/reload: agent doesn't implement Reloader"})
			m.input.Reset()
			m.refreshViewport()
			return true, m, nil
		}
		res, err := reloader.Reload(context.Background())
		if err != nil {
			m.history.Append(Message{Role: RoleError, Text: "/reload: " + err.Error()})
			m.input.Reset()
			m.refreshViewport()
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
		m.refreshViewport()
		return true, m, nil

	case "permissions":
		ctrl, ok := m.opts.Agent.(PermissionController)
		if !ok {
			m.history.Append(Message{Role: RoleSystem, Text: "/permissions: agent doesn't implement PermissionController"})
		} else {
			m.history.Append(Message{Role: RoleSystem, Text: renderApprovalLog(ctrl.SessionApprovals())})
		}
		m.input.Reset()
		m.refreshViewport()
		return true, m, nil

	case "pricing":
		ctrl, ok := m.opts.Agent.(PricingController)
		if !ok {
			m.history.Append(Message{Role: RoleSystem, Text: "/pricing: agent doesn't implement PricingController"})
			m.input.Reset()
			m.refreshViewport()
			return true, m, nil
		}
		text := m.handlePricing(ctrl, args)
		m.history.Append(Message{Role: RoleSystem, Text: text})
		m.input.Reset()
		m.refreshViewport()
		return true, m, nil

	case "subagents":
		lister, ok := m.opts.Agent.(SubagentLister)
		if !ok {
			m.history.Append(Message{Role: RoleSystem, Text: "/subagents: agent doesn't implement SubagentLister"})
		} else {
			m.history.Append(Message{Role: RoleSystem, Text: renderSubagentList(lister.Subagents())})
		}
		m.input.Reset()
		m.refreshViewport()
		return true, m, nil
	}

	return false, m, nil
}

// handlePricing parses the /pricing subcommand and dispatches. Two
// shapes:
//
//	/pricing refresh            → re-pull the upstream price table
//	/pricing set <id> <in> <out> → override per-model rates in $/MTok
func (m Model) handlePricing(ctrl PricingController, args string) string {
	args = strings.TrimSpace(args)
	if args == "" || args == "help" {
		return "/pricing: usage — /pricing refresh OR /pricing set <model-id> <input-per-mtok> <output-per-mtok>"
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
		if len(fields) != 3 {
			return "/pricing set: want <model-id> <input-per-mtok> <output-per-mtok>"
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

func renderMemoryList(files []MemoryFile) string {
	if len(files) == 0 {
		return "/memory: no memory files loaded (host did not wire Options.Memory)"
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("/memory: %d file(s)\n", len(files)))
	for _, f := range files {
		b.WriteString("  • " + f.Path)
		if f.Excerpt != "" {
			b.WriteString(" — " + truncate(f.Excerpt, 60))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderMCPList(servers []MCPServerInfo) string {
	if len(servers) == 0 {
		return "/mcp: no MCP servers configured (host did not wire Options.MCPServers)"
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("/mcp: %d server(s)\n", len(servers)))
	for _, s := range servers {
		status := "connected"
		if !s.Connected {
			status = "disconnected"
		}
		line := fmt.Sprintf("  • %s [%s] — %d tool(s)", s.Name, status, s.ToolCount)
		if s.Transport != "" {
			line += " (" + s.Transport + ")"
		}
		if s.URL != "" {
			line += " " + s.URL
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderSkillList(skills []SkillInfo) string {
	if len(skills) == 0 {
		return "/skills: no skill bundles loaded (host did not wire Options.Skills)"
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("/skills: %d skill(s)\n", len(skills)))
	for _, s := range skills {
		line := "  • " + s.Name
		if s.Source != "" {
			line += " [" + s.Source + "]"
		}
		if s.Description != "" {
			line += " — " + truncate(s.Description, 60)
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderStats(tracker UsageTracker) string {
	if tracker == nil {
		return "/stats: no UsageTracker wired (host did not pass Options.UsageTracker)"
	}
	totals := tracker.SessionTotals()
	cost := tracker.SessionCostUSD()
	last, lastCost := tracker.LastTurn()
	winUsed := tracker.ContextWindowUsed()
	winSize := tracker.ContextWindowSize()

	var b strings.Builder
	b.WriteString("/stats:\n")
	b.WriteString(fmt.Sprintf("  session  — %d in / %d out tokens · $%.4f\n", totals.InputTokens, totals.OutputTokens, cost))
	b.WriteString(fmt.Sprintf("  last turn — %d in / %d out tokens · $%.4f\n", last.InputTokens, last.OutputTokens, lastCost))
	if winSize > 0 {
		b.WriteString(fmt.Sprintf("  context  — %d / %d tokens", winUsed, winSize))
	} else {
		b.WriteString("  context  — (unknown)")
	}
	return b.String()
}

func renderToolList(tools []ToolInfo) string {
	if len(tools) == 0 {
		return "/tools: no tools registered"
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("/tools: %d tool(s)\n", len(tools)))
	for _, t := range tools {
		line := fmt.Sprintf("  • %s [%s, %s]", t.Name, t.Source, t.GateState)
		if t.Description != "" {
			line += " — " + truncate(t.Description, 60)
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderModelList(models []ModelInfo) string {
	if len(models) == 0 {
		return "/model: no models advertised by the agent"
	}
	var b strings.Builder
	b.WriteString("/model: available — use `/model <id>` to switch\n")
	for _, m := range models {
		disp := m.Display
		if disp == "" {
			disp = m.ID
		}
		line := "  • " + disp
		if m.ID != disp {
			line += " (" + m.ID + ")"
		}
		if m.Description != "" {
			line += " — " + truncate(m.Description, 60)
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderApprovalLog(logs []ApprovalLog) string {
	if len(logs) == 0 {
		return "/permissions: no approvals recorded this session"
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("/permissions: %d decision(s) this session\n", len(logs)))
	for _, l := range logs {
		b.WriteString(fmt.Sprintf("  • %s — %s [%s]\n", l.Tool, l.Key, l.Decision))
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderSubagentList(subs []SubagentInfo) string {
	if len(subs) == 0 {
		return "/subagents: none running"
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("/subagents: %d subagent(s)\n", len(subs)))
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
