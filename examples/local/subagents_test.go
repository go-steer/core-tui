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
	"strings"
	"testing"
	"time"

	"github.com/go-steer/core-tui/tui"
)

// resetRoster swaps in an empty roster for one test.
func resetRoster(t *testing.T) {
	t.Helper()
	old := demoRoster
	demoRoster = &demoRosterT{subs: map[string]*demoSubagent{}}
	t.Cleanup(func() { demoRoster = old })
}

// waitFor polls cond until it holds or a second passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func statusOf(name string) string {
	for _, s := range (demoAgent{}).Subagents() {
		if s.Name == name {
			return s.Status
		}
	}
	return ""
}

func TestDemoRoster_ScriptDrivesStatusAndLog(t *testing.T) {
	resetRoster(t)
	demoRoster.spawn("probe", []subagentStep{
		{after: time.Millisecond, report: "looking", turn: say("first")},
		{after: time.Millisecond, status: "paused", turn: say("second")},
		{after: time.Millisecond, status: "done", report: "finished", turn: say("third")},
	})
	if got := statusOf("probe"); got != "running" {
		t.Fatalf("status right after spawn = %q, want running", got)
	}
	waitFor(t, "probe to finish", func() bool { return statusOf("probe") == "done" })

	subs := (demoAgent{}).Subagents()
	if len(subs) != 1 || subs[0].LastReport != "finished" || subs[0].StartedAt.IsZero() {
		t.Errorf("roster = %+v, want one finished entry with a start time", subs)
	}

	page, err := (demoAgent{}).SubagentEvents(context.Background(), "probe", 0)
	if err != nil || len(page.Events) != 3 {
		t.Fatalf("log = %+v, %v; want three turns", page, err)
	}
	// The cursor resumes after what was read, which is what keeps the
	// TUI's once-a-second tail from re-reading the whole log.
	rest, err := (demoAgent{}).SubagentEvents(context.Background(), "probe", page.Events[1].Seq)
	if err != nil || len(rest.Events) != 1 || rest.Events[0].Text != "third" {
		t.Errorf("page after the second turn = %+v, %v; want only the third", rest, err)
	}
}

func TestDemoRoster_UnknownNameIsNotFound(t *testing.T) {
	resetRoster(t)
	demoRoster.spawn("probe", nil)
	_, err := (demoAgent{}).SubagentEvents(context.Background(), "prob", 0)
	var nf *tui.SubagentNotFoundError
	if !errors.As(err, &nf) || len(nf.Available) != 1 || nf.Available[0] != "probe" {
		t.Errorf("err = %v, want SubagentNotFoundError naming probe", err)
	}
}

func TestDemoRoster_SpawnSlash(t *testing.T) {
	resetRoster(t)
	res, err := (demoAgent{}).InvokeSlash(context.Background(), "spawn", "")
	if err != nil || !strings.Contains(res.SystemMessage, "reviewer-1") {
		t.Fatalf("/spawn = %+v, %v; want reviewer-1", res, err)
	}
	res, _ = (demoAgent{}).InvokeSlash(context.Background(), "spawn", " mine ")
	if !strings.Contains(res.SystemMessage, "mine") {
		t.Errorf("/spawn mine = %q, want the given name", res.SystemMessage)
	}
	if got := len((demoAgent{}).Subagents()); got != 2 {
		t.Errorf("roster has %d entries after two spawns, want 2", got)
	}
}

// A sleep step sets the wake and its reason; the next step clears
// both, as a host does when the awaited turn starts. Status stays
// running throughout.
func TestDemoRoster_SleepSetsAndClearsWake(t *testing.T) {
	resetRoster(t)
	demoRoster.spawn("watch", nil)
	before := time.Now()
	demoRoster.apply("watch", subagentStep{sleep: time.Minute, wakeDetail: "polling"})
	s := (demoAgent{}).Subagents()[0]
	if s.Status != "running" || s.WakeDetail != "polling" ||
		s.NextWakeAt.Before(before.Add(time.Minute)) || s.NextWakeAt.After(time.Now().Add(time.Minute)) {
		t.Fatalf("after a sleep step: %+v, want running with a wake a minute out", s)
	}
	demoRoster.apply("watch", subagentStep{report: "polled"})
	s = (demoAgent{}).Subagents()[0]
	if !s.NextWakeAt.IsZero() || s.WakeDetail != "" {
		t.Errorf("after the next step: %+v, want the wake cleared", s)
	}
}

// The launch scripts are what an operator sees first, so they have to
// cover every state the bar draws.
func TestDemoRoster_ScriptsCoverEveryState(t *testing.T) {
	seen := map[string]bool{"running": true}
	for _, s := range spawnScripts {
		for _, step := range s.script() {
			if step.status != "" {
				seen[step.status] = true
			}
			if step.sleep > 0 {
				seen["scheduled"] = true
			}
		}
	}
	for _, want := range []string{"running", "paused", "done", "failed", "scheduled"} {
		if !seen[want] {
			t.Errorf("no launch script reaches %q", want)
		}
	}
}
