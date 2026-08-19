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

// Off-screen spinner suppression (issue #248).
//
// "Zero renders while off screen" and "resumes correctly" are two
// different claims and they fail in opposite directions — the first by
// animating something nobody can see, the second by leaving the
// operator watching a frozen glyph on a turn that is still running.
// They get a test each, on purpose.

package tui

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// offscreenSpinnerModel returns a streaming Model with a transcript
// long enough to scroll, settled through one coalesced refresh so the
// live tail has actually been built — chatTailOffScreen answers "not
// off screen" for a tail that does not exist yet, which is the right
// answer and the wrong starting state for these tests.
func offscreenSpinnerModel(t *testing.T) Model {
	t.Helper()
	m := NewModel(Options{Agent: stubAgent{}})
	// A real sizing, not viewport.SetWidth: refreshViewport returns
	// early while m.width is zero, so a hand-sized viewport builds no
	// tail at all.
	out, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = out.(Model)
	for i := range 40 {
		n := strconv.Itoa(i)
		m.history.Append(Message{Role: RoleUser, Text: "question " + n})
		m.history.Append(Message{Role: RoleAssistant, Text: "answer " + n})
	}
	m = m.submitTurn("one more")
	// The paint the program would have done before the first tick
	// arrived. Direct rather than through coalescedRefreshMsg because
	// submitTurn leaves nothing dirty for that handler to service, and
	// the dirty flag is cleared by hand for the same reason: what the
	// tests below read is whether a TICK asked for a repaint.
	m.refreshViewport()
	m.viewportDirty = false
	if len(m.chatTail) == 0 {
		t.Fatal("setup: the live tail was not built, so there is no animation to hide")
	}
	if m.viewportDirty {
		t.Fatal("setup: the model is still dirty after a refresh")
	}
	return m
}

// scrollAwayFromTail scrolls to the top of the transcript and syncs
// follow the way the key handler does, which is what detaches the
// window from the tail.
func scrollAwayFromTail(t *testing.T, m Model) Model {
	t.Helper()
	m.chatGotoTop()
	m.syncFollow()
	if !m.chatTailOffScreen() {
		t.Fatal("setup: the tail is still on screen after scrolling to the top")
	}
	return m
}

// TestSpinnerOffScreen_TickRendersNothing is the first clause: a tick
// that lands while the operator is reading scrollback advances nothing
// and paints nothing.
//
// The tail is the assertion rather than a render counter because it is
// the thing that would have been re-rendered: buildChatTail is the only
// producer of these lines and refreshViewport is its only caller, so a
// tail that is byte-identical after forty ticks is a tail that was
// never rebuilt.
func TestSpinnerOffScreen_TickRendersNothing(t *testing.T) {
	m := scrollAwayFromTail(t, offscreenSpinnerModel(t))
	frameBefore := m.spinnerFrame
	tailBefore := strings.Join(m.chatTail, "\n")

	for i := range 40 {
		out, cmd := m.Update(spinnerTickMsg{gen: m.spinnerGen})
		m = out.(Model)
		if cmd == nil {
			t.Fatalf("tick %d did not re-arm — a chain torn down off screen has nothing to resume", i)
		}
		if m.viewportDirty {
			t.Fatalf("tick %d marked the viewport dirty, which is a repaint of a frame the "+
				"operator is not looking at", i)
		}
	}

	if m.spinnerFrame != frameBefore {
		t.Errorf("spinnerFrame advanced %d frames off screen (%d -> %d) — the animation is running "+
			"for nobody", m.spinnerFrame-frameBefore, frameBefore, m.spinnerFrame)
	}

	// The paint half gets its chance anyway. Nothing marked dirty, so
	// this services nothing — and a tail that changed regardless would
	// mean the render is happening somewhere the dirty flag does not
	// account for.
	out, _ := m.Update(coalescedRefreshMsg{})
	m = out.(Model)
	if got := strings.Join(m.chatTail, "\n"); got != tailBefore {
		t.Error("the live tail was rebuilt while off screen — that is one item render per tick, " +
			"which is the cost the pause exists to remove")
	}
}

// TestSpinnerOffScreen_ResumesWhenScrolledBack is the second clause,
// and the one a pause implemented as "stop the chain" would fail: the
// operator scrolls back to the tail and the glyph has to be moving
// again by the next tick, not by the next keystroke.
func TestSpinnerOffScreen_ResumesWhenScrolledBack(t *testing.T) {
	m := scrollAwayFromTail(t, offscreenSpinnerModel(t))
	for range 5 {
		out, _ := m.Update(spinnerTickMsg{gen: m.spinnerGen})
		m = out.(Model)
	}
	paused := strings.Join(m.chatTail, "\n")
	frameBefore := m.spinnerFrame

	m.chatGotoBottom()
	m.syncFollow()
	if m.chatTailOffScreen() {
		t.Fatal("setup: the tail is still off screen after scrolling back to the bottom")
	}

	out, cmd := m.Update(spinnerTickMsg{gen: m.spinnerGen})
	m = out.(Model)
	if cmd == nil {
		t.Fatal("the first tick after scrolling back did not re-arm")
	}
	if m.spinnerFrame != frameBefore+1 {
		t.Errorf("spinnerFrame = %d, want %d — the animation did not resume on the first tick back",
			m.spinnerFrame, frameBefore+1)
	}
	if !m.viewportDirty {
		t.Error("the resuming tick did not ask for a repaint, so the moving glyph never reaches the frame")
	}

	out, _ = m.Update(coalescedRefreshMsg{})
	m = out.(Model)
	if got := strings.Join(m.chatTail, "\n"); got == paused {
		t.Error("the tail is byte-identical to the paused one after resuming — the glyph is still frozen")
	}
}

// TestSpinnerOffScreen_FollowingTailAlwaysAnimates guards the common
// case against the pause. A following window is pinned to the end of
// the transcript, which is where the tail is, so it is never off
// screen — and a predicate that got this wrong would freeze the
// spinner for every operator who never scrolls, which is most of them.
func TestSpinnerOffScreen_FollowingTailAlwaysAnimates(t *testing.T) {
	m := offscreenSpinnerModel(t)
	if !m.follow {
		t.Fatal("setup: the model is not following its own tail")
	}
	if m.chatTailOffScreen() {
		t.Fatal("a followed tail reported off screen")
	}
	frameBefore := m.spinnerFrame

	out, _ := m.Update(spinnerTickMsg{gen: m.spinnerGen})
	if got := out.(Model).spinnerFrame; got != frameBefore+1 {
		t.Errorf("spinnerFrame = %d, want %d — the pause swallowed a tick on the following path",
			got, frameBefore+1)
	}
}

// TestSpinnerOffScreen_UnbuiltTailIsNotHidden pins the negative that
// keeps the spinner from never appearing at all. The tail is built
// inside refreshViewport, so between submitTurn and the first paint
// there is nothing to be visible — and answering "hidden" there would
// suppress the very repaint that brings the animation into existence.
func TestSpinnerOffScreen_UnbuiltTailIsNotHidden(t *testing.T) {
	m := NewModel(Options{Agent: stubAgent{}})
	out, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = out.(Model)
	m = m.submitTurn("go")
	m.chatTail = nil
	m.follow = false

	if m.chatTailOffScreen() {
		t.Error("a tail that has not been built yet reported off screen — the first paint of the " +
			"spinner is the one that would be suppressed")
	}
}
