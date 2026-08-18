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

// The session picker, as a question (issue #164, stage 3), for #53's
// /switch built-in.
//
// Second of the async pickers. The model picker established the shape —
// Enter starts a host call, the modal stays up showing progress, and the
// answer arrives from Update when the reply lands (Overlay.resolve,
// design §6.5). This one is where that shape met a list whose rows do
// not all mean the same thing, and the interesting part of the migration
// is that Enter here has FOUR outcomes rather than one:
//
//   - an ordinary row commits a SwitchToSession and does not answer;
//   - the currently-attached row answers immediately, because picking
//     the session you are already in is a choice with nothing to do;
//   - an action row (issue #56) answers nothing and opens a text input
//     STACKED ON TOP of the still-open picker, so esc there returns the
//     operator to the list rather than dumping them back to chat;
//   - and every other navigation stroke is consumed and changes nothing.
//
// The third is the one that needed the design's "a resolver must not
// Open a dialog" rule to be written down first. The picker cannot open
// the input itself either — Key runs inside Overlay.HandleKeyMsg, which
// pops after it — so it names the row in sessionInputRequestedMsg and
// Update does the Open, the same way the theme picker's live preview is
// applied.
//
// What moved off the widget: the SessionSwitcher type assertion, the
// history.Append for a host that enumerated nothing, and building the
// SwitchToSession Cmd out of the live sessionGen. What deliberately did
// NOT move is applySessionSwitch, which attaches the new agent — it is
// shared with `/switch <id>` and with the action row's own reply, and
// duplicating it here would let the three ways into a new session drift.

package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const sessionPickerDialogID = "session-picker"

// sessionPickerWidth is the picker's preferred total width. Wider than
// the model picker's because a session's detail line carries an ID, a
// "(current)" tag and whatever Description the host advertised, and
// they are the evidence the operator is choosing on.
const sessionPickerWidth = 72

// sessionCellLines is how many terminal rows one session occupies in
// the list: a title line and a detail line, always both (issue #163).
//
// Constant, and it has to stay constant. The window arithmetic below
// converts an item index to a line index by multiplying by this, and
// that only works while every cell is the same height. Letting a cell
// grow — a second detail line when the host sets a long Description,
// say, or dropping the detail line when there is nothing to put on it
// — turns `q.idx * sessionCellLines` into a prefix sum over the
// filtered rows that has to be rebuilt on every filter keystroke, and
// drags clampIndex / stepIndex / the wheel handler along with it. The
// cost of holding the constant is a blank detail line on a session
// whose host advertises nothing but an ID; that is a much better
// trade than a variable-height list, and it goes away on its own as
// hosts start populating Display.
const sessionCellLines = 2

// sessionSwitchRequestedMsg is Enter on an ordinary row, on its way to
// a host call — the session picker's twin of modelSwitchRequestedMsg,
// and it exists for the same reason: SwitchToSession needs the live
// SessionSwitcher and the live sessionGen, and both can move while the
// picker is open. A /model switch inside one session replaces
// m.opts.Agent outright, so a switcher captured when the picker opened
// could be the outgoing agent by the time Enter lands.
type sessionSwitchRequestedMsg struct{ ID string }

// sessionInputRequestedMsg is Enter on an action row (issue #56): open
// this row's text input on top of the picker.
//
// A message rather than a direct Open because neither of the two places
// that could do it is allowed to. Key runs inside
// Overlay.HandleKeyMsg, which pops the front dialog after it returns,
// and a resolver runs inside Overlay.resolve, which pops after IT
// returns — so in both cases the modal just pushed is the one that gets
// popped. Update is the only frame with no pending pop.
//
// Carries the whole row rather than its ID: the input dialog's title,
// prompt, placeholder and validator all come off SessionInput, and
// re-finding the row from the snapshot would mean the picker had to
// still be open, which is precisely what the operator's next esc undoes.
type sessionInputRequestedMsg struct{ Row SessionInfo }

// sessionPickerQuestion renders SessionSwitcher.Sessions() as a list of
// two-line cells — title over ID + metadata (issue #163) — with a
// cursor + "(current)" marker on the attached row, and answers with the
// session the operator attached to.
//
// The list is a SNAPSHOT (issue #114): it is pulled once, off the
// Update goroutine, when the picker opens, and installed by
// applySessions. Neither Key nor Body ever calls the host. Sessions()
// is a remote enumeration on the hosts that motivate this capability at
// all, so re-pulling it per keystroke and again per paint was the worst
// version of the pattern.
type sessionPickerQuestion struct {
	// wired is whether the agent implemented SessionSwitcher when the
	// picker opened. A constructor argument rather than a live type
	// assertion, because the assertion needs m.opts.Agent and needing
	// that from Body is what put a host call inside View() before #114.
	//
	// A stale true costs a no-op: Update re-resolves the switcher when
	// the request lands.
	wired bool

	// loaded flips when applySessions installs the snapshot. Until then
	// the body renders a loading line, so the picker opens instantly no
	// matter how slow the enumeration is.
	loaded bool

	// sessions is the snapshot: the full list as the host returned it,
	// never re-pulled while the picker is open.
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
	// "". While it is set the question is committed but not yet
	// answered: every keystroke except esc is swallowed, so the
	// operator cannot stack a second attach against a list that is
	// about to be replaced.
	switching string

	// fail is why the last SwitchToSession came back unusable, shown
	// under the list (issue #245). Set by the Update-side reply handler,
	// which is the only place that knows; the transcript row it pairs
	// with is written by applySessionSwitch and says the same thing.
	fail pickerFailure
}

// newSessionPickerQuestion constructs the picker with no snapshot yet.
// Prefer Model.openSessionPicker, which pairs the ask with the Cmd that
// fills it.
func newSessionPickerQuestion(wired bool) *sessionPickerQuestion {
	return &sessionPickerQuestion{wired: wired, filter: newPickerFilter()}
}

// openSessionPicker asks the session picker (singleton) and returns the
// Cmd that pulls Sessions() off the Update goroutine. Nil Cmd when the
// picker is already open or the capability is unwired — an unwired
// picker still opens, because "this agent cannot switch sessions" is
// worth saying on the surface the operator asked for.
func (m *Model) openSessionPicker() tea.Cmd {
	if m.overlayStack.HasID(sessionPickerDialogID) {
		return nil
	}
	_, wired := m.opts.Agent.(SessionSwitcher)
	q := newSessionPickerQuestion(wired)
	m.overlayStack.ask(q, askOperator, sessionPickerResolver(q))
	return m.sessionsCmd()
}

// sessionPickerOn returns the picker currently asked on o, or nil when
// none is open — the reply-routing check every async arm in Update
// starts with, since a host reply can land against a picker the
// operator escaped out of or replaced. The model picker's
// modelPickerOn is the same function over the other type, and for the
// same reason it is a free function rather than a method on Overlay.
func sessionPickerOn(o *Overlay) *sessionPickerQuestion {
	aq := o.asked(sessionPickerDialogID)
	if aq == nil {
		return nil
	}
	q, _ := aq.q.(*sessionPickerQuestion)
	return q
}

// sessionPickerResolver is the other half of the picker.
//
// Like the model picker's it closes over its question, and for the same
// single purpose: telling the two unrenderable states apart. "This
// agent has no SessionSwitcher" and "the host enumerated zero sessions"
// are both questions that could not be put to the operator, and only
// the second earns a transcript row — the first is a capability the
// host never claimed, and saying so on every stray keystroke would be
// noise.
//
// The chosen arm is empty, and that is the honest shape rather than an
// omission. A session pick is not itself the change: SwitchToSession is
// a host call whose reply carries the new agent, `/switch <id>` reaches
// that same reply with no picker in the loop, and the action row
// reaches it through SessionInput.Submit — so the attach lives in
// applySessionSwitch, which all three share. What the answer buys is
// the pop: the picker closes BECAUSE the attach landed, through the
// exactly-once latch, rather than being closed by whichever handler
// noticed first.
func sessionPickerResolver(q *sessionPickerQuestion) resolver {
	return func(a answer, m *Model) tea.Cmd {
		switch a := a.(type) {
		case chosen:
			return nil
		case dismissed:
			if a.Reason == dismissUnrenderable && q.wired && q.loaded && len(q.sessions) == 0 {
				m.history.Append(Message{Role: RoleSystem, Text: "/switch: no sessions available"})
				m.refreshViewport()
			}
			return nil
		case declined, selected, text, fields, decision:
			// Shapes a single-select list cannot produce. Named rather
			// than defaulted so that a variant added to the sealed set
			// is a lint failure here instead of silently routing to "do
			// nothing".
			return nil
		}
		return nil
	}
}

func (q *sessionPickerQuestion) ID() string { return sessionPickerDialogID }

func (q *sessionPickerQuestion) Title() string { return "Choose a Session" }

func (q *sessionPickerQuestion) Footer() string {
	return keyLegend("type to filter", "↑↓ choose", "enter attach", "esc cancel")
}

func (q *sessionPickerQuestion) Width(avail int) int {
	width := sessionPickerWidth
	if avail > 0 && width > avail-4 {
		width = avail - 4
	}
	return max(width, 30)
}

// rows returns the list the cursor indexes into and Body paints — the
// snapshot narrowed by the filter row, through the same seam and the
// same ranker as modelPickerQuestion.rows.
func (q *sessionPickerQuestion) rows() []SessionInfo {
	filter := q.filter.value()
	if filter == "" {
		return q.sessions
	}
	keys := make([]string, len(q.sessions))
	for i, s := range q.sessions {
		keys[i] = pickerKey(s.Display, s.ID)
	}
	idx := rankNames(keys, filter)
	out := make([]SessionInfo, len(idx))
	for i, at := range idx {
		out[i] = q.sessions[at]
	}
	return out
}

// applySessions installs the open-time snapshot, from Update's
// sessionsLoadedMsg handler.
//
// No cursor seeding, unlike the model picker's applyModels. #110 wanted
// the model cursor on the CURRENT model because a model list is short
// and the operator is usually stepping one row off where they are; a
// session list is a history, the attached session is often the one row
// they are least likely to want, and it is already marked "(current)".
func (q *sessionPickerQuestion) applySessions(sessions []SessionInfo) {
	q.sessions = sessions
	q.loaded = true
	q.idx = 0
	q.off = 0
}

// Key drives the list on navigation strokes and feeds everything else
// to the filter.
func (q *sessionPickerQuestion) Key(msg tea.KeyPressMsg) (answer, tea.Cmd) {
	stroke := msg.String()
	if stroke == "esc" {
		// Esc closes even mid-attach: the host call is already
		// committed, so its reply still applies the target — the
		// operator is just declining to watch. The reply then finds no
		// question to resolve and applySessionSwitch does the
		// announcing on its own.
		return dismissed{Reason: dismissEscape}, nil
	}
	if q.switching != "" {
		// A SwitchToSession is in flight; swallow keys rather than let
		// the operator stack a second attach.
		return nil, nil
	}
	if !q.wired {
		// Nothing to ask: the agent cannot switch sessions at all.
		return dismissed{Reason: dismissUnrenderable}, nil
	}
	if !q.loaded {
		// The enumeration has not landed. Nothing to move, and the
		// keystroke is consumed anyway so it cannot leak to the
		// textarea behind the modal.
		return nil, nil
	}
	if len(q.sessions) == 0 {
		// The HOST enumerated nothing — say so and close, on any key.
		// Distinct from the filter matching nothing, below.
		return dismissed{Reason: dismissUnrenderable}, nil
	}
	if !pickerNavStroke(stroke) {
		cmd, changed := q.filter.handleKeyMsg(msg)
		if changed {
			// The list under the cursor was just replaced. Land on the
			// best match rather than trying to keep hold of a row that
			// may not be in the new list at all.
			q.idx, q.off = 0, 0
			// The failure named a session that may not even be in the
			// narrowed list — drop it rather than leave it pointing at
			// nothing on screen.
			q.fail.clear()
		}
		return nil, cmd
	}

	sessions := q.rows()
	if len(sessions) == 0 {
		// The FILTER matched nothing. Stay open with the row on screen:
		// the operator's next keystroke is a backspace.
		return nil, nil
	}
	q.idx = clampIndex(q.idx, len(sessions))
	// Every stroke from here moves the cursor or commits a new attach,
	// so whatever the last one failed at is no longer what the operator
	// is looking at.
	q.fail.clear()
	switch stroke {
	case "up", "ctrl+p":
		q.idx = stepIndex(q.idx, -1, len(sessions))
		return nil, nil
	case "down", "ctrl+n":
		q.idx = stepIndex(q.idx, 1, len(sessions))
		return nil, nil
	case "enter":
		pick := sessions[q.idx]
		if pick.Input != nil {
			// An action row (issue #56) is a question this question
			// cannot answer — "attach to endpoint…" needs an address
			// typed. Not an answer, therefore, and not a dismissal
			// either: the picker stays open UNDERNEATH the input so esc
			// there comes back to the list. Update does the Open; see
			// sessionInputRequestedMsg for why it has to.
			return nil, requestSessionInputCmd(pick)
		}
		if pick.Current {
			// Picking the row you are already attached to is a real
			// choice with nothing to carry out — there is no session to
			// detach from and no host call to make. Answered here and
			// now, which is also what keeps it from wiping the
			// transcript the way a genuine attach does.
			return chosen{ID: pick.ID, Index: q.idx}, nil
		}
		// Committed, not answered. The answer is the host's reply.
		q.switching = pick.ID
		return nil, requestSessionSwitchCmd(pick.ID)
	}
	// A navigation stroke this picker has no use for. Consumed, as
	// every keystroke into an open modal is, but nothing changes.
	return nil, nil
}

func requestSessionSwitchCmd(id string) tea.Cmd {
	return func() tea.Msg { return sessionSwitchRequestedMsg{ID: id} }
}

func requestSessionInputCmd(row SessionInfo) tea.Cmd {
	return func() tea.Msg { return sessionInputRequestedMsg{Row: row} }
}

// applySessionSwitch is the Update-side half of the Enter path, shared
// with `/switch <id>` and with the action row's SessionInput.Submit
// reply. Returns the listener-batch Cmd applySwitchTarget produces, or
// nil on failure. Called only after the sessionGen guard has passed.
func (m *Model) applySessionSwitch(msg sessionSwitchedMsg) tea.Cmd {
	if reason := sessionSwitchFailure(msg); reason != "" {
		m.history.Append(Message{Role: RoleError, Text: "/switch: " + reason})
		m.refreshViewport()
		return nil
	}
	tgt := msg.target
	return m.applySwitchTarget(&tgt)
}

// sessionSwitchFailure is why a SwitchToSession reply is unusable, or ""
// when it is fine — the session picker's twin of modelSwitchFailure,
// and it exists for the same reason: the transcript row and the
// picker's inline row (issue #245) are two readings of one failure and
// must not drift.
func sessionSwitchFailure(msg sessionSwitchedMsg) string {
	switch {
	case msg.err != nil:
		return msg.err.Error()
	case msg.target.Agent == nil:
		return "SessionSwitcher returned nil Agent"
	}
	return ""
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

func (q *sessionPickerQuestion) Body(width, termHeight int, st Styles) string {
	switch {
	case !q.wired:
		return st.Muted.Render("agent does not implement SessionSwitcher")
	case q.switching != "":
		return st.Muted.Render("attaching to " + q.switching + "…")
	case !q.loaded:
		return st.Muted.Render("loading sessions…")
	case len(q.sessions) == 0:
		return st.Muted.Render("(no sessions advertised by the agent)")
	}

	sessions := q.rows()
	filter := q.filter.value()
	// The filter row is the FIRST body row, always — including when it
	// matched nothing, since it is the thing the operator has to edit
	// to get back. Its row is paid for with modalChromeRows+1 below, and
	// the failure row, which is always the LAST, adds its own.
	lines := []string{q.filter.render(width, len(sessions), len(q.sessions), st)}
	if len(sessions) == 0 {
		lines = append(lines, st.Muted.Render("no sessions match "+quoteFilter(filter)))
		return q.fail.appendTo(lines, width, st)
	}
	// Clamp the cursor into range in case Sessions() shrank between
	// opens, or the filter shrank the list under it.
	q.idx = clampIndex(q.idx, len(sessions))
	// One APPENDED ROW PER LINE, never one row holding a "\n".
	// scrollView hands every row it windows to fitRow, which
	// measures and pads it to exactly contentWidth (issue #157);
	// an embedded newline would be measured as one row, padded as
	// one row, and then painted as two — putting the scrollbar
	// cell on the wrong line and overflowing the height budget
	// the modal was composed against.
	rows := make([]string, 0, len(sessions)*sessionCellLines)
	for i, s := range sessions {
		title, detail := sessionCell(s, i == q.idx, filter, st)
		rows = append(rows, title, detail)
	}
	view := modalBodyHeight(termHeight, modalChromeRows+1+q.fail.rows())
	// Line units from here down: q.idx stays item-indexed (so Key,
	// clampIndex, stepIndex, the filter and the ranker are all
	// untouched) and only the window arithmetic is scaled.
	//
	// listWindow keeps ONE cursor line visible, and a cell is two, so
	// a single call can only ever hold half of the selected cell: pass
	// the title line and the detail line clips off the bottom edge
	// while scrolling down, pass the detail line and the title clips
	// off the top edge while scrolling back up. Both have to be held.
	//
	// Two calls, not one call plus a clamp. A clamp would have to
	// restate listWindow's own edge cases — the total<=view
	// short-circuit and the min() against total-view — to stay
	// consistent with it, and would then have to decide what to do
	// when view is 1 and the cell simply does not fit. Calling twice
	// inherits all of that. Order is load-bearing and is the reason it
	// works: the detail line goes first, then the title line, so the
	// title wins the tie on a one-row window (a very short terminal),
	// which is the half of the cell worth keeping. For any view >= 2
	// the second call can only move the offset UP to reveal the title,
	// which cannot push the detail line — one line below it — back
	// out.
	first := q.idx * sessionCellLines
	q.off = listWindow(q.off, first+sessionCellLines-1, len(rows), view)
	q.off = listWindow(q.off, first, len(rows), view)
	// The offset is deliberately NOT snapped to a cell boundary.
	// Snapping would keep unselected cells whole at the window edges,
	// but rounding an odd offset down can push the selected cell's
	// last line back off the bottom, which is the invariant the two
	// calls above exist to establish; rounding up breaks the top edge
	// symmetrically. A half cell peeking in at an edge is the cheaper
	// cosmetic defect.
	lines = append(lines, scrollView(st, rows, modalBodyWidth(width), view, q.off)...)
	return q.fail.appendTo(lines, width, st)
}

// filtering reports whether the filter row is on screen — the same
// condition as the picker owning the caret. Loading, an in-flight
// attach, an unwired agent and an empty enumeration all render
// something with nothing to narrow.
func (q *sessionPickerQuestion) filtering() bool {
	return q.wired && q.loaded && q.switching == "" && len(q.sessions) > 0
}

// Cursor implements cursorQuestion so the filter row gets a real
// terminal caret rather than modalCursor's (nil, true) — see the model
// picker's for why that matters (#105 / #123).
func (q *sessionPickerQuestion) Cursor(_ int) *tea.Cursor {
	if !q.filtering() {
		return nil
	}
	return filterRowCursor(q.filter.cursor())
}

// The action-row text input the picker stacks on top of itself
// (sessionInputDialogID / newSessionInputDialog) lives in
// dialog_sessioninput.go — it grew an in-flight state and a reply
// handler of its own when SessionInput.Submit moved off the Update
// goroutine (issue #194), and it is still a Dialog rather than a
// question: it is the one modal that has to END somebody else's
// question, which it does through Overlay.resolve.
