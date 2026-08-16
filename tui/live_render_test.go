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

// Render coverage for the LiveAgent path (issue #135).
//
// The live path had accumulation coverage and zero render coverage,
// which is how it shipped accumulating streamed prose and a spinner
// verb into a block that renderInProgress refused to emit. Every
// assertion here therefore goes through renderInProgress or View()
// rather than reading m.inProgressText back out — reading the buffer
// is the assertion shape that missed the defect.

package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// liveVerb is a pinned thinking phrase. The production pools rotate
// and contain prose that could plausibly appear elsewhere in a frame;
// a sentinel verb makes "the spinner line is on screen" a statement
// about the spinner line.
const liveVerb = "LIVEVERB-thinking"

// liveRenderModel builds a sized, live-mode Model with a pinned verb
// pool and, when at is non-nil, a pinned clock. It goes through
// NewModel + WindowSizeMsg so the viewport and chrome budget are the
// real ones — the frame assertions below are worthless against a
// hand-built Model with a zero-width viewport.
func liveRenderModel(t *testing.T, at *time.Time) Model {
	t.Helper()
	return liveRenderModelWith(t, at, newLiveAgentStub())
}

// liveRenderModelWith is liveRenderModel over a caller-supplied host,
// for the tests that need one implementing InjectableAgent as well.
func liveRenderModelWith(t *testing.T, at *time.Time, agent Agent) Model {
	t.Helper()
	m := NewModel(Options{
		Agent:           agent,
		ThinkingPhrases: []string{liveVerb},
	})
	if !m.liveMode {
		t.Fatal("setup: expected liveMode=true for a LiveAgent host")
	}
	if at != nil {
		m.now = fixedClock(at)
	}
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return out.(Model)
}

// livePartial pushes one partial chunk through the real Update path
// (not applyStreamChunk directly) so the sessionGen guard, the
// spinner arming and the render kick all run as they do in a session.
func livePartial(t *testing.T, m Model, text string) Model {
	t.Helper()
	out, _ := m.Update(streamChunkMsg{gen: m.sessionGen, text: text, partial: true})
	next := out.(Model)
	next.refreshViewport()
	return next
}

// TestLiveRender_StreamedProseReachesTheFrame is issue #135's repro,
// inverted: the sentinel must be visible.
//
// Before the fix this model reported liveMode=true, spinnerActive=true
// and inProgressText="SENTINEL streamed prose" while renderInProgress
// returned "" — every piece of state correct and nothing on screen.
func TestLiveRender_StreamedProseReachesTheFrame(t *testing.T) {
	m := liveRenderModel(t, nil)
	m = livePartial(t, m, "SENTINEL streamed prose")

	if m.state == stateStreaming {
		t.Fatal("setup: the live path is not supposed to set stateStreaming — " +
			"if it now does, this test is guarding the wrong thing")
	}
	if !m.spinnerActive {
		t.Fatal("setup: expected spinnerActive after a partial chunk in liveMode")
	}
	if got := m.renderInProgress(); !strings.Contains(got, "SENTINEL") {
		t.Errorf("renderInProgress() = %q, want it to carry the streamed prose", got)
	}
	if got := m.View().Content; !strings.Contains(got, "SENTINEL") {
		t.Error("streamed prose is in the buffer but not on screen — issue #135")
	}
}

// TestLiveRender_SpinnerLineRendersDuringAStretch covers the other
// half of the block the gate was swallowing. The spinner is the only
// signal an operator gets during a tool-only stretch, where there is
// no prose to render at all.
func TestLiveRender_SpinnerLineRendersDuringAStretch(t *testing.T) {
	m := liveRenderModel(t, nil)
	m = livePartial(t, m, "some tokens")

	if got := m.renderSpinnerLine(); !strings.Contains(got, liveVerb) {
		t.Fatalf("setup: renderSpinnerLine() = %q, want the pinned verb", got)
	}
	if got := m.renderInProgress(); !strings.Contains(got, liveVerb) {
		t.Errorf("renderInProgress() = %q, want the spinner verb line", got)
	}
	if got := m.View().Content; !strings.Contains(got, liveVerb) {
		t.Error("spinner verb rotates but never paints in liveMode — issue #135")
	}
}

// TestLiveRender_SpinnerRendersWithNoProse is the tool-only stretch:
// spinnerActive with an empty inProgressText. The gate has to open on
// the spinner alone, or an autonomous stretch that starts with a tool
// call shows nothing until the first token of prose.
func TestLiveRender_SpinnerRendersWithNoProse(t *testing.T) {
	m := liveRenderModel(t, nil)
	m = livePartial(t, m, "")

	if !m.spinnerActive {
		t.Fatal("setup: expected spinnerActive after an empty partial chunk")
	}
	if strings.TrimSpace(m.inProgressText) != "" {
		t.Fatalf("setup: expected no accumulated prose, got %q", m.inProgressText)
	}
	if got := m.renderInProgress(); !strings.Contains(got, liveVerb) {
		t.Errorf("renderInProgress() = %q, want the spinner line on a prose-less stretch", got)
	}
}

// TestLiveRender_CommitClosesTheBlock pins the other edge: once the
// stretch commits, the in-progress block goes away and the text lives
// in history exactly once. A gate that opens on liveMode alone would
// leave the spinner running forever between stretches.
func TestLiveRender_CommitClosesTheBlock(t *testing.T) {
	m := liveRenderModel(t, nil)
	m = livePartial(t, m, "SENTINEL streamed prose")

	out, _ := m.Update(streamChunkMsg{gen: m.sessionGen, text: "SENTINEL streamed prose", partial: false})
	m = out.(Model)
	m.refreshViewport()

	if m.spinnerActive {
		t.Error("commit left spinnerActive set")
	}
	if got := m.renderInProgress(); got != "" {
		t.Errorf("renderInProgress() after commit = %q, want empty", got)
	}
	if got := m.View().Content; strings.Contains(got, liveVerb) {
		t.Error("spinner verb still on screen after the stretch committed")
	}
	if got := m.View().Content; !strings.Contains(got, "SENTINEL") {
		t.Error("committed prose vanished from the frame")
	}
}

// TestLiveRender_ElapsedIsPerStretch verifies issue #111 actually
// paints now that the gate is open, and that a second stretch measures
// from its own origin rather than inheriting the first's.
//
// The readout only exists in the rendered line, so this asserts on the
// frame: turnStarted being correct is already covered in elapsed_test,
// and it was correct all along — it just had nowhere to appear.
func TestLiveRender_ElapsedIsPerStretch(t *testing.T) {
	clock := time.Date(2026, 8, 15, 9, 0, 0, 0, time.UTC)
	m := liveRenderModel(t, &clock)

	m = livePartial(t, m, "first stretch")
	clock = clock.Add(20 * time.Second)
	m.refreshViewport()
	if got := m.View().Content; !strings.Contains(got, "20s") {
		t.Error("elapsed readout missing from the frame twenty seconds into a live stretch")
	}

	// Commit, then idle for ten minutes. Nothing is in flight, so
	// nothing may claim to be.
	out, _ := m.Update(streamChunkMsg{gen: m.sessionGen, text: "first stretch", partial: false})
	m = out.(Model)
	clock = clock.Add(10 * time.Minute)
	m.refreshViewport()
	if got := m.View().Content; strings.Contains(got, "10m") {
		t.Error("idle frame is showing an elapsed readout")
	}

	// Second stretch, five seconds in. It must read 5s — 10m05s is
	// what an inherited origin would produce, and the whole point of
	// stamping at the false→true flip.
	m = livePartial(t, m, "second stretch")
	clock = clock.Add(5 * time.Second)
	m.refreshViewport()
	frame := m.View().Content
	if !strings.Contains(frame, "5s") {
		t.Error("second stretch is not showing its own elapsed readout")
	}
	if strings.Contains(frame, "10m") {
		t.Error("second stretch inherited the first stretch's elapsed origin")
	}
}

// TestLiveRender_SpinnerGenPerStretch verifies issue #112 on the
// visible path: each stretch gets its own tick chain, the superseded
// chain is inert, and the live chain's rotation reaches the frame.
func TestLiveRender_SpinnerGenPerStretch(t *testing.T) {
	m := liveRenderModel(t, nil)
	m = livePartial(t, m, "first")
	firstGen := m.spinnerGen

	// The live chain ticks and the rotation is visible.
	before := m.thinkingIdx
	out, _ := m.Update(spinnerTickMsg{gen: firstGen})
	m = out.(Model)
	if m.thinkingIdx != before+1 {
		t.Errorf("live tick did not rotate the verb: thinkingIdx %d → %d", before, m.thinkingIdx)
	}

	// Commit ends the stretch; a leftover tick from the dead chain
	// must not restart the animation.
	out, _ = m.Update(streamChunkMsg{gen: m.sessionGen, text: "first", partial: false})
	m = out.(Model)
	idle := m.thinkingIdx
	out, _ = m.Update(spinnerTickMsg{gen: firstGen})
	m = out.(Model)
	if m.thinkingIdx != idle {
		t.Error("a tick from the committed stretch kept rotating after the block closed")
	}

	// Second stretch: new generation, and the old stamp stays inert
	// while the new one drives.
	m = livePartial(t, m, "second")
	if m.spinnerGen == firstGen {
		t.Fatalf("second stretch reused spinnerGen %d — issue #112", firstGen)
	}
	stale := m.thinkingIdx
	out, _ = m.Update(spinnerTickMsg{gen: firstGen})
	m = out.(Model)
	if m.thinkingIdx != stale {
		t.Error("superseded tick chain rotated the second stretch's verb")
	}
	out, _ = m.Update(spinnerTickMsg{gen: m.spinnerGen})
	m = out.(Model)
	if m.thinkingIdx != stale+1 {
		t.Error("second stretch's own tick chain did not rotate the verb")
	}
	m.refreshViewport()
	if got := m.View().Content; !strings.Contains(got, liveVerb) {
		t.Error("spinner line missing from the frame during the second stretch")
	}
}

// TestTurnInFlight covers the predicate the gate and the tick handler
// now share, including the combinations that must stay shut: liveMode
// with an idle spinner (between stretches) and a spinner flag set on a
// non-live model (belt and braces — nothing sets that today).
func TestTurnInFlight(t *testing.T) {
	cases := []struct {
		name    string
		state   turnState
		live    bool
		spinner bool
		want    bool
	}{
		{"idle", stateIdle, false, false, false},
		{"run-streaming", stateStreaming, false, false, true},
		{"live-stretch", stateIdle, true, true, true},
		{"live-between-stretches", stateIdle, true, false, false},
		{"spinner-without-live-mode", stateIdle, false, true, false},
		{"live-and-run-streaming", stateStreaming, true, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := Model{state: tc.state, liveMode: tc.live, spinnerActive: tc.spinner}
			if got := m.turnInFlight(); got != tc.want {
				t.Errorf("turnInFlight() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRenderInProgress_NonLivePathsUnchanged is the regression guard
// on the widened gate: the queue-only case (idle state, non-empty
// queue) must still render, and a plain idle model must still render
// nothing at all.
func TestRenderInProgress_NonLivePathsUnchanged(t *testing.T) {
	t.Run("idle-empty", func(t *testing.T) {
		m := NewModel(Options{Agent: &bareAgent{id: "idle"}})
		out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
		m = out.(Model)
		if got := m.renderInProgress(); got != "" {
			t.Errorf("renderInProgress() on an idle model = %q, want empty", got)
		}
	})

	t.Run("queue-only", func(t *testing.T) {
		m := NewModel(Options{Agent: &bareAgent{id: "queue"}})
		out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
		m = out.(Model)
		m.queue = append(m.queue, QueueEntry{Text: "QUEUED-SENTINEL", State: QueueQueued})
		if m.state == stateStreaming {
			t.Fatal("setup: wanted an idle model with a non-empty queue")
		}
		got := m.renderInProgress()
		if !strings.Contains(got, "QUEUED-SENTINEL") {
			t.Errorf("renderInProgress() = %q, want the queue panel", got)
		}
		if strings.Contains(got, liveVerb) {
			t.Error("queue-only render emitted a spinner line")
		}
	})

	t.Run("idle-live-model-with-no-stretch", func(t *testing.T) {
		m := liveRenderModel(t, nil)
		if got := m.renderInProgress(); got != "" {
			t.Errorf("renderInProgress() on a live model between stretches = %q, want empty", got)
		}
	})
}

// liveSubmit types text into a live-mode Model and presses enter,
// returning the resulting Model and the Cmd Update handed back. It
// drains the stub's injectsOut so a test can assert the host was
// actually called.
func liveSubmit(t *testing.T, m Model, text string) (Model, tea.Cmd) {
	t.Helper()
	m.input.SetValue(text)
	out, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	next := out.(Model)
	next.refreshViewport()
	return next, cmd
}

// TestLiveRender_InjectStartsTheSpinnerBeforeTheFirstToken is issue
// #148's repro. The window between "operator pressed enter" and "the
// host's first token arrives" is the one they most need an indicator
// for, and it was the one window with none: the stretch was opened by
// applyStreamChunk, which by definition cannot run before the first
// chunk.
func TestLiveRender_InjectStartsTheSpinnerBeforeTheFirstToken(t *testing.T) {
	agent := newInjectableLiveAgentStub()
	m := liveRenderModelWith(t, nil, agent)
	before := m.spinnerGen

	m, cmd := liveSubmit(t, m, "do the thing")

	select {
	case <-agent.injectsOut:
	default:
		t.Fatal("setup: expected Inject to be called")
	}
	if !m.spinnerActive {
		t.Error("submit did not start a spinner stretch")
	}
	if !m.turnInFlight() {
		t.Error("the in-progress block is still gated shut after a submit")
	}
	if m.spinnerGen == before {
		t.Errorf("spinnerGen not bumped for the new stretch (still %d) — issue #112", before)
	}
	if cmd == nil {
		t.Error("no Cmd returned to arm the tick chain — the glyph would never rotate")
	}
	if got := m.View().Content; !strings.Contains(got, liveVerb) {
		t.Error("no waiting indicator on screen between submit and the first token — issue #148")
	}
}

// TestLiveRender_InjectThenFirstPartialIsOneStretch pins the seam the
// fix creates: two events can now open a stretch, and the second must
// not restart it. A restart would bump spinnerGen out from under the
// live tick chain (#112) and move the elapsed origin off the moment
// the operator pressed enter (#111).
func TestLiveRender_InjectThenFirstPartialIsOneStretch(t *testing.T) {
	clock := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	m := liveRenderModelWith(t, &clock, newInjectableLiveAgentStub())

	m, _ = liveSubmit(t, m, "do the thing")
	gen, started := m.spinnerGen, m.turnStarted

	// Host takes eight seconds to produce its first token.
	clock = clock.Add(8 * time.Second)
	m = livePartial(t, m, "first token")

	if m.spinnerGen != gen {
		t.Errorf("the first partial restarted the stretch: spinnerGen %d → %d", gen, m.spinnerGen)
	}
	if !m.turnStarted.Equal(started) {
		t.Errorf("the first partial moved the elapsed origin: %v → %v", started, m.turnStarted)
	}
	// The readout must measure from the submit, not from the token:
	// 8s is the whole point, 0s is what a restart would show.
	if got := m.View().Content; !strings.Contains(got, "8s") {
		t.Error("elapsed readout is measuring from the first token rather than from the submit")
	}
}

// TestLiveRender_CommitOnlyHostGetsASpinner covers the second half of
// #148: a host that emits whole messages and never a partial. Under
// the old arming rule that host showed a spinner literally never,
// because the only site that opened a stretch required msg.partial.
func TestLiveRender_CommitOnlyHostGetsASpinner(t *testing.T) {
	m := liveRenderModelWith(t, nil, newInjectableLiveAgentStub())

	m, _ = liveSubmit(t, m, "do the thing")
	if got := m.View().Content; !strings.Contains(got, liveVerb) {
		t.Fatal("setup: expected the spinner to be up after the submit")
	}

	// The host's entire reply, in one committed chunk.
	out, _ := m.Update(streamChunkMsg{gen: m.sessionGen, text: "SENTINEL whole reply", partial: false})
	m = out.(Model)
	m.refreshViewport()

	if m.spinnerActive {
		t.Error("the commit left the spinner running")
	}
	frame := m.View().Content
	if strings.Contains(frame, liveVerb) {
		t.Error("spinner verb still on screen after the reply committed")
	}
	if !strings.Contains(frame, "SENTINEL") {
		t.Error("committed reply missing from the frame")
	}
}

// TestLiveRender_FailedInjectDoesNotStartASpinner — the host refused
// the submission, so there is nothing to wait for. Showing a spinner
// under the error row would be the same lie in the other direction.
func TestLiveRender_FailedInjectDoesNotStartASpinner(t *testing.T) {
	agent := newInjectableLiveAgentStub()
	agent.injectErr = errors.New("channel closed")
	m := liveRenderModelWith(t, nil, agent)

	m, cmd := liveSubmit(t, m, "do the thing")

	if m.spinnerActive {
		t.Error("a rejected submission started a spinner stretch")
	}
	if cmd != nil {
		t.Error("a rejected submission armed a tick chain")
	}
	frame := m.View().Content
	if strings.Contains(frame, liveVerb) {
		t.Error("waiting indicator on screen for a submission the host refused")
	}
	if !strings.Contains(frame, "inject failed") {
		t.Error("the inject error is not on screen")
	}
	// The typed text must not be echoed as a user row when the host
	// never took it — pre-existing behaviour, guarded here because
	// the fix restructured this branch.
	for _, e := range m.history.Snapshot() {
		if e.Role == RoleUser {
			t.Errorf("rejected submission echoed as a user row: %q", e.Text)
		}
	}
}

// TestLiveRender_DisconnectStopsTheSpinner — once the stream is gone
// no chunk is coming to close the stretch, so the disconnect has to.
// Reachable only now that a submit can leave a spinner running with
// no tokens ever having arrived.
func TestLiveRender_DisconnectStopsTheSpinner(t *testing.T) {
	cases := []struct {
		name string
		msg  func(gen uint64) tea.Msg
	}{
		{"iterator-returned", func(gen uint64) tea.Msg {
			return liveStreamEndedMsg{gen: gen}
		}},
		{"permanent-error", func(gen uint64) tea.Msg {
			return liveStreamErrMsg{gen: gen, err: errors.New("status 404: session not found")}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := liveRenderModelWith(t, nil, newInjectableLiveAgentStub())
			m, _ = liveSubmit(t, m, "do the thing")
			if !m.spinnerActive {
				t.Fatal("setup: expected a live stretch after the submit")
			}

			out, _ := m.Update(tc.msg(m.sessionGen))
			m = out.(Model)
			m.refreshViewport()

			if m.spinnerActive {
				t.Error("spinner still running after the stream went away")
			}
			if got := m.View().Content; strings.Contains(got, liveVerb) {
				t.Error("waiting indicator still on screen after disconnect")
			}
		})
	}
}
