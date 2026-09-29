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
	"regexp"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Folding the inbox bundle-handling guidance (issue #298). The
// contract under test:
//
//   - a row carrying the guidance draws it as one marker line, by
//     default, on both paths the text can arrive by;
//   - space on the row opens it in place and space again folds it;
//   - Message.Text, and so the saved transcript, keeps the full text;
//   - rows without the block, and rows that merely mention the
//     heading, are left alone.

// testInboxGuidance mirrors core-agent's inboxHandlingGuidance
// (pkg/agent/inbox.go) closely enough to exercise the shape: the
// heading, a bullet list, a closing line, no blank lines.
const testInboxGuidance = "How to handle the bundle:\n" +
	"- A new question, request, or topic → answer or do it, directly.\n" +
	"- Variants of the same ask or signal → treat as ONE; don't re-do work per message.\n" +
	"- Corroborating detail on something you already handled → acknowledge it and move on.\n" +
	"- Mid-task adjustments → adapt your next step.\n" +
	"- Separate asks during an active task → capture with `todo`, continue what you were doing.\n" +
	"- After a completed task → treat the bundle as the next request and respond once.\n" +
	"Summarizing work you already reported is not a response to a new question."

// testInboxBundle is the daemon's "[Inbox]" block as formatInboxBundle
// writes it.
const testInboxBundle = "[Inbox]\n- from platform-oncall@example.com: who are you?\n\n" + testInboxGuidance

var guidanceMarkerRE = regexp.MustCompile(`▸ bundle-handling guidance \((\d+) lines\)`)

// guidanceModel is a live-mode model — the attach path, where the
// inbox turn's prompt arrives as a committed chunk — with the
// transcript focused.
func guidanceModel(t *testing.T) *model {
	t.Helper()
	m := newModel(Options{Agent: &bareAgent{id: "inbox"}})
	m.styles = newStylesWithTheme(true, goldenTheme())
	m.liveMode = true
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	return out.(*model)
}

// rowText is the stripped render of history row i as the transcript
// draws it.
func rowText(m *model, i int) string {
	msg, _ := m.history.at(i)
	return ansi.Strip(strings.Join(m.chatMessageLines(i, m.history.Len(), msg), "\n"))
}

func TestSplitInboxGuidance(t *testing.T) {
	head, guidance, tail, ok := splitInboxGuidance(testInboxBundle + "\n\n---\n\nand check the nodes")
	if !ok {
		t.Fatal("the daemon's bundle was not recognised")
	}
	if guidance != testInboxGuidance {
		t.Errorf("guidance = %q, want the block alone", guidance)
	}
	if !strings.HasSuffix(head, "who are you?\n\n") {
		t.Errorf("head = %q, want the header and bullets", head)
	}
	if tail != "\n\n---\n\nand check the nodes" {
		t.Errorf("tail = %q, want the separator and the prompt", tail)
	}

	for _, text := range []string{
		"",
		"plain answer",
		testInboxGuidance, // no bracketed header
		"[Inbox]\n- see How to handle the bundle: later", // heading not at a line start
		"[Inbox]\n- from ops: disk is full",              // the N-without-guidance shape
	} {
		if _, _, _, ok := splitInboxGuidance(text); ok {
			t.Errorf("splitInboxGuidance(%q) matched, want no guidance", text)
		}
	}
}

// The attach path: a committed chunk carrying the bundle lands as an
// assistant row whose guidance is folded to the marker, with the
// operator's question still in view.
func TestInboxGuidance_FoldedByDefaultOnTheAttachPath(t *testing.T) {
	m := guidanceModel(t)
	out, _ := m.Update(streamChunkMsg{gen: m.sessionGen, text: testInboxBundle})
	m = out.(*model)

	snap := m.history.Snapshot()
	if len(snap) != 1 || snap[0].Role != RoleAssistant {
		t.Fatalf("history = %+v, want one committed assistant row", snap)
	}
	if snap[0].Text != testInboxBundle {
		t.Error("Message.Text lost the guidance; the fold must be display-only")
	}

	got := rowText(m, 0)
	if !strings.Contains(got, "who are you?") {
		t.Errorf("the operator's question is gone from the row:\n%s", got)
	}
	if strings.Contains(got, "Corroborating detail") || strings.Contains(got, "How to handle the bundle") {
		t.Errorf("the folded row still draws the guidance:\n%s", got)
	}
	match := guidanceMarkerRE.FindStringSubmatch(got)
	if match == nil {
		t.Fatalf("no guidance marker in the folded row:\n%s", got)
	}

	// The count is what opening the block adds.
	folded := strings.Count(got, "\n")
	m.setFocus(focusTranscript)
	m.selIdx = 0
	m = press(m, "space")
	open := rowText(m, 0)
	if !strings.Contains(open, "Corroborating detail") {
		t.Fatalf("space did not open the guidance:\n%s", open)
	}
	if guidanceMarkerRE.MatchString(open) {
		t.Error("the opened row still draws the marker")
	}
	n, _ := strconv.Atoi(match[1])
	if grew := strings.Count(open, "\n") - folded; grew < n-2 || grew > n+2 {
		t.Errorf("opening grew the row by %d lines, the marker promised %d", grew, n)
	}

	m = press(m, "space")
	if again := rowText(m, 0); again != got {
		t.Errorf("space again did not restore the folded row:\n got %q\nwant %q", again, got)
	}
}

// The prompt that follows the block ("---" and the operator's own
// text) is not part of the guidance and stays visible.
func TestInboxGuidance_TheFollowingPromptStaysVisible(t *testing.T) {
	m := guidanceModel(t)
	out, _ := m.Update(streamChunkMsg{gen: m.sessionGen, text: testInboxBundle + "\n\n---\n\nand check the nodes"})
	m = out.(*model)
	got := rowText(m, 0)
	if !strings.Contains(got, "and check the nodes") || !guidanceMarkerRE.MatchString(got) {
		t.Errorf("want the marker and the trailing prompt, got:\n%s", got)
	}
}

// The in-process path: the auto-continue row carries the host
// formatter's output, guidance included.
func TestInboxGuidance_FoldedOnAnAutoContinueRow(t *testing.T) {
	m := selectModel(t, 1, 100, 40)
	text := "[Operator notes queued while you were working]\n- also check staging\n\n" + testInboxGuidance
	m.history.Append(Message{Role: RoleUser, Text: text, AutoContinue: true})
	m.refreshViewport()
	i := m.history.Len() - 1

	got := rowText(m, i)
	if !strings.Contains(got, "also check staging") || !guidanceMarkerRE.MatchString(got) {
		t.Fatalf("want the note and the marker, got:\n%s", got)
	}
	if strings.Contains(got, "Mid-task adjustments") {
		t.Errorf("the folded auto-continue row still draws the guidance:\n%s", got)
	}

	m.selIdx = i
	m = press(m, "space")
	if open := rowText(m, i); !strings.Contains(open, "Mid-task adjustments") {
		t.Errorf("space did not open the guidance on the auto-continue row:\n%s", open)
	}
}

// Saved transcripts keep the full text: the fold is display-only.
func TestInboxGuidance_TranscriptKeepsTheFullText(t *testing.T) {
	m := guidanceModel(t)
	out, _ := m.Update(streamChunkMsg{gen: m.sessionGen, text: testInboxBundle})
	m = out.(*model)
	tr := buildTranscript(m)
	if len(tr.Messages) == 0 || tr.Messages[0].Text != testInboxBundle {
		t.Errorf("transcript = %+v, want the full bundle text", tr.Messages)
	}
}

// A row without the block keeps the generic fold (issue #152), and a
// guidance row never takes it on top of the marker.
func TestInboxGuidance_OrdinaryRowsKeepTheGenericFold(t *testing.T) {
	m := selectModel(t, 2, 80, 40)
	m.selIdx = 1
	msg, _ := m.history.at(1)
	m = press(m, "space")
	if !m.chatRowCollapsed(msg) {
		t.Error("an ordinary assistant row no longer folds")
	}

	g := Message{ID: 999, Role: RoleAssistant, Text: testInboxBundle}
	m.collapsed[g.ID] = true
	if m.chatRowCollapsed(g) {
		t.Error("a guidance row took the generic fold")
	}
}
