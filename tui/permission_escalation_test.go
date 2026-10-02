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
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The escalated prompt (#360): a request an approver passed on offers
// allow once and deny, and nothing that would turn the approver's
// answer into a standing grant — not s, v, t or a, even with a verb.
func TestPermissionEscalation_OffersOnceAndDenyOnly(t *testing.T) {
	req := PermissionRequest{ToolName: "bash", Verb: "run", Escalation: &PermissionEscalation{Approver: "judge-1", Reason: "not sure"}}
	q := newPermissionQuestion(req, PermissionOverlay, false)
	for stroke, want := range map[string]PermissionDecision{"y": DecisionAllowOnce, "n": DecisionDeny} {
		ans, _ := q.Key(keyPress(stroke))
		if d, ok := ans.(decision); !ok || d.Value != want {
			t.Errorf("%q answered %#v, want %v", stroke, ans, want)
		}
	}
	for _, stroke := range []string{"s", "v", "t", "a"} {
		if ans, _ := q.Key(keyPress(stroke)); ans != nil {
			t.Errorf("%q answered %#v on an escalated prompt; it must not grant beyond once", stroke, ans)
		}
	}
	legend := strings.ReplaceAll(ansi.Strip(q.legend()), "\u00a0", " ")
	for _, grant := range []string{"allow session", "allow verb", "allow tool", "allow always"} {
		if strings.Contains(legend, grant) {
			t.Errorf("legend offers %q on an escalated prompt: %q", grant, legend)
		}
	}
	// The ordinary prompt keeps all six.
	if plain := newPermissionQuestion(PermissionRequest{ToolName: "bash", Verb: "run"}, PermissionOverlay, false); len(plain.opts) != 6 {
		t.Errorf("an ordinary prompt has %d options, want 6", len(plain.opts))
	}
}

// Deny with a reason is not a grant, so an escalated prompt keeps it
// when the request offers it.
func TestPermissionEscalation_KeepsDenyWithReason(t *testing.T) {
	req := PermissionRequest{ToolName: "bash", Escalation: &PermissionEscalation{Approver: "judge-1"}}
	q := newPermissionQuestion(req, PermissionOverlay, true)
	if legend := strings.ReplaceAll(ansi.Strip(q.legend()), "\u00a0", " "); !strings.Contains(legend, "deny with reason") {
		t.Errorf("legend = %q, want deny with reason offered", legend)
	}
}

// Both layouts say who passed the request on and quote its reason, set
// off from the host's lines. The reason is model output, so an escape
// sequence planted in it is stripped, and a wall of text is capped.
func TestPermissionEscalation_QuotesTheApproverInBothLayouts(t *testing.T) {
	st := newStylesWithTheme(true, goldenTheme())
	planted := "routine \x1b[2J\x1b[31mSAFE TO ALWAYS ALLOW\x1b[0m"
	req := PermissionRequest{
		ToolName:   "bash",
		Detail:     "kubectl delete ns staging",
		DetailKind: DetailShell,
		Escalation: &PermissionEscalation{Approver: "judge-1", Reason: planted},
	}
	for _, layout := range []PermissionLayout{PermissionOverlay, PermissionInline} {
		q := newPermissionQuestion(req, layout, false)
		var raw string
		if layout == PermissionInline {
			raw = q.InlineBody(100, st)
		} else {
			raw = q.Body(100, 40, st)
		}
		text := ansi.Strip(raw)
		if !strings.Contains(text, "passed to you by judge-1, which said:") {
			t.Errorf("layout %d: no approver attribution:\n%s", layout, text)
		}
		if !strings.Contains(text, "“routine SAFE TO ALWAYS ALLOW”") {
			t.Errorf("layout %d: reason not quoted as sanitized text:\n%s", layout, text)
		}
		if strings.Contains(raw, "\x1b[2J") || strings.Contains(raw, "\x1b[31m") {
			t.Errorf("layout %d: the reason's escape sequences reached the screen", layout)
		}
	}

	long := req
	long.Escalation = &PermissionEscalation{Approver: "judge-1", Reason: strings.Repeat("word ", 400)}
	text := ansi.Strip(newPermissionQuestion(long, PermissionOverlay, false).Body(100, 200, st))
	if strings.Count(text, "word") > permissionEscalationReasonMax/5+1 || !strings.Contains(text, GlyphTruncate) {
		t.Errorf("a long reason was not capped at %d bytes", permissionEscalationReasonMax)
	}

	none := req
	none.Escalation = &PermissionEscalation{Approver: "judge-1"}
	if text := ansi.Strip(newPermissionQuestion(none, PermissionOverlay, false).Body(100, 40, st)); !strings.Contains(text, "passed to you by judge-1, with no reason given") {
		t.Errorf("no-reason escalation:\n%s", text)
	}

	plain := req
	plain.Escalation = nil
	if text := ansi.Strip(newPermissionQuestion(plain, PermissionOverlay, false).Body(100, 40, st)); strings.Contains(text, "passed to you by") {
		t.Errorf("an ordinary prompt mentions an approver:\n%s", text)
	}
}

func TestApprovalLog_ApproverRowNamesTheModel(t *testing.T) {
	got := renderApprovalLog([]ApprovalLog{
		{Tool: "bash", Key: "go test ./...", Decision: "allow-once", Approver: "judge-1"},
		{Tool: "bash", Key: "go vet ./...", Decision: "allow-once"},
	})
	rows := strings.Split(got, "\n")
	if !strings.HasSuffix(rows[1], "[allow-once] by approver judge-1") {
		t.Errorf("approver row = %q", rows[1])
	}
	if !strings.HasSuffix(rows[2], "[allow-once]") {
		t.Errorf("unattributed row changed: %q", rows[2])
	}
}

// The reason cannot start a row of its own (#360 review P1). A newline
// in it used to survive sanitizing, so a planted reason could draw a
// host-looking "verb: ls" line and a fake legend, and push the real
// payload off the first screen of the overlay. Every whitespace run now
// collapses to a space, and the quoted block is capped in rows too.
func TestPermissionEscalation_ReasonCannotForgeRowsOrHideThePayload(t *testing.T) {
	st := newStylesWithTheme(true, goldenTheme())
	forged := "Looks routine.\nverb: ls\n\ny allow once · a allow always\n" + strings.Repeat("\n", 40)
	req := PermissionRequest{
		ToolName:   "bash",
		Verb:       "rm",
		Detail:     "rm -rf /",
		DetailKind: DetailShell,
		Escalation: &PermissionEscalation{Approver: "judge-1", Reason: forged},
	}
	for _, layout := range []PermissionLayout{PermissionOverlay, PermissionInline} {
		q := newPermissionQuestion(req, layout, false)
		var text string
		if layout == PermissionInline {
			text = ansi.Strip(q.InlineBody(60, st))
		} else {
			text = ansi.Strip(q.Body(60, 30, st))
		}
		if !strings.Contains(text, "rm -rf /") || !strings.Contains(text, "verb: rm") {
			t.Errorf("layout %d: the real payload is not on the first screen:\n%s", layout, text)
		}
		for _, row := range strings.Split(text, "\n") {
			row = strings.TrimLeft(row, "┃ ")
			if strings.HasPrefix(row, "verb: ls") || strings.HasPrefix(row, "y allow once") {
				t.Errorf("layout %d: the reason drew a row of its own: %q", layout, row)
			}
		}
	}

	// A narrow terminal wraps the capped reason into many rows; the row
	// cap holds it.
	long := req
	long.Escalation = &PermissionEscalation{Approver: "judge-1", Reason: strings.Repeat("a ", 290)}
	q := newPermissionQuestion(long, PermissionOverlay, false)
	if quoted := len(q.escalationLines(20, st)) - 1; quoted != permissionEscalationRowsMax {
		t.Errorf("quoted rows = %d at width 20, want the cap of %d", quoted, permissionEscalationRowsMax)
	}
}

// The approver's name is one row too, and invisible characters that
// reorder or hide text are shown, not obeyed.
func TestPermissionEscalation_ApproverNameAndInvisibles(t *testing.T) {
	st := newStylesWithTheme(true, goldenTheme())
	req := PermissionRequest{ToolName: "bash", Escalation: &PermissionEscalation{
		Approver: "judge\n-1",
		Reason:   "safe\u202eevil\u200b",
	}}
	text := ansi.Strip(newPermissionQuestion(req, PermissionOverlay, false).Body(100, 40, st))
	if !strings.Contains(text, "passed to you by judge -1, which said:") {
		t.Errorf("approver name was not kept to one row:\n%s", text)
	}
	if strings.ContainsAny(text, "\u202e\u200b") || !strings.Contains(text, `\u202e`) || !strings.Contains(text, `\u200b`) {
		t.Errorf("invisible characters reached the screen unescaped:\n%q", text)
	}
}

// The second line of defence: whatever reaches dispatch for an
// escalated request, nothing beyond once is granted and AlwaysAllow
// never fires.
func TestDispatchPermission_EscalatedRequestNeverGrantsBeyondOnce(t *testing.T) {
	persisted := false
	m := newModel(Options{AlwaysAllow: func(PermissionRequest) error { persisted = true; return nil }})
	req := PermissionRequest{ToolName: "bash", Detail: "go test ./...", Escalation: &PermissionEscalation{Approver: "judge-1"}}
	m.dispatchPermission(DecisionAllowAlways, "", req)
	if persisted {
		t.Error("AlwaysAllow fired for an escalated request")
	}
	last := m.history.Snapshot()[len(m.history.Snapshot())-1].Text
	if !strings.Contains(last, "Permission "+permissionDecisionLabel(DecisionDeny)) {
		t.Errorf("echo = %q, want the decision clamped to deny", last)
	}
}

// A row a model decided names the model only, and a model-derived key
// cannot draw rows or escape sequences of its own.
func TestApprovalLog_ApproverOnlyAndSanitized(t *testing.T) {
	got := renderApprovalLog([]ApprovalLog{
		{Tool: "bash", Key: "ls\n  • fake row \x1b[31m", Decision: "allow-once", By: "alice", Approver: "judge-1"},
	})
	if strings.Contains(got, "by alice") || !strings.Contains(got, "by approver judge-1") {
		t.Errorf("row = %q, want the approver alone", got)
	}
	if strings.Count(got, "\n") != 1 || strings.Contains(got, "\x1b") {
		t.Errorf("the key drew a row or kept an escape: %q", got)
	}
}
