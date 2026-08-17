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

// Session picker dialog — Dialog implementation for issue #53's
// /switch built-in. Mirrors dialog_modelpicker.go's shape: cursor
// list of enumerated sessions via SessionSwitcher, Enter commits
// through applySwitchTarget, Esc closes without swap.

package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const sessionPickerDialogID = "session-picker"

// sessionCellLines is how many terminal rows one session occupies in
// the list: a title line and a detail line, always both (issue #163).
//
// Constant, and it has to stay constant. The window arithmetic below
// converts an item index to a line index by multiplying by this, and
// that only works while every cell is the same height. Letting a cell
// grow — a second detail line when the host sets a long Description,
// say, or dropping the detail line when there is nothing to put on it
// — turns `d.idx * sessionCellLines` into a prefix sum over the
// filtered rows that has to be rebuilt on every filter keystroke, and
// drags clampIndex / stepIndex / the wheel handler along with it. The
// cost of holding the constant is a blank detail line on a session
// whose host advertises nothing but an ID; that is a much better
// trade than a variable-height list, and it goes away on its own as
// hosts start populating Display.
const sessionCellLines = 2

// sessionPickerDialog renders SessionSwitcher.Sessions() as a list of
// two-line cells — title over ID + metadata (issue #163) — with a
// cursor + "(current)" marker on the attached row, and dispatches
// SwitchToSession + applySwitchTarget on Enter.
//
// Mirrors dialog_modelpicker.go's snapshot shape (issue #114): the
// list is pulled once, off the Update goroutine, when the dialog
// opens; HandleKey and Render read the snapshot and never call the
// host. Sessions() is a remote enumeration on the hosts that motivate
// this capability at all, so re-pulling it per keystroke and again
// per paint was the worst version of the pattern.
type sessionPickerDialog struct {
	// loaded flips when sessionsLoadedMsg installs the snapshot. As
	// on the model picker there is no "unwired" state: the
	// SessionSwitcher type assertion is free and View()-safe; only
	// the list is a host call.
	loaded bool

	// sessions is the snapshot: the full list as the host returned
	// it, never re-pulled while the dialog is open.
	sessions []SessionInfo

	// idx is the cursor, an index into rows().
	idx int

	// off is the first visible LINE, not the first visible session:
	// cells are sessionCellLines tall (issue #163), so the window
	// arithmetic runs in line units while idx above stays
	// item-indexed. Session lists are host-supplied and unbounded — a
	// long-running endpoint can advertise dozens — so the list windows
	// around the cursor.
	off int

	// filter is the type-to-filter row (issue #117), narrowing rows().
	// A project with a hundred saved sessions is the case that
	// motivates it hardest.
	filter pickerFilter

	// switching is the session ID of an in-flight SwitchToSession, or
	// "". The dialog stays open showing progress until
	// sessionSwitchedMsg lands.
	switching string
}

// newSessionPickerDialog constructs the picker with no snapshot yet.
// Prefer Model.openSessionPicker, which pairs the Open with the Cmd
// that fills it.
func newSessionPickerDialog() *sessionPickerDialog {
	return &sessionPickerDialog{idx: 0, filter: newPickerFilter()}
}

// openSessionPicker pushes the session picker (singleton) and returns
// the Cmd that pulls Sessions() off the Update goroutine. Nil Cmd
// when the picker is already open or the capability is unwired.
func (m *Model) openSessionPicker() tea.Cmd {
	if m.overlayStack.HasID(sessionPickerDialogID) {
		return nil
	}
	m.overlayStack.Open(newSessionPickerDialog())
	return m.sessionsCmd()
}

func (d *sessionPickerDialog) ID() string { return sessionPickerDialogID }

// rows returns the list the cursor indexes into and Render paints —
// the snapshot narrowed by the filter row, through the same seam and
// the same ranker as modelPickerDialog.rows.
func (d *sessionPickerDialog) rows() []SessionInfo {
	filter := d.filter.value()
	if filter == "" {
		return d.sessions
	}
	keys := make([]string, len(d.sessions))
	for i, s := range d.sessions {
		keys[i] = pickerKey(s.Display, s.ID)
	}
	idx := rankNames(keys, filter)
	out := make([]SessionInfo, len(idx))
	for i, at := range idx {
		out[i] = d.sessions[at]
	}
	return out
}

// applySessions installs the open-time snapshot, from Update's
// sessionsLoadedMsg handler.
func (d *sessionPickerDialog) applySessions(sessions []SessionInfo) {
	d.sessions = sessions
	d.loaded = true
	d.idx = 0
	d.off = 0
}

// HandleKey satisfies Dialog for callers holding only a normalized
// stroke; the real handler is HandleKeyMsg, because the filter row
// needs Key.Text and bracketed pastes that a stroke string drops.
func (d *sessionPickerDialog) HandleKey(stroke string, m *Model) DialogAction {
	return d.HandleKeyMsg(keyMsgFromStroke(stroke), m)
}

// HandleKeyMsg is the real key handler: navigation strokes drive the
// list, everything else is filter input.
func (d *sessionPickerDialog) HandleKeyMsg(msg tea.KeyPressMsg, m *Model) DialogAction {
	stroke := msg.String()
	if stroke == "esc" {
		return DialogAction{Consumed: true, Close: true}
	}
	if d.switching != "" {
		// A SwitchToSession is in flight; swallow keys rather than
		// let the operator stack a second attach.
		return DialogAction{Consumed: true}
	}
	switcher, wired := m.opts.Agent.(SessionSwitcher)
	if !wired {
		// Agent doesn't support session switching — close cleanly.
		return DialogAction{Consumed: true, Close: true}
	}
	if !d.loaded {
		return DialogAction{Consumed: true}
	}
	if len(d.sessions) == 0 {
		// The HOST enumerated nothing — unchanged from before the
		// filter existed. Distinct from the filter matching nothing.
		m.history.Append(Message{Role: RoleSystem, Text: "/switch: no sessions available"})
		m.refreshViewport()
		return DialogAction{Consumed: true, Close: true}
	}
	if !pickerNavStroke(stroke) {
		cmd, changed := d.filter.handleKeyMsg(msg)
		if changed {
			d.idx, d.off = 0, 0
		}
		return DialogAction{Consumed: true, Cmd: cmd}
	}

	sessions := d.rows()
	if len(sessions) == 0 {
		// The FILTER matched nothing; stay open so it can be edited.
		return DialogAction{Consumed: true}
	}
	d.idx = clampIndex(d.idx, len(sessions))
	switch stroke {
	case "up", "ctrl+p":
		d.idx = stepIndex(d.idx, -1, len(sessions))
		return DialogAction{Consumed: true}
	case "down", "ctrl+n":
		d.idx = stepIndex(d.idx, 1, len(sessions))
		return DialogAction{Consumed: true}
	case "enter":
		pick := sessions[d.idx]
		// Action row (issue #56) — hand off to a text-input dialog
		// stacked on top of this one instead of switching. We stay
		// open underneath so esc on the input returns the operator
		// to the list rather than dumping them back to chat.
		if pick.Input != nil {
			m.overlayStack.Open(newSessionInputDialog(pick))
			return DialogAction{Consumed: true}
		}
		// Picking the currently-attached row is a no-op — nothing
		// to detach from. Close cleanly without wiping history.
		if pick.Current {
			return DialogAction{Consumed: true, Close: true}
		}
		// Stay open + mark in flight; sessionSwitchedMsg closes us.
		d.switching = pick.ID
		return DialogAction{Consumed: true, Cmd: switchToSessionCmd(switcher, m.sessionGen, pick.ID)}
	}
	// Unhandled key — consume so it doesn't leak to the textarea
	// behind the modal, but don't close.
	return DialogAction{Consumed: true}
}

// applySessionSwitch is the Update-side half of the Enter path.
// Returns the listener-batch Cmd applySwitchTarget produces, or nil
// on failure. Called only after the sessionGen guard has passed.
func (m *Model) applySessionSwitch(msg sessionSwitchedMsg) tea.Cmd {
	if msg.err != nil {
		m.history.Append(Message{Role: RoleError, Text: "/switch: " + msg.err.Error()})
		m.refreshViewport()
		return nil
	}
	if msg.target.Agent == nil {
		m.history.Append(Message{Role: RoleError, Text: "/switch: SessionSwitcher returned nil Agent"})
		m.refreshViewport()
		return nil
	}
	tgt := msg.target
	return m.applySwitchTarget(&tgt)
}

// sessionCell renders one session as its two lines: a title line the
// operator reads, and a muted detail line carrying the identity and
// whatever metadata the host advertised.
//
// The split is the whole point of issue #163. Everything used to be
// concatenated onto one row — "> nightly refactor  (sess-001)
// (current)  2 days ago" — which meant the title, the thing being
// chosen, competed for the row with three pieces of bookkeeping and
// lost the moment the dialog narrowed. Stacked, the first line is the
// choice and the second is the evidence, and truncation on a narrow
// terminal now eats the evidence instead of the choice.
//
// The title cannot be inferred here: core-tui sees an ID and whatever
// the host chose to put in Display, and has no transcript to
// summarise. Populating Display is host work (issue #163's
// "host-side" section); this function's job is to make the empty case
// survivable rather than to fill it.
func sessionCell(s SessionInfo, selected bool, filter string, styles Styles) (title, detail string) {
	base := lipgloss.NewStyle()
	marker, gutter := "  ", "  "
	if selected {
		base = styles.Accent
		marker = "> "
		// The detail line gets the transcript's dotted selection bar
		// (issue #152's glyphSelectBar) rather than blank columns.
		// Without it the cursor marks a LINE and the list is made of
		// CELLS, so "> " on one line and nothing on the next reads as
		// ambiguous the moment two cells are adjacent — is the second
		// line the tail of the selection or the head of the row
		// below? Drawing the bar down the gutter of the selected item
		// is exactly the idiom the transcript already established for
		// a multi-line selection, so reuse it rather than invent a
		// second vocabulary for the same question.
		gutter = styles.Accent.Render(glyphSelectBar) + " "
	}

	// An empty Display must never leave line one blank — that is the
	// state EVERY host is in until it starts setting the field, so the
	// fallback is the common path and not the edge case. Fall back to
	// the ID: opaque, but it is what the picker showed before this
	// change and it is at least selectable.
	label := s.Display
	if label == "" {
		label = s.ID
	}
	title = base.Render(marker) + highlightSpan(label, filter, base)
	// Action rows (issue #56) carry no session identity — showing
	// "(id)" or "(current)" next to "+ Attach to endpoint…" would read
	// as a session that exists. Their affordance is the ▸ chevron, and
	// it belongs on the title line next to the label it qualifies.
	if s.Input != nil {
		title += "  " + styles.Muted.Render(glyphCollapsed)
	}

	var meta []string
	if s.Input == nil {
		// Only when it differs from the label: with Display unset the
		// ID is already the title, and repeating it underneath would
		// spend the second line saying nothing.
		if s.ID != "" && s.ID != label {
			meta = append(meta, highlightSpan(s.ID, filter, styles.Muted))
		}
		if s.Current {
			meta = append(meta, styles.Muted.Render("(current)"))
		}
	}
	if s.Description != "" {
		meta = append(meta, styles.Muted.Render(s.Description))
	}
	// No parenthesis around the ID any more: it was there to fence the
	// ID off from the title it shared a row with, and on a line of its
	// own it has nothing to be fenced from.
	switch {
	case len(meta) > 0:
		detail = gutter + strings.Join(meta, "  ")
	case selected:
		// Nothing to say, but the selection bar still has to reach the
		// bottom of the cell or the cell stops looking two lines tall.
		detail = gutter
	default:
		// The pad that holds sessionCellLines constant.
		detail = ""
	}
	return title, detail
}

func (d *sessionPickerDialog) Render(totalWidth int, m *Model) string {
	width := 72
	if totalWidth > 0 && width > totalWidth-4 {
		width = totalWidth - 4
	}
	if width < 30 {
		width = 30
	}

	// Snapshot-only render (issue #114) — no host call from View().
	_, wired := m.opts.Agent.(SessionSwitcher)
	body := ""
	switch {
	case !wired:
		body = m.styles.Muted.Render("agent does not implement SessionSwitcher")
	case d.switching != "":
		body = m.styles.Muted.Render("attaching to " + d.switching + "…")
	case !d.loaded:
		body = m.styles.Muted.Render("loading sessions…")
	case len(d.sessions) == 0:
		body = m.styles.Muted.Render("(no sessions advertised by the agent)")
	default:
		sessions := d.rows()
		filter := d.filter.value()
		// The filter row is the first body row and costs the +1 on
		// modalChromeRows below.
		lines := []string{d.filter.render(width, len(sessions), len(d.sessions), m.styles)}
		if len(sessions) == 0 {
			lines = append(lines, m.styles.Muted.Render("no sessions match "+quoteFilter(filter)))
			body = strings.Join(lines, "\n")
			break
		}
		// Clamp cursor into range in case Sessions() shrank
		// between opens, or the filter shrank the list under it.
		d.idx = clampIndex(d.idx, len(sessions))
		// One APPENDED ROW PER LINE, never one row holding a "\n".
		// scrollView hands every row it windows to fitRow, which
		// measures and pads it to exactly contentWidth (issue #157);
		// an embedded newline would be measured as one row, padded as
		// one row, and then painted as two — putting the scrollbar
		// cell on the wrong line and overflowing the height budget
		// the modal was composed against.
		rows := make([]string, 0, len(sessions)*sessionCellLines)
		for i, s := range sessions {
			title, detail := sessionCell(s, i == d.idx, filter, m.styles)
			rows = append(rows, title, detail)
		}
		view := modalBodyHeight(m.height, modalChromeRows+1)
		// Line units from here down: d.idx stays item-indexed (so
		// HandleKeyMsg, clampIndex, stepIndex, the filter and the
		// ranker are all untouched) and only the window arithmetic
		// is scaled.
		//
		// listWindow keeps ONE cursor line visible, and a cell is
		// two, so a single call can only ever hold half of the
		// selected cell: pass the title line and the detail line
		// clips off the bottom edge while scrolling down, pass the
		// detail line and the title clips off the top edge while
		// scrolling back up. Both have to be held.
		//
		// Two calls, not one call plus a clamp. A clamp would have to
		// restate listWindow's own edge cases — the total<=view
		// short-circuit and the min() against total-view — to stay
		// consistent with it, and would then have to decide what to
		// do when view is 1 and the cell simply does not fit. Calling
		// twice inherits all of that. Order is load-bearing and is
		// the reason it works: the detail line goes first, then the
		// title line, so the title wins the tie on a one-row window
		// (a very short terminal), which is the half of the cell
		// worth keeping. For any view >= 2 the second call can only
		// move the offset UP to reveal the title, which cannot push
		// the detail line — one line below it — back out.
		first := d.idx * sessionCellLines
		d.off = listWindow(d.off, first+sessionCellLines-1, len(rows), view)
		d.off = listWindow(d.off, first, len(rows), view)
		// The offset is deliberately NOT snapped to a cell boundary.
		// Snapping would keep unselected cells whole at the window
		// edges, but rounding an odd offset down can push the
		// selected cell's last line back off the bottom, which is
		// the invariant the two calls above exist to establish;
		// rounding up breaks the top edge symmetrically. A half cell
		// peeking in at an edge is the cheaper cosmetic defect.
		lines = append(lines, scrollView(m.styles, rows, modalBodyWidth(width), view, d.off)...)
		body = strings.Join(lines, "\n")
	}
	footer := "type to filter " + GlyphSeparator + " ↑↓ choose " +
		GlyphSeparator + " enter attach " + GlyphSeparator + " esc cancel"
	return RenderContext{
		Title:  "Choose a Session",
		Body:   body,
		Footer: footer,
		Width:  width,
		Height: m.height,
		Styles: m.styles,
	}.Render()
}

// filtering reports whether the filter row is on screen — the same
// condition as the picker owning the caret. Loading, an in-flight
// attach, an unwired agent and an empty enumeration all render
// something with nothing to narrow.
func (d *sessionPickerDialog) filtering(m *Model) bool {
	if _, wired := m.opts.Agent.(SessionSwitcher); !wired {
		return false
	}
	return d.loaded && d.switching == "" && len(d.sessions) > 0
}

// DialogCursor implements cursorDialog so the filter row gets a real
// terminal caret rather than modalCursor's (nil, true) — see the
// model picker's for why that matters (#105 / #123).
func (d *sessionPickerDialog) DialogCursor(_ int, m *Model) *tea.Cursor {
	if !d.filtering(m) {
		return nil
	}
	return filterRowCursor(d.filter.cursor())
}

// The action-row text input the picker stacks on top of itself
// (sessionInputDialogID / newSessionInputDialog) lives in
// dialog_sessioninput.go — it grew an in-flight state and a reply
// handler of its own when SessionInput.Submit moved off the Update
// goroutine (issue #194).
