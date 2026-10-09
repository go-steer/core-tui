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
	"time"

	"github.com/charmbracelet/x/ansi"
)

// Scheduled-wake rows on the running-tasks bar. The decision numbers
// below are docs/scheduled-wakes-design.md's.

// A subagent asleep until a scheduled wake counts down to it, and the
// reason it gave for sleeping takes the report column (decisions 3, 5).
func TestTasksBar_ScheduledRow(t *testing.T) {
	m, now := tasksModel(t, StatusHeader, 120, 30)
	m = landRoster(t, m, SubagentInfo{
		Name: "cluster-watch", Status: "running", StartedAt: tasksEpoch,
		NextWakeAt: tasksEpoch.Add(10 * time.Minute),
		WakeDetail: "polling cluster-A on 10m cadence",
		LastReport: "0 unhealthy pods",
	})
	*now = tasksEpoch.Add(108 * time.Second)

	rows := barText(m)
	want := glyphScheduled + " cluster-watch " + GlyphSeparator + " wakes in 8m12s " + GlyphSeparator +
		" polling cluster-A on 10m cadence"
	if len(rows) != 1 || !strings.Contains(rows[0], want) {
		t.Fatalf("bar = %q, want a row containing %q", rows, want)
	}
}

func TestTasksBar_ScheduledFallsBackToReport(t *testing.T) {
	m, _ := tasksModel(t, StatusHeader, 120, 30)
	m = landRoster(t, m, SubagentInfo{
		Name: "w", Status: "running", NextWakeAt: tasksEpoch.Add(time.Minute), LastReport: "last poll clean",
	})
	if rows := barText(m); len(rows) != 1 || !strings.Contains(rows[0], "last poll clean") {
		t.Errorf("bar = %q, want LastReport when there is no WakeDetail", rows)
	}
}

// Decision 4: a wake that has fired but not yet left the polled roster
// reads "waking", never a negative or a frozen countdown.
func TestTasksBar_ScheduledPastDue(t *testing.T) {
	m, now := tasksModel(t, StatusHeader, 120, 30)
	m = landRoster(t, m, SubagentInfo{Name: "w", Status: "running", NextWakeAt: tasksEpoch.Add(5 * time.Second)})
	for _, at := range []time.Duration{4500 * time.Millisecond, 5 * time.Second, 9 * time.Second} {
		*now = tasksEpoch.Add(at)
		rows := barText(m)
		if len(rows) != 1 || !strings.Contains(rows[0], "w "+GlyphSeparator+" waking") {
			t.Fatalf("at +%v: bar = %q, want waking", at, rows)
		}
		if strings.Contains(rows[0], "-") || strings.Contains(rows[0], "in 0s") {
			t.Errorf("at +%v: bar = %q draws a negative or frozen countdown", at, rows)
		}
	}
}

// When the wake fires the host clears NextWakeAt and the row goes back
// to counting up from the start.
func TestTasksBar_WakeFiresBackToRunning(t *testing.T) {
	m, now := tasksModel(t, StatusHeader, 120, 30)
	m = landRoster(t, m, SubagentInfo{Name: "w", Status: "running", StartedAt: tasksEpoch,
		NextWakeAt: tasksEpoch.Add(30 * time.Second)})
	*now = tasksEpoch.Add(31 * time.Second)
	m = landRoster(t, m, SubagentInfo{Name: "w", Status: "running", StartedAt: tasksEpoch})
	if rows := barText(m); len(rows) != 1 || !strings.Contains(rows[0], GlyphToolActive+" w "+GlyphSeparator+" 31s") {
		t.Errorf("bar = %q, want the running row back", rows)
	}
}

// A paused subagent shows as paused whatever it had scheduled, and a
// finished one never shows a wake.
func TestTasksBar_ScheduledOnlyWhileRunning(t *testing.T) {
	m, now := tasksModel(t, StatusHeader, 120, 30)
	wake := tasksEpoch.Add(time.Hour)
	m = landRoster(t, m,
		SubagentInfo{Name: "held", Status: "paused", NextWakeAt: wake},
		SubagentInfo{Name: "ending", Status: "running", NextWakeAt: wake},
	)
	*now = tasksEpoch.Add(time.Second)
	m = landRoster(t, m,
		SubagentInfo{Name: "held", Status: "paused", NextWakeAt: wake},
		SubagentInfo{Name: "ending", Status: "done", NextWakeAt: wake},
	)
	joined := strings.Join(barText(m), "\n")
	if strings.Contains(joined, glyphScheduled) || strings.Contains(joined, "wakes in") {
		t.Errorf("bar = %q, want no scheduled row for a paused or finished subagent", joined)
	}
	if !strings.Contains(joined, GlyphPaused+" held") || !strings.Contains(joined, GlyphToolDone+" ending") {
		t.Errorf("bar = %q, want the paused and done rows", joined)
	}
}

// Decision 6: the count keeps working and sleeping apart, and the
// status line shows it.
func TestTasksBar_CountSplitsScheduled(t *testing.T) {
	wake := tasksEpoch.Add(time.Minute)
	for _, tc := range []struct {
		name string
		subs []SubagentInfo
		want string
	}{
		{"none", nil, ""},
		{"one running", []SubagentInfo{{Name: "a", Status: "running"}}, "1 subagent running"},
		{"one scheduled", []SubagentInfo{{Name: "a", Status: "running", NextWakeAt: wake}}, "1 subagent scheduled"},
		{"two scheduled", []SubagentInfo{
			{Name: "a", Status: "running", NextWakeAt: wake},
			{Name: "b", Status: "running", NextWakeAt: wake},
		}, "2 subagents scheduled"},
		{"mixed", []SubagentInfo{
			{Name: "a", Status: "running"},
			{Name: "b", Status: "running"},
			{Name: "c", Status: "running", NextWakeAt: wake},
			{Name: "d", Status: "paused"},
		}, "2 subagents running " + GlyphSeparator + " 1 scheduled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := tasksModel(t, StatusHeader, 200, 30)
			m = landRoster(t, m, tc.subs...)
			if got := m.subagentsCountLabel(); got != tc.want {
				t.Errorf("count = %q, want %q", got, tc.want)
			}
			if tc.want != "" && !strings.Contains(barHeader(m), tc.want) {
				t.Errorf("header = %q, want it to carry %q", barHeader(m), tc.want)
			}
		})
	}
}

// The status-line cache has to notice a subagent falling asleep even
// though the roster's names and statuses did not change.
func TestTasksBar_StatusCacheSeesSleep(t *testing.T) {
	m, now := tasksModel(t, StatusHeader, 200, 30)
	m = landRoster(t, m, SubagentInfo{Name: "a", Status: "running"})
	_ = m.renderHeader()
	*now = tasksEpoch.Add(time.Second)
	m = landRoster(t, m, SubagentInfo{Name: "a", Status: "running", NextWakeAt: tasksEpoch.Add(time.Minute)})
	if got, want := m.renderHeader(), uncachedHeader(m); got != want {
		t.Errorf("memoized header went stale on sleep\n memoized: %q\n    fresh: %q", got, want)
	}
}

// barHeader is the status header as plain text with its word-wrap
// undone, so a count split across rows still matches.
func barHeader(m *model) string {
	return strings.Join(strings.Fields(ansi.Strip(m.renderHeader())), " ")
}
