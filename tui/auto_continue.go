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

// Auto-continue-from-inbox loop (issue #9). When the host's agent
// satisfies InboxDrainer and Options.MidTurnInjectionMode ==
// AutoContinueFromInbox, the turn-end path drains the inbox and
// submits a synthetic turn carrying every queued message instead
// of asking the operator to type Enter again. Mirrors what
// core-agent's internal/tui ships as PR α of operator-input-design.md.

package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// maybeAutoContinue is the turn-end path for AutoContinueFromInbox
// mode. Drains the host's inbox, formats the messages into a
// synthetic prompt, marks matching queue entries Done, and submits
// the result as a fresh turn with a Message.AutoContinue marker
// so the renderer can distinguish it from an operator-typed turn.
//
// Returns ok=false when the mode is off, the agent doesn't satisfy
// InboxDrainer, the inbox is empty, the formatted prompt is blank,
// or the soft cap has been hit — caller should fall through to the
// regular maybeDrainQueue path in those cases. Every ok=false path
// leaves the model untouched except the cap-reached one, which
// appends a "cap reached" system row to history and refreshes the
// viewport; those writes land on the receiver and persist.
func (m *model) maybeAutoContinue() (tea.Cmd, bool) {
	if m.opts.MidTurnInjectionMode != AutoContinueFromInbox {
		return nil, false
	}
	drainer, ok := m.opts.Agent.(InboxDrainer)
	if !ok {
		return nil, false
	}
	cap := m.opts.AutoContinueCap
	if cap == 0 {
		cap = DefaultAutoContinueCap
	}
	if cap >= 0 && m.consecutiveAutoContinues >= cap {
		// Soft cap hit. Log once, reset so the operator's next
		// prompt picks the messages up cleanly via the normal
		// inbox-prepend path (host responsibility), and fall
		// through. Don't clear consecutiveAutoContinues — that
		// resets only on operator-initiated turns so a second
		// auto-continue burst still surfaces the cap.
		m.history.Append(Message{
			Role: RoleSystem,
			Text: "auto-continue cap reached (" + itoa(cap) + " consecutive). Pending inbox messages will land on your next prompt.",
		})
		m.refreshViewport()
		return nil, false
	}
	drained := compactNonEmpty(drainer.DrainInbox())
	if len(drained) == 0 {
		return nil, false
	}

	formatter := m.opts.AutoContinueFormatter
	if formatter == nil {
		formatter = defaultAutoContinueFormatter
	}
	prompt := formatter(drained)
	if strings.TrimSpace(prompt) == "" {
		return nil, false
	}
	m.consecutiveAutoContinues++

	// submitTypedTurn appends the RoleUser entry itself (as part of
	// the normal turn lifecycle); MarkLastUserAutoContinue then
	// flips the AutoContinue bit so the renderer picks ↻ + muted
	// on the next paint. Avoids a double-append.
	// submitTurnAs expands only the @-references in drained texts this
	// TUI queued, so it reads the queue before it is marked below.
	m.submitTurnAs(prompt, TurnInput{AutoContinue: true, Drained: append([]string(nil), drained...)})
	m.history.MarkLastUserAutoContinue()
	// Mark any matching Queued entries as Done — the operator's
	// view of "what did the system process" stays accurate.
	m.markQueueDoneByText(drained)
	return tea.Batch(m.armSpinner(), m.eventListener()), true
}

// defaultAutoContinueFormatter is the fallback formatting Options.
// AutoContinueFormatter overrides. Frames the drained messages as
// operator notes attached to the previous task, with a "Continue."
// instruction so the model knows the synthetic turn is a follow-
// up rather than a new request.
func defaultAutoContinueFormatter(msgs []string) string {
	var b strings.Builder
	b.WriteString("[Operator notes added during the previous task]\n")
	for _, msg := range msgs {
		b.WriteString("- ")
		b.WriteString(msg)
		b.WriteString("\n")
	}
	b.WriteString("\nContinue.")
	return b.String()
}

// compactNonEmpty filters out trimmed-to-empty messages. Hosts
// occasionally send whitespace-only entries via Inject; including
// them in the bullet list would look broken.
func compactNonEmpty(in []string) []string {
	out := in[:0]
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

// autoContinueFiles is the files section an auto-continue prompt gains
// (#364): the files that @-references in the drained texts this TUI
// queued itself point at, or "" when there are none. A relayed text's
// references are never expanded — the inbox also holds what a host
// relays, a watcher's wake payload or a chat message, and expanding an
// @path in that would read a local file of the operator's into the
// prompt on a stranger's say-so. The inbox keeps the raw text, so the
// drained texts a host sees (TurnInput.Drained) never carry file
// content.
func (m *model) autoContinueFiles(drained []string) string {
	own := m.ownQueuedTexts(drained)
	if len(own) == 0 {
		return ""
	}
	joined := strings.Join(own, "\n")
	expanded := m.expandAndReport(joined)
	if len(expanded) <= len(joined) {
		return ""
	}
	return expanded[len(joined):]
}

// ownQueuedTexts is the drained texts this TUI injected itself — a
// queued, injected entry's text — each matched at most once per entry,
// so a relayed message is never mistaken for one more copy of the
// operator's.
func (m *model) ownQueuedTexts(drained []string) []string {
	pending := map[string]int{}
	for _, e := range m.queue {
		if e.Injected && e.State == QueueQueued {
			pending[e.Text]++
		}
	}
	var own []string
	for _, s := range drained {
		if pending[s] > 0 {
			pending[s]--
			own = append(own, s)
		}
	}
	return own
}

// markQueueDoneByText flips any QueueQueued / QueueInFlight entry
// whose Text matches one of the drained messages to QueueDone.
// Best-effort by text equality — the inbox channel doesn't carry
// queue-entry IDs through to the host, so a queue entry typed
// before AutoContinueFromInbox was wired (and a stale duplicate
// in the inbox) could mis-match. Acceptable: worst case the
// stale entry lingers an extra cullTTL.
func (m *model) markQueueDoneByText(drained []string) {
	if len(drained) == 0 {
		return
	}
	matched := make(map[string]bool, len(drained))
	for _, s := range drained {
		matched[s] = true
	}
	for i := range m.queue {
		if m.queue[i].State == QueueQueued || m.queue[i].State == QueueInFlight {
			if matched[m.queue[i].Text] {
				m.queue[i].State = QueueDone
			}
		}
	}
}
