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
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Issue #311. #299 made the held arm ask "is this a command?", but
// answered it from static tables, so a name only the HOST registers —
// core-agent's /usage, /title, /new, /attach — opened the gate and
// reached the model as the steer instruction. The fix is a
// model-level cache of the host catalog, filled off the event loop.
//
// Helpers (bothRoutes, typeLine, pausableAgent, runCmd, pressKey)
// come from palette_submit_test.go, pause_test.go and
// host_async_test.go.

// pausableSlashAgent is a held-capable agent that also advertises a
// host slash catalog. None of its names appear in builtinSlashNames,
// midTurnSafeSlashes or midTurnRefusedSlashes — the case no #300 test
// could cover, because they all used names core-tui already knows.
type pausableSlashAgent struct {
	pausableAgent

	mu        sync.Mutex
	specs     []SlashCommandSpec
	listCalls int
	invoked   []string
}

func (a *pausableSlashAgent) SlashCommands() []SlashCommandSpec {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.listCalls++
	return a.specs
}

func (a *pausableSlashAgent) InvokeSlash(_ context.Context, name, _ string) (SlashResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.invoked = append(a.invoked, name)
	return SlashResult{SystemMessage: "/" + name + ": ok"}, nil
}

func (a *pausableSlashAgent) calls() (list int, invoked []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.listCalls, append([]string(nil), a.invoked...)
}

func newPausableSlashAgent() *pausableSlashAgent {
	a := &pausableSlashAgent{specs: []SlashCommandSpec{
		{Name: "usage", Description: "token usage"},
		{Name: "attach", Description: "attach to a peer"},
		{Name: "new", Aliases: []string{"blank"}, Description: "start a new session"},
	}}
	a.setState(PauseInfo{Paused: true, Since: time.Unix(0, 0)})
	return a
}

// heldModel is a parked model over agent, with the host-name cache
// left cold.
func heldModel(t *testing.T, agent Agent) *model {
	t.Helper()
	m := newModel(Options{Agent: agent})
	m.width, m.height = 100, 40
	m.pause.PauseInfo = PauseInfo{Paused: true}
	if !m.pause.paused() {
		t.Fatal("setup: model is not held")
	}
	return m
}

// warmHostSlashNames runs the priming fetch Init issues and lands its
// reply, the way the program loop would.
func warmHostSlashNames(t *testing.T, m *model) *model {
	t.Helper()
	c := m.hostSlashNamesCmd()
	if c == nil {
		t.Fatal("hostSlashNamesCmd returned nil for an agent with a slash catalog")
	}
	out, _ := m.Update(c())
	return out.(*model)
}

// TestEnterWhileHeld_HostSlashDispatches is the reported case: with the
// catalog cached, a host-only name typed while parked is dispatched to
// the host and the agent stays parked. /new is reached through its
// alias to pin that aliases are folded in alongside names, as the static
// recognisers'.
func TestEnterWhileHeld_HostSlashDispatches(t *testing.T) {
	for _, line := range []string{"/usage", "/attach peer-7", "/blank"} {
		for _, route := range bothRoutes {
			t.Run(line+"/"+route.name, func(t *testing.T) {
				agent := newPausableSlashAgent()
				m := warmHostSlashNames(t, heldModel(t, agent))
				m = route.load(m, line)

				next, cmd := pressKey(m, tea.Key{Code: tea.KeyEnter})
				if cmd == nil {
					t.Fatal("Enter produced no Cmd; want the host dispatch")
				}
				msg := cmd()
				if got := agent.resumes(); len(got) != 0 {
					t.Fatalf("Resume calls = %+v, want %s dispatched rather than steered", got, line)
				}
				if _, ok := msg.(slashDispatchedMsg); !ok {
					t.Fatalf("Enter's Cmd produced %T, want slashDispatchedMsg", msg)
				}
				out, _ := next.Update(msg)
				next = out.(*model)
				if _, invoked := agent.calls(); len(invoked) != 1 {
					t.Errorf("InvokeSlash calls = %q, want exactly one", invoked)
				}
				if !next.pause.paused() {
					t.Error("the agent left the held state; want it still parked")
				}
			})
		}
	}
}

// TestEnterWhileHeld_ColdHostCacheStillSteers pins the fallback: before
// the catalog has landed, the held arm answers exactly as it did
// before the cache existed, and it does NOT go and ask — the
// keystroke must never wait on SlashCommands(), which for a remote
// host is an HTTP call (#69).
func TestEnterWhileHeld_ColdHostCacheStillSteers(t *testing.T) {
	agent := newPausableSlashAgent()
	m := heldModel(t, agent)
	m.input.SetValue("/usage")

	_, cmd := pressKey(m, tea.Key{Code: tea.KeyEnter})
	if list, _ := agent.calls(); list != 0 {
		t.Fatalf("SlashCommands() called %d time(s) on the keystroke; want none", list)
	}
	runCmd(t, cmd)
	got := agent.resumes()
	if len(got) != 1 || got[0].Mode != ResumeModeSteer || got[0].Steer != "/usage" {
		t.Errorf("Resume calls = %+v, want the cold-cache line steered verbatim", got)
	}
}

// TestEnterWhileHeld_UnlistedSlashStillSteersWithWarmCache: a warm
// cache widens recognition by exactly the host's names. Prose that
// starts with a slash still reaches the agent.
func TestEnterWhileHeld_UnlistedSlashStillSteersWithWarmCache(t *testing.T) {
	agent := newPausableSlashAgent()
	m := warmHostSlashNames(t, heldModel(t, agent))
	m.input.SetValue("/opt/data is full")

	_, cmd := pressKey(m, tea.Key{Code: tea.KeyEnter})
	runCmd(t, cmd)
	if got := agent.resumes(); len(got) != 1 || got[0].Steer != "/opt/data is full" {
		t.Errorf("Resume calls = %+v, want the line steered verbatim", got)
	}
}

// TestHostSlashNames_StaleGenerationIsDropped: a catalog fetched from
// the outgoing session must not teach the incoming one its names. The
// switch clears the cache, and the straggler reply is dropped by the
// gen guard, so /usage — unknown to the new session — steers.
func TestHostSlashNames_StaleGenerationIsDropped(t *testing.T) {
	outgoing := newPausableSlashAgent()
	m := warmHostSlashNames(t, heldModel(t, outgoing))
	stale := m.hostSlashNamesCmd()()

	incoming := &pausableAgent{}
	incoming.setState(PauseInfo{Paused: true, Since: time.Unix(0, 0)})
	m.applySwitchTarget(&SwitchTarget{Agent: incoming})
	if m.hostSlashNames != nil {
		t.Fatalf("hostSlashNames = %v after a session switch, want it cleared", m.hostSlashNames)
	}
	out, _ := m.Update(stale)
	m = out.(*model)
	if m.hostSlashNames != nil {
		t.Fatalf("hostSlashNames = %v, want the outgoing session's reply dropped", m.hostSlashNames)
	}

	m.pause.PauseInfo = PauseInfo{Paused: true}
	m.input.SetValue("/usage")
	_, cmd := pressKey(m, tea.Key{Code: tea.KeyEnter})
	runCmd(t, cmd)
	if got := incoming.resumes(); len(got) != 1 || got[0].Steer != "/usage" {
		t.Errorf("Resume calls = %+v, want /usage steered to the new session", got)
	}
}

// TestHostSlashNames_SwitchRefetchesFromTheNewAgent: the cache is
// refreshed on attach, not just cleared — the switch's Cmd batch
// carries a fetch against the incoming agent under the new gen.
func TestHostSlashNames_SwitchRefetchesFromTheNewAgent(t *testing.T) {
	m := heldModel(t, &pausableAgent{})
	incoming := newPausableSlashAgent()
	cmd := m.applySwitchTarget(&SwitchTarget{Agent: incoming})
	if cmd == nil {
		t.Fatal("applySwitchTarget returned no Cmd")
	}
	// Not drainBatch: the batch also carries the new session's
	// listeners, which block by design. Each child runs on its own
	// goroutine and only the ones that return promptly are read.
	children := []tea.Cmd{cmd}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		children = batch
	}
	replies := make(chan tea.Msg, len(children))
	for _, c := range children {
		if c == nil {
			continue
		}
		go func() { replies <- c() }()
	}
	var landed bool
	deadline := time.After(2 * time.Second)
collect:
	for !landed {
		select {
		case msg := <-replies:
			if sc, ok := msg.(slashCommandsMsg); ok {
				out, _ := m.Update(sc)
				m = out.(*model)
				landed = true
			}
		case <-deadline:
			break collect
		}
	}
	if !landed {
		t.Fatal("applySwitchTarget issued no slash catalog fetch for the new agent")
	}
	if !m.hostNamesASlash("/usage") {
		t.Errorf("hostSlashNames = %v, want the new agent's catalog", m.hostSlashNames)
	}
}

// TestHostSlashNames_OutlivesThePalette: the palette's own fetch keeps
// the cache current, including when the reply lands after the palette
// has closed — its rows are dropped, its names are not.
func TestHostSlashNames_OutlivesThePalette(t *testing.T) {
	agent := newPausableSlashAgent()
	m := heldModel(t, agent)
	out, cmd := m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	m = out.(*model)
	if m.palette == nil || cmd == nil {
		t.Fatal("setup: / did not open the palette with a host fetch")
	}
	reply := cmd()
	m, _ = pressKey(m, tea.Key{Code: tea.KeyBackspace})
	if m.palette != nil {
		t.Fatal("setup: backspace did not close the palette")
	}
	out, _ = m.Update(reply)
	m = out.(*model)
	if !m.hostNamesASlash("/attach") {
		t.Errorf("hostSlashNames = %v, want the late palette reply retained", m.hostSlashNames)
	}
}

// TestHostSlashNames_NotConsultedMidTurn pins the deliberate asymmetry
// from the issue: mid-turn asks the safety question, so a host-only
// name is queued as prose there even with the catalog cached.
func TestHostSlashNames_NotConsultedMidTurn(t *testing.T) {
	agent := newPausableSlashAgent()
	agent.setState(PauseInfo{})
	m := newModel(Options{Agent: agent})
	m.width, m.height = 100, 40
	m = warmHostSlashNames(t, m)
	m.state = stateStreaming
	m.input.SetValue("/usage")

	next, _ := pressKey(m, tea.Key{Code: tea.KeyEnter})
	if len(next.queue) != 1 {
		t.Errorf("queue has %d entries, want /usage queued mid-turn", len(next.queue))
	}
}

// TestHostSlashNameSet_Folds pins the fold: lowercased, trimmed,
// canonicalised, aliases included, and non-nil for an empty catalog
// so "fetched, nothing there" differs from "not fetched".
func TestHostSlashNameSet_Folds(t *testing.T) {
	got := hostSlashNameSet([]SlashCommandSpec{
		{Name: "Usage"},
		{Name: "perms"},
		{Name: "x", Aliases: []string{" Y ", ""}},
	})
	for _, want := range []string{"usage", "permissions", "x", "y"} {
		if !got[want] {
			t.Errorf("set %v is missing %q", got, want)
		}
	}
	if len(got) != 4 {
		t.Errorf("set %v has %d entries, want 4", got, len(got))
	}
	if hostSlashNameSet(nil) == nil {
		t.Error("hostSlashNameSet(nil) = nil, want an empty non-nil set")
	}
}
