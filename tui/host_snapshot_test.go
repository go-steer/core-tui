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

// hostileHost implements Agent + StatusReporter + UsageTracker, but every
// capability method panics if called. It stands in for a slow/wedged host:
// the whole point of the render-path cache (host_snapshot.go) is that
// View() reads a snapshot and never invokes these methods. If a render
// helper reaches through to the host, the panic fails the test — exactly
// the freeze vector we're guarding against, made loud instead of hung.
type hostileHost struct{ t *testing.T }

func (*hostileHost) Run(context.Context, string) iter.Seq2[Event, error] {
	return func(func(Event, error) bool) {}
}

func (h *hostileHost) Status() Status {
	h.t.Helper()
	h.t.Fatal("Status() called from the render path — View() must read from the host snapshot cache, not the host")
	return Status{}
}

func (h *hostileHost) failUsage(method string) {
	h.t.Helper()
	h.t.Fatalf("%s called from the render path — usage summaries must read from the host snapshot cache", method)
}

func (h *hostileHost) SessionTotals() Usage    { h.failUsage("SessionTotals"); return Usage{} }
func (h *hostileHost) SessionCostUSD() float64 { h.failUsage("SessionCostUSD"); return 0 }
func (h *hostileHost) LastTurn() (Usage, float64) {
	h.failUsage("LastTurn")
	return Usage{}, 0
}
func (h *hostileHost) ContextWindowSize() int         { h.failUsage("ContextWindowSize"); return 0 }
func (h *hostileHost) ContextWindowUsed() int         { h.failUsage("ContextWindowUsed"); return 0 }
func (h *hostileHost) SessionTurns() int              { h.failUsage("SessionTurns"); return 0 }
func (h *hostileHost) SessionDuration() time.Duration { h.failUsage("SessionDuration"); return 0 }

// TestRenderHelpers_NeverCallHost is the regression guard for the View()
// hang: the four status-header helpers must read only cached state, never
// the (possibly wedged) host. With a host whose every method fails the
// test, each helper must still return a value.
func TestRenderHelpers_NeverCallHost(t *testing.T) {
	h := &hostileHost{t: t}
	m := NewModel(Options{ForceTheme: ThemeDark, Agent: h, UsageTracker: h})

	// Cold cache: no refresh has landed, so the helpers fall back to
	// placeholders/empty — and crucially do NOT reach the host.
	if got := m.displayModelName(); got != "(model not set)" {
		t.Errorf("cold displayModelName = %q, want placeholder", got)
	}
	if got := m.displayProvider(); got != "" {
		t.Errorf("cold displayProvider = %q, want empty", got)
	}
	if got := m.usageSummaryOneLine(); got != "" {
		t.Errorf("cold usageSummaryOneLine = %q, want empty (no snapshot yet)", got)
	}
	if l1, l2 := m.usageSummaryStacked(); l1 != "" || l2 != "" {
		t.Errorf("cold usageSummaryStacked = (%q, %q), want empty pair", l1, l2)
	}

	// Warm cache: an off-loop refresh has landed a snapshot. The helpers
	// surface it — still without touching the host.
	m.hostSnap = hostSnapshot{
		valid:     true,
		modelName: "claude-opus-4-8",
		provider:  "anthropic",
		hasUsage:  true,
		totals:    Usage{InputTokens: 12000, OutputTokens: 3400},
		cost:      1.2345,
		winUsed:   50000,
		winSize:   200000,
	}
	if got := m.displayModelName(); got != "claude-opus-4-8" {
		t.Errorf("warm displayModelName = %q, want claude-opus-4-8", got)
	}
	if got := m.displayProvider(); got != "anthropic" {
		t.Errorf("warm displayProvider = %q, want anthropic", got)
	}
	if got := m.usageSummaryOneLine(); got == "" {
		t.Error("warm usageSummaryOneLine returned empty, want cached usage block")
	}
	if l1, l2 := m.usageSummaryStacked(); l1 == "" || l2 == "" {
		t.Errorf("warm usageSummaryStacked = (%q, %q), want both populated", l1, l2)
	}
}

// TestHostSnapshotMsg_AdoptsAndReArms verifies the Update wiring: an
// in-generation snapshot is adopted and a re-arm tick is scheduled, while
// a stale-generation snapshot is dropped.
func TestHostSnapshotMsg_AdoptsAndReArms(t *testing.T) {
	m := NewModel(Options{ForceTheme: ThemeDark, Agent: &noopAgent{}})

	snap := hostSnapshot{valid: true, modelName: "gemini-3.5-flash", provider: "gemini"}
	got, cmd := m.Update(hostSnapshotMsg{gen: m.sessionGen, snap: snap})
	m2 := got.(Model)
	if m2.hostSnap.modelName != "gemini-3.5-flash" {
		t.Errorf("adopted snapshot modelName = %q, want gemini-3.5-flash", m2.hostSnap.modelName)
	}
	if cmd == nil {
		t.Error("in-gen hostSnapshotMsg should return a re-arm tick Cmd, got nil")
	}

	// Stale generation: snapshot is ignored, no re-arm.
	got2, cmd2 := m2.Update(hostSnapshotMsg{gen: m2.sessionGen + 1, snap: hostSnapshot{modelName: "stale"}})
	m3 := got2.(Model)
	if m3.hostSnap.modelName != "gemini-3.5-flash" {
		t.Errorf("stale snapshot mutated cache: modelName = %q, want gemini-3.5-flash", m3.hostSnap.modelName)
	}
	if cmd2 != nil {
		t.Error("stale hostSnapshotMsg should not re-arm the tick")
	}
}

// TestPullHostSnapshot_NilSafe confirms the off-loop pull tolerates a host
// that implements neither capability and reports hasUsage correctly.
func TestPullHostSnapshot_NilSafe(t *testing.T) {
	// Neither capability wired.
	snap := pullHostSnapshot(nil, nil, nil)
	if !snap.valid {
		t.Error("pullHostSnapshot(nil, nil).valid = false, want true")
	}
	if snap.hasUsage {
		t.Error("pullHostSnapshot with nil tracker set hasUsage = true")
	}
	if snap.modelName != "" || snap.provider != "" {
		t.Errorf("pullHostSnapshot with nil reporter surfaced model/provider: %+v", snap)
	}
}
