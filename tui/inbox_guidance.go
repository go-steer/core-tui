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

// Folding the inbox bundle-handling guidance (issue #298).
//
// When a turn is driven by the inbox, core-agent prepends a fixed
// block of instructions for the MODEL — "How to handle the bundle:"
// and a list of branches (inboxHandlingGuidance in core-agent's
// pkg/agent/inbox.go) — to the bundle it hands the agent. It reaches
// this package as prompt text: on the attach path the user-authored
// eventlog event is streamed back as an ordinary committed Event.Text,
// and on the in-process path it is the AutoContinueFormatter's output
// on an AutoContinue row. Either way the operator's one-line question
// scrolls past under a dozen lines of boilerplate that is identical on
// every inbox turn and is not addressed to them.
//
// So a row carrying the block draws it as one marker line,
//
//	▸ bundle-handling guidance (N lines)
//
// and the ordinary fold key (space, with the transcript focused — see
// select.go) opens it in place. It is folded by default and it stays
// expandable rather than dropped: when the agent's behaviour looks
// like it is following the guidance, reading the exact text in place
// is the fastest way to confirm that.
//
// # Detection is a prefix match, and that is a coupling
//
// There is no structural handle to key on. The guidance is
// concatenated into the prompt string on core-agent's side, and
// neither Event nor InboxEvent carries a field or marker for it —
// adding one would be a protocol change and a new exported field, for
// a cosmetic fix. So the block is found by its heading, which couples
// this file to the wording of core-agent's const: if the heading is
// reworded, the fold silently stops applying and the text renders in
// full, exactly as it did before this file existed. That failure mode
// is the reason a string match is acceptable here.
//
// The match is kept narrow on purpose: the row must open with a
// bracketed header ("[Inbox]", "[Operator notes …]" — every surface
// core-agent formats a bundle for starts with one), and the heading
// must begin a line. The block runs to the next blank line or the end
// of the text; the guidance itself has no blank lines, and what can
// follow it is the "---" separator and the operator's own prompt,
// which stay visible.
//
// # Display only
//
// Message.Text is never touched, so saved transcripts, /copy and
// everything else that reads the source keep the full text. The fold
// is applied in renderMessage and the toggle bumps the row's Version,
// which is the one place this departs from select.go's "a fold is a
// view of the render": the marker replaces lines in the middle of a
// Glamour render, which cannot be done by slicing its output.
package tui

import (
	"fmt"
	"strings"
)

// inboxGuidanceHeading opens core-agent's inboxHandlingGuidance
// (pkg/agent/inbox.go). Matched verbatim — see the file comment for
// why that coupling is tolerable.
const inboxGuidanceHeading = "How to handle the bundle:"

// splitInboxGuidance splits text around the bundle-handling guidance
// block. ok is false when text carries none, in which case the other
// results are empty. head+guidance+tail == text whenever ok is true.
func splitInboxGuidance(text string) (head, guidance, tail string, ok bool) {
	if !strings.HasPrefix(text, "[") {
		return "", "", "", false
	}
	start := strings.Index(text, "\n"+inboxGuidanceHeading)
	if start < 0 {
		return "", "", "", false
	}
	start++ // past the newline: head keeps it
	end := len(text)
	if j := strings.Index(text[start:], "\n\n"); j >= 0 {
		end = start + j
	}
	return text[:start], text[start:end], text[end:], true
}

// guidanceMarker is the one line a folded guidance block draws.
func guidanceMarker(lines int) string {
	return fmt.Sprintf("%s bundle-handling guidance (%d lines)", glyphCollapsed, lines)
}

// hasInboxGuidance reports whether msg is a row the guidance fold
// applies to. Only prompt text can carry the block: an assistant row
// on the attach path, where the user-authored event arrives as a
// committed chunk, and a user row on the in-process auto-continue
// path.
func hasInboxGuidance(msg Message) bool {
	if msg.Role != RoleAssistant && msg.Role != RoleUser {
		return false
	}
	_, _, _, ok := splitInboxGuidance(msg.Text)
	return ok
}

// guidanceFolded reports whether msg's guidance block is drawn as the
// marker. Folded is the default, so it is the ABSENCE of an entry in
// m.collapsed that means folded here — the inverse of every other row
// — and an explicit false records that the operator opened it.
func (m model) guidanceFolded(msg Message) bool {
	folded, set := m.collapsed[msg.ID]
	return !set || folded
}

// foldedGuidanceText is the source text with the guidance block
// replaced by the marker, for the rows that draw raw text (user rows,
// and assistant rows with no Glamour render). wrap is the row's own
// wrapper, so the count is what opening the block adds to the row.
func foldedGuidanceText(head, guidance, tail string, wrap func(string) string) string {
	return head + guidanceMarker(strings.Count(wrap(guidance), "\n")+1) + tail
}

// renderFoldedAssistantGuidance renders an assistant row whose text
// carries the guidance, with the block folded. The head and tail go
// through Glamour on their own and the marker is spliced between
// them, muted and at Glamour's document margin, so it reads as a
// control rather than as something the agent said. The count is the
// block's own rendered height — what the marker stands in for.
//
// Returns ok=false when there is no renderer to hand the pieces to;
// the caller falls back to the raw path.
func (m model) renderFoldedAssistantGuidance(head, guidance, tail string) (string, bool) {
	mr := m.markdown
	if mr == nil || mr.r == nil {
		return "", false
	}
	n := strings.Count(strings.TrimLeft(mr.renderMarkdown(guidance), "\n"), "\n") + 1
	var b strings.Builder
	if strings.TrimSpace(head) != "" {
		b.WriteString(mr.renderMarkdown(head))
		b.WriteString("\n\n")
	}
	b.WriteString(glamourMargin)
	b.WriteString(m.styles.Muted.Render(guidanceMarker(n)))
	if t := strings.TrimLeft(tail, "\n"); t != "" {
		// Glamour opens a render with a newline of its own; dropping
		// it leaves one blank line under the marker, as above it.
		b.WriteString("\n")
		b.WriteString(strings.TrimPrefix(mr.renderMarkdown(t), "\n"))
	}
	return b.String(), true
}

// glamourMargin is the left margin Glamour's document block draws,
// which the spliced marker line has to match to sit in the same
// column as the text around it.
const glamourMargin = "  "
