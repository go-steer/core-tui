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

package main

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"time"

	"github.com/go-steer/core-tui/examples/core-agent/fakehost"
	"github.com/go-steer/core-tui/tui"
)

// localAdapter is the in-process flavor: core-tui driving a
// core-agent that lives in the same process, one turn per Run.
// It mirrors `cmd/core-agent/coretui_enabled.go`'s coreAgentAdapter.
//
// Everything host-specific stays on the fakehost side of this
// struct. The adapter's whole job is shape translation.
type localAdapter struct {
	inner *fakehost.Agent
}

// ---------------------------------------------------------------
// The canary (docs/design.md §7). These assertions are the reason
// this file exists: `go build ./...` fails the moment core-tui
// renames a method, changes a signature, or splits an interface,
// which is the CI signal that the plug-in surface moved under the
// reference host.
// ---------------------------------------------------------------

var (
	_ tui.Agent                = (*localAdapter)(nil)
	_ tui.InjectableAgent      = (*localAdapter)(nil)
	_ tui.InboxDrainer         = (*localAdapter)(nil)
	_ tui.WakeRequester        = (*localAdapter)(nil)
	_ tui.ModelSwapper         = (*localAdapter)(nil)
	_ tui.Reloader             = (*localAdapter)(nil)
	_ tui.PermissionController = (*localAdapter)(nil)
	_ tui.PricingController    = (*localAdapter)(nil)
	_ tui.ToolLister           = (*localAdapter)(nil)
	_ tui.SubagentReporter     = (*localAdapter)(nil)
	_ tui.StatusReporter       = (*localAdapter)(nil)
	_ tui.SlashProvider        = (*localAdapter)(nil)

	// UsageTracker is deliberately on a SEPARATE type. Capabilities
	// don't have to live on the Agent (design.md §6.0 step 2), and
	// core-agent puts this one on a `coreUsageBridge` because the
	// numbers come from its usage.Tracker, not its agent.
	_ tui.UsageTracker = (*localUsage)(nil)
)

// Capabilities this flavor deliberately does NOT implement:
//
//   - RemoteInterrupter — a local turn is cancelled by cancelling
//     the ctx core-tui already owns, so /interrupt works without it.
//     Note the real core-agent's `Agent.Interrupt() bool` does NOT
//     satisfy it: the interface wants `Interrupt(ctx) error`. The
//     attach flavor is where it earns its keep.
//   - LiveAgent — the in-process agent is per-turn by construction.
//     See attach.go for the observer-mode side.
//   - Pauser — a hold means "no new turn starts until you resume",
//     and in a per-turn host nothing starts a turn but the operator
//     pressing enter. There is nothing to hold back, and a steer
//     would have no loop to redirect. Skipping it here is also what
//     makes this binary exercise both sides of the capability:
//     /continue against the local flavor degrades to "the agent
//     doesn't implement Pauser".
//   - SessionSwitcher — one process, one session.
//   - AsyncSlashProvider — the async variant is exercised in
//     attach.go, where the latency that motivates it is real.
//
// This file satisfies 13 interfaces; attach.go adds 5 more it
// doesn't share, for 18 of the plug-in surface between them. The
// distance between "the required agent surface is tiny" (design.md
// §1 goal 3) and what a real host ends up writing is the input this
// example owes issue #77.

// Run implements tui.Agent — the one required method. The event
// translation itself lives in translate.go because the attach
// flavor needs the identical mapping off the wire.
func (a *localAdapter) Run(ctx context.Context, prompt string) iter.Seq2[tui.Event, error] {
	return func(yield func(tui.Event, error) bool) {
		model := a.inner.ModelName()
		for ev, err := range a.inner.Run(ctx, prompt) {
			if err != nil {
				yield(tui.Event{}, err)
				return
			}
			te := translateEvent(ev, model)
			if ev.UsageMetadata != nil {
				// Pricing and session accounting are the host's, not
				// core-tui's — the TUI only renders the number. The
				// adapter is what holds both halves, so it is what
				// posts the turn to the tracker.
				te.CostUSD = a.inner.AppendUsage(*ev.UsageMetadata).CostUSD
			}
			if !yield(te, nil) {
				return
			}
		}
	}
}

// Inject implements tui.InjectableAgent — a message fed INTO the
// running turn, not queued for the next one.
func (a *localAdapter) Inject(message string) error { return a.inner.Inject(message) }

// DrainInbox / PendingInboxCount implement tui.InboxDrainer. With
// Options.MidTurnInjectionMode == AutoContinueFromInbox these two
// plus Inject let core-tui drive the whole auto-continue loop
// against a runner that exposes no mid-turn hooks of its own.
func (a *localAdapter) DrainInbox() []string   { return a.inner.DrainInbox() }
func (a *localAdapter) PendingInboxCount() int { return a.inner.PendingInboxCount() }

// WakeRequested implements tui.WakeRequester: each receive raises a
// toast so a finished background agent can get the operator's eye.
func (a *localAdapter) WakeRequested() <-chan struct{} { return a.inner.WakeRequested() }

// AvailableModels / SwitchModel implement tui.ModelSwapper (/model).
// Note SwitchModel returns a tui.Agent, not a bare model name: the
// host is expected to hand back a whole new agent, so a re-wrap is
// mandatory even when the host mutates in place.
func (a *localAdapter) AvailableModels() []tui.ModelInfo {
	models := fakehost.Models()
	out := make([]tui.ModelInfo, 0, len(models))
	for _, id := range models {
		out = append(out, tui.ModelInfo{ID: id, Display: id})
	}
	return out
}

// SwitchModel rebuilds the agent around modelID and re-wraps it.
func (a *localAdapter) SwitchModel(modelID string) (tui.Agent, error) {
	next, err := a.inner.WithModel(modelID)
	if err != nil {
		return nil, err
	}
	return &localAdapter{inner: next}, nil
}

// Reload implements tui.Reloader (/reload). ReloadResult carries the
// replacement agent plus fresh views of every reload-able feed;
// leaving a field zero keeps the current value.
func (a *localAdapter) Reload(ctx context.Context) (tui.ReloadResult, error) {
	note, err := a.inner.Reload(ctx)
	if err != nil {
		return tui.ReloadResult{}, err
	}
	return tui.ReloadResult{
		Agent:      a,
		Memory:     memoryFeed(),
		MCPServers: mcpFeed(),
		Skills:     skillFeed(),
		Note:       note,
	}, nil
}

// SessionApprovals / AddAllowPatterns / AddDenyPatterns /
// AddBuiltinAllowExtra implement tui.PermissionController, backing
// /permissions, /allow and /deny against the host's gate.
func (a *localAdapter) SessionApprovals() []tui.ApprovalLog {
	logs := a.inner.Approvals()
	out := make([]tui.ApprovalLog, 0, len(logs))
	for _, l := range logs {
		out = append(out, tui.ApprovalLog{Tool: l.Tool, Key: l.Key, Decision: l.Decision})
	}
	return out
}

// AddAllowPatterns installs session-scoped allow rules.
func (a *localAdapter) AddAllowPatterns(patterns []string) error {
	return a.inner.AllowPatterns(patterns)
}

// AddDenyPatterns installs session-scoped deny rules.
func (a *localAdapter) AddDenyPatterns(patterns []string) error {
	return a.inner.DenyPatterns(patterns)
}

// AddBuiltinAllowExtra turns on one of the host's named bundles.
func (a *localAdapter) AddBuiltinAllowExtra(bundleName string) error {
	return a.inner.AllowBundle(bundleName)
}

// Refresh / Set implement tui.PricingController (/pricing).
func (a *localAdapter) Refresh(ctx context.Context) (string, error) {
	return a.inner.RefreshPricing(ctx)
}

// Set installs a manual price override for one model.
func (a *localAdapter) Set(modelID string, inputPerMTok, outputPerMTok float64) (string, error) {
	if err := a.inner.SetPricing(modelID, inputPerMTok, outputPerMTok); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: $%.2f in / $%.2f out per MTok", modelID, inputPerMTok, outputPerMTok), nil
}

// Tools implements tui.ToolLister (/tools).
func (a *localAdapter) Tools() []tui.ToolInfo {
	tools := a.inner.Tools()
	out := make([]tui.ToolInfo, 0, len(tools))
	for _, t := range tools {
		out = append(out, tui.ToolInfo{
			Name:        t.Name,
			Description: t.Description,
			Source:      t.Source,
			GateState:   t.Gate,
		})
	}
	return out
}

// Subagents implements the roster half of tui.SubagentReporter
// (/subagents).
func (a *localAdapter) Subagents() []tui.SubagentInfo {
	agents := a.inner.Subagents()
	out := make([]tui.SubagentInfo, 0, len(agents))
	for _, s := range agents {
		out = append(out, tui.SubagentInfo{
			Name:       s.Name,
			Status:     s.State,
			LastReport: s.Report,
			StartedAt:  s.Started,
		})
	}
	return out
}

// SubagentEvents implements the drill-down half of
// tui.SubagentReporter (`/subagents <name>`). In local mode the turn
// log is an in-process read, so there is no cursor to honor beyond
// filtering on since and no page limit to report.
//
// The contract's sharp edge is the error type: an unresolvable name
// must come back as *tui.SubagentNotFoundError, not an empty page,
// or the UI renders a convincing empty log for a typo.
func (a *localAdapter) SubagentEvents(_ context.Context, name string, since int64) (tui.SubagentEventPage, error) {
	turns, err := a.inner.SubagentTurns(name, since)
	var unknown *fakehost.UnknownSubagentError
	if errors.As(err, &unknown) {
		return tui.SubagentEventPage{}, &tui.SubagentNotFoundError{
			Name: unknown.Name, Available: unknown.Available,
		}
	}
	if err != nil {
		return tui.SubagentEventPage{}, err
	}
	page := tui.SubagentEventPage{Events: translateSubagentTurns(turns)}
	if n := len(page.Events); n > 0 {
		page.NextSince = page.Events[n-1].Seq
	} else {
		page.NextSince = since
	}
	return page, nil
}

// Status implements tui.StatusReporter, feeding the status header.
// Contractually it must be cheap and concurrency-safe — core-tui
// polls it off the event loop.
func (a *localAdapter) Status() tui.Status {
	return tui.Status{
		ModelName: a.inner.ModelName(),
		State:     a.inner.State(),
		Provider:  a.inner.Provider(),
	}
}

// SlashCommands / InvokeSlash implement tui.SlashProvider, exposing
// the host's own commands alongside core-tui's built-ins.
func (a *localAdapter) SlashCommands() []tui.SlashCommandSpec {
	return []tui.SlashCommandSpec{
		{Name: "btw", Aliases: []string{"by-the-way"}, Description: "ask a side question without touching the turn context"},
		{Name: "wake", Description: "fire a wake signal (demonstrates the toast)"},
	}
}

// InvokeSlash dispatches one host command.
func (a *localAdapter) InvokeSlash(ctx context.Context, name, args string) (tui.SlashResult, error) {
	switch name {
	case "btw", "by-the-way":
		question := args
		if question == "" {
			question = "what were we doing?"
		}
		answer, err := a.inner.AskSideQuestion(ctx, question)
		if err != nil {
			return tui.SlashResult{}, err
		}
		return tui.SlashResult{ModalAnswer: &tui.SideAnswer{Question: question, Answer: answer}}, nil
	case "wake":
		a.inner.RequestWake()
		return tui.SlashResult{SystemMessage: "wake signal sent"}, nil
	default:
		return tui.SlashResult{}, fmt.Errorf("unknown command /%s", name)
	}
}

// localUsage is the UsageTracker projection over the host's own
// accounting — the separate-type case from design.md §6.0.
type localUsage struct {
	inner *fakehost.Agent
}

func (u *localUsage) SessionTotals() tui.Usage {
	s := u.inner.SessionUsage()
	return tui.Usage{InputTokens: s.InputTokens, OutputTokens: s.OutputTokens}
}

func (u *localUsage) SessionCostUSD() float64 { return u.inner.SessionUsage().CostUSD }

func (u *localUsage) LastTurn() (tui.Usage, float64) {
	l := u.inner.LastTurnUsage()
	return tui.Usage{InputTokens: l.InputTokens, OutputTokens: l.OutputTokens}, l.CostUSD
}

func (u *localUsage) ContextWindowSize() int {
	size, _ := u.inner.ContextWindow()
	return size
}

func (u *localUsage) ContextWindowUsed() int {
	_, used := u.inner.ContextWindow()
	return used
}

func (u *localUsage) SessionTurns() int { return u.inner.Turns() }

func (u *localUsage) SessionDuration() time.Duration { return u.inner.Uptime() }

// memoryFeed / mcpFeed / skillFeed are the static per-session feeds
// core-tui renders for /memory, /mcp and /skills. They are plain
// Options fields rather than capability interfaces, which is why a
// host has to re-supply them through ReloadResult on /reload.
func memoryFeed() []tui.MemoryFile {
	return []tui.MemoryFile{
		{Path: "AGENTS.md", Excerpt: "project memory for the reference host", Bytes: 4_812},
		{Path: "~/.core-agent/AGENTS.md", Excerpt: "operator-level preferences", Bytes: 903},
	}
}

func mcpFeed() []tui.MCPServerInfo {
	return []tui.MCPServerInfo{
		{
			Name: "k8s", Transport: "stdio", Connected: true, ToolCount: 1,
			Tools: []tui.MCPToolInfo{{Name: "kubectl_get", Description: "read Kubernetes objects"}},
		},
		{
			Name: "pagerduty", Transport: "http", URL: "https://mcp.example/pd", Connected: false, ToolCount: 1,
			Tools: []tui.MCPToolInfo{{Name: "pagerduty_ack", Description: "acknowledge an incident"}},
		},
	}
}

func skillFeed() []tui.SkillInfo {
	return []tui.SkillInfo{
		{Name: "incident-triage", Description: "walk a production incident", Source: "local", ToolCount: 3},
	}
}
