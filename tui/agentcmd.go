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
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Compile-time enforcement that the unexported *elicitor satisfies
// the public Elicitor interface — flags a regression early if the
// method set drifts.
var _ Elicitor = (*elicitor)(nil)

// spinnerCadence is the rotation period for thinking/working verbs
// (R-CHAT-3).
const spinnerCadence = 3 * time.Second

// toastTTL is how long a wake-triggered toast banner stays visible
// before auto-dismissing (R-WAKE-1). 4s is long enough to read
// without being intrusive.
const toastTTL = 4 * time.Second

// ctrlCExitTTL bounds how long the first idle Ctrl+C arms the
// "press again to exit" one-shot. 2s matches Claude Code's tempo
// — long enough to be a deliberate second press, short enough that
// a stray follow-up Ctrl+C minutes later won't unexpectedly quit.
const ctrlCExitTTL = 2 * time.Second

// toastTick schedules a toastClearMsg toastTTL into the future.
func toastTick() tea.Cmd {
	return tea.Tick(toastTTL, func(time.Time) tea.Msg {
		return toastClearMsg{}
	})
}

// forceRenderTick schedules a forceRenderMsg ~1ms into the future
// to guarantee a fresh Update → View cycle after handlers that
// would otherwise return a nil Cmd in a quiet window (issue #24).
// See the forceRenderMsg doc comment for the underlying scheduler
// quirk this works around.
func forceRenderTick() tea.Cmd {
	return tea.Tick(time.Millisecond, func(time.Time) tea.Msg {
		return forceRenderMsg{}
	})
}

// coalesceWindow is the delay between the first markViewportDirty
// call and the coalescedRefreshMsg that actually re-runs
// refreshViewport. One millisecond matches forceRenderTick's cadence
// — imperceptible to the operator, but long enough to fold many
// SSE events (the whole eventCh buffer, typically) into a single
// paint during attach-to-long-session catch-up.
const coalesceWindow = time.Millisecond

// markViewportDirty flags the viewport as needing a repaint. Cheap
// (a bool flip) — safe to call from every event handler that
// mutates history / usage / model state. The actual refreshViewport
// call runs later via the coalescedRefreshMsg handler.
func (m *Model) markViewportDirty() {
	m.viewportDirty = true
}

// scheduleCoalescedRefresh returns a Cmd that fires coalescedRefreshMsg
// after coalesceWindow, or nil if a refresh is already pending or
// nothing has marked dirty. Idempotent — safe to include in every
// event handler's returned batch; extra calls collapse to nil while
// a tick is in flight, so no matter how many events land in the
// window they trigger exactly one refreshViewport.
func (m *Model) scheduleCoalescedRefresh() tea.Cmd {
	if m.refreshPending || !m.viewportDirty {
		return nil
	}
	m.refreshPending = true
	return tea.Tick(coalesceWindow, func(time.Time) tea.Msg {
		return coalescedRefreshMsg{}
	})
}

// liveStreamRenderCmd returns the Cmd that chat-content Msg
// handlers (streamChunkMsg, toolCallMsg, toolResultMsg, usageMsg)
// should yield after applying their state change (issue #26).
//
// In Run mode it's just the bare eventListener — the per-turn
// iterator keeps the program loop busy with concurrent Msgs.
//
// In LiveAgent mode it batches the eventListener with a paint
// kick so a single non-partial chunk arriving in a quiet window
// (single-shot model reply, solo autonomous tool call) paints
// without waiting for the operator's next keypress. Preference
// order:
//
//  1. scheduleCoalescedRefresh() when the handler flipped
//     viewportDirty — the coalescedRefreshMsg tick both re-runs
//     refreshViewport (paints the state change) and satisfies
//     issue #24's "guarantee an Update → View cycle" contract.
//  2. forceRenderTick() as fallback for the rare handler branch
//     that returns liveStreamRenderCmd without mutating state
//     (e.g. usageMsg with empty payload) — preserves issue #24's
//     paint-kick guarantee without doing redundant work.
//
// The extras parameter folds in additional concurrent Cmds the
// handler may need (e.g. spinnerTick for the partial-text path).
func (m *Model) liveStreamRenderCmd(extras ...tea.Cmd) tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(extras)+3)
	cmds = append(cmds, m.eventListener())
	cmds = append(cmds, extras...)
	if m.liveMode {
		if refresh := m.scheduleCoalescedRefresh(); refresh != nil {
			cmds = append(cmds, refresh)
		} else {
			cmds = append(cmds, forceRenderTick())
		}
	} else if refresh := m.scheduleCoalescedRefresh(); refresh != nil {
		cmds = append(cmds, refresh)
	}
	if len(cmds) == 1 {
		return cmds[0]
	}
	return tea.Batch(cmds...)
}

// pendingExitTick schedules a pendingExitClearMsg ctrlCExitTTL into
// the future so the warn-then-exit one-shot disarms if the operator
// doesn't follow through.
func pendingExitTick() tea.Cmd {
	return tea.Tick(ctrlCExitTTL, func(time.Time) tea.Msg {
		return pendingExitClearMsg{}
	})
}

// promptListener returns a Cmd that blocks on the prompter's
// request channel and forwards each inbound request as a
// permissionRequestMsg (R-PERM-1). Re-issued by Update after every
// dispatch so the loop drains one request at a time. Returns nil
// when no prompter is wired.
func (m Model) promptListener() tea.Cmd {
	if m.opts.Prompter == nil {
		return nil
	}
	p, ok := m.opts.Prompter.(*Prompter)
	if !ok {
		// Host wired its own PermissionPrompter implementation;
		// the TUI can't drain a channel it doesn't own. Adapters
		// pass tui.NewPrompter() — this branch is the diagnostic
		// path if someone substitutes their own.
		return nil
	}
	return func() tea.Msg {
		req, ok := p.nextRequest(context.Background())
		if !ok {
			return nil
		}
		return permissionRequestMsg{req: req}
	}
}

// notifyListener returns a Cmd that blocks on the host-supplied
// Notifier's channel and forwards each inbound notice as a
// noticeMsg (issue #30). Re-issued by Update after every notice
// so the loop drains continuously. Returns nil when no Notifier
// is wired (the common case — Notifier is opt-in).
func (m Model) notifyListener() tea.Cmd {
	if m.opts.Notifier == nil {
		return nil
	}
	ch := m.opts.Notifier.ch
	return func() tea.Msg {
		env, ok := <-ch
		if !ok {
			return nil // channel closed; subscription ends
		}
		// Direct conversion — noticeEnvelope and noticeMsg have
		// identical fields by design (the listener is just a
		// channel-to-msg bridge). Keep them in sync if either grows.
		return noticeMsg(env)
	}
}

// elicitListener returns a Cmd that blocks on the elicitor's
// request channel and forwards each inbound request as an
// elicitRequestMsg (R-ELIC-1). Same drain-loop pattern as
// promptListener.
func (m Model) elicitListener() tea.Cmd {
	if m.opts.Elicitor == nil {
		return nil
	}
	e, ok := m.opts.Elicitor.(*elicitor)
	if !ok {
		return nil
	}
	return func() tea.Msg {
		flow, ok := e.nextRequest(context.Background())
		if !ok {
			return nil
		}
		return elicitRequestMsg{serverName: flow.serverName, req: flow.req}
	}
}

// eventListener returns a Cmd that blocks on the model's event channel
// and forwards the next message into the Bubble Tea loop. Update
// re-issues this Cmd after every event-flavored message so the loop
// drains the channel one message at a time without buffering issues.
func (m Model) eventListener() tea.Cmd {
	if m.eventCh == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := <-m.eventCh
		if !ok {
			return nil
		}
		return msg
	}
}

// spinnerTick returns a Cmd that fires spinnerTickMsg after one
// spinnerCadence. Update re-issues it on every tick while a turn is
// in flight (R-CHAT-3).
func spinnerTick() tea.Cmd {
	return tea.Tick(spinnerCadence, func(time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

// wakeListener returns a Cmd that blocks on the agent's
// WakeRequested channel and forwards each receive as a wakeMsg
// (R-WAKE-1). Update re-issues the Cmd after every wakeMsg so the
// loop drains continuously. Returns nil when the host's agent
// doesn't satisfy WakeRequester.
func (m Model) wakeListener() tea.Cmd {
	waker, ok := m.opts.Agent.(WakeRequester)
	if !ok {
		return nil
	}
	ch := waker.WakeRequested()
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		_, ok := <-ch
		if !ok {
			return nil // channel closed; subscription ends
		}
		return wakeMsg{}
	}
}

// startAgentTurn launches a goroutine that ranges over agent.Run and
// translates each Event into a tea.Msg pushed onto m.eventCh. Returns
// the cancel func for the turn's context so Esc-interrupt (R-CHAT-6)
// can call it. The goroutine emits exactly one terminal message
// (turnDoneMsg / turnErrMsg / turnCancelledMsg) before returning.
func (m Model) startAgentTurn(agent Agent, prompt string) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	started := time.Now()
	// Snapshot the session generation at goroutine start so any
	// terminal msg we emit later carries the gen of the Agent that
	// owned this turn — Update drops it if applySwitchTarget has
	// since bumped m.sessionGen (see model.go). Same rationale for
	// per-event chat msgs; emitEvent takes gen as an argument.
	gen := m.sessionGen

	go func() {
		var fail error
		for ev, err := range agent.Run(ctx, prompt) {
			if err != nil {
				fail = err
				break
			}
			emitEvent(ctx, m.eventCh, gen, ev)
		}

		var terminal tea.Msg
		switch {
		case fail != nil && errors.Is(fail, context.Canceled):
			terminal = turnCancelledMsg{gen: gen}
		case ctx.Err() != nil:
			terminal = turnCancelledMsg{gen: gen}
		case fail != nil:
			terminal = turnErrMsg{gen: gen, err: fail}
		default:
			terminal = turnDoneMsg{gen: gen, elapsed: time.Since(started)}
		}
		select {
		case m.eventCh <- terminal:
		case <-time.After(time.Second):
			// listener is gone — drop the terminal silently.
		}
	}()

	return cancel
}

// startLiveStream launches the single long-lived goroutine that
// drains a LiveAgent (issue #22). Returns the cancel func for the
// stream's context so Esc / shutdown paths can stop it (today
// Esc is a no-op for the live stream by design; this hook exists
// for future "force reconnect" affordances and for clean shutdown
// in tests).
//
// The goroutine ranges over agent.Events(ctx) and:
//   - on each (ev, nil): fan out via emitEvent like the Run path
//   - on each (zero, err): forward liveStreamErrMsg and KEEP
//     draining — the implementation decides whether to keep
//     yielding
//   - on iterator return: forward liveStreamEndedMsg ONCE and exit
//
// ctx cancellation stops the loop without yielding a final error
// (per the LiveAgent semantics).
func (m Model) startLiveStream(agent LiveAgent) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	// Snapshot the session generation at goroutine start; every
	// msg emitted from this drain carries it so a subsequent
	// applySwitchTarget invalidates the stale stream cleanly.
	gen := m.sessionGen
	go func() {
		for ev, err := range agent.Events(ctx) {
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				select {
				case m.eventCh <- liveStreamErrMsg{gen: gen, err: err}:
				case <-ctx.Done():
					return
				}
				continue
			}
			emitEvent(ctx, m.eventCh, gen, ev)
		}
		// Iterator returned cleanly (or stopped yielding). Tell the
		// TUI so the "Disconnected" banner can render.
		select {
		case m.eventCh <- liveStreamEndedMsg{gen: gen}:
		case <-time.After(time.Second):
			// listener gone; drop quietly.
		}
	}()
	return cancel
}

// emitEvent splits a single agent Event into one or more tea.Msgs
// pushed onto the channel. Send is best-effort against ctx
// cancellation so the goroutine doesn't block forever if the listener
// has gone away. gen is the sessionGen the caller (startAgentTurn /
// startLiveStream) captured at goroutine start; every emitted msg
// carries it so Update can drop stragglers from an outgoing session.
func emitEvent(ctx context.Context, ch chan<- tea.Msg, gen uint64, ev Event) {
	send := func(msg tea.Msg) {
		select {
		case ch <- msg:
		case <-ctx.Done():
		}
	}
	if ev.Text != "" {
		send(streamChunkMsg{gen: gen, text: ev.Text, partial: ev.Partial})
	}
	for _, tc := range ev.ToolCalls {
		send(toolCallMsg{gen: gen, id: tc.ID, name: tc.Name, args: tc.Args})
	}
	for _, tr := range ev.ToolResults {
		send(toolResultMsg{
			gen:       gen,
			id:        tr.ID,
			name:      tr.Name,
			response:  tr.Response,
			err:       tr.Error,
			latencyMs: resolveToolLatencyMs(tr),
			savings:   resolveToolSavings(tr),
		})
	}
	if ev.Usage != nil {
		send(usageMsg{gen: gen, usage: *ev.Usage, costUSD: ev.CostUSD, model: ev.Model})
	} else if ev.Model != "" {
		// Adapters that emit a model identifier on a usage-less
		// event (e.g. the first stream chunk) still feed
		// m.currentModel via this msg so the per-turn footer
		// renders the model name from the first event onward.
		send(usageMsg{gen: gen, model: ev.Model})
	}
	// Push-mode SSE payloads (issue #40, spec v1.1.0). One emit
	// per populated optional field. All independent — a single
	// Event MAY carry multiple (rare but tolerated) and they fan
	// out as separate msgs. Hosts that aren't speaking push leave
	// these nil and the cases below are no-ops.
	if ev.StatusUpdate != nil {
		send(statusUpdateMsg{gen: gen, status: *ev.StatusUpdate})
	}
	if ev.UsageUpdate != nil {
		send(usageUpdateMsg{gen: gen, update: *ev.UsageUpdate})
	}
	if ev.Inbox != nil {
		send(inboxStateMsg{gen: gen, event: *ev.Inbox})
	}
	if ev.TurnComplete != nil {
		send(turnSummaryMsg{gen: gen, summary: *ev.TurnComplete})
	}
	if ev.TurnError != nil {
		send(turnErrorMsg{gen: gen, turnError: *ev.TurnError})
	}
}

// permanentStreamStatusMarkers is the fallback substring set the TUI
// scans when a live-stream error doesn't implement PermanentStreamError.
// Matches the string form core-agent's remote adapter already produces
// ("status 404: session not found", etc.). Adapters can adopt the
// PermanentStreamError interface to bypass the heuristic entirely.
var permanentStreamStatusMarkers = []string{
	"status 404",
	"status 401",
	"status 403",
}

// isPermanentStreamErr reports whether err represents a live-stream
// condition the TUI can't recover from by retrying (session gone, auth
// revoked). Adapters signal this by implementing PermanentStreamError;
// as a fallback we string-match the HTTP status markers listed above
// so existing adapters keep the same behavior without a code change.
// See issue #51.
func isPermanentStreamErr(err error) bool {
	if err == nil {
		return false
	}
	var pse PermanentStreamError
	if errors.As(err, &pse) && pse.PermanentStreamErr() {
		return true
	}
	msg := err.Error()
	for _, marker := range permanentStreamStatusMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}
