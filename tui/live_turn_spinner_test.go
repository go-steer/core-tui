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

// Issue #339: on the live path the spinner followed chat chunks only,
// so the commit that precedes an ordinary tool call stopped it and the
// running tool had no indicator under it. The host's turn_state spans
// the whole turn and now drives the stretch.

// liveTurnModel returns a live model with fixed phrase pools, so the
// spinner line reads THINK or WORK and a test can tell them apart.
func liveTurnModel(t *testing.T) *model {
	t.Helper()
	m := newModel(Options{
		Agent:           newLiveAgentStub(),
		ThinkingPhrases: []string{"THINK"},
		WorkingPhrases:  []string{"WORK"},
	})
	m.width, m.height = 100, 40
	m.viewport.SetWidth(80)
	if !m.liveMode {
		t.Fatal("setup: expected liveMode for a LiveAgent host")
	}
	return m
}

func liveStep(t *testing.T, m *model, msg tea.Msg) (*model, tea.Cmd) {
	t.Helper()
	out, cmd := m.Update(msg)
	return out.(*model), cmd
}

func pushTurnState(t *testing.T, m *model, state string) (*model, tea.Cmd) {
	t.Helper()
	return liveStep(t, m, statusUpdateMsg{status: StatusUpdate{TurnState: state}})
}

func TestLiveSpinner_FollowsTurnStateThroughAToolCall(t *testing.T) {
	m := liveTurnModel(t)

	// A turn another client started: turn_state arrives before any
	// token, and that alone has to open the stretch.
	m, _ = pushTurnState(t, m, TurnStateStreaming)
	if !m.spinnerActive {
		t.Fatal("streaming turn_state did not open the live stretch")
	}
	gen := m.spinnerGen
	if !strings.Contains(m.renderInProgress(), "THINK") {
		t.Errorf("before the first token: spinner line missing, got %q", m.renderInProgress())
	}

	m, _ = liveStep(t, m, streamChunkMsg{text: "Let me check.", partial: true})
	m, _ = liveStep(t, m, streamChunkMsg{text: "Let me check.", partial: false})
	if !m.spinnerActive {
		t.Fatal("a mid-turn commit closed the stretch while the host still reports streaming")
	}
	m, _ = liveStep(t, m, toolCallMsg{id: "c1", name: "bash", args: map[string]any{"command": "go test ./..."}})
	if got := m.renderInProgress(); !strings.Contains(got, "WORK") {
		t.Errorf("while the tool runs: want the working spinner line, got %q", got)
	}
	if m.spinnerGen != gen {
		t.Errorf("spinnerGen moved from %d to %d mid-turn — the stretch restarted", gen, m.spinnerGen)
	}

	m, _ = liveStep(t, m, toolResultMsg{id: "c1", name: "bash", response: map[string]any{"output": "ok"}})
	m, _ = liveStep(t, m, streamChunkMsg{text: "All green.", partial: false})
	if !m.spinnerActive {
		t.Fatal("the closing commit ended the stretch before the host reported idle")
	}
	m, _ = pushTurnState(t, m, TurnStateIdle)
	if m.spinnerActive {
		t.Error("idle turn_state did not close the live stretch")
	}
	if got := m.renderInProgress(); got != "" {
		t.Errorf("after idle: want no in-progress block, got %q", got)
	}
	if m.toolActive {
		t.Error("toolActive survived the end of the stretch; the next turn would open on a working verb")
	}
}

func TestLiveSpinner_StreamingPushArmsTheTickChainOnce(t *testing.T) {
	m := liveTurnModel(t)
	m, cmd := pushTurnState(t, m, TurnStateStreaming)
	if cmd == nil {
		t.Fatal("no Cmd returned — the tick chain was never armed")
	}
	gen := m.spinnerGen
	// A repeat push (a model swap mid-turn carries turn_state too)
	// must ride the running chain, not start a second one.
	m, _ = pushTurnState(t, m, TurnStateStreaming)
	if m.spinnerGen != gen {
		t.Errorf("repeat streaming push bumped spinnerGen %d -> %d", gen, m.spinnerGen)
	}
}

func TestLiveSpinner_IdleLeavesPendingTextToTheCommit(t *testing.T) {
	m := liveTurnModel(t)
	m, _ = pushTurnState(t, m, TurnStateStreaming)
	m, _ = liveStep(t, m, streamChunkMsg{text: "half an answ", partial: true})

	// idle overtaking the commit must not hide the text on screen.
	m, _ = pushTurnState(t, m, TurnStateIdle)
	if !m.spinnerActive {
		t.Fatal("idle closed the stretch over uncommitted text")
	}
	if !m.turnInFlight() || m.inProgressText != "half an answ" {
		t.Errorf("pending text off the render gate: inFlight=%v text=%q", m.turnInFlight(), m.inProgressText)
	}
	m, _ = liveStep(t, m, streamChunkMsg{text: "half an answer", partial: false})
	if m.spinnerActive {
		t.Error("the commit after idle did not close the stretch")
	}
}

func TestLiveSpinner_HostWithoutTurnStateKeepsChunkBehaviour(t *testing.T) {
	m := liveTurnModel(t)
	m, _ = liveStep(t, m, streamChunkMsg{text: "hi", partial: true})
	m, _ = liveStep(t, m, streamChunkMsg{text: "hi", partial: false})
	if m.spinnerActive {
		t.Error("with no turn_state ever pushed, the commit must still close the stretch")
	}
}

func TestLiveSpinner_AwaitingStatesSayWhatTheyWaitOn(t *testing.T) {
	for state, want := range map[string]string{
		TurnStateAwaitingPermission: "Waiting for approval",
		TurnStateAwaitingElicit:     "Waiting for input",
	} {
		t.Run(state, func(t *testing.T) {
			m := liveTurnModel(t)
			m, _ = pushTurnState(t, m, TurnStateStreaming)
			m, _ = liveStep(t, m, toolCallMsg{id: "c1", name: "bash"})
			// Observer mode: the prompt went to another client, so
			// nothing local replaces the spinner line.
			m, _ = pushTurnState(t, m, state)
			got := m.renderInProgress()
			if !strings.Contains(got, want) {
				t.Errorf("want %q on the spinner line, got %q", want, got)
			}
			if strings.Contains(got, "WORK") || strings.Contains(got, "THINK") {
				t.Errorf("a turn blocked on a person still claims to be working: %q", got)
			}
		})
	}
}

func TestLiveSpinner_DisconnectedIgnoresTurnState(t *testing.T) {
	m := liveTurnModel(t)
	m.liveDisconnected = true
	m, _ = pushTurnState(t, m, TurnStateStreaming)
	if m.spinnerActive {
		t.Error("a disconnected stream opened a spinner nothing will ever close")
	}
}
