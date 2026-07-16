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

// Package testagent provides in-process stand-ins for a real
// tui.Agent. Two variants:
//
//   - New() returns an idle agent — Run yields no events. Useful for
//     pure-render smoke tests.
//   - NewScripted(events) returns an agent that plays back a fixed
//     sequence of (delay, Event) entries on Run, regardless of the
//     prompt. Useful for visual-preview demos and golden-render
//     tests; consumers can drop in any tui.Event sequence to
//     exercise specific renderer paths.
package testagent

import (
	"context"
	"iter"
	"time"

	"github.com/go-steer/core-tui/tui"
)

// New returns an Agent that produces no events. The TUI renders its
// idle state.
func New() tui.Agent { return idle{} }

type idle struct{}

func (idle) Run(_ context.Context, _ string) iter.Seq2[tui.Event, error] {
	return func(_ func(tui.Event, error) bool) {}
}

// Step is one entry in a scripted playback: how long to wait BEFORE
// emitting Event. Zero Wait emits immediately. Cancellation of the
// turn context aborts the playback.
type Step struct {
	Wait  time.Duration
	Event tui.Event
}

// NewScripted returns an Agent whose Run plays back the given steps
// in order. Same script for every prompt. The Agent honors context
// cancellation between steps.
func NewScripted(script []Step) tui.Agent {
	return scripted{script: script}
}

type scripted struct {
	script []Step
}

func (s scripted) Run(ctx context.Context, _ string) iter.Seq2[tui.Event, error] {
	return func(yield func(tui.Event, error) bool) {
		for _, step := range s.script {
			if step.Wait > 0 {
				select {
				case <-time.After(step.Wait):
				case <-ctx.Done():
					return
				}
			}
			if ctx.Err() != nil {
				return
			}
			if !yield(step.Event, nil) {
				return
			}
		}
	}
}

// CodingDemo returns a believable multi-step coding interaction the
// visual-preview binary plays on each submit. Exercises every
// renderer path (streaming text chunks, tool calls, usage, turn-end
// metadata) at a leisurely cadence — slow enough that the operator
// can comfortably type ahead to queue follow-up prompts (R-CHAT-10)
// during the turn and watch the queue panel grow.
//
// Total wall-clock runtime: ~14 seconds.
func CodingDemo() []Step {
	chunk := 280 * time.Millisecond
	return []Step{
		{Wait: 500 * time.Millisecond, Event: tui.Event{Text: "Looking at the existing schema first ", Partial: true}},
		{Wait: chunk, Event: tui.Event{Text: "to confirm the current column definition.\n\n", Partial: true}},
		{Wait: 1500 * time.Millisecond, Event: tui.Event{ToolCalls: []tui.ToolCall{
			{ID: "t1", Name: "Read", Args: map[string]any{"path": "db/schema/users.sql"}},
		}}},
		// Result for t1 arrives shortly after the call — a realistic
		// MCP round-trip. Populated so the expand-single detail
		// overlay (core-tui #52 tier 1) and the ToolDetailVerbose
		// inline mode (tier 2) both show real content when driven
		// against this demo.
		{Wait: 400 * time.Millisecond, Event: tui.Event{ToolResults: []tui.ToolResult{
			{ID: "t1", Name: "Read", LatencyMs: 320, Response: map[string]any{
				"content": "CREATE TABLE users (\n  id BIGSERIAL PRIMARY KEY,\n  email VARCHAR(255),\n  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()\n);\n",
			}},
		}}},
		{Wait: 1400 * time.Millisecond, Event: tui.Event{Text: "The `email` column is currently `VARCHAR(255)` ", Partial: true}},
		{Wait: chunk, Event: tui.Event{Text: "with no constraint. I'll add a migration that ", Partial: true}},
		{Wait: chunk, Event: tui.Event{Text: "backfills NULLs to empty strings and then adds NOT NULL.\n\n", Partial: true}},
		{Wait: 1200 * time.Millisecond, Event: tui.Event{ToolCalls: []tui.ToolCall{
			{ID: "t2", Name: "Write", Args: map[string]any{"path": "db/migrations/0042_users_email_not_null.sql"}},
		}}},
		{Wait: 300 * time.Millisecond, Event: tui.Event{ToolResults: []tui.ToolResult{
			{ID: "t2", Name: "Write", LatencyMs: 180, Response: map[string]any{
				"path":          "db/migrations/0042_users_email_not_null.sql",
				"bytes_written": 512,
				"lines_written": 12,
			}},
		}}},
		{Wait: 1300 * time.Millisecond, Event: tui.Event{ToolCalls: []tui.ToolCall{
			{ID: "t3", Name: "Bash", Args: map[string]any{"command": "psql -f db/migrations/0042_users_email_not_null.sql"}},
		}}},
		{Wait: 500 * time.Millisecond, Event: tui.Event{ToolResults: []tui.ToolResult{
			{ID: "t3", Name: "Bash", LatencyMs: 2400, Response: map[string]any{
				"stdout":    "BEGIN\nUPDATE 0\nALTER TABLE\nCOMMIT\n",
				"exit_code": 0,
			}},
		}}},
		{Wait: 1500 * time.Millisecond, Event: tui.Event{Text: "Done. The migration is at ", Partial: true}},
		{Wait: chunk, Event: tui.Event{Text: "`db/migrations/0042_users_email_not_null.sql` ", Partial: true}},
		{Wait: chunk, Event: tui.Event{Text: "and verifies cleanly against dev. ", Partial: true}},
		{Wait: chunk, Event: tui.Event{Text: "Want me to also write the matching down-migration?", Partial: true}},
		{Wait: 400 * time.Millisecond, Event: tui.Event{Usage: &tui.Usage{InputTokens: 8421, OutputTokens: 2103}}},
	}
}
