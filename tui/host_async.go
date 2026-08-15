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
