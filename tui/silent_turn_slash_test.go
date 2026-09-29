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
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Issue #308. The typed-slash routing gated R-HOLD-3 on turnInFlight,
// the render gate, which on the live path is spinnerActive. A daemon
// turn sitting in a tool call arms neither a partial chunk nor an
// inject, so through every silent stretch the mid-turn policy was
// never consulted and the refused set ran as if nothing were in
// flight. The fix gates it on turnRunning, which also counts a
// non-idle turn_state pushed by the host.
//
// liveHoldAgent comes from hold_interrupt_test.go; bothRoutes and
// lastText from palette_submit_test.go and host_async_test.go.

// silentDaemonTurn returns a live model whose host has reported a
// running turn that has painted nothing: turn_state streaming, no
// spinner. It fails the test if that is not the state it built.
func silentDaemonTurn(t *testing.T) model {
	t.Helper()
	m := newModel(Options{Agent: &liveHoldAgent{}})
	m.width, m.height = 100, 40
	out, _ := m.Update(statusUpdateMsg{status: StatusUpdate{TurnState: TurnStateStreaming}})
	m = out.(model)
	if !m.liveMode {
		t.Fatal("setup: the fixture must be a LiveAgent host")
	}
	if m.turnInFlight() {
		t.Fatal("setup: the render gate must read idle — a silent turn is the condition under test")
	}
	if !m.turnRunning() {
		t.Fatal("setup: turnRunning must see the host's streaming turn_state")
	}
	return m
}

// TestSilentDaemonTurn_RefusedSlashesAreRefused is the reported defect.
// /clear during a quiet stretch armed its confirmation instead of
// being refused, and the next bare Enter would have wiped the
// transcript the running turn was still writing into. /transcripts is
// refused for the same race (#268).
func TestSilentDaemonTurn_RefusedSlashesAreRefused(t *testing.T) {
	for _, line := range []string{"/clear", "/transcripts", "/resume"} {
		for _, route := range bothRoutes {
			t.Run(line+"/"+route.name, func(t *testing.T) {
				m := route.load(silentDaemonTurn(t), line)
				before := m.history.Len()

				next, _ := pressKey(m, tea.Key{Code: tea.KeyEnter})
				if next.confirmingClear {
					t.Fatal("/clear armed its confirmation during a running daemon turn instead of being refused")
				}
				if got := lastText(next); !strings.Contains(got, "not while a turn is running") {
					t.Errorf("row = %q, want the mid-turn refusal", got)
				}
				if got := next.history.Len(); got != before+1 {
					t.Errorf("history has %d rows, want exactly the refusal added to %d", got, before)
				}
			})
		}
	}
}

// TestSilentDaemonTurn_SafeSlashesDispatch pins the dispatch bucket: an
// allowlisted command still runs now. /stats answers from the model's
// own cache, so its row is proof the dispatcher ran rather than the
// line being refused or queued.
func TestSilentDaemonTurn_SafeSlashesDispatch(t *testing.T) {
	for _, route := range bothRoutes {
		t.Run(route.name, func(t *testing.T) {
			m := route.load(silentDaemonTurn(t), "/stats")

			next, _ := pressKey(m, tea.Key{Code: tea.KeyEnter})
			got := lastText(next)
			if strings.Contains(got, "not while a turn is running") {
				t.Fatalf("row = %q, want /stats dispatched, not refused", got)
			}
			if !strings.Contains(got, "/stats") {
				t.Errorf("row = %q, want /stats to have answered", got)
			}
		})
	}
}

// TestSilentDaemonTurn_UnlistedSlashRoutesAsWhenPainting pins the queue
// bucket. On the live path that bucket falls through to the arms below,
// which key off m.state and m.pause rather than the gate (#308's "the
// queue side is unaffected"), so a quiet stretch must route an
// unlisted line exactly as a painting one does. The painting run is
// the oracle rather than a hard-coded answer: this fix is about which
// turns count as running, not about what the fallthrough does.
func TestSilentDaemonTurn_UnlistedSlashRoutesAsWhenPainting(t *testing.T) {
	const line = "/foo bar"
	route := func(m model) (model, []Message) {
		m.input.SetValue(line)
		next, _ := pressKey(m, tea.Key{Code: tea.KeyEnter})
		return next, next.history.Snapshot()[m.history.Len():]
	}

	silent, silentRows := route(silentDaemonTurn(t))
	painting := silentDaemonTurn(t)
	painting.beginLiveStretch()
	if !painting.turnInFlight() {
		t.Fatal("setup: beginLiveStretch did not arm the render gate")
	}
	lit, litRows := route(painting)

	for _, r := range silentRows {
		if strings.Contains(r.Text, "not while a turn is running") {
			t.Fatalf("row = %q, want an unlisted line left alone, not refused", r.Text)
		}
	}
	if len(silentRows) != len(litRows) || len(silent.queue) != len(lit.queue) {
		t.Errorf("silent turn: %d rows, %d queued; painting turn: %d rows, %d queued — want the same routing",
			len(silentRows), len(silent.queue), len(litRows), len(lit.queue))
	}
}

// TestIdleDaemon_ClearStillConfirms guards against fixing this by
// refusing too much: once the host reports idle, /clear is an ordinary
// command again and arms its confirmation.
func TestIdleDaemon_ClearStillConfirms(t *testing.T) {
	m := silentDaemonTurn(t)
	out, _ := m.Update(statusUpdateMsg{status: StatusUpdate{TurnState: TurnStateIdle}})
	m = out.(model)
	if m.turnRunning() {
		t.Fatal("setup: turnRunning must clear once the host reports idle")
	}
	m.input.SetValue("/clear")

	next, _ := pressKey(m, tea.Key{Code: tea.KeyEnter})
	if !next.confirmingClear {
		t.Errorf("/clear at idle did not arm its confirmation; last row = %q", lastText(next))
	}
}
