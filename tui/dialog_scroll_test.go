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

	tea "charm.land/bubbletea/v2"
)

func TestScrollState_ClampsToContent(t *testing.T) {
	s := &scrollState{}
	s.measure(100, 10) // 100 rows of body in a 10-row window

	if got := s.maxOffset(); got != 90 {
		t.Fatalf("maxOffset = %d, want 90", got)
	}
	s.by(5)
	if s.offset != 5 {
		t.Errorf("after by(5): offset = %d, want 5", s.offset)
	}
	s.by(-50) // past the top
	if s.offset != 0 {
		t.Errorf("after by(-50): offset = %d, want 0 (clamped to top)", s.offset)
	}
	s.by(500) // past the bottom
	if s.offset != 90 {
		t.Errorf("after by(500): offset = %d, want 90 (clamped to bottom)", s.offset)
	}
	// Content shrinking under a scrolled-down offset must not leave
	// the window pointing past the end.
	s.measure(12, 10)
	if s.offset != 2 {
		t.Errorf("after shrink: offset = %d, want 2", s.offset)
	}
}

// A body that fits its window has nothing to scroll, and an unsized
// terminal (view == 0) must not report a scrollable body either —
// that's the pre-resize frame, where the modal renders unwindowed.
func TestScrollState_NoOverflowWhenBodyFits(t *testing.T) {
	fits := &scrollState{}
	fits.measure(4, 10)
	if fits.overflows() || fits.maxOffset() != 0 {
		t.Errorf("body that fits: overflows = %v, maxOffset = %d, want false / 0", fits.overflows(), fits.maxOffset())
	}
	unsized := &scrollState{}
	unsized.measure(400, 0)
	if unsized.overflows() || unsized.maxOffset() != 0 {
		t.Errorf("unsized terminal: overflows = %v, maxOffset = %d, want false / 0", unsized.overflows(), unsized.maxOffset())
	}
}

func TestScrollState_ApplyStroke(t *testing.T) {
	cases := []struct {
		stroke string
		start  int
		want   int
		ok     bool
	}{
		{stroke: "down", start: 0, want: 1, ok: true},
		{stroke: "up", start: 4, want: 3, ok: true},
		{stroke: "pgdn", start: 0, want: 9, ok: true}, // view 10 → page 9
		{stroke: "pgup", start: 20, want: 11, ok: true},
		{stroke: "home", start: 40, want: 0, ok: true},
		{stroke: "end", start: 0, want: 90, ok: true},
		{stroke: "enter", start: 7, want: 7, ok: false},
		{stroke: "y", start: 7, want: 7, ok: false},
		// j/k stay unbound: the elicit form feeds printable runes to
		// its string fields, and stealing them would break typing.
		{stroke: "j", start: 7, want: 7, ok: false},
		{stroke: "k", start: 7, want: 7, ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.stroke, func(t *testing.T) {
			s := &scrollState{offset: tc.start}
			s.measure(100, 10)
			s.offset = tc.start
			if got := s.applyStroke(tc.stroke); got != tc.ok {
				t.Errorf("applyStroke(%q) = %v, want %v", tc.stroke, got, tc.ok)
			}
			if s.offset != tc.want {
				t.Errorf("applyStroke(%q): offset = %d, want %d", tc.stroke, s.offset, tc.want)
			}
		})
	}
}

// follow tracks a moving cursor but leaves a manually-scrolled window
// alone: an operator who wheeled away from the focused field is only
// snapped back when the focus itself moves.
func TestScrollState_FollowOnlyOnCursorMove(t *testing.T) {
	s := &scrollState{}
	s.measure(40, 10)

	s.follow(25) // cursor moved below the window
	if s.offset != 16 {
		t.Fatalf("follow(25): offset = %d, want 16", s.offset)
	}
	s.by(-10) // operator scrolls up, away from the cursor
	if s.offset != 6 {
		t.Fatalf("after manual scroll: offset = %d, want 6", s.offset)
	}
	s.follow(25) // same cursor on the next repaint: don't yank back
	if s.offset != 6 {
		t.Errorf("follow with unchanged cursor: offset = %d, want 6 (unchanged)", s.offset)
	}
	s.follow(0) // Tab back to the top field: window follows
	if s.offset != 0 {
		t.Errorf("follow(0): offset = %d, want 0", s.offset)
	}
}

func TestListWindow_KeepsCursorVisible(t *testing.T) {
	cases := []struct {
		name                          string
		offset, cursor, total, viewpt int
		want                          int
	}{
		{name: "fits-entirely", offset: 0, cursor: 3, total: 5, viewpt: 10, want: 0},
		{name: "unsized-terminal", offset: 4, cursor: 9, total: 50, viewpt: 0, want: 0},
		{name: "cursor-below-window", offset: 0, cursor: 12, total: 30, viewpt: 10, want: 3},
		{name: "cursor-above-window", offset: 8, cursor: 2, total: 30, viewpt: 10, want: 2},
		{name: "cursor-inside-window", offset: 5, cursor: 7, total: 30, viewpt: 10, want: 5},
		{name: "wrap-to-last-row", offset: 0, cursor: 29, total: 30, viewpt: 10, want: 20},
		{name: "offset-past-end-clamps", offset: 99, cursor: 0, total: 30, viewpt: 10, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := listWindow(tc.offset, tc.cursor, tc.total, tc.viewpt); got != tc.want {
				t.Errorf("listWindow(%d,%d,%d,%d) = %d, want %d",
					tc.offset, tc.cursor, tc.total, tc.viewpt, got, tc.want)
			}
		})
	}
}

func TestScrollView_WindowsAndDrawsScrollbar(t *testing.T) {
	styles := NewStyles(true, Branding{})
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = "line-" + strconv.Itoa(i)
	}

	got := scrollView(styles, lines, 20, 8, 5)
	if len(got) != 8 {
		t.Fatalf("windowed height = %d, want 8", len(got))
	}
	if !strings.Contains(got[0], "line-5") {
		t.Errorf("first visible row = %q, want it to carry line-5", got[0])
	}
	if !strings.Contains(got[7], "line-12") {
		t.Errorf("last visible row = %q, want it to carry line-12", got[7])
	}
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "█") || !strings.Contains(joined, "│") {
		t.Errorf("windowed body is missing the scrollbar column:\n%s", joined)
	}

	// Fits in the window / unsized terminal: untouched, no scrollbar.
	short := scrollView(styles, lines[:4], 20, 8, 0)
	if len(short) != 4 || strings.Contains(strings.Join(short, "\n"), "█") {
		t.Errorf("short body should render unwindowed and bare, got %d rows: %v", len(short), short)
	}
	unsized := scrollView(styles, lines, 20, 0, 0)
	if len(unsized) != 30 {
		t.Errorf("unsized terminal: %d rows, want all 30 (no windowing)", len(unsized))
	}
}

// Issue #92: the thumb must stay inside the drawn track at every
// offset, and must rest on the final row at maximum scroll. The old
// off-by-one put it one row past the end of the draw loop, which
// clipped a tall thumb and made a one-row thumb — the common case on
// long content — vanish exactly when the operator hit the bottom.
func TestScrollbar_ThumbStaysInTrackAtEveryOffset(t *testing.T) {
	styles := NewStyles(true, Branding{})
	grids := []struct {
		height, contentSize, viewportSize int
	}{
		{10, 200, 10}, // thumbSize 1 — the disappearing case
		{10, 100, 10},
		{10, 30, 10}, // thumbSize 3
		{8, 9, 8},    // thumbSize 7, maxOffset 1
		{5, 11, 5},
		{20, 21, 20},
	}
	for _, g := range grids {
		thumbSize := max(1, g.height*g.viewportSize/g.contentSize)
		maxOffset := g.contentSize - g.viewportSize
		for offset := 0; offset <= maxOffset; offset++ {
			rows := strings.Split(Scrollbar(styles, g.height, g.contentSize, g.viewportSize, offset), "\n")
			if len(rows) != g.height {
				t.Fatalf("%+v offset=%d: %d rows, want %d", g, offset, len(rows), g.height)
			}
			thumbs := 0
			for _, r := range rows {
				if strings.Contains(r, "█") {
					thumbs++
				}
			}
			if thumbs != thumbSize {
				t.Errorf("%+v offset=%d: %d thumb rows, want %d\n%s",
					g, offset, thumbs, thumbSize, strings.Join(rows, "\n"))
			}
		}
		bottom := strings.Split(Scrollbar(styles, g.height, g.contentSize, g.viewportSize, maxOffset), "\n")
		if !strings.Contains(bottom[g.height-1], "█") {
			t.Errorf("%+v at maxOffset=%d: last row is not a thumb cell\n%s",
				g, maxOffset, strings.Join(bottom, "\n"))
		}
		top := strings.Split(Scrollbar(styles, g.height, g.contentSize, g.viewportSize, 0), "\n")
		if !strings.Contains(top[0], "█") {
			t.Errorf("%+v at offset=0: first row is not a thumb cell\n%s", g, strings.Join(top, "\n"))
		}
	}

	// Content that fits (and a zero-height track) draw nothing.
	if got := Scrollbar(styles, 10, 5, 10, 0); got != "" {
		t.Errorf("content fits: Scrollbar = %q, want \"\"", got)
	}
	if got := Scrollbar(styles, 0, 100, 10, 0); got != "" {
		t.Errorf("zero height: Scrollbar = %q, want \"\"", got)
	}
}

func TestModalBodyHeight(t *testing.T) {
	if got := modalBodyHeight(0, modalChromeRows); got != 0 {
		t.Errorf("unknown terminal height → %d, want 0 (don't window)", got)
	}
	if got := modalBodyHeight(40, modalChromeRows); got != 40-modalChromeRows-modalMarginRows {
		t.Errorf("modalBodyHeight(40) = %d, want %d", got, 40-modalChromeRows-modalMarginRows)
	}
	// The last height in the normal regime: margin intact, and the
	// floor engaging on the tallest chrome is exactly what defines
	// the threshold (see modalFullscreenBelow).
	if got := modalBodyHeight(modalFullscreenBelow, modalPickerChromeRows); got != minModalBodyRows {
		t.Errorf("modalBodyHeight(%d, picker) = %d, want the %d-row floor",
			modalFullscreenBelow, got, minModalBodyRows)
	}
}

// TestModalFullscreen_ShedsMarginAndFloor is issue #142's arithmetic.
// Below modalFullscreenBelow the modal takes the whole terminal: the
// margin contributes nothing and minModalBodyRows stops applying, so
// chrome + body is the terminal height instead of a hard 11-row floor
// that clipFrame then decapitated from the bottom.
func TestModalFullscreen_ShedsMarginAndFloor(t *testing.T) {
	if modalFullscreen(0) {
		t.Error("unknown geometry (height 0) must not count as fullscreen")
	}
	if modalFullscreen(modalFullscreenBelow) {
		t.Errorf("height %d is the first NORMAL height, not a fullscreen one", modalFullscreenBelow)
	}
	if !modalFullscreen(modalFullscreenBelow - 1) {
		t.Errorf("height %d is below the threshold and must be fullscreen", modalFullscreenBelow-1)
	}
	if got := modalMargin(modalFullscreenBelow); got != modalMarginRows {
		t.Errorf("normal-regime margin = %d, want %d", got, modalMarginRows)
	}
	if got := modalMargin(modalFullscreenBelow - 1); got != 0 {
		t.Errorf("fullscreen margin = %d, want 0", got)
	}

	for _, tc := range []struct{ h, chrome, want int }{
		// Issue #142's worked example: shed the margin at eight rows
		// and chrome 5 + body 3 is exactly eight.
		{h: 8, chrome: modalChromeRows, want: 3},
		{h: 8, chrome: modalPickerChromeRows, want: 2},
		// Relaxed floor: two rows, then one, rather than a hard 3.
		{h: 7, chrome: modalChromeRows, want: 2},
		{h: 6, chrome: modalChromeRows, want: 1},
		// Below the chrome floor nothing helps. Never zero (which
		// scrollView reads as "don't window", i.e. render the lot)
		// and never negative.
		{h: 5, chrome: modalChromeRows, want: 1},
		{h: 1, chrome: modalPickerChromeRows, want: 1},
	} {
		got := modalBodyHeight(tc.h, tc.chrome)
		if got != tc.want {
			t.Errorf("modalBodyHeight(%d, %d) = %d, want %d", tc.h, tc.chrome, got, tc.want)
		}
		if tc.h > tc.chrome && got+tc.chrome > tc.h {
			t.Errorf("modalBodyHeight(%d, %d): chrome+body = %d overflows the terminal",
				tc.h, tc.chrome, got+tc.chrome)
		}
	}
}

// --- wheel routing -------------------------------------------------

func wheel(button tea.MouseButton) tea.MouseWheelMsg {
	return tea.MouseWheelMsg(tea.Mouse{Button: button})
}

// keyPress builds the KeyPressMsg whose String() is stroke, for the
// handful of navigation keys these tests drive.
func keyPress(stroke string) tea.KeyPressMsg {
	codes := map[string]rune{
		"up":    tea.KeyUp,
		"down":  tea.KeyDown,
		"pgup":  tea.KeyPgUp,
		"pgdn":  tea.KeyPgDown,
		"home":  tea.KeyHome,
		"end":   tea.KeyEnd,
		"esc":   tea.KeyEscape,
		"enter": tea.KeyEnter,
	}
	if code, ok := codes[stroke]; ok {
		return tea.KeyPressMsg(tea.Key{Code: code})
	}
	// ctrl+<letter> — spelled generically so callers don't have to
	// extend the table for every chord.
	if rest, ok := strings.CutPrefix(stroke, "ctrl+"); ok && len(rest) == 1 {
		return tea.KeyPressMsg(tea.Key{Code: rune(rest[0]), Mod: tea.ModCtrl})
	}
	// A bare printable rune — `?`, say. Text is set as well as Code:
	// it is what the key reports as its String() and what the textarea
	// inserts when the stroke falls through to it.
	if runes := []rune(stroke); len(runes) == 1 {
		return tea.KeyPressMsg(tea.Key{Code: runes[0], Text: stroke})
	}
	panic("keyPress: unmapped stroke " + stroke)
}

// The chat viewport keeps the wheel when no modal is up — handleWheel
// must decline so Update forwards the event as it always has.
func TestHandleWheel_NoModalFallsThroughToViewport(t *testing.T) {
	m := NewModel(Options{Agent: &bareAgent{id: "a"}})
	if _, handled := m.handleWheel(wheel(tea.MouseWheelDown)); handled {
		t.Error("handleWheel claimed the event with no modal open")
	}
}

func TestHandleWheel_ScrollsSideAnswer(t *testing.T) {
	m := sideAnswerModel(t, 200)
	if _, handled := m.handleWheel(wheel(tea.MouseWheelDown)); !handled {
		t.Fatal("wheel not claimed by the side-answer modal")
	}
	if m.scroll().offset != wheelScrollLines {
		t.Errorf("offset after one wheel-down = %d, want %d", m.scroll().offset, wheelScrollLines)
	}
	if _, handled := m.handleWheel(wheel(tea.MouseWheelUp)); !handled {
		t.Fatal("wheel-up not claimed")
	}
	if m.scroll().offset != 0 {
		t.Errorf("offset after wheel back up = %d, want 0", m.scroll().offset)
	}
}

// An inline permission prompt lives in the chat flow, so the wheel
// belongs to the viewport; the centered overlay owns it instead.
func TestHandleWheel_PermissionLayoutDecidesOwner(t *testing.T) {
	req := &PermissionRequest{ToolName: "bash", Detail: strings.Repeat("echo hi\n", 200), DetailKind: DetailShell}

	inline := NewModel(Options{Agent: &bareAgent{id: "a"}})
	inline.width, inline.height = 100, 30
	inline.pendingPermission = req
	if _, handled := inline.handleWheel(wheel(tea.MouseWheelDown)); handled {
		t.Error("inline permission layout claimed the wheel; the chat viewport should keep it")
	}

	overlay := NewModel(Options{Agent: &bareAgent{id: "a"}, PermissionLayout: PermissionOverlay})
	overlay.width, overlay.height = 100, 30
	overlay.pendingPermission = req
	if _, handled := overlay.handleWheel(wheel(tea.MouseWheelDown)); !handled {
		t.Error("centered permission overlay did not claim the wheel")
	}
}

// Wheel over a Dialog that scrolls by lines moves its body; over a
// cursor picker it steps the selection once (not three rows' worth).
func TestOverlayHandleWheel_ScrollDialogVsPicker(t *testing.T) {
	m := modelWithTools(t)
	d := newToolCallDialog(3)
	m.overlayStack.Open(d)
	// Prime the geometry the way a render would.
	d.lastBody, d.lastView = 200, 10

	if consumed, _ := m.overlayStack.HandleWheel(wheelScrollLines, m); !consumed {
		t.Fatal("tool-call dialog did not consume the wheel")
	}
	if d.scroll != wheelScrollLines {
		t.Errorf("tool-call scroll = %d, want %d", d.scroll, wheelScrollLines)
	}

	// The picker repaints the chat on every preview, so it needs a
	// fully-wired model rather than the bare tool-call fixture.
	pm := NewModel(Options{Agent: &bareAgent{id: "a"}})
	pm.width, pm.height = 100, 30
	pm.resize()
	tp := newThemePickerDialog("default")
	pm.overlayStack.Open(tp)
	start := tp.idx
	if consumed, _ := pm.overlayStack.HandleWheel(wheelScrollLines, &pm); !consumed {
		t.Fatal("theme picker did not consume the wheel")
	}
	if tp.idx != start+1 {
		t.Errorf("theme picker moved %d rows on one wheel tick, want exactly 1", tp.idx-start)
	}
}

func TestToolCallDialog_ScrollByClampsToBody(t *testing.T) {
	d := &toolCallDialog{lastBody: 20, lastView: 8}
	d.ScrollBy(100, nil)
	if d.scroll != 12 {
		t.Errorf("scroll after overshoot = %d, want 12 (20 - 8)", d.scroll)
	}
	d.ScrollBy(-100, nil)
	if d.scroll != 0 {
		t.Errorf("scroll after undershoot = %d, want 0", d.scroll)
	}
}

// Scrolling the subagent log up releases the follow-the-tail pin;
// wheeling back to the bottom re-arms it.
func TestSubagentDialog_ScrollByTogglesPin(t *testing.T) {
	m := Model{}
	m.styles = NewStyles(true, Branding{})
	m.height = 40
	d := newSubagentDialog("worker")
	d.lastBody = 200
	d.scroll = nonNeg(d.lastBody - detailViewportHeight(m.height))

	d.ScrollBy(-wheelScrollLines, &m)
	if d.pinned {
		t.Error("scrolling up left the tail pinned")
	}
	d.ScrollBy(wheelScrollLines, &m)
	if !d.pinned {
		t.Error("scrolling back to the bottom did not re-arm the tail pin")
	}
}

// --- keyboard scrolling on the inline modals -----------------------

// sideAnswerModel returns a sized model with an n-line /btw answer
// open, already rendered once so the scroll geometry is measured.
func sideAnswerModel(t *testing.T, n int) *Model {
	t.Helper()
	m := NewModel(Options{Agent: &bareAgent{id: "a"}})
	m.width, m.height = 100, 30
	m.resize()
	body := make([]string, n)
	for i := range body {
		body[i] = "answer line " + strconv.Itoa(i)
	}
	m.sideAnswer = &SideAnswer{Question: "why?", Answer: strings.Join(body, "\n\n")}
	m.renderSideAnswer() // measures into m.modalScroll
	return &m
}

func TestSideAnswer_ScrollsWithKeys(t *testing.T) {
	m := sideAnswerModel(t, 200)
	if !m.scroll().overflows() {
		t.Fatal("200-line answer in a 30-row terminal should overflow")
	}

	out, _ := m.handleKey(keyPress("down"))
	got := out.(Model)
	if got.sideAnswer == nil {
		t.Fatal("down dismissed the modal; it should scroll it")
	}
	if got.scroll().offset != 1 {
		t.Errorf("offset after down = %d, want 1", got.scroll().offset)
	}

	out, _ = got.handleKey(keyPress("end"))
	got = out.(Model)
	if want := got.scroll().maxOffset(); got.scroll().offset != want || want == 0 {
		t.Errorf("offset after end = %d, want %d", got.scroll().offset, want)
	}

	// Dismissal keys still win over scrolling.
	out, _ = got.handleKey(keyPress("esc"))
	if out.(Model).sideAnswer != nil {
		t.Error("esc did not dismiss the side answer")
	}
}

// The windowed body must actually shrink to the terminal, which is
// the bug: before this, a long answer painted past the bottom edge.
func TestSideAnswer_RenderWindowsToTerminal(t *testing.T) {
	m := sideAnswerModel(t, 200)
	rendered := m.renderSideAnswer()
	if h := strings.Count(rendered, "\n") + 1; h > m.height {
		t.Errorf("side-answer modal is %d rows tall in a %d-row terminal", h, m.height)
	}
	if !strings.Contains(rendered, "↑↓ scroll") {
		t.Error("overflowing side answer does not advertise the scroll keys")
	}
}

// A short answer keeps the old chrome exactly: no scrollbar, no hint.
func TestSideAnswer_ShortAnswerUnchanged(t *testing.T) {
	m := sideAnswerModel(t, 2)
	rendered := m.renderSideAnswer()
	if strings.Contains(rendered, "↑↓ scroll") {
		t.Error("short answer should not advertise scroll keys")
	}
	if strings.Contains(rendered, "█") {
		t.Error("short answer should not draw a scrollbar")
	}
}

func TestPermissionOverlay_ScrollsWithKeys(t *testing.T) {
	m := NewModel(Options{Agent: &bareAgent{id: "a"}, PermissionLayout: PermissionOverlay})
	m.width, m.height = 100, 30
	m.resize()
	m.pendingPermission = &PermissionRequest{
		ToolName:   "bash",
		Detail:     strings.Repeat("echo hello\n", 200),
		DetailKind: DetailShell,
	}
	m.renderPermissionModal()
	if !m.scroll().overflows() {
		t.Fatal("200-line shell detail in a 30-row terminal should overflow")
	}

	out, _ := m.handleKey(keyPress("pgdn"))
	got := out.(Model)
	if got.pendingPermission == nil {
		t.Fatal("pgdn resolved the permission prompt; it should only scroll")
	}
	if got.scroll().offset == 0 {
		t.Error("pgdn did not scroll the permission detail")
	}

	// Decision keys still take precedence over scroll keys.
	rendered := got.renderPermissionModal()
	if !strings.Contains(rendered, "↑↓ scroll") {
		t.Error("overflowing permission overlay does not advertise the scroll keys")
	}
	if h := strings.Count(rendered, "\n") + 1; h > got.height {
		t.Errorf("permission overlay is %d rows tall in a %d-row terminal", h, got.height)
	}
}

// Tab-ing to a field below the fold scrolls it into view.
func TestElicitModal_FollowsFocusedField(t *testing.T) {
	m := NewModel(Options{Agent: &bareAgent{id: "a"}})
	m.width, m.height = 100, 24
	m.resize()
	fields := make([]ElicitField, 40)
	for i := range fields {
		fields[i] = ElicitField{Name: "field" + strconv.Itoa(i), Type: ElicitFieldString}
	}
	m.pendingElicit = &ElicitRequest{Title: "big form", Fields: fields}
	m.elicitValues = map[string]any{}
	m.renderElicitModal()
	if !m.scroll().overflows() {
		t.Fatal("40-field form in a 24-row terminal should overflow")
	}

	for i := 0; i < 30; i++ {
		m.handleElicitKey("tab")
	}
	m.renderElicitModal()
	sc := m.scroll()
	if m.elicitFieldIdx != 30 {
		t.Fatalf("field index = %d, want 30", m.elicitFieldIdx)
	}
	if sc.offset == 0 {
		t.Error("focused field walked below the fold without scrolling the form")
	}
	if 30 < sc.offset || 30 >= sc.offset+sc.view {
		t.Errorf("focused row 30 outside the window [%d,%d)", sc.offset, sc.offset+sc.view)
	}

	// Arrow keys scroll the form body rather than falling through.
	before := sc.offset
	m.handleElicitKey("up")
	if m.scroll().offset != before-1 {
		t.Errorf("up in the elicit form: offset %d → %d, want %d", before, m.scroll().offset, before-1)
	}
	if m.elicitFieldIdx != 30 {
		t.Errorf("up moved the field cursor to %d; it should only scroll", m.elicitFieldIdx)
	}
}

// --- picker windowing ----------------------------------------------

func TestThemePicker_WindowsLongListToTerminal(t *testing.T) {
	m := Model{}
	m.styles = NewStyles(true, Branding{})
	m.width, m.height = 100, 14
	d := newThemePickerDialog("default")

	rendered := d.Render(m.width, &m)
	if h := strings.Count(rendered, "\n") + 1; h > m.height {
		t.Errorf("theme picker is %d rows tall in a %d-row terminal", h, m.height)
	}

	// Walking the cursor past the fold pulls the window along.
	last := len(BuiltinThemes()) - 1
	for i := 0; i < last; i++ {
		d.HandleKey("down", &m)
	}
	rendered = d.Render(m.width, &m)
	if !strings.Contains(rendered, BuiltinThemes()[last].Name) {
		t.Errorf("last theme %q not visible after scrolling to it:\n%s", BuiltinThemes()[last].Name, rendered)
	}
}

// An unsized model (no WindowSizeMsg yet) renders every row, exactly
// as it did before windowing existed.
func TestThemePicker_UnsizedRendersEveryRow(t *testing.T) {
	m := Model{}
	m.styles = NewStyles(true, Branding{})
	d := newThemePickerDialog("default")
	rendered := d.Render(80, &m)
	for _, bt := range BuiltinThemes() {
		if !strings.Contains(rendered, bt.Name) {
			t.Errorf("theme %q missing from an unsized render", bt.Name)
		}
	}
}
