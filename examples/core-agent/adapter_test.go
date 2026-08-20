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
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-steer/core-tui/examples/core-agent/fakehost"
	"github.com/go-steer/core-tui/tui"
)

// turn drains an event iterator into a slice, failing on the first
// error and on an unreasonable wait.
func turn(t *testing.T, seq iter.Seq2[tui.Event, error]) []tui.Event {
	t.Helper()
	var out []tui.Event
	for ev, err := range seq {
		if err != nil {
			t.Fatalf("event stream: %v", err)
		}
		out = append(out, ev)
	}
	return out
}

// assertTurnShape checks the properties the renderer depends on,
// independent of which flavor produced the events. Both adapters run
// through it — that both satisfy the same contract identically is
// the claim I-CORE-AGENT makes.
func assertTurnShape(t *testing.T, evs []tui.Event) {
	t.Helper()
	if len(evs) == 0 {
		t.Fatal("no events")
	}

	var partials, commits int
	var calls []tui.ToolCall
	var results []tui.ToolResult
	var usage *tui.Usage
	for _, ev := range evs {
		if ev.Partial {
			partials++
		} else if ev.Text != "" {
			commits++
		}
		if ev.Model == "" {
			t.Error("every event should carry the resolved model name")
		}
		calls = append(calls, ev.ToolCalls...)
		results = append(results, ev.ToolResults...)
		if ev.Usage != nil {
			usage = ev.Usage
		}
	}

	if partials == 0 {
		t.Error("expected streamed partials")
	}
	if commits != 1 {
		t.Errorf("expected exactly one committed (non-partial) text event, got %d", commits)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(calls))
	}
	if calls[0].Name != "read_file" || calls[0].Args["path"] != "deploy/api.yaml" {
		t.Errorf("tool call args did not survive translation: %+v", calls[0])
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 tool results, got %d", len(results))
	}

	// IDs must correlate, or the renderer cannot attach a result to
	// its call row.
	for i := range calls {
		if calls[i].ID != results[i].ID {
			t.Errorf("result %d: ID %q does not match call ID %q", i, results[i].ID, calls[i].ID)
		}
	}
	// The host packs latency into the response map; core-tui plucks
	// it, so the adapter must not strip it.
	if _, ok := results[0].Response["latency_ms"]; !ok {
		t.Error("latency_ms was dropped from the tool response")
	}
	if results[0].Error != "" {
		t.Errorf("successful call reported an error: %q", results[0].Error)
	}
	// The host's in-band "error" key has to become the Error field.
	if !strings.Contains(results[1].Error, "could not find") {
		t.Errorf("in-band error was not split out of the response map: %+v", results[1])
	}
	if _, ok := results[1].Response["error"]; ok {
		t.Error("the error key should not survive in the response map")
	}
	if usage == nil || usage.InputTokens == 0 || usage.OutputTokens == 0 {
		t.Errorf("usage did not reach the TUI: %+v", usage)
	}
}

func TestLocalAdapterRunTranslatesTurn(t *testing.T) {
	agent := fakehost.NewAgent("")
	a := &localAdapter{inner: agent}

	evs := turn(t, a.Run(t.Context(), "check the api deployment"))
	assertTurnShape(t, evs)

	var cost float64
	for _, ev := range evs {
		if ev.CostUSD > 0 {
			cost = ev.CostUSD
		}
	}
	if cost <= 0 {
		t.Error("expected the host-computed per-turn cost on the usage event")
	}
}

func TestLocalAdapterCapabilities(t *testing.T) {
	agent := fakehost.NewAgent("gemini-3.1-pro")
	a := &localAdapter{inner: agent}

	if got := a.Status(); got.ModelName != "gemini-3.1-pro" || got.Provider != "gemini" {
		t.Errorf("Status() = %+v", got)
	}
	if len(a.Tools()) != 5 {
		t.Errorf("Tools() returned %d entries", len(a.Tools()))
	}
	if got := a.Tools()[0]; got.GateState == "" {
		t.Error("ToolLister must report the gate disposition")
	}
	if len(a.Subagents()) != 2 {
		t.Errorf("Subagents() returned %d entries", len(a.Subagents()))
	}
	if len(a.SessionApprovals()) != 3 {
		t.Errorf("SessionApprovals() returned %d rows", len(a.SessionApprovals()))
	}
	if err := a.AddAllowPatterns([]string{"bash:git *"}); err != nil {
		t.Errorf("AddAllowPatterns: %v", err)
	}
	if err := a.AddBuiltinAllowExtra("nope"); err == nil {
		t.Error("an unknown allow bundle should be an error")
	}

	// SwitchModel hands back a whole new tui.Agent, not a name.
	next, err := a.SwitchModel("claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("SwitchModel: %v", err)
	}
	swapped, ok := next.(*localAdapter)
	if !ok {
		t.Fatalf("SwitchModel returned %T, want *localAdapter", next)
	}
	if got := swapped.Status(); got.ModelName != "claude-sonnet-4-6" || got.Provider != "anthropic" {
		t.Errorf("after swap Status() = %+v", got)
	}
	if _, err := a.SwitchModel("gpt-hallucination-9"); err == nil {
		t.Error("an unknown model should be an error")
	}

	res, err := a.Reload(t.Context())
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if res.Agent == nil || res.Note == "" || len(res.MCPServers) == 0 {
		t.Errorf("ReloadResult under-populated: %+v", res)
	}

	if _, err := a.Refresh(t.Context()); err != nil {
		t.Errorf("Refresh: %v", err)
	}
	summary, err := a.Set("gemini-3.1-pro", 1.25, 10)
	if err != nil || !strings.Contains(summary, "gemini-3.1-pro") {
		t.Errorf("Set() = %q, %v", summary, err)
	}
}

func TestLocalAdapterInboxRoundTrip(t *testing.T) {
	a := &localAdapter{inner: fakehost.NewAgent("")}

	if got := a.PendingInboxCount(); got != 0 {
		t.Fatalf("fresh inbox has %d entries", got)
	}
	if err := a.Inject("also check the canary namespace"); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if err := a.Inject("   "); err == nil {
		t.Error("an empty injection should be rejected")
	}
	if got := a.PendingInboxCount(); got != 1 {
		t.Fatalf("PendingInboxCount() = %d, want 1", got)
	}
	drained := a.DrainInbox()
	if len(drained) != 1 || drained[0] != "also check the canary namespace" {
		t.Fatalf("DrainInbox() = %v", drained)
	}
	if got := a.PendingInboxCount(); got != 0 {
		t.Errorf("DrainInbox must empty the inbox, %d left", got)
	}
}

func TestLocalAdapterSlashSideAnswer(t *testing.T) {
	a := &localAdapter{inner: fakehost.NewAgent("")}

	if len(a.SlashCommands()) == 0 {
		t.Fatal("SlashProvider advertised no commands")
	}
	res, err := a.InvokeSlash(t.Context(), "btw", "what changed in prod?")
	if err != nil {
		t.Fatalf("InvokeSlash: %v", err)
	}
	if res.ModalAnswer == nil || res.ModalAnswer.Question != "what changed in prod?" {
		t.Fatalf("SlashResult = %+v", res)
	}
	if _, err := a.InvokeSlash(t.Context(), "nope", ""); err == nil {
		t.Error("an unknown command should be an error")
	}

	// The wake signal has to reach the channel core-tui subscribes to.
	select {
	case <-a.WakeRequested():
		t.Fatal("wake fired before it was requested")
	default:
	}
	if _, err := a.InvokeSlash(t.Context(), "wake", ""); err != nil {
		t.Fatalf("/wake: %v", err)
	}
	select {
	case <-a.WakeRequested():
	case <-time.After(time.Second):
		t.Error("wake signal never arrived")
	}
}

func TestLocalUsageTracker(t *testing.T) {
	agent := fakehost.NewAgent("")
	a := &localAdapter{inner: agent}
	u := &localUsage{inner: agent}

	if u.SessionTurns() != 0 {
		t.Fatalf("fresh session reports %d turns", u.SessionTurns())
	}
	turn(t, a.Run(t.Context(), "hello"))

	if u.SessionTurns() != 1 {
		t.Errorf("SessionTurns() = %d, want 1", u.SessionTurns())
	}
	totals := u.SessionTotals()
	if totals.InputTokens == 0 || totals.OutputTokens == 0 {
		t.Errorf("SessionTotals() = %+v", totals)
	}
	if u.SessionCostUSD() <= 0 {
		t.Error("SessionCostUSD() did not accumulate")
	}
	last, cost := u.LastTurn()
	if last != totals || cost <= 0 {
		t.Errorf("LastTurn() = %+v, %v after a single turn", last, cost)
	}
	if u.ContextWindowUsed() == 0 || u.ContextWindowUsed() >= u.ContextWindowSize() {
		t.Errorf("context window: used %d of %d", u.ContextWindowUsed(), u.ContextWindowSize())
	}
	if u.SessionDuration() <= 0 {
		t.Error("SessionDuration() did not advance")
	}
}

// startDaemon boots the toy daemon and returns an adapter attached
// to it, torn down when the test ends.
func startDaemon(t *testing.T) *attachAdapter {
	t.Helper()
	d, err := fakehost.StartDaemon(fakehost.NewAgent(""))
	if err != nil {
		t.Fatalf("StartDaemon: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return newAttachAdapter(fakehost.NewClient(d.URL()), fakehost.SessionPath)
}

func TestAttachAdapterRunOverHTTP(t *testing.T) {
	a := startDaemon(t)

	// Same assertions as the local flavor, on events that crossed a
	// socket: the two adapters are interchangeable from core-tui's
	// point of view.
	assertTurnShape(t, turn(t, a.Run(t.Context(), "check the api deployment")))

	if got := a.Status(); got.ModelName == "" || got.State == "" {
		t.Errorf("Status() = %+v", got)
	}
	if a.SessionTurns() != 1 {
		t.Errorf("SessionTurns() = %d, want 1", a.SessionTurns())
	}
	if len(a.Tools()) != 5 {
		t.Errorf("Tools() returned %d entries", len(a.Tools()))
	}
	if len(a.Subagents()) != 2 {
		t.Errorf("Subagents() returned %d entries", len(a.Subagents()))
	}
}

func TestAttachObserverEventsSeeInjectedTurn(t *testing.T) {
	a := startDaemon(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	type result struct {
		text  string
		calls int
	}
	done := make(chan result, 1)
	go func() {
		var r result
		for ev, err := range (attachObserver{attachAdapter: a}).Events(ctx) {
			if err != nil {
				continue // transport hiccup; the iterator keeps going
			}
			r.calls += len(ev.ToolCalls)
			if !ev.Partial && ev.Text != "" {
				r.text = ev.Text
				done <- r
				return
			}
		}
		done <- r
	}()

	// Give the observer a moment to subscribe, then drive the daemon
	// from "somewhere else" — the case observer mode exists for.
	time.Sleep(200 * time.Millisecond)
	if err := a.Inject("what is prod doing?"); err != nil {
		t.Fatalf("Inject: %v", err)
	}

	select {
	case r := <-done:
		if r.calls != 2 {
			t.Errorf("observer saw %d tool calls, want 2", r.calls)
		}
		if !strings.Contains(r.text, "replicas") {
			t.Errorf("observer missed the committed text: %q", r.text)
		}
	case <-ctx.Done():
		t.Fatal("observer never saw the turn")
	}
}

// TestAttachPauseHoldsTheLoopAndSteers is the operator hold end to
// end: the gate goes up, the stream says so, a steer opens it, and
// the turn that follows is the steered one rather than whatever the
// daemon was about to do.
//
// The steer arriving as a turn is the part worth pinning. It is what
// separates a hold from a cancel — an operator who is told "no new
// turn starts until you resume" has to be able to make the next turn
// the one they wanted.
func TestAttachPauseHoldsTheLoopAndSteers(t *testing.T) {
	a := startDaemon(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	type seen struct {
		states []string
		text   string
	}
	done := make(chan seen, 1)
	go func() {
		var s seen
		for ev, err := range (attachObserver{attachAdapter: a}).Events(ctx) {
			if err != nil {
				continue
			}
			if ev.Pause != nil {
				s.states = append(s.states, ev.Pause.State)
				continue
			}
			if !ev.Partial && ev.Text != "" {
				s.text = ev.Text
				done <- s
				return
			}
		}
		done <- s
	}()
	time.Sleep(200 * time.Millisecond)

	if err := a.Pause(ctx, "operator wants a word"); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if got := a.PauseState(); !got.Paused || got.Reason != "operator wants a word" {
		t.Fatalf("PauseState() = %+v, want the gate shut with the reason carried", got)
	}
	// A turn started while held must not produce anything: Run blocks
	// on the gate before it spends a token. This is the hang the TUI
	// avoids by routing enter to Resume instead of submitting.
	if err := a.Inject("this should wait"); err != nil {
		t.Fatalf("Inject while held: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if a.SessionTurns() != 0 {
		t.Errorf("a turn ran while the agent was held: SessionTurns() = %d", a.SessionTurns())
	}

	if err := a.Resume(ctx, tui.ResumeRequest{
		Mode:  tui.ResumeModeSteer,
		Steer: "forget that, check the database instead",
	}); err != nil {
		t.Fatalf("Resume(steer): %v", err)
	}

	select {
	case s := <-done:
		if len(s.states) != 2 ||
			s.states[0] != fakehost.PauseStatePaused ||
			s.states[1] != fakehost.PauseStateResumed {
			t.Errorf("observer saw pause states %q, want paused then resumed", s.states)
		}
		if !strings.Contains(s.text, "replicas") {
			t.Errorf("the steered turn never landed: %q", s.text)
		}
	case <-ctx.Done():
		t.Fatal("the steer never produced a turn")
	}

	// The parked turn adopted the steer; it did not run alongside a
	// second one started for it.
	time.Sleep(300 * time.Millisecond)
	if got := a.SessionTurns(); got != 1 {
		t.Errorf("SessionTurns() = %d after one steer, want 1", got)
	}
}

func TestAttachSubagentEvents(t *testing.T) {
	a := startDaemon(t)

	page, err := a.SubagentEvents(t.Context(), "security-review", 0)
	if err != nil {
		t.Fatalf("SubagentEvents: %v", err)
	}
	if len(page.Events) != 4 {
		t.Fatalf("got %d turns, want 4", len(page.Events))
	}
	if page.NextSince != 4 {
		t.Errorf("NextSince = %d, want 4", page.NextSince)
	}
	if len(page.Events[1].ToolCalls) != 1 || page.Events[1].ToolCalls[0].Name != "grep" {
		t.Errorf("subagent tool call did not survive translation: %+v", page.Events[1])
	}

	// The cursor has to actually page.
	rest, err := a.SubagentEvents(t.Context(), "security-review", page.NextSince)
	if err != nil {
		t.Fatalf("SubagentEvents(since=%d): %v", page.NextSince, err)
	}
	if len(rest.Events) != 0 {
		t.Errorf("resuming from the end returned %d turns", len(rest.Events))
	}

	// "No such subagent" must be distinguishable from "did nothing",
	// and must survive the wire as the typed error.
	_, err = a.SubagentEvents(t.Context(), "typo-hunter", 0)
	var notFound *tui.SubagentNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("got %T (%v), want *tui.SubagentNotFoundError", err, err)
	}
	if len(notFound.Available) != 2 {
		t.Errorf("Available = %v, want the two real subagents", notFound.Available)
	}
}

func TestAttachSessionSwitcher(t *testing.T) {
	a := startDaemon(t)

	sessions := a.Sessions()
	if len(sessions) != 3 {
		t.Fatalf("Sessions() returned %d rows, want 2 sessions + 1 action row", len(sessions))
	}
	if !sessions[0].Current {
		t.Error("the attached session should be marked current")
	}

	action := sessions[len(sessions)-1]
	if action.Input == nil {
		t.Fatal("the last row should be the type-in-an-endpoint action row")
	}
	if action.Input.Validate("") == "" {
		t.Error("an empty endpoint should not validate")
	}
	target, err := action.Input.Submit("http://127.0.0.1:9999")
	if err != nil {
		t.Fatalf("action row Submit: %v", err)
	}
	if target.Agent == nil || target.UsageTracker == nil {
		t.Errorf("SwitchTarget under-populated: %+v", target)
	}

	if _, err := a.SwitchToSession(fakehost.SessionPath); err == nil {
		t.Error("switching to the current session should be an error")
	}
	other, err := a.SwitchToSession("/sessions/core-agent/incident-4471")
	if err != nil {
		t.Fatalf("SwitchToSession: %v", err)
	}
	if other.Agent == nil {
		t.Error("SwitchTarget.Agent is required")
	}
}

func TestAttachAsyncSlashAndInterrupt(t *testing.T) {
	a := startDaemon(t)

	preamble, results := a.InvokeSlashAsync(t.Context(), "btw", "what changed?")
	if preamble == "" {
		t.Error("attach commands are slow enough to owe the operator a dispatch-time row")
	}
	select {
	case got := <-results:
		if got.Err != nil {
			t.Fatalf("/btw: %v", got.Err)
		}
		if got.Res.ModalAnswer == nil || !strings.Contains(got.Res.ModalAnswer.Answer, "what changed?") {
			t.Errorf("SlashResult = %+v", got.Res)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("/btw never answered")
	}

	if _, unknown := a.InvokeSlashAsync(t.Context(), "reload", ""); unknown == nil {
		t.Error("an unknown command should still deliver on the channel")
	}

	// Nothing running, and the interrupt still succeeds: it holds
	// the loop. That is the 1.5.0 behaviour the operator wants —
	// esc between two daemon-driven turns has to stop the next one,
	// not report that it was a moment too late.
	if err := a.Interrupt(t.Context()); err != nil {
		t.Errorf("Interrupt on an idle session: %v", err)
	}
	// Interrupted stays false: nothing was killed, because nothing
	// was running. That distinction is the whole reason the field
	// exists — the banner asks a different question in each case.
	if got := a.PauseState(); !got.Paused || got.Interrupted || got.Reason == "" {
		t.Errorf("PauseState() = %+v after interrupting an idle session, want the "+
			"gate shut with a reason and nothing reported killed", got)
	}
	if err := a.Resume(t.Context(), tui.ResumeRequest{Mode: tui.ResumeModeAbandon}); err != nil {
		t.Fatalf("Resume(abandon): %v", err)
	}
	if got := a.PauseState(); got.Paused {
		t.Errorf("PauseState() = %+v after a resume, want the gate open", got)
	}
}

// TestAttachPerTurnSteerIsRunByTheClient is the per-turn half of the
// hold, and the one that shipped broken: this flavor is the bare
// adapter, so there is no standing Events stream, and Run's
// subscription only exists for the duration of a turn Run itself
// started. A steer that asked the DAEMON to run the turn produced a
// turn nobody was watching — the daemon's counter ticked and the
// operator's screen stayed empty.
//
// So core-tui opens the gate with abandon and calls Run. That is what
// this walks: hold, resume-with-nothing, run the operator's text, see
// the whole turn.
func TestAttachPerTurnSteerIsRunByTheClient(t *testing.T) {
	a := startDaemon(t)
	ctx := t.Context()

	if err := a.Pause(ctx, "operator interrupt"); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if err := a.Resume(ctx, tui.ResumeRequest{Mode: tui.ResumeModeAbandon}); err != nil {
		t.Fatalf("Resume(abandon): %v", err)
	}

	// Run has to see an OPEN gate, or it blocks in the daemon's
	// awaitResume — which is why the resume is a separate round trip
	// that has to land first, rather than something batched with it.
	// Drop the pause frames the cursor replays into this
	// subscription: they are gate transitions, not turn events, and
	// assertTurnShape speaks for the turn.
	var evs []tui.Event
	for _, ev := range turn(t, a.Run(ctx, "look at the retries instead")) {
		if ev.Pause == nil {
			evs = append(evs, ev)
		}
	}
	assertTurnShape(t, evs)
	if got := a.SessionTurns(); got != 1 {
		t.Errorf("SessionTurns() = %d, want exactly 1 — the client ran the steer and "+
			"the daemon must not have run one of its own", got)
	}
}

// TestAttachInterruptStopsTheTurnOverHTTP is the operator's path end
// to end, and the check whose absence let #280 ship: esc holds, and a
// hold on its own is a promise about the NEXT turn. Nothing here ever
// asked whether Interrupt had any effect on the turn already running,
// so the TUI could stop calling it and every test stayed green while
// the daemon carried on working under a banner saying it had stopped.
func TestAttachInterruptStopsTheTurnOverHTTP(t *testing.T) {
	a := startDaemon(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	type seen struct {
		texts []string
	}
	first := make(chan struct{})
	done := make(chan seen, 1)
	go func() {
		var s seen
		var once sync.Once
		for ev, err := range (attachObserver{attachAdapter: a}).Events(ctx) {
			if err != nil {
				continue // transport hiccup; the iterator keeps going
			}
			if ev.Pause != nil {
				continue
			}
			// Any frame at all means the turn is under way — the
			// committed text only lands at the end, which is far too
			// late to interrupt.
			once.Do(func() { close(first) })
			if !ev.Partial && ev.Text != "" {
				s.texts = append(s.texts, ev.Text)
				if strings.Contains(ev.Text, "interrupted by operator") {
					done <- s
					return
				}
			}
		}
		done <- s
	}()

	time.Sleep(200 * time.Millisecond)
	if err := a.Inject("what is prod doing?"); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	select {
	case <-first:
	case <-ctx.Done():
		t.Fatal("the injected turn never streamed anything to interrupt")
	}

	if err := a.Interrupt(ctx); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}

	select {
	case s := <-done:
		for _, text := range s.texts {
			if strings.Contains(text, "replicas") {
				t.Errorf("the turn reached its committed answer anyway: %q", text)
			}
		}
	case <-ctx.Done():
		t.Fatal("the daemon ran the turn to completion after the interrupt")
	}

	// And the gate is shut behind it, with the bit that says work was
	// killed — the two halves of one gesture, which is what the TUI
	// now sends as one.
	if got := a.PauseState(); !got.Paused || !got.Interrupted {
		t.Errorf("PauseState() = %+v, want the gate shut and the kill reported", got)
	}
}
