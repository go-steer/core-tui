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

// Tests for issue #353: the prompter, elicitor and asker listeners
// follow a session switch. A listener on a replaced channel is
// released; a request it had already taken is answered on the source
// it came from (deny / cancel) rather than opened over the new
// session; and a kept channel ends the switch with exactly one
// consumer.
//
// The three request kinds share one defect and one fix, so each test
// runs over all three through listenerKind, which is the per-kind
// vocabulary — how to build a source, ask on it, and answer it.

package tui

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// listenerKind adapts one of the three request channels to the shared
// tests. Sources are passed as any because the three concrete types
// (*Prompter, *elicitor, *asker) have no common interface beyond what
// these closures provide.
type listenerKind struct {
	name string
	// newSrc builds a fresh source, as a host would for a SwitchTarget.
	newSrc func() any
	// wire sets src on opts — both Options at construction and the
	// replacement field on a SwitchTarget, which share field names.
	opts   func(src any) Options
	target func(src any) *SwitchTarget
	// listener is the model's listener constructor for this kind.
	listener func(m *model) tea.Cmd
	// ask runs the host's blocking call on its own goroutine and
	// reports the outcome as a label: "refused" for the deny / cancel
	// the TUI answers on its own, "answered" for the operator's yes.
	ask func(ctx context.Context, src any) <-chan string
	// isReq reports whether msg is this kind's request message.
	isReq func(msg tea.Msg) bool
	// open reports whether this kind's question is on the overlay.
	open func(m *model) bool
	// answer resolves the open question the way an operator saying
	// yes would.
	answer func(m *model) tea.Cmd
}

func listenerKinds() []listenerKind {
	agent := func() Agent { return &bareAgent{id: "new"} }
	return []listenerKind{
		{
			name:   "permission",
			newSrc: func() any { return NewPrompter() },
			opts: func(src any) Options {
				return Options{Agent: &bareAgent{id: "old"}, Prompter: src.(*Prompter)}
			},
			target: func(src any) *SwitchTarget {
				tgt := &SwitchTarget{Agent: agent()}
				if src != nil {
					tgt.Prompter = src.(*Prompter)
				}
				return tgt
			},
			listener: (*model).promptListener,
			ask: func(ctx context.Context, src any) <-chan string {
				out := make(chan string, 1)
				go func() {
					d, err := src.(*Prompter).AskApproval(ctx, PermissionRequest{ToolName: "bash"})
					switch {
					case err != nil:
						out <- "error"
					case d == DecisionDeny:
						out <- "refused"
					case d == DecisionAllowOnce:
						out <- "answered"
					default:
						out <- "unexpected"
					}
				}()
				return out
			},
			isReq: func(msg tea.Msg) bool { _, ok := msg.(permissionRequestMsg); return ok },
			open:  func(m *model) bool { return m.openPermission() != nil },
			answer: func(m *model) tea.Cmd {
				return m.overlayStack.resolve(permissionDialogID, decision{Value: DecisionAllowOnce}, m)
			},
		},
		{
			name:   "elicit",
			newSrc: func() any { return NewElicitor() },
			opts: func(src any) Options {
				return Options{Agent: &bareAgent{id: "old"}, Elicitor: src.(Elicitor)}
			},
			target: func(src any) *SwitchTarget {
				tgt := &SwitchTarget{Agent: agent()}
				if src != nil {
					tgt.Elicitor = src.(Elicitor)
				}
				return tgt
			},
			listener: (*model).elicitListener,
			ask: func(ctx context.Context, src any) <-chan string {
				out := make(chan string, 1)
				go func() {
					r, err := src.(Elicitor).Elicit(ctx, "srv", ElicitRequest{
						Mode:   ElicitFormMode,
						Title:  "credentials",
						Fields: []ElicitField{{Name: "token", Description: "API token"}},
					})
					switch {
					case err != nil:
						out <- "error"
					case r.Action == ElicitActionCancel:
						out <- "refused"
					case r.Action == ElicitActionSubmit:
						out <- "answered"
					default:
						out <- "unexpected"
					}
				}()
				return out
			},
			isReq: func(msg tea.Msg) bool { _, ok := msg.(elicitRequestMsg); return ok },
			open:  func(m *model) bool { return m.openElicit() != nil },
			answer: func(m *model) tea.Cmd {
				return m.overlayStack.resolve(elicitDialogID, fields{Values: map[string]any{"token": "t"}}, m)
			},
		},
		{
			name:   "ask",
			newSrc: func() any { return NewAsker() },
			opts: func(src any) Options {
				return Options{Agent: &bareAgent{id: "old"}, Asker: src.(Asker)}
			},
			target: func(src any) *SwitchTarget {
				tgt := &SwitchTarget{Agent: agent()}
				if src != nil {
					tgt.Asker = src.(Asker)
				}
				return tgt
			},
			listener: (*model).askListener,
			ask: func(ctx context.Context, src any) <-chan string {
				out := make(chan string, 1)
				go func() {
					r, err := src.(Asker).Ask(ctx, AskRequest{Kind: AskConfirm, Prompt: "Ship it?"})
					switch {
					case err != nil:
						out <- "error"
					case r.Action == AskCancelled:
						out <- "refused"
					case r.Action == AskAnswered:
						out <- "answered"
					default:
						out <- "unexpected"
					}
				}()
				return out
			},
			isReq: func(msg tea.Msg) bool { _, ok := msg.(askRequestMsg); return ok },
			open:  func(m *model) bool { return m.openAsk() != nil },
			answer: func(m *model) tea.Cmd {
				return m.overlayStack.resolve(askDialogID, chosen{ID: confirmYesID}, m)
			},
		},
	}
}

// switchModel builds a model on src, sized so a question can render,
// with every listener released when the test ends.
func switchModel(t *testing.T, k listenerKind, src any) *model {
	t.Helper()
	m := newModel(k.opts(src))
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = out.(*model)
	t.Cleanup(m.endListeners)
	return m
}

// pump runs cmd the way the Bubble Tea runtime would — every Cmd of a
// batch on its own goroutine — and forwards each message it produces
// to out. The goroutines that park (the event listener, the request
// listeners) are released by switchModel's cleanup.
func pump(cmd tea.Cmd, out chan<- tea.Msg) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				pump(c, out)
			}
			return
		}
		if msg != nil {
			out <- msg
		}
	}()
}

// awaitHostOutcome waits for the host call's outcome.
func awaitHostOutcome(t *testing.T, ch <-chan string, what string) string {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(time.Second):
		t.Fatalf("%s: host call still blocked after 1s", what)
		return ""
	}
}

// awaitReq returns the next request message of kind k from ch.
func awaitReq(t *testing.T, k listenerKind, ch <-chan tea.Msg) tea.Msg {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case msg := <-ch:
			if k.isReq(msg) {
				return msg
			}
		case <-deadline:
			t.Fatal("no request reached the loop within 1s")
			return nil
		}
	}
}

// expectOneConsumer starts two requests on src and requires exactly one
// of them to reach the loop: one consumer takes the first and leaves
// the second queued, two take one each. The first is awaited with the
// usual generous timeout so a slow run cannot fail it; only the
// absence of a second rides on the short window.
func expectOneConsumer(t *testing.T, k listenerKind, src any, ch <-chan tea.Msg) {
	t.Helper()
	k.ask(t.Context(), src)
	k.ask(t.Context(), src)
	awaitReq(t, k, ch)
	deadline := time.After(200 * time.Millisecond)
	for {
		select {
		case msg := <-ch:
			if k.isReq(msg) {
				t.Fatal("a second listener delivered a request: the channel has two consumers")
			}
		case <-deadline:
			return
		}
	}
}

// A request the outgoing listener took in the switch window reaches
// Update after the source was replaced. It is refused on the source
// that asked, opens nothing over the new session, and re-arms nothing.
func TestSwitch_LateRequestOnReplacedSourceIsRefusedThere(t *testing.T) {
	for _, k := range listenerKinds() {
		t.Run(k.name, func(t *testing.T) {
			old := k.newSrc()
			m := switchModel(t, k, old)

			oldCmd := k.listener(m)
			if oldCmd == nil {
				t.Fatal("setup: no listener armed on the outgoing source")
			}
			outcome := k.ask(t.Context(), old)
			// The listener takes the request before the switch lands —
			// the window issue #353 is about.
			late := oldCmd()
			if !k.isReq(late) {
				t.Fatalf("setup: listener returned %T, want the request", late)
			}

			m.applySwitchTarget(k.target(k.newSrc()))

			out, cmd := m.Update(late)
			m = out.(*model)
			if got := awaitHostOutcome(t, outcome, "outgoing source"); got != "refused" {
				t.Errorf("outgoing host got %q, want refused", got)
			}
			if k.open(m) {
				t.Error("the late request opened a question over the new session")
			}
			if cmd != nil {
				t.Error("the late request returned a Cmd; it must not re-arm a listener")
			}
		})
	}
}

// The late request must not touch the new source: a request pending
// there stays pending and on screen, and the operator's answer to it
// reaches the new host.
func TestSwitch_LateRequestIsNotRoutedToNewSource(t *testing.T) {
	for _, k := range listenerKinds() {
		t.Run(k.name, func(t *testing.T) {
			old := k.newSrc()
			m := switchModel(t, k, old)

			oldCmd := k.listener(m)
			oldOutcome := k.ask(t.Context(), old)
			late := oldCmd()

			fresh := k.newSrc()
			msgs := make(chan tea.Msg, 16)
			pump(m.applySwitchTarget(k.target(fresh)), msgs)

			newOutcome := k.ask(t.Context(), fresh)
			out, _ := m.Update(awaitReq(t, k, msgs))
			m = out.(*model)
			if !k.open(m) {
				t.Fatal("setup: the new source's request did not open")
			}

			out, _ = m.Update(late)
			m = out.(*model)
			if got := awaitHostOutcome(t, oldOutcome, "outgoing source"); got != "refused" {
				t.Errorf("outgoing host got %q, want refused", got)
			}
			select {
			case got := <-newOutcome:
				t.Fatalf("the late request answered the new host (%q)", got)
			case <-time.After(50 * time.Millisecond):
			}
			if !k.open(m) {
				t.Fatal("the new source's question is no longer open")
			}

			k.answer(m)
			if got := awaitHostOutcome(t, newOutcome, "new source"); got != "answered" {
				t.Errorf("new host got %q, want answered", got)
			}
		})
	}
}

// Replacing a source releases the listener parked on it.
func TestSwitch_ReplacedSourceListenerIsReleased(t *testing.T) {
	for _, k := range listenerKinds() {
		t.Run(k.name, func(t *testing.T) {
			old := k.newSrc()
			m := switchModel(t, k, old)

			oldCmd := k.listener(m)
			done := make(chan tea.Msg, 1)
			go func() { done <- oldCmd() }()

			m.applySwitchTarget(k.target(k.newSrc()))

			select {
			case msg := <-done:
				if msg != nil {
					t.Errorf("released listener returned %T, want nil", msg)
				}
			case <-time.After(time.Second):
				t.Fatal("the listener on the replaced source is still parked 1s after the switch")
			}
		})
	}
}

// A kept source ends the switch with exactly one consumer, whether its
// listener was parked at the switch or its request was on screen.
func TestSwitch_KeptSourceHasOneConsumer(t *testing.T) {
	for _, k := range listenerKinds() {
		t.Run(k.name+"/listener parked", func(t *testing.T) {
			src := k.newSrc()
			m := switchModel(t, k, src)

			msgs := make(chan tea.Msg, 16)
			pump(k.listener(m), msgs)
			pump(m.applySwitchTarget(k.target(nil)), msgs)

			expectOneConsumer(t, k, src, msgs)
		})

		t.Run(k.name+"/same instance handed back", func(t *testing.T) {
			// A non-nil field naming the instance already in use is a
			// keep, not a replace: its parked listener stays the one
			// consumer.
			src := k.newSrc()
			m := switchModel(t, k, src)

			msgs := make(chan tea.Msg, 16)
			pump(k.listener(m), msgs)
			pump(m.applySwitchTarget(k.target(src)), msgs)

			expectOneConsumer(t, k, src, msgs)
		})

		t.Run(k.name+"/request on screen", func(t *testing.T) {
			src := k.newSrc()
			m := switchModel(t, k, src)

			msgs := make(chan tea.Msg, 16)
			pump(k.listener(m), msgs)
			first := k.ask(t.Context(), src)
			out, _ := m.Update(awaitReq(t, k, msgs))
			m = out.(*model)
			if !k.open(m) {
				t.Fatal("setup: the request did not open")
			}

			pump(m.applySwitchTarget(k.target(nil)), msgs)
			if got := awaitHostOutcome(t, first, "superseded request"); got != "refused" {
				t.Errorf("superseded request got %q, want refused", got)
			}

			expectOneConsumer(t, k, src, msgs)
		})
	}
}

// Requests still queued on a replaced source when the switch lands —
// the buffered one and a sender parked behind it — are refused there:
// the switch released the only thing that would ever read them.
func TestSwitch_QueuedRequestsOnReplacedSourceAreRefused(t *testing.T) {
	for _, k := range listenerKinds() {
		t.Run(k.name, func(t *testing.T) {
			old := k.newSrc()
			m := switchModel(t, k, old)

			first := k.ask(t.Context(), old)
			second := k.ask(t.Context(), old)
			// Let both reach the channel: one in the buffer, one
			// parked in the send behind it.
			time.Sleep(50 * time.Millisecond)

			m.applySwitchTarget(k.target(k.newSrc()))

			for i, ch := range []<-chan string{first, second} {
				if got := awaitHostOutcome(t, ch, "queued request"); got != "refused" {
					t.Errorf("queued request %d got %q, want refused", i, got)
				}
			}
			if k.open(m) {
				t.Error("a queued request opened a question over the new session")
			}
		})
	}
}

// A released listener can still deliver after the session has switched
// back to its source (A → B → A). Its request is refused like any
// other from a released listener, and it does not disarm the listener
// step 8 parked on that same source, which would let the next re-arm
// add a second consumer.
func TestSwitch_ReleasedListenerOnReturnedSourceIsStale(t *testing.T) {
	for _, k := range listenerKinds() {
		t.Run(k.name, func(t *testing.T) {
			a := k.newSrc()
			m := switchModel(t, k, a)

			oldCmd := k.listener(m)
			outcome := k.ask(t.Context(), a)
			late := oldCmd()

			m.applySwitchTarget(k.target(k.newSrc()))
			msgs := make(chan tea.Msg, 16)
			pump(m.applySwitchTarget(k.target(a)), msgs)

			out, cmd := m.Update(late)
			m = out.(*model)
			if got := awaitHostOutcome(t, outcome, "released listener's request"); got != "refused" {
				t.Errorf("released listener's request got %q, want refused", got)
			}
			if k.open(m) {
				t.Error("the released listener's request opened a question")
			}
			if cmd != nil {
				t.Error("the released listener's request returned a Cmd; it must not re-arm")
			}
			// The listener step 8 parked on A must still count as the
			// consumer, so asking for another builds none.
			pump(k.listener(m), msgs)
			expectOneConsumer(t, k, a, msgs)
		})
	}
}
