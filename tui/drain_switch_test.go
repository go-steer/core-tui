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

// Tests for issue #355: the notifier, wake and event listeners keep
// one consumer per channel across a session switch, and nothing a
// released listener still delivers reaches the new session.
//
// Notices and wakes carry no reply, so a stale one is dropped, where
// #353's stale requests are refused. The two share the tests below
// through drainKind. The event channel is never replaced, so it has
// its own tests: one consumer, which is what keeps its messages in
// order, and a drain that a handler returning no listener cannot stop.

package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// drainKind adapts the notifier or wake channel to the shared tests.
// Sources are passed as any: a *Notifier, or a *wakingAgent whose
// channel is the source.
type drainKind struct {
	name   string
	newSrc func() any
	// model builds a model wired to src.
	model func(t *testing.T, src any) *model
	// replace is a SwitchTarget that replaces src's channel with a
	// fresh one; keeps are the SwitchTargets that keep it.
	replace func(src any) *SwitchTarget
	keeps   []namedTarget
	// target is a SwitchTarget that installs src, as a switch back to
	// it would.
	target   func(src any) *SwitchTarget
	listener func(m *model) tea.Cmd
	// send pushes one signal on src.
	send  func(t *testing.T, src any)
	isMsg func(msg tea.Msg) bool
	// rendered reports whether a signal reached the transcript.
	rendered func(m *model) int
}

type namedTarget struct {
	name   string
	target func(src any) *SwitchTarget
}

func drainKinds() []drainKind {
	sized := func(t *testing.T, opts Options) *model {
		t.Helper()
		m := newModel(opts)
		out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
		m = out.(*model)
		t.Cleanup(m.endListeners)
		return m
	}
	countRole := func(m *model, role Role) int {
		n := 0
		for _, e := range m.history.Snapshot() {
			if e.Role == role {
				n++
			}
		}
		return n
	}
	return []drainKind{
		{
			name:   "notifier",
			newSrc: func() any { return NewNotifier() },
			model: func(t *testing.T, src any) *model {
				return sized(t, Options{Agent: &bareAgent{id: "old"}, Notifier: src.(*Notifier)})
			},
			replace: func(any) *SwitchTarget {
				return &SwitchTarget{Agent: &bareAgent{id: "new"}, Notifier: NewNotifier()}
			},
			keeps: []namedTarget{
				{"field nil", func(any) *SwitchTarget { return &SwitchTarget{Agent: &bareAgent{id: "new"}} }},
				{"same instance handed back", func(src any) *SwitchTarget {
					return &SwitchTarget{Agent: &bareAgent{id: "new"}, Notifier: src.(*Notifier)}
				}},
			},
			target: func(src any) *SwitchTarget {
				return &SwitchTarget{Agent: &bareAgent{id: "back"}, Notifier: src.(*Notifier)}
			},
			listener: (*model).notifyListener,
			send:     func(_ *testing.T, src any) { src.(*Notifier).Notify("notice") },
			isMsg:    func(msg tea.Msg) bool { _, ok := msg.(noticeMsg); return ok },
			rendered: func(m *model) int { return countRole(m, RoleNotice) },
		},
		{
			name:   "wake",
			newSrc: func() any { return newWakingAgent() },
			model: func(t *testing.T, src any) *model {
				return sized(t, Options{Agent: src.(*wakingAgent)})
			},
			replace: func(any) *SwitchTarget { return &SwitchTarget{Agent: newWakingAgent()} },
			keeps: []namedTarget{
				{"same agent handed back", func(src any) *SwitchTarget {
					return &SwitchTarget{Agent: src.(*wakingAgent)}
				}},
				{"new agent on the same channel", func(src any) *SwitchTarget {
					return &SwitchTarget{Agent: &wakingAgent{wakeCh: src.(*wakingAgent).wakeCh}}
				}},
			},
			target:   func(src any) *SwitchTarget { return &SwitchTarget{Agent: src.(*wakingAgent)} },
			listener: (*model).wakeListener,
			send: func(t *testing.T, src any) {
				select {
				case src.(*wakingAgent).wakeCh <- struct{}{}:
				case <-time.After(time.Second):
					t.Fatal("wake channel full")
				}
			},
			isMsg:    func(msg tea.Msg) bool { _, ok := msg.(wakeMsg); return ok },
			rendered: func(m *model) int { return countRole(m, RoleSystem) },
		},
	}
}

// awaitMsg returns the next message from ch that match accepts.
func awaitMsg(t *testing.T, ch <-chan tea.Msg, match func(tea.Msg) bool, what string) tea.Msg {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case msg := <-ch:
			if match(msg) {
				return msg
			}
		case <-deadline:
			t.Fatalf("no %s reached the loop within 1s", what)
			return nil
		}
	}
}

// expectNoMsg fails if ch yields a message match accepts within a short
// window. Only ever used after a long wait for the first message, so a
// slow run cannot fail it spuriously; it can only miss a late second.
func expectNoMsg(t *testing.T, ch <-chan tea.Msg, match func(tea.Msg) bool, why string) {
	t.Helper()
	deadline := time.After(200 * time.Millisecond)
	for {
		select {
		case msg := <-ch:
			if match(msg) {
				t.Fatal(why)
			}
		case <-deadline:
			return
		}
	}
}

// expectOneDrainConsumer sends two signals on src and requires exactly
// one to reach the loop: one consumer takes the first and waits for
// Update before taking the second, two take one each.
func expectOneDrainConsumer(t *testing.T, k drainKind, src any, ch <-chan tea.Msg) {
	t.Helper()
	k.send(t, src)
	k.send(t, src)
	awaitMsg(t, ch, k.isMsg, k.name+" signal")
	expectNoMsg(t, ch, k.isMsg, "a second listener delivered: the channel has two consumers")
}

// A kept channel ends the switch with exactly one consumer, and the
// signals queued on it are all still read, once each and in order, by
// that consumer as Update re-arms it.
func TestSwitch_KeptDrainHasOneConsumerAndLosesNothing(t *testing.T) {
	for _, k := range drainKinds() {
		for _, keep := range k.keeps {
			t.Run(k.name+"/"+keep.name, func(t *testing.T) {
				src := k.newSrc()
				m := k.model(t, src)

				msgs := make(chan tea.Msg, 16)
				pump(k.listener(m), msgs)
				pump(m.applySwitchTarget(keep.target(src)), msgs)

				expectOneDrainConsumer(t, k, src, msgs)
			})

			t.Run(k.name+"/"+keep.name+"/queued signals are read", func(t *testing.T) {
				src := k.newSrc()
				m := k.model(t, src)

				msgs := make(chan tea.Msg, 16)
				pump(k.listener(m), msgs)
				k.send(t, src)
				k.send(t, src)
				k.send(t, src)
				pump(m.applySwitchTarget(keep.target(src)), msgs)

				before := k.rendered(m)
				for i := range 3 {
					out, cmd := m.Update(awaitMsg(t, msgs, k.isMsg, fmt.Sprintf("signal %d", i)))
					m = out.(*model)
					pump(cmd, msgs)
				}
				if got := k.rendered(m) - before; got != 3 {
					t.Errorf("%d of 3 queued signals rendered", got)
				}
				expectNoMsg(t, msgs, k.isMsg, "a fourth signal arrived: one was delivered twice")
			})
		}
	}
}

// Replacing the channel releases the listener parked on it.
func TestSwitch_ReplacedDrainListenerIsReleased(t *testing.T) {
	for _, k := range drainKinds() {
		t.Run(k.name, func(t *testing.T) {
			m := k.model(t, k.newSrc())

			cmd := k.listener(m)
			if cmd == nil {
				t.Fatal("setup: no listener armed")
			}
			done := make(chan tea.Msg, 1)
			go func() { done <- cmd() }()

			m.applySwitchTarget(k.replace(nil))

			select {
			case msg := <-done:
				if msg != nil {
					t.Errorf("released listener returned %T, want nil", msg)
				}
			case <-time.After(time.Second):
				t.Fatal("the listener on the replaced channel is still parked 1s after the switch")
			}
		})
	}
}

// A signal the outgoing listener took in the switch window reaches
// Update after the switch. It is dropped: nothing reaches the new
// session's transcript, and it re-arms nothing, so the new channel
// keeps its one consumer.
func TestSwitch_LateSignalFromReplacedDrainIsDropped(t *testing.T) {
	for _, k := range drainKinds() {
		t.Run(k.name, func(t *testing.T) {
			old := k.newSrc()
			m := k.model(t, old)

			cmd := k.listener(m)
			k.send(t, old)
			late := cmd()
			if !k.isMsg(late) {
				t.Fatalf("setup: listener returned %T, want the signal", late)
			}

			fresh := k.replace(nil)
			msgs := make(chan tea.Msg, 16)
			pump(m.applySwitchTarget(fresh), msgs)
			before := k.rendered(m)

			out, cmd := m.Update(late)
			m = out.(*model)
			if got := k.rendered(m) - before; got != 0 {
				t.Errorf("the late signal rendered %d row(s) in the new session", got)
			}
			if m.toast != "" {
				t.Errorf("the late signal raised a toast %q in the new session", m.toast)
			}
			if cmd != nil {
				t.Error("the late signal returned a Cmd; it must not re-arm a listener")
			}

			var src any
			if fresh.Notifier != nil {
				src = fresh.Notifier
			} else {
				src = fresh.Agent
			}
			expectOneDrainConsumer(t, k, src, msgs)
		})
	}
}

// A released listener can still deliver after the session has switched
// back to its channel (A → B → A). Its signal is dropped, and it does
// not disarm the listener step 8 parked on that channel.
func TestSwitch_ReleasedDrainListenerOnReturnedSourceIsStale(t *testing.T) {
	for _, k := range drainKinds() {
		t.Run(k.name, func(t *testing.T) {
			a := k.newSrc()
			m := k.model(t, a)

			cmd := k.listener(m)
			k.send(t, a)
			late := cmd()

			m.applySwitchTarget(k.replace(nil))
			msgs := make(chan tea.Msg, 16)
			pump(m.applySwitchTarget(k.target(a)), msgs)
			before := k.rendered(m)

			out, cmd := m.Update(late)
			m = out.(*model)
			if got := k.rendered(m) - before; got != 0 {
				t.Errorf("the released listener's signal rendered %d row(s)", got)
			}
			if cmd != nil {
				t.Error("the released listener's signal returned a Cmd; it must not re-arm")
			}
			pump(k.listener(m), msgs)
			expectOneDrainConsumer(t, k, a, msgs)
		})
	}
}

// probeMsg is pushed straight onto eventCh by the event tests. Update
// has no case for it, which is what they want: it exercises only the
// drain, not any handler.
type probeMsg struct{ n int }

func isProbe(msg tea.Msg) bool {
	if ev, ok := msg.(eventMsg); ok {
		msg = ev.msg
	}
	_, ok := msg.(probeMsg)
	return ok
}

func eventModel(t *testing.T, agent Agent) *model {
	t.Helper()
	m := newModel(Options{Agent: agent})
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = out.(*model)
	t.Cleanup(m.endListeners)
	return m
}

// expectOneEventConsumer pushes two probes onto eventCh and requires
// exactly one to reach the loop before Update has handled it.
func expectOneEventConsumer(t *testing.T, m *model, ch <-chan tea.Msg) {
	t.Helper()
	m.eventCh <- probeMsg{n: 1}
	m.eventCh <- probeMsg{n: 2}
	awaitMsg(t, ch, isProbe, "event")
	expectNoMsg(t, ch, isProbe, "a second listener delivered: eventCh has two consumers, so its messages can reach Update out of order")
}

// eventCh survives every switch, so the listener armed at Init is still
// the one consumer afterwards; step 8 must not add another.
func TestSwitch_EventChannelHasOneConsumer(t *testing.T) {
	m := eventModel(t, &bareAgent{id: "old"})

	msgs := make(chan tea.Msg, 16)
	pump(m.eventListener(), msgs)
	for range 3 {
		pump(m.applySwitchTarget(&SwitchTarget{Agent: &bareAgent{id: "new"}}), msgs)
	}

	expectOneEventConsumer(t, m, msgs)
}

// A LiveAgent host's liveStreamStartedMsg does not arrive on eventCh,
// but its handler asks for an event listener. Init has already parked
// one, so it must get nil rather than a second consumer for the whole
// observer session.
func TestLiveStreamStarted_DoesNotAddEventConsumer(t *testing.T) {
	m := eventModel(t, newLiveAgentStub())

	msgs := make(chan tea.Msg, 16)
	pump(m.eventListener(), msgs)
	_, cmd := m.Update(liveStreamStartedMsg{gen: m.sessionGen, cancel: func() {}})
	pump(cmd, msgs)

	expectOneEventConsumer(t, m, msgs)
}

// What one consumer buys: a stream of chunks driven through the loop
// the way the runtime drives it — every Cmd on its own goroutine —
// arrives in Update in the order it was sent, each chunk once, across
// switches that each re-ask for an event listener.
func TestEventChannel_ChunksArriveInOrderAcrossSwitches(t *testing.T) {
	m := eventModel(t, &bareAgent{id: "old"})

	msgs := make(chan tea.Msg, 64)
	pump(m.eventListener(), msgs)
	for range 3 {
		pump(m.applySwitchTarget(&SwitchTarget{Agent: &bareAgent{id: "new"}}), msgs)
	}
	m.state = stateStreaming
	m.spinnerActive = true

	const n = 200
	gen, ch := m.sessionGen, m.eventCh
	go func() {
		for i := range n {
			ch <- streamChunkMsg{gen: gen, text: fmt.Sprintf("%d,", i), partial: true}
		}
	}()

	var want strings.Builder
	for i := range n {
		fmt.Fprintf(&want, "%d,", i)
	}
	deadline := time.After(10 * time.Second)
	for len(m.inProgressText) < want.Len() {
		select {
		case msg := <-msgs:
			// Only the drain's own messages: the switches' other Cmds
			// (and the render ticks this returns) are not the subject.
			if _, ok := msg.(eventMsg); !ok {
				if _, ok := msg.(streamChunkMsg); !ok {
					continue
				}
			}
			out, cmd := m.Update(msg)
			m = out.(*model)
			pump(cmd, msgs)
		case <-deadline:
			t.Fatalf("stream stalled after %q", m.inProgressText)
		}
	}
	if m.inProgressText != want.String() {
		t.Fatalf("chunks reached Update out of order or duplicated:\n got %q\nwant %q", m.inProgressText, want.String())
	}
}

// An event handler that returns no listener cannot stop the drain:
// Update re-arms one for every message the listener delivered. An inbox
// state the protocol has not defined is such a handler.
func TestEventMsg_DrainSurvivesHandlerWithoutListener(t *testing.T) {
	m := eventModel(t, &bareAgent{id: "a"})
	// The listener this message came from.
	if m.eventListener() == nil {
		t.Fatal("setup: no event listener armed")
	}

	_, cmd := m.Update(eventMsg{msg: inboxStateMsg{gen: m.sessionGen, event: InboxEvent{State: "undefined"}}})
	msgs := make(chan tea.Msg, 16)
	pump(cmd, msgs)

	m.eventCh <- probeMsg{n: 1}
	awaitMsg(t, msgs, isProbe, "event after a handler that returned no listener")
}

// Likewise a pending pricing form, which takes every message before
// the switch in update sees it.
func TestEventMsg_DrainSurvivesPendingForm(t *testing.T) {
	m := eventModel(t, &bareAgent{id: "a"})
	if m.eventListener() == nil {
		t.Fatal("setup: no event listener armed")
	}
	m.pendingForm = newPricingForm("model-x", 60)

	_, cmd := m.Update(eventMsg{msg: probeMsg{n: 0}})
	msgs := make(chan tea.Msg, 16)
	pump(cmd, msgs)

	m.eventCh <- probeMsg{n: 1}
	awaitMsg(t, msgs, isProbe, "event while a form is pending")
}

// A straggler from the outgoing session that the event listener
// delivers after a switch is still dropped by its sessionGen stamp —
// that guard was already right, and the wrapper does not bypass it —
// and the drain goes on with one consumer.
func TestEventMsg_StragglerFromOutgoingSessionIsDropped(t *testing.T) {
	m := eventModel(t, &bareAgent{id: "old"})
	cmd := m.eventListener()
	m.eventCh <- streamChunkMsg{gen: m.sessionGen, text: "old session", partial: true}
	late := cmd()

	msgs := make(chan tea.Msg, 16)
	pump(m.applySwitchTarget(&SwitchTarget{Agent: &bareAgent{id: "new"}}), msgs)

	out, cmd := m.Update(late)
	m = out.(*model)
	if m.inProgressText != "" {
		t.Errorf("straggler leaked into the new session: %q", m.inProgressText)
	}
	pump(cmd, msgs)
	expectOneEventConsumer(t, m, msgs)
}
