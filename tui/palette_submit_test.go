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

// typeLine enters text one keystroke at a time, which is what an
// operator does and what a test that calls input.SetValue does not.
// The difference is the whole of issue #278: the leading `/` opens the
// slash palette, and the palette has its own Enter handler.
func typeLine(m model, text string) model {
	for _, r := range text {
		out, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = out.(model)
	}
	return m
}

// submitRoute is one of the two ways a line reaches the submit path.
type submitRoute struct {
	name string
	load func(model, string) model
}

// bothRoutes: typed rune by rune (palette open, the operator's route)
// and pre-loaded into the textarea (no palette, the route every test
// used before issue #278). They must agree.
var bothRoutes = []submitRoute{
	{"typed", typeLine},
	{"preloaded", func(m model, text string) model {
		m.input.SetValue(text)
		return m
	}},
}

// TestPaletteEnter_MidTurnRefusalStillFires is the reported defect.
// /clear typed during a turn armed its confirmation instead of being
// refused, because the palette's Enter dispatched it before the
// mid-turn gate ever ran.
func TestPaletteEnter_MidTurnRefusalStillFires(t *testing.T) {
	for _, route := range bothRoutes {
		t.Run(route.name, func(t *testing.T) {
			m := newModel(Options{Agent: &liveAgentStub{}})
			m.width, m.height = 100, 40
			m.state = stateStreaming
			m = route.load(m, "/clear")

			next, _ := pressKey(m, tea.Key{Code: tea.KeyEnter})
			if next.confirmingClear {
				t.Error("/clear armed its confirmation during a turn instead of being refused")
			}
			if got := lastText(next); !strings.Contains(got, "not while a turn is running") {
				t.Errorf("row = %q, want the mid-turn refusal", got)
			}
		})
	}
}

// TestPaletteEnter_ProseStillQueuesMidTurn guards R-HOLD-3's default
// bucket on the palette route. Through the bypass, "/foo bar" was
// dispatched and answered `unknown command` — prose hijacked as a
// command, which is exactly what the bucket exists to prevent.
func TestPaletteEnter_ProseStillQueuesMidTurn(t *testing.T) {
	for _, route := range bothRoutes {
		t.Run(route.name, func(t *testing.T) {
			m := newModel(Options{Agent: &liveAgentStub{}})
			m.width, m.height = 100, 40
			m.state = stateStreaming
			m = route.load(m, "/foo bar")

			next, _ := pressKey(m, tea.Key{Code: tea.KeyEnter})
			if len(next.queue) != 1 {
				t.Fatalf("queue has %d entries, want the line queued as prompt text", len(next.queue))
			}
			if got := lastText(next); strings.Contains(got, "unknown command") {
				t.Errorf("row = %q, want nothing dispatched", got)
			}
		})
	}
}

// TestPaletteEnter_MidTurnSafeSlashesStillDispatch. The gate is a
// three-way split and the fix must not turn the palette into a fourth
// answer: an allowlisted slash still runs now.
func TestPaletteEnter_MidTurnSafeSlashesStillDispatch(t *testing.T) {
	for _, route := range bothRoutes {
		t.Run(route.name, func(t *testing.T) {
			agent := &pausableAgent{}
			m := newModel(Options{Agent: agent})
			m.width, m.height = 100, 40
			m.state = stateStreaming
			cancelled := false
			m.cancelTurn = func() { cancelled = true }
			m = route.load(m, "/interrupt")

			next, cmd := pressKey(m, tea.Key{Code: tea.KeyEnter})
			if !cancelled {
				t.Fatal("/interrupt did not reach the dispatcher")
			}
			if len(next.queue) != 0 {
				t.Errorf("/interrupt was queued; queue has %d entries", len(next.queue))
			}
			runCmd(t, cmd)
			if got := agent.pauses(); len(got) != 1 {
				t.Errorf("Pause calls = %q, want the hold to follow the cancel", got)
			}
		})
	}
}

// TestEnterWhileHeld_SlashDispatches is the defect the bypass was
// hiding rather than causing. The steer arm does not look at the
// leading slash, so with the palette closed a /stats typed while the
// agent is parked went to the host as steer prose.
func TestEnterWhileHeld_SlashDispatches(t *testing.T) {
	for _, route := range bothRoutes {
		t.Run(route.name, func(t *testing.T) {
			agent := &pausableAgent{}
			agent.setState(PauseInfo{Paused: true, Since: time.Unix(0, 0)})
			m := newModel(Options{Agent: agent})
			m.width, m.height = 100, 40
			m.pause.PauseInfo = PauseInfo{Paused: true}
			if !m.pause.paused() {
				t.Fatal("setup: model is not held")
			}
			m = route.load(m, "/stats")

			next, cmd := pressKey(m, tea.Key{Code: tea.KeyEnter})
			// /stats paints from the model's own cache, so a nil Cmd
			// here is the dispatch working, not a missing one.
			if cmd != nil {
				runCmd(t, cmd)
			}
			if got := agent.resumes(); len(got) != 0 {
				t.Fatalf("Resume calls = %+v, want /stats dispatched rather than sent as a steer", got)
			}
			if got := lastText(next); !strings.Contains(got, "/stats") {
				t.Errorf("row = %q, want /stats to have answered", got)
			}
		})
	}
}

// TestEnterWhileHeld_ProseStillSteers is the other half: the held
// input box is a steer field, so a line that only looks like a command
// still has to reach the host.
func TestEnterWhileHeld_ProseStillSteers(t *testing.T) {
	for _, route := range bothRoutes {
		t.Run(route.name, func(t *testing.T) {
			agent := &pausableAgent{}
			agent.setState(PauseInfo{Paused: true, Since: time.Unix(0, 0)})
			m := newModel(Options{Agent: agent})
			m.width, m.height = 100, 40
			m.pause.PauseInfo = PauseInfo{Paused: true}
			m = route.load(m, "/tmp is full, take a look")

			_, cmd := pressKey(m, tea.Key{Code: tea.KeyEnter})
			runCmd(t, cmd)
			got := agent.resumes()
			if len(got) != 1 {
				t.Fatalf("Resume calls = %+v, want the line steered", got)
			}
			if got[0].Mode != ResumeModeSteer {
				t.Errorf("Mode = %q, want %q", got[0].Mode, ResumeModeSteer)
			}
			if got[0].Steer != "/tmp is full, take a look" {
				t.Errorf("Steer = %q, want the line verbatim", got[0].Steer)
			}
		})
	}
}

// TestPaletteEnter_ClearConfirmationAnswersInsteadOfDispatching. The
// /clear prompt says "anything else cancels", and a slash typed at it
// is anything else. Through the bypass the slash ran and the
// confirmation stayed armed underneath it, so the next bare Enter
// wiped the transcript.
func TestPaletteEnter_ClearConfirmationAnswersInsteadOfDispatching(t *testing.T) {
	for _, route := range bothRoutes {
		t.Run(route.name, func(t *testing.T) {
			m := newModel(Options{Agent: &liveAgentStub{}})
			m.width, m.height = 100, 40
			m.confirmingClear = true
			m = route.load(m, "/help")

			next, _ := pressKey(m, tea.Key{Code: tea.KeyEnter})
			if next.confirmingClear {
				t.Error("confirmation still armed after an answer that is not y/yes")
			}
			if got := lastText(next); !strings.Contains(got, "clear cancelled") {
				t.Errorf("row = %q, want the cancellation", got)
			}
		})
	}
}

// TestPaletteEnter_IdleDispatchIsUnchanged. The palette's one-keystroke
// insert-and-submit is the reason it has an Enter arm at all, and
// routing it through the shared path must not cost it that.
func TestPaletteEnter_IdleDispatchIsUnchanged(t *testing.T) {
	m := newModel(Options{Agent: &liveAgentStub{}})
	m.width, m.height = 100, 40
	m = typeLine(m, "/keys")
	if m.palette == nil {
		t.Fatal("typing / did not open the slash palette — this test proves nothing")
	}

	next, _ := pressKey(m, tea.Key{Code: tea.KeyEnter})
	if next.input.Value() != "" {
		t.Errorf("input = %q, want the line submitted in one keystroke", next.input.Value())
	}
	if next.history.Len() == 0 {
		t.Fatal("/keys from the palette rendered nothing")
	}
}
