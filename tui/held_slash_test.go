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
	"time"

	tea "charm.land/bubbletea/v2"
)

// Issue #299. The held arm classified a submitted line with
// midTurnSlashDisposition, whose tables answer "is this safe against a
// running turn?" — a question with no meaning while parked, and one
// whose conservative default sent every command missing from them to
// the host as steer prose.
//
// The helpers here (bothRoutes, typeLine, pausableAgent, runCmd) come
// from palette_submit_test.go, which covers the same arm for the two
// cases #278 was about.

// TestEnterWhileHeld_ClientLocalSlashDispatches walks the commands the
// TUI dispatches itself that appear in neither mid-turn table.
//
// /quit is the case that makes this more than cosmetic: an operator
// who parks a misbehaving agent and then types /quit was opening the
// gate and handing it "/quit" as its next instruction — the command's
// effect exactly inverted, at the moment an operator is most likely to
// reach for it.
func TestEnterWhileHeld_ClientLocalSlashDispatches(t *testing.T) {
	cmds := []string{
		"/mouse", "/quit", "/exit", "/theme", "/model", "/switch",
		"/reload", "/permissions", "/pricing", "/resume", "/deny",
	}
	for _, cmd := range cmds {
		for _, route := range bothRoutes {
			t.Run(cmd+"/"+route.name, func(t *testing.T) {
				agent := &pausableAgent{}
				agent.setState(PauseInfo{Paused: true, Since: time.Unix(0, 0)})
				m := newModel(Options{Agent: agent})
				m.width, m.height = 100, 40
				m.pause.PauseInfo = PauseInfo{Paused: true}
				m = route.load(m, cmd)

				_, c := pressKey(m, tea.Key{Code: tea.KeyEnter})
				if c != nil {
					runCmd(t, c)
				}
				if got := agent.resumes(); len(got) != 0 {
					t.Errorf("%s while held: Resume calls = %+v, want it dispatched locally rather than sent as a steer", cmd, got)
				}
			})
		}
	}
}

// TestEnterWhileHeld_UnknownSlashStillSteers pins the other side of the
// widened test: the question is recognition, not "starts with a
// slash". A name nothing owns is prose and still has to reach the
// host, or the steer field stops being one. /mous and "/quitting time"
// are the near-misses that would fall to a prefix match.
func TestEnterWhileHeld_UnknownSlashStillSteers(t *testing.T) {
	// Not "/mous": typing that leaves the slash palette open with
	// "mouse" highlighted, so Enter completes it. That is the
	// palette doing its job, not the hold arm doing anything.
	for _, line := range []string{"/mousetrap", "/quitting time, wrap it up", "/opt/data is full"} {
		t.Run(line, func(t *testing.T) {
			agent := &pausableAgent{}
			agent.setState(PauseInfo{Paused: true, Since: time.Unix(0, 0)})
			m := newModel(Options{Agent: agent})
			m.width, m.height = 100, 40
			m.pause.PauseInfo = PauseInfo{Paused: true}
			m = typeLine(m, line)

			_, cmd := pressKey(m, tea.Key{Code: tea.KeyEnter})
			runCmd(t, cmd)
			got := agent.resumes()
			if len(got) != 1 || got[0].Mode != ResumeModeSteer || got[0].Steer != line {
				t.Errorf("Resume calls = %+v, want %q steered verbatim", got, line)
			}
		})
	}
}

// TestBuiltinSlashNames_MatchesDispatcher is the drift guard, and it
// runs in both directions because either drift reintroduces #299: a
// name the switch answers to but the set omits steers while held, and
// a name in the set the switch has since dropped gets swallowed here
// instead of reaching the host's provider.
func TestBuiltinSlashNames_MatchesDispatcher(t *testing.T) {
	// switchAgent rather than liveAgentStub: /switch is the one case
	// that answers handled=false on purpose, when the host is not a
	// SessionSwitcher, so a host-provided "switch" can take the name
	// instead. Handing the oracle a host that HAS the capability
	// keeps the check strict everywhere rather than carving out an
	// exemption a future genuinely-missing name could hide in.
	for name := range builtinSlashNames {
		m := newModel(Options{Agent: &switchAgent{}})
		m.width, m.height = 100, 40
		if handled, _, _ := m.dispatchBuiltinSlash(name, ""); !handled {
			t.Errorf("builtinSlashNames has %q but dispatchBuiltinSlash does not handle it", name)
		}
	}
	// Every name the palette advertises has to be recognized too. The
	// palette is where an operator learns a command exists, so one
	// listed there and unrecognized while held is the same bug in a
	// different hat. Multiword entries ("pricing set") key off their
	// first word, which is how dispatch reads them.
	for _, item := range builtinSlashItems() {
		first, _, _ := strings.Cut(item.Name, " ")
		if !builtinSlashNames[canonicalSlashName(first)] {
			t.Errorf("palette advertises %q but builtinSlashNames omits %q", item.Name, first)
		}
	}
}
