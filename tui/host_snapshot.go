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
	"time"

	tea "charm.land/bubbletea/v2"
)

// hostSnapshot caches the host's StatusReporter + UsageTracker +
// SubagentReporter reads so the render path never calls the host from
// inside View().
//
// Why this matters: bubble-tea runs Update and View on a single event-
// loop goroutine. The status header pulls the model name, provider, and
// usage figures on every paint, and the sidebar pulls the subagent
// roster; if any of those host methods blocks (a slow or wedged remote
// daemon, say), View() blocks, the event loop freezes, and the whole TUI
// locks up — keys and Ctrl+C included, since those arrive as messages the
// frozen loop can't read. Reading from this cache instead keeps View()
// pure and non-blocking regardless of how the host implements its
// capability methods.
//
// The cache is refreshed off the event loop by refreshHostSnapshotCmd on
// a periodic tick (see hostSnapshotInterval). Push-mode fields the Update
// handlers already maintain (currentModel, pushedProvider) still overlay
// this as a fresher source where available.
type hostSnapshot struct {
	valid     bool   // a refresh has completed at least once
	modelName string // StatusReporter.Status().ModelName
	provider  string // StatusReporter.Status().Provider
	hasUsage  bool   // a UsageTracker was wired, so the usage figures are meaningful
	totals    Usage  // UsageTracker.SessionTotals()
	cost      float64
	winUsed   int // UsageTracker.ContextWindowUsed()
	winSize   int // UsageTracker.ContextWindowSize()

	// hasSubagents reports that a SubagentReporter was wired, so an
	// empty subagents slice means "none running" rather than "no
	// capability". The sidebar renders a different row for each.
	hasSubagents bool
	subagents    []SubagentInfo // SubagentReporter.Subagents()
}

// hostSnapshotMsg carries a completed off-loop refresh back into Update.
// gen is the sessionGen captured when the refresh was issued so a switch
// mid-flight discards the stale snapshot (same pattern as the other
// generation-guarded messages).
type hostSnapshotMsg struct {
	gen  uint64
	snap hostSnapshot
}

// hostSnapshotTickMsg re-arms the refresh cycle. gen-guarded so a switch
// retires the outgoing session's cycle and applySwitchTarget starts a
// fresh one for the new generation.
type hostSnapshotTickMsg struct{ gen uint64 }

// hostSnapshotInterval bounds how often the render-path host cache is
// refreshed. Modest cadence: the header only needs to feel live, and
// each pull is either a cheap in-memory read (in-process hosts) or served
// from the remote adapter's own short-TTL cache. One refresh + one
// pending tick are ever in flight, so a slow host method parks a single
// background goroutine rather than piling up.
const hostSnapshotInterval = time.Second

// pullHostSnapshot reads the host capabilities once. Runs inside the
// refresh Cmd's goroutine (off the event loop), never from View(). Every
// argument is nil-safe: a host may implement none, some, or all.
func pullHostSnapshot(status StatusReporter, tracker UsageTracker, subs SubagentReporter) hostSnapshot {
	snap := hostSnapshot{valid: true}
	if status != nil {
		s := status.Status()
		snap.modelName = s.ModelName
		snap.provider = s.Provider
	}
	if tracker != nil {
		snap.hasUsage = true
		snap.totals = tracker.SessionTotals()
		snap.cost = tracker.SessionCostUSD()
		snap.winUsed = tracker.ContextWindowUsed()
		snap.winSize = tracker.ContextWindowSize()
	}
	if subs != nil {
		snap.hasSubagents = true
		snap.subagents = subs.Subagents()
	}
	return snap
}

// refreshHostSnapshotCmd builds the off-loop refresh Cmd, or nil when the
// host implements none of the cached capabilities (nothing to cache — the
// View helpers fall back to placeholders). The host interfaces and
// sessionGen are captured at construction so the closure doesn't touch the
// model from its goroutine.
func (m model) refreshHostSnapshotCmd() tea.Cmd {
	status, _ := m.opts.Agent.(StatusReporter)
	subs, _ := m.opts.Agent.(SubagentReporter)
	tracker := m.opts.UsageTracker
	if status == nil && tracker == nil && subs == nil {
		return nil
	}
	gen := m.sessionGen
	return func() tea.Msg {
		return hostSnapshotMsg{gen: gen, snap: pullHostSnapshot(status, tracker, subs)}
	}
}

// hostSnapshotTick schedules the next refresh, tagged with the issuing
// generation so a session switch retires it.
func hostSnapshotTick(gen uint64) tea.Cmd {
	return tea.Tick(hostSnapshotInterval, func(time.Time) tea.Msg {
		return hostSnapshotTickMsg{gen: gen}
	})
}
