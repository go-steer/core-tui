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

// Tests for the deny-with-reason step (R-PERM-9, issue #344).
//
// The reason is opt-in per request, by which method the host called:
// AskApprovalDetailed offers it and plain AskApproval does not. Every
// test here drives the real path — a live Prompter, the listener's
// message, Update — so "the host got the reason" is asserted as the
// blocked call returning it, not as a field someone set.

package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// reasonRig is permissionRig for either entry point. detailed picks
// AskApprovalDetailed; the returned channel carries whatever the
// blocked call returned, with the Reason empty for plain AskApproval.
func reasonRig(t *testing.T, layout PermissionLayout, detailed bool) (*model, <-chan PermissionOutcome) {
	t.Helper()
	p := NewPrompter()
	done := make(chan PermissionOutcome, 1)
	req := PermissionRequest{ToolName: "bash", Verb: "run", Detail: "rm -rf /tmp/x"}
	go func() {
		if detailed {
			out, _ := p.AskApprovalDetailed(context.Background(), req)
			done <- out
			return
		}
		d, _ := p.AskApproval(context.Background(), req)
		done <- PermissionOutcome{Decision: d}
	}()

	m := newModel(Options{Agent: &bareAgent{id: "a"}, Prompter: p, PermissionLayout: layout})
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = out.(*model)
	// Through the listener, so the opt-in flag travels the way it does
	// in production rather than being set on the message by hand.
	msg := m.promptListener()()
	out, _ = m.Update(msg)
	m = out.(*model)
	if m.openPermission() == nil {
		t.Fatal("setup: the permission question is not on the overlay stack")
	}
	m.overlayStack.asked(permissionDialogID).shownAt = time.Now().Add(-modalInputGrace - time.Millisecond)
	return m, done
}

func pressKeys(t *testing.T, m *model, strokes ...string) *model {
	t.Helper()
	for _, s := range strokes {
		out, _ := m.Update(keyPress(s))
		m = out.(*model)
	}
	return m
}

func pasteText(t *testing.T, m *model, s string) *model {
	t.Helper()
	out, _ := m.Update(tea.PasteMsg{Content: s})
	return out.(*model)
}

func awaitOutcome(t *testing.T, done <-chan PermissionOutcome) PermissionOutcome {
	t.Helper()
	select {
	case o := <-done:
		return o
	case <-time.After(time.Second):
		t.Fatal("the host call is still blocked a second later")
	}
	return PermissionOutcome{}
}

func assertUndecided(t *testing.T, m *model, done <-chan PermissionOutcome) {
	t.Helper()
	if m.openPermission() == nil {
		t.Fatal("the prompt closed; nothing should have been decided")
	}
	select {
	case o := <-done:
		t.Fatalf("the host got %+v; nothing should have been decided", o)
	default:
	}
}

// A request through plain AskApproval never offers the reason: no "r"
// in the legend, and pressing it does nothing. A host that cannot
// forward a reason must never collect one.
func TestPermissionReason_PlainAskApprovalOffersNoReason(t *testing.T) {
	for _, layout := range []PermissionLayout{PermissionInline, PermissionOverlay} {
		m, done := reasonRig(t, layout, false)
		q := m.openPermission()
		if strings.Contains(ansi.Strip(q.legend()), "reason") {
			t.Errorf("layout %v: legend offers a reason to a plain AskApproval: %q", layout, q.legend())
		}
		m = pressKeys(t, m, "r")
		if m.openPermission().typing() {
			t.Errorf("layout %v: r opened the reason input on a plain AskApproval", layout)
		}
		assertUndecided(t, m, done)
		pressKeys(t, m, "n")
		if o := awaitOutcome(t, done); o.Decision != DecisionDeny || o.Reason != "" {
			t.Errorf("layout %v: n gave %+v, want a plain deny", layout, o)
		}
	}
}

// And AskApprovalDetailed does, next to the plain deny.
func TestPermissionReason_DetailedOffersTheReasonKey(t *testing.T) {
	m, _ := reasonRig(t, PermissionOverlay, true)
	legend := strings.ReplaceAll(ansi.Strip(m.openPermission().legend()), "\u00a0", " ")
	if !strings.Contains(legend, "n deny · r deny with reason…") {
		t.Errorf("legend = %q, want r next to the plain deny", legend)
	}
}

// The whole round trip, in both layouts: r, type, enter, and the host's
// blocked call returns deny plus exactly what was typed (trimmed), and
// the transcript echo records it.
func TestPermissionReason_DenyWithReasonRoundTrips(t *testing.T) {
	for _, layout := range []PermissionLayout{PermissionInline, PermissionOverlay} {
		m, done := reasonRig(t, layout, true)
		m = pressKeys(t, m, "r")
		if !m.openPermission().typing() {
			t.Fatalf("layout %v: r did not open the reason input", layout)
		}
		m = pasteText(t, m, "  use a scratch dir under ./tmp instead  ")
		m = pressKeys(t, m, "enter")
		o := awaitOutcome(t, done)
		if o.Decision != DecisionDeny || o.Reason != "use a scratch dir under ./tmp instead" {
			t.Errorf("layout %v: host got %+v", layout, o)
		}
		if m.openPermission() != nil {
			t.Errorf("layout %v: the prompt is still open after the deny", layout)
		}
		snap := m.history.Snapshot()
		echo := snap[len(snap)-1].Text
		if !strings.Contains(echo, "Permission deny") || !strings.Contains(echo, "Reason: use a scratch dir") {
			t.Errorf("layout %v: echo %q does not record the deny and its reason", layout, echo)
		}
	}
}

// An empty or whitespace-only reason is a plain deny.
func TestPermissionReason_EmptyReasonIsAPlainDeny(t *testing.T) {
	for _, typed := range []string{"", "   "} {
		m, done := reasonRig(t, PermissionOverlay, true)
		m = pressKeys(t, m, "r")
		if typed != "" {
			m = pasteText(t, m, typed)
		}
		m = pressKeys(t, m, "enter")
		if o := awaitOutcome(t, done); o.Decision != DecisionDeny || o.Reason != "" {
			t.Errorf("reason %q gave %+v, want a plain deny", typed, o)
		}
		snap := m.history.Snapshot()
		if echo := snap[len(snap)-1].Text; strings.Contains(echo, "Reason:") {
			t.Errorf("reason %q: echo %q records a reason", typed, echo)
		}
	}
}

// Esc in the input goes back to the choices without deciding; esc at
// the choices still denies — plainly, with no reason, even though text
// had been typed.
func TestPermissionReason_EscFromTheInputReturnsToTheChoices(t *testing.T) {
	m, done := reasonRig(t, PermissionInline, true)
	m = pressKeys(t, m, "r")
	m = pasteText(t, m, "half a thought")
	if got := m.footerHint(); !strings.Contains(got, "esc back") {
		t.Errorf("footer while typing = %q, want esc back", got)
	}
	m = pressKeys(t, m, "esc")
	q := m.openPermission()
	if q == nil || q.typing() {
		t.Fatal("esc in the input did not return to the choices")
	}
	assertUndecided(t, m, done)
	if got := m.footerHint(); !strings.Contains(got, "esc deny") {
		t.Errorf("footer at the choices = %q, want esc deny", got)
	}

	// r again picks up where the operator left off.
	m = pressKeys(t, m, "r")
	if got := m.openPermission().input.Value(); got != "half a thought" {
		t.Errorf("reopened input = %q, want the text kept", got)
	}
	pressKeys(t, m, "esc", "esc")
	if o := awaitOutcome(t, done); o.Decision != DecisionDeny || o.Reason != "" {
		t.Errorf("esc at the choices gave %+v, want a plain deny", o)
	}
}

// Plain n stays a single keypress on a request that offers the reason.
func TestPermissionReason_NStaysASingleKeyDeny(t *testing.T) {
	m, done := reasonRig(t, PermissionOverlay, true)
	pressKeys(t, m, "n")
	if o := awaitOutcome(t, done); o.Decision != DecisionDeny || o.Reason != "" {
		t.Errorf("n gave %+v, want a plain deny", o)
	}
}

// While the input is open the decision letters are text. Typing "yes
// stay" must not allow anything, and the letters land in the input.
func TestPermissionReason_LettersTypeRatherThanDecide(t *testing.T) {
	for _, layout := range []PermissionLayout{PermissionInline, PermissionOverlay} {
		m, done := reasonRig(t, layout, true)
		m = pressKeys(t, m, "r", "y", "s", "t", "a", "v", "n", "r")
		assertUndecided(t, m, done)
		if got := m.openPermission().input.Value(); got != "ystavnr" {
			t.Errorf("layout %v: input = %q, want every letter typed", layout, got)
		}
		pressKeys(t, m, "enter")
		if o := awaitOutcome(t, done); o.Decision != DecisionDeny || o.Reason != "ystavnr" {
			t.Errorf("layout %v: host got %+v", layout, o)
		}
	}
}

// The cap is 500 BYTES, the server's limit, not 500 runes: 200 × "é"
// is 400 bytes and goes; 300 × "é" is 600 and is refused with a note,
// left open for the operator to shorten, and never truncated.
func TestPermissionReason_ByteCap(t *testing.T) {
	m, done := reasonRig(t, PermissionOverlay, true)
	m = pressKeys(t, m, "r")
	m = pasteText(t, m, strings.Repeat("é", 300))
	frame, _ := m.modalFrame()
	if !strings.Contains(ansi.Strip(frame), "600/500 bytes") {
		t.Errorf("frame does not show the byte count over the cap:\n%s", ansi.Strip(frame))
	}
	m = pressKeys(t, m, "enter")
	assertUndecided(t, m, done)
	frame, _ = m.modalFrame()
	if !strings.Contains(ansi.Strip(frame), "too long to send") {
		t.Errorf("refused enter left no note:\n%s", ansi.Strip(frame))
	}
	if got := m.openPermission().input.Value(); got != strings.Repeat("é", 300) {
		t.Error("the over-long reason was altered; it must be left for the operator to shorten")
	}

	// Shorten to 200 runes = 400 bytes: within the cap, and it goes.
	for range 100 {
		m = pressKeys(t, m, "backspace")
	}
	if m.openPermission().overLimit {
		t.Error("the note outlived the edit that fixed it")
	}
	pressKeys(t, m, "enter")
	o := awaitOutcome(t, done)
	if o.Decision != DecisionDeny || o.Reason != strings.Repeat("é", 200) || len(o.Reason) != 400 {
		t.Errorf("host got decision %v with a %d-byte reason, want deny with 400", o.Decision, len(o.Reason))
	}
}

// Exactly at the cap is allowed; one byte past is not.
func TestPermissionReason_CapBoundary(t *testing.T) {
	m, done := reasonRig(t, PermissionOverlay, true)
	m = pressKeys(t, m, "r")
	m = pasteText(t, m, strings.Repeat("x", permissionDenyReasonMax+1))
	m = pressKeys(t, m, "enter")
	assertUndecided(t, m, done)
	pressKeys(t, m, "backspace", "enter")
	if o := awaitOutcome(t, done); len(o.Reason) != permissionDenyReasonMax {
		t.Errorf("a reason of exactly the cap was not sent whole: %d bytes", len(o.Reason))
	}
}

// Plain AskApproval still returns every decision it always did, through
// the same keys.
func TestPermissionReason_AskApprovalUnchanged(t *testing.T) {
	want := map[string]PermissionDecision{
		"y":   DecisionAllowOnce,
		"n":   DecisionDeny,
		"s":   DecisionAllowSession,
		"v":   DecisionAllowSessionVerb,
		"t":   DecisionAllowSessionTool,
		"a":   DecisionAllowAlways,
		"esc": DecisionDeny,
	}
	for stroke, d := range want {
		m, done := reasonRig(t, PermissionOverlay, false)
		pressKeys(t, m, stroke)
		if o := awaitOutcome(t, done); o.Decision != d {
			t.Errorf("%q: AskApproval returned %v, want %v", stroke, o.Decision, d)
		}
	}
}

// Allows never carry a reason, even from a request that offered one.
func TestPermissionReason_AllowsCarryNoReason(t *testing.T) {
	m, done := reasonRig(t, PermissionOverlay, true)
	pressKeys(t, m, "y")
	if o := awaitOutcome(t, done); o.Decision != DecisionAllowOnce || o.Reason != "" {
		t.Errorf("y gave %+v", o)
	}
	// And the dispatch path drops one handed to it on an allow, or to
	// a flow that never offered the step.
	p := NewPrompter()
	res := make(chan PermissionOutcome, 1)
	go func() {
		d, _ := p.AskApproval(context.Background(), PermissionRequest{ToolName: "x"})
		res <- PermissionOutcome{Decision: d}
	}()
	if _, ok := p.nextRequest(context.Background()); !ok {
		t.Fatal("setup: no request")
	}
	p.dispatchDecision(DecisionDeny, "should not travel")
	if o := <-res; o.Reason != "" {
		t.Errorf("a plain flow received a reason: %+v", o)
	}
	go func() {
		o, _ := p.AskApprovalDetailed(context.Background(), PermissionRequest{ToolName: "x"})
		res <- o
	}()
	if _, ok := p.nextRequest(context.Background()); !ok {
		t.Fatal("setup: no request")
	}
	p.dispatchDecision(DecisionAllowOnce, "should not travel")
	if o := <-res; o.Decision != DecisionAllowOnce || o.Reason != "" {
		t.Errorf("an allow carried a reason: %+v", o)
	}
}

// A cancelled ctx on AskApprovalDetailed is a reasonless deny plus the
// ctx error, the same as AskApproval's.
func TestPermissionReason_DetailedHonoursCancel(t *testing.T) {
	p := NewPrompter()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	o, err := p.AskApprovalDetailed(ctx, PermissionRequest{ToolName: "x"})
	if err == nil || o.Decision != DecisionDeny || o.Reason != "" {
		t.Errorf("cancelled AskApprovalDetailed = %+v, %v", o, err)
	}
}

// A session switch tearing the prompt down while the operator is
// mid-reason is still a plain deny — dismissals never carry text.
func TestPermissionReason_SupersededIsAPlainDeny(t *testing.T) {
	m, done := reasonRig(t, PermissionOverlay, true)
	m = pressKeys(t, m, "r")
	m = pasteText(t, m, "typed but never sent")
	m.overlayStack.resolveAll(dismissSuperseded, m)
	if o := awaitOutcome(t, done); o.Decision != DecisionDeny || o.Reason != "" {
		t.Errorf("superseded prompt gave %+v, want a plain deny", o)
	}
}

// The grace window: while the input is open only enter is held, so a
// letter typed in the first instant still lands in the input. At the
// choices, r is held like the decision keys, because it begins a deny.
func TestPermissionReason_GraceWindow(t *testing.T) {
	q := newPermissionQuestion(PermissionRequest{ToolName: "bash"}, PermissionOverlay, true)
	if !q.Commits(keyPress("r")) {
		t.Error("r is not held by the grace window")
	}
	q.Key(keyPress("r"))
	if q.Commits(keyPress("y")) {
		t.Error("y is held while typing; the grace window would swallow text")
	}
	if !q.Commits(keyPress("enter")) {
		t.Error("enter is not held while typing")
	}
	plain := newPermissionQuestion(PermissionRequest{ToolName: "bash"}, PermissionOverlay, false)
	if plain.Commits(keyPress("r")) {
		t.Error("r is held on a prompt that does not offer it")
	}
}

// The caret: the centered layout hands the terminal a real one on the
// input row; the inline layout hides the composer's while typing, and
// gives it back once the input closes.
func TestPermissionReason_Caret(t *testing.T) {
	m, _ := reasonRig(t, PermissionOverlay, true)
	if c, _ := m.modalCursor(""); c != nil {
		t.Error("a caret is shown before the input is open")
	}
	m = pressKeys(t, m, "r")
	m = pasteText(t, m, "abc")
	frame, ok := m.modalFrame()
	if !ok {
		t.Fatal("no modal frame")
	}
	c, covered := m.modalCursor(frame)
	if c == nil || !covered {
		t.Fatal("no caret on the reason input in the centered layout")
	}
	rows := strings.Split(ansi.Strip(m.View().Content), "\n")
	if c.Y < 0 || c.Y >= len(rows) || !strings.Contains(rows[c.Y], "▎ abc") {
		t.Errorf("caret row %d is not the input row", c.Y)
	}

	m, _ = reasonRig(t, PermissionInline, true)
	m = pressKeys(t, m, "r")
	if c, covered := m.modalCursor(""); c != nil || !covered {
		t.Error("the composer keeps the caret while the inline reason input is open")
	}
	m = pressKeys(t, m, "esc")
	if _, covered := m.modalCursor(""); covered {
		t.Error("the composer did not get the caret back")
	}
}
