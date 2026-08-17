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

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Compositing a modal over the frame instead of in place of it
// (issue #156).
//
// View used to finish a modal frame with
//
//	body = lipgloss.Place(m.width, m.height, Center, Center, modal)
//
// which reads like an overlay and is not one. Place does not layer:
// it ignores whatever was composed before it and returns a fresh
// block of the requested size with the modal centred in blank space.
// So opening any dialog wiped the transcript, the header, the input
// box and the footer, and reappearing them on Esc read as a mode
// switch rather than as a sheet lifting off.
//
// What replaces it is a splice, not a canvas. Measured against a
// full cell-buffer compositor on a 100x40 frame the splice was both
// smaller and cheaper (about 0.9 ms / 75 KB against 1.1 ms / 522 KB),
// and a cell buffer would have to re-derive SGR runs for every cell
// it copies. Rows the modal does not touch are passed through by
// reference — for a 12-row modal on a 40-row frame that is 28 rows
// that cost nothing at all.
//
// The whole file works in terminal CELLS, never bytes or runes: every
// measurement is ansi.StringWidth and every cut is ansi.Truncate or
// ansi.TruncateLeft, so a frame carrying CJK, emoji or styled text
// splices at the same column the operator sees.

// compositeModal draws block over bg, centred, on a canvas of exactly
// width x height cells.
//
// The centring arithmetic is placeCenterOffset — deliberately the
// same function cursor.go uses to re-derive the modal's origin. The
// two have to agree exactly or the hardware cursor lands in a cell
// the modal is not actually drawn in, and nothing would catch it: the
// golden corpus captures tea.View.Content, not tea.View.Cursor.
// Keeping one function rather than two copies of `gap / 2` is what
// makes that agreement structural.
func compositeModal(bg, block string, width, height int) string {
	if width <= 0 || height <= 0 {
		return block
	}
	x := placeCenterOffset(width, lipgloss.Width(block))
	y := placeCenterOffset(height, lipgloss.Height(block))
	return overlayAt(fitCanvas(bg, width, height), block, x, y, width)
}

// fitCanvas brings bg to exactly width x height cells: rows past the
// bottom dropped, short rows padded, long rows truncated, missing
// rows appended blank.
//
// A modal cannot be spliced into a ragged background. The row it
// lands on has to be at least x cells wide before there is an x to
// splice at, and the rows below the background's last one have to
// exist before anything can be written into them. lipgloss.Place got
// this for free by discarding the background; paying for it
// explicitly is the cost of keeping the background.
//
// Only the modal path calls this. Applying it to every frame would
// pad the trailing edge of rows that are currently emitted short,
// which is bytes on the wire for no visible difference — and would
// churn every golden in the corpus for a frame nobody is looking at
// differently.
func fitCanvas(bg string, width, height int) string {
	lines := strings.Split(bg, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i, ln := range lines {
		lines[i] = fitRow(ln, width)
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return strings.Join(lines, "\n")
}

// overlayAt splices block into bg with its top-left cell at (x, y).
// bg must already be a rectangle of width cells per row (fitCanvas);
// rows the block would need past the end of bg are appended blank.
//
// Rows outside the block's span are returned untouched — not
// re-measured, not re-emitted — which is what makes this cheaper than
// a compositor for the shape it is used in: a small modal on a large
// frame.
func overlayAt(bg, block string, x, y, width int) string {
	if block == "" {
		return bg
	}
	rows := strings.Split(bg, "\n")
	blockRows := strings.Split(block, "\n")
	blockW := lipgloss.Width(block)

	for i, br := range blockRows {
		r := y + i
		if r < 0 {
			continue
		}
		for r >= len(rows) {
			rows = append(rows, strings.Repeat(" ", width))
		}
		rows[r] = spliceRow(rows[r], fitRow(br, blockW), x, blockW, width)
	}
	return strings.Join(rows, "\n")
}

// spliceRow returns row with cells [x, x+blockW) replaced by block.
// row must already be exactly width cells, which is what fitCanvas
// exists to guarantee, so the piece to the block's right is exactly
// width-x-blockW cells before any rounding.
//
// The three pieces are cut and re-fitted rather than concatenated raw,
// because a cut through styled or wide text is where an overlay goes
// wrong in ways that are invisible until a host ships colour:
//
//   - The left piece may end with a colour still switched on, which
//     would bleed into the modal's first cell. fitRow closes it.
//   - The right piece has to resume whatever the background was
//     wearing at the cut. ansi.TruncateLeft re-emits the SGR sequences
//     it passed over, so it already does; what it does not do is
//     guarantee a cell count.
//   - Both cuts can land inside a double-width rune, and the two
//     functions round it opposite ways: ansi.Truncate drops the
//     straddling rune, ansi.TruncateLeft keeps it. So the right piece
//     comes back either a cell short or a cell early, and each needs
//     its own correction below.
func spliceRow(row, block string, x, blockW, width int) string {
	left := fitRow(ansi.Truncate(row, x, ""), x)

	var right string
	if want := width - x - blockW; want > 0 {
		right = ansi.TruncateLeft(row, x+blockW, "")
		if ansi.StringWidth(right) > want {
			// The cut landed inside a double-width rune and
			// TruncateLeft kept it whole, so the piece begins one cell
			// to the left of the column it is about to be drawn at.
			// Drop that rune rather than shifting the whole tail right
			// by a cell: its other half is under the modal, and half a
			// glyph cannot be drawn.
			right = ansi.TruncateLeft(right, 2, "")
		}
		if w := ansi.StringWidth(right); w < want {
			// Measured again after the cuts, not folded into the
			// branch above: dropping a straddling rune is also how the
			// piece ends up a cell short. Pad at the HEAD, because the
			// missing cell is the half the modal covers — padding the
			// tail instead would slide the whole surviving background
			// one column left, under the modal.
			right = strings.Repeat(" ", want-w) + right
		}
	}
	return left + block + right
}
