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
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// pausableAgent implements Agent + Pauser. Records every call so the
// tests can pin which disposition reached the host — the whole point
// of /continue vs /abandon is that they arrive as different modes.
type pausableAgent struct {
	liveAgentStub

	mu          sync.Mutex
	pauseCalls  []string
	resumeCalls []ResumeRequest
	state       PauseInfo
	pauseErr    error
	resumeErr   error
}

func (a *pausableAgent) Pause(_ context.Context, reason string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pauseCalls = append(a.pauseCalls, reason)
	return a.pauseErr
}

func (a *pausableAgent) Resume(_ context.Context, req ResumeRequest) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.resumeCalls = append(a.resumeCalls, req)
	return a.resumeErr
}

func (a *pausableAgent) PauseState() PauseInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

func (a *pausableAgent) setState(info PauseInfo) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = info
}

func (a *pausableAgent) pauses() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.pauseCalls...)
}

func (a *pausableAgent) resumes() []ResumeRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]ResumeRequest(nil), a.resumeCalls...)
}

// pausedModel returns a model wired to agent and already holding, as
// if a pause event had landed.
func pausedModel(t *testing.T, agent Agent, interrupted bool) model {
	t.Helper()
	m := newModel(Options{Agent: agent})
	m.width, m.height = 100, 40
	next, changed := m.pause.applyEvent(PauseEvent{
		State:       PauseStatePaused,
		Interrupted: interrupted,
		At:          time.Unix(1700000000, 0),
	}, time.Unix(1700000000, 0))
	if !changed {
		t.Fatal("applyEvent(paused) reported no change on a fresh model")
	}
	m.pause = next
	return m
}

// runCmd runs a Cmd and returns the msg it produced, failing when the
// Cmd is nil — every path under test here is supposed to dispatch.
func runCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a Cmd, got nil")
	}
	return cmd()
}

// TestPauseEvent_EntersAndLeavesTheHeldState pins the primary attach
// path: the host's pause frame is what closes and opens the gate.
func TestPauseEvent_EntersAndLeavesTheHeldState(t *testing.T) {
	m := newModel(Options{Agent: &pausableAgent{}})
	m.width, m.height = 100, 40

	out, _ := m.Update(pauseEventMsg{gen: m.sessionGen, event: PauseEvent{
		State:       PauseStatePaused,
		Reason:      "operator interrupt",
		Interrupted: true,
	}})
	held := out.(model)
	if !held.pause.paused() {
		t.Fatal("pause event did not close the gate")
	}
	if !held.pause.Interrupted {
		t.Error("Interrupted did not survive the event")
	}
	if got := lastText(held); !strings.Contains(got, "Interrupted") {
		t.Errorf("held system row = %q, want it to lead with Interrupted", got)
	}
	if !strings.Contains(held.renderPauseBanner(80), "Interrupted") {
		t.Error("banner does not render the interrupted variant")
	}

	out, _ = held.Update(pauseEventMsg{gen: held.sessionGen, event: PauseEvent{
		State: PauseStateResumed,
		Mode:  ResumeModeContinue,
	}})
	free := out.(model)
	if free.pause.paused() {
		t.Fatal("resumed event did not open the gate")
	}
	if got := lastText(free); !strings.Contains(got, "carrying on") {
		t.Errorf("resumed system row = %q, want the continue wording", got)
	}
	if free.renderPauseBanner(80) != "" {
		t.Error("banner still renders after the gate opened")
	}
}

// TestPauseEvent_UnknownStateIsTolerated pins the spec §2.8
// forward-compat rule: a state this build doesn't know is a no-op, not
// a spurious transition.
func TestPauseEvent_UnknownStateIsTolerated(t *testing.T) {
	m := pausedModel(t, &pausableAgent{}, true)
	before := m.pause
	out, _ := m.Update(pauseEventMsg{gen: m.sessionGen, event: PauseEvent{State: "hibernating"}})
	if got := out.(model).pause; got.PauseInfo != before.PauseInfo {
		t.Errorf("unknown pause state changed the gate: %+v → %+v", before.PauseInfo, got.PauseInfo)
	}
}

// TestPausePoll_SettleWindowProtectsAFreshTransition is the
// reconciliation test. A poll sampled before a resume landed must not
// flip the banner back on; the same poll must win once the window has
// passed, or a client that missed an event stays wrong forever.
func TestPausePoll_SettleWindowProtectsAFreshTransition(t *testing.T) {
	base := time.Unix(1700000000, 0)
	resumed := pauseInfo{appliedAt: base}

	stale := PauseInfo{Paused: true, Reason: "operator interrupt"}
	if _, changed := resumed.applyPoll(stale, base.Add(pauseSettleWindow/2)); changed {
		t.Error("a poll inside the settle window overrode a just-applied resume")
	}
	next, changed := resumed.applyPoll(stale, base.Add(pauseSettleWindow+time.Millisecond))
	if !changed || !next.Paused {
		t.Errorf("poll past the settle window did not win: changed=%v paused=%v", changed, next.Paused)
	}

	// A poll that agrees with the model is never a transition, so it
	// must not restamp appliedAt — otherwise a 1s tick against a
	// steady state would hold the window open forever and no poll
	// could ever correct a missed event.
	steady := pauseInfo{PauseInfo: stale, appliedAt: base}
	if _, changed := steady.applyPoll(stale, base.Add(time.Hour)); changed {
		t.Error("an agreeing poll reported a change")
	}
}

// TestPausePoll_AttachToAnAlreadyPausedSession pins why PauseState
// exists at all: the event that closed the gate fired before this
// client connected, so the poll is the only way the banner ever
// appears.
func TestPausePoll_AttachToAnAlreadyPausedSession(t *testing.T) {
	agent := &pausableAgent{}
	agent.setState(PauseInfo{Paused: true, Reason: "operator interrupt", Interrupted: true})
	m := newModel(Options{Agent: agent})
	m.width, m.height = 100, 40

	snap := pullHostSnapshot(nil, nil, nil, agent)
	if !snap.hasPause || !snap.pause.Paused {
		t.Fatalf("pull did not carry the gate: %+v", snap)
	}
	out, _ := m.Update(hostSnapshotMsg{gen: m.sessionGen, snap: snap})
	if !out.(model).pause.paused() {
		t.Fatal("host snapshot did not apply the already-paused state")
	}
}

// TestEnterWhilePaused_SteersRatherThanSubmitting is the regression
// net on the hang. Routing typed text through submitTurn while the
// host's gate is shut means Agent.Run blocks in awaitResume before it
// does anything — the spinner would run against a gate only this
// keystroke could have opened.
func TestEnterWhilePaused_SteersRatherThanSubmitting(t *testing.T) {
	agent := &pausableAgent{}
	m := pausedModel(t, agent, true)
	m.input.SetValue("look at the retries instead")

	next, cmd := pressKey(m, tea.Key{Code: tea.KeyEnter})
	if agent.runCalled {
		t.Fatal("Enter while held started a turn — this is the awaitResume hang")
	}
	msg := runCmd(t, cmd)
	done, ok := msg.(resumeDoneMsg)
	if !ok {
		t.Fatalf("Enter while held produced %T, want resumeDoneMsg", msg)
	}
	if done.mode != ResumeModeSteer {
		t.Errorf("resume mode = %q, want %q", done.mode, ResumeModeSteer)
	}
	calls := agent.resumes()
	if len(calls) != 1 || calls[0].Steer != "look at the retries instead" {
		t.Fatalf("Resume calls = %+v, want one carrying the typed text", calls)
	}
	// The operator has to see what they sent: the host's event stream
	// may never echo a steer back.
	if got := lastText(next); got != "look at the retries instead" {
		t.Errorf("steer was not rendered as a user row; last row = %q", got)
	}
	if next.input.Value() != "" {
		t.Errorf("input not cleared after steering; got %q", next.input.Value())
	}
}

// TestEnterWhilePaused_NoPauserFallsThrough pins that the paused
// branch can't strand a host that never wired the capability. Only a
// Pauser can produce a pause in the first place, but the branch reads
// m.pause and Options.Agent independently, so the degrade path is
// worth a test rather than an argument.
func TestEnterWhilePaused_NoPauserFallsThrough(t *testing.T) {
	agent := &liveAgentStub{}
	m := pausedModel(t, agent, false)
	m.liveMode = true
	m.input.SetValue("hello")

	next, _ := pressKey(m, tea.Key{Code: tea.KeyEnter})
	// The LiveAgent read-only note is the expected outcome — what
	// matters is that nothing panicked and the text didn't vanish
	// without explanation.
	if got := lastText(next); !strings.Contains(got, "Read-only view") {
		t.Errorf("expected the read-only degrade row, got %q", got)
	}
}

// TestSlashContinue_And_Abandon_CarryDistinctModes. The two commands
// differ only in disposition, and the disposition is the entire
// meaning: continue wakes the loop, abandon drops the work.
func TestSlashContinue_And_Abandon_CarryDistinctModes(t *testing.T) {
	for _, tc := range []struct {
		slash string
		want  string
	}{
		{"continue", ResumeModeContinue},
		{"cont", ResumeModeContinue},
		{"abandon", ResumeModeAbandon},
	} {
		t.Run(tc.slash, func(t *testing.T) {
			agent := &pausableAgent{}
			m := pausedModel(t, agent, true)
			handled, _, cmd := m.dispatchBuiltinSlash(tc.slash, "")
			if !handled {
				t.Fatalf("/%s not handled by the built-in dispatcher", tc.slash)
			}
			runCmd(t, cmd)
			calls := agent.resumes()
			if len(calls) != 1 || calls[0].Mode != tc.want {
				t.Fatalf("/%s produced %+v, want one Resume with mode %q", tc.slash, calls, tc.want)
			}
			if calls[0].Steer != "" {
				t.Errorf("/%s carried a steer payload: %q", tc.slash, calls[0].Steer)
			}
		})
	}
}

// TestSlashResume_StillLoadsTranscripts guards the collision that
// named /continue in the first place (issue #268): /resume belongs to
// the saved-session loader and must not have been quietly repurposed.
func TestSlashResume_StillLoadsTranscripts(t *testing.T) {
	agent := &pausableAgent{}
	m := pausedModel(t, agent, true)
	handled, _, _ := m.dispatchBuiltinSlash("resume", "")
	if !handled {
		t.Fatal("/resume not handled")
	}
	if got := agent.resumes(); len(got) != 0 {
		t.Fatalf("/resume reached the pause gate: %+v", got)
	}
}

// TestSlashPause_HoldsWithoutCancelling. /pause and /interrupt are
// different intents — "stop after this" vs "stop now" — and only the
// latter touches the running turn.
func TestSlashPause_HoldsWithoutCancelling(t *testing.T) {
	agent := &pausableAgent{}
	m := newModel(Options{Agent: agent})
	m.state = stateStreaming
	cancelled := false
	m.cancelTurn = func() { cancelled = true }

	handled, _, cmd := m.dispatchBuiltinSlash("pause", "reviewing the plan")
	if !handled {
		t.Fatal("/pause not handled")
	}
	runCmd(t, cmd)
	if cancelled {
		t.Error("/pause cancelled the in-flight turn; it should let it finish")
	}
	if got := agent.pauses(); len(got) != 1 || got[0] != "reviewing the plan" {
		t.Fatalf("Pause calls = %q, want one carrying the typed reason", got)
	}
}

// TestPauseSlashes_DegradeWithoutTheCapability pins the standard
// optional-capability contract: a system row, no panic, no Cmd.
func TestPauseSlashes_DegradeWithoutTheCapability(t *testing.T) {
	for _, slash := range []string{"pause", "continue", "abandon"} {
		t.Run(slash, func(t *testing.T) {
			m := newModel(Options{Agent: &liveAgentStub{}})
			handled, next, cmd := m.dispatchBuiltinSlash(slash, "")
			if !handled {
				t.Fatalf("/%s not handled", slash)
			}
			if cmd != nil {
				t.Errorf("/%s dispatched a Cmd without a Pauser", slash)
			}
			if got := lastText(next.(model)); !strings.Contains(got, "Pauser") {
				t.Errorf("/%s degrade row = %q, want it to name the missing capability", slash, got)
			}
		})
	}
}

// TestResumeSlashes_RefuseWhenNotHeld. /continue against an agent
// that is not held would open a gate nobody closed; saying so beats
// a round trip that reads as a no-op.
func TestResumeSlashes_RefuseWhenNotHeld(t *testing.T) {
	agent := &pausableAgent{}
	m := newModel(Options{Agent: agent})
	handled, next, cmd := m.dispatchBuiltinSlash("continue", "")
	if !handled {
		t.Fatal("/continue not handled")
	}
	if cmd != nil {
		t.Error("/continue dispatched a Resume against an agent that isn't held")
	}
	if got := lastText(next.(model)); !strings.Contains(got, "isn't held") {
		t.Errorf("row = %q, want the not-held explanation", got)
	}
}

// TestEsc_ArmsTheHoldWithNoTurnInFlight is the headline behaviour
// change (R-HOLD-1). Observer mode between daemon-driven turns is
// exactly when an operator wants to get ahead of the next one, and
// that is the case where the old cascade did nothing at all.
func TestEsc_ArmsTheHoldWithNoTurnInFlight(t *testing.T) {
	agent := &pausableAgent{}
	m := newModel(Options{Agent: agent})
	m.width, m.height = 100, 40
	m.liveMode = true

	_, cmd := pressKey(m, tea.Key{Code: tea.KeyEscape})
	msg := runCmd(t, cmd)
	if _, ok := msg.(pauseDoneMsg); !ok {
		t.Fatalf("esc at idle produced %T, want pauseDoneMsg", msg)
	}
	if got := agent.pauses(); len(got) != 1 {
		t.Fatalf("Pause calls = %q, want exactly one", got)
	}
}

// TestEsc_CancelsLocallyThenHolds pins the ordering. The local cancel
// is instant and unwedges the stack; the hold is what stops the
// scheduler starting the next turn, which is what esc means.
func TestEsc_CancelsLocallyThenHolds(t *testing.T) {
	agent := &pausableAgent{}
	m := newModel(Options{Agent: agent})
	m.width, m.height = 100, 40
	m.state = stateStreaming
	cancelled := false
	m.cancelTurn = func() { cancelled = true }

	_, cmd := pressKey(m, tea.Key{Code: tea.KeyEscape})
	if !cancelled {
		t.Error("esc did not fire the local cancel")
	}
	runCmd(t, cmd)
	if got := agent.pauses(); len(got) != 1 {
		t.Fatalf("Pause calls = %q, want the hold to follow the local cancel", got)
	}
}

// TestEsc_WhileHeldDismissesWithoutResuming. Esc backs out of things,
// and an operator pressing it twice means "I've read that" — not
// "never mind, carry on". A reflexive keypress must not unleash an
// agent they just stopped.
func TestEsc_WhileHeldDismissesWithoutResuming(t *testing.T) {
	agent := &pausableAgent{}
	m := pausedModel(t, agent, true)

	next, cmd := pressKey(m, tea.Key{Code: tea.KeyEscape})
	if cmd != nil {
		t.Errorf("esc while held dispatched %T", cmd())
	}
	if got := agent.resumes(); len(got) != 0 {
		t.Fatalf("esc while held resumed the agent: %+v", got)
	}
	if !next.pause.paused() {
		t.Error("esc while held opened the gate")
	}
	if next.renderPauseBanner(80) != "" {
		t.Error("banner still renders after dismissal")
	}
	// Dismissing hides the explanation, not the state.
	if !strings.Contains(next.renderFooter(80), "Held") {
		t.Error("footer stopped reporting the hold after the banner was dismissed")
	}
}

// TestPauseBanner_CountsRunningSubagents. A hold stops the main loop;
// an operator who reads that as "everything stopped" is wrong in a way
// that costs money.
func TestPauseBanner_CountsRunningSubagents(t *testing.T) {
	m := pausedModel(t, &pausableAgent{}, true)
	m.hostSnap = hostSnapshot{
		valid:        true,
		hasSubagents: true,
		subagents: []SubagentInfo{
			{Name: "watcher", Status: "running"},
			{Name: "auditor", Status: "done"},
			{Name: "prober", Status: "Running"},
		},
	}
	banner := m.renderPauseBanner(100)
	if !strings.Contains(banner, "2 background subagents still running") {
		t.Errorf("banner did not count the running subagents:\n%s", banner)
	}
}

// TestPauseAndResumeErrors_SurfaceLoudly. A failed hold is the case
// the operator most needs told: they are about to assume an agent is
// parked when it isn't.
func TestPauseAndResumeErrors_SurfaceLoudly(t *testing.T) {
	m := newModel(Options{Agent: &pausableAgent{}})

	out, _ := m.Update(pauseDoneMsg{err: errors.New("connection refused")})
	if got := lastText(out.(model)); !strings.Contains(got, "connection refused") {
		t.Errorf("failed hold row = %q", got)
	}

	out, _ = m.Update(resumeDoneMsg{mode: ResumeModeContinue, err: errors.New("gate already open")})
	got := lastText(out.(model))
	if !strings.Contains(got, "gate already open") || !strings.Contains(got, ResumeModeContinue) {
		t.Errorf("failed resume row = %q, want the error and the mode", got)
	}
}

// TestPauseSuccess_StaysQuiet. The banner is the confirmation and the
// host's pause event appends the row; a second one on the ack would
// double up.
func TestPauseSuccess_StaysQuiet(t *testing.T) {
	m := newModel(Options{Agent: &pausableAgent{}})
	before := len(m.history.Snapshot())
	out, cmd := m.Update(pauseDoneMsg{})
	if cmd != nil {
		t.Error("a successful hold dispatched a follow-up Cmd")
	}
	after := out.(model)
	if got := len(after.history.Snapshot()); got != before {
		t.Errorf("history grew by %d on a successful hold", got-before)
	}
}
