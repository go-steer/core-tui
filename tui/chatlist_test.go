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
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// listItem-addressed transcript (issue #161). The contract under test:
//
//   - a frame costs what is on screen, not what is in scrollback;
//   - the window it draws is the same window a flat offset names;
//   - the scroll pair and the flat offset are inverses;
//   - scrolling stops at both ends and lands on the tail exactly;
//   - no row leaves the transcript wider than the transcript.

// chatAllLines is the whole transcript, row by row — the flat blob
// the pair offset replaced. Tests use it as the oracle: chatView must
// draw a window of exactly this sequence. EXHAUSTIVE by definition, so
// it belongs in a test and nowhere else.
//
// Cut to the window's width the way chatView cuts, since #154 moved
// that from the cache to the draw: a cached row is now as wide as its
// content, and the oracle has to be what reaches the frame.
func chatAllLines(m model) []string {
	var out []string
	for i := range m.chatRowCount() {
		for _, ln := range m.chatRowLines(i) {
			out = append(out, chatCutLine(ln, 0, m.viewport.Width()))
		}
	}
	return out
}

// chatViewLines splits a drawn frame back into lines with the
// selection gutter (issue #152) and the lipgloss pad removed, so it
// can be compared against the unpadded source lines of a row.
func chatViewLines(view string) []string {
	lines := strings.Split(view, "\n")
	for i, ln := range lines {
		w := ansi.StringWidth(ln)
		if w <= chatGutterWidth {
			lines[i] = ""
			continue
		}
		lines[i] = strings.TrimRight(ansi.Cut(ln, chatGutterWidth, w), " ")
	}
	return lines
}

// chatEntriesAtWidth counts the rows the cache holds an exact render
// for at the current width — i.e. how many rows the model has
// actually assembled since the cache was dropped.
func chatEntriesAtWidth(m model) int {
	n := 0
	for key := range m.listCache.entries {
		if key.width == m.viewport.Width() {
			n++
		}
	}
	return n
}

// TestChatFrame_RenderCountIsFlatInTranscriptSize — the whole point of
// issue #161. A repaint from a cold cache assembles the rows the
// window shows and no others, so the count must not move when the
// transcript behind it grows 4x. Before the item-addressed walk this
// was one render per message.
func TestChatFrame_RenderCountIsFlatInTranscriptSize(t *testing.T) {
	counts := map[int]int{}
	for _, turns := range []int{25, 100, 400} {
		m := benchModel(t, turns, 100, 40)
		m.listCache = newListCache()
		m.refreshViewport()
		_ = m.chatView()
		counts[turns] = chatEntriesAtWidth(m)
	}
	if counts[25] == 0 {
		t.Fatal("a repaint assembled nothing — the window has to render something")
	}
	if counts[100] != counts[25] || counts[400] != counts[25] {
		t.Errorf("per-frame renders track transcript size: 25 turns → %d, 100 → %d, 400 → %d (want all equal)",
			counts[25], counts[100], counts[400])
	}
	// A frame is 40 rows tall and every row is at least one line, so a
	// window can never legitimately need more than 40 of them. Well
	// under that in practice; the bound is what protects the property
	// if the fixture's prose ever changes length.
	if counts[400] > 40 {
		t.Errorf("a 40-row window assembled %d rows", counts[400])
	}
}

// TestChatFrame_ScrollbackIsNeverAssembled — the same guarantee stated
// from the other side: the rows above the window are not merely cheap,
// they are untouched.
func TestChatFrame_ScrollbackIsNeverAssembled(t *testing.T) {
	m := benchModel(t, 200, 100, 40)
	m.listCache = newListCache()
	m.refreshViewport()
	_ = m.chatView()

	snap := m.history.Snapshot()
	for i := 0; i < len(snap)/2; i++ {
		item := messageItem{msg: snap[i], idx: i, total: len(snap)}
		if _, ok := m.listCache.getLines(item, m.viewport.Width()); ok {
			t.Fatalf("row %d is in the first half of a 200-turn transcript pinned to the tail; drawing the tail must not have rendered it", i)
		}
	}
}

// TestChatView_DrawsTheWindowTheOffsetNames — the walk-and-skip in
// chatView has to agree with the flat offset it is a substitution for.
// Checked at several positions, including one mid-row (an offsetLine
// that is not zero), which is the case a naive per-row walk gets wrong.
func TestChatView_DrawsTheWindowTheOffsetNames(t *testing.T) {
	m := benchModel(t, 30, 100, 20)
	all := chatAllLines(m)
	height := m.viewport.Height()

	for _, y := range []int{0, 1, 7, 13, 40, len(all) - height - 3} {
		if y < 0 || y+height > len(all) {
			continue
		}
		m.chatSetYOffset(y)
		if got := m.chatYOffset(); got != y {
			t.Fatalf("setting offset %d landed on %d", y, got)
		}
		got := chatViewLines(m.chatView())
		if len(got) != height {
			t.Fatalf("offset %d: frame is %d lines, want %d", y, len(got), height)
		}
		for i := range height {
			want := strings.TrimRight(all[y+i], " ")
			if got[i] != want {
				t.Fatalf("offset %d, line %d:\n got %q\nwant %q", y, i, got[i], want)
			}
		}
	}
}

// TestChatOffset_PairAndFlatAreInverses — chatSetYOffset resolves a
// flat offset against the rows and chatYOffset walks it back; every
// reachable position has to survive the round trip.
func TestChatOffset_PairAndFlatAreInverses(t *testing.T) {
	m := benchModel(t, 20, 80, 15)
	total := len(chatAllLines(m))
	for y := range total {
		m.chatSetYOffset(y)
		if got := m.chatYOffset(); got != y {
			t.Fatalf("offset %d round-tripped to %d", y, got)
		}
	}
}

// TestChatScroll_LineByLineMatchesTheFlatWalk — scrolling one line at
// a time must advance the window exactly one line, all the way from
// the top to the bottom. This is where an off-by-one at a row boundary
// shows up: the offsetLine wraps to the next row exactly when the
// current row runs out.
func TestChatScroll_LineByLineMatchesTheFlatWalk(t *testing.T) {
	m := benchModel(t, 15, 80, 12)
	m.chatGotoTop()
	for y := 0; !m.chatAtBottom(); y++ {
		if y > 10000 {
			t.Fatal("scrolling down never reached the bottom")
		}
		if got := m.chatYOffset(); got != y {
			t.Fatalf("after %d single-line scrolls the offset is %d", y, got)
		}
		m.chatScrollBy(1)
	}
	bottom := m.chatYOffset()
	for y := bottom; y > 0; y-- {
		if got := m.chatYOffset(); got != y {
			t.Fatalf("scrolling back up: offset %d, want %d", got, y)
		}
		m.chatScrollBy(-1)
	}
}

// TestChatScroll_StopsAtBothEnds — over-scrolling in either direction
// is a clamp, not a wrap and not a runaway offset.
func TestChatScroll_StopsAtBothEnds(t *testing.T) {
	m := benchModel(t, 10, 80, 12)

	m.chatGotoTop()
	m.chatScrollBy(-50)
	if idx, line := m.viewport.Offset(); idx != 0 || line != 0 {
		t.Errorf("scrolling up past the top left the offset at (%d, %d), want (0, 0)", idx, line)
	}

	m.chatGotoBottom()
	wantIdx, wantLine := m.viewport.Offset()
	m.chatScrollBy(500)
	if idx, line := m.viewport.Offset(); idx != wantIdx || line != wantLine {
		t.Errorf("scrolling down past the bottom moved the offset from (%d, %d) to (%d, %d)", wantIdx, wantLine, idx, line)
	}
	if !m.chatAtBottom() {
		t.Error("chatAtBottom is false at the bottom offset")
	}
}

// TestChatGotoBottom_ShowsTheLastLine — the tail pin has to put the
// last content line on the last row of the window, with nothing
// scrolled off the end. chatBottomOffset walks up from the end rather
// than measuring the transcript, so this is the check that the walk
// terminates in the right place.
func TestChatGotoBottom_ShowsTheLastLine(t *testing.T) {
	m := benchModel(t, 30, 100, 20)
	all := chatAllLines(m)
	m.chatGotoBottom()

	got := chatViewLines(m.chatView())
	last := strings.TrimRight(all[len(all)-1], " ")
	if got[len(got)-1] != last {
		t.Errorf("bottom of the frame is %q, want the transcript's last line %q", got[len(got)-1], last)
	}
	if want := len(all) - m.viewport.Height(); m.chatYOffset() != want {
		t.Errorf("bottom offset is %d, want %d", m.chatYOffset(), want)
	}
}

// TestChatRowLines_LiveTailIsTheLastRow — the in-progress block is a
// row of the list like any other, just an uncached one, and it sits
// after every history entry.
func TestChatRowLines_LiveTailIsTheLastRow(t *testing.T) {
	m := benchModel(t, 4, 80, 20)
	before := m.chatRowCount()

	m.state = stateStreaming
	m.inProgressText = "partial answer arriving"
	m.refreshViewport()

	if got := m.chatRowCount(); got != before+1 {
		t.Fatalf("streaming added %d rows, want 1 (row count %d → %d)", got-before, before, got)
	}
	tail := m.chatRowLines(m.chatRowCount() - 1)
	if len(tail) == 0 {
		t.Fatal("the live tail row rendered nothing")
	}
	if !strings.Contains(ansi.Strip(strings.Join(tail, "\n")), "partial answer arriving") {
		t.Error("the live tail row does not carry the in-progress text")
	}
	// It must not be in the cache: it changes on every chunk, so an
	// entry per chunk would be pure garbage.
	if got := m.chatRowCount(); chatEntriesAtWidth(m) >= got {
		t.Error("the live tail was cached; it changes every chunk and must not be")
	}
}

// TestChatView_NoLineExceedsTheWidth — without the viewport there is
// nothing downstream that cuts an over-wide line, so a row that
// overruns would shift the panel beside it. chatCutLine is the guard;
// this is the assertion that it is actually reached, including on the
// live tail — which since #154 is cut at draw time like everything
// else rather than on its own.
func TestChatView_NoLineExceedsTheWidth(t *testing.T) {
	m := benchModel(t, 6, 60, 20)
	m.history.Append(Message{
		Role:     RoleAssistant,
		Text:     "wide",
		Rendered: strings.Repeat("verywidecontent", 20),
	})
	m.state = stateStreaming
	m.inProgressText = strings.Repeat("streaming-overrun ", 30)
	m.refreshViewport()
	m.chatGotoBottom()

	// The drawn block is a row's width plus the selection gutter the
	// marker lives in (issue #152) — that sum is the column the frame
	// handed the transcript, and every line has to fill it exactly.
	width := m.viewport.Width() + chatGutterWidth
	for i, ln := range strings.Split(m.chatView(), "\n") {
		if got := ansi.StringWidth(ln); got != width {
			t.Fatalf("frame line %d is %d cells wide, want exactly %d", i, got, width)
		}
	}
}

// TestChatView_ZeroSizeDrawsNothing — the transcript is asked to draw
// before the first WindowSizeMsg lands, and during a drag a narrow
// pane can take the derived width to zero.
func TestChatView_ZeroSizeDrawsNothing(t *testing.T) {
	m := newModel(Options{})
	m.history.Append(Message{Role: RoleUser, Text: "hello"})
	for _, size := range [][2]int{{0, 0}, {0, 20}, {80, 0}} {
		m.viewport.SetWidth(size[0])
		m.viewport.SetHeight(size[1])
		if got := m.chatView(); got != "" {
			t.Errorf("width=%d height=%d drew %q, want empty", size[0], size[1], got)
		}
	}
}

// TestChatRowLines_SeparatorOpensUserTurns — the row includes the gap
// above it, and a user turn opens with the rule that marks a new
// exchange. Line counts matter as much as content: they are what the
// lazy walk budgets against.
func TestChatRowLines_SeparatorOpensUserTurns(t *testing.T) {
	m := newModel(Options{})
	m.viewport.SetWidth(60)
	m.viewport.SetHeight(20)
	m.history.Append(Message{Role: RoleUser, Text: "first", Rendered: "first"})
	m.history.Append(Message{Role: RoleAssistant, Text: "answer", Rendered: "answer"})
	m.history.Append(Message{Role: RoleUser, Text: "second", Rendered: "second"})

	first := m.chatRowLines(0)
	if strings.TrimSpace(ansi.Strip(first[0])) == "" {
		t.Error("the first row must not be preceded by a blank line")
	}
	assistant := m.chatRowLines(1)
	if strings.TrimSpace(ansi.Strip(assistant[0])) != "" {
		t.Errorf("an assistant row opens with %q, want a blank separator line", assistant[0])
	}
	second := m.chatRowLines(2)
	if len(second) < 2 {
		t.Fatalf("a user row is %d lines, want a rule plus a blank plus content", len(second))
	}
	if !strings.Contains(second[0], GlyphRule) {
		t.Errorf("a user row opens with %q, want the separator rule", second[0])
	}
	if strings.TrimSpace(ansi.Strip(second[1])) != "" {
		t.Errorf("the rule is followed by %q, want a blank line", second[1])
	}
}

// TestChatRowLines_CachedRowIsTheColdRender — the cache is transparent
// or it is a bug. Every row a walk hands back has to be what
// renderMessage produces for it.
func TestChatRowLines_CachedRowIsTheColdRender(t *testing.T) {
	m := benchModel(t, 8, 90, 20)
	snap := m.history.Snapshot()
	for i, msg := range snap {
		lines := m.chatMessageLines(i, len(snap), msg)
		want := strings.Split(m.renderMessage(msg), "\n")
		if strings.Join(lines, "\n") != strings.Join(want, "\n") {
			t.Fatalf("row %d does not match its cold render", i)
		}
		// Second read is the cached path.
		if again := m.chatMessageLines(i, len(snap), msg); strings.Join(again, "\n") != strings.Join(want, "\n") {
			t.Fatalf("row %d changed on the cached read", i)
		}
	}
}

// BenchmarkChatViewWarm is the headline number from issue #161: a
// repaint with the cache warm, which is what every stream chunk,
// spinner tick and keystroke pays. It must not grow with the
// transcript.
func BenchmarkChatViewWarm(b *testing.B) {
	for _, turns := range benchTurnCounts {
		b.Run("turns="+strconv.Itoa(turns), func(b *testing.B) {
			m := benchModel(b, turns, 100, 40)
			_ = m.chatView()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_ = m.chatView()
			}
		})
	}
}

// BenchmarkChatScrollLine measures a single-line scroll, the
// finest-grained operation the transcript has. It renders at most one
// new row, so it is where an accidental transcript-wide walk would be
// most obvious.
func BenchmarkChatScrollLine(b *testing.B) {
	for _, turns := range benchTurnCounts {
		b.Run("turns="+strconv.Itoa(turns), func(b *testing.B) {
			m := benchModel(b, turns, 100, 40)
			m.chatGotoBottom()
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				if i%2 == 0 {
					m.chatScrollBy(-1)
				} else {
					m.chatScrollBy(1)
				}
			}
		})
	}
}
