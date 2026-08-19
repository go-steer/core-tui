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
	"strconv"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Resize-drag benchmarks (issue #104). A terminal drag does not emit
// one WindowSizeMsg — it emits a stream of them, one per column the
// operator's mouse crosses. These benchmarks model that: dragBurst
// consecutive width-changing events over a session of N turns.
//
//	go test ./tui -run '^$' -bench 'ResizeDrag|ResizeEvent|ResizeSettle' -benchmem
//
// The turn counts (100 / 400) match the measurement table in issue
// #104 so the numbers are directly comparable.

// benchSink keeps the benchmarked model reachable past the timed
// region so nothing measured can be optimized away.
var benchSink model

// dragBurst is how many WindowSizeMsg events one benchmarked drag
// delivers — ~30 is what a half-second mouse drag across 15 columns
// produces on a terminal that reports every intermediate size.
const dragBurst = 30

// benchDragBaseWidth / benchDragSpan describe the triangular width
// sweep the drag walks: base → base-span → base, one column per
// event, so consecutive events always change the width (never a
// no-op) and the sweep revisits widths it has already rendered at
// — exactly the pattern a real back-and-forth drag produces.
const (
	benchDragBaseWidth = 120
	benchDragSpan      = 15
)

// dragWidthAt returns the terminal width the i-th drag event
// reports.
func dragWidthAt(i int) int {
	period := 2 * benchDragSpan
	phase := i % period
	if phase >= benchDragSpan {
		phase = period - phase
	}
	return benchDragBaseWidth - phase - 1
}

// benchAssistantText is a representative assistant turn: prose,
// a bullet list, and a fenced code block. Glamour's cost is driven
// by block count and inline spans, so a turn that is all one
// paragraph would flatter the numbers.
func benchAssistantText(i int) string {
	n := strconv.Itoa(i)
	return "Here is what I found in pass " + n + `.

The handler walks the history on every event, which is the part
that dominates the profile. Three things stand out:

- ` + "`refreshViewport`" + ` rebuilds the whole content string.
- Every assistant row is re-rendered through Glamour.
- The list cache is dropped wholesale, so nothing survives.

` + "```go" + `
func (m *Model) rerender() {
	for i, msg := range m.history.Snapshot() {
		m.history.SetRendered(i, render(msg.Text))
	}
}
` + "```" + `

The fix is to **debounce** the pass and key the cache by width.`
}

// newBenchDragModel builds a sized model carrying turns full
// user+assistant exchanges, each assistant row already Glamour-
// rendered at the starting width — the steady state a drag
// actually starts from.
func newBenchDragModel(turns int) model {
	m := newModel(Options{})
	out, _ := m.Update(tea.WindowSizeMsg{Width: benchDragBaseWidth, Height: 40})
	m = out.(model)
	mr := m.ensureMarkdown()
	for i := range turns {
		m.history.Append(Message{Role: RoleUser, Text: "question number " + strconv.Itoa(i)})
		text := benchAssistantText(i)
		m.history.Append(Message{Role: RoleAssistant, Text: text, Rendered: mr.renderMarkdown(text)})
	}
	m.refreshViewport()
	// Retire the reflow the startup WindowSizeMsg armed (0 → base
	// width is a width change) so the drag starts from a settled
	// model, the way a real session does. The visible tick first:
	// it is what clears reflowHot, and a model that still thinks a
	// drag is in flight would treat the first event of the NEXT one
	// as a continuation rather than a leading edge (issue #247).
	m = deliverResizeVisibleTick(m)
	for m.reflowPending {
		out, _ = m.Update(resizeReflowMsg{gen: m.resizeGen})
		m = out.(model)
	}
	return m
}

// deliverResizeVisibleTick hands the model the visible tick a real
// runtime would deliver resizeVisibleWindow after the last event of
// a drag (issue #247). Delivering the msg directly rather than
// running the returned tea.Tick Cmd keeps the debounce interval out
// of tests and benchmarks that are measuring the work, not the wait.
func deliverResizeVisibleTick(m model) model {
	out, _ := m.Update(resizeReflowMsg{gen: m.resizeGen, visible: true})
	return out.(model)
}

// benchmarkResizeDrag measures a whole drag: dragBurst consecutive
// width-changing WindowSizeMsg events delivered back to back, with
// no settle in between. This is the number the operator feels as a
// freeze while the mouse is down.
func benchmarkResizeDrag(b *testing.B, turns int) {
	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		m := newBenchDragModel(turns)
		b.StartTimer()
		for i := range dragBurst {
			out, _ := m.Update(tea.WindowSizeMsg{Width: dragWidthAt(i), Height: 40})
			m = out.(model)
		}
	}
}

func BenchmarkResizeDrag100(b *testing.B) { benchmarkResizeDrag(b, 100) }
func BenchmarkResizeDrag400(b *testing.B) { benchmarkResizeDrag(b, 400) }

// benchmarkResizeEvent measures ONE width-changing WindowSizeMsg
// — the per-event cost from the issue #104 table.
func benchmarkResizeEvent(b *testing.B, turns int) {
	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		m := newBenchDragModel(turns)
		b.StartTimer()
		out, _ := m.Update(tea.WindowSizeMsg{Width: benchDragBaseWidth - 7, Height: 40})
		benchSink = out.(model)
	}
}

func BenchmarkResizeEvent100(b *testing.B) { benchmarkResizeEvent(b, 100) }
func BenchmarkResizeEvent400(b *testing.B) { benchmarkResizeEvent(b, 400) }

// drainResizeSettle delivers settle + warm ticks (and the coalesced
// repaint each one schedules) until the reflow retires. Delivering
// the msgs directly rather than running the returned tea.Tick Cmds
// keeps the sleeps out of the measurement — what's being measured is
// the work the ticks do, not the debounce interval itself.
func drainResizeSettle(b *testing.B, m model) model {
	m = deliverResizeVisibleTick(m)
	for i := 0; m.reflowPending; i++ {
		if i > 100000 {
			b.Fatal("resize reflow never retired")
		}
		out, _ := m.Update(resizeReflowMsg{gen: m.resizeGen})
		m = out.(model)
		out, _ = m.Update(coalescedRefreshMsg{})
		m = out.(model)
	}
	return m
}

// benchmarkResizeSettle measures the post-drag settle in isolation:
// warming every message the drag deferred, one resizeWarmChunk-sized
// slice per tick, with the coalesced repaint each slice schedules.
// This is work the pre-#104 code did inline during the drag.
func benchmarkResizeSettle(b *testing.B, turns int) {
	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		m := newBenchDragModel(turns)
		for i := range dragBurst {
			out, _ := m.Update(tea.WindowSizeMsg{Width: dragWidthAt(i), Height: 40})
			m = out.(model)
		}
		b.StartTimer()
		benchSink = drainResizeSettle(b, m)
	}
}

func BenchmarkResizeSettle100(b *testing.B) { benchmarkResizeSettle(b, 100) }
func BenchmarkResizeSettle400(b *testing.B) { benchmarkResizeSettle(b, 400) }
