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

// Static-repaint allocation guard (issue #160).
//
// A repaint with nothing changing was allocating on the order of a
// megabyte per frame, and the cost of collecting it showed up as the
// p99 and max of keystroke-to-frame latency rather than as the
// median: a flat median with a bad tail, on a frame doing no new
// work, is the shape of GC pressure. Issues #170 and #157 took most
// of it; chatBlock (chatlist.go) took the rest.
//
// BenchmarkViewCompose in render_bench_test.go is the measurement —
// View() end to end, cache warm, at three transcript sizes. It is not
// duplicated here. What is here is the pair of things a benchmark
// cannot do:
//
//   - TestChatBlockMatchesStyle, which pins the substitution that
//     bought the reduction. chatBlock is only allowed to be faster
//     than the lipgloss Style it replaced if it is also
//     byte-identical to it, and "byte-identical" over rows carrying
//     escape sequences, wide runes and tabs is not something reading
//     the code twice establishes.
//   - TestStaticRepaintAllocations, a ceiling that fails the build if
//     a frame starts allocating like it used to.
package tui

import (
	"math/rand"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// chatBlockReference is what chatBlock replaced, kept verbatim as the
// oracle the fast path is checked against. Deliberately spelled out
// rather than called through chatBlock's own fallback: a test whose
// expected value is computed by the code under test proves only that
// the code agrees with itself.
func chatBlockReference(rows []string, width, height int) string {
	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		Render(strings.Join(rows, "\n"))
}

// TestChatBlockMatchesStyle checks chatBlock against the lipgloss
// Style it replaced over the cases the fast path is allowed to take
// and the cases it must refuse.
//
// The awkward inputs are the point. A row that is already exactly the
// block width, a row that ends in the spaces ansi.Wrap is entitled to
// eat, a row whose last escape left a colour switched on, a row with
// a tab in it (sanitizeLine exempts TAB, and lipgloss expands it),
// and a row of double-width runes are each a way for "just append
// spaces" to be subtly wrong, and each one is why chatRowPlain exists
// in the shape it does.
func TestChatBlockMatchesStyle(t *testing.T) {
	const esc = "\x1b"
	cases := []struct {
		name          string
		rows          []string
		width, height int
	}{
		{"empty", nil, 10, 4},
		{"one short row", []string{"hi"}, 10, 4},
		{"exact width", []string{"0123456789"}, 10, 1},
		{"full block", []string{"aaa", "bbb", "ccc"}, 3, 3},
		{"short of height", []string{"a"}, 6, 5},
		{"blank rows", []string{"", "", ""}, 5, 3},
		{"trailing spaces", []string{"ab   "}, 5, 2},
		{"trailing spaces at width", []string{"abcd "}, 5, 2},
		{"leading spaces", []string{"  ab"}, 8, 2},
		{"all spaces at width", []string{"     "}, 5, 2},
		{"closed sgr", []string{esc + "[31mred" + esc + "[0m"}, 8, 2},
		{"closed sgr bare reset", []string{esc + "[31mred" + esc + "[m"}, 8, 2},
		{"open sgr", []string{esc + "[31mred"}, 8, 2},
		{"open sgr then plain row", []string{esc + "[31mred", "plain"}, 8, 3},
		{"truecolor closed", []string{esc + "[38;2;1;2;3mx" + esc + "[0m"}, 4, 2},
		{"tab", []string{"a\tb"}, 12, 2},
		{"carriage return", []string{"ab\r"}, 6, 2},
		{"hyperlink", []string{esc + "]8;;https://example.com" + esc + "\\link"}, 9, 2},
		{"wide runes", []string{"日本語"}, 9, 2},
		{"wide runes short", []string{"日本"}, 9, 2},
		{"combining", []string{"éx"}, 6, 2},
		{"over width", []string{"0123456789abc"}, 6, 2},
		{"more rows than height", []string{"a", "b", "c"}, 4, 2},
		{"zero width", []string{"abc"}, 0, 3},
		{"zero height", []string{"abc"}, 4, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := chatBlockReference(tc.rows, tc.width, tc.height)
			if got := chatBlock(tc.rows, tc.width, tc.height); got != want {
				t.Errorf("chatBlock(%q, %d, %d)\n got %q\nwant %q",
					tc.rows, tc.width, tc.height, got, want)
			}
		})
	}
}

// TestChatBlockMatchesStyleRandom is the same equivalence over
// randomly assembled rows, because the table above tests the cases
// someone thought of and the interesting failures in ANSI handling
// are the ones nobody did. The seed is fixed so a failure is
// reproducible and a green run is not luck that varies per CI job.
//
// The alphabet is drawn to hit both sides of chatRowPlain: plain
// text, closed and open SGR, wide runes, tabs and OSC, at widths that
// straddle the block width so the truncate and pad branches both run.
func TestChatBlockMatchesStyleRandom(t *testing.T) {
	pieces := []string{
		"a", "bc", " ", "  ", "-", "日", "é", "é", "\t", "\r",
		"\x1b[31m", "\x1b[0m", "\x1b[m", "\x1b[38;2;10;20;30m", "\x1b[1m",
		"\x1b]8;;https://example.com\x1b\\",
	}
	rng := rand.New(rand.NewSource(1)) //nolint:gosec // determinism, not secrecy
	for i := range 3000 {
		width := 1 + rng.Intn(12)
		height := rng.Intn(5)
		rows := make([]string, rng.Intn(height+1))
		for r := range rows {
			var b strings.Builder
			for range rng.Intn(8) {
				b.WriteString(pieces[rng.Intn(len(pieces))])
			}
			rows[r] = b.String()
		}
		want := chatBlockReference(rows, width, height)
		if got := chatBlock(rows, width, height); got != want {
			t.Fatalf("case %d: chatBlock(%q, %d, %d)\n got %q\nwant %q",
				i, rows, width, height, got, want)
		}
	}
}

// TestChatRowPlain pins which rows chatBlock is allowed to pad by
// appending spaces, from the other side.
//
// The equivalence tests above stay green if chatRowPlain refuses
// everything — the fallback is correct by construction, it is just
// slow — so on its own the pair would not notice the fast path
// silently switching itself off and the allocations coming back. This
// is the half that says the plain rows really are taken.
func TestChatRowPlain(t *testing.T) {
	const esc = "\x1b"
	cases := []struct {
		row  string
		want bool
	}{
		{"", true},
		{"plain text", true},
		{"  indented", true},
		{"trailing  ", true},
		{"日本語 wide", true},
		{esc + "[31mred" + esc + "[0m", true},
		{esc + "[31mred" + esc + "[m", true},
		{esc + "[1;38;2;1;2;3mx" + esc + "[0m", true},
		{esc + "[0m" + esc + "[38;5;9mcolour" + esc + "[00m", true},
		{esc + "[31mstill red", false},
		{esc + "[1mstill bold" + esc + "[31m", false},
		{"tab\there", false},
		{"carriage\r", false},
		{"bell\a", false},
		{"del\x7f", false},
		{esc + "]8;;https://example.com" + esc + "\\link" + esc + "]8;;" + esc + "\\", false},
	}
	for _, tc := range cases {
		if got := chatRowPlain(tc.row); got != tc.want {
			t.Errorf("chatRowPlain(%q) = %v, want %v", tc.row, got, tc.want)
		}
	}
}

// staticRepaintAllocCeiling is the most allocations one repaint of a
// settled frame may make before this test fails.
//
// A CEILING WITH HEADROOM, NOT A PINNED NUMBER, and deliberately so.
// A figure pinned to the current measurement is a ratchet: every
// unrelated PR that adds a row of chrome has to edit it, the edit is
// never questioned, and after a dozen of them the assertion has
// tracked the regression it was supposed to catch. What issue #160 is
// actually defending against is an order of magnitude — the frame
// this test guards was allocating 5,177 objects before the change and
// 50,297 before #170 — so the useful question is "is a frame still in
// the low thousands", not "is it still exactly 1,026".
//
// The number is the measurement at the time of writing rounded up
// with roughly 45% of room on top. That absorbs the ordinary drift of
// adding chrome without absorbing a wrapper reintroduced onto the
// frame path, which costs thousands. Raise it if a change genuinely
// needs the room, but say in the commit message what bought the
// allocations.
//
// It was 1,500 against a measurement of about 1,030 until #200 and
// #201 memoized the composer and the status header, which took the
// same fixture to about 147. A ceiling left at 1,500 after that would
// no longer be a ceiling: it would sit an order of magnitude above
// the thing it measures, and the whole of the composer's 826-
// allocation rebuild could come back without it noticing. Lowering it
// with the win is what keeps the win.
const staticRepaintAllocCeiling = 250

// TestStaticRepaintAllocations fails when a repaint of a frame that
// is not changing starts allocating like the one in issue #160.
//
// Same fixture as BenchmarkViewCompose so the two numbers are
// comparable, and the same 100-turn size: the count does not move
// with transcript length (that is #161's dividend, and
// chatlist_test.go asserts it directly), so one size is enough here.
//
// AllocsPerRun rather than testing.Benchmark: it pins GOMAXPROCS for
// the duration and runs the body once before it starts counting, so
// the first-call lazy work inside View — the scroll state, the
// terminal capability probe — lands in the warm-up rather than in the
// sample.
func TestStaticRepaintAllocations(t *testing.T) {
	m := benchModel(t, 100, 100, 40)
	// Settled: nothing about this model changes between repaints, so
	// every allocation the body makes is one the operator pays for a
	// frame that looks exactly like the last one.
	got := testing.AllocsPerRun(20, func() {
		_ = m.View().Content
	})
	if got > staticRepaintAllocCeiling {
		t.Errorf("static repaint allocated %.0f objects, ceiling is %d; "+
			"profile it with\n"+
			"\tgo test ./tui -run '^$' -bench 'BenchmarkViewCompose/turns=100' "+
			"-benchmem -memprofile mem.out",
			got, staticRepaintAllocCeiling)
	}
	t.Logf("static repaint: %.0f allocations (ceiling %d)", got, staticRepaintAllocCeiling)
}
