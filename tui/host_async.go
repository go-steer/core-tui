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

// Off-loop host calls (issue #114).
//
// Update is the single-threaded heart of the bubble-tea loop: every
// keystroke, every agent event, and every tick is dispatched from it,
// one at a time. A host capability method called from inside Update
// therefore freezes the whole TUI for its duration — no spinner, no
// cursor blink, no Ctrl+C, because those all arrive as messages the
// frozen loop can't read. core-tui is a LIBRARY: the capability
// interfaces are host-implemented, so it cannot assume any of them
// are fast.
//
// Every Cmd in this file follows the four properties host_snapshot.go
// established for the render-path cache:
//
//  1. The capability handle is resolved BEFORE the closure, so the
//     goroutine never type-asserts against a model field.
//  2. gen := m.sessionGen is captured at construction, so a session
//     switch landing mid-flight retires the reply (the ~26 gen guards
//     in update.go).
//  3. The closure touches NO model state — only the values copied
//     into it.
//  4. A nil Cmd is returned when the capability is unwired, so
//     callers keep their "not available in this host" degradation.
//
// Methods whose signature takes a context.Context get a bounded one.
// Handing a host context.Background() when it has explicitly asked to
// be cancellable is the worst version of this bug: the contract says
// "I may be slow" and we reply "take as long as you like".
//
// Issue #137 extended the same treatment to the call sites that fire
// on an explicit /cmd rather than a bare keystroke, and to the
// Options.* host callbacks. Those callbacks are not §3.3 capabilities
// — they are plain func fields — but they are still host-supplied
// code, they routinely write to disk, and Shift+Tab reached two of
// them from a bare keystroke with the error thrown away.

package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
)

// reloadTimeout bounds Reloader.Reload. Generous because a reload
// rebuilds the agent from disk — re-reading config, re-loading memory
// files, and possibly re-handshaking MCP servers. Past this the host
// is wedged, not slow, and the operator gets an error row instead of
// a dead UI.
const reloadTimeout = 60 * time.Second

// pricingRefreshTimeout bounds PricingController.Refresh. Shorter than
// reload: it is one upstream price-table pull, not a rebuild.
const pricingRefreshTimeout = 30 * time.Second

// availableModelsCmd pulls ModelSwapper.AvailableModels() off the
// Update goroutine for the model picker's open-time snapshot. Nil when
// the capability is unwired — the dialog renders its own
// "does not implement ModelSwapper" body in that case.
func (m Model) availableModelsCmd() tea.Cmd {
	swapper, ok := m.opts.Agent.(ModelSwapper)
	if !ok {
		return nil
	}
	gen := m.sessionGen
	return func() tea.Msg {
		return modelsLoadedMsg{gen: gen, models: swapper.AvailableModels()}
	}
}

// switchModelCmd runs ModelSwapper.SwitchModel off the Update
// goroutine. This is the headline call site from issue #114: hosts
// that validate credentials or open a connection inside SwitchModel
// hang the UI for the whole round trip.
//
// The reply carries gen because SwitchModel REPLACES m.opts.Agent.
// Landing a stale switch after a `/switch` session change would
// attach the outgoing session's agent to the incoming session.
func switchModelCmd(swapper ModelSwapper, gen uint64, id string) tea.Cmd {
	return func() tea.Msg {
		agent, err := swapper.SwitchModel(id)
		return modelSwitchedMsg{gen: gen, id: id, agent: agent, err: err}
	}
}

// sessionsCmd pulls SessionSwitcher.Sessions() off the Update
// goroutine for the session picker's open-time snapshot. Nil when the
// capability is unwired.
func (m Model) sessionsCmd() tea.Cmd {
	switcher, ok := m.opts.Agent.(SessionSwitcher)
	if !ok {
		return nil
	}
	gen := m.sessionGen
	return func() tea.Msg {
		return sessionsLoadedMsg{gen: gen, sessions: switcher.Sessions()}
	}
}

// switchToSessionCmd runs SessionSwitcher.SwitchToSession off the
// Update goroutine. Same gen rationale as switchModelCmd — the reply
// hands Update a whole new SwitchTarget to attach to.
func switchToSessionCmd(switcher SessionSwitcher, gen uint64, id string) tea.Cmd {
	return func() tea.Msg {
		tgt, err := switcher.SwitchToSession(id)
		return sessionSwitchedMsg{gen: gen, id: id, target: tgt, err: err}
	}
}

// slashCommandsCmd pulls SlashProvider.SlashCommands() off the Update
// goroutine so the `/` keypress opens the palette immediately with the
// built-ins and merges the host's commands when they arrive. Nil when
// no SlashProvider is wired — the built-in catalog is the whole
// palette and nothing needs to land later.
//
// seq (not gen alone) identifies the palette instance: a palette is
// opened and closed many times within one session generation, so the
// handler needs to know the reply belongs to the palette that is open
// right now.
func (m Model) slashCommandsCmd(seq uint64) tea.Cmd {
	provider, ok := m.opts.Agent.(SlashProvider)
	if !ok {
		return nil
	}
	gen := m.sessionGen
	return func() tea.Msg {
		return slashCommandsMsg{gen: gen, seq: seq, items: slashProviderItems(provider)}
	}
}

// scanFileItemsCmd runs the @-palette's filepath.WalkDir off the
// Update goroutine. The scan defaults to the whole working directory
// when PathScope is unset, which on a large tree stalls the loop for
// the length of a full walk — one stall per `@`, on a bare keystroke.
//
// scope is copied into the closure; the walk never reads the model.
func scanFileItemsCmd(scope PathScope, gen, seq uint64) tea.Cmd {
	return func() tea.Msg {
		return fileItemsMsg{gen: gen, seq: seq, items: scanFileItems(scope)}
	}
}

// reloadCmd runs Reloader.Reload off the Update goroutine under a
// bounded context. Reload's signature takes a context.Context — the
// host has been told it may be slow — so handing it
// context.Background() with no deadline was the contract inverted.
func reloadCmd(reloader Reloader, gen uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), reloadTimeout)
		defer cancel()
		res, err := reloader.Reload(ctx)
		return reloadDoneMsg{gen: gen, result: res, err: err}
	}
}

// pricingRefreshCmd runs PricingController.Refresh off the Update
// goroutine under a bounded context. Same contract inversion as
// reloadCmd: the method takes a ctx and was handed an unbounded one.
func pricingRefreshCmd(ctrl PricingController, gen uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), pricingRefreshTimeout)
		defer cancel()
		summary, err := ctrl.Refresh(ctx)
		return pricingRefreshedMsg{gen: gen, summary: summary, err: err}
	}
}

// ---- issue #137: the /cmd path and the Options.* callbacks ----

// permissionModeCmd applies one Shift+Tab step of the permission-mode
// cycle through Options.PermissionMode, off the Update goroutine.
//
// This is #137's headline call site and the only one in this group
// that fires on a BARE KEYSTROKE. Both halves are host code: Set hands
// the new policy to the gate, and Persist writes it to the host's
// config — a disk write, on Shift+Tab, from inside Update. Both
// returned errors used to be discarded into `_`.
//
// prev rides along so the handler can roll the chip back when the host
// refuses the mode. A chip reading "bypassPermissions" while the gate
// is still asking for every call is worse than a stale chip; it is a
// safety claim core-tui cannot back.
func permissionModeCmd(wiring PermissionModeWiring, gen uint64, prev, mode PermissionMode) tea.Cmd {
	set, persist := wiring.Set, wiring.Persist
	if set == nil {
		return nil
	}
	return func() tea.Msg {
		out := permissionModeAppliedMsg{gen: gen, prev: prev, mode: mode}
		if out.err = set(mode); out.err != nil {
			// Don't persist a mode the gate rejected.
			return out
		}
		if persist != nil {
			out.persistErr = persist(mode)
		}
		return out
	}
}

// persistChoiceCmd runs one of the Options persistence callbacks
// (PersistModelChoice, PersistThemeChoice, PersistStatusLayout) off
// the Update goroutine. Each writes the operator's pick to the host's
// config file, so each is disk I/O reached from a keystroke — Ctrl+B
// for the layout, Enter in the theme picker, Enter in the model
// picker.
//
// what is the prefix for the failure row ("/model", "/theme", …). fn
// and v are copied into the closure, which touches nothing else; a nil
// fn means the host didn't wire persistence and there is no Cmd.
func persistChoiceCmd[T any](gen uint64, what string, fn func(T) error, v T) tea.Cmd {
	if fn == nil {
		return nil
	}
	return func() tea.Msg {
		return persistDoneMsg{gen: gen, what: what, err: fn(v)}
	}
}

// toolsCmd pulls ToolLister.Tools() for /tools off the Update
// goroutine. The catalog can be large and, on a remote host, is a
// round trip.
func toolsCmd(lister ToolLister, gen uint64) tea.Cmd {
	return func() tea.Msg {
		return toolsListedMsg{gen: gen, tools: lister.Tools()}
	}
}

// sessionApprovalsCmd pulls PermissionController.SessionApprovals()
// for /permissions off the Update goroutine.
func sessionApprovalsCmd(ctrl PermissionController, gen uint64) tea.Cmd {
	return func() tea.Msg {
		return approvalsListedMsg{gen: gen, logs: ctrl.SessionApprovals()}
	}
}

// permissionRuleOp names which PermissionController mutator a
// permissionRuleCmd should call. One Cmd covers all three because the
// three differ only in the method name — the dispatch shape, the
// generation guard, and the result row are identical.
type permissionRuleOp int

const (
	permissionRuleAllow       permissionRuleOp = iota // AddAllowPatterns
	permissionRuleDeny                                // AddDenyPatterns
	permissionRuleAllowBundle                         // AddBuiltinAllowExtra
)

// permissionRuleCmd runs one of the PermissionController mutators for
// /allow or /deny off the Update goroutine. These write through to the
// host's permission gate and, on hosts that persist their rule set,
// to disk.
func permissionRuleCmd(ctrl PermissionController, gen uint64, op permissionRuleOp, arg string) tea.Cmd {
	return func() tea.Msg {
		var err error
		switch op {
		case permissionRuleAllowBundle:
			err = ctrl.AddBuiltinAllowExtra(arg)
		case permissionRuleDeny:
			err = ctrl.AddDenyPatterns([]string{arg})
		default:
			err = ctrl.AddAllowPatterns([]string{arg})
		}
		return permissionRuleAddedMsg{gen: gen, op: op, arg: arg, err: err}
	}
}

// subagentRosterCmd pulls SubagentReporter.Subagents() for a bare
// /subagents off the Update goroutine. The sidebar's copy of this read
// already goes through hostSnapshot; this is the slash path, which
// wants a fresh pull rather than a snapshot up to
// hostSnapshotInterval old.
func subagentRosterCmd(reporter SubagentReporter, gen uint64) tea.Cmd {
	return func() tea.Msg {
		return subagentRosterMsg{gen: gen, subs: reporter.Subagents()}
	}
}

// pricingSetCmd runs PricingController.Set off the Update goroutine.
// Shared by the positional `/pricing set <id> <in> <out>` form and the
// huh form's submit handler, so both reach the host the same way.
//
// Set takes no context, so there is nothing to bound — but "no ctx" is
// not a promise of speed, and a host that writes its price table to
// disk (or upstream) inside Set would stall the loop for the write.
func pricingSetCmd(ctrl PricingController, gen uint64, id string, in, out float64) tea.Cmd {
	return func() tea.Msg {
		summary, err := ctrl.Set(id, in, out)
		return pricingSetMsg{gen: gen, summary: summary, err: err}
	}
}

// slashInvokeTimeout bounds SlashProvider.InvokeSlash. Same contract
// inversion reloadCmd fixed: the method takes a context.Context and
// was being handed context.Background(). Generous, because a /cmd can
// legitimately be slow — a compaction pass, an upstream lookup — and
// the async provider variants are the seam for anything that wants to
// report progress while it works.
const slashInvokeTimeout = 60 * time.Second

// switchLookupCmd runs stage one of `/switch <id>`: the Sessions()
// enumerate that decides whether id names an action row (issue #56) or
// a session. The row match happens inside the closure because it is a
// pure filter over the id we handed it — the goroutine reads nothing
// from the model, and the handler gets an answer rather than a list to
// re-scan.
//
// Stage two (SwitchToSession) is deliberately NOT chained here. Two
// dependent host calls in one Cmd would commit the switch before
// Update had a chance to notice the enumerate was superseded, which is
// exactly the failure the seq stamp exists to prevent.
func switchLookupCmd(switcher SessionSwitcher, gen, seq uint64, id string) tea.Cmd {
	return func() tea.Msg {
		out := switchLookupMsg{gen: gen, seq: seq, id: id}
		for _, s := range switcher.Sessions() {
			if s.ID == id && s.Input != nil {
				row := s
				out.row = &row
				break
			}
		}
		return out
	}
}

// sessionInputSubmitCmd runs a SessionInfo action row's
// SessionInput.Submit off the Update goroutine (issue #194). The
// closure is documented as carrying SwitchToSession's contract, and
// the row that motivates the feature at all dials an endpoint the
// operator just typed — so it is a network call in every sense that
// matters, one that simply arrives through the row instead of through
// the capability interface.
//
// submit is handed in already resolved rather than as a SessionInfo:
// the goroutine gets a function and two strings and reads nothing
// else, least of all the dialog that dispatched it. gen and seq are
// the caller's stamp as of the committing keystroke; rowID and value
// come back untouched so Update can match the reply to the dialog
// still waiting for it.
func sessionInputSubmitCmd(submit func(string) (SwitchTarget, error), gen, seq uint64, rowID, value string) tea.Cmd {
	return func() tea.Msg {
		tgt, err := submit(value)
		return sessionInputSubmittedMsg{
			gen: gen, seq: seq, rowID: rowID, value: value, target: tgt, err: err,
		}
	}
}

// slashDispatchCmd runs the host-provider half of a `/cmd` off the
// Update goroutine: the SlashCommands() name match and, when invoke is
// set, the InvokeSlash call itself under slashInvokeTimeout.
//
// The two travel together because splitting them buys nothing — the
// match is only ever asked in order to decide whether to invoke — and
// costs the operator a frame of silence between their Enter and any
// feedback at all.
//
// invoke is resolved by the caller, on the loop, from the provider's
// concrete shape: only a plain SlashProvider can be driven to
// completion out here. The async variants come back through Update
// first, because starting one means arming the model's cancel func,
// in-flight record and toast.
func slashDispatchCmd(provider SlashProvider, gen, seq uint64, name, args string, invoke bool) tea.Cmd {
	return func() tea.Msg {
		out := slashDispatchedMsg{gen: gen, seq: seq, name: name, args: args}
		for _, spec := range provider.SlashCommands() {
			if spec.Name == name || sliceContains(spec.Aliases, name) {
				out.matched = true
				break
			}
		}
		if !out.matched || !invoke {
			return out
		}
		ctx, cancel := context.WithTimeout(context.Background(), slashInvokeTimeout)
		defer cancel()
		out.invoked = true
		out.res, out.err = provider.InvokeSlash(ctx, name, args)
		return out
	}
}

// helpCommandsCmd pulls SlashProvider.SlashCommands() for /help off
// the Update goroutine. The built-in half of the help text needs no
// host at all, so it renders on the keystroke and the host's section
// lands as a follow-up row.
func helpCommandsCmd(provider SlashProvider, gen uint64) tea.Cmd {
	return func() tea.Msg {
		return helpCommandsMsg{gen: gen, specs: provider.SlashCommands()}
	}
}
