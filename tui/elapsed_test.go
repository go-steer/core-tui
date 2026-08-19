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

// Turn elapsed-time readout on the thinking line (issue #111).
//
// The output is a function of wall-clock time, so none of this is
// goldened. Every test pins model.now to a controllable clock and
// moves it by hand — the same technique the spinner-generation tests
// use for the tick chain, for the same reason: a test that waits out
// real time is a test that flakes.

package tui

import (
	"strings"
	"testing"
	"time"
)

// fixedClock returns a clock function reading from a caller-owned
// time.Time, so a test can advance it between renders.
func fixedClock(at *time.Time) func() time.Time {
	return func() time.Time { return *at }
}

// elapsedModel returns a streaming model whose clock the caller
// drives via the returned pointer. The turn is submitted at the
// clock's initial value, so advancing it is the elapsed time.
func elapsedModel(t *testing.T, at *time.Time) model {
	t.Helper()
	m := newModel(Options{Agent: stubAgent{}})
	m.now = fixedClock(at)
	m.viewport.SetWidth(80)
	m = m.submitTurn("how long is this going to take")
	if m.state != stateStreaming {
		t.Fatalf("setup: state = %v, want stateStreaming", m.state)
	}
	if m.turnStarted.IsZero() {
		t.Fatal("setup: submitTurn left turnStarted at the zero value")
	}
	return m
}

// TestFormatTurnElapsed_Boundaries pins the shape across the
// second / minute / hour bands, including the zero-padding that
// keeps the line from shifting a column mid-turn.
func TestFormatTurnElapsed_Boundaries(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "0s"},
		{999 * time.Millisecond, "0s"},  // sub-second truncates, never rounds up
		{1500 * time.Millisecond, "1s"}, // ... and never shows a decimal
		{3 * time.Second, "3s"},         // the floor, the first value shown
		{12 * time.Second, "12s"},       // issue #111's example
		{59*time.Second + 999*time.Millisecond, "59s"},
		{time.Minute, "1m00s"},                 // band change, zero-padded minor unit
		{time.Minute + 4*time.Second, "1m04s"}, // 1m04s, not 1m4s — stable width
		{time.Minute + 10*time.Second, "1m10s"},
		{9*time.Minute + 59*time.Second, "9m59s"},
		{10 * time.Minute, "10m00s"},
		{59*time.Minute + 59*time.Second, "59m59s"},
		{time.Hour, "1h00m"}, // past an hour the seconds are churn
		{time.Hour + 2*time.Minute, "1h02m"},
		{time.Hour + 2*time.Minute + 59*time.Second, "1h02m"},
		{25*time.Hour + 30*time.Minute, "25h30m"},
		{-5 * time.Second, "0s"}, // backwards clock clamps, never "-5s"
	}
	for _, c := range cases {
		if got := formatTurnElapsed(c.in); got != c.want {
			t.Errorf("formatTurnElapsed(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFormatTurnElapsed_WidthIsStableWithinABand is the anti-jitter
// property stated as a property: inside the minute band the string
// only changes width when the minutes count gains a digit, never
// when the seconds do. Without the zero-pad, 1m9s → 1m10s would
// shift the tail of the thinking line every third tick.
func TestFormatTurnElapsed_WidthIsStableWithinABand(t *testing.T) {
	width := len(formatTurnElapsed(time.Minute))
	for s := 0; s < 60; s++ {
		got := formatTurnElapsed(time.Minute + time.Duration(s)*time.Second)
		if len(got) != width {
			t.Errorf("formatTurnElapsed(1m%ds) = %q (width %d), want width %d — the line will jitter",
				s, got, len(got), width)
		}
	}
}

// TestSpinnerLine_ShowsElapsedWhileStreaming is the feature: after
// the floor, the thinking line carries the readout, and it advances
// on the repaints the spinner tick already causes.
func TestSpinnerLine_ShowsElapsedWhileStreaming(t *testing.T) {
	clock := time.Date(2026, 8, 15, 9, 0, 0, 0, time.UTC)
	m := elapsedModel(t, &clock)

	clock = clock.Add(12 * time.Second)
	line := m.renderSpinnerLine()
	if !strings.Contains(line, "12s") {
		t.Errorf("spinner line = %q, want an elapsed suffix of 12s", line)
	}
	// The verb is still there — the readout is a suffix, not a
	// replacement.
	if !strings.Contains(line, "Thinking") {
		t.Errorf("spinner line = %q, want the cognition verb retained", line)
	}

	clock = clock.Add(52 * time.Second)
	if line := m.renderSpinnerLine(); !strings.Contains(line, "1m04s") {
		t.Errorf("spinner line = %q, want 1m04s after crossing the minute", line)
	}

	clock = clock.Add(time.Hour)
	if line := m.renderSpinnerLine(); !strings.Contains(line, "1h01m") {
		t.Errorf("spinner line = %q, want 1h01m after crossing the hour", line)
	}
}

// TestSpinnerLine_SuppressedBelowTheFloor keeps the readout out of
// the way of short turns: nobody needs to watch "0s".
func TestSpinnerLine_SuppressedBelowTheFloor(t *testing.T) {
	clock := time.Date(2026, 8, 15, 9, 0, 0, 0, time.UTC)
	m := elapsedModel(t, &clock)

	// Compare against the same line with the suffix suppressed
	// rather than scanning for digits: the styled output is full of
	// ANSI escapes, which are themselves full of digits.
	bare := m.renderSpinnerLine()
	for _, d := range []time.Duration{0, time.Second, turnElapsedFloor - time.Millisecond} {
		clock = clock.Add(d)
		if got := m.renderTurnElapsed(); got != "" {
			t.Errorf("at %v the elapsed suffix = %q, want empty below the %v floor",
				d, got, turnElapsedFloor)
		}
		if line := m.renderSpinnerLine(); line != bare {
			t.Errorf("at %v the spinner line changed to %q, want the bare line %q", d, line, bare)
		}
		clock = clock.Add(-d)
	}

	clock = clock.Add(turnElapsedFloor)
	if got := m.renderTurnElapsed(); !strings.Contains(got, "3s") {
		t.Errorf("elapsed suffix = %q, want the readout to appear at the floor exactly", got)
	}
}

// TestElapsed_NotRenderedWhenIdle covers the two idle shapes: the
// whole in-progress block is suppressed off stateStreaming, and
// finalizeTurn drops the origin so nothing downstream can measure
// from a finished turn.
func TestElapsed_NotRenderedWhenIdle(t *testing.T) {
	clock := time.Date(2026, 8, 15, 9, 0, 0, 0, time.UTC)
	m := elapsedModel(t, &clock)
	clock = clock.Add(30 * time.Second)

	if got := m.renderInProgress(); !strings.Contains(got, "30s") {
		t.Fatalf("setup: in-progress block = %q, want the streaming readout", got)
	}

	m.finalizeTurn(30*time.Second, "")
	if m.state != stateIdle {
		t.Fatalf("finalizeTurn left state = %v, want stateIdle", m.state)
	}
	if !m.turnStarted.IsZero() {
		t.Error("finalizeTurn left turnStarted set — the invariant is non-zero iff an animation is live")
	}
	if got := m.renderInProgress(); got != "" {
		t.Errorf("in-progress block after finalizeTurn = %q, want empty", got)
	}
	if got := m.turnElapsed(); got != 0 {
		t.Errorf("turnElapsed after finalizeTurn = %v, want 0", got)
	}

	// An hour later the idle model still measures nothing, rather
	// than reporting the wall time since the last turn.
	clock = clock.Add(time.Hour)
	if got := m.renderTurnElapsed(); got != "" {
		t.Errorf("renderTurnElapsed while idle = %q, want empty", got)
	}
}

// TestElapsed_LiveAgentPathStampsTheStretch is the bug this feature
// could most easily have shipped: the LiveAgent path never calls
// submitTurn, so an unstamped turnStarted would measure from the
// zero time and render a fifty-five-year turn.
func TestElapsed_LiveAgentPathStampsTheStretch(t *testing.T) {
	clock := time.Date(2026, 8, 15, 9, 0, 0, 0, time.UTC)
	m := newModel(Options{Agent: newLiveAgentStub()})
	m.now = fixedClock(&clock)
	m.viewport.SetWidth(80)

	next, _ := m.Update(streamChunkMsg{gen: m.sessionGen, text: "tok", partial: true})
	live := next.(model)
	if !live.spinnerActive {
		t.Fatal("setup: expected spinnerActive after a partial chunk in liveMode")
	}
	if live.turnStarted.IsZero() {
		t.Fatal("liveMode stretch left turnStarted at the zero value — the readout would report 1970")
	}
	if !live.turnStarted.Equal(clock) {
		t.Errorf("turnStarted = %v, want the stretch's start %v", live.turnStarted, clock)
	}

	clock = clock.Add(20 * time.Second)
	if got := live.renderTurnElapsed(); !strings.Contains(got, "20s") {
		t.Errorf("liveMode elapsed = %q, want 20s", got)
	}

	// Commit ends the stretch; the origin goes with it.
	live.applyStreamChunk(streamChunkMsg{text: "done", partial: false})
	if !live.turnStarted.IsZero() {
		t.Error("liveMode commit left turnStarted set")
	}

	// A second stretch measures from ITS start, not the first one's
	// — the failure mode an IsZero-only guard would have kept.
	clock = clock.Add(10 * time.Minute)
	second := clock
	restarted, _ := live.Update(streamChunkMsg{gen: live.sessionGen, text: "more", partial: true})
	rm := restarted.(model)
	if !rm.turnStarted.Equal(second) {
		t.Fatalf("second stretch turnStarted = %v, want %v", rm.turnStarted, second)
	}
	// Unstyled, because the ANSI wrapper renderTurnElapsed adds is
	// itself full of digits and 'm's.
	clock = clock.Add(5 * time.Second)
	if got := formatTurnElapsed(rm.turnElapsed()); got != "5s" {
		t.Errorf("second stretch five seconds in = %q, want 5s — not the 10m05s it would show if it had kept the first stretch's origin", got)
	}
	if got := rm.renderTurnElapsed(); !strings.Contains(got, "5s") {
		t.Errorf("second stretch suffix = %q, want it to carry 5s", got)
	}
}

// TestElapsed_ZeroValueModelRendersNothing guards the nil clock on a
// hand-built model{} — the fixture shape a lot of the render-path
// tests use. nowFn must not panic and turnElapsed must read the zero
// turnStarted as "no turn".
func TestElapsed_ZeroValueModelRendersNothing(t *testing.T) {
	var m model
	if m.nowFn().IsZero() {
		t.Error("nowFn on a zero-value Model returned the zero time, want time.Now")
	}
	if got := m.turnElapsed(); got != 0 {
		t.Errorf("turnElapsed on a zero-value Model = %v, want 0", got)
	}
	if got := m.renderTurnElapsed(); got != "" {
		t.Errorf("renderTurnElapsed on a zero-value Model = %q, want empty", got)
	}
}

// TestElapsed_BackwardsClockDoesNotRenderGarbage covers the NTP step
// / suspend-resume case. The readout degrades to absent, never to a
// negative or a wrapped duration.
func TestElapsed_BackwardsClockDoesNotRenderGarbage(t *testing.T) {
	clock := time.Date(2026, 8, 15, 9, 0, 0, 0, time.UTC)
	m := elapsedModel(t, &clock)

	clock = clock.Add(-90 * time.Second)
	if got := m.turnElapsed(); got != 0 {
		t.Errorf("turnElapsed with a backwards clock = %v, want 0", got)
	}
	if got := m.renderTurnElapsed(); got != "" {
		t.Errorf("renderTurnElapsed with a backwards clock = %q, want empty", got)
	}
}

// TestSubmitTurn_ReStampsTheOrigin: a queue drain or auto-continue
// re-enters submitTurn, and the new turn's readout must start at
// zero rather than continue the previous turn's count.
func TestSubmitTurn_ReStampsTheOrigin(t *testing.T) {
	clock := time.Date(2026, 8, 15, 9, 0, 0, 0, time.UTC)
	m := elapsedModel(t, &clock)

	clock = clock.Add(45 * time.Second)
	m = m.submitTurn("second prompt")
	if !m.turnStarted.Equal(clock) {
		t.Fatalf("turnStarted = %v, want the second turn's start %v", m.turnStarted, clock)
	}
	if got := m.renderTurnElapsed(); got != "" {
		t.Errorf("second turn rendered %q at t=0, want empty rather than the first turn's 45s", got)
	}
	clock = clock.Add(4 * time.Second)
	if got := m.renderTurnElapsed(); !strings.Contains(got, "4s") {
		t.Errorf("second turn elapsed = %q, want 4s", got)
	}
}
