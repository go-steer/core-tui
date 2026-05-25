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
	"time"

	tea "charm.land/bubbletea/v2"
)

// spinnerCadence is the rotation period for thinking/working verbs
// (R-CHAT-3).
const spinnerCadence = 3 * time.Second

// toastTTL is how long a wake-triggered toast banner stays visible
// before auto-dismissing (R-WAKE-1). 4s is long enough to read
// without being intrusive.
const toastTTL = 4 * time.Second

// toastTick schedules a toastClearMsg toastTTL into the future.
func toastTick() tea.Cmd {
	return tea.Tick(toastTTL, func(time.Time) tea.Msg {
		return toastClearMsg{}
	})
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

	go func() {
		var fail error
		for ev, err := range agent.Run(ctx, prompt) {
			if err != nil {
				fail = err
				break
			}
			emitEvent(ctx, m.eventCh, ev)
		}

		var terminal tea.Msg
		switch {
		case fail != nil && errors.Is(fail, context.Canceled):
			terminal = turnCancelledMsg{}
		case ctx.Err() != nil:
			terminal = turnCancelledMsg{}
		case fail != nil:
			terminal = turnErrMsg{err: fail}
		default:
			terminal = turnDoneMsg{elapsed: time.Since(started)}
		}
		select {
		case m.eventCh <- terminal:
		case <-time.After(time.Second):
			// listener is gone — drop the terminal silently.
		}
	}()

	return cancel
}

// emitEvent splits a single agent Event into one or more tea.Msgs
// pushed onto the channel. Send is best-effort against ctx
// cancellation so the goroutine doesn't block forever if the listener
// has gone away.
func emitEvent(ctx context.Context, ch chan<- tea.Msg, ev Event) {
	send := func(msg tea.Msg) {
		select {
		case ch <- msg:
		case <-ctx.Done():
		}
	}
	if ev.Text != "" {
		send(streamChunkMsg{text: ev.Text, partial: ev.Partial})
	}
	for _, tc := range ev.ToolCalls {
		send(toolCallMsg{id: tc.ID, name: tc.Name, args: tc.Args})
	}
	if ev.Usage != nil {
		send(usageMsg{usage: *ev.Usage})
	}
}
