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

// Command notifier-smoke is a focused smoke-test binary for the
// out-of-band Notifier capability (issue #30 / PR #39). It boots a
// minimal TUI shell and spawns a producer goroutine that pushes
// realistic-looking notices on a rotation so the operator can
// visually verify:
//
//   - RoleNotice renders with the ◇ glyph + muted color (distinct
//     from RoleSystem's ℹ + italic)
//   - notices interleave with whatever else is in the chat in
//     arrival-time order
//   - the (+N dropped) coalescence marker appears when a burst
//     overflows the buffered channel (the rotation periodically
//     fires a 25-msg burst to exercise this path)
//   - notices stop cleanly on quit (no panic from a host goroutine
//     racing the TUI's shutdown)
//
// Cadence: a single realistic notice every ~7s, plus a 25-msg
// burst every ~50s. Quit with ctrl+c or ctrl+d. Type anything to
// drive a streaming turn — notices will interleave during streams
// too, which is what the kick logic in update.go's noticeMsg case
// guarantees.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/go-steer/core-tui/tui"
	"github.com/go-steer/core-tui/tui/testagent"
)

func main() {
	notifier := tui.NewNotifier()

	// Producer goroutine. Runs until the process exits — Notifier's
	// closed-flag guard makes Notify a no-op once the TUI's exit
	// defer in program.go calls Notifier.close(), so we don't have
	// to wire cancellation here.
	go produceNotices(notifier)

	opts := tui.Options{
		Agent:        testagent.NewScripted(testagent.CodingDemo()),
		Notifier:     notifier,
		StatusLayout: tui.StatusHeader,
		SeedHistory: []tui.Message{{
			Role: tui.RoleSystem,
			Text: "notifier-smoke — watch the chat. A producer goroutine pushes notices " +
				"on a rotation: one realistic notice every ~7s, plus a 25-msg burst every " +
				"~50s to exercise the (+N dropped) coalescence marker. Notices render with " +
				"the ◇ glyph in muted color, distinct from this system ℹ + italic. Quit with " +
				"ctrl+c.",
		}},
	}
	if err := tui.Run(context.Background(), opts); err != nil {
		fmt.Fprintln(os.Stderr, "notifier-smoke:", err)
		os.Exit(1)
	}
}

// produceNotices rotates through a script of realistic notices,
// pacing them so the operator sees a steady stream. Every Nth tick
// it fires a burst that overflows the buffer to demonstrate the
// drop-with-coalescence path. Runs forever — the process exits
// when the user quits the TUI; the Notifier's closed flag turns
// further Notify calls into silent drops.
func produceNotices(n *tui.Notifier) {
	// Realistic notice script — same use cases the issue #30
	// design proposal enumerated.
	script := []string{
		"Daemon reconnected after 2s outage. Session state preserved.",
		"operator-2 (alice@example.com) joined this session.",
		"Local network dropped 3 RPC frames in the last 60s — UI may briefly lag.",
		"Daemon version mismatch: core-tui v0.7.0 expects v0.8+. Some features may be unavailable.",
		"Background subagent 'security-review' finished — see /subagents for the report.",
		"Auth token will expire in 5 minutes. /reload to refresh.",
		"Telemetry: this session is being sampled for performance analysis.",
		"operator-2 detached.",
		"Daemon restarting in 60s for routine maintenance. Reconnect will be automatic.",
		"Disk usage on /var/log exceeded 80%. Older transcripts will be rotated.",
	}

	// Initial delay so the operator has a moment to read the seed
	// message before notices start pouring in.
	time.Sleep(3 * time.Second)

	tick := time.NewTicker(7 * time.Second)
	defer tick.Stop()

	burstTick := time.NewTicker(50 * time.Second)
	defer burstTick.Stop()

	i := 0
	for {
		select {
		case <-tick.C:
			n.Notify(script[i%len(script)])
			i++
		case <-burstTick.C:
			// 25 in rapid succession — buffer is size 16, so ~9
			// will land in the (+N dropped) coalesced marker on
			// the next successful enqueue.
			for j := 0; j < 25; j++ {
				n.Notify(fmt.Sprintf("burst-notice-%d (flooding to exercise drop-with-coalescence)", j))
			}
		}
	}
}
