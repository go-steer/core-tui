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
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// pauseRecorder is the Pauser half on its own, so it can be bolted
// onto a LiveAgent host and a per-turn one alike — which host shape a
// Pauser sits on decides what Enter does while held, so both have to
// be constructible.
type pauseRecorder struct {
	mu          sync.Mutex
	pauseCalls  []string
	resumeCalls []ResumeRequest
	state       PauseInfo
	pauseErr    error
	resumeErr   error
}

// pausableAgent implements Agent + LiveAgent + Pauser. Records every
// call so the tests can pin which disposition reached the host — the
// whole point of /continue vs /abandon is that they arrive as
// different modes.
type pausableAgent struct {
	liveAgentStub
	pauseRecorder
}

func (a *pauseRecorder) Pause(_ context.Context, reason string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pauseCalls = append(a.pauseCalls, reason)
	return a.pauseErr
}

func (a *pauseRecorder) Resume(_ context.Context, req ResumeRequest) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.resumeCalls = append(a.resumeCalls, req)
	return a.resumeErr
}

func (a *pauseRecorder) PauseState() PauseInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

func (a *pauseRecorder) setState(info PauseInfo) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = info
}

func (a *pauseRecorder) pauses() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.pauseCalls...)
}

func (a *pauseRecorder) resumes() []ResumeRequest {
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

// TestPauseBanner_IsBudgetedWhenTheGateMoves is the regression test
// for the banner eating the bottom of the screen. The banner's rows
// are charged in allocateChrome, which only runs from resize(), and
// the two paths that close the gate at runtime — the push event and
// the poll — did not call it. The frame then composed two rows over
// m.height and clipFrame took the footer and the lower half of the
// input box off the bottom, which reads as the banner covering the
// prompt.
//
// Driven entirely through Update, with no resize() of its own: the
// exact-grid cases in resize_budget_test.go call resize() in their
// setup, which is exactly the step that was missing in the live path.
func TestPauseBanner_IsBudgetedWhenTheGateMoves(t *testing.T) {
	const w, h = 100, 24

	for _, tc := range []struct {
		name string
		hold func(model) model
	}{
		{
			name: "push event",
			hold: func(m model) model {
				out, _ := m.Update(pauseEventMsg{gen: m.sessionGen, event: PauseEvent{
					State:       PauseStatePaused,
					Reason:      "operator interrupt",
					Interrupted: true,
				}})
				return out.(model)
			},
		},
		{
			name: "poll",
			hold: func(m model) model {
				out, _ := m.Update(hostSnapshotMsg{gen: m.sessionGen, snap: hostSnapshot{
					hasPause: true,
					pause: PauseInfo{
						Paused:      true,
						Reason:      "operator interrupt",
						Interrupted: true,
					},
				}})
				return out.(model)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(Options{Agent: &pausableAgent{}})
			out, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
			sized := out.(model)
			assertBudgetExact(t, sized)

			held := tc.hold(sized)
			if !held.pause.paused() {
				t.Fatal("the gate did not close")
			}
			banner := lipgloss.Height(held.renderPauseBanner(w))
			if banner == 0 {
				t.Fatal("no banner to budget")
			}
			if held.chrome.banner != banner {
				t.Errorf("chrome.banner = %d, want %d — allocateChrome did not re-run",
					held.chrome.banner, banner)
			}
			// composedRows is the height BEFORE clipFrame, which is
			// the only place the overflow is still visible — View()'s
			// output is clamped to h either way.
			assertBudgetExact(t, held)
			// And the consequence, stated in the terms the operator
			// reported it in: the footer is the last thing View
			// stacks, so it is the first thing an over-tall frame
			// loses, with the bottom of the input box behind it.
			if !strings.Contains(held.View().Content, "enter steers") {
				t.Error("the held footer is missing — the frame overflowed and clipFrame took it")
			}
		})
	}
}

// perTurnPausable is a Pauser on a host that does NOT drive its own
// loop: Agent.Run and nothing else. Deliberately no Events method —
// that is the distinction under test, and adding one would make
// core-tui bypass Run entirely.
type perTurnPausable struct {
	pauseRecorder

	runMu     sync.Mutex
	runPrompt string
	runCalls  int
}

func (a *perTurnPausable) Run(_ context.Context, prompt string) iter.Seq2[Event, error] {
	a.runMu.Lock()
	a.runPrompt = prompt
	a.runCalls++
	a.runMu.Unlock()
	return func(_ func(Event, error) bool) {}
}

func (a *perTurnPausable) ran() (string, int) {
	a.runMu.Lock()
	defer a.runMu.Unlock()
	return a.runPrompt, a.runCalls
}

// hasOneUserRow reports whether the transcript holds exactly one
// RoleUser row with this text — "exactly" because one failure mode of
// the split below is a steer appended twice, once by the held branch
// and once by submitTurn.
func hasOneUserRow(m model, text string) bool {
	n := 0
	for _, row := range m.history.Snapshot() {
		if row.Role == RoleUser && row.Text == text {
			n++
		}
	}
	return n == 1
}

// TestEnterWhilePaused_PerTurnHostRunsTheSteerItself. On a LiveAgent
// host the steer goes out as ResumeModeSteer and the host's own loop
// makes it the next turn, which the standing Events stream shows. A
// per-turn host has no standing stream: Agent.Run opens a subscription
// for the turn IT starts and closes it again, so a turn the HOST
// starts on a steer streams to nobody. The operator sees their prompt
// land and then silence — which is exactly what `-flavor attach`
// (per-turn) did, with the daemon's turn counter ticking the whole
// time.
//
// So on a per-turn host the gate opens with ResumeModeAbandon and the
// client runs the steer itself.
func TestEnterWhilePaused_PerTurnHostRunsTheSteerItself(t *testing.T) {
	agent := &perTurnPausable{}
	m := pausedModel(t, agent, true)
	if m.liveMode {
		t.Fatal("perTurnPausable was detected as a LiveAgent — it must not have an Events method")
	}
	const steer = "look at the retries instead"
	m.input.SetValue(steer)

	before := len(m.history.Snapshot())
	next, cmd := pressKey(m, tea.Key{Code: tea.KeyEnter})
	if _, calls := agent.ran(); calls != 0 {
		t.Fatal("the turn started before the gate was open — this is the awaitResume hang")
	}
	// No user row yet: submitTurn appends it when the resume lands,
	// and a row here would double it.
	if got := len(next.history.Snapshot()); got != before {
		t.Errorf("history grew by %d before the resume landed, want 0", got-before)
	}
	if got := next.input.Value(); got != "" {
		t.Errorf("input = %q after Enter, want it cleared", got)
	}

	msg := runCmd(t, cmd)
	done, ok := msg.(resumeDoneMsg)
	if !ok {
		t.Fatalf("Enter while held produced %T, want resumeDoneMsg", msg)
	}
	if done.mode != ResumeModeAbandon {
		t.Errorf("resume mode = %q, want %q — a steer the client will run itself must not "+
			"ALSO ask the host to run it", done.mode, ResumeModeAbandon)
	}
	if done.submit != steer {
		t.Errorf("resumeDoneMsg.submit = %q, want %q", done.submit, steer)
	}
	if calls := agent.resumes(); len(calls) != 1 || calls[0].Steer != "" {
		t.Errorf("Resume calls = %+v, want one abandon carrying no steer", calls)
	}

	out, _ := next.Update(done)
	ran := out.(model)
	prompt, calls := agent.ran()
	if calls != 1 || prompt != steer {
		t.Fatalf("Run called %d time(s) with %q, want once with the steer", calls, prompt)
	}
	if ran.state != stateStreaming {
		t.Errorf("state = %v after the steered turn started, want stateStreaming", ran.state)
	}
	if !hasOneUserRow(ran, steer) {
		t.Errorf("want exactly one user row carrying the steer, got %+v", ran.history.Snapshot())
	}
}

// TestEnterWhilePaused_PerTurnResumeFailureHandsTheTextBack. The box
// was cleared on the keystroke, but a refused resume means no turn is
// coming to carry the text. Losing it would make the operator retype
// a steer they already committed to.
func TestEnterWhilePaused_PerTurnResumeFailureHandsTheTextBack(t *testing.T) {
	agent := &perTurnPausable{}
	agent.resumeErr = errors.New("gate already open")
	m := pausedModel(t, agent, true)
	const steer = "look at the retries instead"
	m.input.SetValue(steer)

	next, cmd := pressKey(m, tea.Key{Code: tea.KeyEnter})
	out, _ := next.Update(runCmd(t, cmd))
	failed := out.(model)

	if got := failed.input.Value(); got != steer {
		t.Errorf("input = %q after a refused resume, want the steer back", got)
	}
	if got := lastText(failed); !strings.Contains(got, "gate already open") {
		t.Errorf("failed resume row = %q", got)
	}
	if _, calls := agent.ran(); calls != 0 {
		t.Error("a turn started even though the gate never opened")
	}
}
