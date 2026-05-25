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

import "time"

// Internal tea.Msg types that carry events from the agent dispatch
// goroutine back into the Bubble Tea Update loop. The translation
// happens in agentcmd.go (startAgentTurn).
//
// Splitting Event into per-kind Msgs keeps Update's switch readable
// — each branch handles one concern (text accumulation, tool call
// dedup, usage snapshot, turn lifecycle).

// streamChunkMsg carries one Text event from the agent. Partial is
// true while the model is still streaming; false on the committed
// full-text event some agents emit at turn-end.
type streamChunkMsg struct {
	text    string
	partial bool
}

// toolCallMsg carries one ToolCall event. ID enables dedup against
// partial+committed echoes (R-CHAT-5).
type toolCallMsg struct {
	id   string
	name string
	args map[string]any
}

// usageMsg snapshots the latest Usage from the agent. The TUI keeps
// only the most recent value and reports it once at turn-end on the
// finalized assistant message (R-USE-1).
type usageMsg struct {
	usage Usage
}

// turnDoneMsg signals clean turn completion. Populated with the
// elapsed wall-clock time so the per-turn footer can render it.
type turnDoneMsg struct {
	elapsed time.Duration
}

// turnErrMsg signals turn failure. The error is rendered as an Error
// row in the chat; the TUI stays interactive (no auto-quit per §4.2).
type turnErrMsg struct {
	err error
}

// turnCancelledMsg signals Esc-interrupt (R-CHAT-6). The TUI emits an
// "(interrupted)" notice instead of an error banner.
type turnCancelledMsg struct{}

// spinnerTickMsg fires every spinnerCadence to rotate the
// thinking/working verb (R-CHAT-3).
type spinnerTickMsg struct{}

// wakeMsg fires when the host's WakeRequester capability signals
// the operator should be notified (R-WAKE-1). Carries no payload —
// the toast banner content is fixed; subsequent design slices can
// extend with a Reason field if hosts want per-wake messages.
type wakeMsg struct{}

// toastClearMsg fires toastTTL after a toast was raised; Update
// drops the toast on receive unless a fresher wake has restarted
// the timer (R-WAKE-1).
type toastClearMsg struct{}
