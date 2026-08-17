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
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The frame corpus exercises compositeModal on real dialogs over a real
// transcript, which is the integration this file does not repeat. What
// it cannot reach is the awkward input: styled runs cut mid-colour,
// double-width runes straddling the splice column, backgrounds shorter
// or narrower than the canvas. Those are the cases where an overlay
// looks right in ASCII and shears the moment a host ships colour or an
// operator pastes CJK, so they are asserted here in cells.

// cellRows strips SGR and splits, which is the only way to compare an
// overlay by column: the styled string's byte offsets say nothing about
// where a cell is.
func cellRows(s string) []string {
	return strings.Split(ansi.Strip(s), "\n")
}

// assertRect fails unless every row of s is exactly width cells and
// there are exactly height of them. An overlay that is off by one cell
// on one row is a scrollbar or a border that steps sideways, which is
// exactly the class of bug #157 was.
func assertRect(t *testing.T, s string, width, height int) {
	t.Helper()
	rows := strings.Split(s, "\n")
	if len(rows) != height {
		t.Errorf("composite is %d rows, want %d", len(rows), height)
	}
	for i, r := range rows {
		if w := ansi.StringWidth(r); w != width {
			t.Errorf("row %d is %d cells, want %d: %q", i, w, width, ansi.Strip(r))
		}
	}
}

func TestCompositeModal_BackgroundSurvivesOutsideTheBlock(t *testing.T) {
	const w, h = 20, 7
	bg := strings.TrimSuffix(strings.Repeat(strings.Repeat("b", w)+"\n", h), "\n")
	block := "MMMM\nMMMM\nMMMM"

	got := compositeModal(bg, block, w, h)
	assertRect(t, got, w, h)
	rows := cellRows(got)

	// 3 rows centred in 7 starts at 2; 4 cells centred in 20 starts at 8.
	for i, want := range []string{
		"bbbbbbbbbbbbbbbbbbbb",
		"bbbbbbbbbbbbbbbbbbbb",
		"bbbbbbbbMMMMbbbbbbbb",
		"bbbbbbbbMMMMbbbbbbbb",
		"bbbbbbbbMMMMbbbbbbbb",
		"bbbbbbbbbbbbbbbbbbbb",
		"bbbbbbbbbbbbbbbbbbbb",
	} {
		if rows[i] != want {
			t.Errorf("row %d:\n got  %q\n want %q", i, rows[i], want)
		}
	}
}

func TestCompositeModal_CentringMatchesTheCursorOrigin(t *testing.T) {
	// modalCursor offsets the caret by placeCenterOffset on both axes.
	// If compositeModal ever centres by a different rule the caret
	// lands in a cell the modal is not drawn in, and no golden would
	// notice: the corpus captures tea.View.Content, not the cursor.
	for _, tc := range []struct{ w, h, bw, bh int }{
		{20, 7, 4, 3},   // both gaps even
		{21, 8, 4, 3},   // both gaps odd
		{20, 7, 20, 7},  // exact fit, no margin at all
		{10, 4, 40, 20}, // block larger than the canvas
	} {
		bg := strings.TrimSuffix(strings.Repeat(strings.Repeat(".", tc.w)+"\n", tc.h), "\n")
		block := strings.TrimSuffix(strings.Repeat(strings.Repeat("#", tc.bw)+"\n", tc.bh), "\n")
		rows := cellRows(compositeModal(bg, block, tc.w, tc.h))

		wantX := placeCenterOffset(tc.w, tc.bw)
		wantY := placeCenterOffset(tc.h, tc.bh)
		if wantY >= len(rows) {
			continue
		}
		if got := strings.IndexByte(rows[wantY], '#'); got != wantX {
			t.Errorf("%dx%d canvas, %dx%d block: first block cell on row %d is at column %d, "+
				"but modalCursor will offset the caret by %d",
				tc.w, tc.h, tc.bw, tc.bh, wantY, got, wantX)
		}
	}
}

func TestFitCanvas_ClipsPadsAndSquares(t *testing.T) {
	// Three raggednesses at once: a short row, an over-long row, and
	// fewer rows than the canvas has.
	got := fitCanvas("ab\nabcdefgh", 5, 4)
	assertRect(t, got, 5, 4)
	want := []string{"ab   ", "abcde", "     ", "     "}
	for i, r := range cellRows(got) {
		if r != want[i] {
			t.Errorf("row %d: got %q, want %q", i, r, want[i])
		}
	}

	// Rows past the bottom are dropped rather than left to push the
	// frame past the terminal, which is clipFrame's contract too.
	if rows := cellRows(fitCanvas("a\nb\nc\nd", 1, 2)); len(rows) != 2 {
		t.Errorf("fitCanvas kept %d of 4 rows for a 2-row canvas", len(rows))
	}
}

func TestSpliceRow_RightPieceResumesTheBackgroundStyle(t *testing.T) {
	// The whole background row is red. Splicing a plain block into its
	// middle must leave the tail still red: a terminal that never sees
	// the SGR again paints the rest of the row in the modal's colour.
	const red, reset = "\x1b[31m", "\x1b[0m"
	row := red + strings.Repeat("b", 12) + reset

	got := spliceRow(row, "MMMM", 4, 4, 12)
	if w := ansi.StringWidth(got); w != 12 {
		t.Fatalf("spliced row is %d cells, want 12: %q", w, ansi.Strip(got))
	}
	if plain := ansi.Strip(got); plain != "bbbbMMMMbbbb" {
		t.Fatalf("spliced cells are %q, want %q", plain, "bbbbMMMMbbbb")
	}
	head, tail, ok := strings.Cut(got, "MMMM")
	if !ok {
		t.Fatalf("block not found in %q", got)
	}
	if !strings.Contains(tail, red) {
		t.Errorf("right piece does not re-open the background colour, so the modal's style "+
			"bleeds across the rest of the row: %q", tail)
	}
	if !strings.HasSuffix(head, "\x1b[0m") && !strings.HasSuffix(head, "\x1b[m") {
		t.Errorf("left piece does not close its colour before the block starts, "+
			"so the background bleeds into the modal's first cell: %q", head)
	}
}

func TestSpliceRow_CutsThroughWideRunesKeepTheRowSquare(t *testing.T) {
	// Every column of a 12-cell row of double-width runes: half the
	// splices cut a rune in two on at least one side, and ansi.Truncate
	// drops the straddling rune rather than half-drawing it. Padding
	// has to make up the cell either way or the row shears.
	row := strings.Repeat("日", 6)
	for x := 0; x <= 8; x++ {
		got := spliceRow(row, "MMMM", x, 4, 12)
		if w := ansi.StringWidth(got); w != 12 {
			t.Errorf("splice at column %d produced %d cells, want 12: %q", x, w, ansi.Strip(got))
		}
		// The block itself must land on the columns it was promised,
		// not slide left into the hole a dropped rune leaves.
		if at := ansi.StringWidth(ansi.Strip(strings.SplitN(ansi.Strip(got), "M", 2)[0])); at != x {
			t.Errorf("splice at column %d put the block at column %d", x, at)
		}
	}
}

func TestOverlayAt_GrowsTheCanvasRatherThanDroppingRows(t *testing.T) {
	// A block placed past the background's last row has to bring the
	// rows it needs with it. Silently dropping them is how a modal
	// loses the footer hint that closes it — the #147/#149 shape.
	got := overlayAt("aaaa", "MM\nMM", 1, 1, 4)
	rows := cellRows(got)
	if len(rows) != 3 {
		t.Fatalf("overlay produced %d rows, want 3: %q", len(rows), rows)
	}
	for i, want := range []string{"aaaa", " MM ", " MM "} {
		if rows[i] != want {
			t.Errorf("row %d: got %q, want %q", i, rows[i], want)
		}
	}
}

func TestOverlayAt_EmptyBlockIsIdentity(t *testing.T) {
	bg := "aaaa\nbbbb"
	if got := overlayAt(bg, "", 0, 0, 4); got != bg {
		t.Errorf("empty block changed the background: %q", got)
	}
}

func TestCompositeModal_DegenerateSizes(t *testing.T) {
	// A zero-sized terminal reaches View through the Bubble Tea startup
	// window before the first WindowSizeMsg. Returning the block
	// unmodified is what the frame invariants already expect of that
	// state; padding to zero cells would return the empty string and
	// the modal would simply not exist.
	if got := compositeModal("bg", "MM", 0, 5); got != "MM" {
		t.Errorf("zero width: got %q, want %q", got, "MM")
	}
	if got := compositeModal("bg", "MM", 5, 0); got != "MM" {
		t.Errorf("zero height: got %q, want %q", got, "MM")
	}
}
