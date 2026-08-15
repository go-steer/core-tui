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

// Hardware-cursor tests (issue #105).
//
// A cursor assertion that only checks "not nil" is worthless: the
// bug this file exists to catch is an OFFSET being wrong, and every
// wrong offset is still non-nil. So the assertions here read the
// composed frame back and check what is actually printed in the
// cells the cursor claims to sit after — if the cursor says column
// 8 of row 21, then the first 8 cells of row 21 had better be
// "▎ hello". That catches an off-by-one, a layout confusion, and a
// bytes-vs-cells measurement error, none of which a coordinate
// comparison against a hand-computed constant would.

package tui

import (
	"strconv"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// cursorModel builds a sized model in the requested layout with the
// theme pinned, matching newFrameModel but with the inline
// permission layout left at its default.
func cursorModel(t *testing.T, layout StatusLayout, w, h int) Model {
	t.Helper()
	m := NewModel(Options{
		Agent:        &bareAgent{id: "cursor"},
		StatusLayout: layout,
	})
	m.styles = NewStylesWithTheme(true, goldenTheme())
	out, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return out.(Model)
}

// typeInto feeds text into the model one grapheme at a time through
// the real Update path, so the test exercises the same keystroke
// plumbing an operator does (and not textarea.SetValue, which skips
// it).
func typeIntoModel(m Model, text string) Model {
	for _, r := range text {
		out, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)}))
		m = out.(Model)
	}
	return m
}

// frameRowPrefix returns the first x terminal cells of row y of
// frame, with ANSI escapes stripped. This is "what is printed to the
// LEFT of the cursor" — the thing that proves a coordinate is right.
func frameRowPrefix(t *testing.T, frame string, x, y int) string {
	t.Helper()
	lines := strings.Split(frame, "\n")
	if y < 0 || y >= len(lines) {
		t.Fatalf("cursor row %d is outside the %d-row frame", y, len(lines))
	}
	return ansi.Strip(ansi.Truncate(lines[y], x, ""))
}

// assertCursorFollows checks that the cursor sits immediately after
// want on its row — i.e. that both coordinates are right. Matched as
// a suffix because a centered modal is preceded by lipgloss.Place's
// padding, which is not what the test is pinning; a cursor off by one
// in either direction still fails, because the prefix would carry one
// cell too many or one too few.
func assertCursorFollows(t *testing.T, v tea.View, want string) {
	t.Helper()
	if v.Cursor == nil {
		t.Fatalf("no cursor set; expected one just after %q", want)
	}
	got := frameRowPrefix(t, v.Content, v.Cursor.X, v.Cursor.Y)
	if !strings.HasSuffix(got, want) {
		t.Errorf("cursor at (%d,%d) sits after %q, want it to sit after %q",
			v.Cursor.X, v.Cursor.Y, got, want)
	}
}

// TestCursor_TextareaHeaderLayout pins the common case: the chat
// textarea focused in StatusHeader layout. The cursor must land on
// the textarea row, one column past the typed text and past the
// prompt rail.
func TestCursor_TextareaHeaderLayout(t *testing.T) {
	m := typeIntoModel(cursorModel(t, StatusHeader, 100, 30), "hello")
	v := m.View()

	assertCursorFollows(t, v, DefaultPromptGlyph+"hello")

	// Independently: the row above the textarea is renderInputBox's
	// top rule, which is nothing but the rule glyph. If the cursor
	// drifted onto the chat or the footer this fails too.
	if rule := frameRowPrefix(t, v.Content, 10, v.Cursor.Y-1); strings.Trim(rule, GlyphRule) != "" {
		t.Errorf("row above the cursor is %q, want renderInputBox's top rule", rule)
	}
}

// TestCursor_TextareaSidebarLayout is the same assertion in the
// layout with a different origin. StatusSidebar drops the header and
// puts the input inside a chatWidth-wide left column, so a cursor
// path that hard-coded the header layout's row offset — or assumed
// the input box spans m.width — lands somewhere else here. This is
// where an offset bug hides.
func TestCursor_TextareaSidebarLayout(t *testing.T) {
	const w, h = 100, 30
	m := typeIntoModel(cursorModel(t, StatusSidebar, w, h), "hello")
	v := m.View()

	if got := m.effectiveLayout(); got != StatusSidebar {
		t.Fatalf("layout fell back to %v; the sidebar arm is not under test", got)
	}
	assertCursorFollows(t, v, DefaultPromptGlyph+"hello")

	// The input lives in the left column, not across the frame: the
	// cursor must stay left of the sidebar divider.
	chatWidth := w - sidebarWidth - 3
	if v.Cursor.X >= chatWidth {
		t.Errorf("cursor x=%d is at or past the chat column edge (%d) — it is on the sidebar",
			v.Cursor.X, chatWidth)
	}

	// The cursor's own row must carry the sidebar chrome to its
	// right — the divider at chatWidth and the panel beyond it. If
	// the row offset were computed against the header layout's stack
	// this lands on the footer or the toast, neither of which has a
	// divider.
	row := ansi.Strip(strings.Split(v.Content, "\n")[v.Cursor.Y])
	if !strings.Contains(row, GlyphColumn) {
		t.Errorf("cursor row %d is %q; a sidebar-layout input row carries the %q divider",
			v.Cursor.Y, row, GlyphColumn)
	}
}

// TestCursor_MultibyteAdvancesByCells types text whose byte length,
// rune count and terminal width all disagree. The cursor column is a
// CELL count: é is one cell but two bytes, 日 is two cells and three
// bytes, 😀 is two cells and four bytes. Measuring with len() or with
// a rune count puts the caret — and therefore the IME candidate
// window — in the wrong column, which is precisely the failure this
// issue is about.
func TestCursor_MultibyteAdvancesByCells(t *testing.T) {
	base := cursorModel(t, StatusHeader, 100, 30)
	empty := base.View()
	if empty.Cursor == nil {
		t.Fatal("empty focused textarea should still own the cursor")
	}
	originX := empty.Cursor.X

	for _, tc := range []struct {
		text  string
		cells int
	}{
		{"a", 1},
		{"é", 1},
		{"日", 2},
		{"😀", 2},
		{"aé日😀", 6},
	} {
		t.Run(tc.text, func(t *testing.T) {
			v := typeIntoModel(base, tc.text).View()
			if v.Cursor == nil {
				t.Fatal("cursor went nil after typing")
			}
			if want := ansi.StringWidth(tc.text); want != tc.cells {
				t.Fatalf("test bug: %q is %d cells, table says %d", tc.text, want, tc.cells)
			}
			if got := v.Cursor.X - originX; got != tc.cells {
				t.Errorf("typing %q advanced the cursor %d columns, want %d cells "+
					"(bytes=%d runes=%d)",
					tc.text, got, tc.cells, len(tc.text), len([]rune(tc.text)))
			}
			assertCursorFollows(t, v, DefaultPromptGlyph+tc.text)
		})
	}
}

// TestCursor_ModalTakesCursor opens a text-input Dialog and checks
// the cursor moves into it. The dialog is centered with
// lipgloss.Place, so its origin is a function of its own rendered
// size — this fails loudly if that origin is guessed rather than
// derived.
func TestCursor_ModalTakesCursor(t *testing.T) {
	m := typeIntoModel(cursorModel(t, StatusHeader, 100, 30), "hello")
	behind := m.View()
	if behind.Cursor == nil {
		t.Fatal("precondition: the textarea should own the cursor before the modal opens")
	}

	m.overlayStack.Open(NewTextInputDialog(TextInputConfig{
		Title:   "Attach to Endpoint",
		Prompt:  "Daemon URL:",
		Initial: "https://example.test",
	}))
	v := m.View()

	// "▎ " is the dialog input's own prompt rail (NewTextInputDialog).
	assertCursorFollows(t, v, "▎ https://example.test")
	if v.Cursor.Y == behind.Cursor.Y && v.Cursor.X == behind.Cursor.X {
		t.Error("cursor stayed on the chat textarea while a modal owned input")
	}
}

// TestCursor_ModalCaretCountsCells is issue #125's reproducer at the
// frame level: a text-input dialog holding double-width runes.
//
// bubbles' textinput.Cursor() computes its column as
// `m.Position() + promptWidth` — a RUNE INDEX plus a CELL WIDTH — so
// every wide rune before the caret loses it a cell. With eight CJK
// runes the caret lands eight columns left of the glyph it belongs
// on, which is where an IME would open its candidate window. The
// irony noted in the issue is that IME anchoring is the entire reason
// the hardware cursor was wired up (#105).
//
// This is the same assertion as TestCursor_ModalTakesCursor with the
// initial value swapped, which is exactly how the issue describes
// reproducing it.
func TestCursor_ModalCaretCountsCells(t *testing.T) {
	for _, initial := range []string{
		"日本語プロジェクト",   // 9 runes, 18 cells
		"a日b語c",       // mixed widths
		"😀😀😀",         // 3 runes, 6 cells
		"plain-ascii", // the case that always worked
	} {
		t.Run(initial, func(t *testing.T) {
			m := cursorModel(t, StatusHeader, 100, 30)
			m.overlayStack.Open(NewTextInputDialog(TextInputConfig{
				Title:   "Attach to Endpoint",
				Prompt:  "Daemon URL:",
				Initial: initial,
			}))
			v := m.View()
			// "▎ " is the dialog input's own prompt rail.
			assertCursorFollows(t, v, "▎ "+initial)
		})
	}
}

// TestTextInputCursor_ColumnIsCells pins the corrected column
// directly, away from the modal chrome: prompt cells plus the cell
// width of the value up to the caret, for a caret anywhere in the
// value rather than only at the end.
func TestTextInputCursor_ColumnIsCells(t *testing.T) {
	cases := []struct {
		name   string
		prompt string
		value  string
		left   int // times to press Left from the end
		wantX  int
	}{
		{name: "ascii-end", prompt: "▎ ", value: "hello", wantX: 2 + 5},
		{name: "ascii-mid", prompt: "▎ ", value: "hello", left: 2, wantX: 2 + 3},
		{name: "cjk-end", prompt: "▎ ", value: "日本語", wantX: 2 + 6},
		{name: "cjk-mid", prompt: "▎ ", value: "日本語", left: 1, wantX: 2 + 4},
		{name: "cjk-start", prompt: "▎ ", value: "日本語", left: 3, wantX: 2},
		{name: "emoji", prompt: "▎ ", value: "😀x", left: 1, wantX: 2 + 2},
		{name: "combining", prompt: "▎ ", value: "café", wantX: 2 + 4},
		{name: "no-prompt", prompt: "", value: "日", wantX: 2},
		{name: "empty-value", prompt: "▎ ", value: "", wantX: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ti := textinput.New()
			ti.Prompt = tc.prompt
			ti.SetVirtualCursor(false)
			ti.SetWidth(40)
			_ = ti.Focus()
			ti.SetValue(tc.value)
			ti.CursorEnd()
			for i := 0; i < tc.left; i++ {
				ti, _ = ti.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft}))
			}
			c := textInputCursor(ti, tc.prompt)
			if c == nil {
				t.Fatal("focused input returned no cursor")
			}
			if c.X != tc.wantX {
				t.Errorf("cursor X = %d, want %d (value %q, caret at rune %d)",
					c.X, tc.wantX, tc.value, ti.Position())
			}
		})
	}
}

// TestTextInputCursor_ScrolledFallsBackToUpstream pins the HALF of
// issue #125 that cannot be fixed downstream, so the split stays
// deliberate rather than becoming an oversight someone later "fixes"
// with a guess.
//
// Bug 2 is that textinput.Cursor() ignores m.offset, the horizontal
// scroll position. There is no exported accessor for it — no
// Offset() on textinput.Model — so once the value is wider than the
// box, Value() and Position() no longer describe what is on screen
// and any column computed from them is a different wrong answer.
// Correcting it means reimplementing handleOverflow's arithmetic
// against state we cannot observe. So: fall through to upstream, and
// pin that we do.
//
// The value that FITS is corrected, which is where the caret spends
// almost all of its life. When the upstream fix lands, this is the
// test that changes.
func TestTextInputCursor_ScrolledFallsBackToUpstream(t *testing.T) {
	ti := textinput.New()
	ti.Prompt = "▎ "
	ti.SetVirtualCursor(false)
	ti.SetWidth(10)
	_ = ti.Focus()
	ti.SetValue("https://example.test/a/rather/long/path")
	ti.CursorEnd()
	for i := 0; i < 5; i++ {
		ti, _ = ti.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft}))
	}

	upstream := ti.Cursor()
	if upstream == nil {
		t.Fatal("focused input returned no cursor")
	}
	got := textInputCursor(ti, ti.Prompt)
	if got == nil {
		t.Fatal("textInputCursor dropped the cursor for a scrolled value")
	}
	if got.X != upstream.X {
		t.Errorf("scrolled value: textInputCursor X = %d, upstream = %d — "+
			"the scrolled case is documented as upstream's, not ours to guess",
			got.X, upstream.X)
	}

	// The boundary: shrink the value until it fits and the correction
	// takes over again.
	ti.SetValue("日本語")
	ti.CursorEnd()
	if got := textInputCursor(ti, ti.Prompt); got == nil || got.X != 2+6 {
		t.Fatalf("a value that fits should be corrected: got %v, want X=%d", got, 2+6)
	}
}

// TestTextInputCursor_BlurredIsNil keeps the nil contract: a blurred
// input owns no caret, and the correction must not manufacture one.
func TestTextInputCursor_BlurredIsNil(t *testing.T) {
	ti := textinput.New()
	ti.SetVirtualCursor(false)
	ti.SetValue("hello")
	ti.Blur()
	if c := textInputCursor(ti, ti.Prompt); c != nil {
		t.Errorf("blurred input yielded a cursor at X=%d", c.X)
	}
}

// TestCursor_ElicitFormField puts the cursor on the elicit form's
// focused field. The form is not a bubbles widget — the caret column
// is derived from the row format the renderer uses — so this also
// pins that the two stay in agreement.
func TestCursor_ElicitFormField(t *testing.T) {
	m := cursorModel(t, StatusHeader, 100, 30)
	out, _ := m.Update(elicitRequestMsg{
		serverName: "srv",
		req: ElicitRequest{
			Mode:  ElicitFormMode,
			Title: "Deployment target",
			Fields: []ElicitField{
				{Name: "project", Type: ElicitFieldString, Required: true},
				{Name: "region", Type: ElicitFieldString},
			},
		},
	})
	m = out.(Model)
	m = typeIntoModel(m, "acme")

	v := m.View()
	assertCursorFollows(t, v, "> project*:        acme")

	// Tab to the second field: the caret must follow the focus onto
	// the next row and sit at that field's own value column.
	out, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	m = out.(Model)
	empty := m.View()
	if empty.Cursor == nil {
		t.Fatal("cursor went nil after tabbing to the second string field")
	}
	if empty.Cursor.Y <= v.Cursor.Y {
		t.Errorf("tab moved the cursor to row %d, want a row below %d",
			empty.Cursor.Y, v.Cursor.Y)
	}

	// Typing two cells must advance the caret exactly two columns —
	// which also pins where it sat while the field was empty (at the
	// head of the value, not at the end of the "(empty)" placeholder
	// the renderer draws there).
	m = typeIntoModel(m, "eu")
	filled := m.View()
	assertCursorFollows(t, filled, "> region:          eu")
	if got := filled.Cursor.X - empty.Cursor.X; got != 2 {
		t.Errorf("typing 2 cells moved the caret %d columns; the empty-field "+
			"caret was not at the value column", got)
	}
}

// TestCursor_ElicitNonTextFieldHasNoCaret: a boolean toggles with
// space and an enum cycles with the arrow keys. Neither takes typed
// text, so neither should claim the terminal cursor.
func TestCursor_ElicitNonTextFieldHasNoCaret(t *testing.T) {
	m := cursorModel(t, StatusHeader, 100, 30)
	out, _ := m.Update(elicitRequestMsg{
		serverName: "srv",
		req: ElicitRequest{
			Mode:  ElicitFormMode,
			Title: "Deployment target",
			Fields: []ElicitField{
				{Name: "confirm", Type: ElicitFieldBoolean},
				{Name: "region", Type: ElicitFieldEnum, EnumChoices: []string{"a", "b"}},
			},
		},
	})
	m = out.(Model)
	for i, field := range []string{"boolean", "enum"} {
		if c := m.View().Cursor; c != nil {
			t.Errorf("%s field (index %d) claimed the cursor at (%d,%d)", field, i, c.X, c.Y)
		}
		out, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
		m = out.(Model)
	}
}

// TestCursor_NilWhenNothingOwnsIt covers every "leave it nil" arm:
// read-only modals, a blurred textarea, and a frame with no geometry
// yet. Parking the cursor somewhere arbitrary is worse than hiding
// it — the terminal draws a caret on whatever cell it is given.
func TestCursor_NilWhenNothingOwnsIt(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T) Model
	}{
		{
			name: "unsized-model",
			setup: func(*testing.T) Model {
				return NewModel(Options{Agent: &bareAgent{id: "cursor"}})
			},
		},
		{
			name: "blurred-textarea",
			setup: func(t *testing.T) Model {
				m := cursorModel(t, StatusHeader, 100, 30)
				m.input.Blur()
				return m
			},
		},
		{
			name: "side-answer-viewer",
			setup: func(t *testing.T) Model {
				m := cursorModel(t, StatusHeader, 100, 30)
				m.sideAnswer = &SideAnswer{
					Question: "what is a goroutine",
					Answer:   "a lightweight thread",
				}
				return m
			},
		},
		{
			name: "permission-overlay",
			setup: func(t *testing.T) Model {
				m := cursorModel(t, StatusHeader, 100, 30)
				m.opts.PermissionLayout = PermissionOverlay
				out, _ := m.Update(permissionRequestMsg{req: PermissionRequest{
					Kind:     PermissionKindBash,
					ToolName: "bash",
					Detail:   "rm -rf /tmp/x",
				}})
				return out.(Model)
			},
		},
		{
			// The three pickers used to be this case. Issue #117 gave
			// them a filter row and therefore a caret; the tool-call
			// detail overlay is the arrow-nav dialog that remains,
			// and it still has nothing to type into.
			name: "arrow-nav-dialog",
			setup: func(t *testing.T) Model {
				m := cursorModel(t, StatusHeader, 100, 30)
				m.overlayStack.Open(newToolCallDialog(0))
				return m
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.setup(t)
			if c := m.View().Cursor; c != nil {
				t.Errorf("cursor set to (%d,%d); nothing on screen owns it", c.X, c.Y)
			}
		})
	}
}

// TestCursor_InsideClippedFrame is the geometry invariant, run over
// the same state x size grid as TestFrameInvariants_Grid: whatever
// the cursor is, it must address a cell that survived clipFrame. A
// cursor past the right edge is on a column that was truncated away;
// a cursor past the bottom is on a row that was dropped, and Bubble
// Tea would scroll the frame to reach it.
func TestCursor_InsideClippedFrame(t *testing.T) {
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
						m = typeIntoModel(m, "hi")
						assertCursorInFrame(t, m.View(), w, h)
					})
				}
			}
		}
	}
}

// assertCursorInFrame checks the cursor addresses a real cell of the
// frame that was actually rendered.
func assertCursorInFrame(t *testing.T, v tea.View, w, h int) {
	t.Helper()
	if v.Cursor == nil {
		return // hidden is always in bounds
	}
	x, y := v.Cursor.X, v.Cursor.Y
	if x < 0 || y < 0 {
		t.Fatalf("cursor at (%d,%d) has a negative coordinate", x, y)
	}
	if x >= w || y >= h {
		t.Fatalf("cursor at (%d,%d) is outside the %dx%d terminal", x, y, w, h)
	}
	lines := strings.Split(v.Content, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	if y >= len(lines) {
		t.Fatalf("cursor row %d is past the %d rendered rows — the surface was clipped away, "+
			"so the cursor should have been nil", y, len(lines))
	}
}

// TestClampCursor pins the two halves of the clamp rule directly:
// an out-of-frame ROW drops the cursor (its surface was clipped
// away), while an out-of-frame COLUMN is pulled back to the last
// cell (the caret legitimately sits one past the final glyph).
func TestClampCursor(t *testing.T) {
	cases := []struct {
		name       string
		in         *tea.Cursor
		w, h       int
		wantNil    bool
		wantX      int
		wantY      int
		wantReason string
	}{
		{name: "nil-in", in: nil, w: 80, h: 24, wantNil: true},
		{name: "zero-width", in: tea.NewCursor(0, 0), w: 0, h: 24, wantNil: true},
		{name: "zero-height", in: tea.NewCursor(0, 0), w: 80, h: 0, wantNil: true},
		{name: "in-bounds", in: tea.NewCursor(5, 7), w: 80, h: 24, wantX: 5, wantY: 7},
		{name: "last-cell", in: tea.NewCursor(79, 23), w: 80, h: 24, wantX: 79, wantY: 23},
		{name: "row-past-bottom", in: tea.NewCursor(5, 24), w: 80, h: 24, wantNil: true},
		{name: "negative-row", in: tea.NewCursor(5, -1), w: 80, h: 24, wantNil: true},
		{name: "col-past-right", in: tea.NewCursor(80, 3), w: 80, h: 24, wantX: 79, wantY: 3},
		{name: "negative-col", in: tea.NewCursor(-4, 3), w: 80, h: 24, wantX: 0, wantY: 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := clampCursor(tc.in, tc.w, tc.h)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("clampCursor = (%d,%d), want nil", got.X, got.Y)
				}
				return
			}
			if got == nil {
				t.Fatalf("clampCursor = nil, want (%d,%d)", tc.wantX, tc.wantY)
			}
			if got.X != tc.wantX || got.Y != tc.wantY {
				t.Errorf("clampCursor = (%d,%d), want (%d,%d)", got.X, got.Y, tc.wantX, tc.wantY)
			}
		})
	}
}

// TestPlaceCenterOffset checks the offset helper against
// lipgloss.Place itself rather than against a re-derivation of the
// same formula — if lipgloss changes how it splits the gap, this
// fails instead of silently misplacing every modal cursor.
func TestPlaceCenterOffset(t *testing.T) {
	for _, outer := range []int{0, 1, 10, 11, 80, 81, 100} {
		for _, inner := range []int{1, 2, 7, 10, 79, 100, 120} {
			block := strings.Repeat("#", inner)
			placed := lipglossPlaceHorizontalPad(block, outer)
			if got := placeCenterOffset(outer, inner); got != placed {
				t.Errorf("placeCenterOffset(%d, %d) = %d, lipgloss pads %d",
					outer, inner, got, placed)
			}
		}
	}
}

// lipglossPlaceHorizontalPad centers block in width cols the way
// View does and counts the leading spaces lipgloss actually emitted.
func lipglossPlaceHorizontalPad(block string, width int) int {
	placed := lipgloss.PlaceHorizontal(width, lipgloss.Center, block)
	return len(placed) - len(strings.TrimLeft(placed, " "))
}
