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

// Frame-invariant grid (issue #101). `package tui` had 202
// strings.Contains assertions and five tests that called View() at
// all — a combination that proves a token is present somewhere in
// the output and says nothing about where it landed or how wide it
// is. This file is the machine-checked definition of a correct
// frame:
//
//	1. every rendered line's ansi.StringWidth is <= m.width
//	2. the frame's total line count is <= m.height
//
// Both hold across a width x height matrix and across every UI
// state that composes chrome differently — base chat, permission
// overlay, elicit modal, help panel, and both status layouts.
//
// The invariants are enforced by the clipping post-pass at the end
// of View() (issue #102). Without that post-pass 83 of this grid's
// 200 cells fail: 82 columns of output in a 40-column terminal, 62
// rows in a 24-row one, and 10 rows at height 4 — and Bubble Tea
// drops the TOP lines when a frame is too tall, so the failure mode
// is the status header silently vanishing.

package tui

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// frameWidths / frameHeights are the matrix axes. The realistic
// sizes (80x24, 100x50, 120x50) are what operators actually run;
// the hostile ones (40 cols, 4 rows) are where the composition
// arithmetic in resize() runs out of budget and every join starts
// overflowing.
var (
	frameWidths  = []int{40, 80, 100, 120, 200}
	frameHeights = []int{4, 10, 24, 50}
)

// frameState is one named UI configuration to drive the grid over.
// setup receives a model already sized to (w, h) and returns the
// model whose View() gets measured.
type frameState struct {
	name  string
	setup func(t *testing.T, m Model, w, h int) Model
}

// frameStates enumerates the UI states the invariants must hold in.
// Each one composes chrome through a different path in View():
// the plain JoinVertical stack, the JoinHorizontal sidebar split,
// and the three lipgloss.Place overlay arms.
func frameStates() []frameState {
	return []frameState{
		{
			name: "base-chat-empty",
			setup: func(_ *testing.T, m Model, _, _ int) Model {
				return m
			},
		},
		{
			name: "base-chat-transcript",
			setup: func(_ *testing.T, m Model, _, _ int) Model {
				return withHostileTranscript(m)
			},
		},
		{
			name: "permission-modal",
			setup: func(_ *testing.T, m Model, _, _ int) Model {
				m = withHostileTranscript(m)
				out, _ := m.Update(permissionRequestMsg{req: PermissionRequest{
					Kind:     PermissionKindBash,
					ToolName: "bash",
					Verb:     "rm",
					Detail:   "rm -rf /tmp/a-really-quite-long-path/that/keeps/going/well/past/any/sensible/terminal/width",
				}})
				return out.(Model)
			},
		},
		{
			name: "elicit-modal",
			setup: func(_ *testing.T, m Model, _, _ int) Model {
				m = withHostileTranscript(m)
				out, _ := m.Update(elicitRequestMsg{
					serverName: "an-mcp-server-with-a-long-name",
					req: ElicitRequest{
						Mode:        ElicitFormMode,
						Title:       "Confirm the deployment target for this rollout",
						Description: "The server needs a project and a region before it can continue the rollout.",
						Fields: []ElicitField{
							{Name: "project", Description: "GCP project id", Type: ElicitFieldString, Required: true},
							{Name: "region", Description: "Compute region", Type: ElicitFieldEnum, EnumChoices: []string{"us-central1", "europe-west4"}},
						},
					},
				})
				return out.(Model)
			},
		},
		{
			name: "help-panel",
			setup: func(_ *testing.T, m Model, _, _ int) Model {
				m = withHostileTranscript(m)
				m.helpOpen = true
				m.resize()
				m.refreshViewport()
				return m
			},
		},
		{
			// Issue #119: the help panel paginates to the row cap the
			// chrome budget gives it, and a page past the first is a
			// different set of rows at a different height. The
			// invariants have to hold on those too.
			name: "help-panel-paged",
			setup: func(_ *testing.T, m Model, _, _ int) Model {
				m = withHostileTranscript(m)
				m.advanceHelp()
				m.resize()
				m.advanceHelp()
				m.resize()
				m.refreshViewport()
				return m
			},
		},
		{
			// Issue #135: the LiveAgent path composed no frame in this
			// grid at all — the in-progress block was gated shut on it,
			// so every cell measured a frame that live mode never
			// actually produces. That absence is why the streamed prose
			// and the spinner line could both go missing without a test
			// noticing. Now that the gate opens, the block's own
			// geometry — wrapped Glamour prose plus the spinner verb
			// with its widest elapsed suffix — is inside the invariants.
			name: "live-stretch",
			setup: func(_ *testing.T, m Model, _, _ int) Model {
				return withLiveStretch(withHostileTranscript(m))
			},
		},
		{
			// Issue #117: the pickers grew a filter row. It costs a
			// body row (paid for with modalChromeRows+1) and puts a
			// hardware caret on a modal that used to have none, so
			// both the geometry grid and TestCursor_InsideClippedFrame
			// — which types into whatever this returns — need a
			// picker in the matrix. The theme picker is the one that
			// needs no host capability to populate itself.
			//
			// Issue #142: heights 4 and 10 used to pass this grid
			// only because clipFrame rescued them — the modal
			// composed 11 rows regardless and the clamp trimmed the
			// footer hint off the bottom. They pass on their own
			// merit now. This grid still cannot tell the difference,
			// because it measures View()'s output and View()'s output
			// is post-clamp; dialog_fit_test.go is where the modal is
			// measured BEFORE the clamp, which is the assertion that
			// can.
			name: "theme-picker",
			setup: func(_ *testing.T, m Model, _, _ int) Model {
				m = withHostileTranscript(m)
				m.overlayStack.Open(newThemePickerDialog(m.themeName))
				return m
			},
		},
		{
			// Issue #121: the auto-grown input box. resize() used to
			// reset the textarea to textareaMinHeight on every call,
			// so no state in this grid could ever compose a frame
			// with a box taller than three rows and the clamp on it
			// was untested by construction.
			name: "tall-textarea",
			setup: func(_ *testing.T, m Model, _, _ int) Model {
				return withTallTextarea(withHostileTranscript(m))
			},
		},
	}
}

// withLiveStretch puts the model mid-LiveAgent-stretch: liveMode on,
// a partial chunk accumulated, spinner running.
//
// The elapsed readout is pinned through the injected clock (Model.now,
// issue #111) rather than left on the wall clock — both because a
// time-dependent frame is a flaky frame, and because the value chosen
// is the widest the suffix ever gets ("2h00m"), which is the case the
// width invariant should be measuring.
func withLiveStretch(m Model) Model {
	m.liveMode = true
	long := strings.Repeat("live-streamed-unbreakable-token-", 8)
	out, _ := m.Update(streamChunkMsg{gen: m.sessionGen, text: long, partial: true})
	m = out.(Model)
	start := time.Date(2026, 8, 15, 9, 0, 0, 0, time.UTC)
	m.turnStarted = start
	m.now = func() time.Time { return start.Add(2 * time.Hour) }
	m.refreshViewport()
	return m
}

// withHostileTranscript seeds a transcript whose content is chosen
// to stress the width budget: an unbreakable token far longer than
// any terminal, a wide fenced code block, and enough rows to fill
// the tallest viewport in the matrix.
func withHostileTranscript(m Model) Model {
	m.history.Append(Message{
		Role:     RoleUser,
		Text:     "please read the file",
		Rendered: "please read the file",
	})
	long := strings.Repeat("wide-unbreakable-token-", 12)
	m.history.Append(Message{
		Role:     RoleAssistant,
		Text:     long,
		Rendered: long,
	})
	code := "func main() { fmt.Println(\"" + strings.Repeat("x", 180) + "\") }"
	m.history.Append(Message{
		Role:     RoleAssistant,
		Text:     code,
		Rendered: code,
	})
	for i := 0; i < 60; i++ {
		line := "transcript row " + strconv.Itoa(i) + " " + strings.Repeat("filler ", 20)
		m.history.Append(Message{Role: RoleAssistant, Text: line, Rendered: line})
	}
	m.refreshViewport()
	return m
}

// newFrameModel builds a model sized to (w, h) in the requested
// status layout. The theme is pinned so a palette change can't turn
// this into a flaky test through some width-carrying token.
func newFrameModel(layout StatusLayout, w, h int) Model {
	m := NewModel(Options{
		Agent:            &bareAgent{id: "frame"},
		StatusLayout:     layout,
		PermissionLayout: PermissionOverlay,
	})
	m.styles = NewStylesWithTheme(true, goldenTheme())
	out, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return out.(Model)
}

// assertFrameFits is the invariant itself. Kept deliberately small
// and free of Contains-style assertions: it measures geometry only.
func assertFrameFits(t *testing.T, frame string, w, h int) {
	t.Helper()
	lines := strings.Split(frame, "\n")
	// A frame ending in a newline yields a trailing empty element
	// that costs no terminal row.
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	if len(lines) > h {
		t.Errorf("frame is %d lines at height %d — Bubble Tea drops the TOP %d, so the header vanishes silently",
			len(lines), h, len(lines)-h)
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > w {
			t.Errorf("line %d is %d cols at width %d (overflow %d): %q",
				i, got, w, got-w, ansi.Truncate(line, w+20, "…"))
		}
	}
}

// --- Panel survival (issue #158) -----------------------------------
//
// assertFrameFits above measures two numbers, and a frame that has
// silently lost a whole panel scores perfectly on both of them: the
// survivors reflow into the freed rows and the dimensions still check
// out. That is not hypothetical — #147 and #149 were both exactly this
// shape, and the spike hit a third instance (a sidebar loop that
// capped at *n sessions* rather than *n lines*, which pushed the input
// row and the footer off the bottom of every frame while this grid
// stayed green). clipFrame is what makes an oversized frame legal, so
// a clip pass without a survival assertion beside it is a safety net
// that also hides the fall.
//
// # Why this is not strings.Contains
//
// The obvious assertion — "the footer text appears somewhere in the
// frame" — is the same mistake this file was written to undo (see the
// header comment: 202 Contains assertions that proved nothing). A
// marker present *somewhere* says nothing about where it landed. A
// sidebar collapsed to one column contains its markers. A footer
// composed into the middle of the chat contains its markers. Both are
// broken frames and both pass Contains.
//
// So the assertion here is positional: every panel is located to a
// rectangle — a row range and a column band — and checked to occupy
// the rectangle the layout contract gives it.
//
// # How each panel is identified
//
// By re-rendering it and finding its own text in the frame's cell
// grid, NOT by looking for a marker and NOT by re-deriving row numbers
// from the chrome budget.
//
// The distinction matters. View composes the left column by stacking
// the blocks its renderers return, in a fixed order, with
// JoinVertical; the row each panel starts at is therefore the sum of
// the heights of the blocks above it, and nothing else. This test
// re-does exactly that sum — from lipgloss.Height of the very blocks
// View joins — and asserts the frame is the top min(stack, h) rows of
// it. It never reads m.chrome. That is deliberate: allocateChrome
// (budget.go) is one of the things that can break, and a test whose
// expected geometry comes out of allocateChrome would happily assert
// the bug. Here, if the budget hands the help panel rows the input box
// needed, the blocks still stack the same way, the stack still
// overflows, and irreducibleRows below says the terminal had room —
// which is the failure.
//
// Columns are the one place a constant is consulted: chromeWidth() is
// a pure function of m.width and the sidebarWidth constant, has no row
// arithmetic in it, and its own doc comment nominates it as the thing
// tests should ask rather than re-deriving. The divider column is also
// cross-checked against the frame itself.
//
// # What "survival" means when the terminal is genuinely too short
//
// At 40x4 the header, the footer and the input box are together taller
// than the screen; no allocation can win and clipFrame trims the
// bottom. That is documented behaviour, not panel loss, so the
// assertion is conditional — on irreducibleRows (resize_budget_test.go),
// which measures the panel RENDERERS and adds the two published floors
// (textareaMinHeight, chatMinHeight) and never consults the allocator.
// A stack that overflows a terminal with room for that floor is a bug
// even though the frame still fits.
//
// Degenerate geometry is checked separately, because a renderer that
// collapses is invisible to a match against its own output: the
// sidebar has to be at least 3 rows and 16 columns, the input box at
// least its border plus textareaMinHeight, the header and the footer
// at least one non-blank row. For the same reason the sidebar band is
// checked to be BLANK below the sidebar's own last row — a panel that
// grows past its rectangle somewhere between its renderer and the join
// does not change the size of what the renderer returns, so matching
// the renderer's output cannot see it. That is the spike's F2 exactly.
//
// # What it still cannot see
//
// On a terminal too short for the irreducible chrome the bottom of the
// stack is already off-screen, so a panel dropped there is invisible to
// any frame-level assertion — the frame is identical either way. The
// grid's 40x4 and 40x10 cells are in that regime for the base layout.
// This is stated rather than worked around: the alternative is to
// assert something about a frame that does not contain the evidence.
// The modal arm has no such hole, because #147 gave modals a height
// regime that fits every terminal in the grid.

// frameCellRows splits a composed frame into one ANSI-stripped string
// per terminal row. Styling is dropped because position, not colour,
// is what is being asserted; a trailing newline costs no row.
func frameCellRows(frame string) []string {
	lines := strings.Split(frame, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	rows := make([]string, 0, len(lines))
	for _, l := range lines {
		rows = append(rows, ansi.Strip(l))
	}
	return rows
}

// cellWindow returns the display cells of an ANSI-stripped row in the
// half-open column band [c0, c1). Cells, not bytes and not runes: a
// frame full of box-drawing glyphs and CJK is neither one byte nor one
// rune per column, and a column band that splits a wide glyph would
// otherwise slide every comparison after it.
func cellWindow(row string, c0, c1 int) string {
	start := len(row)
	w := 0
	for i, r := range row {
		if w >= c0 {
			start = i
			break
		}
		w += ansi.StringWidth(string(r))
	}
	s := row[start:]
	w = 0
	for i, r := range s {
		if w >= c1-c0 {
			return s[:i]
		}
		w += ansi.StringWidth(string(r))
	}
	return s
}

// framePanel is one region of the layout: the block View renders for
// it, and the column band the layout contract confines it to.
type framePanel struct {
	name string
	// block is the panel's own render — the exact string View joins
	// into the body.
	block    string
	col, end int
}

// rows is the panel's own rendered height, which is the number of
// terminal rows it is entitled to in the composed frame.
func (p framePanel) rows() int { return lipgloss.Height(p.block) }

// matchAt reports how many of the panel's leading rows are present in
// the frame starting at (row, p.col), plus a description of the first
// row that is not. Trailing blanks are ignored on both sides:
// JoinVertical pads every part out to the width of the widest one, and
// that padding is not the panel's content.
func (p framePanel) matchAt(frame []string, row int) (int, string) {
	lines := strings.Split(p.block, "\n")
	for i, pl := range lines {
		fr := row + i
		if fr >= len(frame) {
			return i, ""
		}
		got := strings.TrimRight(cellWindow(frame[fr], p.col, p.end), " ")
		want := strings.TrimRight(ansi.Strip(pl), " ")
		if got != want {
			return i, fmt.Sprintf("frame row %d, cols %d..%d:\n     want %q\n     got  %q",
				fr, p.col, p.end, want, got)
		}
	}
	return len(lines), ""
}

// composedStack re-derives the vertical stack View builds, in View's
// order, from the same renderers View calls. The conditional members
// (help panel, palette, toast) are included on the same conditions
// View includes them on — a non-empty render.
//
// It returns the left column's stack, the sidebar panel, and whether
// the sidebar is part of this layout at all.
func composedStack(m Model) (stack []framePanel, sidebar framePanel, hasSidebar bool) {
	cw := m.chromeWidth()
	add := func(name, block string) {
		stack = append(stack, framePanel{name: name, block: block, col: 0, end: cw})
	}
	if m.effectiveLayout() == StatusHeader {
		add("status header", m.renderHeader())
	}
	add("chat", m.chatView())
	if help := m.renderHelpPanel(cw); help != "" {
		add("help panel", help)
	}
	if pal := m.renderPalette(cw); pal != "" {
		add("palette", pal)
	}
	add("input box", m.renderInputBox())
	if toast := m.renderToast(cw); toast != "" {
		add("toast", toast)
	}
	add("footer legend", m.renderFooter(cw))

	if m.effectiveLayout() == StatusSidebar {
		// The sidebar is a horizontal sibling of the whole left column,
		// so it starts at frame row 0 and at the column just right of
		// the one-cell divider.
		sidebar = framePanel{
			name:  "sidebar",
			block: m.renderSidebar(),
			col:   cw + 1,
			end:   m.width,
		}
		hasSidebar = true
	}
	return stack, sidebar, hasSidebar
}

// modalBlock returns the block View would centre over the body, and
// whether there is one. The switch is View's switch: an active modal
// REPLACES the body rather than overlaying it, so when one is up the
// panel stack is not what the frame contains and the survival question
// becomes "did the modal keep its own footer" — which is #147.
func modalBlock(m *Model) (string, bool) {
	switch {
	case m.pendingPermission != nil && m.opts.PermissionLayout == PermissionOverlay:
		return m.renderPermissionModal(), true
	case m.pendingElicit != nil:
		return m.renderElicitModal(), true
	case m.pendingForm != nil:
		return m.styles.ModalBorder.Padding(1, 2).Render(m.pendingForm.View()), true
	case m.sideAnswer != nil:
		return m.renderSideAnswer(), true
	case m.overlayStack.HasDialogs():
		return m.overlayStack.Render(m.width, m), true
	}
	return "", false
}

// assertPanelsSurvive is the panel-survival invariant: every panel the
// layout contract puts on screen is on screen, in its own rectangle,
// whole.
func assertPanelsSurvive(t *testing.T, m Model, w, h int) {
	t.Helper()
	frame := frameCellRows(m.View().Content)

	if block, ok := modalBlock(&m); ok {
		assertModalSurvives(t, m, frame, block, w, h)
		return
	}

	stack, sidebar, hasSidebar := composedStack(m)

	// The stack may only be taller than the terminal when the terminal
	// cannot hold the irreducible chrome. Anywhere else, an overflow
	// means some panel took rows another panel needed and clipFrame
	// quietly ate the difference.
	stackH := 0
	for _, p := range stack {
		stackH += p.rows()
	}
	need := irreducibleRows(m)
	switch {
	case stackH > h && need <= h:
		t.Errorf("composed stack is %d rows at height %d, but the irreducible chrome needs only %d — "+
			"clipFrame will trim %d rows off the bottom and whatever is there vanishes silently",
			stackH, h, need, stackH-h)
	case stackH > h && len(frame) != h:
		// A terminal too short for the irreducible chrome is the one
		// case clipFrame is contracted to fire in, and it keeps the
		// first h rows. Fewer than h means rows went missing some other
		// way.
		t.Errorf("stack of %d rows overflows a %d-row terminal, but the frame is %d rows — "+
			"clipFrame keeps the first %d, so %d rows were lost before the clamp",
			stackH, h, len(frame), h, h-len(frame))
	case stackH <= h && len(frame) != stackH:
		// The footer is the bottom of the stack, so a frame that fits
		// ends on the footer's last row and has nothing below it.
		t.Errorf("frame is %d rows in a %d-row terminal but the panels stack to %d — "+
			"the frame is not the panels",
			len(frame), h, stackH)
	}

	// Every panel sits at the row the blocks above it end on, in its
	// own column band, whole.
	row := 0
	for _, p := range stack {
		matched, diff := p.matchAt(frame, row)
		switch {
		case matched == p.rows():
			// Whole panel present where the stack puts it.
		case diff != "":
			t.Errorf("%s is not in its rectangle: %d of %d rows match from frame row %d\n     %s",
				p.name, matched, p.rows(), row, diff)
		case len(frame) < h:
			// The frame did not fill the terminal, so the clip pass
			// cannot be what removed these rows: the panel was dropped
			// from the composition.
			t.Errorf("%s is missing: it needs frame rows %d..%d but the frame is only %d rows "+
				"in a %d-row terminal — the panel was dropped from the composition, not clipped",
				p.name, row, row+p.rows()-1, len(frame), h)
		case need <= h:
			t.Errorf("%s is clipped: %d of its %d rows survive from frame row %d in a %d-row terminal "+
				"that had room for the %d rows the layout cannot give up",
				p.name, matched, p.rows(), row, h, need)
		default:
			// The terminal is genuinely shorter than the irreducible
			// chrome. clipFrame trims from the bottom and the loss is
			// reported once, above, rather than once per panel.
		}
		row += p.rows()
	}

	assertPanelsNotDegenerate(t, stack)

	if !hasSidebar {
		return
	}

	// The divider is a full-height column of GlyphColumn, and it is
	// what separates the two bands. Locating it in the frame is the
	// independent check that the sidebar band is where chromeWidth says
	// it is.
	divider := 0
	for _, r := range frame {
		if strings.HasPrefix(cellWindow(r, sidebar.col-1, sidebar.col), GlyphColumn) {
			divider++
		}
	}
	if divider != len(frame) {
		t.Errorf("sidebar divider is at column %d on %d of %d frame rows — the sidebar band has no left edge",
			sidebar.col-1, divider, len(frame))
	}

	// The spike's F2 shape: JoinHorizontal makes the body as tall as
	// its tallest column, so a sidebar that renders past its own
	// rectangle overflows a frame the left column fits in perfectly
	// well — and the clip pass makes that legal.
	if sb := sidebar.rows(); sb > h && stackH <= h {
		t.Errorf("sidebar renders %d rows in a %d-row terminal the %d-row left column fits — "+
			"the sidebar alone is what pushes the joined body past the clamp",
			sb, h, stackH)
	}
	// A sidebar clipped by a terminal shorter than the sidebar is not
	// panel loss; a sidebar that does not match where it does fit is.
	wantSB := sidebar.rows()
	if wantSB > len(frame) {
		wantSB = len(frame)
	}
	if matched, diff := sidebar.matchAt(frame, 0); matched != wantSB {
		if diff == "" {
			diff = fmt.Sprintf("frame is %d rows", len(frame))
		}
		t.Errorf("sidebar is not in its rectangle: %d of %d rows match from frame row 0, cols %d..%d\n     %s",
			matched, wantSB, sidebar.col, sidebar.end, diff)
	}
	// Below its own last row the band belongs to nobody: JoinHorizontal
	// pads the shorter column with blanks. Anything drawn there is
	// content the sidebar renderer did not account for, which is how a
	// panel grows past its rectangle without its own render changing
	// size.
	for r := sidebar.rows(); r < len(frame); r++ {
		if got := strings.TrimRight(cellWindow(frame[r], sidebar.col, sidebar.end), " "); got != "" {
			t.Errorf("frame row %d has %q in the sidebar band (cols %d..%d) below the sidebar's own %d rows — "+
				"the panel is taller than the rectangle it reports",
				r, got, sidebar.col, sidebar.end, sidebar.rows())
			break
		}
	}
	if sbw, sbh := lipgloss.Width(sidebar.block), sidebar.rows(); sbw < 16 || sbh < 3 {
		t.Errorf("sidebar collapsed to %dx%d cells — it is contracted to a %d-column panel, "+
			"and a marker-presence check would not notice",
			sbw, sbh, sidebarWidth)
	}
}

// assertPanelsNotDegenerate checks the panels are not merely present
// but usable. A panel that has collapsed still matches its own render,
// so these are floors stated here rather than measured off the model.
func assertPanelsNotDegenerate(t *testing.T, stack []framePanel) {
	t.Helper()
	for _, p := range stack {
		switch p.name {
		case "input box":
			// The border row plus a textarea the operator can type in.
			if floor := 1 + textareaMinHeight; p.rows() < floor {
				t.Errorf("input box is %d rows, below its floor of %d — "+
					"an operator who cannot see the line they are typing cannot drive the TUI",
					p.rows(), floor)
			}
		case "status header", "footer legend", "help panel", "palette":
			if p.rows() < 1 || strings.TrimSpace(ansi.Strip(p.block)) == "" {
				t.Errorf("%s rendered %d blank rows — it is on screen and says nothing", p.name, p.rows())
			}
		case "chat":
			if p.rows() < 1 {
				t.Errorf("chat region is %d rows", p.rows())
			}
		}
	}
}

// assertModalSurvives is the modal arm of the invariant, and the one
// #147 and #149 needed: a modal overflowing a short terminal kept its
// title and lost its bottom, which is where the footer rule and the
// key hint that closes the thing live. Asserting the whole block lands
// inside the frame at its centred origin says the footer survived
// without naming the footer's text.
func assertModalSurvives(t *testing.T, m Model, frame []string, block string, w, h int) {
	t.Helper()
	bw, bh := lipgloss.Width(block), lipgloss.Height(block)
	if bh > h || bw > w {
		t.Errorf("modal is %dx%d in a %dx%d terminal — clipFrame will take the overflow off the bottom, "+
			"which is where the footer hint is",
			bw, bh, w, h)
		return
	}
	// Centring is integer division, so the block's top-left cell is a
	// function of its own size. The rectangle ends where the block
	// ends, not at the frame's right edge: since #156 the modal is
	// composited over the body rather than placed instead of it, so the
	// cells beside it hold the transcript rather than blanks.
	col := atLeast((w-bw)/2, 0)
	top := atLeast((h-bh)/2, 0)
	p := framePanel{name: "modal", block: block, col: col, end: col + bw}
	if matched, diff := p.matchAt(frame, top); matched != bh {
		if diff == "" {
			diff = fmt.Sprintf("frame is %d rows", len(frame))
		}
		t.Errorf("modal is not in its rectangle: %d of %d rows match from frame row %d, col %d\n     %s",
			matched, bh, top, p.col, diff)
	}

	stack, _, _ := composedStack(m)
	assertBackgroundSurvives(t, frame, stack, top, bh)
}

// assertBackgroundSurvives is #156's own invariant, and the reason the
// modal's rectangle above had to stop at the block's own right edge: a
// modal is a sheet over the frame, not a mode switch. Every row the
// modal does not cover has to be exactly what the panel rendered, so
// the transcript stays readable behind a permission prompt and Esc
// reveals the screen rather than restoring it.
//
// Rows the modal does cover are skipped rather than inverted. The
// block's own cells are asserted above; the background cells beside it
// are the splice's business, and composite_test.go owns those column by
// column with styled and double-width input the frame corpus cannot
// reach.
func assertBackgroundSurvives(t *testing.T, frame []string, stack []framePanel, modalTop, modalRows int) {
	t.Helper()
	row := 0
	for _, p := range stack {
		lines := strings.Split(p.block, "\n")
		for i, pl := range lines {
			fr := row + i
			if fr >= len(frame) {
				break
			}
			if fr >= modalTop && fr < modalTop+modalRows {
				continue
			}
			got := strings.TrimRight(cellWindow(frame[fr], p.col, p.end), " ")
			want := strings.TrimRight(ansi.Strip(pl), " ")
			if got != want {
				t.Errorf("%s row %d was blanked by the modal even though the modal does not cover it "+
					"(modal holds frame rows %d..%d)\n     frame row %d, cols %d..%d:\n     want %q\n     got  %q",
					p.name, i, modalTop, modalTop+modalRows-1, fr, p.col, p.end, want, got)
				break
			}
		}
		row += len(lines)
	}
}

// --- The width contract at the render site (issue #159) ------------
//
// assertFrameFits measures View()'s output, and View()'s output is
// post-clipFrame. That makes every renderer in the package look
// well-behaved by construction: a panel that hands the layout a line
// twenty columns too wide scores exactly the same as one that does
// not, because the clamp trimmed the difference before the assertion
// ran. The clamp is therefore both the only enforcement of the width
// contract AND the reason no test can see the contract being broken.
//
// This is the assertion that can see it. It renders each panel
// directly — the same call View makes, at the same width — and
// measures the block BEFORE it enters a join. A failure names the
// renderer, which is the piece of information clipping destroys: by
// the time an oversized line has been composed into a frame, "who
// produced it" is not recoverable from the frame.
//
// # Why this is not the same list as composedStack
//
// composedStack (above) locates panels in the composed frame, so its
// column bands are the bands the LAYOUT gives a panel. The width a
// renderer was ASKED for is a different number in one case: the
// sidebar band is chromeWidth's leftover, which is sidebarWidth plus
// the two columns of slack chromeWidth() reserves, while
// renderSidebar composes against the sidebarWidth constant itself.
// Asserting against the band would let the sidebar overrun its own
// fixed column by up to two cells and call it legal — which is
// exactly how the section rule came to be one cell too long without
// any frame-level test noticing.
//
// # Why transcript rows are not in the list
//
// They are contracted to overrun. Since #154 the render cache holds
// rows at their NATURAL width so that panning has columns left to
// pan to, and the bound is applied per drawn line in chatView
// (chatCutLine). chatView's output is in the list; the rows behind it
// deliberately are not.
//
// # The tab clause
//
// Every width measurement in this package — ansi.StringWidth,
// lipgloss.Width, and therefore chatCutLine and clipFrame — prices a
// TAB at zero cells. lipgloss's Render expands it to contentTabWidth
// spaces on its way to the terminal. A raw tab that survives into a
// rendered block is therefore four cells the frame's own arithmetic
// cannot see, and neither the render-time cut nor the compose-time
// clamp can bound the line it sits on. Today no renderer emits one —
// every styled path goes through lipgloss and every untrusted path
// goes through sanitizeLine, whose truncateCells prices the tab at
// contentTabWidth precisely so the two agree. The clause is here so
// that stays true: `withPreview` splices Message.ToolPreview into a
// tool row verbatim, so a host that pre-renders its own preview is
// one raw tab away from a frame whose width nothing in the package
// can measure.

// widthContract is one renderer's output paired with the width it was
// handed. name is the function that produced it, so a failure reads
// as an accusation rather than as a coordinate.
type widthContract struct {
	name  string
	block string
	width int
}

// renderedBlocks calls every renderer whose output View joins into
// the frame, at the width View calls it with.
//
// The conditional members are included on the same conditions View
// includes them on — a non-empty render — because a renderer that
// returns "" has composed nothing and has no contract to break.
func renderedBlocks(m *Model) []widthContract {
	cw := m.chromeWidth()
	out := []widthContract{
		// chatView pads to the viewport plus the selection gutter,
		// which together are the column the frame gave the transcript.
		{"chatView", m.chatView(), m.viewport.Width() + chatGutterWidth},
		{"renderInputBox", m.renderInputBox(), cw},
		{"renderFooter", m.renderFooter(cw), cw},
	}
	if m.effectiveLayout() == StatusSidebar {
		out = append(out, widthContract{"renderSidebar", m.renderSidebar(), sidebarWidth})
	} else {
		out = append(out, widthContract{"renderHeader", m.renderHeader(), m.width})
	}
	if help := m.renderHelpPanel(cw); help != "" {
		out = append(out, widthContract{"renderHelpPanel", help, cw})
	}
	if pal := m.renderPalette(cw); pal != "" {
		out = append(out, widthContract{"renderPalette", pal, cw})
	}
	if toast := m.renderToast(cw); toast != "" {
		out = append(out, widthContract{"renderToast", toast, cw})
	}
	if block, ok := modalBlock(m); ok {
		// A modal is centred over the whole terminal rather than
		// stacked in the chrome column, so its width is m.width.
		out = append(out, widthContract{"modal", block, m.width})
	}
	return out
}

// assertRenderersHonorWidth is the invariant: no renderer hands the
// layout a line wider than the width it was asked for, and no
// renderer hands it a line whose width the layout cannot measure.
func assertRenderersHonorWidth(t *testing.T, m Model) {
	t.Helper()
	for _, c := range renderedBlocks(&m) {
		for i, line := range strings.Split(c.block, "\n") {
			if got := ansi.StringWidth(line); got > c.width {
				t.Errorf("%s produced line %d at %d cols after being asked for %d (overflow %d) — "+
					"it reaches the layout that wide and clipFrame trims it at the frame edge, "+
					"which is a silent loss of whatever was in those %d cells and, for a panel "+
					"joined horizontally, a shift of everything to its right:\n     %q",
					c.name, i, got, c.width, got-c.width, got-c.width,
					ansi.Truncate(ansi.Strip(line), c.width+20, "…"))
			}
			if strings.Contains(line, "\t") {
				t.Errorf("%s produced line %d containing a raw TAB — ansi.StringWidth prices it at 0 "+
					"and lipgloss expands it to %d spaces at draw time, so neither chatCutLine nor "+
					"clipFrame can bound this line:\n     %q",
					c.name, i, contentTabWidth, ansi.Strip(line))
			}
		}
	}
}

// TestFrameInvariants_Grid is the core of issue #101: every cell of
// the width x height x state x layout matrix must produce a frame
// that fits the terminal in both dimensions.
func TestFrameInvariants_Grid(t *testing.T) {
	layouts := []struct {
		name   string
		layout StatusLayout
	}{
		{"header", StatusHeader},
		{"sidebar", StatusSidebar},
	}
	for _, lay := range layouts {
		for _, st := range frameStates() {
			for _, w := range frameWidths {
				for _, h := range frameHeights {
					name := lay.name + "/" + st.name + "/" +
						strconv.Itoa(w) + "x" + strconv.Itoa(h)
					t.Run(name, func(t *testing.T) {
						m := newFrameModel(lay.layout, w, h)
						m = st.setup(t, m, w, h)
						assertFrameFits(t, m.View().Content, w, h)
					})
				}
			}
		}
	}
}

// TestFrameInvariants_ResizeSequence walks a single model through a
// sequence of resizes rather than building a fresh one per size.
// State that survives a resize (viewport offset, cached renders,
// the textarea's own width) is exactly where a clamp regression
// would hide from the fresh-model grid above.
func TestFrameInvariants_ResizeSequence(t *testing.T) {
	m := newFrameModel(StatusSidebar, 120, 40)
	m = withHostileTranscript(m)

	seq := []struct{ w, h int }{
		{120, 40}, {80, 24}, {40, 4}, {200, 50}, {100, 10}, {41, 5}, {120, 40},
	}
	for _, s := range seq {
		out, _ := m.Update(tea.WindowSizeMsg{Width: s.w, Height: s.h})
		m = out.(Model)
		t.Run(strconv.Itoa(s.w)+"x"+strconv.Itoa(s.h), func(t *testing.T) {
			assertFrameFits(t, m.View().Content, s.w, s.h)
			assertPanelsSurvive(t, m, s.w, s.h)
		})
	}
}

// TestFrameInvariants_PanelSurvival is issue #158: the same matrix as
// TestFrameInvariants_Grid, asserting that the frame which fits is
// also the frame that still has all its panels in it.
//
// It walks the grid separately rather than riding on the Grid test's
// t.Run so a failure names which invariant broke — "the frame is too
// tall" and "the frame lost its footer" are different bugs with
// different fixes, and a clip pass is capable of turning the second
// into a clean bill of health on the first.
func TestFrameInvariants_PanelSurvival(t *testing.T) {
	layouts := []struct {
		name   string
		layout StatusLayout
	}{
		{"header", StatusHeader},
		{"sidebar", StatusSidebar},
	}
	for _, lay := range layouts {
		for _, st := range frameStates() {
			for _, w := range frameWidths {
				for _, h := range frameHeights {
					name := lay.name + "/" + st.name + "/" +
						strconv.Itoa(w) + "x" + strconv.Itoa(h)
					t.Run(name, func(t *testing.T) {
						m := newFrameModel(lay.layout, w, h)
						m = st.setup(t, m, w, h)
						assertPanelsSurvive(t, m, w, h)
					})
				}
			}
		}
	}
}

// TestFrameInvariants_RenderersHonorWidth is issue #159: the same
// matrix again, asserting that the frame which fits and keeps its
// panels is also a frame the clip pass did not have to rescue.
//
// A separate walk rather than another assertion inside the Grid test,
// for the reason TestFrameInvariants_PanelSurvival gives: "the frame
// is too wide", "the frame lost its footer" and "the sidebar renderer
// overran its own fixed column" are three different bugs with three
// different fixes, and the third is the only one that can be true
// while the other two are false.
func TestFrameInvariants_RenderersHonorWidth(t *testing.T) {
	layouts := []struct {
		name   string
		layout StatusLayout
	}{
		{"header", StatusHeader},
		{"sidebar", StatusSidebar},
	}
	for _, lay := range layouts {
		for _, st := range frameStates() {
			for _, w := range frameWidths {
				for _, h := range frameHeights {
					name := lay.name + "/" + st.name + "/" +
						strconv.Itoa(w) + "x" + strconv.Itoa(h)
					t.Run(name, func(t *testing.T) {
						m := newFrameModel(lay.layout, w, h)
						m = st.setup(t, m, w, h)
						assertRenderersHonorWidth(t, m)
					})
				}
			}
		}
	}
}

// TestRenderSidebar_BoundsHostSuppliedContent is the case the grid
// above cannot reach: the sidebar's content comes from the HOST — a
// model id from displayModelName, a roster from SubagentLister — and
// the grid drives a bare agent that supplies neither, so every cell of
// it renders a sidebar whose longest line is the word "subagents".
//
// The width it is asserted against is sidebarWidth rather than the
// column band the layout leaves beside the chat, because those are
// different numbers: chromeWidth() reserves two columns of slack, and
// a bound that permits the slack permits a panel that is not the
// fixed-width panel it is documented to be. The values here are
// deliberately realistic rather than pathological — a provider-
// qualified model id is what a host actually passes, and it is 50-odd
// cells against a 32-cell column.
func TestRenderSidebar_BoundsHostSuppliedContent(t *testing.T) {
	m := newFrameModel(StatusSidebar, 120, 40)
	m.currentModel = "anthropic/claude-opus-4-1-20250805-extended-thinking"
	m.hostSnap.valid = true
	m.hostSnap.subagents = []SubagentInfo{
		{Name: "a-subagent-with-a-really-quite-long-name", Status: "running"},
		{Name: "short", Status: "idle"},
	}

	lines := strings.Split(m.renderSidebar(), "\n")
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > sidebarWidth {
			t.Errorf("sidebar line %d is %d cols against a %d-column panel (overflow %d) — "+
				"JoinVertical widens the whole block to the longest line, JoinHorizontal then "+
				"makes the composed body that much wider than the terminal, and clipFrame takes "+
				"the difference off the right edge of EVERY row: %q",
				i, got, sidebarWidth, got-sidebarWidth, ansi.Strip(line))
		}
	}
	// A bound that achieved the width by rendering nothing would pass
	// the loop above, so pin that the rows still carry their content up
	// to the cut.
	if want := "anthropic/claude"; !strings.Contains(ansi.Strip(lines[0]), want) {
		t.Errorf("sidebar row 0 = %q, want the model id truncated rather than dropped (looking for %q)",
			ansi.Strip(lines[0]), want)
	}
}

// TestFrameInvariants_ZeroSize pins the degenerate cases the clip
// post-pass has to guard: an unsized model renders empty, and a
// model whose Update never delivered a WindowSizeMsg must not panic
// through the truncation path.
func TestFrameInvariants_ZeroSize(t *testing.T) {
	m := NewModel(Options{Agent: &bareAgent{id: "zero"}})
	if got := m.View().Content; got != "" {
		t.Errorf("unsized model should render empty, got %q", got)
	}
}
