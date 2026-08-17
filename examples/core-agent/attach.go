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
	"sync"
	"time"

	"github.com/go-steer/core-tui/examples/core-agent/fakehost"
	"github.com/go-steer/core-tui/tui"
)

// attachAdapter is the attachclient flavor: core-tui driving a
// core-agent that lives in another process, reached over HTTP + SSE.
// It mirrors core-agent's `internal/coretuiremote.Adapter`, which is
// what `cmd/core-agent-tui` hands to tui.Run.
//
// From core-tui's side this is the same contract localAdapter
// satisfies — that equivalence is what I-CORE-AGENT asks the example
// to demonstrate. What differs is entirely inside the methods:
// every capability is a round trip, so the ones core-tui expects to
// answer synchronously have to be served from cache.
type attachAdapter struct {
	client      *fakehost.Client
	sessionPath string

	mu       sync.Mutex
	lastSeq  int64
	status   fakehost.StatusInfo
	usage    fakehost.UsageInfo
	polledAt time.Time
}

func newAttachAdapter(client *fakehost.Client, sessionPath string) *attachAdapter {
	return &attachAdapter{client: client, sessionPath: sessionPath}
}

// ---------------------------------------------------------------
// The canary, remote half. Between this block and local.go's, the
// example touches 18 of core-tui's plug-in interfaces; a rename or
// signature change on any of them breaks `go build ./...`.
// ---------------------------------------------------------------

var (
	_ tui.Agent              = (*attachAdapter)(nil)
	_ tui.LiveAgent          = attachObserver{}
	_ tui.InjectableAgent    = (*attachAdapter)(nil)
	_ tui.RemoteInterrupter  = (*attachAdapter)(nil)
	_ tui.ToolLister         = (*attachAdapter)(nil)
	_ tui.SubagentReporter   = (*attachAdapter)(nil)
	_ tui.StatusReporter     = (*attachAdapter)(nil)
	_ tui.SessionSwitcher    = (*attachAdapter)(nil)
	_ tui.UsageTracker       = (*attachAdapter)(nil)
	_ tui.AsyncSlashProvider = (*attachAdapter)(nil)
)

// Capabilities this flavor deliberately does NOT implement, matching
// the "defer" column of MIGRATION.md §4.2: ModelSwapper, Reloader,
// PermissionController and PricingController have no attach-API
// RPCs behind them, so /model, /reload, /permissions and /pricing
// degrade to "not available in this host" — which is the graceful
// path the capability design promises, and the reason the split
// between the two flavors is worth showing.

// Run implements tui.Agent. In attach mode a "turn" is not a call
// the operator's process makes; it is an inject followed by watching
// the shared stream until the daemon says the turn is done. Any
// other attached operator sees the same events.
func (a *attachAdapter) Run(ctx context.Context, prompt string) iter.Seq2[tui.Event, error] {
	return func(yield func(tui.Event, error) bool) {
		// Cancel the subscription when the turn ends, or the SSE
		// connection outlives the turn it was opened for and the
		// daemon accumulates readers.
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		// Subscribe BEFORE injecting, or the echo of our own prompt
		// races the subscription and the first tokens are lost.
		frames, err := a.client.Stream(ctx, a.sessionPath, a.cursor())
		if err != nil {
			yield(tui.Event{}, fmt.Errorf("stream: %w", err))
			return
		}
		if err := a.client.Inject(ctx, a.sessionPath, prompt); err != nil {
			yield(tui.Event{}, fmt.Errorf("inject: %w", err))
			return
		}
		model := a.Status().ModelName
		for f := range frames {
			a.advance(f.Seq)
			if f.Event == nil {
				continue
			}
			if !yield(translateEvent(f.Event, model), nil) {
				return
			}
			if f.Event.TurnComplete {
				// The daemon just booked the turn's usage. Drop the
				// cache so /stats and the status header don't show
				// pre-turn numbers for the next second.
				a.invalidate()
				return
			}
		}
	}
}

// attachObserver is the same adapter in observer mode. Events lives
// on this wrapper rather than on attachAdapter itself for a reason
// worth knowing about: LiveAgent WINS over Run, silently, whenever
// one type satisfies both. A host that grows an Events method to
// support observer mode therefore turns OFF its per-turn path
// everywhere, with no compile error and no runtime warning. Keeping
// the two on separate types makes main.go choose explicitly.
type attachObserver struct{ *attachAdapter }

// Events implements tui.LiveAgent. core-tui calls it once at startup
// and ranges over it for the life of the session, so reconnection is
// entirely the adapter's problem: resume from the last sequence,
// back off between attempts, and surface transport trouble in the
// error position (core-tui renders those as chat rows and keeps
// draining).
func (o attachObserver) Events(ctx context.Context) iter.Seq2[tui.Event, error] {
	a := o.attachAdapter
	return func(yield func(tui.Event, error) bool) {
		const (
			initialBackoff = time.Second
			maxBackoff     = 30 * time.Second
		)
		backoff := initialBackoff
		for ctx.Err() == nil {
			frames, err := a.client.Stream(ctx, a.sessionPath, a.cursor())
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				if !yield(tui.Event{}, fmt.Errorf("stream disconnected: %w — retrying", err)) {
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(backoff):
				}
				if backoff < maxBackoff {
					backoff *= 2
				}
				continue
			}
			backoff = initialBackoff
			model := a.Status().ModelName
			for f := range frames {
				a.advance(f.Seq)
				if f.Event == nil {
					continue
				}
				if !yield(translateEvent(f.Event, model), nil) {
					return
				}
				if f.Event.TurnComplete {
					a.invalidate()
				}
			}
		}
	}
}

// Inject implements tui.InjectableAgent: one POST, no stream — the
// answer arrives on the Events subscription like everything else.
func (a *attachAdapter) Inject(message string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return a.client.Inject(ctx, a.sessionPath, message)
}

// Interrupt implements tui.RemoteInterrupter. This is the capability
// attach mode exists for: an autonomous turn the daemon started has
// no local context for core-tui to cancel, so /interrupt has to go
// over the wire. Note the signature difference from core-agent's own
// `Agent.Interrupt() bool` — the adapter absorbs it.
func (a *attachAdapter) Interrupt(ctx context.Context) error {
	cancelled, err := a.client.Interrupt(ctx, a.sessionPath)
	if err != nil {
		return err
	}
	if !cancelled {
		return errors.New("daemon reported no turn in flight")
	}
	return nil
}

// Tools implements tui.ToolLister over one round trip.
func (a *attachAdapter) Tools() []tui.ToolInfo {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tools, err := a.client.Tools(ctx, a.sessionPath)
	if err != nil {
		return nil
	}
	out := make([]tui.ToolInfo, 0, len(tools))
	for _, t := range tools {
		out = append(out, tui.ToolInfo{
			Name: t.Name, Description: t.Description, Source: t.Source, GateState: t.Gate,
		})
	}
	return out
}

// Subagents implements the roster half of tui.SubagentReporter over
// one round trip.
func (a *attachAdapter) Subagents() []tui.SubagentInfo {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	agents, err := a.client.Agents(ctx, a.sessionPath)
	if err != nil {
		return nil
	}
	out := make([]tui.SubagentInfo, 0, len(agents))
	for _, s := range agents {
		out = append(out, tui.SubagentInfo{
			Name: s.Name, Status: s.State, LastReport: s.Report, StartedAt: s.Started,
		})
	}
	return out
}

// SubagentEvents implements the drill-down half of
// tui.SubagentReporter — the paged, cursored turn log behind
// `/subagents <name>`. The contract's sharp edge is the error type:
// an unresolvable name must come back as *tui.SubagentNotFoundError,
// not an empty page, or the UI shows a convincing empty log for a
// typo.
func (a *attachAdapter) SubagentEvents(ctx context.Context, name string, since int64) (tui.SubagentEventPage, error) {
	resp, err := a.client.SubagentEvents(ctx, a.sessionPath, name, since)
	var unknown *fakehost.UnknownSubagentError
	if errors.As(err, &unknown) {
		return tui.SubagentEventPage{}, &tui.SubagentNotFoundError{
			Name: unknown.Name, Available: unknown.Available,
		}
	}
	if err != nil {
		return tui.SubagentEventPage{}, err
	}
	return tui.SubagentEventPage{
		Events:    translateSubagentTurns(resp.Events),
		NextSince: resp.NextSince,
		Truncated: resp.Truncated,
	}, nil
}

// Status implements tui.StatusReporter. The contract says cheap and
// non-blocking, so this serves the last poll and refreshes in the
// background — a synchronous GET here would put a network round trip
// on the status-header refresh path.
func (a *attachAdapter) Status() tui.Status {
	a.poll()
	a.mu.Lock()
	defer a.mu.Unlock()
	return tui.Status{
		ModelName: a.status.Model,
		State:     a.status.State,
		Provider:  a.status.Provider,
	}
}

// SessionTotals / SessionCostUSD / LastTurn / ContextWindow* /
// SessionTurns / SessionDuration implement tui.UsageTracker off the
// same cached poll. Seven methods for what is one JSON document on
// the wire — a consolidation candidate for issue #77.
func (a *attachAdapter) SessionTotals() tui.Usage {
	u := a.cachedUsage()
	return tui.Usage{InputTokens: u.Session.InputTokens, OutputTokens: u.Session.OutputTokens}
}

func (a *attachAdapter) SessionCostUSD() float64 { return a.cachedUsage().Session.CostUSD }

func (a *attachAdapter) LastTurn() (tui.Usage, float64) {
	u := a.cachedUsage()
	return tui.Usage{InputTokens: u.LastTurn.InputTokens, OutputTokens: u.LastTurn.OutputTokens}, u.LastTurn.CostUSD
}

func (a *attachAdapter) ContextWindowSize() int { return a.cachedUsage().WindowSize }

func (a *attachAdapter) ContextWindowUsed() int { return a.cachedUsage().WindowUsed }

func (a *attachAdapter) SessionTurns() int { return a.cachedUsage().Turns }

func (a *attachAdapter) SessionDuration() time.Duration {
	return time.Duration(a.cachedUsage().UptimeMillis) * time.Millisecond
}

// Sessions implements the read half of tui.SessionSwitcher (/switch):
// the daemon's session list, plus an action row that types in
// another daemon's URL. Action rows never reach SwitchToSession —
// their own Submit closure produces the target.
func (a *attachAdapter) Sessions() []tui.SessionInfo {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sessions, err := a.client.Sessions(ctx)
	if err != nil {
		return nil
	}
	out := make([]tui.SessionInfo, 0, len(sessions)+1)
	for _, s := range sessions {
		out = append(out, tui.SessionInfo{
			ID:          s.Path,
			Display:     s.Label,
			Description: s.Note,
			Current:     s.Path == a.sessionPath,
		})
	}
	return append(out, tui.SessionInfo{
		ID:      "attach",
		Display: "+ Attach to endpoint…",
		Input: &tui.SessionInput{
			Title:       "Attach to Endpoint",
			Prompt:      "Daemon URL:",
			Placeholder: "http://127.0.0.1:7778",
			Validate: func(v string) string {
				if v == "" {
					return "endpoint is required"
				}
				return ""
			},
			Submit: func(v string) (tui.SwitchTarget, error) {
				next := newAttachAdapter(fakehost.NewClient(v), fakehost.SessionPath)
				return tui.SwitchTarget{
					Agent:        next,
					UsageTracker: next,
					Note:         "attached to " + v,
				}, nil
			},
		},
	})
}

// SwitchToSession implements the write half. core-tui does not touch
// the outgoing agent — teardown, if any, belongs here.
func (a *attachAdapter) SwitchToSession(id string) (tui.SwitchTarget, error) {
	if id == a.sessionPath {
		return tui.SwitchTarget{}, errors.New("already attached to that session")
	}
	next := newAttachAdapter(a.client, id)
	return tui.SwitchTarget{
		Agent:        next,
		UsageTracker: next,
		Note:         "attached to " + id,
	}, nil
}

// SlashCommands / InvokeSlashAsync implement tui.AsyncSlashProvider.
// The async shape is the right one for attach mode: every command is
// a network call, and the preamble lands a "this is running" row in
// the chat so a slow round trip doesn't look like a dropped
// keystroke.
func (a *attachAdapter) SlashCommands() []tui.SlashCommandSpec {
	return []tui.SlashCommandSpec{
		{Name: "btw", Aliases: []string{"by-the-way"}, Description: "ask the remote agent a side question"},
	}
}

// InvokeSlashAsync returns the preamble synchronously and the result
// on a channel. Exactly one send, then done.
func (a *attachAdapter) InvokeSlashAsync(ctx context.Context, name, args string) (string, <-chan tui.SlashResultOrErr) {
	results := make(chan tui.SlashResultOrErr, 1)
	if name != "btw" && name != "by-the-way" {
		results <- tui.SlashResultOrErr{Err: fmt.Errorf("unknown command /%s", name)}
		return "", results
	}
	question := args
	if question == "" {
		question = "what were we doing?"
	}
	go func() {
		answer, err := a.client.Btw(ctx, a.sessionPath, question)
		if err != nil {
			results <- tui.SlashResultOrErr{Err: err}
			return
		}
		results <- tui.SlashResultOrErr{Res: tui.SlashResult{
			ModalAnswer: &tui.SideAnswer{Question: question, Answer: answer},
		}}
	}()
	return "asking the daemon…", results
}

// cursor / advance track the SSE resume point so a reconnect picks
// up where the last frame left off instead of replaying the session.
func (a *attachAdapter) cursor() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastSeq
}

func (a *attachAdapter) advance(seq int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if seq > a.lastSeq {
		a.lastSeq = seq
	}
}

// invalidate forces the next Status / usage read to go to the wire.
func (a *attachAdapter) invalidate() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.polledAt = time.Time{}
}

func (a *attachAdapter) cachedUsage() fakehost.UsageInfo {
	a.poll()
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.usage
}

// poll refreshes the status + usage cache at most once a second, on
// the caller's goroutine. core-tui pulls these off its event loop,
// so a second of staleness is cheaper than a round trip per frame.
func (a *attachAdapter) poll() {
	a.mu.Lock()
	if time.Since(a.polledAt) < time.Second {
		a.mu.Unlock()
		return
	}
	a.polledAt = time.Now()
	a.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	status, statusErr := a.client.Status(ctx, a.sessionPath)
	usage, usageErr := a.client.Usage(ctx, a.sessionPath)

	a.mu.Lock()
	defer a.mu.Unlock()
	if statusErr == nil {
		a.status = status
	}
	if usageErr == nil {
		a.usage = usage
	}
}
