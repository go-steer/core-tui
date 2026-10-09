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

// The running-tasks bar: a strip between the input box and the footer
// with one row per subagent that is still in flight, so the operator
// can see what is running in the background without typing
// `/subagents`.
//
// The data is the SubagentReporter roster the host snapshot already
// pulls off the event loop once a second (host_snapshot.go), so the
// bar costs no host calls of its own and View never blocks on it. The
// same tick is what makes the elapsed column count up.
//
// A running subagent asleep until a scheduled wake (SubagentInfo's
// NextWakeAt) gets a row that counts down to the wake instead of up
// from the start; see docs/scheduled-wakes-design.md.
//
// A subagent that finishes stays on the bar for tasksLinger with its
// outcome, so a short-lived one is seen to have run and the operator
// sees how it ended without having to catch the frame where it
// vanished. Only a subagent the bar watched running lingers: a roster
// read on attach can carry any number of long-finished entries, and
// flashing all of them up for five seconds would announce history as
// news.

package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	// tasksLinger is how long a finished subagent keeps its row.
	tasksLinger = 5 * time.Second

	// tasksBarMaxRows caps the bar. It is a glance at what is in
	// flight, not the roster — that is `/subagents` — and every row it
	// takes is a row of transcript.
	tasksBarMaxRows = 3
)

// taskRoster is the bar's view of the subagent roster across snapshot
// ticks. The snapshot itself is stateless — each one replaces the
// last — so knowing that something just FINISHED needs this memory of
// what was running a tick ago.
type taskRoster struct {
	// active is the in-flight subagents from the latest snapshot, in
	// start order.
	active []SubagentInfo

	// running is the set of names active on the previous tick: the
	// ones a transition to a terminal status should linger for.
	running map[string]bool

	// finished is the lingering rows, oldest first.
	finished []finishedTask
}

// finishedTask is one lingering row and the moment the bar first saw
// it finished.
type finishedTask struct {
	info SubagentInfo
	at   time.Time
}

// taskActive reports whether a roster status means the subagent is
// still in flight. Paused counts: it has not ended, and it will come
// back without being asked.
func taskActive(status string) bool {
	switch strings.ToLower(status) {
	case "running", "paused":
		return true
	}
	return false
}

// observe folds one roster snapshot into the bar's state at now.
func (r *taskRoster) observe(subs []SubagentInfo, now time.Time) {
	running := make(map[string]bool, len(subs))
	var active []SubagentInfo
	for _, s := range subs {
		if taskActive(s.Status) {
			active = append(active, s)
			running[s.Name] = true
			continue
		}
		if r.running[s.Name] {
			r.finished = append(r.finished, finishedTask{info: s, at: now})
		}
	}
	// Stable, so a host that reports no StartedAt keeps its own order.
	sort.SliceStable(active, func(i, j int) bool {
		return active[i].StartedAt.Before(active[j].StartedAt)
	})

	// A subagent that vanished from the roster mid-run is dropped, not
	// lingered: the roster said nothing about how it ended, and a
	// "done" the bar made up would be a claim nobody made.
	kept := r.finished[:0]
	for _, f := range r.finished {
		if now.Sub(f.at) < tasksLinger && !running[f.info.Name] {
			kept = append(kept, f)
		}
	}
	r.finished = kept
	r.active = active
	r.running = running
}

// taskScheduled reports whether a roster entry is asleep until a
// scheduled wake. Only a running subagent counts: a paused one shows as
// paused whatever it had scheduled, because the gate is what the
// operator has to act on, and a finished one has nothing pending.
func taskScheduled(s SubagentInfo) bool {
	return strings.EqualFold(s.Status, "running") && !s.NextWakeAt.IsZero()
}

// counts splits the in-flight subagents the way the status count
// reports them: working, and asleep until a wake. Paused ones are on
// the bar but are neither.
func (r *taskRoster) counts() (running, scheduled int) {
	for _, s := range r.active {
		switch {
		case taskScheduled(s):
			scheduled++
		case strings.EqualFold(s.Status, "running"):
			running++
		}
	}
	return running, scheduled
}

// subagentsCountLabel is the status-line / sidebar count, or "" when
// nothing is running or scheduled. The two are kept apart because the
// count is what survives when the bar is squeezed out, and "2 running"
// for two subagents asleep for ten minutes would be wrong.
func (m *model) subagentsCountLabel() string {
	running, scheduled := m.tasks.counts()
	var parts []string
	switch running {
	case 0:
	case 1:
		parts = append(parts, "1 subagent running")
	default:
		parts = append(parts, fmt.Sprintf("%d subagents running", running))
	}
	if scheduled > 0 {
		label := fmt.Sprintf("%d scheduled", scheduled)
		if running == 0 {
			label = fmt.Sprintf("%d %s scheduled", scheduled, pluralize(scheduled, "subagent", "subagents"))
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, " "+GlyphSeparator+" ")
}

// pluralize picks the singular or plural form for n.
func pluralize(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// tasksBarLines renders the bar's rows at width, at most maxRows of
// them (and never more than tasksBarMaxRows). Nil when there is
// nothing to show or no row to show it in.
//
// When the entries do not fit, the last row becomes a count pointing
// at `/subagents` rather than a silently shorter list: the operator
// should know the bar is not the whole roster.
func (m *model) tasksBarLines(width, maxRows int) []string {
	maxRows = min(maxRows, tasksBarMaxRows)
	total := len(m.tasks.active) + len(m.tasks.finished)
	if total == 0 || maxRows <= 0 {
		return nil
	}
	shown := total
	if total > maxRows {
		shown = maxRows - 1
	}
	now := m.nowFn()
	lines := make([]string, 0, shown+1)
	for i := 0; i < shown; i++ {
		if i < len(m.tasks.active) {
			lines = append(lines, m.tasksBarRow(m.tasks.active[i], true, now, width))
		} else {
			f := m.tasks.finished[i-len(m.tasks.active)]
			lines = append(lines, m.tasksBarRow(f.info, false, now, width))
		}
	}
	if shown < total {
		more := fmt.Sprintf("  + %d more", total-shown)
		if shown == 0 {
			more = fmt.Sprintf("  %d subagents", total)
		}
		lines = append(lines, fitCells(
			m.styles.Muted.Render(more+" "+GlyphSeparator+" /subagents"), width))
	}
	return lines
}

// tasksBarRow renders one subagent: a status glyph, the name, then
// how long it has been running (in flight), when it next wakes
// (scheduled) or how it ended (lingering), then the latest report, cut
// at the frame edge.
func (m *model) tasksBarRow(s SubagentInfo, active bool, now time.Time, width int) string {
	sep := m.sep()
	report := s.LastReport
	var glyph, state string
	switch status := strings.ToLower(s.Status); {
	case active && taskScheduled(s):
		// Counts down to the wake rather than up from the start: for
		// a sleeping subagent "when does it act next" is the question.
		// Past due reads "waking" — the roster is a second-old poll,
		// and a negative or frozen 0s would draw stale data as fact.
		glyph = m.styles.Muted.Render(glyphScheduled)
		if until := s.NextWakeAt.Sub(now); until >= time.Second {
			state = m.styles.Muted.Render("wakes in " + formatTurnElapsed(until))
		} else {
			state = m.styles.Muted.Render("waking")
		}
		// The reason it gave for sleeping is the best one-line
		// account of what it is doing while it sleeps.
		if s.WakeDetail != "" {
			report = s.WakeDetail
		}
	case status == "paused":
		glyph = m.styles.Muted.Render(GlyphPaused)
		state = m.styles.Muted.Render("paused")
	case active:
		glyph = m.styles.Accent.Render(GlyphToolActive)
		if !s.StartedAt.IsZero() {
			state = m.styles.Muted.Render(formatTurnElapsed(now.Sub(s.StartedAt)))
		}
	case status == "failed" || status == "error":
		glyph = m.styles.ErrorText.Render(GlyphToolFail)
		state = m.styles.ErrorText.Render(status)
	default:
		glyph = m.styles.Muted.Render(GlyphToolDone)
		state = m.styles.Muted.Render(status)
	}
	row := "  " + glyph + " " + m.styles.AgentIdentity.Render(sanitizeLine(s.Name))
	if state != "" {
		row += sep + state
	}
	if report := reportHeadline(report); report != "" {
		row += sep + m.styles.Muted.Render(report)
	}
	if width > 0 && ansi.StringWidth(row) > width {
		row = ansi.Truncate(row, width, GlyphTruncate)
	}
	return row
}

// reportHeadline is the first non-blank line of a host-supplied report,
// sanitized for a single terminal row.
func reportHeadline(s string) string {
	for _, line := range strings.Split(normalizeNewlines(s), "\n") {
		if line = strings.TrimSpace(sanitizeLine(line)); line != "" {
			return line
		}
	}
	return ""
}

// renderTasksBar is the bar as View stacks it, held to the rows the
// chrome budget gave it.
func (m *model) renderTasksBar(width int) string {
	return strings.Join(m.tasksBarLines(width, m.chrome.tasks), "\n")
}

// tasksBarWant is how many rows the bar would take with no budget
// pressure — what allocateChrome measures and rebudgetTasks compares.
// Every line is one row: tasksBarLines cuts each at width.
func (m *model) tasksBarWant(width int) int {
	return len(m.tasksBarLines(width, tasksBarMaxRows))
}

// rebudgetTasks re-runs the chrome allocation when a roster change
// moved the bar's height, or the status line's count re-wrapped the
// header. Same reasoning as rebudgetFooter: both are budgeted chrome,
// and drawing them into the old reservation would let clipFrame take
// rows off the bottom of the frame.
func (m *model) rebudgetTasks() {
	if m.width == 0 || m.height == 0 || m.chrome.footer == 0 {
		return
	}
	cw := m.chromeWidth()
	headerMoved := m.effectiveLayout() == StatusHeader &&
		lipgloss.Height(m.renderHeader()) != m.chrome.header
	if !headerMoved && m.tasksBarWant(cw) == m.chrome.tasksWant {
		return
	}
	m.resize()
	m.refreshViewport()
}
