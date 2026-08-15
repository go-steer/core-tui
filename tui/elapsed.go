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

// Turn elapsed-time readout for the thinking line (issue #111).
// The spinner tells the operator that something is happening; it
// never told them for how long, which is the difference between
// "this is a big one" and "this is wedged".
//
// The readout hangs off the existing spinner repaint cadence — no
// new timer — and is deliberately coarse: see formatTurnElapsed for
// why a live counter must not change width on every tick.

package tui

import (
	"fmt"
	"time"
)

// turnElapsedFloor is how long the turn must have been running
// before the readout appears at all.
//
// One spinnerCadence, because that is the repaint that advances the
// number: anything shown earlier is a value the operator cannot
// watch move, and "0s" / "1s" tells them nothing the spinner hasn't
// already. Starting at the first tick also means the suffix appears
// exactly once per turn rather than flickering in and out on short
// turns that finish inside the first cadence.
const turnElapsedFloor = spinnerCadence

// nowFn is the Model's clock. Tests set Model.now directly to make
// the elapsed readout deterministic; everything else gets time.Now.
// Nil-safe so a zero-value Model{} — how a lot of the render-path
// tests build their fixture — still works.
func (m Model) nowFn() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

// turnElapsed reports how long the current turn's spinner animation
// has been running, or 0 when no turn is in flight.
//
// The IsZero guard is load-bearing: turnStarted is stamped only
// where the animation starts (submitTurn and applyStreamChunk's
// spinnerActive false→true flip), so an unstamped Model would
// otherwise measure from the zero time and report a fifty-five-year
// turn. A backwards clock (NTP step, suspend/resume) clamps to 0 for
// the same reason — the readout degrades to absent, never to garbage.
func (m Model) turnElapsed() time.Duration {
	if m.turnStarted.IsZero() {
		return 0
	}
	d := m.nowFn().Sub(m.turnStarted)
	if d < 0 {
		return 0
	}
	return d
}

// formatTurnElapsed renders a duration for the live thinking line:
// `12s`, `1m04s`, `1h02m`.
//
// Three properties matter more than precision here, because this
// string is re-rendered under a spinner that is already moving:
//
//   - No sub-second component. `0.4s` → `1.2s` → `2.7s` is noise on
//     a line whose job is to answer "roughly how long".
//   - Zero-padded minor unit. `1m4s` → `1m10s` would shift the line
//     a column wider mid-turn; `1m04s` → `1m10s` does not. Width
//     changes only at 10s, 10m and 10h — once each, per turn.
//   - Coarser as it grows. Past an hour the seconds digit is churn,
//     so the minor unit becomes minutes.
//
// Deliberately not formatLatency (tool_latency.go): that one is
// tuned for a settled, one-shot tool-row badge where milliseconds
// are load-bearing and a variable width costs nothing.
func formatTurnElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
	case d >= time.Minute:
		return fmt.Sprintf("%dm%02ds", int(d/time.Minute), int(d%time.Minute/time.Second))
	default:
		return fmt.Sprintf("%ds", int(d/time.Second))
	}
}

// renderTurnElapsed returns the styled suffix for the thinking line
// — a leading space plus the muted duration — or "" when the turn is
// younger than turnElapsedFloor or no turn is in flight.
func (m Model) renderTurnElapsed() string {
	d := m.turnElapsed()
	if d < turnElapsedFloor {
		return ""
	}
	// Muted but NOT italic: the verb ahead of it is prose, this is
	// a measurement, and the contrast keeps the eye from reading
	// "12s" as the tail of the sentence.
	return " " + m.styles.Muted.Render(formatTurnElapsed(d))
}
