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

// Issue #199: every modal surface wears a box edge, and it costs two
// columns of body.
//
// The two halves are tested separately because they fail separately. A
// surface can carry the edge and still overrun — that is #157, #159 and
// #210, three bugs of exactly the shape "someone subtracted the old
// number" — and the overrun is invisible in a screenshot, because
// lipgloss wraps the offending row and puts an edge glyph on the
// continuation too.

package tui

import (
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// modalEdgeWidths spans the range a dialog actually meets: below the
// 30-column floor, at the floor, through the widths where the modal is
// still tracking the terminal, and past the point where it stops
// growing and the terminal gets wider around it.
var modalEdgeWidths = []int{24, 34, 40, 60, 76, 100, 160}

// TestModalEdge_EverySurfaceCarriesIt is the "all five or none" half of
// #199. Four surfaces out of five reads as a rendering bug rather than
// as a style, so the assertion runs over the whole modal corpus — every
// dialog on the Overlay stack, the permission overlay, the elicitation,
// the side answer and the embedded huh form — at every width.
//
// It asserts on the block each surface returns, which is the block
// View splices over the frame. Going through Model.modalFrame for the
// form rather than reaching for modalSurface directly is deliberate:
// the claim is that the cascade cannot grow a sixth surface that misses
// the treatment, and that is a claim about the cascade.
func TestModalEdge_EverySurfaceCarriesIt(t *testing.T) {
	const h = 40

	cases := modalFitCases()
	for _, tc := range cases {
		for _, w := range modalEdgeWidths {
			t.Run(tc.name+"/"+strconv.Itoa(w), func(t *testing.T) {
				m := tc.open(t, w, h)
				assertModalEdge(t, tc.render(&m), h)
			})
		}
	}

	// The huh form is the fifth surface and the only one that is not
	// a RenderContext: it brings its own layout, sizes its own block
	// and asks modalSurface for a row of vertical padding. It has no
	// entry in modalFitCases because there is no chrome for the fit
	// pass to shed, which is exactly how a surface goes unnoticed.
	for _, w := range modalEdgeWidths {
		t.Run("huh-form/"+strconv.Itoa(w), func(t *testing.T) {
			m := newFrameModel(StatusHeader, w, h)
			m.pendingForm = newPricingForm("gpt-4o", w)
			block, ok := m.modalFrame()
			if !ok {
				t.Fatal("a model holding a pendingForm has no modal frame — " +
					"the form is not in the cascade any more")
			}
			assertModalEdge(t, block, h)
		})
	}
}

// TestModalEdge_BodyFitsTheReducedColumn is the other half, and the one
// the border alone would not catch.
//
// A body row composed against the pre-#199 column is two cells too wide
// for the box. Nothing rejects it: lipgloss wraps it, and the two
// leftover cells become a continuation row with an edge glyph at each
// end, so the modal still looks like a modal. What it costs is a row —
// off a budget fitModalContent then balances by shedding the spacing,
// or, lower down, the body itself. So this counts rows rather than
// looking at them.
//
// The theme picker at 20 rows is the sharpest case available: the list
// is longer than the window, so every body row goes through fitRow and
// is padded to exactly modalBodyWidth, and the expected height is then
// a closed form. Any surviving hand-copied width-4 puts the block over
// it.
func TestModalEdge_BodyFitsTheReducedColumn(t *testing.T) {
	const h = 20

	for _, w := range modalEdgeWidths {
		t.Run(strconv.Itoa(w), func(t *testing.T) {
			m := newFrameModel(StatusHeader, w, h)
			askThemePicker(&m)
			block := m.overlayStack.Render(m.width, &m)

			bw := lipgloss.Width(block)
			inner := modalInnerWidth(bw)
			view := modalBodyHeight(h, modalPickerChromeRows)
			if view >= len(BuiltinThemes()) {
				t.Fatalf("the window (%d rows) is not shorter than the list (%d themes), so the "+
					"rows are not cut to the body column and this case proves nothing",
					view, len(BuiltinThemes()))
			}
			footer := "type to filter " + GlyphSeparator + " ↑↓ preview " +
				GlyphSeparator + " enter accept " + GlyphSeparator + " esc cancel"

			// Title, blank, filter row, the window, blank, footer
			// rule, the footer hint as it wraps — and the edge.
			want := 1 + 1 + 1 + view + 1 + 1 + wrappedContentRows(footer, inner) + modalEdgeRows
			if got := lipgloss.Height(block); got != want {
				t.Errorf("theme picker is %d rows at width %d, want %d. Either a body row "+
					"wrapped past the %d-column body — a width computation that has not been "+
					"told the edge costs %d columns — or the edge is not on the block and its "+
					"%d rows are missing\n%s",
					got, w, want, modalBodyWidth(bw), modalEdgeCols, modalEdgeRows,
					strings.Join(modalContentLines(block), "\n"))
			}
		})
	}
}
