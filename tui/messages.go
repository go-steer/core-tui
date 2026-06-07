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

// toolResultMsg carries one ToolResult event. ID correlates with
// the originating toolCallMsg.id; Update looks up the Message
// whose ToolCallID matches and updates its preview with the
// rendered result. Adapters that don't surface tool results never
// emit this — the TUI keeps the call-only preview unchanged.
type toolResultMsg struct {
	id       string
	name     string
	response map[string]any
	err      string
}

// usageMsg snapshots the latest Usage from the agent. The TUI keeps
// only the most recent value and reports it once at turn-end on the
// finalized assistant message (R-USE-1). Cost / Model travel
// alongside so adapters that compute pricing per turn can surface
// it without an extra round-trip; zero values suppress the
// respective footer/sidebar segments.
type usageMsg struct {
	usage   Usage
	costUSD float64
	model   string
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

// pendingExitClearMsg fires ctrlCExitTTL after the first Ctrl+C
// idle press so the "press again to exit" warning doesn't latch
// forever — if no follow-up arrives the warning quietly disarms.
type pendingExitClearMsg struct{}

// permissionRequestMsg fires when the prompter's request channel
// surfaces an inbound PermissionRequest (R-PERM-1). Update sets
// the modal-pending state; the modal's key handler dispatches the
// decision back via Prompter.dispatchDecision.
type permissionRequestMsg struct {
	req PermissionRequest
}

// elicitRequestMsg fires when the elicitor's request channel
// surfaces an inbound ElicitRequest (R-ELIC-1). Update sets the
// elicit-pending state; the form's key handler dispatches the
// result back via elicitor.dispatchResult.
type elicitRequestMsg struct {
	serverName string
	req        ElicitRequest
}

// slashResultMsg carries the eventual outcome of an
// AsyncSlashProvider.InvokeSlashAsync call (issue #10). Posted
// onto m.eventCh by a goroutine the dispatcher spawns, then
// dispatched by Update like any other event so the modal /
// system message / error path stays consistent with the
// synchronous case.
type slashResultMsg struct {
	name string
	res  SlashResult
	err  error
}

// liveStreamStartedMsg fires once at startup after the LiveAgent
// drain goroutine launches; carries the cancel func so the
// Update handler can stash it on the model's cancelLiveStream
// field (Init has a value receiver and can't mutate). Also
// triggers the one-time "Attached as observer" system row so the
// operator knows they're in LiveAgent mode.
type liveStreamStartedMsg struct {
	cancel func()
}

// liveStreamErrMsg carries a non-nil error yielded by a
// LiveAgent.Events iterator (issue #22). The drain goroutine
// surfaces it as a RoleError row and keeps draining — the
// iterator decides whether to keep yielding events.
type liveStreamErrMsg struct {
	err error
}

// liveStreamEndedMsg fires when a LiveAgent.Events iterator
// returns / stops yielding (issue #22). core-tui renders a
// "Disconnected — Ctrl+C to quit" system row and keeps the
// program alive so the operator can read scrollback. No
// auto-reconnect; the LiveAgent implementation owns that.
type liveStreamEndedMsg struct{}

// forceRenderMsg is a no-op msg used to force a fresh Update →
// View cycle (issue #24). Bubble-tea v2 occasionally defers the
// next paint when an Update returns (m, nil) in a "quiet window"
// — no other Cmds in flight, no inbound keypresses, no spinner
// ticks. Listener handlers that need to surface a modal in that
// quiet window (permission prompt arriving from a remote bridge,
// elicit request landing between turns, the live-stream
// disconnect banner) return a forceRenderTick alongside their
// state mutation so a forceRenderMsg arrives ~1ms later and
// guarantees the paint. The handler for this msg is a deliberate
// no-op + nil Cmd; the value is in the fact that it WAS
// processed.
type forceRenderMsg struct{}

// noticeMsg carries one host-initiated notice from the
// Options.Notifier channel through to the Update loop. Internal
// type — hosts push via Notifier.Notify(text), they don't
// construct this directly.
type noticeMsg struct {
	text    string
	dropped int // coalesced drop count; appended to rendered text as "(+N dropped)"
}

// ThemeChangedMsg is emitted by the /theme picker (and `/theme
// <name>` with a known name) when the operator commits a new
// theme. Hosts have two equivalent ways to persist:
//
//   - Set Options.PersistThemeChoice — a callback the picker
//     invokes inline (mirrors PersistModelChoice). Less host
//     code; no Update-loop intercept needed.
//   - Observe ThemeChangedMsg in the host's Update loop. Useful
//     when the host already has a custom Update wrapper or
//     wants to react to theme changes beyond persistence (e.g.
//     emit telemetry).
//
// Both fire on every committed change — pick one or both,
// whichever fits the host's architecture. On next launch, hosts
// seed the persisted name via Options.InitialThemeName.
//
// Exported (capital M) because it crosses the package boundary
// — unlike most msgs in this file, which are tui-internal.
type ThemeChangedMsg struct{ Name string }
