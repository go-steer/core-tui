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

// Chat-content + terminal msgs carry a `gen` field stamped by the
// emitter (startAgentTurn / startLiveStream / emitEvent) with the
// Model's sessionGen at goroutine start. Update's cases guard
// `if msg.gen != m.sessionGen { drop }` so a mid-run applySwitchTarget
// (issue #48 / #53) invalidates every in-flight msg from the outgoing
// Agent without needing to swap m.eventCh or wait for cancellation
// to propagate.

// streamChunkMsg carries one Text event from the agent. Partial is
// true while the model is still streaming; false on the committed
// full-text event some agents emit at turn-end.
type streamChunkMsg struct {
	gen     uint64
	text    string
	partial bool
}

// toolCallMsg carries one ToolCall event. ID enables dedup against
// partial+committed echoes (R-CHAT-5).
type toolCallMsg struct {
	gen  uint64
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
	gen       uint64
	id        string
	name      string
	response  map[string]any
	err       string
	latencyMs int64
	// savings, when non-nil, carries the digest wrap's per-call
	// reduction — original vs. digested byte / token counts + router
	// decision + (agentic path only) subagent usage. Renderers pluck
	// it via resolveToolSavings from the response map or from
	// ToolResult.Savings directly; both surfaces flow through here
	// as a typed value so downstream handlers don't re-parse.
	savings *ToolSavings
}

// usageMsg snapshots the latest Usage from the agent. The TUI keeps
// only the most recent value and reports it once at turn-end on the
// finalized assistant message (R-USE-1). Cost / Model travel
// alongside so adapters that compute pricing per turn can surface
// it without an extra round-trip; zero values suppress the
// respective footer/sidebar segments.
type usageMsg struct {
	gen     uint64
	usage   Usage
	costUSD float64
	model   string
}

// turnDoneMsg signals clean turn completion. Populated with the
// elapsed wall-clock time so the per-turn footer can render it.
type turnDoneMsg struct {
	gen     uint64
	elapsed time.Duration
}

// turnErrMsg signals turn failure. The error is rendered as an Error
// row in the chat; the TUI stays interactive (no auto-quit per §4.2).
type turnErrMsg struct {
	gen uint64
	err error
}

// turnCancelledMsg signals Esc-interrupt (R-CHAT-6). The TUI emits an
// "(interrupted)" notice instead of an error banner.
type turnCancelledMsg struct{ gen uint64 }

// spinnerTickMsg fires every spinnerFrameCadence to advance the
// Braille glyph, and every spinnerFramesPerVerb of those to rotate the
// thinking/working verb (R-CHAT-3). gen is the spinnerGen stamp
// taken when the tick was armed — the Update handler drops the msg
// when it no longer matches, so a chain left over from an earlier
// turn (or an earlier LiveAgent stretch) dies instead of re-arming
// alongside the current one. Same guard shape as resizeReflowMsg.
type spinnerTickMsg struct{ gen uint64 }

// initialPromptMsg fires exactly once from Init() when the host set
// Options.InitialPrompt to a non-empty value. Update routes it
// through the same submitTurn path an operator-typed submission uses,
// so the seed prompt renders as a normal RoleUser row + streams the
// response into chat scroll.
type initialPromptMsg struct{ text string }

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

// remoteInterruptDoneMsg carries the outcome of a /interrupt slash
// that dispatched through RemoteInterrupter — the fallthrough path
// used when the TUI has no local per-turn context to cancel
// (LiveAgent / observer mode). Empty err = the remote endpoint
// accepted the cancel; non-nil err = network hiccup, endpoint
// missing, or the daemon reported no in-flight turn. Either way
// Update appends a follow-up system row so the operator sees
// resolution, not just the "cancelling remote turn…" placeholder.
type remoteInterruptDoneMsg struct{ err error }

// liveStreamStartedMsg fires once at startup after the LiveAgent
// drain goroutine launches; carries the cancel func so the
// Update handler can stash it on the model's cancelLiveStream
// field (Init has a value receiver and can't mutate). Also
// triggers the one-time "Attached as observer" system row so the
// operator knows they're in LiveAgent mode.
type liveStreamStartedMsg struct {
	gen    uint64
	cancel func()
}

// liveStreamErrMsg carries a non-nil error yielded by a
// LiveAgent.Events iterator (issue #22). The drain goroutine
// surfaces it as a RoleError row and keeps draining — the
// iterator decides whether to keep yielding events.
type liveStreamErrMsg struct {
	gen uint64
	err error
}

// liveStreamEndedMsg fires when a LiveAgent.Events iterator
// returns / stops yielding (issue #22). core-tui renders a
// "Disconnected — Ctrl+C to quit" system row and keeps the
// program alive so the operator can read scrollback. No
// auto-reconnect; the LiveAgent implementation owns that.
type liveStreamEndedMsg struct{ gen uint64 }

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

// coalescedRefreshMsg is the tick fired by markViewportDirty +
// scheduleCoalescedRefresh: when the handler runs it clears the
// dirty + pending flags and calls refreshViewport once. All event
// msgs that land inside the ~1ms coalesce window between the first
// mark and the tick share the same refresh — turning the previously
// per-event O(N) concat + SetContent into O(N × batch-size).
// Critical for attach-to-long-session latency; see model.go's
// viewportDirty / refreshPending fields for the mechanism.
type coalescedRefreshMsg struct{}

// Push-mode SSE event-stream msgs (issue #40, spec v1.1.0).
// One per spec event type; emitEvent in agentcmd.go emits the
// matching msg when an Event carries the corresponding optional
// payload field. Internal types — host adapters populate the
// exported Event fields, they don't construct these directly.

// statusUpdateMsg carries the spec §2.2 status-update payload
// through to the Update loop. Merge semantics: handler applies
// non-empty fields onto model state and leaves the rest unchanged.
type statusUpdateMsg struct {
	gen    uint64
	status StatusUpdate
}

// usageUpdateMsg carries the spec §2.3 usage-update payload —
// cumulative session totals + optional per-model breakdown. Replaces
// the current snapshot rather than merging (the wire payload always
// carries totals, not deltas).
type usageUpdateMsg struct {
	gen    uint64
	update UsageUpdate
}

// inboxStateMsg carries the spec §2.4 inbox payload — operator-
// typed prompt queued/dequeued state change.
type inboxStateMsg struct {
	gen   uint64
	event InboxEvent
}

// turnSummaryMsg carries the spec §2.5 turn-complete payload —
// per-turn token + cost + latency metrics. Snapshots into the
// per-turn footer fields (currentUsage, currentCost, etc.) so the
// rendered footer reads the same values regardless of which path
// produced them (legacy usageMsg vs push-mode turnSummaryMsg).
type turnSummaryMsg struct {
	gen     uint64
	summary TurnSummary
}

// turnErrorMsg carries the spec §2.6 turn-error payload — a
// structured pipeline failure that should be rendered as a styled
// block in the chat. Handler appends a RoleError Message with the
// structured payload attached so the renderer can pick out kind /
// hint / retryable for richer presentation than a flat text row.
type turnErrorMsg struct {
	gen       uint64
	turnError TurnError
}

// noticeMsg carries one host-initiated notice from the
// Options.Notifier channel through to the Update loop. Internal
// type — hosts push via Notifier.Notify(text), they don't
// construct this directly.
type noticeMsg struct {
	text    string
	dropped int // coalesced drop count; appended to rendered text as "(+N dropped)"
}

// Off-loop host-call replies (issue #114). Each is produced by a Cmd
// in host_async.go and carries the sessionGen captured when the Cmd
// was built, so a mid-flight `/switch` retires the reply exactly like
// every other generation-guarded msg above.

// modelsLoadedMsg carries the ModelSwapper.AvailableModels() snapshot
// pulled when the model picker opened. Update installs it on the
// dialog (addressed via overlayStack.Get, so a modal stacked on top
// of the picker in the meantime doesn't misroute it).
type modelsLoadedMsg struct {
	gen    uint64
	models []ModelInfo
}

// modelSwitchedMsg carries the outcome of ModelSwapper.SwitchModel.
//
// gen matters more here than anywhere else in this group: the handler
// assigns msg.agent to m.opts.Agent, so landing a stale reply after a
// session switch would attach the WRONG agent to the current session.
// A mismatched gen is dropped without touching opts.
type modelSwitchedMsg struct {
	gen   uint64
	id    string
	agent Agent
	err   error
}

// sessionsLoadedMsg carries the SessionSwitcher.Sessions() snapshot
// pulled when the session picker opened.
type sessionsLoadedMsg struct {
	gen      uint64
	sessions []SessionInfo
}

// sessionSwitchedMsg carries the outcome of
// SessionSwitcher.SwitchToSession. The handler runs applySwitchTarget,
// which itself bumps sessionGen — so a second reply from a superseded
// picker is dropped by the guard rather than switching twice.
type sessionSwitchedMsg struct {
	gen    uint64
	id     string
	target SwitchTarget
	err    error
}

// slashCommandsMsg carries the host's SlashProvider.SlashCommands()
// rows for a `/` palette that already opened with the built-ins.
//
// seq identifies the palette instance (Model.paletteSeq, bumped on
// every open). A palette opens and closes many times inside one
// session generation, so gen alone can't tell whether the reply still
// belongs to what is on screen.
type slashCommandsMsg struct {
	gen   uint64
	seq   uint64
	items []paletteItem
}

// fileItemsMsg carries the @-palette's directory-walk result for the
// palette instance identified by seq. Same seq rationale as
// slashCommandsMsg; gen rides along for symmetry with the rest of the
// group even though the walk is filesystem- not agent-derived.
type fileItemsMsg struct {
	gen   uint64
	seq   uint64
	items []paletteItem
}

// reloadDoneMsg carries the outcome of Reloader.Reload, run off-loop
// under reloadTimeout.
type reloadDoneMsg struct {
	gen    uint64
	result ReloadResult
	err    error
}

// pricingRefreshedMsg carries the outcome of
// PricingController.Refresh, run off-loop under
// pricingRefreshTimeout.
type pricingRefreshedMsg struct {
	gen     uint64
	summary string
	err     error
}

// Off-loop replies for the /cmd path and the Options.* host callbacks
// (issue #137, the follow-up to #114). Same generation contract as the
// group above: each carries the sessionGen its Cmd was built with and
// Update drops it when the session has moved on.

// permissionModeAppliedMsg carries the outcome of one Shift+Tab step
// through Options.PermissionMode — Set, then Persist when Set
// succeeded.
//
// prev is the mode the chip showed before the keystroke. The handler
// rolls back to it when Set failed, so the chip never advertises a
// policy the host declined to enforce. Both errors used to be
// discarded; they now surface as error rows.
type permissionModeAppliedMsg struct {
	gen        uint64
	prev       PermissionMode
	mode       PermissionMode
	err        error // Options.PermissionMode.Set
	persistErr error // Options.PermissionMode.Persist
}

// persistDoneMsg carries the outcome of one Options persistence
// callback (PersistModelChoice / PersistThemeChoice /
// PersistStatusLayout). what is the row prefix — "/model", "/theme",
// "status layout". A nil err produces no row: persistence succeeding
// is not news.
type persistDoneMsg struct {
	gen  uint64
	what string
	err  error
}

// clipboardWrittenMsg carries the outcome of one
// Options.ClipboardWriter call (issue #175). Unlike persistDoneMsg a
// nil err IS news: it is the only acknowledgement a copy can get, so
// it is what lets the notice stop hedging about OSC 52.
//
// gen is Model.copyGen at the time of the copy, not sessionGen — the
// message edits a notice, and the notice turns over far faster than
// the session does.
type clipboardWrittenMsg struct {
	gen uint64
	err error
}

// toolsListedMsg carries the ToolLister.Tools() catalog for /tools.
type toolsListedMsg struct {
	gen   uint64
	tools []ToolInfo
}

// approvalsListedMsg carries PermissionController.SessionApprovals()
// for /permissions.
type approvalsListedMsg struct {
	gen  uint64
	logs []ApprovalLog
}

// permissionRuleAddedMsg carries the outcome of a /allow or /deny
// mutation. op + arg are echoed back so the handler can compose the
// same result rows the inline version wrote.
type permissionRuleAddedMsg struct {
	gen uint64
	op  permissionRuleOp
	arg string
	err error
}

// subagentRosterMsg carries SubagentLister.Subagents() for a bare
// /subagents. drillable was resolved at dispatch (SubagentEventReader
// type assertion) and only decides whether the rendered list mentions
// `/subagents <name>`.
type subagentRosterMsg struct {
	gen       uint64
	subs      []SubagentInfo
	drillable bool
}

// pricingSetMsg carries the outcome of PricingController.Set, from
// either the positional `/pricing set <id> <in> <out>` or the huh
// form's submit handler.
type pricingSetMsg struct {
	gen     uint64
	summary string
	err     error
}

// helpCommandsMsg carries the host's SlashProvider.SlashCommands()
// specs for /help. The built-in section is already on screen by the
// time this lands; the handler appends the "Agent commands:" block
// underneath it, and appends nothing when the host exposes none.
type helpCommandsMsg struct {
	gen   uint64
	specs []SlashCommandSpec
}

// The two call sites #137 deferred: `/switch <id>` and dispatchSlash's
// match-then-invoke. Both carry seq — Model.slashSeq as of the
// dispatch — on top of gen, because both can be overtaken by the next
// /cmd without the session ever changing.

// switchLookupMsg carries stage one of `/switch <id>`: the
// SessionSwitcher.Sessions() enumerate, with the row matching id
// already picked out inside the goroutine (a pure filter over the id
// the closure was handed — it reads no model state).
//
// row non-nil means id named an ACTION row (issue #56) rather than a
// session, and the handler opens that row's text-input dialog. row nil
// sends the handler on to stage two, SwitchToSession, whose reply
// lands as the ordinary sessionSwitchedMsg.
//
// The seq guard is what keeps the two stages honest: an enumerate that
// has been superseded must never be allowed to drive a switch.
type switchLookupMsg struct {
	gen uint64
	seq uint64
	id  string
	row *SessionInfo
}

// sessionInputSubmittedMsg carries the outcome of a SessionInfo
// action row's SessionInput.Submit — the "+ Attach to endpoint…"
// closure, which the picker used to run inline from its text input's
// Enter handler (issue #194).
//
// Stamped with the same gen + seq pair as switchLookupMsg, and for
// the same reason: the dialog it answers can be dismissed, and the
// operator can be somewhere else entirely by the time a wedged
// endpoint gives up. rowID and value identify the dialog that asked
// — see applySessionInputSubmit for why the stamp alone isn't enough
// when the picker, which bumps no counter, is the way in.
//
// Success is applied through the ordinary sessionSwitchedMsg path
// rather than carrying its own attach logic.
type sessionInputSubmittedMsg struct {
	gen    uint64
	seq    uint64
	rowID  string
	value  string
	target SwitchTarget
	err    error
}

// slashDispatchedMsg carries the host-provider half of a `/cmd`: the
// SlashCommands() name match, plus — for a plain SlashProvider — the
// InvokeSlash that follows it, both inside one off-loop Cmd.
//
// matched=false is the "unknown command" verdict, and it is the reason
// this message is stamped at all. That row used to be written in the
// dispatching frame, so it could not be out of order; with the match
// off-loop, an operator who types /foo and then /help would otherwise
// read "unknown command /foo" underneath /help's output and reasonably
// conclude /help is the command that doesn't exist.
//
// invoked separates the two matched shapes. A plain SlashProvider is
// invoked inside the same Cmd, so res/err are the whole answer. An
// Async(WithPreamble)SlashProvider is not: launching one arms
// m.cancelSlash, m.inFlightSlash and the toast, which is model state
// only Update may touch.
type slashDispatchedMsg struct {
	gen     uint64
	seq     uint64
	name    string
	args    string
	matched bool
	invoked bool
	res     SlashResult
	err     error
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
