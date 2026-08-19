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

// TestMidTurnSlashDisposition_Buckets pins the three-way split
// (R-HOLD-3). The interesting column is the third: everything
// unrecognised stays prompt text, because "/tmp is full, look at it"
// is prose and hijacking it would be worse than a slash that waits.
func TestMidTurnSlashDisposition_Buckets(t *testing.T) {
	cases := []struct {
		line string
		want midTurnDisposition
	}{
		// Turn control — useless if it has to wait for the turn it
		// exists to act on.
		{"/interrupt", midTurnDispatch},
		{"/int", midTurnDispatch},
		{"/pause", midTurnDispatch},
		{"/continue", midTurnDispatch},
		{"/cont", midTurnDispatch},
		{"/abandon", midTurnDispatch},
		// Read-only introspection.
		{"/help", midTurnDispatch},
		{"/?", midTurnDispatch},
		{"/stats", midTurnDispatch},
		{"/subagents watcher", midTurnDispatch},
		// Host-side, and mid-turn is when /btw is for.
		{"/btw what does exponential backoff mean", midTurnDispatch},
		{"/status", midTurnDispatch},
		// Rewrites the conversation the running turn is appending to.
		{"/compact", midTurnRefuse},
		{"/clear", midTurnRefuse},
		{"/subagent auditor", midTurnRefuse},
		{"/sub auditor", midTurnRefuse}, // via the shared alias fold
		{"/done", midTurnRefuse},
		// Everything else queues, including prose.
		{"/tmp is full, take a look", midTurnQueue},
		{"/foo bar", midTurnQueue},
		{"/model", midTurnQueue},
		{"/theme dracula", midTurnQueue},
		{"not a slash at all", midTurnQueue},
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			if got := midTurnSlashDisposition(tc.line); got != tc.want {
				t.Errorf("midTurnSlashDisposition(%q) = %d, want %d", tc.line, got, tc.want)
			}
		})
	}
}

// TestMidTurnSlashDisposition_SharesTheAliasFold is the reason
// canonicalSlashName was extracted rather than duplicated: a second
// copy would eventually disagree, and the failure mode is a slash
// silently queued as prompt text.
func TestMidTurnSlashDisposition_SharesTheAliasFold(t *testing.T) {
	pairs := [][2]string{
		{"/int", "/interrupt"},
		{"/sub x", "/subagent x"},
		{"/by-the-way hi", "/btw hi"},
	}
	for _, p := range pairs {
		if a, b := midTurnSlashDisposition(p[0]), midTurnSlashDisposition(p[1]); a != b {
			t.Errorf("%q and %q disagree: %d vs %d", p[0], p[1], a, b)
		}
	}
}

// TestEnterMidTurn_DispatchesSafeSlashes is the defect this fixes:
// /interrupt typed during a turn used to be queued as the literal
// string "/interrupt" and dispatch nothing.
func TestEnterMidTurn_DispatchesSafeSlashes(t *testing.T) {
	agent := &pausableAgent{}
	m := newModel(Options{Agent: agent})
	m.width, m.height = 100, 40
	m.state = stateStreaming
	cancelled := false
	m.cancelTurn = func() { cancelled = true }
	m.input.SetValue("/interrupt")

	next, cmd := pressKey(m, tea.Key{Code: tea.KeyEnter})
	if !cancelled {
		t.Fatal("/interrupt typed mid-turn did not reach the dispatcher")
	}
	if len(next.queue) != 0 {
		t.Errorf("/interrupt was queued as prompt text; queue has %d entries", len(next.queue))
	}
	// It also holds, so the scheduler doesn't just start the next turn.
	runCmd(t, cmd)
	if got := agent.pauses(); len(got) != 1 {
		t.Errorf("Pause calls = %q, want the hold to follow the cancel", got)
	}
}

// TestEnterMidTurn_RefusesStateRewrites. Compacting under a live turn
// is a race the operator can't see, and the host refuses most of these
// server-side anyway — this makes the client agree out loud instead of
// silently queueing the text for later.
func TestEnterMidTurn_RefusesStateRewrites(t *testing.T) {
	m := newModel(Options{Agent: &liveAgentStub{}})
	m.width, m.height = 100, 40
	m.state = stateStreaming
	m.input.SetValue("/compact")

	next, _ := pressKey(m, tea.Key{Code: tea.KeyEnter})
	if len(next.queue) != 0 {
		t.Errorf("/compact was queued rather than refused; queue has %d entries", len(next.queue))
	}
	got := lastText(next)
	if !strings.Contains(got, "not while a turn is running") {
		t.Errorf("refusal row = %q, want the explanation", got)
	}
	if !strings.Contains(got, "/interrupt") {
		t.Errorf("refusal row = %q, want it to name the way out", got)
	}
	if next.input.Value() != "" {
		t.Errorf("input not cleared after a refusal; got %q", next.input.Value())
	}
}

// TestEnterMidTurn_ProseStillQueues is the guard on the default
// bucket. This is the behaviour that existed before the allowlist and
// it must survive it.
func TestEnterMidTurn_ProseStillQueues(t *testing.T) {
	m := newModel(Options{Agent: &liveAgentStub{}})
	m.width, m.height = 100, 40
	m.state = stateStreaming
	m.input.SetValue("/tmp is full, take a look")

	next, _ := pressKey(m, tea.Key{Code: tea.KeyEnter})
	if len(next.queue) != 1 {
		t.Fatalf("prose beginning with a slash was not queued; queue has %d entries", len(next.queue))
	}
	if next.input.Value() != "" {
		t.Errorf("input not cleared after queueing; got %q", next.input.Value())
	}
}

// TestEnterAtIdle_SlashDispatchIsUnchanged. The allowlist only gates
// the mid-turn path; at idle every slash still dispatches, allowlisted
// or not.
func TestEnterAtIdle_SlashDispatchIsUnchanged(t *testing.T) {
	m := newModel(Options{Agent: &liveAgentStub{}})
	m.width, m.height = 100, 40
	m.input.SetValue("/compact")

	next, _ := pressKey(m, tea.Key{Code: tea.KeyEnter})
	if got := lastText(next); strings.Contains(got, "not while a turn is running") {
		t.Errorf("idle /compact was refused as if a turn were running: %q", got)
	}
}
