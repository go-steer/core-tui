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

// Modal fit on a short terminal (issue #142).
//
// The frame invariants (frame_invariant_test.go) only ever measured
// View()'s output, which is clipFrame's output — so a modal composed
// three rows too tall passed the grid on clipFrame's merit rather
// than its own, and what clipFrame trimmed was the bottom: the footer
// rule and the footer key hint. The operator was left with a
// decapitated modal and no on-screen indication of which key closes
// it.
//
// This file measures the modal BEFORE the clamp, across every modal
// surface, at every height from "roomy" down to "two rows". Two
// things are asserted at each cell:
//
//	1. the composed block is <= the terminal height wherever that is
//	   achievable at all
//	2. the footer key hint is still in it
//
// (2) is the one that would have caught the defect. (1) alone is
// satisfied by clipping, which is exactly what was happening.

package tui

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// modalFitHeights brackets the two regimes and the boundary between
// them. 24 and 16 are normal (margin intact); 14 is the first
// fullscreen height; 10 is the brief's worked example (chrome 7 + body
// 3 fits exactly once the margin goes); 8 is where chrome alone fills
// the terminal; 3 and 2 are below that, where the honest answer is a
// title and a footer hint and nothing else.
//
// Every one of those boundaries moved up by modalEdgeRows when the
// modal grew a box edge (issue #199), so the list keeps the old
// landmarks as well: they are interior points of the fullscreen
// regime now, and they still have to behave.
var modalFitHeights = []int{24, 16, 15, 14, 13, 12, 11, 10, 8, 6, 4, 3, 2}

// modalFitCase is one modal surface: how to open it, how to render
// just its own block, and a token from its footer hint that must
// survive to whatever height the modal renders at.
type modalFitCase struct {
	name string
	// open returns a model sized to (w, h) with the modal open.
	open func(t *testing.T, w, h int) Model
	// render returns the modal's composed block — the string
	// lipgloss.Place centers, before clipFrame sees it.
	render func(m *Model) string
	// footer is a substring of the modal's footer key hint, written
	// with ordinary spaces — the assertion normalizes the U+00A0 that
	// keyLegend binds a key to its action with, so a case states the
	// phrase an operator reads and stays silent on where it may
	// break. It is deliberately the CLOSE key in every case: that is
	// the row whose loss strands the operator.
	footer string
	// footerPairs are the key/action pairs the footer is built from,
	// written with ordinary spaces. Empty for a surface whose hint is
	// not a key legend. Read by
	// TestModalFooters_KeysStayWithTheirActions (issue #230), which
	// asserts each pair reaches the frame with its space bound.
	footerPairs []string
	// title is a substring of the modal's title line — the row shed
	// LAST before the footer, and the marker for "this cell is below
	// the floor where anything but the hint fits".
	title string
	// minBodyHeight is the shortest terminal at which this modal
	// still renders a body row (as opposed to title + footer only).
	// Below it the body assertion is skipped, not relaxed. The box
	// edge's own two rows are part of it: they are drawn outside
	// everything fitModalContent is able to shed, so they raise the
	// floor rather than competing with the content for it.
	minBodyHeight int
	// body, when non-empty, is a substring of the first body row —
	// the row the shedding order promises to keep longest.
	body string
}

func modalFitCases() []modalFitCase {
	return []modalFitCase{
		{
			name: "theme-picker",
			open: func(_ *testing.T, w, h int) Model {
				m := newFrameModel(StatusHeader, w, h)
				askThemePicker(&m)
				return m
			},
			render:        func(m *Model) string { return m.overlayStack.Render(m.width, m) },
			footer:        "esc cancel",
			footerPairs:   []string{"type to filter", "↑↓ preview", "enter accept", "esc cancel"},
			title:         "Choose a Theme",
			body:          filterPlaceholder,
			minBodyHeight: modalEdgeRows + 3,
		},
		{
			name: "model-picker",
			open: func(t *testing.T, w, h int) Model {
				m, _ := openModelPickerFixture(t)
				return resizeModel(m, w, h)
			},
			render:        func(m *Model) string { return m.overlayStack.Render(m.width, m) },
			footer:        "esc cancel",
			footerPairs:   []string{"type to filter", "↑↓ choose", "enter accept", "esc cancel"},
			title:         "Choose a Model",
			body:          filterPlaceholder,
			minBodyHeight: modalEdgeRows + 3,
		},
		{
			name: "session-picker",
			open: func(t *testing.T, w, h int) Model {
				m, _ := openSessionPickerFixture(t)
				return resizeModel(m, w, h)
			},
			render:        func(m *Model) string { return m.overlayStack.Render(m.width, m) },
			footer:        "esc cancel",
			footerPairs:   []string{"type to filter", "↑↓ choose", "enter attach", "esc cancel"},
			title:         "Choose a Session",
			body:          filterPlaceholder,
			minBodyHeight: modalEdgeRows + 3,
		},
		{
			name: "permission-modal",
			open: func(_ *testing.T, w, h int) Model {
				m := newFrameModel(StatusHeader, w, h)
				out, _ := m.Update(permissionRequestMsg{req: PermissionRequest{
					Kind:     PermissionKindBash,
					ToolName: "bash",
					Verb:     "rm",
					Detail: "rm -rf /tmp/a-really-quite-long-path/that/keeps/going/" +
						"well/past/any/sensible/terminal/width",
				}})
				return out.(Model)
			},
			render: func(m *Model) string { return m.renderPermissionModal() },
			// permissionKeyHint glues each key to its action with a
			// non-breaking space so the pair never wraps apart.
			footer: "esc deny",
			footerPairs: []string{"y allow once", "n deny", "s allow session",
				"v allow verb", "t allow tool", "a allow always", "esc deny"},
			title:         "Permission required",
			minBodyHeight: modalEdgeRows + 3,
		},
		{
			name: "elicit-modal",
			open: func(_ *testing.T, w, h int) Model {
				m := newFrameModel(StatusHeader, w, h)
				out, _ := m.Update(elicitRequestMsg{
					serverName: "an-mcp-server-with-a-long-name",
					req: ElicitRequest{
						Mode:        ElicitFormMode,
						Title:       "Confirm the deployment target for this rollout",
						Description: "The server needs a project and a region first.",
						Fields: []ElicitField{
							{Name: "project", Description: "GCP project id", Type: ElicitFieldString, Required: true},
							{Name: "region", Description: "Compute region", Type: ElicitFieldEnum,
								EnumChoices: []string{"us-central1", "europe-west4"}},
						},
					},
				})
				return out.(Model)
			},
			render:        func(m *Model) string { return m.overlayStack.Render(m.width, m) },
			footer:        "esc cancel",
			footerPairs:   []string{"tab next field", "enter submit", "ctrl+d decline", "esc cancel"},
			title:         "an-mcp-server-with-a-long-name",
			minBodyHeight: modalEdgeRows + 3,
		},
		{
			// Issue #149. This overlay and the subagent one below
			// were the two the #142 table never named, which is how
			// they kept a body allowance that predated the height
			// regime and a RenderContext that never told
			// fitModalContent how tall the terminal was.
			name: "tool-call",
			open: func(_ *testing.T, w, h int) Model {
				m := newFrameModel(StatusHeader, w, h)
				m.history.Append(Message{
					Role:            RoleTool,
					ToolName:        "read_file",
					ToolCallID:      "call-1",
					ToolArgsMap:     map[string]any{"path": "/etc/hosts"},
					ToolResponseMap: map[string]any{"content": strings.Repeat("a line of the file\n", 30)},
				})
				m.history.Append(Message{
					Role:        RoleTool,
					ToolName:    "grep",
					ToolCallID:  "call-2",
					ToolArgsMap: map[string]any{"pattern": "TODO"},
					ToolError:   "regex compile failed",
				})
				m.overlayStack.Open(newToolCallDialog(2))
				return m
			},
			render:      func(m *Model) string { return m.overlayStack.Render(m.width, m) },
			footer:      "esc close",
			footerPairs: []string{"← → walk", "↑↓ scroll", "esc close"},
			title:       "Tool call detail",
			// The header banner is this dialog's first body row, and
			// the two-of-two counter is the part of it that cannot
			// wrap away.
			body: "2/2",
			// The banner wraps to two rows at 40 columns, so the
			// shortest terminal that holds title + banner + hint is
			// four rows, not three.
			minBodyHeight: modalEdgeRows + 4,
		},
		{
			name: "subagent",
			open: func(_ *testing.T, w, h int) Model {
				m := newFrameModel(StatusHeader, w, h)
				d := newSubagentDialog("auditor")
				d.apply(subagentEventsMsg{
					name:    "auditor",
					info:    SubagentInfo{Name: "auditor", Status: "running"},
					hasInfo: true,
					page: SubagentEventPage{Events: []SubagentEvent{
						{Seq: 1, Author: "model", Text: strings.Repeat("a turn of the subagent log. ", 12)},
						{Seq: 2, Author: "model", Text: strings.Repeat("and another one. ", 12)},
					}},
				})
				// Unpinned, i.e. an operator who has scrolled up to
				// the top of the log. Left pinned the overlay follows
				// the tail by design and the first body row is
				// legitimately off-window, which would make the body
				// assertion below measure the scroll pin rather than
				// the shedding order.
				d.pinned = false
				m.overlayStack.Open(d)
				return m
			},
			render:      func(m *Model) string { return m.overlayStack.Render(m.width, m) },
			footer:      "esc close",
			footerPairs: []string{"↑↓ scroll", "G follow", "esc close"},
			title:       "Subagent",
			// The status banner is the first body row; "running" is
			// its leading chip.
			body:          "running",
			minBodyHeight: modalEdgeRows + 3,
		},
		{
			name: "text-input",
			open: func(_ *testing.T, w, h int) Model {
				m := newFrameModel(StatusHeader, w, h)
				m.overlayStack.Open(NewTextInputDialog(TextInputConfig{
					Title:  "Attach to Endpoint",
					Prompt: "URL:",
				}))
				return m
			},
			render:        func(m *Model) string { return m.overlayStack.Render(m.width, m) },
			footer:        "esc cancel",
			footerPairs:   []string{"enter submit", "esc cancel"},
			title:         "Attach to Endpoint",
			body:          "URL:",
			minBodyHeight: modalEdgeRows + 3,
		},
		{
			name: "side-answer",
			open: func(_ *testing.T, w, h int) Model {
				m := newFrameModel(StatusHeader, w, h)
				m.sideAnswer = &SideAnswer{
					Question: "what does the scheduler do when a node goes unready",
					Answer: strings.Repeat("The node controller marks the node NotReady "+
						"and the pods are evicted after the toleration expires. ", 6),
				}
				return m
			},
			render:        func(m *Model) string { return m.renderSideAnswer() },
			footer:        "dismiss",
			title:         "by the way",
			minBodyHeight: modalEdgeRows + 3,
		},
	}
}

// resizeModel drives a WindowSizeMsg through Update so the model
// re-derives its budget the way a real resize does, rather than
// having m.height poked behind resize()'s back.
func resizeModel(m Model, w, h int) Model {
	out, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return out.(Model)
}

// modalRows is the modal block's height in terminal rows, ignoring a
// trailing newline the way assertFrameFits does.
func modalRows(block string) int {
	lines := strings.Split(block, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return len(lines)
}

// TestModalFit_ShortTerminal is issue #142's table. Every modal
// surface, at every height in modalFitHeights, in both a narrow and a
// wide terminal — narrow because the defect's real magnitude comes
// from body rows WRAPPING, which is invisible in the logical-row
// arithmetic modalBodyHeight does.
func TestModalFit_ShortTerminal(t *testing.T) {
	for _, w := range []int{40, 100} {
		for _, tc := range modalFitCases() {
			for _, h := range modalFitHeights {
				name := tc.name + "/" + strconv.Itoa(w) + "x" + strconv.Itoa(h)
				t.Run(name, func(t *testing.T) {
					m := tc.open(t, w, h)
					block := tc.render(&m)
					if block == "" {
						t.Fatalf("%s rendered nothing at %dx%d", tc.name, w, h)
					}
					// Non-breaking spaces are how keyLegend keeps a key
					// and its action on one line; they read as ordinary
					// spaces, so they are ordinary spaces to the
					// assertions below. A hint that genuinely wrapped
					// mid-phrase still fails: the break puts a newline
					// and a box edge between the two words, and no
					// amount of space-normalizing removes those.
					plain := unbindLegend(ansi.Strip(block))
					hasTitle := strings.Contains(plain, tc.title)

					// (1) The modal composes inside the terminal
					// "wherever that is achievable". The floor is the
					// footer hint's own wrapped height: on a
					// 40-column terminal the permission modal's six
					// key bindings wrap to five rows, and no amount of
					// shedding makes those fit in four. Overflow is
					// therefore tolerated in exactly one shape —
					// everything else already gone, footer alone.
					if got := modalRows(block); got > h && hasTitle {
						t.Errorf("modal is %d rows in a %d-row terminal (overflow %d) with the "+
							"title still on it — a sheddable row was not shed\n%s",
							got, h, got-h, plain)
					}
					// (2) The footer key hint survives. This is the
					// row that says how to close the modal, and
					// clipFrame took it first.
					if !strings.Contains(plain, tc.footer) {
						t.Errorf("footer hint %q was sacrificed at %dx%d\n%s",
							tc.footer, w, h, plain)
					}
					// (3) Where there is a body at all, its first row
					// is the one that survives — the shedding order
					// takes spacing before content and content from
					// the bottom.
					if tc.body != "" && h >= tc.minBodyHeight && hasTitle &&
						!strings.Contains(plain, tc.body) {
						t.Errorf("first body row %q went missing at %dx%d\n%s",
							tc.body, w, h, plain)
					}
				})
			}
		}
	}
}

// TestModalFit_BelowTheChromeFloor pins the honest degradation at
// sizes where nothing helps. Below the height the footer key hint
// itself wraps to, fitModalContent has nothing left to give: it sheds
// the title too and returns the hint alone, and clipFrame trims what
// still doesn't fit. What matters here is that it does not panic, does
// not compose a negative height, spends its last row on the CLOSE key
// rather than on the title, and that View() still emits a frame that
// fits the terminal.
func TestModalFit_BelowTheChromeFloor(t *testing.T) {
	for _, w := range []int{40, 100} {
		for _, h := range []int{1, 2} {
			t.Run(strconv.Itoa(w)+"x"+strconv.Itoa(h), func(t *testing.T) {
				m := newFrameModel(StatusHeader, w, h)
				askThemePicker(&m)
				block := m.overlayStack.Render(m.width, &m)
				plain := unbindLegend(ansi.Strip(block))
				if !strings.Contains(plain, "esc cancel") {
					t.Errorf("the last row spent is not the close key\n%s", plain)
				}
				if h == 1 && strings.Contains(plain, "Choose a Theme") {
					t.Errorf("a one-row terminal cannot hold both the title and the hint; "+
						"the title should have been shed\n%s", plain)
				}
				// View() must still produce a frame that fits.
				assertFrameFits(t, m.View().Content, w, h)
			})
		}
	}
}

// TestModalFit_ComposesExactly is the invariant issue #142 says
// frame_invariant_test.go could not assert: with a modal open the
// PRE-clip frame is exactly m.height rows.
//
// View composes a modal by centering its block over the terminal with
// lipgloss.Place(m.width, m.height, ...), which pads a short block up
// to m.height and leaves a tall one alone. So measuring the placed
// block is a direct test of "the modal fit without clipFrame's help":
// anything other than m.height means the block was taller than the
// terminal and the clamp was doing the work.
//
// The one exemption is the case documented on fitModalContent — a
// footer key hint that wraps to more rows than the terminal has. It is
// recognizable rather than hand-waved: the modal is down to the hint
// alone, with no title on it.
func TestModalFit_ComposesExactly(t *testing.T) {
	for _, w := range []int{40, 100} {
		for _, tc := range modalFitCases() {
			for _, h := range modalFitHeights {
				name := tc.name + "/" + strconv.Itoa(w) + "x" + strconv.Itoa(h)
				t.Run(name, func(t *testing.T) {
					m := tc.open(t, w, h)
					block := tc.render(&m)
					placed := lipgloss.Place(m.width, m.height,
						lipgloss.Center, lipgloss.Center, block)
					got := lipgloss.Height(placed)
					if got == h {
						return
					}
					if got > h && !strings.Contains(ansi.Strip(block), tc.title) {
						t.Logf("height %d: the footer hint alone wraps to %d rows here, "+
							"which is below the floor — clipFrame trims %d", h, got, got-h)
						return
					}
					t.Errorf("placed frame is %d rows in a %d-row terminal, want exactly %d "+
						"— clipFrame would have to trim %d", got, h, h, got-h)
				})
			}
		}
	}
}

// TestModalFit_NoRegressionAtNormalSizes is the other half of the
// trade: above modalFullscreenBelow the fix must be invisible. The
// margin is still reserved, the body allowance is still the old
// arithmetic to the row, and fitModalContent sheds nothing it did not
// have to — the two blank spacer rows and the footer rule are still
// on the block wherever the terminal has a row to spare.
//
// (TestGolden_ModalFrame pins the same claim at the byte level.)
func TestModalFit_NoRegressionAtNormalSizes(t *testing.T) {
	for _, h := range modalRoomyHeights() {
		// The pre-#142 arithmetic, restated rather than called, so
		// this fails if modalBodyHeight's normal branch is touched.
		for _, chrome := range []int{modalChromeRows, modalPickerChromeRows} {
			want := max(minModalBodyRows, h-chrome-modalMarginRows)
			if got := modalBodyHeight(h, chrome); got != want {
				t.Errorf("modalBodyHeight(%d, %d) = %d, want %d — the normal regime moved",
					h, chrome, got, want)
			}
		}
		if got := modalMargin(h); got != modalMarginRows {
			t.Errorf("height %d: margin = %d, want the full %d", h, got, modalMarginRows)
		}
	}
	for _, tc := range modalFitCases() {
		for _, h := range modalRoomyHeights() {
			t.Run(tc.name+"/"+strconv.Itoa(h), func(t *testing.T) {
				block := ansi.Strip(tc.render(ptr(tc.open(t, 100, h))))
				// The blank row under the title and the blank row
				// above the footer rule: two empty content rows,
				// present iff fitModalContent shed nothing.
				//
				// "Had room" is measured rather than assumed. A
				// dialog whose rows wrap past the logical allowance
				// modalBodyHeight lent it can fill the terminal
				// outright at a height the regime still calls
				// roomy: the theme picker does exactly that at 24
				// rows, where twelve themes whose descriptions wrap
				// in a 72-column modal want more terminal rows than
				// twelve. Once the block has grown to the full
				// height of the screen there is no room left to
				// keep the spacing in, and shedding it is
				// fitModalContent honouring its documented order
				// rather than the height regime being wrong. Short
				// of that, the spacing must survive.
				if got := blankContentRows(block); got < 2 && lipgloss.Height(block) < h {
					t.Errorf("height %d: modal has %d blank spacer rows, want 2 — spacing "+
						"was shed on a terminal with %d rows still to spare\n%s",
						h, got, h-lipgloss.Height(block), block)
				}
				if !strings.Contains(block, tc.title) {
					t.Errorf("height %d: the title line was shed on a roomy terminal\n%s", h, block)
				}
				if !strings.Contains(block, strings.Repeat(GlyphRule, 20)) {
					t.Errorf("height %d: the footer rule was shed on a roomy terminal\n%s", h, block)
				}
			})
		}
	}
}

// TestModalFit_OverflowingModalTakesItsWholeAllowance is the second
// half of issue #149: not "does it fit" but "does it fit for the
// right reason".
//
// fitModalContent guarantees the footer survives, and it does that by
// shedding rows — so a dialog that over-budgets its body still LOOKS
// correct on screen while its own scroll geometry (lastView, the
// "↑↓ scroll" hint, the maxScroll clamp) describes a modal nobody is
// looking at. A dialog that under-budgets looks correct too; it just
// leaves rows of the operator's terminal empty.
//
// So this asserts the allotment directly. A modal whose content
// overflows occupies exactly the rows the shared height regime gives
// it — terminal minus margin — with the arithmetic restated here
// rather than called, the way TestModalFit_NoRegressionAtNormalSizes
// restates modalBodyHeight. Heights are limited to the normal regime
// at and above the point where minModalBodyRows stops eating into the
// margin, which is the range where "exactly its allotment" is a
// statement about the budget rather than about the floor.
//
// The tool-call overlay's predecessor arithmetic fails this in both
// directions: 17 rows of a 24-row terminal (three wasted) and 13 rows
// of a 14-row terminal (three over, into the margin).
func TestModalFit_OverflowingModalTakesItsWholeAllowance(t *testing.T) {
	cases := []struct {
		name   string
		chrome int
		open   func(w, h int) Model
	}{
		{
			name:   "tool-call",
			chrome: toolCallDialogChromeRows,
			open: func(w, h int) Model {
				m := newFrameModel(StatusHeader, w, h)
				m.history.Append(Message{
					Role:            RoleTool,
					ToolName:        "read_file",
					ToolCallID:      "call-1",
					ToolArgsMap:     map[string]any{"path": "/etc/hosts"},
					ToolResponseMap: manyKeys(60),
				})
				m.overlayStack.Open(newToolCallDialog(1))
				return m
			},
		},
		{
			name:   "subagent",
			chrome: modalChromeRows,
			open: func(w, h int) Model {
				m := newFrameModel(StatusHeader, w, h)
				d := newSubagentDialog("auditor")
				events := make([]SubagentEvent, 0, 60)
				for i := 1; i <= 60; i++ {
					events = append(events, SubagentEvent{
						Seq: int64(i), Author: "model", Text: fmt.Sprintf("turn %d", i),
					})
				}
				d.apply(subagentEventsMsg{
					name:    "auditor",
					info:    SubagentInfo{Name: "auditor", Status: "running"},
					hasInfo: true,
					page:    SubagentEventPage{Events: events},
				})
				m.overlayStack.Open(d)
				return m
			},
		},
	}
	for _, tc := range cases {
		for _, h := range modalRoomyHeights() {
			// Below chrome+margin+floor the floor is what sets the
			// body height, not the budget, and the modal is entitled
			// to spill into the margin.
			if h < tc.chrome+modalMarginRows+minModalBodyRows {
				continue
			}
			t.Run(tc.name+"/"+strconv.Itoa(h), func(t *testing.T) {
				m := tc.open(100, h)
				block := m.overlayStack.Render(m.width, &m)
				want := h - modalMarginRows
				if got := modalRows(block); got != want {
					t.Errorf("overflowing modal is %d rows in a %d-row terminal, want exactly %d "+
						"(terminal minus the %d-row margin) — the body allowance disagrees with "+
						"the shared height regime\n%s", got, h, want, modalMarginRows, ansi.Strip(block))
				}
			})
		}
	}
}

// manyKeys builds a response map that renders as one JSON line per
// key, so the tool-call detail body overflows any terminal in the
// table above by logical rows rather than by wrapping.
func manyKeys(n int) map[string]any {
	out := make(map[string]any, n)
	for i := range n {
		out[fmt.Sprintf("k%02d", i)] = fmt.Sprintf("v%02d", i)
	}
	return out
}

// ptr is the addressable-copy helper the render funcs need — they
// take *Model because View's own modal renderers do.
func ptr(m Model) *Model { return &m }

// modalRoomyHeights is the set of terminal heights at which every
// modal composes with its margin intact and sheds nothing: the first
// such height, the one above it, and two comfortable ones. Derived
// from modalFullscreenBelow rather than written out, because the
// threshold moves whenever the chrome does — it went from 13 to 15
// when the modal grew a box edge (issue #199), and a literal 14 in
// this list silently became a fullscreen height asserting the
// normal-regime arithmetic against itself.
func modalRoomyHeights() []int {
	return []int{modalFullscreenBelow, modalFullscreenBelow + 1, 24, 50}
}

// modalContentLines strips the box edge off a rendered modal and
// returns what is inside it: ANSI removed, right-trimmed, one string
// per row, without the edge's own top and bottom rows.
//
// Issue #199 put a glyph at both ends of every row, which is the whole
// point on screen and a nuisance to a test reading the body. A
// paragraph that wraps now has "│ … │" between its halves, so a
// substring check for the unwrapped text fails on the edge rather than
// on the content; a row that renders nothing is no longer the empty
// string. Tests that assert about CONTENT come through here. Tests
// that assert about the edge itself (assertModalEdge) must not — they
// would be checking their own helper.
//
// The single column of padding is left in place, so a caller sees the
// same leading space the modal frame has always produced.
func modalContentLines(block string) []string {
	lines := strings.Split(ansi.Strip(block), "\n")
	b := lipgloss.RoundedBorder()
	if len(lines) > 0 && strings.HasPrefix(lines[0], b.TopLeft) {
		lines = lines[1 : len(lines)-1]
	}
	out := make([]string, len(lines))
	for i, ln := range lines {
		ln = strings.TrimPrefix(ln, b.Left)
		ln = strings.TrimSuffix(ln, b.Right)
		out[i] = strings.TrimRight(ln, " ")
	}
	return out
}

// blankContentRows counts the modal rows that carry no printable
// content inside the box edge.
func blankContentRows(block string) int {
	n := 0
	for _, line := range modalContentLines(block) {
		if strings.TrimSpace(line) == "" {
			n++
		}
	}
	return n
}

// TestModalFit_ScrollStillWorksWhenDegraded checks the fullscreen
// mode shrinks the scroll window rather than removing it. A modal
// that fits by giving up scrolling would fit by hiding its content,
// which is the defect wearing a different hat.
func TestModalFit_ScrollStillWorksWhenDegraded(t *testing.T) {
	m := newFrameModel(StatusHeader, 100, 8)
	if !modalFullscreen(m.height) {
		t.Fatalf("height %d should be in the fullscreen regime", m.height)
	}
	m.sideAnswer = &SideAnswer{
		Question: "explain the eviction path",
		Answer:   strings.Repeat("a distinct line of the answer\n\n", 40),
	}
	first := ansi.Strip(m.renderSideAnswer())

	sc := m.scroll()
	if !sc.overflows() {
		t.Fatalf("a 40-paragraph answer in an 8-row terminal must overflow; "+
			"total=%d view=%d", sc.total, sc.view)
	}
	if sc.view <= 0 {
		t.Fatalf("degraded mode left no scroll window at all (view=%d)", sc.view)
	}
	sc.to(sc.maxOffset())
	last := ansi.Strip(m.renderSideAnswer())
	if first == last {
		t.Errorf("scrolling to the bottom changed nothing in the degraded body:\n%s", last)
	}
	if !strings.Contains(last, "dismiss") {
		t.Errorf("the footer hint went missing once scrolled:\n%s", last)
	}
}
