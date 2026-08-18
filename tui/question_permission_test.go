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

// Tests for the permission prompt as a question (#164 stage 3).
//
// The pixels are pinned by golden_permission_test.go and the grace
// window by modal_grace_test.go; what is left, and what is here, is the
// behaviour the migration is allowed to change and the behaviour it is
// not. The option table replaced three hand-maintained lists that had
// to agree by inspection, so the first tests are about the lists still
// agreeing — now by construction, which is only worth claiming if
// something checks it.

package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Every one of R-PERM-2's keys reaches its decision, and each one is a
// distinct decision. A table rather than six cases because the point is
// the mapping as a whole: a duplicate value here is a key that silently
// grants the wrong scope, which no single-key test would catch.
func TestPermissionQuestion_EveryKeyDecides(t *testing.T) {
	q := newPermissionQuestion(PermissionRequest{ToolName: "bash", Verb: "run"}, PermissionOverlay)
	want := map[string]PermissionDecision{
		"y": DecisionAllowOnce,
		"n": DecisionDeny,
		"s": DecisionAllowSession,
		"v": DecisionAllowSessionVerb,
		"t": DecisionAllowSessionTool,
		"a": DecisionAllowAlways,
	}
	seen := map[PermissionDecision]string{}
	for stroke, wantDecision := range want {
		ans, _ := q.Key(keyPress(stroke))
		d, ok := ans.(decision)
		if !ok {
			t.Errorf("%q answered %T, want decision", stroke, ans)
			continue
		}
		if d.Value != wantDecision {
			t.Errorf("%q decided %v, want %v", stroke, d.Value, wantDecision)
		}
		if other, dup := seen[d.Value]; dup {
			t.Errorf("%q and %q both decide %v", other, stroke, d.Value)
		}
		seen[d.Value] = stroke
	}
}

// The verb key is conditional, and the condition is in one place now.
// Without a Verb the key must neither appear in the legend nor answer —
// advertising a grant that does nothing is confusing, and honouring an
// unadvertised one widens authority the operator was never shown.
func TestPermissionQuestion_VerbKeyOnlyWithAVerb(t *testing.T) {
	q := newPermissionQuestion(PermissionRequest{ToolName: "bash"}, PermissionOverlay)
	if strings.Contains(ansi.Strip(q.legend()), "allow verb") {
		t.Errorf("legend offers the verb grant with no verb to scope it to: %q", q.legend())
	}
	if ans, _ := q.Key(keyPress("v")); ans != nil {
		t.Errorf("v answered %#v with no verb on the request", ans)
	}
}

// The legend and the key switch read the same slice, so they cannot
// disagree — this is the test that says so. Every pair the footer shows
// is either esc or a key the question answers, and every key it answers
// is shown.
func TestPermissionQuestion_LegendMatchesTheKeys(t *testing.T) {
	for _, verb := range []string{"", "run"} {
		q := newPermissionQuestion(PermissionRequest{ToolName: "bash", Verb: verb}, PermissionOverlay)
		// keyLegend joins with a separator and binds each pair with a
		// non-breaking space, so split on the separator and read the
		// first field back off.
		advertised := map[string]bool{}
		for _, pair := range strings.Split(ansi.Strip(q.legend()), " "+GlyphSeparator+" ") {
			advertised[strings.Fields(strings.ReplaceAll(pair, " ", " "))[0]] = true
		}
		if !advertised["esc"] {
			t.Errorf("verb=%q: legend does not mention esc, which always denies", verb)
		}
		delete(advertised, "esc")
		for _, o := range q.opts {
			if !advertised[o.key] {
				t.Errorf("verb=%q: %q decides but is not in the legend", verb, o.key)
			}
			delete(advertised, o.key)
		}
		for stroke := range advertised {
			t.Errorf("verb=%q: legend offers %q, which the question does not answer", verb, stroke)
		}
	}
}

// Esc dismisses rather than deciding, and the resolver is what turns
// that into a deny — the split matters because it is what lets the
// grace window exempt esc without exempting a grant.
func TestPermissionQuestion_EscIsADismissal(t *testing.T) {
	q := newPermissionQuestion(PermissionRequest{ToolName: "bash"}, PermissionOverlay)
	ans, _ := q.Key(keyPress("esc"))
	d, ok := ans.(dismissed)
	if !ok {
		t.Fatalf("esc answered %T, want dismissed", ans)
	}
	if d.Reason != dismissEscape {
		t.Errorf("esc reason = %v, want dismissEscape", d.Reason)
	}
}

// permissionRig delivers a request through the normal path with a live
// Prompter behind it, so a dispatch is observable as the blocked
// AskApproval call returning.
func permissionRig(t *testing.T, layout PermissionLayout) (Model, <-chan PermissionDecision) {
	t.Helper()
	p := NewPrompter()
	decided := make(chan PermissionDecision, 1)
	req := PermissionRequest{ToolName: "bash", Verb: "run", Detail: "rm -rf /tmp/x"}
	go func() {
		d, _ := p.AskApproval(context.Background(), req)
		decided <- d
	}()
	if _, ok := p.nextRequest(context.Background()); !ok {
		t.Fatal("setup: nextRequest returned !ok with a pending request")
	}

	m := NewModel(Options{Agent: &bareAgent{id: "a"}, Prompter: p, PermissionLayout: layout})
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = out.(Model)
	out, _ = m.Update(permissionRequestMsg{req: req})
	m = out.(Model)
	if m.openPermission() == nil {
		t.Fatal("setup: the permission question is not on the overlay stack")
	}
	// Past the grace window, so the decision keys are live.
	m.overlayStack.asked(permissionDialogID).shownAt = time.Now().Add(-modalInputGrace - time.Millisecond)
	return m, decided
}

// A decision dispatches to the blocked host call, pops the question and
// echoes the choice into the transcript. The echo is the only record an
// audit reader has of why a tool ran.
func TestPermissionQuestion_DecisionDispatchesAndEchoes(t *testing.T) {
	m, decided := permissionRig(t, PermissionOverlay)
	before := m.history.Len()

	out, _ := m.Update(keyPress("s"))
	m = out.(Model)

	if m.openPermission() != nil {
		t.Error("the question is still on the stack after a decision")
	}
	select {
	case got := <-decided:
		if got != DecisionAllowSession {
			t.Errorf("decision = %v, want AllowSession", got)
		}
	case <-time.After(time.Second):
		t.Fatal("AskApproval still blocked a second after the decision")
	}
	if m.history.Len() != before+1 {
		t.Fatalf("history %d → %d, want one decision echo", before, m.history.Len())
	}
	snap := m.history.Snapshot()
	if echo := snap[len(snap)-1].Text; !strings.Contains(echo, "allow-session") {
		t.Errorf("transcript echo %q does not name the decision", echo)
	}
}

// Esc denies through the resolver. Same assertion as the decision case
// on purpose: the operator backing out must reach the host exactly as
// firmly as the operator saying no, or the tool call hangs.
func TestPermissionQuestion_EscDeniesThroughTheResolver(t *testing.T) {
	m, decided := permissionRig(t, PermissionOverlay)

	out, _ := m.Update(keyPress("esc"))
	m = out.(Model)

	if m.openPermission() != nil {
		t.Error("esc left the question on the stack")
	}
	select {
	case got := <-decided:
		if got != DecisionDeny {
			t.Errorf("esc dispatched %v, want Deny", got)
		}
	case <-time.After(time.Second):
		t.Fatal("esc dispatched nothing")
	}
}

// The inline layout is not a modal: nothing composites over the frame,
// the caret stays in the composer, and the block appears in the
// transcript instead. All three are the same claim, and all three are
// read by different callers, so all three are asserted.
func TestPermissionQuestion_InlineIsNotAModal(t *testing.T) {
	m, _ := permissionRig(t, PermissionInline)

	if frame, ok := m.modalFrame(); ok {
		t.Errorf("inline prompt reported a modal frame: %q", ansi.Strip(frame))
	}
	if _, covered := m.modalCursor(""); covered {
		t.Error("inline prompt claims to cover the frame; the composer still owns the caret")
	}
	// The block is drawn in place of the spinner line, so it needs a
	// turn in flight to be drawn at all — which is the only state a
	// tool call can ask for permission from.
	m.state = stateStreaming
	m.refreshViewport()
	if body := ansi.Strip(m.View().Content); !strings.Contains(body, "Permission required: bash") {
		t.Error("inline prompt is not in the transcript")
	}
}

// And the centered layout is: the same request under PermissionOverlay
// composites a frame. Paired with the test above so a layout wired to
// the wrong branch fails on one side or the other.
func TestPermissionQuestion_OverlayIsAModal(t *testing.T) {
	m, _ := permissionRig(t, PermissionOverlay)

	frame, ok := m.modalFrame()
	if !ok {
		t.Fatal("centered prompt reported no modal frame")
	}
	if !strings.Contains(ansi.Strip(frame), "Permission required: bash") {
		t.Errorf("modal frame is not the permission prompt: %q", ansi.Strip(frame))
	}
}

// A modal opened behind an inline prompt waits rather than drawing over
// it. Before the migration the prompt was Model state and the overlay
// stack drew regardless, so a picker could cover the block while the
// keys still went to the block — pixels and keys disagreeing, which is
// what #164 is about.
func TestPermissionQuestion_InlinePromptSuppressesTheStackBehindIt(t *testing.T) {
	m, _ := permissionRig(t, PermissionInline)
	askThemePicker(&m)
	// The picker went on FIRST in this fixture's terms — the prompt was
	// already open — so it is now the front one and does draw.
	if _, ok := m.modalFrame(); !ok {
		t.Fatal("setup: a picker opened over the inline prompt should draw")
	}

	// Resolve the picker and the inline prompt is front again.
	m.overlayStack.closeFront()
	if _, ok := m.modalFrame(); ok {
		t.Error("the inline prompt is front again; nothing should composite over the frame")
	}
}

// The regression the golden corpus caught: the diff arm used to render
// through the model's chat-column renderer and ignore the width it was
// handed, so an 80-column modal holding a diff drew a box as wide as
// the terminal. Asserted on geometry rather than on bytes because the
// goldens already own the bytes — this is the invariant that says why
// they changed.
func TestPermissionQuestion_DiffRendersAtTheModalWidth(t *testing.T) {
	req := PermissionRequest{
		ToolName:   "edit_file",
		Verb:       "edit",
		DetailKind: DetailDiff,
		Detail:     "--- a/config.go\n+++ b/config.go\n@@ -10,4 +10,4 @@\n-    return parse(b)\n+    return parse(b), nil\n",
	}
	m := NewModel(Options{Agent: &bareAgent{id: "a"}, PermissionLayout: PermissionOverlay})
	m.styles = newStylesWithTheme(true, goldenTheme())
	out, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m = out.(Model)
	out, _ = m.Update(permissionRequestMsg{req: req})
	m = out.(Model)

	frame, ok := m.modalFrame()
	if !ok {
		t.Fatal("no modal frame for a centered permission prompt")
	}
	// The box is permissionModalWidth wide, so no row of it may draw
	// wider — a payload that ignores its width expands the lipgloss
	// surface and takes the box with it.
	for i, line := range strings.Split(frame, "\n") {
		if w := ansi.StringWidth(line); w > permissionModalWidth {
			t.Errorf("modal row %d draws at %d cells in an %d-column box: %q",
				i, w, permissionModalWidth, ansi.Strip(line))
		}
	}
}
