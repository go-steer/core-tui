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
// Returns ok=false (and leaves model untouched) when the agent
// doesn't satisfy InboxDrainer, the inbox is empty, or the soft
// cap has been hit — caller should fall through to the regular
// maybeDrainQueue path in those cases.
func (m Model) maybeAutoContinue() (Model, tea.Cmd, bool) {
	if m.opts.MidTurnInjectionMode != AutoContinueFromInbox {
		return m, nil, false
	}
	drainer, ok := m.opts.Agent.(InboxDrainer)
	if !ok {
		return m, nil, false
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
		return m, nil, false
	}
	drained := compactNonEmpty(drainer.DrainInbox())
	if len(drained) == 0 {
		return m, nil, false
	}

	formatter := m.opts.AutoContinueFormatter
	if formatter == nil {
		formatter = defaultAutoContinueFormatter
	}
	prompt := formatter(drained)
	if strings.TrimSpace(prompt) == "" {
		return m, nil, false
	}

	// Mark any matching Queued entries as Done — the operator's
	// view of "what did the system process" stays accurate.
	m.markQueueDoneByText(drained)
	m.consecutiveAutoContinues++

	// submitTurn appends the RoleUser entry itself (as part of
	// the normal turn lifecycle); MarkLastUserAutoContinue then
	// flips the AutoContinue bit so the renderer picks ↻ + muted
	// on the next paint. Avoids a double-append.
	out := m.submitTurn(prompt)
	out.history.MarkLastUserAutoContinue()
	return out, tea.Batch(spinnerTick(), out.eventListener()), true
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

// markQueueDoneByText flips any QueueQueued / QueueInFlight entry
// whose Text matches one of the drained messages to QueueDone.
// Best-effort by text equality — the inbox channel doesn't carry
// queue-entry IDs through to the host, so a queue entry typed
// before AutoContinueFromInbox was wired (and a stale duplicate
// in the inbox) could mis-match. Acceptable: worst case the
// stale entry lingers an extra cullTTL.
func (m *Model) markQueueDoneByText(drained []string) {
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
