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

// Selecting a transcript item, and folding it away (issue #152).
//
// A single tool result or diff can be hundreds of lines, and the
// transcript rendered every one of them: one unlucky item buried the
// rest of the session and the only way past it was to hold a scroll
// key. This file adds the two halves of the answer — a cursor that
// says which item you are on, and a fold that takes a long one down
// to a few lines.
//
// # The gutter is reserved, not borrowed
//
// The marker is a dotted bar down the left edge of the selected item
// (GlyphSelectBar), and the two columns it lives in are subtracted
// from the transcript's width for EVERY row, selected or not. Drawing
// the bar into a gutter that only exists while an item is selected
// would be cheaper by two columns and much worse: an item's wrap
// width would then depend on whether the cursor is on it, so moving
// the cursor would re-wrap two items, invalidate their cache entries
// at the width they were rendered at, and shift the text of the item
// you are pointing at out from under you. With the gutter reserved,
// the marker is a per-line PREFIX applied at draw time in chatView —
// the render cache never sees it, moving the cursor renders nothing,
// and the item under the cursor does not move.
//
// # Folding is a view of the render, not a second render
//
// A collapsed item is the first chatCollapsedLines lines of its
// ordinary render plus a count of what is hidden. That keeps the fold
// out of the cache key: renderMessage never learns about it, a fold
// or unfold invalidates nothing, and an item that is folded while
// off-screen costs a slice header when it comes back. It also means
// the fold works the same way for every role — a diff, a tool result
// and an assistant turn all fold, because none of them had to opt in.
//
// # Heights change under the viewport, and that is fine
//
// Folding an item changes its height, which used to be the hard part:
// a flat line offset into a concatenated transcript is invalidated by
// any height change ABOVE it, so the scroll position had to be
// re-derived from a total that no longer described the same content.
// Issue #161 removed the flat offset. The position is (offsetIdx,
// offsetLine) — an item and a line within it — so an item folding
// three screens up changes nothing about where the window sits, and
// the only case left to handle is folding the row the window starts
// on, which clampChatOffset already handles for every other reason a
// row can shrink.
package tui

import (
	"fmt"
	"strings"
)

const (
	// chatGutterWidth is the column pair reserved on the left of
	// every transcript row for the selection marker: the bar and the
	// space that separates it from the content.
	chatGutterWidth = 2

	// chatCollapsedLines is how much of a folded item stays on
	// screen. Three lines is enough to identify what the item is —
	// the tool row and the head of its output, the first sentence of
	// an assistant turn — which is what the operator needs to decide
	// whether to unfold it. One line would be a table of contents,
	// and a fold you cannot navigate by is a fold nobody opens.
	chatCollapsedLines = 3
)

// ---------------------------------------------------------------
// The cursor
// ---------------------------------------------------------------

// chatSelectBy moves the cursor delta items, positive for down, and
// scrolls whatever is needed to bring the new item into view. Stops
// at both ends rather than wrapping: a cursor that reappears at the
// other end of a long transcript reads as a jump, not as a move.
func (m *Model) chatSelectBy(delta int) {
	n := m.history.Len()
	if n == 0 {
		return
	}
	m.selIdx = min(atLeast(m.selIdx+delta, 0), n-1)
	m.chatRevealSelection()
}

// chatSelectFirst / chatSelectLast put the cursor on the ends of the
// transcript. The scroll they pair with is the one the operator asked
// for by pressing g / G — the first line of the session and the tail
// — rather than the minimal scroll that would reveal the item.
func (m *Model) chatSelectFirst() {
	m.selIdx = 0
	m.chatGotoTop()
}

func (m *Model) chatSelectLast() {
	m.selIdx = atLeast(m.history.Len()-1, 0)
	m.chatGotoBottom()
}

// chatSeedSelection places the cursor when the transcript takes the
// keyboard.
//
// Keeps the previous selection when it is still on screen — tabbing
// out to type and back should not lose your place — and otherwise
// takes the LAST item in the window. Last, not first: the operator
// who just tabbed into the transcript is almost always looking at the
// newest turn, and seeding at the top of the window would make the
// common case a run of down-presses. Whichever it picks is already
// visible, so entering focus mode never scrolls.
func (m *Model) chatSeedSelection() {
	n := m.history.Len()
	if n == 0 {
		m.selIdx = 0
		return
	}
	last, keep := -1, false
	m.chatVisitWindow(func(i int) {
		if i >= n {
			return // the live tail is not selectable
		}
		if i == m.selIdx {
			keep = true
		}
		if i > last {
			last = i
		}
	})
	switch {
	case keep:
	case last >= 0:
		m.selIdx = last
	default:
		// The window shows only the live tail (a long in-progress
		// answer). The item above it is the one to land on.
		m.selIdx = n - 1
	}
}

// chatRevealSelection scrolls the minimum distance that puts the
// selected item on screen, and nothing at all when it already is.
//
// Bounded like everything else on this path: the forward probe gives
// up after one window's worth of lines, and the fallback walks up from
// the selected item for one window's worth more. Neither can be made
// to measure the whole transcript, which is the rule chatlist.go's
// header sets out.
func (m *Model) chatRevealSelection() {
	n := m.history.Len()
	height := m.viewport.Height()
	if n == 0 || height <= 0 {
		return
	}
	sel := min(atLeast(m.selIdx, 0), n-1)
	m.selIdx = sel

	if sel < m.viewport.offsetIdx {
		m.viewport.offsetIdx, m.viewport.offsetLine = sel, 0
		return
	}
	// An item taller than the window can never be fully revealed, so
	// reveal its head. Pinning its foot would scroll past everything
	// the operator is about to read.
	h := len(m.chatRowLines(sel))
	if h >= height {
		if sel > m.viewport.offsetIdx {
			m.viewport.offsetIdx, m.viewport.offsetLine = sel, 0
		}
		return
	}
	// A partly scrolled top row contributes only the lines below the
	// fold, so the budget starts in deficit by offsetLine — the same
	// accounting chatVisitWindow does.
	used := -m.viewport.offsetLine
	for i := m.viewport.offsetIdx; i < sel && used < height; i++ {
		used += len(m.chatRowLines(i))
	}
	if used >= 0 && used+h <= height {
		return
	}
	m.viewport.offsetIdx, m.viewport.offsetLine = m.chatEndOffset(sel)
}

// clampChatSelection drags the cursor back inside the transcript
// after the content changed under it — a /clear, a session switch, a
// resume. Called from refreshViewport next to clampChatOffset, for
// the same reason and at the same cost.
func (m *Model) clampChatSelection() {
	n := m.history.Len()
	if n == 0 {
		m.selIdx = 0
		return
	}
	m.selIdx = min(atLeast(m.selIdx, 0), n-1)
}

// resetChatSelection forgets both the cursor and every fold. Called
// where the transcript is replaced wholesale rather than appended to,
// alongside the listCache reset those paths already do: the folds are
// keyed by Message.ID and History hands out fresh IDs after a reset,
// so nothing would be misapplied — but the operator asked for a new
// transcript and getting the old one's folds back is not that.
func (m *Model) resetChatSelection() {
	m.selIdx = 0
	m.collapsed = nil
}

// ---------------------------------------------------------------
// The fold
// ---------------------------------------------------------------

// chatToggleCollapsed folds or unfolds the selected item.
//
// The follow branch is the whole subtlety: folding the newest item
// while pinned to the tail moves the tail up by however many lines
// went away, and an operator watching a stream expects to stay
// pinned. Re-pinning here rather than letting syncFollow observe the
// gap is the same rule the rest of the transcript follows — follow is
// tracked intent, not a measurement (issue #93).
func (m *Model) chatToggleCollapsed() {
	msg, ok := m.chatSelectedMessage()
	if !ok {
		return
	}
	if m.collapsed == nil {
		m.collapsed = make(map[uint64]bool)
	}
	if m.collapsed[msg.ID] {
		delete(m.collapsed, msg.ID)
	} else {
		m.collapsed[msg.ID] = true
	}
	m.clampChatOffset()
	if m.follow {
		m.chatGotoBottom()
		return
	}
	m.chatRevealSelection()
}

// chatSelectedMessage is the history entry under the cursor.
func (m Model) chatSelectedMessage() (Message, bool) {
	if m.selIdx < 0 || m.selIdx >= m.history.Len() {
		return Message{}, false
	}
	return m.history.at(m.selIdx)
}

// chatCollapsedRow folds a rendered item down to its head plus a
// count of what is hidden.
//
// Returns the input untouched when folding would save nothing — an
// item of four lines or fewer is already shorter than its own summary
// would make it, and a fold that grows the item is a bug the operator
// would have to discover by pressing space twice.
//
// The lines it returns are a fresh slice: the ones it copies from
// belong to the render cache, and clampChatLines writes in place.
func (m Model) chatCollapsedRow(lines []string) []string {
	if len(lines) <= chatCollapsedLines+1 {
		return lines
	}
	out := make([]string, 0, chatCollapsedLines+1)
	out = append(out, lines[:chatCollapsedLines]...)
	summary := fmt.Sprintf("%s %d more lines", GlyphCollapsed, len(lines)-chatCollapsedLines)
	return append(out, clampChatLines([]string{m.styles.Muted.Render(summary)}, m.viewport.Width())...)
}

// chatRowCollapsed reports whether history row i is folded.
func (m Model) chatRowCollapsed(msg Message) bool {
	return len(m.collapsed) > 0 && m.collapsed[msg.ID]
}

// ---------------------------------------------------------------
// The marker
// ---------------------------------------------------------------

// chatRowMarked reports whether row i draws the selection bar.
//
// Only while the transcript holds the keyboard: with focus in the
// composer the cursor is not what the operator is driving, and a
// marker that stays lit would claim otherwise. The state survives the
// round trip either way — it is drawing, not selection, that the
// focus gates.
func (m Model) chatRowMarked(i int) bool {
	return m.focus == focusTranscript && i == m.selIdx && i < m.history.Len()
}

// chatGutterPrefixes returns the marked and unmarked gutters, built
// once per frame because every drawn line takes one of them.
func (m Model) chatGutterPrefixes() (marked, plain string) {
	return m.styles.Accent.Render(GlyphSelectBar) + " ", strings.Repeat(" ", chatGutterWidth)
}
