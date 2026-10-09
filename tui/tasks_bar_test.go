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

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

var tasksEpoch = time.Date(2026, 10, 8, 18, 0, 0, 0, time.UTC)

// tasksModel is a sized header-layout model on a settable clock.
func tasksModel(t *testing.T, layout StatusLayout, w, h int) (*model, *time.Time) {
	t.Helper()
	m := newModel(Options{Agent: &bareAgent{id: "tasks"}, StatusLayout: layout})
	m.styles = newStylesWithTheme(true, goldenTheme())
	now := tasksEpoch
	m.now = func() time.Time { return now }
	out, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return out.(*model), &now
}

// landRoster delivers one host snapshot carrying subs, the way the
// off-loop refresh does.
func landRoster(t *testing.T, m *model, subs ...SubagentInfo) *model {
	t.Helper()
	out, _ := m.Update(hostSnapshotMsg{gen: m.sessionGen, snap: hostSnapshot{
		valid: true, hasSubagents: true, subagents: subs,
	}})
	return out.(*model)
}

func barText(m *model) []string {
	bar := m.renderTasksBar(m.chromeWidth())
	if bar == "" {
		return nil
	}
	return strings.Split(ansi.Strip(bar), "\n")
}

func TestTasksBar_RunningRow(t *testing.T) {
	m, now := tasksModel(t, StatusHeader, 120, 30)
	m = landRoster(t, m, SubagentInfo{
		Name: "reviewer", Status: "running", StartedAt: tasksEpoch,
		LastReport: "\n  Load and pin actions were examined\nsecond line",
	})
	*now = tasksEpoch.Add(65 * time.Second)

	rows := barText(m)
	if len(rows) != 1 {
		t.Fatalf("bar rows = %q, want one", rows)
	}
	want := GlyphToolActive + " reviewer " + GlyphSeparator + " 1m05s " + GlyphSeparator +
		" Load and pin actions were examined"
	if !strings.Contains(rows[0], want) {
		t.Errorf("row = %q, want it to contain %q (the report's first non-blank line, never the rest)",
			rows[0], want)
	}
	if strings.Contains(rows[0], "second line") {
		t.Errorf("row = %q carries a report line past the first", rows[0])
	}
}

func TestTasksBar_HiddenWhenIdle(t *testing.T) {
	m, _ := tasksModel(t, StatusHeader, 100, 30)
	before := m.chrome.chat
	m = landRoster(t, m)
	if got := m.renderTasksBar(m.chromeWidth()); got != "" {
		t.Errorf("idle bar = %q, want nothing", got)
	}
	if m.chrome.tasks != 0 || m.chrome.chat != before {
		t.Errorf("idle bar took rows: tasks=%d chat %d→%d", m.chrome.tasks, before, m.chrome.chat)
	}
}

func TestTasksBar_FinishedLingersThenGoes(t *testing.T) {
	for _, tc := range []struct {
		status, glyph string
	}{
		{"done", GlyphToolDone},
		{"failed", GlyphToolFail},
	} {
		t.Run(tc.status, func(t *testing.T) {
			m, now := tasksModel(t, StatusHeader, 100, 30)
			m = landRoster(t, m, SubagentInfo{Name: "probe", Status: "running", StartedAt: tasksEpoch})

			*now = tasksEpoch.Add(time.Second)
			m = landRoster(t, m, SubagentInfo{Name: "probe", Status: tc.status, LastReport: "all clear"})
			rows := barText(m)
			if len(rows) != 1 || !strings.Contains(rows[0], tc.glyph+" probe "+GlyphSeparator+" "+tc.status) {
				t.Fatalf("just-finished bar = %q, want the %s row with its outcome", rows, tc.status)
			}

			*now = tasksEpoch.Add(time.Second + tasksLinger - time.Millisecond)
			m = landRoster(t, m, SubagentInfo{Name: "probe", Status: tc.status})
			if len(barText(m)) != 1 {
				t.Fatalf("row gone before tasksLinger elapsed")
			}

			*now = tasksEpoch.Add(time.Second + tasksLinger)
			m = landRoster(t, m, SubagentInfo{Name: "probe", Status: tc.status})
			if rows := barText(m); rows != nil {
				t.Errorf("bar after tasksLinger = %q, want empty", rows)
			}
			if m.chrome.tasks != 0 {
				t.Errorf("expired row still budgeted: chrome.tasks = %d", m.chrome.tasks)
			}
		})
	}
}

// A roster read on attach can carry any amount of history. Only a
// transition the bar watched is news.
func TestTasksBar_FinishedOnFirstSightIsNotShown(t *testing.T) {
	m, _ := tasksModel(t, StatusHeader, 100, 30)
	m = landRoster(t, m,
		SubagentInfo{Name: "old-1", Status: "done"},
		SubagentInfo{Name: "old-2", Status: "failed"},
	)
	if rows := barText(m); rows != nil {
		t.Errorf("bar = %q, want nothing for subagents that were never seen running", rows)
	}
}

func TestTasksBar_VanishedMidRunIsDropped(t *testing.T) {
	m, now := tasksModel(t, StatusHeader, 100, 30)
	m = landRoster(t, m, SubagentInfo{Name: "probe", Status: "running"})
	*now = tasksEpoch.Add(time.Second)
	m = landRoster(t, m)
	if rows := barText(m); rows != nil {
		t.Errorf("bar = %q, want nothing — the roster never said how it ended", rows)
	}
}

func TestTasksBar_RestartReplacesLinger(t *testing.T) {
	m, now := tasksModel(t, StatusHeader, 100, 30)
	m = landRoster(t, m, SubagentInfo{Name: "probe", Status: "running"})
	*now = tasksEpoch.Add(time.Second)
	m = landRoster(t, m, SubagentInfo{Name: "probe", Status: "done"})
	*now = tasksEpoch.Add(2 * time.Second)
	m = landRoster(t, m, SubagentInfo{Name: "probe", Status: "running"})
	rows := barText(m)
	if len(rows) != 1 || !strings.Contains(rows[0], GlyphToolActive+" probe") {
		t.Errorf("bar = %q, want one running row and no stale done row beside it", rows)
	}
}

func TestTasksBar_PausedRow(t *testing.T) {
	m, _ := tasksModel(t, StatusHeader, 100, 30)
	m = landRoster(t, m, SubagentInfo{Name: "held", Status: "paused", StartedAt: tasksEpoch})
	rows := barText(m)
	if len(rows) != 1 || !strings.Contains(rows[0], GlyphPaused+" held "+GlyphSeparator+" paused") {
		t.Errorf("bar = %q, want the paused row", rows)
	}
	// Paused is in flight but not running.
	if got := m.subagentsCountLabel(); got != "" {
		t.Errorf("running label = %q with only a paused subagent, want empty", got)
	}
}

func TestTasksBar_OrdersByStart(t *testing.T) {
	m, _ := tasksModel(t, StatusHeader, 100, 30)
	m = landRoster(t, m,
		SubagentInfo{Name: "second", Status: "running", StartedAt: tasksEpoch.Add(time.Minute)},
		SubagentInfo{Name: "first", Status: "running", StartedAt: tasksEpoch},
	)
	rows := barText(m)
	if len(rows) != 2 || !strings.Contains(rows[0], "first") || !strings.Contains(rows[1], "second") {
		t.Errorf("bar = %q, want oldest first", rows)
	}
}

func TestTasksBar_CapsWithMoreRow(t *testing.T) {
	m, _ := tasksModel(t, StatusHeader, 100, 30)
	var subs []SubagentInfo
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		subs = append(subs, SubagentInfo{Name: n, Status: "running"})
	}
	m = landRoster(t, m, subs...)
	rows := barText(m)
	if len(rows) != tasksBarMaxRows {
		t.Fatalf("bar = %q, want %d rows", rows, tasksBarMaxRows)
	}
	if want := "+ 3 more " + GlyphSeparator + " /subagents"; !strings.Contains(rows[2], want) {
		t.Errorf("last row = %q, want %q", rows[2], want)
	}

	// Squeezed to one row, the bar is a bare count.
	one := m.tasksBarLines(100, 1)
	if len(one) != 1 || !strings.Contains(ansi.Strip(one[0]), "5 subagents "+GlyphSeparator+" /subagents") {
		t.Errorf("one-row bar = %q, want the count", one)
	}
}

func TestTasksBar_RowsHonorWidth(t *testing.T) {
	m, _ := tasksModel(t, StatusHeader, 40, 30)
	m = landRoster(t, m, SubagentInfo{
		Name: strings.Repeat("n", 80), Status: "running", LastReport: strings.Repeat("r ", 80),
	})
	for _, line := range strings.Split(m.renderTasksBar(40), "\n") {
		if w := ansi.StringWidth(line); w > 40 {
			t.Errorf("row is %d cols at width 40: %q", w, ansi.Strip(line))
		}
	}
	if rows := barText(m); !strings.HasSuffix(rows[0], GlyphTruncate) {
		t.Errorf("cut row = %q, want it to end in %s so the cut reads as one", rows[0], GlyphTruncate)
	}
}

// The bar sits between the input box and the footer, and the frame
// stays exactly the terminal's height as it appears and goes.
func TestTasksBar_FrameLayout(t *testing.T) {
	for _, layout := range []StatusLayout{StatusHeader, StatusSidebar} {
		m, now := tasksModel(t, layout, 120, 30)
		m = landRoster(t, m, SubagentInfo{Name: "probe", Status: "running", LastReport: "probing"})

		frame := strings.Split(ansi.Strip(m.View().Content), "\n")
		if len(frame) != 30 {
			t.Fatalf("layout %v: frame is %d rows, want 30", layout, len(frame))
		}
		bar, footer := -1, -1
		for i, row := range frame {
			if strings.Contains(row, GlyphToolActive+" probe") {
				bar = i
			}
			if strings.Contains(row, "? for more") {
				footer = i
			}
		}
		if bar < 0 || footer < 0 || bar >= footer {
			t.Fatalf("layout %v: bar row %d, footer row %d — want the bar above the footer:\n%s",
				layout, bar, footer, strings.Join(frame, "\n"))
		}

		*now = tasksEpoch.Add(time.Second)
		m = landRoster(t, m)
		frame = strings.Split(ansi.Strip(m.View().Content), "\n")
		if len(frame) != 30 {
			t.Errorf("layout %v: after the bar went the frame is %d rows, want 30", layout, len(frame))
		}
		if strings.Contains(strings.Join(frame, "\n"), "probe") {
			t.Errorf("layout %v: bar still drawn after the roster emptied", layout)
		}
	}
}

// On a terminal with nothing to spare the bar yields entirely rather
// than pushing the footer out through clipFrame. The status line's
// count still says what it would have.
//
// Swept at 200 columns so the status line stays one row: at narrower
// widths the count can wrap the header, which is the header's own
// (unshrinkable) growth and not the bar's to yield.
func TestTasksBar_YieldsOnShortTerminal(t *testing.T) {
	squeezed := false
	for h := 4; h <= 20; h++ {
		idle, _ := tasksModel(t, StatusHeader, 200, h)
		if idle.chrome.overflow != 0 {
			// Too short for the irreducible chrome already; nothing
			// the bar does can make that worse or better.
			continue
		}
		m, _ := tasksModel(t, StatusHeader, 200, h)
		m = landRoster(t, m,
			SubagentInfo{Name: "a", Status: "running"},
			SubagentInfo{Name: "b", Status: "running"},
			SubagentInfo{Name: "c", Status: "running"},
		)
		if m.chrome.overflow != 0 {
			t.Errorf("200x%d: the bar caused overflow %d", h, m.chrome.overflow)
		}
		frame := ansi.Strip(m.View().Content)
		if got := len(strings.Split(frame, "\n")); got != h {
			t.Errorf("200x%d: frame is %d rows", h, got)
		}
		if !strings.Contains(frame, "3 subagents running") {
			t.Errorf("200x%d: frame lost the running count:\n%s", h, frame)
		}
		if m.chrome.tasks < m.chrome.tasksWant {
			squeezed = true
		}
	}
	if !squeezed {
		t.Errorf("no height in the sweep squeezed the bar, so the yield path went untested")
	}
}

func TestTasksBar_StatusLineCount(t *testing.T) {
	m, _ := tasksModel(t, StatusHeader, 160, 30)
	m = landRoster(t, m,
		SubagentInfo{Name: "a", Status: "running"},
		SubagentInfo{Name: "b", Status: "running"},
	)
	if got := ansi.Strip(m.renderHeader()); !strings.Contains(got, "2 subagents running") {
		t.Errorf("header = %q, want the plural count", got)
	}
}

func TestTasksBar_SessionSwitchClears(t *testing.T) {
	m, _ := tasksModel(t, StatusHeader, 100, 30)
	m = landRoster(t, m, SubagentInfo{Name: "probe", Status: "running"})
	m.applySwitchTarget(&SwitchTarget{Agent: &bareAgent{id: "next"}})
	if len(m.tasks.active) != 0 || m.tasks.running != nil {
		t.Errorf("tasks = %+v after a switch, want the outgoing session's roster gone", m.tasks)
	}
	if rows := barText(m); rows != nil {
		t.Errorf("bar = %q after a switch, want empty", rows)
	}
}
