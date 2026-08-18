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

// Footer key legends never wrap between a key and its action
// (issue #230).
//
// wordWrap breaks on spaces, and the space inside "enter accept" is
// as good a break point as the ones around the " · " separator — so a
// legend one word too long for the box could put `enter` at the end
// of one row and `accept` at the start of the next, which reads as a
// binding with no key on it. keyLegend binds each pair with U+00A0;
// this file is the assertion that every modal footer goes through it.
//
// The check is written against the frame rather than against
// keyLegend's return value on purpose. Asserting keyLegend(pair) is
// in the render would be a tautology — the same function would supply
// both sides, and deleting the binding would move both — so the
// expectation spells the U+00A0 itself.

package tui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// legendNBSP is U+00A0, spelled here rather than borrowed from
// keyLegend so removing the binding from keyLegend fails these tests
// instead of silently relaxing them.
const legendNBSP = " "

// unbindLegend turns a rendered legend back into the phrase an
// operator reads. A pair that genuinely wrapped is NOT repaired by
// it: the break puts a newline and a box edge between the two words,
// and normalizing spaces removes neither.
func unbindLegend(s string) string { return strings.ReplaceAll(s, legendNBSP, " ") }

// bindLegendPair is the shape a pair must have on the frame.
func bindLegendPair(pair string) string { return strings.ReplaceAll(pair, " ", legendNBSP) }

// TestModalFooters_KeysStayWithTheirActions drives every modal that
// has a key legend at a narrow and a roomy width, and asserts each
// pair arrives bound.
//
// A pair the surface does not show at a given size is skipped rather
// than failed — the tool-call overlay drops "← → walk" in a
// single-call session, and both scroll hints appear only once the
// body overflows. Presence is probed on the ACTION word, which is
// distinctive within a footer, and the subtest fails if a case
// exercised nothing at all, so a skip cannot quietly become the whole
// test.
func TestModalFooters_KeysStayWithTheirActions(t *testing.T) {
	for _, w := range []int{40, 100} {
		for _, tc := range modalFitCases() {
			if len(tc.footerPairs) == 0 {
				continue
			}
			t.Run(tc.name+"/"+strconv.Itoa(w), func(t *testing.T) {
				m := tc.open(t, w, 24)
				plain := ansi.Strip(tc.render(&m))
				checked := 0
				for _, pair := range tc.footerPairs {
					action := pair[strings.LastIndex(pair, " ")+1:]
					if !strings.Contains(unbindLegend(plain), action) {
						continue
					}
					checked++
					if !strings.Contains(plain, bindLegendPair(pair)) {
						t.Errorf("%q is not bound in the footer at width %d — its key can "+
							"wrap away from its action\n%s", pair, w, unbindLegend(plain))
					}
				}
				if checked == 0 {
					t.Errorf("no footer pair of %v was exercised at width %d; the case's "+
						"pairs no longer describe what it renders\n%s",
						tc.footerPairs, w, unbindLegend(plain))
				}
			})
		}
	}
}

// TestModalFooters_LegendsBreakOnASeparator is the other half of the
// claim, and it is about where the break LANDS rather than that a
// pair survived it.
//
// Binding the pairs must not make the legend one unbreakable token:
// wordWrap falls back to a hard break when a single word is wider
// than the box, so a "fix" that bound the separators too would slice
// the legend mid-word. It must not truncate either — a legend that
// fits by losing its tail has no wrapped pair to catch. So every
// footer row has to START with one of the keys, and all four pairs
// have to be on the frame.
//
// The theme picker at 40 columns is the issue's own worked example:
// four pairs, 55 cells, a box that holds 36.
func TestModalFooters_LegendsBreakOnASeparator(t *testing.T) {
	pairs := []string{"type to filter", "↑↓ preview", "enter accept", "esc cancel"}
	keys := []string{"type", "↑↓", "enter", "esc"}

	m := newFrameModel(StatusHeader, 40, 24)
	askThemePicker(&m)
	block := ansi.Strip(m.overlayStack.render(m.width, &m))

	// Footer rows are the boxed rows carrying a bound pair. The
	// placeholder row says "type to filter" as well, with ordinary
	// spaces, so matching the BOUND form is what separates the two.
	var footer []string
	for _, line := range strings.Split(block, "\n") {
		inner, ok := modalRowContent(line)
		if !ok {
			continue
		}
		for _, p := range pairs {
			if strings.Contains(inner, bindLegendPair(p)) {
				footer = append(footer, inner)
				break
			}
		}
	}
	if len(footer) < 2 {
		t.Fatalf("the legend did not wrap at 40 columns (%d footer rows)\n%s", len(footer), block)
	}
	for _, row := range footer {
		if !strings.HasPrefix(row, keys[0]) && !strings.HasPrefix(row, keys[1]) &&
			!strings.HasPrefix(row, keys[2]) && !strings.HasPrefix(row, keys[3]) {
			t.Errorf("a footer row starts mid-legend rather than on a key: %q\n%s", row, block)
		}
	}
	joined := unbindLegend(strings.Join(footer, " "))
	for _, p := range pairs {
		if !strings.Contains(joined, p) {
			t.Errorf("%q was truncated off the legend rather than wrapped\n%s", p, block)
		}
	}
}

// modalRowContent strips a modal row's box edge and padding, and
// reports false for a row that is not one — the top and bottom
// rules, and anything composited beside the block.
func modalRowContent(line string) (string, bool) {
	l, r := strings.Index(line, "│"), strings.LastIndex(line, "│")
	if l < 0 || r <= l {
		return "", false
	}
	return strings.TrimSpace(line[l+len("│") : r]), true
}
