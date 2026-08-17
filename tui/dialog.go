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

// Dialog overlay stack (agentic-tui skill §9). Replaces the ad-hoc
// modal precedence cascade with a Dialog interface + Overlay
// container so adding a new modal is one type + one Open() call
// instead of two new fields + a new case in the Esc cascade + a
// new case in renderTUI's z-order switch.
//
// Permission and Elicit modals still use their inline state in
// Model (pendingPermission / pendingElicit) because they're tied
// to the channel-based Prompter / Elicitor lifecycle that needs
// special dispatch semantics. New modals (model picker today;
// settings / debug panels future) ride the Overlay.

package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Dialog is the contract for any modal that wants to ride the
// Overlay stack. Each method is keystroke-driven; the front-most
// dialog gets every key until it returns DialogActionClose.
type Dialog interface {
	// ID is a stable identifier (e.g. "model-picker", "settings")
	// so Overlay.Close(id) can target a specific dialog regardless
	// of z-order.
	ID() string

	// HandleKey is invoked for every keystroke the front-most
	// dialog receives. Returns the action the Overlay should
	// take (consume + render; close + pop; etc.).
	HandleKey(stroke string, m *Model) DialogAction

	// Render returns the styled string for the dialog body at
	// the given total terminal width. The Overlay wraps the
	// result in chrome via RenderContext.
	Render(width int, m *Model) string
}

// KeyMsgDialog is an optional extension of Dialog for modals whose
// body owns a real text-editing widget (issue #56's TextInputDialog
// is the first). Dialog.HandleKey receives a NORMALIZED stroke
// ("ctrl+u", "shift+enter") which is lossy for an input widget: it
// drops Key.Text — the grapheme(s) the terminal actually delivered,
// which is exactly what a textinput wants to insert — and it can't
// carry a bracketed paste.
//
// Dialogs that implement this interface get the raw
// tea.KeyPressMsg from Overlay.HandleKeyMsg; every other Dialog
// keeps the stroke-string contract untouched. Implementations must
// still provide HandleKey (Dialog is embedded) — the convention is
// to synthesize a KeyPressMsg from the stroke and delegate, so both
// entry points stay behaviorally identical.
type KeyMsgDialog interface {
	Dialog

	// HandleKeyMsg is the full-fidelity twin of HandleKey.
	HandleKeyMsg(msg tea.KeyPressMsg, m *Model) DialogAction
}

// DialogAction is the return shape of HandleKey. Composite so
// dialogs can signal "consume key" + "close me" + "emit a Cmd"
// (e.g. ThemeChangedMsg from the theme picker) in one go.
type DialogAction struct {
	// Consumed reports whether the dialog handled the key. When
	// false, the Overlay lets the key fall through to the rest of
	// the handleKey switch (e.g. for Ctrl+C which always quits).
	Consumed bool
	// Close pops THIS dialog off the stack after the current
	// frame renders. Pair with Consumed=true to also stop the
	// key from falling through.
	Close bool
	// Cmd is an optional tea.Cmd to dispatch alongside the
	// state mutation — used by dialogs that need to notify the
	// host of a commit (e.g. ThemeChangedMsg). Nil for the
	// common case where the dialog just mutates Model.
	Cmd tea.Cmd
}

// Overlay is the modal z-order stack. Open() pushes onto the top;
// HandleKey routes only to the front; Render iterates in stack
// order so later opens render on top. Empty stack = no modal.
type Overlay struct {
	dialogs []Dialog
}

// Open pushes a new dialog onto the top of the stack. No
// dedup — opening "model-picker" twice stacks twice; callers
// that want singletons check HasID() first.
func (o *Overlay) Open(d Dialog) {
	o.dialogs = append(o.dialogs, d)
}

// Close removes the dialog with id from the stack (any
// position). No-op when not present.
func (o *Overlay) Close(id string) {
	out := o.dialogs[:0]
	for _, d := range o.dialogs {
		if d.ID() == id {
			continue
		}
		out = append(out, d)
	}
	o.dialogs = out
}

// CloseFront pops the front-most dialog. No-op on empty stack.
func (o *Overlay) CloseFront() {
	if len(o.dialogs) == 0 {
		return
	}
	o.dialogs = o.dialogs[:len(o.dialogs)-1]
}

// HasDialogs reports whether anything is open.
func (o *Overlay) HasDialogs() bool { return len(o.dialogs) > 0 }

// Get returns the dialog with id from anywhere in the stack, or nil.
// Unlike Front it doesn't care what's on top: an async reply for a
// dialog that another modal has since covered still belongs to it.
func (o *Overlay) Get(id string) Dialog {
	for _, d := range o.dialogs {
		if d.ID() == id {
			return d
		}
	}
	return nil
}

// HasID reports whether a dialog with id is on the stack
// (useful for singleton checks before Open).
func (o *Overlay) HasID(id string) bool {
	for _, d := range o.dialogs {
		if d.ID() == id {
			return true
		}
	}
	return false
}

// Front returns the front-most dialog, or nil on empty stack.
func (o *Overlay) Front() Dialog {
	if len(o.dialogs) == 0 {
		return nil
	}
	return o.dialogs[len(o.dialogs)-1]
}

// HandleKey routes the keystroke to the front-most dialog and
// applies the returned action. Returns Consumed so the caller
// (handleKey) can decide whether to fall through, plus an
// optional Cmd for dialogs that need to emit a msg (e.g. the
// theme picker firing ThemeChangedMsg on commit).
func (o *Overlay) HandleKey(stroke string, m *Model) (consumed bool, cmd tea.Cmd) {
	front := o.Front()
	if front == nil {
		return false, nil
	}
	act := front.HandleKey(stroke, m)
	if act.Close {
		o.CloseFront()
	}
	return act.Consumed, act.Cmd
}

// HandleKeyMsg is HandleKey with the raw keystroke preserved. When
// the front-most dialog implements KeyMsgDialog it gets the full
// tea.KeyPressMsg; otherwise we fall back to the normalized-stroke
// contract. This is the entry point handleKey uses — HandleKey
// stays for callers that only hold a stroke string.
func (o *Overlay) HandleKeyMsg(msg tea.KeyPressMsg, m *Model) (consumed bool, cmd tea.Cmd) {
	front := o.Front()
	if front == nil {
		return false, nil
	}
	var act DialogAction
	if kd, ok := front.(KeyMsgDialog); ok {
		act = kd.HandleKeyMsg(msg, m)
	} else {
		act = front.HandleKey(msg.String(), m)
	}
	if act.Close {
		o.CloseFront()
	}
	return act.Consumed, act.Cmd
}

// Render iterates the stack and returns the front-most dialog's
// styled string wrapped in modal chrome. Empty stack returns "".
// Today we only render the FRONT (no layered painting); future
// translucent overlays would draw deeper dialogs first.
func (o *Overlay) Render(width int, m *Model) string {
	front := o.Front()
	if front == nil {
		return ""
	}
	return front.Render(width, m)
}

// RenderContext assembles a dialog body with consistent chrome:
// title bar, body, footer. Mirrors agentic-tui skill §9.C —
// every dialog inherits identical border / title styling without
// duplicating the lipgloss boilerplate.
type RenderContext struct {
	Title  string
	Body   string
	Footer string
	Width  int
	// Height is the terminal height the composed modal has to fit
	// inside — Model.height at the call site. Zero means "unknown
	// geometry" (a pre-resize frame, or a bare Model{} in a test)
	// and disables the fit pass, which is the historical behavior.
	// Callers that set it get fitModalContent's guarantee: on a
	// terminal too short for the whole modal, spacing goes before
	// content and the footer key hint is the last thing sacrificed.
	Height int

	Styles Styles
}

// A modal wears a box edge on all four sides (issue #199).
//
// Since #156 the block is spliced OVER the frame rather than drawn
// instead of it, so there is transcript on both sides of every modal
// row and nothing marked where the modal stopped: the left and right
// edges were implied by wherever the content happened to end, and a
// short body row left the eye no way to tell dialog from background.
//
// Drawn rather than dimmed, which was the alternative #199 put up.
// A box character renders on every terminal we can be asked to run
// on; a dimmed backdrop needs an SGR-aware pass that has to survive
// host content already carrying its own colour and degrades to
// nothing where the terminal has no dim. It is also a layout change,
// which the frame invariants can police exactly — dimming is a
// colour change, which they cannot see at all.
//
// The edge is charged to the modal's OWN budget rather than added
// around it: total width and total height stay the numbers the layout
// already agreed to, and the two columns and two rows come out of the
// content. That is what keeps the centring, the margin and the
// clip pass untouched. Both costs are named here because a dozen call
// sites subtract them, and a subtraction copied by hand is precisely
// the shape of #157, #159 and #210.
const (
	// modalEdgeCols / modalEdgeRows are what the box itself spends.
	modalEdgeCols = 2
	modalEdgeRows = 2
	// modalPadCols is RenderContext's one column of padding inside
	// the edge, on each side.
	modalPadCols = 2
	// modalScrollbarCols is the gutter plus the bar column scrollView
	// glues onto the right of every windowed body row. Reserved
	// whether or not the body currently overflows, so it doesn't
	// reflow the moment it starts to.
	modalScrollbarCols = 2
)

// modalContentX / modalBodyTop are the modal-local coordinates of the
// body's first cell: past the box edge and the padding, and below the
// box edge, the title line and the blank row under it. The three
// caret paths (cursor.go, the picker filter row, the text-input
// dialog) add these rather than spelling the chrome out again, so a
// change to the chrome moves the hardware cursor with it — nothing
// else would catch that drift, because the golden corpus captures
// tea.View.Content and not tea.View.Cursor.
const (
	modalContentX = 1 + modalPadCols/2
	modalBodyTop  = 1 + 2
)

// modalInnerWidth is the content column inside a modal whose total
// width is w: what is left after the box edge and the padding on both
// sides. Every rule, every body and every caret offset in the package
// measures against this.
func modalInnerWidth(w int) int { return nonNeg(w - modalEdgeCols - modalPadCols) }

// modalBodyWidth is the column a WINDOWED body composes against —
// modalInnerWidth less the scrollbar column and its gutter.
func modalBodyWidth(w int) int { return nonNeg(modalInnerWidth(w) - modalScrollbarCols) }

// modalSurface draws the outer edge every modal surface wears, and is
// the single place it is drawn. The permission overlay, the elicit
// form, the embedded huh form, the side answer and everything on the
// Overlay stack all pass through here — a treatment applied to four
// of the five would read as a rendering bug rather than as a style.
//
// width is the TOTAL column count, edge included; a non-positive
// width leaves the block to size itself, which is what the huh form
// needs since it brings its own layout. padY is the vertical padding:
// zero for the RenderContext surfaces, whose chrome supplies its own
// spacer rows, and one for the huh form, which supplies none.
//
// termHeight is the terminal's height, and it buys the one case where
// the edge is NOT drawn. fitModalContent's last resort, on a terminal
// too short even for the footer key hint, is to return the hint alone
// and let clipFrame trim it: the operator gets the row that says which
// key closes the modal and nothing else. Two rows of edge on top of
// that block are two rows clipFrame takes off the bottom, and the
// bottom is where "esc cancel" ends — so the edge would be buying its
// own visibility with the only content #142 promised never to shed.
// Below that floor the edge goes and the hint stays. Everywhere else,
// including a fullscreen modal, it is drawn; a zero termHeight means
// the geometry is unknown and the fit pass is skipped, as ever.
//
// The edge takes its colour from ModalBorder — the same
// Theme.BorderActive token the title and footer rules already read —
// rather than from a literal, so a palette swap moves all three
// together. lipgloss does not inherit a style's Foreground into its
// border, hence the explicit BorderForeground.
func modalSurface(s Styles, content string, width, termHeight, padY int) string {
	st := s.ModalBorder.Padding(padY, modalPadCols/2)
	if width > 0 {
		st = st.Width(width)
	}
	if modalEdgeFits(content, width, termHeight, padY) {
		st = st.Border(lipgloss.RoundedBorder()).BorderForeground(s.Theme.BorderActive)
	}
	return st.Render(content)
}

// modalEdgeFits reports whether the terminal can afford the edge's two
// rows on top of the content — see modalSurface for why the answer is
// ever no.
//
// The content is measured WRAPPED, at the column it will occupy once
// the edge is on it. Counting its logical lines instead would read
// fitModalContent's last-resort return — one long footer hint, still
// unwrapped — as a single row and conclude there was room for the box
// on a four-row terminal that the hint alone fills.
func modalEdgeFits(content string, width, termHeight, padY int) bool {
	if termHeight <= 0 {
		return true
	}
	rows := lipgloss.Height(content)
	if width > 0 {
		rows = modalRowCount(modalInnerWidth(width), strings.Split(content, "\n"))
	}
	return rows+2*padY+modalEdgeRows <= termHeight
}

// Render returns the framed dialog as a styled string. Title
// renders bold-accent with a horizontal rule continuing to the
// right edge; body sits in the middle with a single blank line
// above and below; footer renders muted at the bottom with its
// own rule; the whole block sits inside the box edge.
func (rc RenderContext) Render() string {
	width := rc.Width
	if width < 30 {
		width = 30
	}
	inner := modalInnerWidth(width)
	titleBar := rc.Styles.ModalTitle.Render(rc.Title)
	titleRule := rc.Styles.ModalBorder.Render(strings.Repeat(GlyphRule, nonNeg(inner-lipgloss.Width(titleBar)-1)))
	titleLine := titleBar + " " + titleRule

	footerRule := rc.Styles.ModalBorder.Render(strings.Repeat(GlyphRule, inner))
	footerLine := rc.Styles.ModalFooter.Render(rc.Footer)

	content := fitModalContent(width, rc.Height, titleLine, rc.Body, footerRule, footerLine)
	return modalSurface(rc.Styles, content, width, rc.Height, 0)
}

// fitModalContent is the single place the six-part modal chrome —
// title line, blank, body, blank, footer rule, footer text — is
// joined, and the only place that knows how tall the terminal is
// while it is being joined.
//
// Why it exists (issue #142). modalBodyHeight budgets the body in
// LOGICAL rows, but a body row that is wider than the modal wraps to
// two or three terminal rows, so the budget can be met and the
// composed block still be taller than the terminal. The only thing
// downstream is clipFrame, which keeps the FIRST height rows — so
// what an overflowing modal loses is its bottom: the footer rule and
// the footer key hint, i.e. the row that says which key closes the
// modal. That is exactly backwards. The operator can live without a
// blank spacer; they cannot live without "esc cancel".
//
// So the rows are shed here instead, cheapest first:
//
//  1. the blank line above the footer rule
//  2. the blank line under the title
//  3. the footer rule itself
//  4. body rows, from the bottom
//  5. the title line
//
// The footer key hint is never shed, which is the whole point of the
// ordering: an operator who can see "esc cancel" and nothing else can
// still get out, and an operator who can see the title and nothing
// else cannot. Dropping the title last rather than first is also what
// makes the guarantee statable — the modal fits whenever the terminal
// is at least as tall as the footer hint wraps to.
//
// Below THAT there is no honest answer left: a footer hint that wraps
// to five rows on a 40-column terminal cannot be shown in four, and
// there is no truncation of it that keeps the close key (which is at
// the end of the hint, not the start). So this returns the footer
// alone and lets clipFrame trim it. It cannot return a negative
// height and it cannot panic, which is the whole bar at that size.
//
// A non-positive termHeight means the geometry is unknown; the fit
// pass is skipped and the join is exactly what it always was.
//
// termHeight is the TERMINAL's height, not the content's: the two
// rows of box edge modalSurface adds afterwards are subtracted here
// (issue #199), so a caller still passes Model.height and does not
// have to know the edge exists.
func fitModalContent(width, termHeight int, titleLine, body, footerRule, footerLine string) string {
	head := []string{titleLine, ""}
	tail := []string{"", footerRule, footerLine}
	var bodyLines []string
	if body != "" {
		bodyLines = strings.Split(body, "\n")
	}
	// The content column inside the box edge and the padding — what
	// modalSurface wraps to, and therefore what the row count has to
	// be measured against.
	inner := modalInnerWidth(width)
	// The rows left for content once the edge has taken its two. The
	// edge is drawn by modalSurface AFTER this function returns, so
	// shedding against the raw terminal height would compose a block
	// two rows too tall and hand clipFrame the footer key hint —
	// which is issue #142's defect with a new cause.
	budget := termHeight - modalEdgeRows
	for termHeight > 0 && modalRowCount(inner, head, bodyLines, tail) > budget {
		switch {
		case len(tail) == 3:
			tail = tail[1:]
		case len(head) == 2:
			head = head[:1]
		case len(tail) == 2:
			tail = tail[1:]
		case len(bodyLines) > 0:
			bodyLines = bodyLines[:len(bodyLines)-1]
		case len(head) == 1:
			head = nil
		default:
			return footerLine
		}
	}
	rows := make([]string, 0, len(head)+len(bodyLines)+len(tail))
	rows = append(rows, head...)
	rows = append(rows, bodyLines...)
	rows = append(rows, tail...)
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

// modalRowCount is how many terminal rows the given groups of
// content lines occupy once wrapped to width. Recomputed on each
// shedding pass rather than tracked incrementally: the body here is
// already windowed to roughly the terminal height by
// modalBodyHeight, so the loop is bounded by a screenful.
func modalRowCount(width int, groups ...[]string) int {
	n := 0
	for _, g := range groups {
		for _, line := range g {
			n += wrappedContentRows(line, width)
		}
	}
	return n
}

// wrappedContentRows is how many terminal rows one content line
// occupies once the modal frame wraps it to width. It runs the line
// through the same lipgloss width-wrap the outer Render will, rather
// than dividing display width by the column count: lipgloss breaks
// on word boundaries, so ceiling division under-counts, and
// under-counting here is what lets the footer fall off the bottom.
func wrappedContentRows(line string, width int) int {
	if width <= 0 {
		return 1
	}
	return max(1, lipgloss.Height(lipgloss.NewStyle().Width(width).Render(line)))
}

// Scrollbar renders a vertical scrollbar character column of
// `height` rows showing thumb position relative to (contentSize,
// viewportSize, offset). Returns "" when content fits in viewport
// or when height <= 0. Lifted from agentic-tui skill §9.F so
// any dialog with overflowing content can frame a consistent
// scroll indicator without writing the math twice.
func Scrollbar(s Styles, height, contentSize, viewportSize, offset int) string {
	if height <= 0 || contentSize <= viewportSize {
		return ""
	}
	thumbSize := max(1, height*viewportSize/contentSize)
	maxOffset := contentSize - viewportSize
	// Positions the thumb can occupy: rows 0 .. height-thumbSize, so
	// at maxOffset the thumb spans height-thumbSize .. height-1 and
	// sits flush with the bottom of the track. An extra +1 here would
	// push the last position one row past the draw loop below — which
	// clips a tall thumb and hides a thumbSize==1 thumb entirely, i.e.
	// exactly the long-content case this bar exists for.
	trackSpace := height - thumbSize
	thumbPos := 0
	if trackSpace > 0 && maxOffset > 0 {
		thumbPos = min(trackSpace, offset*trackSpace/maxOffset)
	}
	var sb strings.Builder
	thumb := s.Accent.Render("█")
	track := s.Muted.Render("│")
	for i := 0; i < height; i++ {
		if i > 0 {
			sb.WriteByte('\n')
		}
		if i >= thumbPos && i < thumbPos+thumbSize {
			sb.WriteString(thumb)
		} else {
			sb.WriteString(track)
		}
	}
	return sb.String()
}
