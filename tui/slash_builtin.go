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
	case "themes":
		name = "theme"
	case "perms":
		name = "permissions"
	case "by-the-way":
		name = "btw"
	case "sub":
		name = "subagent"
	case "sess":
		name = "switch"
	}

	switch name {
	case "help", "?":
		// The built-in catalog needs no host, so it paints on the
		// keystroke. The host's SlashCommands() section follows as a
		// second row when helpCommandsMsg lands (issue #137).
		m.history.Append(Message{Role: RoleSystem, Text: m.renderBuiltinHelp()})
		m.input.Reset()
		m.refreshAndScroll()
		if provider, ok := m.opts.Agent.(SlashProvider); ok {
			return true, m, helpCommandsCmd(provider, m.sessionGen)
		}
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
		return true, m, m.quitCmd()

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
		// Runtime toggle. View() reads m.opts.Mouse every frame so
		// flipping the pointer + refreshing is enough — bubble-tea
		// picks up the new MouseMode on the next render. Initial
		// state comes from Options.Mouse (default: enabled).
		on := true
		if m.opts.Mouse != nil {
			on = *m.opts.Mouse
		}
		on = !on
		m.opts.Mouse = &on
		state := "off"
		if on {
			state = "on"
		}
		m.history.Append(Message{Role: RoleSystem, Text: "/mouse: capture " + state})
		m.input.Reset()
		m.refreshAndScroll()
		return true, m, nil

	case "interrupt", "int":
		// Local Run-path cancel wins when we have it — that's the
		// original operator-initiated turn context, and cancelling it
		// unwedges the whole stack (agent goroutine + iterator +
		// spinner) synchronously.
		if m.state == stateStreaming && m.cancelTurn != nil {
			m.cancelTurn()
			m.input.Reset()
			return true, m, nil
		}
		// LiveAgent / observer-mode fallthrough: the daemon is running
		// the turn autonomously (k8s-event injects, runaway tool loops,
		// etc.) so we have no local context to cancel. If the host
		// implements RemoteInterrupter, dispatch a remote cancel. The
		// RemoteInterrupter contract says implementations may block
		// briefly on network I/O; we run it in a bounded goroutine to
		// keep the Update loop responsive, and surface the outcome as
		// a follow-up system row via remoteInterruptDoneMsg.
		if ri, ok := m.opts.Agent.(RemoteInterrupter); ok {
			// Immediate feedback so the operator knows the slash landed.
			m.history.Append(Message{Role: RoleSystem, Text: "/interrupt: cancelling remote turn…"})
			m.input.Reset()
			m.refreshAndScroll()
			return true, m, remoteInterruptCmd(ri)
		}
		// Neither path available. Original message preserved so
		// operators used to it don't see a behavior regression.
		m.history.Append(Message{Role: RoleSystem, Text: "/interrupt: no turn in flight"})
		m.input.Reset()
		m.refreshAndScroll()
		return true, m, nil

	case "tools":
		lister, ok := m.opts.Agent.(ToolLister)
		if !ok {
			m.history.Append(Message{Role: RoleSystem, Text: "/tools: agent doesn't implement ToolLister"})
			m.input.Reset()
			m.refreshAndScroll()
			return true, m, nil
		}
		// Tools() is host code — on a remote host it is a round trip,
		// and the catalog can be large (issue #137). Acknowledge now,
		// render the table when toolsListedMsg lands.
		m.history.Append(Message{Role: RoleSystem, Text: "/tools: reading the tool catalog…"})
		m.input.Reset()
		m.refreshAndScroll()
		return true, m, toolsCmd(lister, m.sessionGen)

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
			// already showing is a no-op. The list arrives via
			// the returned Cmd, off the Update loop (issue #114).
			cmd := m.openModelPicker()
			m.input.Reset()
			return true, m, cmd
		}
		// `/model <id>` switches without opening a picker — useful for
		// scripted/replay flows and as a fallback while the picker
		// modal is still being built. Runs through the same off-loop
		// Cmd the picker uses; modelSwitchedMsg does the attach.
		m.input.Reset()
		m.refreshAndScroll()
		return true, m, switchModelCmd(swapper, m.sessionGen, args)

	case "switch":
		// SessionSwitcher (issues #48 / #53) is optional. When
		// absent we return handled=false so a host-provided
		// AsyncSlashProvider that registers "switch" still gets
		// dispatched via the SlashProvider path — hosts can ship
		// their own picker without core-tui knowing the
		// enumeration shape.
		switcher, ok := m.opts.Agent.(SessionSwitcher)
		if !ok {
			return false, m, nil
		}
		if args == "" {
			// Same shape as /model: open instantly, pull
			// Sessions() off-loop (issue #114).
			cmd := m.openSessionPicker()
			m.input.Reset()
			return true, m, cmd
		}
		// `/switch <id>` is two dependent host calls, which is why it
		// didn't ride along with the picker in #114: Sessions() first,
		// because an id naming an action row (issue #56) opens that
		// row's text-input dialog rather than switching — the host's
		// SwitchToSession has never heard of that ID — and only then
		// SwitchToSession itself. Both used to run inline.
		//
		// They run as two stages now (issue #137). The enumerate lands
		// as switchLookupMsg and Update picks the second half, so a
		// superseded enumerate is dropped before it can switch
		// anything. The acknowledgement row is what the operator has
		// to look at meanwhile; on the switching path it goes away
		// with the rest of the transcript.
		m.history.Append(Message{Role: RoleSystem, Text: "/switch: looking up " + args + "…"})
		m.input.Reset()
		m.refreshAndScroll()
		return true, m, switchLookupCmd(switcher, m.sessionGen, m.slashSeq, args)

	case "theme":
		if args == "" {
			// No-arg form opens the interactive picker dialog.
			// Singleton — re-opening while already showing is
			// a no-op.
			if !m.overlayStack.HasID(themePickerDialogID) {
				m.overlayStack.ask(
					newThemePickerQuestion(BuiltinThemes(), m.themeName),
					themePickerResolver(m.themeName),
				)
			}
			m.input.Reset()
			return true, m, nil
		}
		// `/theme <name>` switches without opening a picker —
		// useful for scripted / replay flows. Unknown names fall
		// through to defaultTheme via ThemeByName; we surface that
		// via a system message instead of erroring so a typo is
		// recoverable in one keystroke.
		name := strings.TrimSpace(args)
		known := false
		for _, bt := range BuiltinThemes() {
			if strings.EqualFold(bt.Name, name) {
				name = bt.Name // canonicalize casing
				known = true
				break
			}
		}
		m.applyNamedTheme(name)
		if known {
			m.history.Append(Message{Role: RoleSystem, Text: "/theme: switched to " + name})
			// Callback persistence (mirrors PersistModelChoice).
			// Only fires on a KNOWN name — fallback-to-default on
			// a typo isn't worth persisting.
			if m.opts.PersistThemeChoice != nil {
				if perr := m.opts.PersistThemeChoice(name); perr != nil {
					m.history.Append(Message{Role: RoleError, Text: "/theme: persist failed: " + perr.Error()})
				}
			}
		} else {
			m.history.Append(Message{Role: RoleSystem, Text: "/theme: unknown theme " + name + " — falling back to default. Try /theme to see the list."})
		}
		m.input.Reset()
		m.refreshAndScroll()
		// Also emit ThemeChangedMsg — hosts can use either the
		// callback OR observe the msg.
		if known {
			canonical := name
			return true, m, func() tea.Msg { return ThemeChangedMsg{Name: canonical} }
		}
		return true, m, nil

	case "reload":
		reloader, ok := m.opts.Agent.(Reloader)
		if !ok {
			m.history.Append(Message{Role: RoleSystem, Text: "/reload: agent doesn't implement Reloader"})
			m.input.Reset()
			m.refreshAndScroll()
			return true, m, nil
		}
		// Reload rebuilds the agent from disk — Reloader takes a ctx
		// precisely because it may be slow, and it used to be handed
		// context.Background() from inside Update (issue #114). Now
		// it runs off-loop under reloadTimeout; the immediate system
		// row is the operator's acknowledgement, reloadDoneMsg is the
		// outcome. Same shape as remoteInterruptCmd's call site.
		m.history.Append(Message{Role: RoleSystem, Text: "/reload: rebuilding agent…"})
		m.input.Reset()
		m.refreshAndScroll()
		return true, m, reloadCmd(reloader, m.sessionGen)

	case "permissions":
		ctrl, ok := m.opts.Agent.(PermissionController)
		if !ok {
			m.history.Append(Message{Role: RoleSystem, Text: "/permissions: agent doesn't implement PermissionController"})
			m.input.Reset()
			m.refreshAndScroll()
			return true, m, nil
		}
		// SessionApprovals() reads the host's gate, which on a remote
		// host means a round trip (issue #137).
		m.history.Append(Message{Role: RoleSystem, Text: "/permissions: reading the session approval log…"})
		m.input.Reset()
		m.refreshAndScroll()
		return true, m, sessionApprovalsCmd(ctrl, m.sessionGen)

	case "pricing":
		ctrl, ok := m.opts.Agent.(PricingController)
		if !ok {
			m.history.Append(Message{Role: RoleSystem, Text: "/pricing: agent doesn't implement PricingController"})
			m.input.Reset()
			m.refreshAndScroll()
			return true, m, nil
		}
		text, cmd := m.handlePricing(ctrl, args)
		if text != "" {
			m.history.Append(Message{Role: RoleSystem, Text: text})
		}
		m.input.Reset()
		m.refreshAndScroll()
		return true, m, cmd

	case "subagents":
		// Bare `/subagents` lists the roster; `/subagents <name>`
		// drills into one — the untruncated report plus its turn
		// log, live-tailed while open (issues #70 and #71).
		if name := strings.TrimSpace(args); name != "" {
			text, cmd := m.openSubagentDetail(name)
			if text != "" {
				m.history.Append(Message{Role: RoleSystem, Text: text})
			}
			m.input.Reset()
			m.refreshAndScroll()
			return true, m, cmd
		}
		reporter, ok := m.opts.Agent.(SubagentReporter)
		if !ok {
			m.history.Append(Message{Role: RoleSystem, Text: "/subagents: agent doesn't implement SubagentReporter"})
			m.input.Reset()
			m.refreshAndScroll()
			return true, m, nil
		}
		// The sidebar's copy of this read comes from hostSnapshot; the
		// slash path wants a fresh pull, and gets it off-loop (#137).
		m.history.Append(Message{Role: RoleSystem, Text: "/subagents: reading the roster…"})
		m.input.Reset()
		m.refreshAndScroll()
		return true, m, subagentRosterCmd(reporter, m.sessionGen)

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

	case "allow", "deny":
		text, cmd := m.handleAllowDeny(args, name)
		m.history.Append(Message{Role: RoleSystem, Text: text})
		m.input.Reset()
		m.refreshAndScroll()
		return true, m, cmd
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
				glyphCollapsed,
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
// bundles. Parsing and validation stay on the Update goroutine — they
// touch no host code — but the three PermissionController mutators run
// off-loop (issue #137), so the returned text is the immediate
// acknowledgement and the outcome arrives as permissionRuleAddedMsg.
// A nil Cmd means the text is the whole answer (unwired capability,
// usage hint, or a malformed bundle name).
func (m Model) handleAllowDeny(args, op string) (string, tea.Cmd) {
	ctrl, ok := m.opts.Agent.(PermissionController)
	if !ok {
		return "/" + op + ": agent doesn't implement PermissionController", nil
	}
	args = strings.TrimSpace(args)
	if args == "" {
		hint := "<pattern>   e.g. /" + op + " bash:git *"
		if op == "allow" {
			hint += "   or   /allow bundle:dev_tools"
		}
		return "/" + op + ": usage — /" + op + " " + hint, nil
	}
	if op == "allow" && strings.HasPrefix(args, "bundle:") {
		name := strings.TrimPrefix(args, "bundle:")
		if name == "" {
			return "/allow bundle: empty bundle name — try /allow bundle:dev_tools", nil
		}
		return "/allow: enabling bundle " + name + "…",
			permissionRuleCmd(ctrl, m.sessionGen, permissionRuleAllowBundle, name)
	}
	ruleOp := permissionRuleAllow
	if op == "deny" {
		ruleOp = permissionRuleDeny
	}
	return "/" + op + ": adding " + args + "…", permissionRuleCmd(ctrl, m.sessionGen, ruleOp, args)
}

// renderPermissionRuleResult composes the follow-up row for a
// completed /allow or /deny. Wording matches what the inline version
// wrote so operators (and host docs) see no change beyond the extra
// acknowledgement row.
func renderPermissionRuleResult(msg permissionRuleAddedMsg) string {
	if msg.op == permissionRuleAllowBundle {
		if msg.err != nil {
			return "/allow bundle: " + msg.err.Error()
		}
		return "/allow: enabled bundle " + msg.arg
	}
	op := "allow"
	if msg.op == permissionRuleDeny {
		op = "deny"
	}
	if msg.err != nil {
		return "/" + op + ": " + msg.err.Error()
	}
	return "/" + op + ": added " + msg.arg
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
// flows fast. Returns the text to echo as a system message plus an
// optional Cmd. Both host-touching shapes run off the Update
// goroutine, so for those the text is the immediate acknowledgement
// and the outcome lands later — `refresh` as pricingRefreshedMsg,
// `set <id> <in> <out>` as pricingSetMsg.
func (m *Model) handlePricing(ctrl PricingController, args string) (string, tea.Cmd) {
	args = strings.TrimSpace(args)
	if args == "" || args == "help" {
		return "/pricing: usage — /pricing refresh OR /pricing set (form) OR /pricing set <model-id> <input-per-mtok> <output-per-mtok>", nil
	}
	sub, rest, _ := strings.Cut(args, " ")
	switch sub {
	case "refresh":
		// Refresh pulls an upstream price table over the network and
		// takes a ctx to say so; running it inline froze the loop for
		// the round trip (issue #114). Off-loop under
		// pricingRefreshTimeout, with an immediate acknowledgement.
		return "/pricing refresh: pulling the upstream price table…", pricingRefreshCmd(ctrl, m.sessionGen)
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
			return "/pricing: opening form (esc cancels)", nil
		}
		if len(fields) != 3 {
			return "/pricing set: want <model-id> <input-per-mtok> <output-per-mtok>, or pass nothing to open the form", nil
		}
		in, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			return "/pricing set: invalid input rate " + fields[1] + " — " + err.Error(), nil
		}
		out, err := strconv.ParseFloat(fields[2], 64)
		if err != nil {
			return "/pricing set: invalid output rate " + fields[2] + " — " + err.Error(), nil
		}
		// Set takes no ctx, but "no ctx" isn't a promise of speed: a
		// host that writes its price table through to disk does that
		// write inside Set. Off-loop like the rest (issue #137); the
		// summary arrives as pricingSetMsg.
		return "/pricing set: applying " + fields[0] + "…",
			pricingSetCmd(ctrl, m.sessionGen, fields[0], in, out)
	default:
		return "/pricing: unknown subcommand " + sub + " — try /pricing refresh or /pricing set", nil
	}
}

// renderBuiltinHelp produces the built-in half of the /help text.
// Needs no host call, so it renders on the keystroke; the host's
// commands follow in renderHostCommandHelp once SlashCommands()
// answers off-loop (issue #137). The rendered output is a single
// block — the renderer will glamour-ify it on the next viewport
// refresh.
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
	b.WriteString("  /switch [<id>]       — pick another session (in-place)\n")
	b.WriteString("  /theme [<name>]      — pick a theme (default, google, gopher, …)\n")
	b.WriteString("  /reload              — rebuild agent from disk\n")
	b.WriteString("  /permissions         — review session approvals\n")
	b.WriteString("  /pricing refresh|set — manage cost rates\n")
	b.WriteString("  /subagents [<name>]  — list subagents or open one's turn log\n")
	b.WriteString("  /interrupt, /int     — cancel the in-flight turn\n")
	b.WriteString("  /mouse               — toggle terminal mouse capture\n")

	return strings.TrimRight(b.String(), "\n")
}

// renderHostCommandHelp produces the "Agent commands:" section of
// /help from the host's SlashCommands() specs. Rendered as its own
// system row underneath the built-in block, because the specs arrive
// after it (helpCommandsMsg). Never called with an empty slice — the
// handler skips the row entirely when the host exposes no commands.
func renderHostCommandHelp(specs []SlashCommandSpec) string {
	var b strings.Builder
	b.WriteString("Agent commands:\n")
	for _, s := range specs {
		name := "/" + s.Name
		for _, a := range s.Aliases {
			name += ", /" + a
		}
		b.WriteString("  " + padRight(name, 20) + " — " + s.Description + "\n")
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
	return lipgloss.NewStyle().Foreground(brandViolet).Bold(true)
}

func (m Model) itemNameStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(brandPink).Bold(true)
}

func (m Model) renderMemoryList(files []MemoryFile) string {
	if len(files) == 0 {
		return "No memory files loaded. Drop AGENTS.md / CLAUDE.md / GEMINI.md in the project or user-home tree to surface them here."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Memory files (%d):\n\n", len(files))
	for i, f := range files {
		fmt.Fprintf(&b, "  %s %s", glyphCollapsed, m.itemNameStyle().Render(f.Path))
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
				fmt.Fprintf(&b, "    %s %s\n", glyphCollapsed, m.itemNameStyle().Render(t.Name))
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
		fmt.Fprintf(&b, "  %s %s", glyphCollapsed, m.itemNameStyle().Render(s.Name))
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
	// Per-model breakdown rows, from m.sessionUsage.ByModel —
	// populated by push-mode SSE usage-update events from a remote
	// daemon (v0.9.0+, issue #38). Authoritative in remote/attach
	// mode because it reflects the daemon's own tracker, which the
	// local UsageTracker can't observe directly.
	//
	// Until v0.21.0 there was a second source: a local tracker
	// implementing the optional SessionByModelTracker (issue #18).
	// Nothing ever implemented it — not the reference host, not
	// either example adapter — so the fallback branch was dead in
	// every configuration, and embedded mode has been reading the
	// push path all along. See docs/api-audit.md §5.2.
	//
	// Single-entry / empty maps are skipped — the breakdown row
	// would just restate SessionTotals.
	if m.sessionUsage != nil && len(m.sessionUsage.ByModel) > 1 {
		b.WriteString(formatModelBreakdown(m.sessionUsage.ByModel))
	}
	return strings.TrimRight(b.String(), "\n")
}

// formatModelBreakdown renders the per-model usage map as a
// "Models:" block under /stats. Entries are sorted by descending
// CostUSD (priciest first) so the operator's eye lands on the
// dominant tier; ties broken by descending output tokens, then
// by model name for stable order.
func formatModelBreakdown(breakdown map[string]UsageByModel) string {
	type modelRow struct {
		name string
		UsageByModel
	}
	rows := make([]modelRow, 0, len(breakdown))
	for name, t := range breakdown {
		rows = append(rows, modelRow{name: name, UsageByModel: t})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CostUSD != rows[j].CostUSD {
			return rows[i].CostUSD > rows[j].CostUSD
		}
		if rows[i].TokensOut != rows[j].TokensOut {
			return rows[i].TokensOut > rows[j].TokensOut
		}
		return rows[i].name < rows[j].name
	})
	var b strings.Builder
	for i, r := range rows {
		prefix := "  Models:     "
		if i > 0 {
			prefix = "              + "
		}
		fmt.Fprintf(&b, "%s%s (%d turn%s, %d in / %d out, $%.4f)\n",
			prefix, r.name, r.Turns, plural(r.Turns), r.TokensIn, r.TokensOut, r.CostUSD)
	}
	return b.String()
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
		fmt.Fprintf(&b, "  %s %s", glyphCollapsed, m.itemNameStyle().Render(t.Name))
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

// renderSubagentList renders the roster. Reports stay clipped to one
// line here — the list is a scan surface, and a subagent that just
// wrote three paragraphs shouldn't bury the others. The trailing hint
// points at where the rest of it lives (issue #70): the
// `/subagents <name>` overlay, which renders the report in full.
//
// The hint is unconditional. Until v0.21.0 it was gated on a
// `drillable` bool threaded from a second type assertion at dispatch,
// against a SubagentEventReader that was separate from the lister; a
// roster can only be rendered by a SubagentReporter, which serves the
// turn log too, so the gate is now always open.
func renderSubagentList(subs []SubagentInfo) string {
	if len(subs) == 0 {
		return "/subagents: none running"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "/subagents: %d subagent(s)\n", len(subs))
	clipped := false
	for _, s := range subs {
		line := fmt.Sprintf("  • %s [%s]", s.Name, s.Status)
		if !s.StartedAt.IsZero() {
			line += " — started " + s.StartedAt.Format("15:04:05")
		}
		if report := collapseWhitespace(s.LastReport); report != "" {
			short := truncate(report, 60)
			clipped = clipped || short != report
			line += " — " + short
		}
		b.WriteString(line + "\n")
	}
	hint := "  /subagents <name> for the full report and turn log"
	if !clipped {
		hint = "  /subagents <name> for its turn log"
	}
	b.WriteString(hint + "\n")
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

// applySwitchLookup is the Update-side half of stage one of
// `/switch <id>` (issue #137). Called only after the gen AND seq
// guards have passed, so by the time it runs the enumerate is known
// still to describe the session the operator is looking at.
//
// Two outcomes: the id named an action row, and we open its dialog; or
// it didn't, and we go on to stage two. The switcher is re-resolved
// here rather than carried through the message because the agent can
// be replaced inside one session generation (a /model switch does
// exactly that), and stage two must reach whichever agent is attached
// now — or nobody, if the replacement dropped the capability.
func (m *Model) applySwitchLookup(msg switchLookupMsg) tea.Cmd {
	if msg.row != nil {
		if !m.overlayStack.HasID(sessionInputDialogID) {
			m.overlayStack.Open(newSessionInputDialog(*msg.row))
		}
		return nil
	}
	switcher, ok := m.opts.Agent.(SessionSwitcher)
	if !ok {
		m.history.Append(Message{Role: RoleError, Text: "/switch: agent no longer implements SessionSwitcher"})
		m.refreshAndScroll()
		return nil
	}
	return switchToSessionCmd(switcher, m.sessionGen, msg.id)
}

// applyReload installs a completed Reloader.Reload (issue #114's
// off-loop /reload path). Called only after the sessionGen guard has
// passed — like SwitchModel it can replace m.opts.Agent, so a reply
// that outlived its session must not land.
func (m *Model) applyReload(msg reloadDoneMsg) {
	if msg.err != nil {
		m.history.Append(Message{Role: RoleError, Text: "/reload: " + msg.err.Error()})
		m.refreshAndScroll()
		return
	}
	res := msg.result
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
	m.refreshAndScroll()
}

// remoteInterruptCmd runs a RemoteInterrupter.Interrupt in a
// goroutine off the Update loop and posts the outcome back as a
// remoteInterruptDoneMsg. Bounded context (5s) so a hung endpoint
// doesn't leave the operator waiting silently.
//
// The 5s bound is generous — a healthy attach endpoint responds in
// tens of milliseconds; anything longer signals a broken daemon,
// which we surface as an error row rather than blocking.
func remoteInterruptCmd(ri RemoteInterrupter) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return remoteInterruptDoneMsg{err: ri.Interrupt(ctx)}
	}
}
