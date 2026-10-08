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
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/go-steer/core-tui/tui"
)

// The SubagentReporter demo: a scripted roster that drives the
// running-tasks bar (R-SUB-4), the /subagents roster (R-SUB-1) and
// the /subagents <name> drill-down (R-SUB-2).
//
// Each demo subagent plays a script of steps on its own goroutine:
// every step waits, then moves the subagent's status and LastReport
// and appends a turn to its log. The TUI never sees any of that
// happen — it polls Subagents() once a second from its host-snapshot
// goroutine, which is exactly how a real host's roster reaches it, so
// the bar here behaves the way it will against core-agent.
//
// The roster is package-level for the same reason demoHooks is:
// demoAgent is rebuilt on every /model and /switch, and the subagents
// it spawned should not vanish with it.

// subagentStep is one beat of a demo subagent's script.
type subagentStep struct {
	after  time.Duration // wait before applying the step
	status string        // new status; "" keeps the current one
	report string        // new LastReport; "" keeps the current one
	turn   tui.SubagentEvent
}

// demoSubagent is one roster entry and its turn log.
type demoSubagent struct {
	info tui.SubagentInfo
	log  []tui.SubagentEvent
}

// demoRosterT is the roster. All access is under mu: Subagents() and
// SubagentEvents() are called from the TUI's background goroutines
// while the scripts write from theirs.
type demoRosterT struct {
	mu   sync.Mutex
	subs map[string]*demoSubagent
	seq  int64
	next int // which spawnScripts entry /spawn plays next
}

var demoRoster = &demoRosterT{subs: map[string]*demoSubagent{}}

var _ tui.SubagentReporter = demoAgent{}

// spawn starts name playing script. A name already on the roster is
// restarted, which is also what a host does when a subagent is
// re-run under the same name.
func (r *demoRosterT) spawn(name string, script []subagentStep) {
	r.mu.Lock()
	r.subs[name] = &demoSubagent{info: tui.SubagentInfo{
		Name: name, Status: "running", StartedAt: time.Now(), LastReport: "starting",
	}}
	r.mu.Unlock()
	go func() {
		for _, step := range script {
			time.Sleep(step.after)
			r.apply(name, step)
		}
	}()
}

func (r *demoRosterT) apply(name string, step subagentStep) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.subs[name]
	if !ok {
		return
	}
	if step.status != "" {
		s.info.Status = step.status
	}
	if step.report != "" {
		s.info.LastReport = step.report
	}
	if step.turn.Author != "" || step.turn.Text != "" || len(step.turn.ToolCalls) > 0 {
		r.seq++
		t := step.turn
		t.Seq = r.seq
		t.Timestamp = time.Now()
		s.log = append(s.log, t)
	}
}

// Subagents implements the roster half of tui.SubagentReporter.
func (demoAgent) Subagents() []tui.SubagentInfo {
	r := demoRoster
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]tui.SubagentInfo, 0, len(r.subs))
	for _, s := range r.subs {
		out = append(out, s.info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

// SubagentEvents implements the drill-down half: the turns after the
// since cursor, and an unknown name as *SubagentNotFoundError so the
// overlay can list the names that would have resolved.
func (demoAgent) SubagentEvents(_ context.Context, name string, since int64) (tui.SubagentEventPage, error) {
	r := demoRoster
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.subs[name]
	if !ok {
		nf := &tui.SubagentNotFoundError{Name: name}
		for n := range r.subs {
			nf.Available = append(nf.Available, n)
		}
		sort.Strings(nf.Available)
		return tui.SubagentEventPage{}, nf
	}
	page := tui.SubagentEventPage{NextSince: since}
	for _, ev := range s.log {
		if ev.Seq > since {
			page.Events = append(page.Events, ev)
			page.NextSince = ev.Seq
		}
	}
	return page, nil
}

// say is a turn that only speaks.
func say(text string) tui.SubagentEvent {
	return tui.SubagentEvent{Author: "model", Text: text}
}

// call is a turn that calls one tool and gets its result back.
func call(id, tool string, args, resp map[string]any) tui.SubagentEvent {
	return tui.SubagentEvent{
		Author:      "model",
		ToolCalls:   []tui.SubagentToolCall{{ID: id, Name: tool, Args: args}},
		ToolResults: []tui.SubagentToolResult{{ID: id, Name: tool, Response: resp}},
	}
}

// reviewerScript is the long one: it outlives the others so the bar
// always has a row counting up, and its reports are long enough to be
// cut at the column edge.
func reviewerScript() []subagentStep {
	return []subagentStep{
		{after: 2 * time.Second, report: "Reading the diff against main to scope the review",
			turn: call("r1", "Bash", map[string]any{"command": "git diff main --stat"},
				map[string]any{"stdout": " tui/tasks_bar.go | 240 +++\n 1 file changed", "exit_code": 0})},
		{after: 6 * time.Second, report: "Load and pin actions were examined, focusing on resource cleanup after potential failures",
			turn: say("The budget path looks sound; checking what happens when the roster empties mid-frame.")},
		{after: 10 * time.Second, report: "Checking the frame-height invariant under a squeezed terminal",
			turn: call("r2", "Bash", map[string]any{"command": "go test ./tui -run FrameInvariants"},
				map[string]any{"stdout": "ok  \tgithub.com/go-steer/core-tui/tui\t2.3s", "exit_code": 0})},
		{after: 14 * time.Second, report: "Two nits, no blockers — writing them up",
			turn: say("Nit 1: the linger window should be a named constant. Nit 2: the test sweep could note why it runs at 200 columns.")},
		{after: 8 * time.Second, status: "done", report: "Review complete: 0 blockers, 2 nits",
			turn: say("Review complete. No blockers; two nits filed inline.")},
	}
}

// linterScript finishes fast, so the five-second linger on a done row
// is visible a few seconds into the session.
func linterScript() []subagentStep {
	return []subagentStep{
		{after: 3 * time.Second, report: "Running golangci-lint on ./...",
			turn: call("l1", "Bash", map[string]any{"command": "golangci-lint run ./..."},
				map[string]any{"stdout": "", "exit_code": 0})},
		{after: 5 * time.Second, status: "done", report: "0 findings",
			turn: say("Lint clean.")},
	}
}

// indexerScript pauses partway, then fails, so the bar shows the
// paused glyph and the failed row in one run.
func indexerScript() []subagentStep {
	return []subagentStep{
		{after: 3 * time.Second, report: "Walking the module graph",
			turn: call("i1", "Read", map[string]any{"path": "go.mod"},
				map[string]any{"content": "module github.com/go-steer/core-tui\n"})},
		{after: 6 * time.Second, status: "paused", report: "Waiting on a rate-limited symbol server",
			turn: say("Symbol server returned 429; backing off.")},
		{after: 8 * time.Second, status: "running", report: "Resumed; indexing tui/",
			turn: say("Backoff elapsed, resuming.")},
		{after: 7 * time.Second, status: "failed", report: "symbol server: 503 Service Unavailable",
			turn: tui.SubagentEvent{Author: "model", Text: "Giving up after the second outage.",
				ToolResults: []tui.SubagentToolResult{{ID: "i2", Name: "fetch_symbols", Error: "503 Service Unavailable"}}}},
	}
}

// spawnScripts is what /spawn plays, in rotation.
var spawnScripts = []struct {
	name   string
	script func() []subagentStep
}{
	{"reviewer", reviewerScript},
	{"linter", linterScript},
	{"indexer", indexerScript},
}

// demoSubagentsAfter starts the launch roster after delay: all three
// scripts, staggered so the rows arrive one at a time.
func demoSubagentsAfter(delay time.Duration) {
	time.Sleep(delay)
	for i, s := range spawnScripts {
		if i > 0 {
			time.Sleep(time.Second)
		}
		demoRoster.spawn(s.name, s.script())
	}
}

// spawnNext is /spawn: the next script in rotation, under name if one
// was given, otherwise under the script's own name plus a counter so
// repeated spawns pile up and push the bar past its row cap.
func spawnNext(name string) string {
	r := demoRoster
	r.mu.Lock()
	s := spawnScripts[r.next%len(spawnScripts)]
	r.next++
	n := r.next
	r.mu.Unlock()
	if name == "" {
		name = fmt.Sprintf("%s-%d", s.name, n)
	}
	r.spawn(name, s.script())
	return name
}
