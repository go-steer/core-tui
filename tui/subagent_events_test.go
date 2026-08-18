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
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// subagentAgent is a stub host that implements both subagent
// capabilities. pages is consulted per (name, since) call; missing
// names produce the *SubagentNotFoundError a conforming host owes.
type subagentAgent struct {
	mu     sync.Mutex
	roster []SubagentInfo
	// turns is the whole log per subagent; SubagentEvents serves the
	// slice after the since cursor, exactly like a real paged host.
	turns map[string][]SubagentEvent
	err   error
}

func (*subagentAgent) Run(_ context.Context, _ string) iter.Seq2[Event, error] {
	return func(_ func(Event, error) bool) {}
}

func (a *subagentAgent) Subagents() []SubagentInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.roster
}

func (a *subagentAgent) SubagentEvents(_ context.Context, name string, since int64) (SubagentEventPage, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return SubagentEventPage{}, a.err
	}
	all, ok := a.turns[name]
	if !ok {
		avail := make([]string, 0, len(a.turns))
		for k := range a.turns {
			avail = append(avail, k)
		}
		return SubagentEventPage{}, &SubagentNotFoundError{Name: name, Available: avail}
	}
	var page SubagentEventPage
	for _, e := range all {
		if e.Seq > since {
			page.Events = append(page.Events, e)
			page.NextSince = e.Seq
		}
	}
	if page.NextSince == 0 {
		page.NextSince = since
	}
	return page, nil
}

func turn(seq int64, author, text string) SubagentEvent {
	return SubagentEvent{
		Seq:       seq,
		Timestamp: time.Date(2026, 8, 13, 12, 4, int(seq), 0, time.UTC),
		Author:    author,
		Text:      text,
	}
}

func subagentModel(t *testing.T, a Agent) *Model {
	t.Helper()
	m := Model{}
	m.styles = newStyles(true, Branding{})
	m.width, m.height = 120, 40
	m.opts.Agent = a
	m.seenToolIDs = make(map[string]bool)
	m.subagentTails = make(map[string]*subagentTail)
	m.subagentNotTail = make(map[string]bool)
	// `/subagents <name>` resolves against the off-loop host snapshot
	// rather than calling Subagents() from Update (issue #137), so the
	// fixture seeds one the way the periodic refresh would.
	if cmd := m.refreshHostSnapshotCmd(); cmd != nil {
		if snap, ok := cmd().(hostSnapshotMsg); ok {
			m.hostSnap = snap.snap
		}
	}
	return &m
}

func TestResolveSubagentName(t *testing.T) {
	roster := []SubagentInfo{
		{Name: "cluster-1"},
		{Name: "cluster-2"},
		{Name: "cluster-probe"},
		{Name: "auditor"},
	}
	tests := []struct {
		name       string
		query      string
		want       string
		candidates []string
	}{
		{"exact", "auditor", "auditor", nil},
		{"case insensitive", "AUDITOR", "auditor", nil},
		{"exact instance", "cluster-1", "cluster-1", nil},
		// "cluster" is the DECLARED name of cluster-1 and cluster-2
		// both, so it can't be resolved silently — but cluster-probe
		// must not be dragged in: "-probe" is a name, not a counter.
		{"ambiguous declared name", "cluster", "", []string{"cluster-1", "cluster-2"}},
		{"unknown falls through to the host", "nope", "", nil},
		{"empty", "  ", "", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, cands := resolveSubagentName(tc.query, roster)
			if got != tc.want {
				t.Errorf("resolveSubagentName(%q) = %q, want %q", tc.query, got, tc.want)
			}
			if strings.Join(cands, ",") != strings.Join(tc.candidates, ",") {
				t.Errorf("candidates = %v, want %v", cands, tc.candidates)
			}
		})
	}
}

func TestResolveSubagentName_SingleInstanceResolves(t *testing.T) {
	// The common case: one live instance of a subagent the operator
	// declared as "cluster". Typing the declared name must find it.
	roster := []SubagentInfo{{Name: "cluster-1"}, {Name: "auditor"}}
	got, cands := resolveSubagentName("cluster", roster)
	if got != "cluster-1" || cands != nil {
		t.Fatalf("resolveSubagentName(cluster) = (%q, %v), want (cluster-1, nil)", got, cands)
	}
}

func TestSubagentDeclaredName(t *testing.T) {
	tests := map[string]string{
		"cluster-1":     "cluster",
		"cluster-12":    "cluster",
		"cluster-probe": "cluster-probe", // not a counter
		"cluster-":      "cluster-",
		"-1":            "-1",
		"plain":         "plain",
	}
	for in, want := range tests {
		if got := subagentDeclaredName(in); got != want {
			t.Errorf("subagentDeclaredName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMergeSubagentEvents_DropsSeqDuplicates(t *testing.T) {
	have := []SubagentEvent{turn(1, "model", "a"), turn(2, "model", "b")}
	got := mergeSubagentEvents(have, []SubagentEvent{turn(2, "model", "b"), turn(3, "model", "c")})
	if len(got) != 3 {
		t.Fatalf("expected 3 events after merge, got %d", len(got))
	}
	if got[2].Text != "c" {
		t.Errorf("expected the new turn last, got %q", got[2].Text)
	}
}

func TestRenderSubagentTurns_ShowsToolTraffic(t *testing.T) {
	styles := newStyles(true, Branding{})
	evs := []SubagentEvent{{
		Seq:       7,
		Timestamp: time.Date(2026, 8, 13, 12, 4, 7, 0, time.UTC),
		Author:    "model",
		Text:      "checking the cluster",
		ToolCalls: []SubagentToolCall{{
			ID: "c1", Name: "bash", Args: map[string]any{"command": "kubectl get pods"},
		}},
		ToolResults: []SubagentToolResult{{
			ID: "c1", Name: "bash", Response: map[string]any{"output": "3 pods running"},
		}},
	}}
	got := strings.Join(renderSubagentTurns(evs, styles, 100), "\n")
	for _, want := range []string{"12:04:07", "model", "checking the cluster", "bash", "kubectl get pods", "3 pods running"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected turn render to contain %q, got:\n%s", want, got)
		}
	}
}

func TestRenderSubagentTurns_FailedToolReadsAsError(t *testing.T) {
	styles := newStyles(true, Branding{})
	evs := []SubagentEvent{{
		Seq:         1,
		ToolResults: []SubagentToolResult{{Name: "bash", Error: "exit status 1"}},
	}}
	got := strings.Join(renderSubagentTurns(evs, styles, 100), "\n")
	if !strings.Contains(got, "exit status 1") || !strings.Contains(got, GlyphToolFail) {
		t.Errorf("expected a failure marker and the error text, got:\n%s", got)
	}
}

func TestRenderSubagentTurns_TruncatesToWidth(t *testing.T) {
	styles := newStyles(true, Branding{})
	long := strings.Repeat("x", 500)
	lines := renderSubagentTurns([]SubagentEvent{turn(1, "model", long)}, styles, 80)
	if len(lines) != 1 {
		t.Fatalf("expected one line, got %d", len(lines))
	}
	if len([]rune(ansi.Strip(lines[0]))) > 80 {
		t.Errorf("line exceeds the 80-col budget: %d cols", len([]rune(ansi.Strip(lines[0]))))
	}
}

func TestTruncateDisplay_CutsOnRuneBoundaries(t *testing.T) {
	// A byte-wise cut of this string mid-rune would produce U+FFFD.
	got := truncateDisplay("日本語のテキストです", 5)
	if strings.ContainsRune(got, '�') {
		t.Fatalf("truncateDisplay cut mid-rune: %q", got)
	}
	if n := len([]rune(got)); n != 5 {
		t.Errorf("expected 5 runes, got %d (%q)", n, got)
	}
}

// --- overlay ---------------------------------------------------------

func TestSubagentDialog_RendersUntruncatedReport(t *testing.T) {
	// Issue #70: the roster list clips LastReport at 60 columns; the
	// overlay is where the rest of it has to be readable.
	report := "found " + strings.Repeat("a very long finding. ", 12)
	m := subagentModel(t, &subagentAgent{})
	d := newSubagentDialog("auditor")
	d.apply(subagentEventsMsg{
		name:    "auditor",
		info:    SubagentInfo{Name: "auditor", Status: "done", LastReport: report},
		hasInfo: true,
		page:    SubagentEventPage{Events: []SubagentEvent{turn(1, "model", "starting")}},
	})
	// Read inside the box edge: the report wraps, and a row's closing
	// edge glyph followed by the next row's opening one would land in
	// the middle of the text this is looking for.
	out := strings.Join(modalContentLines(d.Render(m.width, m)), "\n")
	// The tail of the report — the part the list would have cut —
	// has to be present somewhere in the (wrapped) body.
	if !strings.Contains(collapseWhitespace(out), collapseWhitespace(report)) {
		t.Errorf("expected the full report in the overlay, got:\n%s", out)
	}
	if !strings.Contains(out, "starting") {
		t.Errorf("expected the turn log under the report, got:\n%s", out)
	}
}

func TestSubagentDialog_NotFoundNamesAlternatives(t *testing.T) {
	m := subagentModel(t, &subagentAgent{})
	d := newSubagentDialog("clustr")
	d.apply(subagentEventsMsg{
		name: "clustr",
		err:  &SubagentNotFoundError{Name: "clustr", Available: []string{"cluster-1", "auditor"}},
	})
	out := ansi.Strip(d.Render(m.width, m))
	if !strings.Contains(out, "cluster-1") || !strings.Contains(out, "auditor") {
		t.Errorf("expected the available names in the body, got:\n%s", out)
	}
	if strings.Contains(out, "no turns recorded yet") {
		t.Errorf("a typo must not render as an empty-but-valid log:\n%s", out)
	}
}

func TestSubagentDialog_TailAppendsAndFollows(t *testing.T) {
	m := subagentModel(t, &subagentAgent{})
	m.height = 20 // small viewport so the log overflows and scroll matters
	d := newSubagentDialog("auditor")
	for i := 1; i <= 30; i++ {
		d.apply(subagentEventsMsg{
			name: "auditor",
			page: SubagentEventPage{Events: []SubagentEvent{turn(int64(i), "model", fmt.Sprintf("step %d", i))},
				NextSince: int64(i)},
		})
	}
	if d.since != 30 {
		t.Errorf("cursor should advance with the pages, got %d", d.since)
	}
	out := ansi.Strip(d.Render(m.width, m))
	if !strings.Contains(out, "step 30") {
		t.Errorf("pinned overlay should follow the newest turn, got:\n%s", out)
	}
	// Scrolling up releases the pin, and later turns must not yank
	// the operator back to the bottom.
	for i := 0; i < 25; i++ {
		d.HandleKey("up", m)
	}
	d.apply(subagentEventsMsg{
		name: "auditor",
		page: SubagentEventPage{Events: []SubagentEvent{turn(31, "model", "step 31")}, NextSince: 31},
	})
	out = ansi.Strip(d.Render(m.width, m))
	if strings.Contains(out, "step 31") {
		t.Errorf("unpinned overlay should stay where the operator left it, got:\n%s", out)
	}
	// G re-pins.
	d.HandleKey("G", m)
	out = ansi.Strip(d.Render(m.width, m))
	if !strings.Contains(out, "step 31") {
		t.Errorf("G should jump back to the newest turn, got:\n%s", out)
	}
}

func TestSubagentDialog_StaleReplyIgnored(t *testing.T) {
	d := newSubagentDialog("auditor")
	if d.apply(subagentEventsMsg{name: "someone-else", page: SubagentEventPage{
		Events: []SubagentEvent{turn(1, "model", "wrong log")}}}) {
		t.Fatal("a reply for another subagent must not be adopted")
	}
	if len(d.events) != 0 {
		t.Errorf("expected no events, got %d", len(d.events))
	}
}

func TestOpenSubagentDetail_AmbiguousAsksRatherThanGuessing(t *testing.T) {
	a := &subagentAgent{roster: []SubagentInfo{{Name: "cluster-1"}, {Name: "cluster-2"}}}
	m := subagentModel(t, a)
	text, cmd := m.openSubagentDetail("cluster")
	if cmd != nil {
		t.Error("expected no fetch for an ambiguous name")
	}
	if !strings.Contains(text, "cluster-1") || !strings.Contains(text, "cluster-2") {
		t.Errorf("expected both candidates offered, got %q", text)
	}
	if m.overlayStack.hasID(subagentDialogID) {
		t.Error("expected no overlay opened for an ambiguous name")
	}
}

func TestOpenSubagentDetail_OpensAndFetches(t *testing.T) {
	a := &subagentAgent{
		roster: []SubagentInfo{{Name: "auditor", Status: "running"}},
		turns:  map[string][]SubagentEvent{"auditor": {turn(1, "model", "hello")}},
	}
	m := subagentModel(t, a)
	text, cmd := m.openSubagentDetail("auditor")
	if text != "" {
		t.Errorf("expected no system message on success, got %q", text)
	}
	if cmd == nil {
		t.Fatal("expected a fetch + tick batch")
	}
	if !m.overlayStack.hasID(subagentDialogID) {
		t.Fatal("expected the overlay on the stack")
	}
	// Re-issuing retargets rather than stacking a second copy.
	m.openSubagentDetail("auditor")
	n := 0
	for m.overlayStack.hasID(subagentDialogID) {
		m.overlayStack.close(subagentDialogID)
		n++
		if n > 3 {
			break
		}
	}
	if n != 1 {
		t.Errorf("expected exactly one overlay instance, found %d", n)
	}
}

func TestOpenSubagentDetail_NoReporterExplainsWhy(t *testing.T) {
	m := subagentModel(t, stubAgent{})
	text, cmd := m.openSubagentDetail("auditor")
	if cmd != nil {
		t.Error("expected no fetch without a SubagentReporter")
	}
	if !strings.Contains(text, "SubagentReporter") {
		t.Errorf("expected the missing capability named, got %q", text)
	}
}

// --- inline live tail ------------------------------------------------

func TestSubagentTail_RendersUnderRunningToolRow(t *testing.T) {
	a := &subagentAgent{turns: map[string][]SubagentEvent{
		"auditor": {turn(1, "model", "reading the manifest")},
	}}
	m := subagentModel(t, a)
	call := toolCallMsg{id: "call-1", name: "auditor"}
	m.applyToolCall(call)
	if cmd := m.startSubagentTail(call); cmd == nil {
		t.Fatal("expected a tail to start for a subagent-shaped tool call")
	} else {
		msg, ok := cmd().(subagentTailMsg)
		if !ok {
			t.Fatalf("expected a subagentTailMsg, got %T", cmd())
		}
		m.applySubagentTail(msg)
	}
	idx := m.history.FindByToolCallID("call-1")
	preview := ansi.Strip(m.history.Snapshot()[idx].ToolPreview)
	if !strings.Contains(preview, "reading the manifest") {
		t.Errorf("expected the live turn under the tool row, got:\n%s", preview)
	}
	if !strings.Contains(preview, "1 turn") {
		t.Errorf("expected a counts line, got:\n%s", preview)
	}
}

func TestSubagentTail_CollapsesOnResult(t *testing.T) {
	a := &subagentAgent{turns: map[string][]SubagentEvent{
		"auditor": {turn(1, "model", "reading the manifest"), turn(2, "model", "done")},
	}}
	m := subagentModel(t, a)
	call := toolCallMsg{id: "call-1", name: "auditor"}
	m.applyToolCall(call)
	cmd := m.startSubagentTail(call)
	m.applySubagentTail(cmd().(subagentTailMsg))

	m.applyToolResult(toolResultMsg{id: "call-1", name: "auditor",
		response: map[string]any{"output": "clean"}})
	idx := m.history.FindByToolCallID("call-1")
	preview := ansi.Strip(m.history.Snapshot()[idx].ToolPreview)
	if strings.Contains(preview, "reading the manifest") {
		t.Errorf("expected the live block to collapse once the result landed, got:\n%s", preview)
	}
	if !strings.Contains(preview, "2 turns") || !strings.Contains(preview, "/subagents auditor") {
		t.Errorf("expected a summary pointing at the overlay, got:\n%s", preview)
	}
	if _, still := m.subagentTails["call-1"]; still {
		t.Error("expected the tail to be retired")
	}
}

func TestSubagentTail_OrdinaryToolStopsAndIsCachedOff(t *testing.T) {
	// read_file isn't a subagent. The tail gets a few polls of grace
	// (a real subagent legitimately has no turns yet at call time),
	// then gives up and never pays for that tool name again.
	a := &subagentAgent{turns: map[string][]SubagentEvent{"auditor": {turn(1, "model", "hi")}}}
	m := subagentModel(t, a)
	call := toolCallMsg{id: "call-1", name: "read_file", args: map[string]any{"path": "a.go"}}
	m.applyToolCall(call)
	cmd := m.startSubagentTail(call)
	if cmd == nil {
		t.Fatal("the first call of an unknown tool should still be probed")
	}
	for i := 0; i < subagentTailMissLimit; i++ {
		msg := subagentTailMsg{callID: "call-1", name: "read_file",
			err: &SubagentNotFoundError{Name: "read_file"}}
		cmd = m.applySubagentTail(msg)
	}
	if cmd != nil {
		t.Error("expected the tail to stop after the miss limit")
	}
	if !m.subagentNotTail["read_file"] {
		t.Error("expected read_file to be cached as not-a-subagent")
	}
	if got := m.startSubagentTail(toolCallMsg{id: "call-2", name: "read_file"}); got != nil {
		t.Error("expected no tail for a tool already proven not to be a subagent")
	}
}

func TestSubagentTail_SlowStartIsNotGivenUpOn(t *testing.T) {
	// A sync subagent that hasn't written its first turn yet 404s
	// exactly like an ordinary tool. Giving up on the first miss
	// would mean the feature only worked for already-running
	// subagents — so the tail keeps asking.
	m := subagentModel(t, &subagentAgent{})
	call := toolCallMsg{id: "call-1", name: "auditor"}
	m.applyToolCall(call)
	m.startSubagentTail(call)
	cmd := m.applySubagentTail(subagentTailMsg{callID: "call-1", name: "auditor",
		err: &SubagentNotFoundError{Name: "auditor"}})
	if cmd == nil {
		t.Fatal("expected the tail to keep polling after one miss")
	}
	if m.subagentNotTail["auditor"] {
		t.Error("one miss must not poison the tool name")
	}
}

func TestSubagentTail_TransportErrorDropsTailQuietly(t *testing.T) {
	m := subagentModel(t, &subagentAgent{})
	call := toolCallMsg{id: "call-1", name: "auditor"}
	m.applyToolCall(call)
	m.startSubagentTail(call)
	cmd := m.applySubagentTail(subagentTailMsg{callID: "call-1", name: "auditor",
		err: errors.New("connection reset")})
	if cmd != nil {
		t.Error("expected no further polls after a transport failure")
	}
	if m.subagentNotTail["auditor"] {
		t.Error("a transport failure says nothing about whether the tool is a subagent")
	}
}

func TestSubagentTail_NoReporterNoPolling(t *testing.T) {
	m := subagentModel(t, stubAgent{})
	if cmd := m.startSubagentTail(toolCallMsg{id: "call-1", name: "auditor"}); cmd != nil {
		t.Error("a host without SubagentReporter must never be polled")
	}
}

func TestSubagentTail_BlockIsBounded(t *testing.T) {
	// A chatty subagent must not push the composer off the screen:
	// the inline block keeps only the newest few lines.
	m := subagentModel(t, &subagentAgent{})
	m.applyToolCall(toolCallMsg{id: "call-1", name: "auditor"})
	m.subagentTails["call-1"] = &subagentTail{name: "auditor"}
	var evs []SubagentEvent
	for i := 1; i <= 40; i++ {
		evs = append(evs, turn(int64(i), "model", fmt.Sprintf("step %d", i)))
	}
	m.applySubagentTail(subagentTailMsg{callID: "call-1", name: "auditor",
		page: SubagentEventPage{Events: evs, NextSince: 40}})
	preview := ansi.Strip(m.history.Snapshot()[m.history.FindByToolCallID("call-1")].ToolPreview)
	if n := len(strings.Split(preview, "\n")); n > subagentTailMaxLines+1 {
		t.Errorf("inline block is %d lines, want at most %d", n, subagentTailMaxLines+1)
	}
	if !strings.Contains(preview, "step 40") {
		t.Errorf("expected the NEWEST turns kept, got:\n%s", preview)
	}
	if strings.Contains(preview, "step 1 ") {
		t.Errorf("expected the oldest turns dropped, got:\n%s", preview)
	}
}

func TestSubagentTailName_PrefersExplicitArg(t *testing.T) {
	got := subagentTailName("run_subagent", map[string]any{"agent": "cluster-1"})
	if got != "cluster-1" {
		t.Errorf("expected the arg-named target, got %q", got)
	}
	if got := subagentTailName("auditor", nil); got != "auditor" {
		t.Errorf("expected the tool name as the default, got %q", got)
	}
}

// subagentEventsCmd and the tick helpers are thin, but the gen guard
// they feed is what keeps a retired session's replies from painting
// into a new one — worth pinning.
func TestSubagentPollTick_CarriesGeneration(t *testing.T) {
	cmd := subagentPollTick(7, "auditor")
	msg, ok := cmd().(subagentPollMsg)
	if !ok {
		t.Fatalf("expected subagentPollMsg, got %T", cmd())
	}
	if msg.gen != 7 || msg.name != "auditor" {
		t.Errorf("got %+v, want gen 7 / auditor", msg)
	}
}

func TestSubagentEventsCmd_CarriesRosterInfo(t *testing.T) {
	a := &subagentAgent{
		roster: []SubagentInfo{{Name: "auditor", Status: "running", LastReport: "all good"}},
		turns:  map[string][]SubagentEvent{"auditor": {turn(1, "model", "hi")}},
	}
	cmd := subagentEventsCmd(a, 1, "auditor", 0)
	msg, ok := cmd().(subagentEventsMsg)
	if !ok {
		t.Fatalf("expected subagentEventsMsg, got %T", cmd())
	}
	if !msg.hasInfo || msg.info.LastReport != "all good" {
		t.Errorf("expected the roster entry to ride along, got %+v", msg)
	}
	if len(msg.page.Events) != 1 {
		t.Errorf("expected one turn, got %d", len(msg.page.Events))
	}
}

func TestSubagentUpdate_DropsStaleGeneration(t *testing.T) {
	a := &subagentAgent{turns: map[string][]SubagentEvent{"auditor": {turn(1, "model", "hi")}}}
	m := subagentModel(t, a)
	m.sessionGen = 2
	m.overlayStack.open(newSubagentDialog("auditor"))
	next, _ := m.Update(subagentEventsMsg{gen: 1, name: "auditor",
		page: SubagentEventPage{Events: []SubagentEvent{turn(1, "model", "stale")}}})
	got := next.(Model)
	d := got.overlayStack.get(subagentDialogID).(*subagentDialog)
	if len(d.events) != 0 {
		t.Errorf("a retired generation's page must not paint, got %d events", len(d.events))
	}
}

func TestRenderSubagentList_PointsAtTheOverlayWhenClipped(t *testing.T) {
	subs := []SubagentInfo{{Name: "auditor", Status: "done",
		LastReport: strings.Repeat("finding ", 30)}}
	got := renderSubagentList(subs)
	if !strings.Contains(got, "/subagents <name>") {
		t.Errorf("expected a pointer to the drill-down, got:\n%s", got)
	}
	if !strings.Contains(got, "full report") {
		t.Errorf("expected the hint to mention the clipped report, got:\n%s", got)
	}
	// An unclipped report still points at the overlay, for the turn
	// log — but doesn't promise more of a report than was shown.
	short := renderSubagentList([]SubagentInfo{{Name: "auditor", Status: "done", LastReport: "all good"}})
	if !strings.Contains(short, "/subagents <name>") {
		t.Errorf("expected the turn-log pointer even unclipped, got:\n%s", short)
	}
	if strings.Contains(short, "full report") {
		t.Errorf("nothing was clipped, so the hint must not offer the rest of it, got:\n%s", short)
	}
}

var _ tea.Cmd = subagentTailTick(1, "x")
