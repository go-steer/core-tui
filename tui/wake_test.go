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
	"iter"
	"testing"
	"time"
)

// wakingAgent is a stub Agent + WakeRequester for wake tests. The
// wake channel is exposed so the test can fire signals into it.
type wakingAgent struct {
	wakeCh chan struct{}
}

func newWakingAgent() *wakingAgent {
	return &wakingAgent{wakeCh: make(chan struct{}, 4)}
}

func (a *wakingAgent) Run(_ context.Context, _ string) iter.Seq2[Event, error] {
	return func(_ func(Event, error) bool) {}
}
func (a *wakingAgent) WakeRequested() <-chan struct{} { return a.wakeCh }

// TestWakeListener_FiresWakeMsg pins R-WAKE-1: a value sent into the
// agent's wake channel surfaces as a wakeMsg through the Cmd the
// listener returns.
func TestWakeListener_FiresWakeMsg(t *testing.T) {
	agent := newWakingAgent()
	m := NewModel(Options{Agent: agent})

	cmd := m.wakeListener()
	if cmd == nil {
		t.Fatal("wakeListener returned nil for an agent that implements WakeRequester")
	}

	// Fire a wake; the Cmd should receive it and return wakeMsg.
	agent.wakeCh <- struct{}{}
	got := cmd()
	if _, ok := got.(wakeMsg); !ok {
		t.Errorf("got %T, want wakeMsg", got)
	}
}

// TestWakeListener_NilForAgentWithoutCapability pins that a host
// whose agent doesn't satisfy WakeRequester gets nil from the
// listener — Init still launches cleanly, no goroutine leak.
func TestWakeListener_NilForAgentWithoutCapability(t *testing.T) {
	m := NewModel(Options{Agent: stubAgent{}})
	if cmd := m.wakeListener(); cmd != nil {
		t.Errorf("wakeListener should be nil for an agent without WakeRequester")
	}
}

// TestWakeListener_NilChannelHandledGracefully pins that an agent
// that returns nil from WakeRequested() is treated as "no signals"
// — no panic, no leaked goroutine.
func TestWakeListener_NilChannelHandledGracefully(t *testing.T) {
	m := NewModel(Options{Agent: nilWakeAgent{}})
	if cmd := m.wakeListener(); cmd != nil {
		t.Errorf("wakeListener should be nil when WakeRequested() returns nil")
	}
}

// TestUpdate_WakeMsgRaisesToast pins that the Update handler for
// wakeMsg sets m.toast and stamps toastSetAt so the render path can
// surface the banner.
func TestUpdate_WakeMsgRaisesToast(t *testing.T) {
	agent := newWakingAgent()
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	before := time.Now()
	out, _ := m.Update(wakeMsg{})
	got := out.(Model)

	if got.toast == "" {
		t.Errorf("toast = empty, want non-empty after wakeMsg")
	}
	if got.toastSetAt.Before(before) {
		t.Errorf("toastSetAt = %v, want >= %v", got.toastSetAt, before)
	}
}

// TestRenderToast_RespectsTTL pins that a toast past its TTL renders
// as empty (the cull check in renderToast is the secondary defense
// behind the toastClearMsg timer).
func TestRenderToast_RespectsTTL(t *testing.T) {
	m := NewModel(Options{})
	m.viewport.SetWidth(80)
	m.toast = "stale"
	m.toastSetAt = time.Now().Add(-2 * toastTTL)
	if got := m.renderToast(80); got != "" {
		t.Errorf("renderToast for expired toast = %q, want empty", got)
	}
}

// nilWakeAgent satisfies WakeRequester but returns nil — exercises
// the defensive nil-channel branch in wakeListener.
type nilWakeAgent struct{}

func (nilWakeAgent) Run(_ context.Context, _ string) iter.Seq2[Event, error] {
	return func(_ func(Event, error) bool) {}
}
func (nilWakeAgent) WakeRequested() <-chan struct{} { return nil }
