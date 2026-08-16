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

// Committed render benchmarks (issue #101). Nothing in the package
// measured render cost, so a regression in the hot repaint path was
// invisible until an operator noticed the TUI feel sticky.
//
// All three are parameterized over transcript size (10 / 100 / 400
// turns) because the cost that matters is the one that grows with
// the session: a 10-turn transcript hides an O(n) repaint that a
// 400-turn one exposes immediately.
//
// BenchmarkResizeWidthChange is the one issue #104 is about. A
// width change invalidates every entry in the lazy-render cache
// (listcache.go keys on width), so the whole transcript is
// re-wrapped, re-Glamoured and re-highlighted on a single
// WindowSizeMsg. BenchmarkResizeHeightOnly is its control: the same
// message shape, the same code path, but the cache stays warm — the
// delta between the two IS the re-render cost #104 wants to remove.
//
//	go test ./tui -run '^$' -bench 'Resize|RefreshViewport' -benchmem

package tui

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// benchTurnCounts are the transcript sizes every benchmark sweeps.
var benchTurnCounts = []int{10, 100, 400}

// benchModel builds a sized model carrying n user/assistant turns
// of realistic prose. The theme is pinned so a palette change can't
// move the numbers.
//
// testing.TB rather than *testing.B so the transcript tests in
// chatlist_test.go can assert on the same fixture the benchmarks
// measure — a boundedness property that holds only for a fixture
// nobody times is not the property anyone cares about.
func benchModel(tb testing.TB, turns, w, h int) Model {
	tb.Helper()
	m := NewModel(Options{Agent: &bareAgent{id: "bench"}})
	m.styles = NewStylesWithTheme(true, goldenTheme())
	out, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m = out.(Model)
	for i := 0; i < turns; i++ {
		q := "turn " + strconv.Itoa(i) + ": what does this function do?"
		m.history.Append(Message{Role: RoleUser, Text: q, Rendered: q})
		a := "Turn " + strconv.Itoa(i) + ". " +
			strings.Repeat("It reads the config, validates it, and returns a handle. ", 4)
		m.history.Append(Message{Role: RoleAssistant, Text: a, Rendered: a})
	}
	m.refreshViewport()
	return m
}

// BenchmarkRefreshViewport measures the repaint itself at a steady
// width — the path every streamed chunk runs through. The lazy
// render cache is warm here by construction, so this is the floor
// cost of walking the transcript and re-joining it.
func BenchmarkRefreshViewport(b *testing.B) {
	for _, turns := range benchTurnCounts {
		b.Run("turns="+strconv.Itoa(turns), func(b *testing.B) {
			m := benchModel(b, turns, 100, 40)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.refreshViewport()
			}
		})
	}
}

// BenchmarkRefreshViewportColdCache measures the same repaint with
// the lazy-render cache dropped each iteration — the cost a width
// change actually pays, isolated from the WindowSizeMsg plumbing.
func BenchmarkRefreshViewportColdCache(b *testing.B) {
	for _, turns := range benchTurnCounts {
		b.Run("turns="+strconv.Itoa(turns), func(b *testing.B) {
			m := benchModel(b, turns, 100, 40)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				m.listCache = newListCache()
				b.StartTimer()
				m.refreshViewport()
			}
		})
	}
}

// BenchmarkResizeWidthChange is the issue #104 measurement: a
// WindowSizeMsg whose WIDTH differs from the current one. Every
// cached message render is keyed by width, so all of them miss and
// the entire transcript is re-rendered inside one Update. Alternate
// between two widths so no iteration can accidentally hit a cache
// entry left behind by the previous one.
func BenchmarkResizeWidthChange(b *testing.B) {
	for _, turns := range benchTurnCounts {
		b.Run("turns="+strconv.Itoa(turns), func(b *testing.B) {
			m := benchModel(b, turns, 100, 40)
			widths := [2]int{100, 101}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out, _ := m.Update(tea.WindowSizeMsg{Width: widths[i%2], Height: 40})
				m = out.(Model)
			}
		})
	}
}

// BenchmarkResizeHeightOnly is the control arm: the same
// WindowSizeMsg handler, the same resize arithmetic, but the width
// never moves so the render cache stays warm. Subtract this from
// BenchmarkResizeWidthChange to get the re-render cost alone.
func BenchmarkResizeHeightOnly(b *testing.B) {
	for _, turns := range benchTurnCounts {
		b.Run("turns="+strconv.Itoa(turns), func(b *testing.B) {
			m := benchModel(b, turns, 100, 40)
			heights := [2]int{40, 41}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: heights[i%2]})
				m = out.(Model)
			}
		})
	}
}

// BenchmarkViewCompose measures View() end to end, clipping post-
// pass included, so the cost of the #102 safety net is visible
// rather than folded into the resize numbers.
func BenchmarkViewCompose(b *testing.B) {
	for _, turns := range benchTurnCounts {
		b.Run("turns="+strconv.Itoa(turns), func(b *testing.B) {
			m := benchModel(b, turns, 100, 40)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = m.View().Content
			}
		})
	}
}
