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

// Subagent turn drill-down plumbing (issues #70 + #71): name
// resolution, the off-loop fetch/poll Cmds, and the renderers that
// turn a page of SubagentEvents into transcript-shaped lines.
//
// Two consumers share all of it — the `/subagents <name>` detail
// overlay (dialog_subagent.go) and the inline live tail under a
// running sync-subagent tool row (update.go). Both poll rather than
// stream: a subagent's turns are deliberately kept OFF the parent's
// event stream (they'd flood every attached chat view), so the only
// live view of them is a cursored re-read of the host's log.

package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	// subagentFetchTimeout bounds one overlay page fetch. Generous:
	// the operator is looking at a spinner, and a wedged daemon
	// should surface as an error row rather than a hang.
	subagentFetchTimeout = 5 * time.Second

	// subagentTailTimeout bounds one inline-tail fetch. Tighter than
	// the overlay's: the tail re-fires every second, so a slow reply
	// is better abandoned than queued behind.
	subagentTailTimeout = 3 * time.Second

	// subagentPollInterval is the live-tail cadence for both
	// surfaces. One second reads as live without turning a
	// long-running subagent into a request generator.
	subagentPollInterval = time.Second

	// subagentTailMaxLines caps the inline preview block under a
	// running tool row. The block is a "what is it doing right now"
	// window, not the log — the log is one keystroke away in the
	// overlay, and an unbounded block would push the composer off
	// the screen.
	subagentTailMaxLines = 6

	// subagentTailMissLimit is how many consecutive empty/unresolved
	// polls a tool row gets before the TUI concludes the tool isn't
	// a subagent at all and stops tailing it.
	//
	// It can't be 1: a sync subagent that has been called but hasn't
	// written its first turn yet is indistinguishable from an
	// ordinary `read_file`, and giving up on the first miss would
	// mean the feature only ever worked for subagents that were
	// already running. Three seconds of patience covers the startup
	// window; anything past that has nothing to show anyway.
	subagentTailMissLimit = 3
)

// subagentEventsMsg carries an overlay page back to Update. Both the
// events and a fresh roster snapshot ride along, so the overlay's
// status line and report body stay current without a second Cmd.
type subagentEventsMsg struct {
	gen     uint64
	name    string
	page    SubagentEventPage
	info    SubagentInfo
	hasInfo bool
	err     error
}

// subagentPollMsg re-arms the overlay's live tail.
type subagentPollMsg struct {
	gen  uint64
	name string
}

// subagentTailMsg carries an inline-tail page back to Update, keyed
// by the tool call whose row it renders under.
type subagentTailMsg struct {
	gen    uint64
	callID string
	name   string
	page   SubagentEventPage
	err    error
}

// subagentTailTickMsg re-arms one inline tail.
type subagentTailTickMsg struct {
	gen    uint64
	callID string
}

// subagentTail is the per-tool-row state of an inline live tail: how
// far the cursor has advanced, the rendered lines to show under the
// row, and how many consecutive polls have come back with nothing.
type subagentTail struct {
	name   string
	since  int64
	lines  []string
	turns  int
	tools  int
	misses int
	// inflight guards against stacking fetches when the host is
	// slower than the poll interval.
	inflight bool
}

// subagentEventsCmd fetches one page off the event loop. The roster
// read rides along because both are host calls and the overlay wants
// them consistent with each other.
func subagentEventsCmd(reader SubagentEventReader, lister SubagentLister, gen uint64, name string, since int64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), subagentFetchTimeout)
		defer cancel()
		out := subagentEventsMsg{gen: gen, name: name}
		if lister != nil {
			if info, ok := findSubagent(lister.Subagents(), name); ok {
				out.info, out.hasInfo = info, true
			}
		}
		out.page, out.err = reader.SubagentEvents(ctx, name, since)
		return out
	}
}

// subagentPollTick schedules the overlay's next refresh.
func subagentPollTick(gen uint64, name string) tea.Cmd {
	return tea.Tick(subagentPollInterval, func(time.Time) tea.Msg {
		return subagentPollMsg{gen: gen, name: name}
	})
}

// subagentTailCmd fetches one inline-tail page off the event loop.
func subagentTailCmd(reader SubagentEventReader, gen uint64, callID, name string, since int64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), subagentTailTimeout)
		defer cancel()
		page, err := reader.SubagentEvents(ctx, name, since)
		return subagentTailMsg{gen: gen, callID: callID, name: name, page: page, err: err}
	}
}

// subagentTailTick schedules one inline tail's next poll.
func subagentTailTick(gen uint64, callID string) tea.Cmd {
	return tea.Tick(subagentPollInterval, func(time.Time) tea.Msg {
		return subagentTailTickMsg{gen: gen, callID: callID}
	})
}

// startSubagentTail begins tailing a tool call's subagent turns, so
// the operator watches a sync subagent work instead of staring at a
// spinner for two minutes. Returns nil — no tail, no cost — when the
// host can't serve turns, the row has no wire-level call ID to key
// on, or this tool name has already been proven not to be a subagent.
func (m *Model) startSubagentTail(msg toolCallMsg) tea.Cmd {
	reader, ok := m.opts.Agent.(SubagentEventReader)
	if !ok || msg.id == "" {
		return nil
	}
	name := subagentTailName(msg.name, msg.args)
	if name == "" || m.subagentNotTail[name] {
		return nil
	}
	if m.subagentTails == nil {
		m.subagentTails = make(map[string]*subagentTail)
	}
	if _, dup := m.subagentTails[msg.id]; dup {
		return nil
	}
	m.subagentTails[msg.id] = &subagentTail{name: name, inflight: true}
	return subagentTailCmd(reader, m.sessionGen, msg.id, name, 0)
}

// subagentTailName is the subagent a tool call is presumed to drive.
// A sync subagent is invoked as a tool named after itself, so the
// tool name is the default; hosts that wrap the call in a generic
// dispatch tool name the target in the args instead.
func subagentTailName(tool string, args map[string]any) string {
	for _, k := range []string{"agent", "subagent", "agent_name", "subagent_name"} {
		if s, ok := args[k].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return strings.TrimSpace(tool)
}

// applySubagentTail folds one tail page into the tool row and decides
// whether to keep polling.
func (m *Model) applySubagentTail(msg subagentTailMsg) tea.Cmd {
	t := m.subagentTails[msg.callID]
	if t == nil || t.name != msg.name {
		return nil
	}
	t.inflight = false
	switch {
	case msg.err != nil:
		if _, notFound := asSubagentNotFound(msg.err); !notFound {
			// A transport failure says nothing about whether this
			// tool is a subagent, so it doesn't feed the negative
			// cache — but it also isn't worth retrying under a
			// running tool row. Drop the tail silently; the
			// overlay is where errors belong.
			delete(m.subagentTails, msg.callID)
			return nil
		}
		t.misses++
	case len(msg.page.Events) == 0:
		t.misses++
	default:
		t.misses = 0
		t.turns += len(msg.page.Events)
		t.tools += countSubagentTools(msg.page.Events)
		if msg.page.NextSince > t.since {
			t.since = msg.page.NextSince
		}
		t.lines = append(t.lines, renderSubagentTurns(msg.page.Events, m.styles, m.viewport.Width()-8)...)
		if len(t.lines) > subagentTailMaxLines {
			t.lines = t.lines[len(t.lines)-subagentTailMaxLines:]
		}
		m.refreshSubagentTailPreview(msg.callID, t)
	}
	if t.turns == 0 && t.misses >= subagentTailMissLimit {
		// Nothing ever answered for this tool, so it isn't a
		// subagent. Remember the NAME, not the call: every later
		// `read_file` then skips the polls entirely.
		if m.subagentNotTail == nil {
			m.subagentNotTail = make(map[string]bool)
		}
		m.subagentNotTail[t.name] = true
		delete(m.subagentTails, msg.callID)
		return nil
	}
	return subagentTailTick(m.sessionGen, msg.callID)
}

// resumeSubagentTail issues the next poll for one tail.
func (m *Model) resumeSubagentTail(callID string) tea.Cmd {
	t := m.subagentTails[callID]
	if t == nil || t.inflight {
		return nil
	}
	reader, ok := m.opts.Agent.(SubagentEventReader)
	if !ok {
		return nil
	}
	t.inflight = true
	return subagentTailCmd(reader, m.sessionGen, callID, t.name, t.since)
}

// stopSubagentTail retires a tail when its tool call completes,
// collapsing the live block to a one-line summary that points at the
// overlay holding the full log. Returns false when the call wasn't
// being tailed, so applyToolResult can render normally.
func (m *Model) stopSubagentTail(callID string) (*subagentTail, bool) {
	t := m.subagentTails[callID]
	if t == nil {
		return nil, false
	}
	delete(m.subagentTails, callID)
	if t.turns == 0 {
		return nil, false
	}
	return t, true
}

// refreshSubagentTailPreview rewrites the tool row's preview with the
// current tail block. Recomputed from the call-time preview each time
// rather than appended, so repeated polls don't stack blocks.
func (m *Model) refreshSubagentTailPreview(callID string, t *subagentTail) {
	idx := m.history.FindByToolCallID(callID)
	if idx < 0 {
		return
	}
	snap := m.history.Snapshot()
	if idx >= len(snap) {
		return
	}
	preview := renderToolPreview(snap[idx].ToolName, snap[idx].ToolArgsMap, m.styles)
	if block := renderSubagentTailBlock(t, m.styles); block != "" {
		if preview != "" {
			preview += "\n"
		}
		preview += block
	}
	m.history.SetToolPreview(idx, preview)
	m.markViewportDirty()
}

// renderSubagentTailBlock is the live block under a running
// subagent's tool row: a counts line anchored with the same ⎿ glyph
// every other preview uses, then the last few turn lines.
func renderSubagentTailBlock(t *subagentTail, styles Styles) string {
	if t == nil || t.turns == 0 {
		return ""
	}
	lines := []string{styles.Muted.Render(summaryIndent) + subagentTailCounts(t, styles)}
	for _, l := range t.lines {
		lines = append(lines, detailIndent+l)
	}
	return strings.Join(lines, "\n")
}

// renderSubagentTailSummary is what the block collapses to once the
// result lands: one line of counts plus where to read the rest.
func renderSubagentTailSummary(t *subagentTail, styles Styles) string {
	if t == nil || t.turns == 0 {
		return ""
	}
	return styles.Muted.Render(summaryIndent) + subagentTailCounts(t, styles) +
		styles.Muted.Render("  "+GlyphSeparator+"  /subagents "+t.name)
}

func subagentTailCounts(t *subagentTail, styles Styles) string {
	s := fmt.Sprintf("%d turn%s", t.turns, plural(t.turns))
	if t.tools > 0 {
		s += fmt.Sprintf(", %d tool call%s", t.tools, plural(t.tools))
	}
	return styles.Muted.Render(t.name + " " + GlyphSeparator + " " + s)
}

// findSubagent looks a name up in a roster snapshot, exact first then
// case-insensitively.
func findSubagent(subs []SubagentInfo, name string) (SubagentInfo, bool) {
	for _, s := range subs {
		if s.Name == name {
			return s, true
		}
	}
	for _, s := range subs {
		if strings.EqualFold(s.Name, name) {
			return s, true
		}
	}
	return SubagentInfo{}, false
}

// resolveSubagentName maps what the operator typed onto a roster
// entry. Returns the resolved name, or — when the query is
// AMBIGUOUS — an empty name and the candidates to disambiguate
// between. A query that matches nothing returns ("", nil): the
// roster only holds live subagents, while the host's log outlives
// them, so "not in the roster" is not "doesn't exist" and the caller
// should ask the host rather than refuse.
//
// The declared-vs-instance split is the reason this isn't a map
// lookup: a subagent declared as `cluster` is spawned as `cluster-1`,
// and the roster reports the instance. The operator knows the name
// they wrote in config, so `cluster` has to find `cluster-1` —
// while `cluster-probe`, a genuinely different subagent, must not
// answer to `cluster`. Same rule the host applies to branch labels
// (go-steer/core-agent#694): only a `-<digits>` suffix is an
// instance counter.
func resolveSubagentName(query string, subs []SubagentInfo) (name string, candidates []string) {
	q := strings.TrimSpace(query)
	if q == "" {
		return "", nil
	}
	if s, ok := findSubagent(subs, q); ok {
		return s.Name, nil
	}
	var inst, prefix []string
	for _, s := range subs {
		switch {
		case strings.EqualFold(subagentDeclaredName(s.Name), q):
			inst = append(inst, s.Name)
		case strings.HasPrefix(strings.ToLower(s.Name), strings.ToLower(q)):
			prefix = append(prefix, s.Name)
		}
	}
	// Instance matches win outright: they're the same declared
	// subagent, and a prefix match that also turned up is a
	// different one that merely starts with the same letters.
	for _, tier := range [][]string{inst, prefix} {
		if len(tier) == 1 {
			return tier[0], nil
		}
		if len(tier) > 1 {
			sort.Strings(tier)
			return "", tier
		}
	}
	return "", nil
}

// subagentDeclaredName strips a `-<digits>` instance counter, so
// "cluster-1" reduces to the "cluster" the operator declared.
// "cluster-probe" is left alone — that's a name, not a counter.
func subagentDeclaredName(name string) string {
	i := strings.LastIndex(name, "-")
	if i <= 0 || i == len(name)-1 {
		return name
	}
	for _, r := range name[i+1:] {
		if !unicode.IsDigit(r) {
			return name
		}
	}
	return name[:i]
}

// openSubagentDetail backs `/subagents <name>`: resolve the argument
// against the roster, open the drill-down overlay on it, and kick off
// the first fetch plus the live tail. Returns a system-message string
// instead when there's nothing to open.
func (m *Model) openSubagentDetail(query string) (string, tea.Cmd) {
	reader, ok := m.opts.Agent.(SubagentEventReader)
	if !ok {
		return "/subagents: agent doesn't implement SubagentEventReader — no turn log to show", nil
	}
	lister, _ := m.opts.Agent.(SubagentLister)
	var subs []SubagentInfo
	if lister != nil {
		subs = lister.Subagents()
	}
	name, candidates := resolveSubagentName(query, subs)
	if len(candidates) > 0 {
		return fmt.Sprintf("/subagents: %q is ambiguous — did you mean %s?",
			query, strings.Join(candidates, ", ")), nil
	}
	if name == "" {
		// Nothing in the live roster. Ask the host anyway: its log
		// outlives the roster, and if the name really is unknown it
		// answers with the names that would have worked.
		name = strings.TrimSpace(query)
	}
	// Singleton, like every other overlay — re-issuing the command
	// retargets it rather than stacking a second copy.
	m.overlayStack.Close(subagentDialogID)
	m.overlayStack.Open(newSubagentDialog(name))
	m.refreshViewport()
	return "", tea.Batch(
		subagentEventsCmd(reader, lister, m.sessionGen, name, 0),
		subagentPollTick(m.sessionGen, name),
	)
}

// mergeSubagentEvents appends the new page to have, dropping anything
// already present by Seq. Hosts with no seq cursor re-send from the
// start on every poll, so de-duplication is what keeps that case from
// growing the log quadratically.
func mergeSubagentEvents(have, add []SubagentEvent) []SubagentEvent {
	if len(add) == 0 {
		return have
	}
	seen := make(map[int64]bool, len(have))
	for _, e := range have {
		if e.Seq != 0 {
			seen[e.Seq] = true
		}
	}
	out := have
	for _, e := range add {
		if e.Seq != 0 && seen[e.Seq] {
			continue
		}
		if e.Seq != 0 {
			seen[e.Seq] = true
		}
		out = append(out, e)
	}
	return out
}

// countSubagentTools totals the tool calls across a run of turns —
// the "6 tools" half of the collapsed one-line summary.
func countSubagentTools(evs []SubagentEvent) int {
	n := 0
	for _, e := range evs {
		n += len(e.ToolCalls)
	}
	return n
}

// renderSubagentTurns renders turns as transcript-shaped lines: a
// muted timestamp + author gutter, the turn's prose, and one line per
// tool call and result. Returns lines rather than a block so callers
// can window it (the inline tail keeps the last few; the overlay
// scrolls all of them).
func renderSubagentTurns(evs []SubagentEvent, styles Styles, width int) []string {
	var out []string
	for _, e := range evs {
		out = append(out, renderSubagentTurn(e, styles, width)...)
	}
	return out
}

// renderSubagentTurn renders one turn.
//
// Every truncation happens on plain text *before* styling: measuring
// a string that already carries SGR escapes counts the escape bytes
// as content and elides the visible text early (or not at all).
func renderSubagentTurn(e SubagentEvent, styles Styles, width int) []string {
	gutter := subagentGutter(e, styles)
	w := subagentTextWidth(width)
	var out []string
	if text := collapseWhitespace(e.Text); text != "" {
		out = append(out, gutter+truncateDisplay(text, w))
	}
	for _, c := range e.ToolCalls {
		name := truncateDisplay(c.Name, w-2)
		line := styles.Accent.Render(GlyphTool+" ") + name
		// -5 covers the glyph, the two spaces and the separator.
		if hint := collapseWhitespace(toolArgHint(c.Name, c.Args)); hint != "" {
			if hint = truncateDisplay(hint, w-runeLen(name)-5); hint != "" {
				line += " " + styles.Muted.Render(GlyphSeparator+" "+hint)
			}
		}
		out = append(out, gutter+line)
	}
	for _, r := range e.ToolResults {
		text, style := subagentResultSummary(r, styles)
		out = append(out, gutter+styles.Muted.Render("  ↳ ")+
			style.Render(truncateDisplay(text, w-4)))
	}
	if len(out) == 0 {
		// A turn with neither prose nor tool traffic still happened
		// — usually a state-only event. Show it rather than leaving
		// a gap in the seq numbering the operator can't explain.
		out = append(out, gutter+styles.Muted.Render("(no content)"))
	}
	return out
}

// subagentGutter is the muted `12:04:07 model ` prefix. Both fields
// are optional; an event with neither gets no gutter at all.
func subagentGutter(e SubagentEvent, styles Styles) string {
	var parts []string
	if !e.Timestamp.IsZero() {
		parts = append(parts, e.Timestamp.Format("15:04:05"))
	}
	if e.Author != "" {
		parts = append(parts, truncate(e.Author, 12))
	}
	if len(parts) == 0 {
		return ""
	}
	return styles.Muted.Render(strings.Join(parts, " ")) + " "
}

// subagentResultSummary compresses a tool result to one line: the
// error if it failed, else the most content-shaped field the host
// sent, else a bare "ok". Returns the text and the style to render
// it in separately so the caller can truncate before styling.
func subagentResultSummary(r SubagentToolResult, styles Styles) (string, lipgloss.Style) {
	if err := collapseWhitespace(r.Error); err != "" {
		return GlyphToolFail + " " + err, styles.ErrorText
	}
	for _, key := range []string{"output", "result", "content", "text", "summary", "stdout"} {
		if v, ok := r.Response[key]; ok {
			if s := collapseWhitespace(fmt.Sprint(v)); s != "" {
				return s, styles.ToolBody
			}
		}
	}
	if len(r.Response) == 0 {
		return "ok", styles.Muted
	}
	// Unknown shape: name the keys rather than dumping JSON into a
	// single line that would just get elided anyway.
	keys := make([]string, 0, len(r.Response))
	for k := range r.Response {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", "), styles.Muted
}

// subagentTextWidth is how much room a turn line's content gets after
// the gutter. Clamped so a narrow terminal still renders something.
func subagentTextWidth(width int) int {
	w := width - 22
	if w < 20 {
		w = 20
	}
	return w
}

// collapseWhitespace flattens a multi-line string to one line. Turn
// lines are single-line by construction; a report body that contains
// newlines would otherwise break the caller's line accounting.
func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// truncateDisplay is truncate() in runes rather than bytes, so a
// multibyte turn line doesn't get cut mid-character or elided far
// earlier than its visible width warrants. Callers pass plain text:
// a budget that leaves no room returns "" rather than overflowing.
func truncateDisplay(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return GlyphTruncate
	}
	return strings.TrimRight(string(r[:n-1]), " ") + GlyphTruncate
}

func runeLen(s string) int { return len([]rune(s)) }
