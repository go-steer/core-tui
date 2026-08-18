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
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
)

// Init asks the terminal for its background color so the style bundle
// can resolve dark vs light at startup (R-MD-2), starts the textarea
// cursor blink, primes the event listener that drains messages from
// the agent dispatch goroutine, and (when the host's agent
// implements WakeRequester) subscribes to the wake channel for
// transient toast banners (R-WAKE-1).
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		tea.RequestBackgroundColor,
		textarea.Blink,
		m.eventListener(),
		m.wakeListener(),
		m.promptListener(),
		m.elicitListener(),
		m.notifyListener(),
	}
	// Startup wordmark wipe (issue #165). nil unless the banner is
	// actually going to animate — a host that has turned it off, or an
	// environment asking for reduced motion, gets no tick chain at all
	// and keeps the settled wordmark NewModel already seeded.
	if c := m.armBanner(); c != nil {
		cmds = append(cmds, c)
	}
	// Prime the render-path host cache (see host_snapshot.go) so the
	// status header reads model/provider/usage from a snapshot refreshed
	// off the event loop instead of calling the host from View(). nil
	// when the host implements neither capability.
	if c := m.refreshHostSnapshotCmd(); c != nil {
		cmds = append(cmds, c)
	}
	// Issue #22: LiveAgent mode spawns the single long-lived drain
	// goroutine at startup so autonomous activity reaches the
	// chat view even before the operator types anything. The
	// returned cancel func lives on the model so the test harness
	// + future force-reconnect paths can stop it; Esc does NOT
	// fire it (cancelling the only event source via Esc would be
	// a foot-gun).
	if m.liveMode {
		if liveAgent, ok := m.opts.Agent.(LiveAgent); ok {
			// Init has a value receiver — we can't mutate m here.
			// startLiveStream's cancel needs to live somewhere
			// addressable; stash via a tea.Cmd that returns a
			// liveStreamStartedMsg carrying the cancel func.
			cmds = append(cmds, m.spawnLiveStreamCmd(liveAgent))
		}
	}
	// Options.InitialPrompt seeds the first turn on startup — the msg
	// lands in Update after the wiring above so the event listener +
	// theme are already primed by the time we start submitTurn. Skip
	// in liveMode since the autonomous stream is already producing
	// events; the two don't compose cleanly.
	if m.opts.InitialPrompt != "" && !m.liveMode {
		text := m.opts.InitialPrompt
		cmds = append(cmds, func() tea.Msg { return initialPromptMsg{text: text} })
	}
	return tea.Batch(cmds...)
}

// spawnLiveStreamCmd is the bridge that lets Init() (value
// receiver) hand the eventually-mutating cancelLiveStream onto
// the model: returns a Cmd that starts the drain goroutine and
// reports back via liveStreamStartedMsg so the Update handler
// can stash the cancel on the pointer it owns.
func (m Model) spawnLiveStreamCmd(agent LiveAgent) tea.Cmd {
	// Capture the current sessionGen at Cmd-construction time so
	// the returned liveStreamStartedMsg is discarded if
	// applySwitchTarget bumps m.sessionGen before it lands.
	gen := m.sessionGen
	return func() tea.Msg {
		cancel := m.startLiveStream(agent)
		return liveStreamStartedMsg{gen: gen, cancel: cancel}
	}
}

// Update is the Bubble Tea dispatcher. The visual-preview slice
// handles window-resize, background-color, and a small keymap; later
// slices add agent-event dispatch, modal forms, etc.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Pending huh.Form intercepts EVERY tea.Msg (KeyPress,
	// WindowSize, ticks) so the embedded form runs its own
	// state machine. On completion / abort, updatePricingForm
	// dispatches the result + clears m.pendingForm; the
	// remaining Update cases run on the next msg.
	if m.pendingForm != nil {
		cmd := m.updatePricingForm(msg)
		return m, cmd
	}

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		// Bubble Tea can emit multiple WindowSizeMsg during startup
		// (initial size + terminal-negotiated size); skip if
		// nothing actually changed so we don't run the reflow or
		// repaint for no reason.
		if msg.Width == m.width && msg.Height == m.height {
			return m, nil
		}
		widthChanged := msg.Width != m.width
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		// Cached Glamour Rendered text is width-pinned at the
		// time of the render; when the viewport narrows the cached
		// output is wider than the visible area and the terminal
		// clips it (leading whitespace can vanish along with the
		// first few content chars). Height-only changes leave
		// wrapping identical, so skip the reflow entirely then.
		//
		// Issue #104: this used to re-render EVERY assistant message
		// through Glamour right here, inline — 91.5ms per event at
		// 100 turns, paid once per event for the whole stream of
		// them a pane drag emits. beginResizeReflow instead reflows
		// only what is on screen (bounded by viewport height) and
		// returns a settle tick that warms the rest incrementally
		// once the drag stops. See resize.go.
		var cmd tea.Cmd
		if widthChanged {
			cmd = m.beginResizeReflow()
		}
		m.refreshViewport()
		return m, cmd

	case resizeReflowMsg:
		// Settle / warm tick for a width change (issue #104). The
		// generation guard is the same shape as the sessionGen
		// checks below: multiple drags can have ticks in flight, and
		// a tick from a superseded drag must not resume a walk whose
		// target width is no longer current.
		if msg.gen != m.resizeGen {
			return m, nil
		}
		return m, m.continueResizeReflow()

	case tea.BackgroundColorMsg:
		// When the host has set Options.ForceTheme, the operator's
		// explicit choice wins over whatever the terminal reports
		// — some SSH stacks / tmux passthroughs respond with the
		// wrong color, and we'd otherwise flip them to the wrong
		// variant on every redraw.
		if m.opts.ForceTheme == ThemeDark || m.opts.ForceTheme == ThemeLight {
			return m, nil
		}
		// Set Dark first so refreshTheme picks the right variant
		// (refreshTheme reads m.styles.Dark for the dark/light
		// branching inside resolveStyles + textareaStyles).
		m.styles.Dark = msg.IsDark()
		m.refreshTheme()
		m.refreshViewport()
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case tea.MouseWheelMsg:
		// A modal on screen owns the wheel. Without this the event
		// falls through to the chat viewport BEHIND the modal and
		// scrolls something the operator can't see — the modal
		// itself never moves. handleWheel returns false when
		// nothing modal is open (or when the surface under the
		// wheel really is the chat, e.g. an inline permission
		// prompt), and we fall through to the viewport as before.
		if cmd, handled := m.handleWheel(msg); handled {
			return m, cmd
		}

	case tea.PasteMsg:
		// Bracketed paste never goes through handleKey, so without
		// this a paste while a text-input Dialog is open would land
		// in the chat textarea BEHIND the modal — the single most
		// likely operator move at an "Attach to endpoint (URL):"
		// prompt. Route it to the front dialog as a synthetic
		// key press carrying the whole run in Key.Text. Since #117
		// the three pickers implement KeyMsgDialog too, so a paste
		// lands in their filter row — which is what you want when
		// the clipboard holds the model ID you are looking for. The
		// dialogs that still don't implement it (the tool-call and
		// subagent overlays) never see it and the paste falls
		// through as before.
		if _, ok := m.overlayStack.Front().(KeyMsgDialog); ok {
			if key, ok := pasteKeyMsg(msg.Content); ok {
				if consumed, cmd := m.overlayStack.HandleKeyMsg(key, &m); consumed {
					m.refreshViewport()
					return m, cmd
				}
			}
			return m, nil
		}
		// No dialog wanted it and the transcript holds the keyboard:
		// take focus back before the paste falls through to the
		// forwarding tail. bubbles drops every message a blurred
		// textarea gets, so the alternative is a paste that vanishes
		// without a trace — and pasting is about as unambiguous a
		// "I am composing now" gesture as there is (issue #151).
		m.setFocus(focusInput)

	case streamChunkMsg:
		// Straggler from an outgoing session (issue #48 / #53) —
		// don't let it bleed into the new session's assistant
		// buffer during the switch race window.
		if msg.gen != m.sessionGen {
			return m, m.eventListener()
		}
		needSpinner := m.liveMode && msg.partial && !m.spinnerActive
		m.applyStreamChunk(msg)
		// In LiveAgent mode the spinner tick isn't scheduled by
		// any submitTurn — kick it off when the first partial
		// chunk after an idle stretch arrives. applyStreamChunk
		// flips m.spinnerActive=true in that case; needSpinner
		// is captured BEFORE the call so we only spawn a single
		// tick per active stretch.
		//
		// Issue #26: also fold in the LiveAgent render kick so a
		// single non-partial chat-content chunk arriving in a
		// quiet window paints without waiting for a keypress.
		// liveStreamRenderCmd handles the conditional batching.
		if needSpinner {
			// applyStreamChunk bumped spinnerGen on the false→true
			// flip, so this arms a chain that supersedes anything
			// left over from the previous stretch (issue #112).
			return m, m.liveStreamRenderCmd(m.armSpinner())
		}
		return m, m.liveStreamRenderCmd()
	case toolCallMsg:
		if msg.gen != m.sessionGen {
			return m, m.eventListener()
		}
		m.applyToolCall(msg)
		// Issue #26: render kick in LiveAgent mode — a solo
		// autonomous tool call could otherwise sit invisible
		// until the next operator keypress.
		//
		// Issue #71: a sync subagent runs for as long as it takes
		// and says nothing on the parent's stream, so start tailing
		// its turns into the row we just appended. No-op for
		// ordinary tools.
		return m, m.liveStreamRenderCmd(m.startSubagentTail(msg))
	case toolResultMsg:
		if msg.gen != m.sessionGen {
			return m, m.eventListener()
		}
		m.applyToolResult(msg)
		// Issue #26: same — a result event landing without other
		// Msgs in flight needs the kick.
		return m, m.liveStreamRenderCmd()
	case slashResultMsg:
		// Issue #13: clear the in-flight indicators now that the
		// host's call has completed (success, error, or cancel —
		// all three land here). Drop the toast so the operator sees
		// the result row that's about to be appended; release the
		// cancel func so a stale Esc press doesn't double-cancel.
		m.inFlightSlash = nil
		m.cancelSlash = nil
		m.toast = ""
		return m.applySlashResult(msg.name, msg.res, msg.err)
	case liveStreamStartedMsg:
		// Issue #48: a switch away from a LiveAgent host may have
		// bumped m.sessionGen before this msg landed — the
		// previous drain's cancel is meaningless to us now. Fire
		// it defensively and drop.
		if msg.gen != m.sessionGen {
			if msg.cancel != nil {
				msg.cancel()
			}
			return m, nil
		}
		// Issue #22: stash the cancel func; log the one-time
		// system row announcing LiveAgent mode.
		//
		// Issue #50 (revised 2026-07-18): banner text branches on
		// InjectableAgent capability. Three shapes matter, not two:
		//
		//   1. **Read-only observer** (LiveAgent, no Inject) — host
		//      streams events but rejects operator input. Original
		//      "Attached as observer" text.
		//   2. **Live session** (LiveAgent + Inject, autonomous
		//      producer possibly nil) — the honest framing here is
		//      "events stream + you can inject", NOT "your messages
		//      drive the agent." The demo case (2026-07-18) is a
		//      k8s-event-watcher pushing incident injects
		//      autonomously while the operator observes and CAN
		//      inject follow-up prompts — the "you drive" wording
		//      was actively misleading because most turns weren't
		//      operator-initiated. New wording is neutral about who
		//      drives.
		//
		// Pure-Inject-without-LiveAgent hosts don't reach this msg
		// (the msg only fires on LiveAgent construction).
		m.cancelLiveStream = msg.cancel
		bannerText := "Attached as observer — agent runs autonomously; events stream below."
		if _, ok := m.opts.Agent.(InjectableAgent); ok {
			bannerText = "Attached to live session — events stream below; type to send a message."
		}
		m.history.Append(Message{Role: RoleSystem, Text: bannerText})
		m.markViewportDirty()
		// Issue #28: route through liveStreamRenderCmd so the
		// eventListener stays armed. This Msg actually arrives
		// via the spawnLiveStreamCmd Cmd-result path (not
		// eventCh) so dropping the listener wouldn't kill the
		// drain in itself, but routing through the helper keeps
		// every LiveAgent handler consistent — and the render
		// kick (#24) comes along for free.
		return m, m.liveStreamRenderCmd()
	case liveStreamErrMsg:
		if msg.gen != m.sessionGen {
			return m, m.eventListener()
		}
		// Issue #51: classify the error. Permanent conditions (session
		// gone, auth revoked) don't recover on retry — surface a
		// distinct row and flip the disconnected bit so the operator
		// can read scrollback and quit. Everything else keeps draining
		// via the eventListener path.
		if isPermanentStreamErr(msg.err) {
			m.liveDisconnected = true
			// Nothing further is coming on this stream, so a
			// spinner left turning is now claiming otherwise
			// (issue #148).
			m.endLiveStretch()
			m.history.Append(Message{
				Role: RoleError,
				Text: "session unavailable: " + msg.err.Error() + " — relaunch to start a fresh session",
			})
			m.refreshViewport()
			return m, nil
		}
		// Surface as an Error row and keep draining. The iterator
		// itself decides whether to keep yielding events.
		m.history.Append(Message{
			Role: RoleError,
			Text: "live stream error: " + msg.err.Error() + " — waiting to reconnect",
		})
		m.markViewportDirty()
		// Issue #28: this Msg ARRIVED via eventListener (it was
		// pushed onto m.eventCh by startLiveStream's drain
		// goroutine). Returning only the kick would leave nothing
		// reading m.eventCh — subsequent events (reconnect
		// notices, post-error frames) would sit buffered until
		// some other path happens to re-issue eventListener.
		// liveStreamRenderCmd batches eventListener + render kick.
		return m, m.liveStreamRenderCmd()
	case liveStreamEndedMsg:
		if msg.gen != m.sessionGen {
			return m, m.eventListener()
		}
		// Iterator returned. Flip the disconnected bit so the
		// banner can render; keep the program alive so the
		// operator can read scrollback and choose to quit.
		m.liveDisconnected = true
		// Same as the permanent-error arm: the iterator has
		// returned, so stop claiming a turn is in flight (#148).
		m.endLiveStretch()
		m.history.Append(Message{
			Role: RoleSystem,
			Text: "Disconnected from live stream. Press Ctrl+C to quit.",
		})
		m.markViewportDirty()
		// Issue #28: same root cause as liveStreamErrMsg. Even
		// though the iterator has stopped pushing new events,
		// m.eventCh may still carry events that were buffered
		// before the iterator returned — without re-arming the
		// listener those would be lost. One extra listener that
		// eventually blocks forever (no more pushes) is harmless.
		return m, m.liveStreamRenderCmd()
	case forceRenderMsg:
		// Issue #24: no-op handler. The value is the fact that
		// bubble-tea processed a Msg → ran Update → triggered
		// the View pass that the modal-setting handler above
		// needed. Do not change model state here — this msg
		// must stay a paint-only hint.
		return m, nil
	case coalescedRefreshMsg:
		// The paired paint half of markViewportDirty +
		// scheduleCoalescedRefresh (see agentcmd.go). Every event
		// handler that landed inside the last coalesceWindow
		// shares this one refresh; refreshPending is cleared here
		// so the NEXT dirty flag schedules a fresh tick. If more
		// events kept flowing after the tick was scheduled but
		// before it fired, viewportDirty is still true and gets
		// serviced here in the same pass.
		m.refreshPending = false
		if m.viewportDirty {
			m.viewportDirty = false
			m.refreshViewport()
		}
		return m, nil
	case usageMsg:
		if msg.gen != m.sessionGen {
			return m, m.eventListener()
		}
		// Empty Usage (zero in/out) is the model-only signal — adapters
		// flag the live model on the first chunk before any real usage
		// has been computed. Don't clobber an existing currentUsage in
		// that case.
		if msg.usage.InputTokens != 0 || msg.usage.OutputTokens != 0 {
			m.currentUsage = &msg.usage
		}
		if msg.costUSD > 0 {
			m.currentCost = msg.costUSD
		}
		if msg.model != "" {
			m.currentModel = msg.model
		}
		// Issue #26: render kick in LiveAgent mode — a standalone
		// usage update (per-turn cost, model swap, etc.) can land
		// without other Msgs in flight.
		return m, m.liveStreamRenderCmd()

	// Push-mode SSE handlers (issue #40, spec v1.1.0). Each
	// applies its payload onto model state and re-renders. All
	// safe to fire without an in-flight turn (push mode is
	// architecturally "the server sends state whenever it
	// changes" — not gated on Run lifecycle).
	case statusUpdateMsg:
		if msg.gen != m.sessionGen {
			return m, m.eventListener()
		}
		// Merge semantics per spec §2.2 — non-empty fields apply,
		// empty fields leave existing state unchanged.
		if msg.status.Model != "" {
			m.currentModel = msg.status.Model
		}
		if msg.status.Provider != "" {
			m.pushedProvider = msg.status.Provider
			// Provider change can flip per-provider themes when
			// AutoProviderTheme is on; resolveStyles consults
			// displayProvider() which now reads from pushedProvider.
			m.refreshTheme()
		}
		if msg.status.ContextPct != nil {
			v := *msg.status.ContextPct
			m.pushedContextPct = &v
		}
		// PermMode + TurnState fields land here too but don't
		// drive any v1 rendering — the existing in-band turn
		// lifecycle (state field, spinnerActive flag) is the
		// source of truth for those today. Reserved for follow-up
		// work that unifies push + in-band turn state.
		return m, m.liveStreamRenderCmd()
	case usageUpdateMsg:
		if msg.gen != m.sessionGen {
			return m, m.eventListener()
		}
		// Snapshot the cumulative session payload. Renderers that
		// surface session-level usage (/stats, status sidebar)
		// can read from m.sessionUsage as a richer source than
		// the per-turn currentUsage / currentCost fields.
		u := msg.update
		m.sessionUsage = &u
		// Issue #57: usage-update's LastTurn (spec v1.1.1) carries
		// authoritative per-turn cost — server-side pricing has
		// already applied cache-discount + operator overrides. In
		// observer mode this is the only in-band path that yields
		// cost, since turn-complete's CostUSD is 0 for servers that
		// compute cost out-of-band (core-agent). Update current* +
		// back-annotate the tail assistant Message. When LastTurn is
		// nil (pre-#249 servers), current* and the footer stamp stay
		// on whatever turnSummaryMsg / usageMsg populated.
		if lt := u.LastTurn; lt != nil {
			if lt.CostUSD > 0 {
				m.currentCost = lt.CostUSD
			}
			if lt.Model != "" {
				m.currentModel = lt.Model
			}
			if lt.TokensIn != 0 || lt.TokensOut != 0 {
				m.currentUsage = &Usage{
					InputTokens:  lt.TokensIn,
					OutputTokens: lt.TokensOut,
				}
			}
			m.history.StampLatestAssistantFooter(lt.Model, m.currentUsage, lt.CostUSD, 0)
		}
		return m, m.liveStreamRenderCmd()
	case hostSnapshotMsg:
		// Off-loop host-capability refresh landed (see host_snapshot.go).
		// Drop stale generations, adopt the snapshot, and re-arm the tick
		// so the render-path cache keeps refreshing without ever calling
		// the host from View().
		if msg.gen != m.sessionGen {
			return m, nil
		}
		providerChanged := m.hostSnap.provider != msg.snap.provider
		m.hostSnap = msg.snap
		// Under AutoProviderTheme the palette follows the provider, which
		// now arrives via this cache rather than a synchronous Status()
		// call. Re-resolve styles when the cached provider changes so the
		// theme still flips (push-mode does the same in statusUpdateMsg).
		// Skipped when push already owns the provider tag.
		if providerChanged && m.opts.AutoProviderTheme && m.pushedProvider == "" {
			m.refreshTheme()
			m.refreshViewport()
		}
		return m, hostSnapshotTick(m.sessionGen)
	case subagentEventsMsg:
		// A `/subagents <name>` overlay page landed (issue #71).
		// Addressed to the dialog wherever it sits in the stack —
		// another modal may have opened on top of it since.
		if msg.gen != m.sessionGen {
			return m, nil
		}
		if d, ok := m.overlayStack.Get(subagentDialogID).(*subagentDialog); ok && d.apply(msg) {
			m.refreshViewport()
		}
		return m, nil
	case subagentPollMsg:
		// Live tail for the open overlay. Polling stops by simply
		// not re-arming: closing the dialog, retargeting it, or
		// switching sessions all drop the next tick on the floor.
		if msg.gen != m.sessionGen {
			return m, nil
		}
		d, ok := m.overlayStack.Get(subagentDialogID).(*subagentDialog)
		if !ok || d.name != msg.name {
			return m, nil
		}
		reporter, ok := m.opts.Agent.(SubagentReporter)
		if !ok {
			return m, nil
		}
		return m, tea.Batch(
			subagentEventsCmd(reporter, m.sessionGen, d.name, d.since),
			subagentPollTick(m.sessionGen, d.name),
		)
	case subagentTailMsg:
		// Inline tail page for a running sync-subagent tool row.
		if msg.gen != m.sessionGen {
			return m, nil
		}
		return m, m.applySubagentTail(msg)
	case subagentTailTickMsg:
		if msg.gen != m.sessionGen {
			return m, nil
		}
		return m, m.resumeSubagentTail(msg.callID)
	case hostSnapshotTickMsg:
		// Tick fired — issue the next off-loop refresh. Stale ticks from a
		// retired session are dropped (applySwitchTarget starts a fresh
		// cycle for the new generation).
		if msg.gen != m.sessionGen {
			return m, nil
		}
		return m, m.refreshHostSnapshotCmd()

	// ---- off-loop host-call replies (issue #114) ----

	case modelsLoadedMsg:
		// The model picker's open-time AvailableModels() snapshot.
		// Addressed through Get, not Front: another modal may have
		// opened on top of the picker while the host was answering,
		// and the reply still belongs to the picker (dialog.go).
		if msg.gen != m.sessionGen {
			return m, nil
		}
		if q := modelPickerOn(&m.overlayStack); q != nil {
			q.applyModels(msg.models, m.displayModelName())
			m.refreshViewport()
		}
		return m, nil
	case modelSwitchRequestedMsg:
		// The picker's Enter arm, come back for the two things a
		// question is not allowed to hold: the live ModelSwapper and
		// the live generation. Both can move while the picker is open —
		// a completed switch replaces m.opts.Agent, and a session
		// switch bumps the generation without closing the overlay
		// stack — so reading them here rather than capturing them at
		// open time is what keeps the call pointed at the agent the
		// operator is actually talking to.
		q := modelPickerOn(&m.overlayStack)
		if q == nil || q.switching != msg.ID {
			// Esc, or a second picker, between the keystroke and this
			// message. Nothing has been asked of the host yet, so
			// dropping it is free.
			return m, nil
		}
		swapper, wired := m.opts.Agent.(ModelSwapper)
		if !wired {
			// The agent lost ModelSwapper since the picker opened —
			// only reachable through a switch landing underneath it.
			// Release the in-flight state so the list is usable again.
			q.switching = ""
			return m, nil
		}
		return m, switchModelCmd(swapper, m.sessionGen, msg.ID)
	case modelSwitchedMsg:
		// SwitchModel REPLACES m.opts.Agent, so the generation guard
		// is load-bearing here rather than merely tidy: a reply that
		// left under the previous session would attach the outgoing
		// session's agent to the incoming one.
		if msg.gen != m.sessionGen {
			return m, nil
		}
		cmd := m.applyModelSwitch(msg)
		// Answer the picker only if it is the one that issued this
		// switch: esc mid-flight pops it, and the operator may have
		// re-opened a fresh picker before the reply landed. The switch
		// itself still applies either way — the host call was committed
		// the moment it left, and applyModelSwitch above has already
		// announced it.
		q := modelPickerOn(&m.overlayStack)
		if q == nil || q.switching != msg.id {
			return m, cmd
		}
		q.switching = ""
		if reason := modelSwitchFailure(msg); reason != "" {
			// The switch failed. The question was never answered, so
			// leave the picker open on its list rather than closing it:
			// the operator's next move is almost always the next model
			// down, and closing would make them re-run /model to get
			// back to a list they were already looking at.
			//
			// Say why ON the picker (issue #245). applyModelSwitch has
			// already put the reason in the transcript, but the
			// transcript is behind this modal, so on its own it leaves
			// the operator looking at an unchanged list with no account
			// of what their Enter did.
			q.fail.set(reason)
			return m, cmd
		}
		return m, tea.Batch(cmd, m.overlayStack.resolve(modelPickerDialogID, chosen{ID: msg.id}, &m))
	case sessionsLoadedMsg:
		if msg.gen != m.sessionGen {
			return m, nil
		}
		if q := sessionPickerOn(&m.overlayStack); q != nil {
			q.applySessions(msg.sessions)
			m.refreshViewport()
		}
		return m, nil
	case sessionInputRequestedMsg:
		// Enter on an action row (issue #56). Opened from here rather
		// than from the picker because Overlay pops the front dialog
		// after Key returns, so a modal pushed from inside Key is the
		// one that gets popped. Guarded the same way applySwitchLookup
		// guards its Open: two Enters in flight must not stack two
		// inputs.
		if !m.overlayStack.HasID(sessionInputDialogID) {
			m.overlayStack.Open(newSessionInputDialog(msg.Row))
		}
		return m, nil
	case sessionSwitchRequestedMsg:
		// Enter on an ordinary row, resolved against the LIVE agent and
		// generation — see sessionSwitchRequestedMsg for why the picker
		// could not hold either.
		q := sessionPickerOn(&m.overlayStack)
		if q == nil || q.switching != msg.ID {
			return m, nil
		}
		switcher, wired := m.opts.Agent.(SessionSwitcher)
		if !wired {
			// The agent lost SessionSwitcher since the picker opened.
			// Release the in-flight state so the list is usable again.
			q.switching = ""
			return m, nil
		}
		return m, switchToSessionCmd(switcher, m.sessionGen, msg.ID)
	case sessionSwitchedMsg:
		if msg.gen != m.sessionGen {
			return m, nil
		}
		cmd := m.applySessionSwitch(msg)
		// Same ownership check as modelSwitchedMsg: answer the picker
		// only if it is the one that issued this attach. Esc mid-flight
		// pops it, and the operator may have re-opened a fresh picker
		// before the reply landed. The attach applies either way.
		q := sessionPickerOn(&m.overlayStack)
		if q == nil || q.switching != msg.id {
			return m, cmd
		}
		q.switching = ""
		if reason := sessionSwitchFailure(msg); reason != "" {
			// The attach failed. The question was never answered, so
			// leave the picker open on its list — the same call the
			// model picker makes, and for the same reason: the next
			// move after "endpoint unreachable" is almost always the
			// next session down. And the same inline reason under it
			// (issue #245), since the transcript row applySessionSwitch
			// wrote is behind this modal.
			q.fail.set(reason)
			return m, cmd
		}
		return m, tea.Batch(cmd, m.overlayStack.resolve(sessionPickerDialogID, chosen{ID: msg.id}, &m))
	case slashCommandsMsg:
		// Host slash commands merging into an already-open / palette.
		if msg.gen != m.sessionGen || m.palette == nil ||
			m.palette.kind != paletteSlash || m.palette.seq != msg.seq {
			return m, nil
		}
		m.palette.applyItems(msg.items)
		m.resize()
		return m, nil
	case fileItemsMsg:
		// The @ palette's directory walk finished.
		if msg.gen != m.sessionGen || m.palette == nil ||
			m.palette.kind != paletteFile || m.palette.seq != msg.seq {
			return m, nil
		}
		m.palette.applyItems(msg.items)
		m.resize()
		return m, nil
	case reloadDoneMsg:
		if msg.gen != m.sessionGen {
			return m, nil
		}
		m.applyReload(msg)
		return m, nil
	case pricingRefreshedMsg:
		if msg.gen != m.sessionGen {
			return m, nil
		}
		text := msg.summary
		if msg.err != nil {
			text = "/pricing refresh: " + msg.err.Error()
		}
		if text != "" {
			m.history.Append(Message{Role: RoleSystem, Text: text})
		}
		m.refreshAndScroll()
		return m, nil

	// ---- off-loop /cmd + Options-callback replies (issue #137) ----

	case permissionModeAppliedMsg:
		if msg.gen != m.sessionGen {
			return m, nil
		}
		if msg.err != nil {
			// The gate refused the mode. Roll the chip back so it
			// doesn't advertise a policy the host isn't enforcing —
			// but only when nothing has moved on since. A second
			// Shift+Tab landing while this was in flight owns the chip
			// now, exactly like the `d.switching == msg.id` ownership
			// check on the pickers.
			if m.permMode == msg.mode {
				m.permMode = msg.prev
			}
			m.history.Append(Message{Role: RoleError, Text: "permission mode: " + msg.err.Error()})
			m.refreshAndScroll()
			return m, nil
		}
		if msg.persistErr != nil {
			// Persist failed but Set succeeded: the session is in the
			// new mode, it just won't survive a restart. Keep the chip.
			m.history.Append(Message{Role: RoleError, Text: "permission mode: persist failed: " + msg.persistErr.Error()})
			m.refreshAndScroll()
		}
		return m, nil
	case themePreviewMsg:
		// The theme picker's live preview, applied on the Update
		// goroutine because the widget has no *Model to apply it
		// with. Guarded on the picker still being open: the message
		// is asynchronous, so an operator who arrows and immediately
		// escapes can have it land after the resolver already put the
		// original theme back.
		if m.overlayStack.HasID(themePickerDialogID) {
			m.applyNamedTheme(msg.Name)
		}
		return m, nil
	case persistDoneMsg:
		if msg.gen != m.sessionGen || msg.err == nil {
			return m, nil
		}
		m.history.Append(Message{Role: RoleError, Text: msg.what + ": persist failed: " + msg.err.Error()})
		m.refreshAndScroll()
		return m, nil
	case clipboardWrittenMsg:
		// Edits the footer notice and nothing else — no history row,
		// no re-render of the transcript, because the operator is
		// parked on a selection they were reading. See
		// applyClipboardResult.
		m.applyClipboardResult(msg)
		return m, nil
	case toolsListedMsg:
		if msg.gen != m.sessionGen {
			return m, nil
		}
		m.history.Append(Message{Role: RoleSystem, Text: m.renderToolList(msg.tools)})
		m.refreshAndScroll()
		return m, nil
	case approvalsListedMsg:
		if msg.gen != m.sessionGen {
			return m, nil
		}
		m.history.Append(Message{Role: RoleSystem, Text: renderApprovalLog(msg.logs)})
		m.refreshAndScroll()
		return m, nil
	case permissionRuleAddedMsg:
		if msg.gen != m.sessionGen {
			return m, nil
		}
		role := RoleSystem
		if msg.err != nil {
			role = RoleError
		}
		m.history.Append(Message{Role: role, Text: renderPermissionRuleResult(msg)})
		m.refreshAndScroll()
		return m, nil
	case subagentRosterMsg:
		if msg.gen != m.sessionGen {
			return m, nil
		}
		m.history.Append(Message{Role: RoleSystem, Text: renderSubagentList(msg.subs)})
		m.refreshAndScroll()
		return m, nil
	case pricingSetMsg:
		if msg.gen != m.sessionGen {
			return m, nil
		}
		if msg.err != nil {
			m.history.Append(Message{Role: RoleError, Text: "/pricing set: " + msg.err.Error()})
		} else if msg.summary != "" {
			m.history.Append(Message{Role: RoleSystem, Text: msg.summary})
		}
		m.refreshAndScroll()
		return m, nil
	case helpCommandsMsg:
		if msg.gen != m.sessionGen || len(msg.specs) == 0 {
			return m, nil
		}
		m.history.Append(Message{Role: RoleSystem, Text: renderHostCommandHelp(msg.specs)})
		m.refreshAndScroll()
		return m, nil
	case switchLookupMsg:
		// Stage one of `/switch <id>`. seq as well as gen: an
		// enumerate the operator has already moved past must not be
		// allowed to drive stage two.
		if msg.gen != m.sessionGen || msg.seq != m.slashSeq {
			return m, nil
		}
		return m, m.applySwitchLookup(msg)
	case sessionInputSubmittedMsg:
		// A SessionInfo action row's own Submit closure coming back
		// (issue #194). Same pair of guards as the enumerate above,
		// and the handler applies one more of its own: the dialog
		// that asked has to still be open and still be waiting for
		// exactly this answer.
		if msg.gen != m.sessionGen || msg.seq != m.slashSeq {
			return m, nil
		}
		return m, m.applySessionInputSubmit(msg)
	case slashDispatchedMsg:
		// The host's name match, and its InvokeSlash when the provider
		// was the plain synchronous shape. Same seq guard, and here it
		// is load-bearing for the NEGATIVE case — see the msg's godoc.
		if msg.gen != m.sessionGen || msg.seq != m.slashSeq {
			return m, nil
		}
		return m.applySlashDispatch(msg)

	case inboxStateMsg:
		if msg.gen != m.sessionGen {
			return m, m.eventListener()
		}
		// Queued: brief toast so the operator sees "your prompt
		// reached the server." Dequeued: drop the toast (the
		// streaming turn that follows is the visible signal that
		// processing started). Future iteration can polish this
		// into a richer indicator (e.g. inline ✉ chip on the
		// user-prompt row) — v1 keeps it cheap and visible.
		switch msg.event.State {
		case InboxStateQueued:
			m.toast = "✉ prompt queued"
			m.toastSetAt = time.Now()
			return m, tea.Batch(m.liveStreamRenderCmd(), toastTick())
		case InboxStateDequeued:
			if m.toast == "✉ prompt queued" {
				m.toast = ""
			}
			return m, m.liveStreamRenderCmd()
		}
		// Unknown state per spec — tolerated as no-op.
		return m, nil
	case turnSummaryMsg:
		if msg.gen != m.sessionGen {
			return m, m.eventListener()
		}
		// Per-turn metrics from the spec's turn-complete event.
		// Snapshot into the same currentUsage / currentCost /
		// currentModel fields the existing renderTurnFooter
		// reads, so per-turn footers paint identically regardless
		// of which path fed them (legacy usageMsg vs push-mode
		// turnSummaryMsg). CostUSD may be 0 in v1.1.0 spec —
		// authoritative cost arrives on the immediately-following
		// usageUpdateMsg.
		if msg.summary.TokensIn != 0 || msg.summary.TokensOut != 0 {
			m.currentUsage = &Usage{
				InputTokens:  msg.summary.TokensIn,
				OutputTokens: msg.summary.TokensOut,
			}
		}
		if msg.summary.CostUSD > 0 {
			m.currentCost = msg.summary.CostUSD
		}
		if msg.summary.Model != "" {
			m.currentModel = msg.summary.Model
		}
		// Issue #57: observer mode never calls finalizeTurn, so stamp
		// the tail assistant Message with what we know from
		// turn-complete (tokens + model + latency). Cost lands on the
		// immediately-following usageUpdateMsg via its LastTurn
		// field. No-op for chat-mode operators — finalizeTurn already
		// stamped the fields; StampLatestAssistantFooter only fills
		// currently-zero fields.
		elapsed := time.Duration(msg.summary.LatencyMs) * time.Millisecond
		m.history.StampLatestAssistantFooter(msg.summary.Model, m.currentUsage, msg.summary.CostUSD, elapsed)
		return m, m.liveStreamRenderCmd()
	case turnErrorMsg:
		if msg.gen != m.sessionGen {
			return m, m.eventListener()
		}
		// Append a styled error row carrying the structured
		// payload. Renderer (renderMessage) checks for a
		// non-nil Message.TurnError and renders the richer
		// "kind · message · hint" block instead of bare text.
		te := msg.turnError
		m.history.Append(Message{
			Role:      RoleError,
			Text:      te.Message,
			TurnError: &te,
		})
		m.refreshAndScroll()
		return m, m.liveStreamRenderCmd()
	case turnDoneMsg:
		if msg.gen != m.sessionGen {
			return m, m.eventListener()
		}
		m.finalizeTurn(msg.elapsed, "")
		// Issue #9: AutoContinueFromInbox mode pulls the inbox
		// and submits a synthetic auto-continue turn instead of
		// draining one queue entry at a time. Falls through to
		// maybeDrainQueue when not applicable.
		if next, cmd, ok := m.maybeAutoContinue(); ok {
			return next, cmd
		}
		return m.maybeDrainQueue()
	case turnErrMsg:
		if msg.gen != m.sessionGen {
			return m, m.eventListener()
		}
		m.finalizeTurn(0, msg.err.Error())
		return m.maybeDrainQueue()
	case turnCancelledMsg:
		if msg.gen != m.sessionGen {
			return m, m.eventListener()
		}
		m.finalizeTurn(0, "(interrupted)")
		return m.maybeDrainQueue()
	case spinnerTickMsg:
		// Identity guard (issue #112). The two level gates below can
		// only tell whether *a* spinner should be running right now,
		// not which chain this tick belongs to — and m.state flips
		// back to stateStreaming well inside one spinnerFrameCadence
		// when a turn end is immediately followed by another
		// submitTurn (queue drain, auto-continue). The superseded
		// chain's next tick therefore passes the level gates and
		// re-arms, and the animation runs at 2x for the rest of the
		// session. The stamp is what distinguishes the chains.
		//
		// The window this can happen in got ten times smaller when
		// issue #162 sped the tick up, which makes the bug rarer and
		// no less real — the guard is what fixes it, not the odds.
		if msg.gen != m.spinnerGen {
			return m, nil
		}
		// Two gating paths, both folded into turnInFlight:
		//   - per-turn (Run): m.state == stateStreaming
		//   - LiveAgent (#22): m.spinnerActive driven by
		//     applyStreamChunk's partial-vs-commit logic
		//
		// Shares the predicate with renderInProgress on purpose
		// (issue #135). A chain that ticks while the render gate is
		// shut rotates a verb nobody can see, which is exactly the
		// state the live path sat in.
		if !m.turnInFlight() {
			return m, nil
		}
		m.spinnerFrame++
		m.markViewportDirty()
		if refresh := m.scheduleCoalescedRefresh(); refresh != nil {
			return m, tea.Batch(m.armSpinner(), refresh)
		}
		return m, m.armSpinner()
	case bannerTickMsg:
		// One frame of the startup wipe (issue #165). Advance first,
		// then ask armBanner whether there is another — bannerAnimates
		// reads the counter, so incrementing past bannerFrames is what
		// terminates the chain, and the last tick is the one that
		// paints the settled wordmark.
		//
		// No stale-chain guard is needed here (see bannerTickMsg), but
		// the transcript-empty gate inside bannerAnimates is: the
		// operator can type during the wipe, and once the banner is
		// off screen there is nothing left for a tick to repaint.
		if m.bannerFrame >= bannerFrames {
			return m, nil
		}
		m.bannerFrame++
		m.markViewportDirty()
		if refresh := m.scheduleCoalescedRefresh(); refresh != nil {
			return m, tea.Batch(m.armBanner(), refresh)
		}
		return m, m.armBanner()
	case initialPromptMsg:
		// Options.InitialPrompt lands here exactly once, right after
		// Init. Defensive guards mirror the Enter-key handler at the
		// top of update.go: empty text is a no-op; a mid-stream state
		// shouldn't be reachable this early but we skip rather than
		// stack turns; slash commands are refused (seed prompts drive
		// the model, not the TUI's own control surface). Everything
		// else flows through submitTurn identically to a real
		// operator submission.
		text := strings.TrimSpace(msg.text)
		if text == "" {
			return m, nil
		}
		if m.state == stateStreaming {
			return m, nil
		}
		if strings.HasPrefix(text, "/") {
			m.history.Append(Message{
				Role: RoleError,
				Text: "InitialPrompt cannot be a slash command; ignoring: " + text,
			})
			m.refreshViewport()
			return m, nil
		}
		m.recordPrompt(text)
		out := m.submitTurn(text)
		return out, out.armSpinner()
	case remoteInterruptDoneMsg:
		// Follow-up to the "/interrupt: cancelling remote turn…"
		// placeholder appended in the slash handler. Success case
		// stays quiet in the transcript itself — the operator
		// already saw the placeholder; the actual signal they care
		// about is the streamed tool calls stopping. Errors surface
		// as an inline row so the operator knows to escalate
		// (retry, restart daemon, manual kill).
		if msg.err != nil {
			m.history.Append(Message{Role: RoleError, Text: "/interrupt: " + msg.err.Error()})
		} else {
			m.history.Append(Message{Role: RoleSystem, Text: "/interrupt: remote turn cancelled"})
			// The cancel landed, so the stretch this spinner was
			// animating is over — and on the live path there is no
			// commit chunk coming to close it (issue #148). The
			// error arm deliberately does not do this: a failed
			// cancel means the remote turn is still running.
			m.endLiveStretch()
		}
		m.refreshAndScroll()
		return m, nil
	case wakeMsg:
		// Issue #7: the wake signal also fires whenever Inject() is
		// called by the queue panel (operator typed during streaming).
		// In that case the operator can already see the queued entry
		// in the panel — surfacing a "background subagent's report"
		// system message is both wrong and confusing. Suppress the
		// noisy half (toast + system row) when the queue has a
		// pending entry; the wake subscription itself still re-issues.
		if m.hasPendingQueueEntry() {
			return m, m.wakeListener()
		}
		// Transient toast says "something arrived"; permanent
		// history entry says "this is what arrived + how to act".
		// Wake signals are sourced from inbox pushes (subagent
		// report_alert, etc.) — the inbox auto-drains on the
		// next operator-initiated turn, so the only "action" is
		// to continue working.
		m.toast = "⚠ wake — alert in inbox · drains on next turn · /subagents for status"
		m.toastSetAt = time.Now()
		m.history.Append(Message{
			Role: RoleSystem,
			Text: "Wake signal received — an external alert (typically a background subagent's report) is waiting in the inbox. It will be prepended to the model's context on your next turn. Run /subagents to see which subagents have run recently.",
		})
		m.refreshViewport()
		// Re-issue both the wake subscription (drain the next one)
		// and a tick that auto-clears the toast after toastTTL.
		return m, tea.Batch(m.wakeListener(), toastTick())
	case permissionRequestMsg:
		req := msg.req
		m.pendingPermission = &req
		m.permissionShownAt = time.Now()
		m.scroll().reset()
		// Inline permission layout: force-snap viewport to bottom
		// so the prompt is visible (operator was likely watching
		// the assistant text; the new prompt appears below it and
		// we don't want them to miss it because they'd scrolled).
		// Centered-overlay layout doesn't care about viewport
		// scroll, so this is harmless either way.
		m.refreshAndScroll()
		// Issue #24: hosts delivering prompts from a quiet window
		// (remote bridge, scheduled callback) need the render
		// kick so the modal actually paints without waiting for
		// the operator's next keypress.
		return m, forceRenderTick()
	case elicitRequestMsg:
		r := msg.req
		// R-ELIC-3: a schema the modal cannot draw is refused
		// automatically, and both parties are told which way it went.
		// Screening here rather than inside Elicit is what makes the
		// operator's half possible — only the loop can append to the
		// transcript (issue #209). A request refused with no trace is
		// indistinguishable, from where the operator sits, from a
		// server that never asked.
		//
		// The server's half is an error, not an action. Nobody was
		// consulted, so no ElicitAction is true here; Cancel rides
		// along because the result type has an Action field, and
		// ErrElicitUnsupported is what a host actually branches on.
		if !supportedElicit(r) {
			m.history.Append(Message{
				Role: RoleSystem,
				Text: elicitUnsupportedNotice(msg.serverName, r),
			})
			m.dispatchElicitErr(
				ElicitResult{Action: ElicitActionCancel},
				elicitUnsupportedError(r),
			)
			m.refreshAndScroll()
			return m, tea.Batch(m.elicitListener(), forceRenderTick())
		}
		// askAgent, not askOperator: the request arrived unbidden, so
		// its committing keys stay inert for modalInputGrace (#95).
		m.overlayStack.ask(newElicitQuestion(msg.serverName, r), askAgent, elicitResolver)
		m.refreshViewport()
		// Issue #24: same render-kick rationale as permissionRequestMsg
		// — hosts that deliver elicit requests from a remote bridge
		// or background goroutine need the kick so the form paints.
		return m, forceRenderTick()
	case noticeMsg:
		// Issue #30: host-initiated chat row, drained from
		// Options.Notifier. Append as RoleNotice (distinct from
		// RoleSystem so operators can tell framework speech from
		// agent system response). Coalesced-drop count is
		// surfaced inline so a notice flood doesn't get silently
		// lost — the operator sees "(+N dropped)" appended.
		text := msg.text
		if msg.dropped > 0 {
			text = fmt.Sprintf("%s  (+%d dropped)", text, msg.dropped)
		}
		m.history.Append(Message{Role: RoleNotice, Text: text})
		m.refreshAndScroll()
		// Re-issue the listener (drain the next one) AND kick a
		// render — Notifier callers are typically background
		// goroutines landing in quiet windows; same rationale as
		// permission / elicit handlers above.
		return m, tea.Batch(m.notifyListener(), forceRenderTick())
	case toastClearMsg:
		// Issue #13: the async-slash indicator uses the toast
		// surface but must NOT auto-clear — a /compact that takes
		// 10s would lose its indicator at the 4s TTL and the
		// operator would be back in silence-land. Keep the toast
		// alive as long as a slash is in flight; the slashResultMsg
		// handler clears both.
		if m.inFlightSlash != nil {
			return m, nil
		}
		// Only clear if the same toast is still up (a fresh wake
		// during the TTL window restarts the timer).
		if time.Since(m.toastSetAt) >= toastTTL {
			m.toast = ""
			m.refreshViewport()
		}
		return m, nil
	case pendingExitClearMsg:
		// Quiet disarm of the warn-then-exit one-shot. We don't
		// echo a "warning cleared" message — the operator either
		// pressed Ctrl+C again (already handled) or moved on.
		m.pendingExit = false
		return m, nil
	}

	// Forward unhandled messages to the input + transcript so
	// navigation keys work even when our switch above doesn't claim
	// them.
	var taCmd tea.Cmd
	m.input, taCmd = m.input.Update(msg)
	m.forwardChatScroll(msg)
	// The chat wheel lands here too (handleWheel declines when no
	// modal owns it), so this is a scroll path: re-read the follow
	// intent from where the viewport ended up.
	m.syncFollow()
	// Same auto-grow reconciliation handleKey does after forwarding a
	// keystroke. A bracketed paste reaches the textarea through HERE,
	// not through handleKey — so without this the multi-line paste
	// that issue #121's doc comment promises would "grow the box
	// visibly" only grew it on the next unrelated keystroke. One bool
	// read when nothing moved.
	if m.syncInputHeight() {
		m.resize()
		m.refreshViewport()
	}
	return m, taCmd
}

// handleWheel routes a mouse-wheel tick to whichever modal surface
// is on screen. Returns handled=false when the event belongs to the
// chat viewport instead — no modal open, an inline permission prompt
// (which lives IN the chat flow and scrolls with it), or a
// horizontal wheel, which no modal consumes.
//
// Mirrors View's z-order cascade: permission → elicit → side answer
// → Dialog stack. The embedded huh form never reaches here; Update
// hands it every msg before the switch.
func (m *Model) handleWheel(msg tea.MouseWheelMsg) (tea.Cmd, bool) {
	var delta int
	switch msg.Button {
	case tea.MouseWheelUp:
		delta = -wheelScrollLines
	case tea.MouseWheelDown:
		delta = wheelScrollLines
	default:
		return nil, false
	}

	switch {
	case m.pendingPermission != nil:
		// Only the centered overlay scrolls here. The default
		// inline layout renders the prompt inside the chat
		// viewport, so the wheel should keep scrolling the chat —
		// that IS the surface showing the prompt.
		if m.opts.PermissionLayout != PermissionOverlay {
			return nil, false
		}
		m.scroll().by(delta)
		return nil, true
	case m.sideAnswer != nil:
		m.scroll().by(delta)
		return nil, true
	case m.overlayStack.HasDialogs():
		consumed, cmd := m.overlayStack.HandleWheel(delta, m)
		if consumed {
			// Same repaint the key path does: a wheel tick on the
			// theme picker moves the selection, which live-previews
			// the palette on the chat behind the modal.
			m.refreshViewport()
		}
		return cmd, consumed
	}
	return nil, false
}

// modalInputGrace is how long after a permission prompt or an
// elicitation form appears its committing keys stay inert.
//
// Both modals are opened by the agent, not by the operator, so
// whatever was already in the terminal's input buffer when the
// request landed would otherwise be read as the answer. Typing "say"
// at the wrong moment used to dispatch allow-session, allow-always
// and allow-once — three grants, one of them persistent, from a word
// aimed at the prompt. The window has to cover buffered input without
// being perceptible to someone reacting to the modal; a third of a
// second is well inside human reaction time (~250ms at best) and well
// past a terminal's buffer flush.
const modalInputGrace = 300 * time.Millisecond

// withinGrace reports whether a modal stamped at shownAt is still
// inside its no-commit window. A zero stamp is treated as expired so
// a directly-constructed Model (tests, hosts poking state) behaves
// exactly as it did before the window existed — and so that a
// question the OPERATOR opened, which carries no stamp, is never held
// (see askOrigin).
func withinGrace(shownAt time.Time) bool {
	return !shownAt.IsZero() && time.Since(shownAt) < modalInputGrace
}

// isPermissionDecisionKey reports whether a keystroke commits a
// permission decision — R-PERM-2's six. "v" is included regardless of
// whether the request carries a Verb: during the grace window the
// safe reading of any of these is "the operator was typing".
func isPermissionDecisionKey(stroke string) bool {
	switch stroke {
	case "y", "n", "s", "v", "t", "a":
		return true
	}
	return false
}

// handleKey runs the keymap for the visual-preview slice. The slice
// owns these bindings (no real agent dispatch yet); follow-up slices
// will replace this with full slash routing and modal state machines.
//
// We use msg.String() (a normalized keystroke like "ctrl+b" /
// "shift+enter") for dispatch — Code+Mod bit-fiddling is brittle in
// the face of v2's keyboard-enhancement protocol.
func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	stroke := msg.String()

	// Esc cascades through overlays (R-CHAT-6): side-answer modal →
	// other modal → help panel → palette → interrupt-in-flight →
	// no-op. Never quits.
	if stroke == "esc" {
		if m.pendingPermission != nil {
			m.dispatchPermission(DecisionDeny)
			return m, m.promptListener()
		}
		if m.sideAnswer != nil {
			m.sideAnswer = nil
			m.resize()
			m.refreshViewport()
			return m, nil
		}
		// Esc goes to the front-most Dialog first so it can do
		// cancel-time work (the theme picker restores the palette
		// it previewed; a nested text input pops back to its
		// parent picker). Dialogs all return Consumed+Close for
		// esc; the CloseFront below is the fallback for one that
		// declines to handle it.
		if m.overlayStack.HasDialogs() {
			if consumed, cmd := m.overlayStack.HandleKeyMsg(msg, &m); consumed {
				m.refreshViewport()
				return m, cmd
			}
			m.overlayStack.CloseFront()
			m.refreshViewport()
			return m, nil
		}
		if m.helpOpen {
			m.closeHelp()
			m.resize()
			m.refreshViewport()
			return m, nil
		}
		if m.palette != nil {
			m.palette = nil
			m.resize()
			m.refreshViewport()
			return m, nil
		}
		// Hand the keyboard back to the composer (issue #151).
		// Placed here — below every open surface, above the two
		// cancel arms — because focus is a mode the operator is
		// inside of, and esc's contract is to back out of the
		// innermost thing first. The cost is that interrupting a
		// turn from focus mode takes two presses; the alternative
		// costs the operator a mode they can't escape from with the
		// key that escapes everything else.
		if m.focus != focusInput {
			m.setFocus(focusInput)
			return m, nil
		}
		// Issue #13 bonus: Esc cancels an in-flight async slash
		// via the cancellable ctx we stashed in dispatchSlash.
		// Hosts that honor the AsyncSlashProvider ctx contract
		// will bail and send a slashResultMsg with the ctx error;
		// hosts that ignore ctx run to completion and the result
		// still lands (cancel becomes a no-op). Either way the
		// loop terminates cleanly.
		if m.cancelSlash != nil {
			m.cancelSlash()
			m.cancelSlash = nil
			if m.inFlightSlash != nil {
				m.toast = "▸ /" + m.inFlightSlash.name + " cancelling…"
				m.toastSetAt = time.Now()
				m.refreshViewport()
			}
			return m, nil
		}
		if m.state == stateStreaming && m.cancelTurn != nil {
			m.cancelTurn() // goroutine emits turnCancelledMsg
			return m, nil
		}
		return m, nil
	}

	// Side-answer modal also dismisses on Enter / Space (R-CMD-5).
	if m.sideAnswer != nil && (stroke == "enter" || stroke == "space") {
		m.sideAnswer = nil
		m.resize()
		m.refreshViewport()
		return m, nil
	}

	// A /btw answer longer than the terminal is tall gets windowed
	// by renderSideAnswer; these keys move the window. Claiming them
	// here also stops "up" from quietly walking prompt history
	// behind the modal, which is what it did before.
	if m.sideAnswer != nil && m.scroll().applyStroke(stroke) {
		return m, nil
	}

	// Permission modal — R-PERM-2's six decision keys. Highest
	// precedence after Esc so nothing else fires while pending.
	//
	// A prompt arrives asynchronously, so for the first
	// modalInputGrace the decision keys are inert and anything the
	// operator was already typing goes to the prompt instead of
	// answering a modal they haven't seen (issue #95). Esc is
	// deliberately outside the window (handled above): it denies,
	// which is the fail-safe direction, and a buffered Esc costs at
	// most a re-ask.
	if m.pendingPermission != nil {
		if withinGrace(m.permissionShownAt) && isPermissionDecisionKey(stroke) {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			if m.syncInputHeight() {
				m.resize()
			}
			m.refreshViewport()
			return m, cmd
		}
		switch stroke {
		case "y":
			m.dispatchPermission(DecisionAllowOnce)
			return m, m.promptListener()
		case "n":
			m.dispatchPermission(DecisionDeny)
			return m, m.promptListener()
		case "s":
			m.dispatchPermission(DecisionAllowSession)
			return m, m.promptListener()
		case "v":
			if m.pendingPermission.Verb != "" {
				m.dispatchPermission(DecisionAllowSessionVerb)
				return m, m.promptListener()
			}
		case "t":
			m.dispatchPermission(DecisionAllowSessionTool)
			return m, m.promptListener()
		case "a":
			m.dispatchPermission(DecisionAllowAlways)
			return m, m.promptListener()
		}
		// Arrow / page keys scroll the detail body — a 200-line diff
		// in the centered overlay was previously readable only down
		// to whatever fit on screen. Inline layout needs nothing
		// here: it renders in the chat viewport, which already
		// scrolls.
		if m.opts.PermissionLayout == PermissionOverlay {
			m.scroll().applyStroke(stroke)
		}
		// Swallow any other key — modal is exclusive while open.
		return m, nil
	}

	// Dialog overlay — front-most dialog gets every keystroke
	// before the rest of the modal cascade. Returns Consumed=true
	// when the dialog handled it; we then return early so the key
	// doesn't double-fire on textarea / viewport. The optional
	// Cmd is dialogs' channel for emitting outbound msgs (e.g.
	// theme picker fires ThemeChangedMsg here on commit).
	if m.overlayStack.HasDialogs() {
		if consumed, cmd := m.overlayStack.HandleKeyMsg(msg, &m); consumed {
			m.refreshViewport()
			return m, cmd
		}
	}

	// Palette dispatch — when a palette is open we consume the nav
	// keys ourselves; everything else falls through to the textarea
	// and the post-forward filter sync re-reads the input.
	if m.palette != nil {
		switch stroke {
		case "up", "ctrl+p":
			m.palette.moveCursor(-1)
			return m, nil
		case "down", "ctrl+n":
			m.palette.moveCursor(1)
			return m, nil
		case "tab":
			// Tab inserts the selection without submitting so the
			// operator can keep typing args (`/allow ` → `/allow pat`).
			return m.paletteComplete(), nil
		case "enter":
			// Enter on a slash palette item: insert AND submit in one
			// keystroke (mirrors internal/tui's UX so `/mcp ⏎` from
			// the palette renders the catalog in one press, not two).
			// Items marked NoAutoSubmit (compound commands needing
			// more args, e.g. "/allow bundle:<name>") just insert.
			// File palette items always just insert — there's
			// typically more text to type after the @path.
			kind := m.palette.kind
			noAuto := false
			if sel, ok := m.palette.selected(); ok {
				noAuto = sel.NoAutoSubmit
			}
			m = m.paletteInsert().(Model)
			if kind == paletteSlash && !noAuto {
				text := strings.TrimSpace(m.input.Value())
				if strings.HasPrefix(text, "/") {
					return m.dispatchSlash(text)
				}
			}
			return m, nil
		}
	}

	// Reset the warn-then-exit one-shot on every keystroke that
	// isn't a follow-up Ctrl+C. Without this a Ctrl+C followed by
	// any typing would still latch the next Ctrl+C as an exit.
	if stroke != "ctrl+c" && m.pendingExit {
		m.pendingExit = false
	}

	// Transcript focus (issue #151) gets first refusal on everything
	// the modal cascade above left. Keys it declines fall through to
	// the switch below on purpose — the frame-level chords there are
	// not the composer's, and needing to leave focus mode to press
	// ctrl+g would defeat the mode. See handleTranscriptKey.
	if m.focus == focusTranscript {
		if cmd, claimed := m.handleTranscriptKey(stroke); claimed {
			return m, cmd
		}
	}

	switch stroke {
	case "ctrl+c":
		// Three-step ladder (mirrors internal/tui:626-641 + Claude Code):
		//  1. mid-stream  -> cancel the in-flight turn, don't quit
		//  2. idle, fresh -> set pendingExit + system warning; schedule
		//                    a reset so the warning doesn't latch forever
		//  3. idle, armed -> quit
		if m.state == stateStreaming && m.cancelTurn != nil {
			m.cancelTurn() // goroutine emits turnCancelledMsg
			return m, nil
		}
		if !m.pendingExit {
			m.pendingExit = true
			m.history.Append(Message{
				Role: RoleSystem,
				Text: "press ctrl+c again within 2s to exit",
			})
			m.refreshViewport()
			return m, pendingExitTick()
		}
		m.quitting = true
		return m, m.quitCmd()
	case "ctrl+d":
		// Ctrl+D quits unconditionally — "EOF closes input" is the
		// muscle memory and most TUIs honor it without a warning.
		m.quitting = true
		return m, m.quitCmd()

	case "ctrl+l":
		// Reset viewport scroll to the top. Mirrors the shell-style
		// "redraw / clear screen" muscle memory without actually
		// clearing history (use /clear for that). An explicit jump
		// away from the tail ends follow — otherwise the next repaint
		// would drag the operator straight back down.
		m.follow = false
		m.chatGotoTop()
		return m, nil

	case "end":
		// The counterpart to ctrl+l: jump back to the tail and
		// re-arm follow so a live stream resumes pinning. Without
		// this the only way back from the backlog is holding PgDn
		// until AtBottom() re-arms follow on its own.
		//
		// Deliberately not refreshAndScroll: this is a pure scroll,
		// and that helper also runs syncInputHeight + resize.
		//
		// Claimed ONLY while the input is empty. bubbles' textarea
		// binds end to LineEnd, and "end goes to end-of-line" is far
		// too strong a convention to shadow in a box the operator is
		// composing in — the more so since syncInputHeight grows that
		// box to textareaMaxHeight rows, where line-end is the whole
		// point. With nothing typed there is no line to end, so the
		// key is free and the chat can have it. ctrl+e reaches
		// end-of-line in either state.
		if m.input.Value() == "" {
			m.follow = true
			m.chatGotoBottom()
			return m, nil
		}
		// Non-empty input: fall through to the textarea below.

	case "ctrl+u":
		// Clear the input field + exit history navigation (shell-
		// style "kill line back to start"). Doesn't touch history.
		m.input.Reset()
		m.historyCursor = -1
		m.historyDraft = ""
		m.refreshViewport()
		return m, nil

	case "ctrl+b":
		if m.statusLayout == StatusHeader {
			m.statusLayout = StatusSidebar
		} else {
			m.statusLayout = StatusHeader
		}
		m.resize()
		m.refreshViewport()
		// PersistStatusLayout is host code that writes the operator's
		// pick to the host's config; it ran inline on this bare
		// keystroke with its error discarded (issue #137).
		return m, persistChoiceCmd(m.sessionGen, "status layout",
			m.opts.PersistStatusLayout, m.statusLayout)

	case "tab":
		// Move the keyboard between the composer and the transcript
		// (issue #151). Tab is the one navigation key bubbles'
		// textarea does NOT claim, so taking it costs the composer
		// nothing; the palette claims it earlier in this function
		// for prefix completion, which is the more local meaning
		// while a palette is up.
		m.cycleFocus()
		return m, nil

	case "shift+tab":
		// Cycle the permission-mode chip. The chip flips immediately —
		// this is a bare keystroke and the operator expects the
		// indicator to track the key, not the host — but
		// Options.PermissionMode.Set and .Persist are host code, and
		// Persist writes to the host's config file. Both ran inline
		// here with their errors thrown away (issue #137).
		//
		// permissionModeAppliedMsg rolls the chip back when the host
		// refuses the mode, and surfaces either error as a row.
		if !m.permissionModeWired() {
			return m, nil
		}
		prev := m.permMode
		m.permMode = prev.Next()
		return m, permissionModeCmd(m.opts.PermissionMode, m.sessionGen, prev, m.permMode)

	case "ctrl+g":
		// Open the model picker dialog. Singleton — re-press
		// while open is a no-op (HasID check). The dialog paints
		// a loading body immediately; AvailableModels() runs in
		// the returned Cmd so a slow host can't stall the
		// keystroke (issue #114).
		if _, ok := m.opts.Agent.(ModelSwapper); ok {
			if cmd := m.openModelPicker(); cmd != nil {
				m.refreshViewport()
				return m, cmd
			}
		}
		return m, nil

	case "ctrl+x":
		// Open the expand-single tool-call detail dialog (core-tui
		// #52 tier 1). No-op when there are no tool calls in the
		// session yet — nothing useful to show. Singleton via
		// HasID so re-press while open doesn't stack duplicates.
		if !m.overlayStack.HasID(toolCallDialogID) {
			tools := collectToolCalls(m.history.Snapshot())
			if len(tools) > 0 {
				d := newToolCallDialog(len(tools))
				// Open on the row the operator already pointed at
				// (issue #233). The overlay's own list predates the
				// transcript cursor and its header says so; now that
				// there is a cursor, landing on the newest call
				// instead of the selected one makes the operator walk
				// back to a choice they had already made.
				//
				// Only from focus mode: the marker is drawn only
				// while the transcript holds the keyboard
				// (chatRowMarked), so seeding off selIdx from the
				// composer would aim the overlay with a cursor
				// nobody can see. Every other case — composer focus,
				// a selected row that is not a tool call, no history
				// under the cursor — falls back to most-recent,
				// which is what the binding has always done.
				if m.focus == focusTranscript {
					if sel, ok := m.chatSelectedMessage(); ok {
						if i := indexOfToolCall(tools, sel.ID); i >= 0 {
							d.idx = i
						}
					}
				}
				m.overlayStack.Open(d)
				m.refreshViewport()
			}
		}
		return m, nil

	case "up":
		// Shell-style history recall when the input is empty:
		// step backward through the promptHistory ring. When non-
		// empty the keypress falls through to the textarea so
		// cursor movement still works mid-edit (parity with
		// internal/tui:434-442).
		if strings.TrimSpace(m.input.Value()) == "" || m.historyCursor >= 0 {
			m.recallPrompt(-1)
			return m, nil
		}
	case "down":
		// Forward through history when actively navigating;
		// fall through to textarea cursor movement otherwise
		// (more common while composing).
		if m.historyCursor >= 0 {
			m.recallPrompt(+1)
			return m, nil
		}

	case "enter":
		// Submit (R-CHAT-1). When idle: dispatch as a slash command
		// if the input begins with `/`, otherwise append the typed
		// text as a RoleUser message and start an agent turn. When
		// streaming (R-CHAT-10): append to the prompt queue and clear
		// the input; the queue drains one entry per turn-end.
		text := strings.TrimSpace(m.input.Value())
		// /clear confirmation: the prior /clear submission armed
		// confirmingClear. The prompt says "press enter for y/yes",
		// so a bare Enter (empty text) counts as the y/yes answer;
		// any typed text other than y/yes cancels. This branch
		// runs BEFORE the empty-input early-return below so the
		// bare-Enter path doesn't get swallowed.
		if m.confirmingClear {
			m.confirmingClear = false
			m.input.Reset()
			lower := strings.ToLower(text)
			if text == "" || lower == "y" || lower == "yes" {
				m.history.Reset()
				m.resetChatSelection()
				m.refreshViewport()
				return m, nil
			}
			m.history.Append(Message{Role: RoleSystem, Text: "clear cancelled"})
			m.refreshViewport()
			return m, nil
		}
		if text == "" {
			return m, nil
		}
		if m.state == stateStreaming {
			m.enqueueDuringStream(text)
			m.input.Reset()
			m.refreshViewport()
			return m, nil
		}
		if strings.HasPrefix(text, "/") {
			return m.dispatchSlash(text)
		}
		// Issue #22 — LiveAgent mode bypasses the per-turn Run
		// path entirely. Operator submissions flow through
		// InjectableAgent.Inject when available; otherwise the
		// TUI logs a one-time "read-only view" system note and
		// discards the typed text (the issue's "no-op" branch,
		// surfaced explicitly so the operator knows why nothing
		// happened).
		if m.liveMode {
			m.recordPrompt(text)
			m.input.Reset()
			if injector, ok := m.opts.Agent.(InjectableAgent); ok {
				// Inject feeds the host's stream; events flow back
				// through the same Events(ctx) iterator and land
				// in scrollback like everything else.
				if err := injector.Inject(text); err != nil {
					m.history.Append(Message{Role: RoleError, Text: "inject failed: " + err.Error()})
					m.refreshViewport()
					return m, nil
				}
				// Render the typed prompt as a normal user row
				// so the operator sees what they sent — the
				// host's event stream may not echo it back.
				m.history.Append(Message{Role: RoleUser, Text: text})
				// Issue #148: the submit IS the start of the turn,
				// so it is where the waiting indicator starts.
				// Waiting for the first partial chunk to open the
				// stretch leaves the one window the operator most
				// needs an indicator for — prompt sent, host
				// thinking, nothing on screen yet — completely
				// blank, and leaves a host that only ever emits
				// committed messages with no indicator at all.
				//
				// Only the false→true flip arms a tick chain; an
				// Inject during a stretch that is already running
				// (the host is mid-answer and the operator adds a
				// steer) rides the live one.
				var spin tea.Cmd
				if m.beginLiveStretch() {
					spin = m.armSpinner()
				}
				m.refreshViewport()
				return m, spin
			}
			if !m.liveReadOnlyNoted {
				m.liveReadOnlyNoted = true
				m.history.Append(Message{
					Role: RoleSystem,
					Text: "Read-only view — this LiveAgent host doesn't implement Inject(), so typing is disabled. Use Ctrl+C to quit.",
				})
				m.refreshViewport()
			}
			return m, nil
		}
		// Record the non-slash, non-empty prompt in history so
		// ↑/↓ can recall it next time. recordPrompt dedupes
		// consecutive duplicates + caps the ring at promptHistoryCap.
		m.recordPrompt(text)
		// Operator-initiated turn resets the auto-continue cap so
		// the next streak gets the full budget. (Issue #9.)
		m.consecutiveAutoContinues = 0
		out := m.submitTurn(text)
		return out, out.armSpinner()

	case "shift+enter", "ctrl+j", "alt+enter":
		// Insert a newline (R-CHAT-1). All three forms are accepted
		// because terminals encode "modifier + enter" inconsistently
		// (see defaultNewlineHint comments). Whichever combo the
		// operator actually used becomes the footer hint going
		// forward so it stops suggesting something that doesn't
		// work in their terminal.
		m.newlineHint = stroke
		fakeEnter := tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(fakeEnter)
		// The newline just changed the line count, so run the same
		// reconciliation the forwarded-keystroke path at the bottom
		// of this function does. This arm returns early, so without
		// it the box stayed at its old height until the operator
		// typed something else — the one gesture whose whole purpose
		// is to add a row was the one that did not grow the box
		// (issue #121).
		if m.syncInputHeight() {
			m.resize()
			m.refreshViewport()
		}
		return m, cmd

	case "?":
		// Walk the bottom-anchored stacked help panel: open it, page
		// through whatever does not fit the terminal, then close. Only
		// fires when input is empty so users can still type `?`
		// mid-sentence without hijacking the key — which is also what
		// makes it safe to page on, and why the panel does not have to
		// take pgup / pgdn away from the chat to be height-aware
		// (issue #119, help.go).
		//
		// The empty-input condition exists to protect typing, so it
		// lapses when the transcript holds the keyboard and there is
		// no typing to protect (issue #151). Without that, `?` would
		// be the one key the focus-mode legend advertises that a
		// half-written draft could silently disable.
		if m.focus == focusTranscript || strings.TrimSpace(m.input.Value()) == "" {
			m.advanceHelp()
			m.resize()
			m.refreshViewport()
			return m, nil
		}
	}

	// Forward unmatched keys to the input field for typing. Viewport
	// gets the message too so PgUp/PgDn scroll the chat even while
	// the input is focused. Those two are the whole chat-scroll
	// keymap: NewModel replaces the viewport's vim-flavored default
	// bindings with the non-letter forms only (they'd otherwise eat
	// typed text), and of those, ctrl+u / ctrl+d are claimed earlier
	// in this switch. bubbles v2's viewport binds neither Home nor
	// End. Home stays with the textarea (ctrl+l is the goto-top key);
	// End reaches here only when the input is non-empty, in which
	// case the textarea's LineEnd is what the operator meant.
	//
	// The composer is fed only when it holds the keyboard (issue
	// #151). A blurred textarea already drops everything it is
	// handed, so this guard is not what keeps text out of the
	// prompt — it is what stops that from being an invariant we
	// hold on loan from bubbles' Update.
	var taCmd tea.Cmd
	if m.focus == focusInput {
		m.input, taCmd = m.input.Update(msg)
	}
	m.forwardChatScroll(msg)
	// PgUp / PgDn just moved the viewport (nothing else reaches it);
	// re-read the follow intent before anything touches the layout.
	m.syncFollow()
	// Auto-grow textarea: if typing (or pasting) bumped the line
	// count, re-clamp the textarea height between min/max and
	// re-resolve the layout so the viewport shrinks to make room.
	// refreshViewport re-snaps to the tail from m.follow — sampling
	// AtBottom() here instead would read the post-resize geometry and
	// drop follow exactly like the WindowSizeMsg path did (#93).
	if m.syncInputHeight() {
		m.resize()
		m.refreshViewport()
	}
	// Refresh palette state from the updated input — opens a new
	// palette on a fresh `/` or `@` trigger, closes the active one
	// when the trigger is deleted, or updates the filter on any
	// other keystroke. Opening returns the Cmd that fills the panel
	// off the event loop (issue #114); re-filtering returns nil.
	paletteCmd := m.refreshPalette()
	return m, tea.Batch(taCmd, paletteCmd)
}

// refreshPalette re-derives palette state from the current textarea
// content. Called from handleKey after every forwarded keystroke so
// the palette opens / closes / re-filters in lock-step with the user
// typing into the input.
//
// Triggering rules (R-PAL-1 / R-PAL-2):
//   - `/` at the very start of input opens a slash palette.
//   - `@` at a word boundary anywhere opens a file palette.
//   - Deleting the trigger char closes the active palette.
//
// Returns a non-nil Cmd only on the keystroke that OPENS a palette:
// the item source is a host call (SlashProvider.SlashCommands) or a
// full directory walk (scanFileItems), and neither may run on the
// Update goroutine. The panel opens immediately either way and fills
// in when the reply lands.
func (m *Model) refreshPalette() tea.Cmd {
	value := m.input.Value()

	if m.palette == nil {
		if strings.HasPrefix(value, "/") {
			// Built-ins paint immediately; agent-provided
			// SlashProvider commands (/btw, /subagent, …) merge in
			// via slashCommandsMsg so they stay discoverable in the
			// palette, not just executable when typed.
			m.paletteSeq++
			cmd := m.slashCommandsCmd(m.paletteSeq)
			m.palette = newSlashPalette(0, m.paletteSeq, cmd != nil)
			m.palette.filter = value[1:]
			m.resize()
			return cmd
		}
		if idx := lastAtTokenStart(value); idx >= 0 {
			m.paletteSeq++
			m.palette = newFilePalette(idx, m.paletteSeq)
			m.palette.filter = atFilterFrom(value, idx)
			m.resize()
			return scanFileItemsCmd(m.opts.PathScope, m.sessionGen, m.paletteSeq)
		}
		return nil
	}

	// Palette is open — verify the trigger char still sits at
	// triggerPos, otherwise close it.
	if m.palette.triggerPos >= len(value) {
		m.palette = nil
		m.resize()
		return nil
	}
	if string(value[m.palette.triggerPos]) != m.palette.triggerRune() {
		m.palette = nil
		m.resize()
		return nil
	}
	if m.palette.kind == paletteSlash {
		m.palette.filter = value[1:]
	} else {
		m.palette.filter = atFilterFrom(value, m.palette.triggerPos)
	}
	// Clamp cursor to the new filtered list.
	if n := len(m.palette.filtered()); m.palette.cursor >= n {
		if n > 0 {
			m.palette.cursor = n - 1
		} else {
			m.palette.cursor = 0
		}
	}
	// Re-filtering an already-populated palette needs no fetch: the
	// items were snapshotted at open and the filter is pure string
	// work over them.
	return nil
}

// lastAtTokenStart returns the byte index of the most recent `@` in s
// that sits at a word boundary (start of string or after whitespace),
// or -1 when no such `@` exists.
func lastAtTokenStart(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] != '@' {
			continue
		}
		if i == 0 {
			return 0
		}
		switch s[i-1] {
		case ' ', '\t', '\n':
			return i
		}
	}
	return -1
}

// atFilterFrom returns the filter text following an `@` at position
// triggerPos in s — everything after `@` up to (but not including)
// the next whitespace or end-of-string.
func atFilterFrom(s string, triggerPos int) string {
	rest := s[triggerPos+1:]
	if sp := strings.IndexAny(rest, " \t\n"); sp >= 0 {
		return rest[:sp]
	}
	return rest
}

// paletteComplete extends the input with the longest common prefix
// of the matched palette items (Tab while palette is open). Leaves
// the palette open for further filtering. Idempotent when the filter
// is already the full common prefix.
func (m Model) paletteComplete() tea.Model {
	if m.palette == nil {
		return m
	}
	extension := m.palette.completion()
	if extension == "" {
		return m
	}
	value := m.input.Value()
	tokenEnd := m.palette.triggerPos + 1 + len(m.palette.filter)
	if tokenEnd > len(value) {
		tokenEnd = len(value)
	}
	newValue := value[:m.palette.triggerPos] + m.palette.triggerRune() + extension + value[tokenEnd:]
	m.input.SetValue(newValue)
	// The trigger char is untouched, so this only re-filters the
	// snapshot the palette already holds — never a fresh fetch.
	_ = m.refreshPalette()
	return m
}

// submitTurn appends the user's message, kicks off the agent dispatch
// goroutine, schedules a spinner tick, and flips to the streaming
// state. The textarea stays focused so the operator can type ahead
// (R-CHAT-10 prompt queueing). Called from the Enter handler and
// from maybeDrainQueue.
//
// Before dispatching, expands any `@<path>` tokens by reading the
// referenced files and appending them under a "Referenced files:"
// section so the model sees inline content (R-PAL-2 + R-CHAT-13).
// Failed reads are surfaced as system messages so the operator can
// fix typos; the prompt still ships with the readable refs.
func (m Model) submitTurn(text string) Model {
	m.history.Append(Message{Role: RoleUser, Text: text})

	// Resolve @-refs against the operator's view of the filesystem.
	expanded, refs, diags := expandAtRefs(text, readFileSafe(maxAtRefBytes))
	for _, d := range diags {
		m.history.Append(Message{Role: RoleError, Text: d})
	}
	if len(refs) > 0 {
		m.history.Append(Message{
			Role: RoleSystem,
			Text: "Inlined file references: " + strings.Join(refs, ", "),
		})
	}
	text = expanded
	m.input.Reset()
	m.state = stateStreaming
	// Origin for the thinking line's elapsed readout (issue #111).
	// Through m.nowFn so tests can pin it.
	m.turnStarted = m.nowFn()
	m.inProgressText = ""
	m.currentUsage = nil
	m.currentCost = 0
	m.currentModel = ""
	m.inProgressStablePrefix = ""
	m.inProgressStableRender = ""
	m.toolActive = false
	m.spinnerFrame = 0
	m.spinnerActive = true
	// A new turn starts a new spinner animation, so it retires the
	// previous chain (issue #112). Turn-level granularity is the
	// point: a queue drain or an auto-continue re-enters submitTurn
	// while the finishing turn's tick is still in flight, and both
	// turns live in the same session, so sessionGen would not
	// separate them.
	m.spinnerGen++
	for k := range m.seenToolIDs {
		delete(m.seenToolIDs, k)
	}
	m.cancelTurn = m.startAgentTurn(m.opts.Agent, text)
	// Operator-initiated submit always scrolls to bottom — they want
	// to see their own message land and the response start, even if
	// they'd been scrolled up reading backlog. Re-arming follow is
	// part of that: the reply streams in below, and they asked for it.
	m.follow = true
	m.refreshViewport()
	m.chatGotoBottom()
	// Spinner tick scheduled separately from event listener; both
	// stream their own messages into Update.
	return m
}

// applyStreamChunk handles a streamChunkMsg from the agent. Accumulates
// partial tokens into m.inProgressText, flips the spinner from
// tool-active back to model-active (R-CHAT-3), and re-renders the
// viewport so the user sees the in-progress message grow.
func (m *Model) applyStreamChunk(msg streamChunkMsg) {
	m.toolActive = false
	// Stream chunk arriving after a tool call means that tool has
	// finished — bump its Version so the lazy-render cache
	// re-renders the row with the inactive glyph + dimmer color.
	if m.activeToolID != 0 {
		m.history.BumpVersion(m.activeToolID)
		m.activeToolID = 0
	}
	if msg.partial {
		m.inProgressText += msg.text
	} else {
		// Committed full text — overwrite (some agents echo the
		// full message at turn-end).
		m.inProgressText = msg.text
	}
	// Issue #22: LiveAgent mode has no turnDoneMsg — Partial=false
	// IS the commit signal, so we drive spinner state + finalize
	// the in-progress assistant row from here.
	if m.liveMode {
		if msg.partial {
			m.liveLastPartialAt = time.Now()
			// Spinner active while tokens flow. A no-op when the
			// operator's own Inject already opened this stretch
			// (issue #148) — see beginLiveStretch.
			m.beginLiveStretch()
		} else {
			m.liveLastCommitAt = time.Now()
			// Commit: freeze the Glamour render on the just-
			// finalized assistant row (mirrors finalizeTurn's
			// commit path) and stop the spinner.
			//
			// Issue #57: stamp current* fields onto the Message so
			// renderTurnFooter has something to show even in observer
			// mode (LiveAgent has no turnDoneMsg → finalizeTurn never
			// fires → footer stays blank otherwise). current* fields
			// are populated by usageMsg (per-event, when the adapter
			// emits ev.Usage) and turnSummaryMsg (turn-complete);
			// they may be zero here when turn-complete lands AFTER
			// this commit — the turnSummaryMsg / usageUpdateMsg
			// handlers back-annotate via StampLatestAssistantFooter
			// in that case, so both orderings converge on a stamped
			// footer.
			if strings.TrimSpace(m.inProgressText) != "" {
				mr := m.ensureMarkdown()
				msg := Message{
					Role:     RoleAssistant,
					Text:     m.inProgressText,
					Rendered: mr.renderMarkdown(m.inProgressText),
					// Stamp the wrap width so the resize reflow
					// (issue #104) can tell this render is already
					// correct for the current viewport.
					renderedWidth: mr.width,
					Model:         m.currentModel,
					Usage:         m.currentUsage,
					CostUSD:       m.currentCost,
				}
				m.history.Append(msg)
				m.inProgressText = ""
				m.inProgressStablePrefix = ""
				m.inProgressStableRender = ""
			}
			m.endLiveStretch()
		}
	}
	m.markViewportDirty()
}

// applyToolCall handles a toolCallMsg. Dedup by ID (R-CHAT-5),
// close the in-progress assistant segment (so subsequent chunks
// land in a NEW segment below the tool row — without this the
// pre-tool text and post-tool text would merge into one blob with
// the tool row floating below both), append a one-line tool row,
// flip the spinner to tool-active.
//
// Args are summarized via toolArgHint, which knows the common
// built-ins (bash → "$ <cmd>", read_file → path, grep → "pattern
// in dir", etc.) and falls back to the first arg's value for
// unknown tools.
func (m *Model) applyToolCall(msg toolCallMsg) {
	if msg.id != "" {
		if m.seenToolIDs[msg.id] {
			return
		}
		m.seenToolIDs[msg.id] = true
	}

	// Segment boundary: commit whatever assistant text streamed
	// before this tool call as its own finalized Message so the
	// next stream chunks build up a fresh in-progress segment
	// below the tool row. Glamour render is cached on the segment
	// to match finalizeTurn's behavior. Also reset the
	// incremental cache so the post-tool segment starts fresh.
	if strings.TrimSpace(m.inProgressText) != "" {
		mr := m.ensureMarkdown()
		m.history.Append(Message{
			Role:          RoleAssistant,
			Text:          m.inProgressText,
			Rendered:      mr.renderMarkdown(m.inProgressText),
			renderedWidth: mr.width,
		})
		m.inProgressText = ""
		m.inProgressStablePrefix = ""
		m.inProgressStableRender = ""
	}

	hint := toolArgHint(msg.name, msg.args)
	if hint == "" && len(msg.args) > 0 {
		// Fallback for unknown tools: first arg value, truncated.
		for _, v := range msg.args {
			hint = trimToolArg(fmt.Sprint(v), 200)
			break
		}
	}
	// A previous tool call had already transitioned to "active"
	// — if a NEW tool call arrives without any intervening text,
	// that older tool is also done. Bump its Version so it
	// renders with the inactive glyph too.
	if m.activeToolID != 0 {
		m.history.BumpVersion(m.activeToolID)
	}
	m.history.Append(Message{
		Role:        RoleTool,
		ToolName:    msg.name,
		ToolArgs:    hint,
		ToolPreview: renderToolPreview(msg.name, msg.args, m.styles),
		ToolCallID:  msg.id,
		ToolArgsMap: msg.args,
	})
	m.activeToolID = m.history.LastID()
	m.toolActive = true
	m.markViewportDirty()
}

// applyToolResult attaches a tool's completion (success result or
// error) to the matching RoleTool row by wire-level ToolCallID.
// Re-renders the row's ToolPreview through renderToolPreviewWithResult
// so the operator sees both the original call info and the result
// content (read_file body, bash stdout, error text, etc.) inline.
//
// Silently no-ops when the result has no ID or no matching call —
// adapters that emit results out of order with retries shouldn't
// crash the TUI; the worst outcome is a missed preview update.
func (m *Model) applyToolResult(msg toolResultMsg) {
	if msg.id == "" {
		return
	}
	idx := m.history.FindByToolCallID(msg.id)
	if idx < 0 {
		return
	}
	snap := m.history.Snapshot()
	if idx >= len(snap) {
		return
	}
	preview := renderToolPreviewWithResult(
		msg.name, snap[idx].ToolArgsMap, msg.response, msg.err, m.styles,
	)
	// Tier 3 (core-tui #60 / SSE spec v1.2.0): append the muted
	// `[2.4s]` badge to the compact preview when the host reported
	// per-call latency. Zero suppresses the badge silently so
	// pre-v1.2.0 servers keep rendering unchanged.
	if badge := renderLatencyBadge(msg.latencyMs, m.styles); badge != "" {
		preview += badge
	}
	// SSE spec v1.3.0: append the muted `[12k→2k tok · struct]` badge
	// when the digest wrap emitted savings for this call. Sits AFTER
	// the latency badge so the two chips read left-to-right in wall-
	// clock-then-cost order. Passthrough / missing token counts
	// suppress silently — pre-v1.3.0 servers render unchanged.
	if badge := renderSavingsBadge(msg.savings, m.styles); badge != "" {
		preview += badge
	}
	// Issue #71: this row was tailing a sync subagent's turns while
	// it ran. The result is in, so collapse the live block to one
	// line of counts pointing at `/subagents <name>` — the whole log
	// is in the overlay, and leaving six turn lines under every
	// finished subagent would bury the transcript.
	if t, tailed := m.stopSubagentTail(msg.id); tailed {
		if line := renderSubagentTailSummary(t, m.styles); line != "" {
			if preview != "" {
				preview += "\n"
			}
			preview += line
		}
	}
	if m.opts.ToolDetailVerbose {
		// Tier 2 (core-tui #52): append the full args + response
		// dump under the compact preview when the operator has opted
		// in. Compact stays useful as a quick summary; verbose gives
		// operators the raw data without hunting through the SSE
		// stream. Skipped when the detail block is empty (unknown
		// tools with no args/response and no error).
		if detail := renderToolDetail(snap[idx].ToolArgsMap, msg.response, msg.err, m.styles); detail != "" {
			preview = preview + "\n" + detail
		}
	}
	m.history.SetToolPreview(idx, preview)
	m.history.SetToolResult(idx, msg.response, msg.err, msg.latencyMs, msg.savings)
	m.markViewportDirty()
}

// toolArgHint produces the muted-italic detail that renders after
// the bold tool name. Knows the core-agent built-ins so a `bash`
// call reads `⚙ bash · $ ls -la /tmp` rather than the generic
// first-arg dump. Lifted from internal/tui/model.go:397-464.
func toolArgHint(name string, args map[string]any) string {
	if args == nil {
		return ""
	}
	pick := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := args[k]; ok {
				if s, ok := v.(string); ok && s != "" {
					return s
				}
			}
		}
		return ""
	}
	switch name {
	case "bash":
		if cmd := pick("command", "cmd"); cmd != "" {
			return "$ " + strings.ReplaceAll(strings.ReplaceAll(cmd, "\n", " "), "\t", " ")
		}
	case "read_file", "write_file", "edit_file":
		return pick("path", "file", "filename")
	case "read_many_files":
		if pattern := pick("pattern"); pattern != "" {
			return pattern
		}
		if paths, ok := args["paths"].([]any); ok && len(paths) > 0 {
			if s, ok := paths[0].(string); ok {
				if len(paths) > 1 {
					return fmt.Sprintf("%s (+%d)", s, len(paths)-1)
				}
				return s
			}
		}
	case "grep", "glob":
		pattern := pick("pattern", "query")
		path := pick("path", "dir")
		switch {
		case pattern != "" && path != "":
			return strconv.Quote(pattern) + " in " + path
		case pattern != "":
			return strconv.Quote(pattern)
		case path != "":
			return path
		}
	case "list_files", "ls", "list_dir":
		return pick("path", "dir")
	case "go_build", "go_test", "go_vet":
		if p := pick("pattern"); p != "" {
			return p
		}
		return "./..."
	case "go_doc":
		return pick("target")
	case "go_symbol_find":
		return pick("name")
	case "go_implements":
		return pick("interface")
	case "todo":
		action := pick("action")
		if action == "add" {
			if text := pick("text"); text != "" {
				return "add: " + text
			}
		}
		return action
	}
	return ""
}

// finalizeTurn closes the active turn: appends the in-progress
// assistant text as a finalized Message with cached Glamour render +
// Usage / Model / Elapsed metadata, flips back to idle, re-focuses
// the input. When notice is non-empty, an extra system / error row
// is appended (used for "(interrupted)" and turnErr error text).
func (m *Model) finalizeTurn(elapsed time.Duration, notice string) {
	if m.cancelTurn != nil {
		m.cancelTurn()
		m.cancelTurn = nil
	}
	m.state = stateIdle
	m.spinnerActive = false
	// The turn's animation is over, so its elapsed origin goes with
	// it (issue #111). Clearing state, spinnerActive and turnStarted
	// together is what keeps turnInFlight and the readout agreeing:
	// the field's invariant is "non-zero iff an animation is live",
	// and leaning on a render-path gate to hide a stale value is
	// what issue #135 turned out to be.
	m.turnStarted = time.Time{}

	// Commit the streamed text as a Message. Skip when empty (the
	// agent emitted only tool calls, no assistant prose).
	if strings.TrimSpace(m.inProgressText) != "" {
		mr := m.ensureMarkdown()
		msg := Message{
			Role:          RoleAssistant,
			Text:          m.inProgressText,
			Rendered:      mr.renderMarkdown(m.inProgressText),
			renderedWidth: mr.width,
			Model:         m.currentModel,
			Usage:         m.currentUsage,
			CostUSD:       m.currentCost,
			Elapsed:       elapsed,
		}
		m.history.Append(msg)
	}
	m.inProgressText = ""

	switch {
	case notice == "(interrupted)":
		m.history.Append(Message{Role: RoleSystem, Text: notice})
		m.markInFlightTerminal(false, "interrupted")
	case notice != "":
		m.history.Append(Message{Role: RoleError, Text: notice})
		m.markInFlightTerminal(false, notice)
	default:
		m.markInFlightTerminal(true, "")
	}

	// Re-arm the textarea, but only if it is the region that owns
	// the keyboard. A turn ending is not a reason to yank focus out
	// of a transcript the operator deliberately moved it to, and
	// re-focusing without moving m.focus would put the hardware
	// cursor back on a composer that is still inert (issue #151).
	if m.focus == focusInput {
		_ = m.input.Focus()
	}
	m.refreshViewport()
}

// dispatchSlash handles `/name args...` submitted from the input.
// Tries the TUI's built-in dispatcher first (slash_builtin.go);
// unrecognized names fall through to the agent's optional
// SlashProvider (/btw, /subagent, etc.); anything still unmatched
// surfaces as a system row pointing at /help.
func (m Model) dispatchSlash(text string) (tea.Model, tea.Cmd) {
	rest := strings.TrimPrefix(text, "/")
	name, args, _ := strings.Cut(rest, " ")
	name = strings.ToLower(name)
	args = strings.TrimSpace(args)

	// Every submitted /cmd turns the slash generation over, built-ins
	// included: what makes a pending reply stale is the operator
	// having moved on, and /help is as much moving on as /btw is.
	// The two-stage paths stamp their Cmds with this and Update drops
	// anything overtaken (model.go's slashSeq).
	m.slashSeq++

	if handled, model, cmd := m.dispatchBuiltinSlash(name, args); handled {
		return model, cmd
	}

	provider, ok := m.opts.Agent.(SlashProvider)
	if !ok {
		m.history.Append(Message{
			Role: RoleSystem,
			Text: "unknown command /" + name + " — the agent doesn't expose any slash commands",
		})
		m.input.Reset()
		m.refreshViewport()
		return m, nil
	}

	// The name match reads the host's SlashCommands() and the
	// synchronous InvokeSlash below took context.Background() — both
	// on the event loop, the second one also with the contract
	// inverted (issue #137). They move off together: the match is only
	// ever asked in order to decide whether to invoke.
	//
	// Which of the two provider shapes will run is decided HERE, on
	// the loop, by type assertion; the closure never asks the model
	// anything. Only the plain synchronous shape can be driven to
	// completion off-loop — the async one returns through Update so
	// applySlashDispatch can arm the cancel func, the in-flight record
	// and the toast, none of which a goroutine may touch.
	_, async := provider.(AsyncSlashProvider)
	m.input.Reset()
	m.refreshViewport()
	return m, slashDispatchCmd(provider, m.sessionGen, m.slashSeq, name, args, !async)
}

// applySlashDispatch is the Update-side half of a host /cmd (issue
// #137). Runs only once the gen and seq guards have passed, so an
// "unknown command" row can no longer land under the output of
// whatever the operator typed while the host was answering.
//
// The three cases: no match at all, a plain provider already invoked
// out of line, or an async provider still to be started.
func (m Model) applySlashDispatch(msg slashDispatchedMsg) (tea.Model, tea.Cmd) {
	if !msg.matched {
		m.history.Append(Message{
			Role: RoleSystem,
			Text: "unknown command /" + msg.name + " — type / to see what's available",
		})
		m.refreshViewport()
		return m, nil
	}
	if msg.invoked {
		return m.applySlashResult(msg.name, msg.res, msg.err)
	}
	provider, ok := m.opts.Agent.(SlashProvider)
	if !ok {
		// The agent was replaced mid-match by one with no slash
		// surface. Nothing to invoke and nothing worth saying — the
		// swap itself already wrote its own row.
		return m, nil
	}
	name, args := msg.name, msg.args
	// Issue #10: hosts that implement AsyncSlashProvider get the
	// non-blocking path so any network / file I/O the call needs runs
	// off the Update goroutine. The TUI stays responsive (cursor
	// blinks, spinner ticks, Ctrl+C lands) while the host works; the
	// eventual result arrives via slashResultMsg and goes through the
	// same modal/system-message/error machinery below.
	//
	// The preamble (issue #16) is the host's chance to put a
	// chat-visible "running…" row next to the prompt the operator just
	// typed, for a call slow enough that the bottom-bar toast alone is
	// easy to miss. It lands as a RoleSystem row BEFORE the goroutine
	// is launched. An empty preamble skips the row, which is what a
	// host with nothing to say returns.
	if asyncProv, ok := provider.(AsyncSlashProvider); ok {
		if refusal, refused := m.refuseConcurrentSlash(name); refused {
			return refusal, nil
		}
		// Cancellable ctx so the Esc handler can fire cancelSlash and
		// the host can bail per the AsyncSlashProvider contract.
		// Sticky toast + status-line segment land via m.inFlightSlash;
		// the toastClearMsg handler honors the sticky bit so the
		// indicator persists for the full duration of the call.
		ctx, cancel := context.WithCancel(context.Background())
		m.cancelSlash = cancel
		m.inFlightSlash = &slashFlight{name: name, startedAt: time.Now()}
		m.toast = "▸ /" + name + " running…"
		m.toastSetAt = time.Now()
		preamble, ch := asyncProv.InvokeSlashAsync(ctx, name, args)
		if preamble != "" {
			m.history.Append(Message{Role: RoleSystem, Text: preamble})
		}
		m.refreshViewport()
		return m, awaitSlashChannel(name, ch)
	}
	// Not the async shape, and the match Cmd didn't invoke: the agent
	// was swapped for a plain provider while the match was in flight.
	// Re-issue under the current stamp rather than reaching for
	// InvokeSlash from here — this is the one path back to the loop,
	// and it is not going to become an unbounded inline call again.
	return m, slashDispatchCmd(provider, m.sessionGen, m.slashSeq, name, args, true)
}

// refuseConcurrentSlash applies issue #13's concurrent-slash policy:
// when an async slash is already in flight, log a RoleSystem refusal
// for the new dispatch and return refused=true.
func (m Model) refuseConcurrentSlash(name string) (Model, bool) {
	if m.inFlightSlash == nil {
		return m, false
	}
	m.history.Append(Message{
		Role: RoleSystem,
		Text: "/" + name + " refused — /" + m.inFlightSlash.name + " is still running. Wait for it (or press Esc to cancel) then retry.",
	})
	m.refreshViewport()
	return m, true
}

// awaitSlashChannel returns a tea.Cmd that drains exactly one value
// from the host's result channel and forwards it as a slashResultMsg.
// Kept separate from the dispatch branch that calls it so the
// single-shot contract reads in one place.
func awaitSlashChannel(name string, ch <-chan SlashResultOrErr) tea.Cmd {
	return func() tea.Msg {
		out, ok := <-ch
		if !ok {
			return slashResultMsg{name: name}
		}
		return slashResultMsg{name: name, res: out.Res, err: out.Err}
	}
}

// applySlashResult is the shared post-processing for both the
// synchronous and async slash paths. Returns the new model + a
// Cmd (typically nil, or a listener batch when SwitchTo triggers
// a session swap).
func (m Model) applySlashResult(name string, res SlashResult, err error) (tea.Model, tea.Cmd) {
	if err != nil {
		m.history.Append(Message{
			Role: RoleError,
			Text: "/" + name + " failed: " + err.Error(),
		})
		m.refreshViewport()
		return m, nil
	}
	if res.ModalAnswer != nil {
		m.sideAnswer = res.ModalAnswer
		m.scroll().reset()
	}
	if res.SystemMessage != "" {
		m.history.Append(Message{Role: RoleSystem, Text: res.SystemMessage})
	}
	m.resize()
	m.refreshViewport()

	// Issue #48: SwitchTo requests a mid-run detach + attach. Any
	// SystemMessage / ModalAnswer above has already landed against
	// the OUTGOING session (documented in SlashResult godoc);
	// applySwitchTarget wipes history and repaints against the
	// incoming Agent.
	if res.SwitchTo != nil {
		if res.SwitchTo.Agent == nil {
			m.history.Append(Message{
				Role: RoleError,
				Text: "/" + name + ": SwitchTo has nil Agent — ignored",
			})
			m.refreshViewport()
			return m, nil
		}
		return m, m.applySwitchTarget(res.SwitchTo)
	}
	return m, nil
}

// applySwitchTarget detaches the current Agent's local subscriptions
// and attaches to tgt.Agent (issues #48 / #53). Bumps sessionGen so
// stragglers from the outgoing session are dropped by the guards in
// Update; cancels the local ctxs we own (releasing SSE sockets /
// halting embedded model calls); resets streaming + modal + queue +
// history state; swaps non-nil Options fields; re-detects LiveAgent
// on the new Agent; returns a fresh listener Cmd batch (plus a live-
// stream spawn Cmd when applicable).
//
// The outgoing Agent handle is NOT touched — host owns its lifecycle
// (see SwitchTarget godoc). Server-side sessions are unaffected;
// detach is local only.
func (m *Model) applySwitchTarget(tgt *SwitchTarget) tea.Cmd {
	m.sessionGen++

	// Step 1 — release local subscriptions on the outgoing Agent.
	// These cancels close SSE sockets / halt embedded model calls;
	// remote daemons observe a dropped reader and keep the session
	// running per their own reattach policy.
	if m.cancelTurn != nil {
		m.cancelTurn()
		m.cancelTurn = nil
	}
	if m.cancelSlash != nil {
		m.cancelSlash()
		m.cancelSlash = nil
	}
	if m.cancelLiveStream != nil {
		m.cancelLiveStream()
		m.cancelLiveStream = nil
	}

	// Step 2 — reset per-session state so the new Agent paints on
	// a blank canvas. Chrome (theme, size, permMode) survives; the
	// overlay stack survives too so a picker dialog can close
	// itself normally after returning the switch Cmd.
	m.state = stateIdle
	m.spinnerActive = false
	m.turnStarted = time.Time{}
	m.inProgressText = ""
	m.inProgressStablePrefix = ""
	m.inProgressStableRender = ""
	m.currentUsage = nil
	m.currentCost = 0
	m.currentModel = ""
	m.toolActive = false
	m.activeToolID = 0
	for k := range m.seenToolIDs {
		delete(m.seenToolIDs, k)
	}
	// Tails and the not-a-subagent cache are both per-host facts:
	// the new Agent has its own subagents and its own tool names.
	clear(m.subagentTails)
	clear(m.subagentNotTail)
	m.queue = nil
	// A prompt that is on screen when the session changes has to be
	// answered, not merely forgotten. Clearing the field leaves the
	// outgoing host's AskApproval / Elicit call parked on a response
	// channel that no longer has a writer; it comes back only if that
	// host happened to derive the call's ctx from the turn ctx step 1
	// cancels, which is the host's business and not something the
	// contract requires of it. Deny and cancel are the honest answers:
	// the operator switched away instead of approving, so nothing may
	// proceed on their behalf. Both dispatchers must run before step 4
	// swaps opts, so the reply reaches the Prompter / Elicitor that
	// asked, and the decision rows they append are wiped with the rest
	// of the outgoing transcript by step 3 rather than following the
	// operator into the new session.
	if m.pendingPermission != nil {
		m.dispatchPermission(DecisionDeny)
	}
	// The elicit form is a question on the overlay stack, so it is
	// resolved by id rather than cleared: resolveAll would take the
	// pickers with it, and step 2 keeps those on purpose so a picker
	// can close itself after returning the switch Cmd. Superseded
	// rather than escape — the operator did not dismiss anything, and
	// the resolver reads the reason to decide NOT to re-arm the elicit
	// listener, which step 8 below re-arms for the new session.
	m.overlayStack.resolve(elicitDialogID, dismissed{Reason: dismissSuperseded}, m)
	m.pendingPermission = nil
	m.sideAnswer = nil
	m.scroll().reset()
	m.pendingForm = nil
	m.palette = nil
	m.consecutiveAutoContinues = 0
	m.sessionUsage = nil
	m.pushedProvider = ""
	m.pushedContextPct = nil
	m.hostSnap = hostSnapshot{}
	m.liveDisconnected = false
	m.liveReadOnlyNoted = false
	m.liveLastPartialAt = time.Time{}
	m.liveLastCommitAt = time.Time{}
	m.pendingExit = false
	m.confirmingClear = false
	m.inFlightSlash = nil
	m.toast = ""

	// Step 3 — wipe history + list cache + the transcript cursor.
	m.history.Reset()
	if m.listCache != nil {
		m.listCache.reset(m.viewport.Width())
	}
	m.resetChatSelection()

	// Step 4 — swap opts fields per SwitchTarget contract
	// (non-nil / non-zero replaces; nil / zero keeps).
	m.opts.Agent = tgt.Agent
	if tgt.UsageTracker != nil {
		m.opts.UsageTracker = tgt.UsageTracker
	}
	if tgt.Prompter != nil {
		m.opts.Prompter = tgt.Prompter
	}
	if tgt.Elicitor != nil {
		m.opts.Elicitor = tgt.Elicitor
	}
	if tgt.Notifier != nil {
		m.opts.Notifier = tgt.Notifier
	}
	if tgt.Memory != nil {
		m.opts.Memory = tgt.Memory
	}
	if tgt.Skills != nil {
		m.opts.Skills = tgt.Skills
	}
	if tgt.MCPServers != nil {
		m.opts.MCPServers = tgt.MCPServers
	}
	if tgt.Branding != nil {
		m.opts.Branding = *tgt.Branding
	}

	// Step 5 — re-detect LiveAgent capability on the new Agent.
	_, m.liveMode = m.opts.Agent.(LiveAgent)

	// Step 6 — refresh theme (new Agent may report a different
	// provider under AutoProviderTheme) + re-focus the input so
	// the operator can type into the new session immediately.
	m.refreshTheme()
	// Unlike the turn-end re-arm this one resets the focus mode
	// outright (issue #151): a session switch replaces the
	// transcript the operator had focused, so leaving the keyboard
	// parked on someone else's history is not continuity.
	m.setFocus(focusInput)

	// Step 7 — optional post-switch system row so the operator
	// sees which session they landed on.
	if tgt.Note != "" {
		m.history.Append(Message{Role: RoleSystem, Text: tgt.Note})
	}

	// A fresh session starts on its tail, following.
	m.follow = true
	m.refreshViewport()
	m.chatGotoBottom()

	// Step 8 — return fresh listener Cmds. Old blocked listener
	// goroutines that were reading from replaced channels are
	// harmless leaks (no future traffic → GC on program exit;
	// same pattern accepted for LiveAgent teardown).
	cmds := make([]tea.Cmd, 0, 7)
	if c := m.eventListener(); c != nil {
		cmds = append(cmds, c)
	}
	if c := m.promptListener(); c != nil {
		cmds = append(cmds, c)
	}
	if c := m.elicitListener(); c != nil {
		cmds = append(cmds, c)
	}
	if c := m.notifyListener(); c != nil {
		cmds = append(cmds, c)
	}
	if c := m.wakeListener(); c != nil {
		cmds = append(cmds, c)
	}
	if m.liveMode {
		if la, ok := m.opts.Agent.(LiveAgent); ok {
			cmds = append(cmds, m.spawnLiveStreamCmd(la))
		}
	}
	// Restart the render-path host cache refresh cycle for the new
	// session (host_snapshot.go). The outgoing cycle's straggler tick is
	// dropped by the gen guard in Update.
	if c := m.refreshHostSnapshotCmd(); c != nil {
		cmds = append(cmds, c)
	}
	if len(cmds) == 0 {
		return nil
	}
	if len(cmds) == 1 {
		return cmds[0]
	}
	return tea.Batch(cmds...)
}

// promptHistoryCap bounds the shell-style recall buffer. 100 entries
// is plenty for a single session — comparable shells (bash, zsh) keep
// far more on disk but the TUI's buffer is session-only.
const promptHistoryCap = 100

// recordPrompt appends text to the recall buffer, dedupes against the
// most recent entry, caps at promptHistoryCap, and resets the cursor
// so the next ↑ starts from the freshest entry.
func (m *Model) recordPrompt(text string) {
	if text == "" {
		return
	}
	if n := len(m.promptHistory); n > 0 && m.promptHistory[n-1] == text {
		m.historyCursor = -1
		m.historyDraft = ""
		return
	}
	m.promptHistory = append(m.promptHistory, text)
	if len(m.promptHistory) > promptHistoryCap {
		m.promptHistory = m.promptHistory[len(m.promptHistory)-promptHistoryCap:]
	}
	m.historyCursor = -1
	m.historyDraft = ""
}

// recallPrompt walks the recall buffer. delta = -1 steps back (older),
// +1 forward (newer). The first backward step saves the operator's
// in-flight input as historyDraft so stepping all the way forward
// past the newest entry restores what they were typing.
func (m *Model) recallPrompt(delta int) {
	if len(m.promptHistory) == 0 {
		return
	}
	if m.historyCursor < 0 {
		// First nav. Save the in-flight draft so we can restore it
		// on the eventual forward-past-newest step.
		m.historyDraft = m.input.Value()
		// Start from "past the newest" so ↑ lands on the newest.
		m.historyCursor = len(m.promptHistory)
	}
	next := m.historyCursor + delta
	if next < 0 {
		next = 0
	}
	if next >= len(m.promptHistory) {
		// Stepped past the newest entry → exit history mode and
		// restore whatever the operator had been composing.
		m.historyCursor = -1
		m.input.SetValue(m.historyDraft)
		m.historyDraft = ""
		if m.syncInputHeight() {
			m.resize()
		}
		m.refreshViewport()
		return
	}
	m.historyCursor = next
	m.input.SetValue(m.promptHistory[next])
	if m.syncInputHeight() {
		m.resize()
	}
	m.refreshViewport()
}

// sliceContains is a tiny generic membership check used by dispatchSlash
// to test slash-command aliases. We avoid pulling slices.Contains so the
// code reads top-to-bottom without an import jump.
func sliceContains(xs []string, target string) bool {
	for _, x := range xs {
		if x == target {
			return true
		}
	}
	return false
}

// dispatchPermission writes the operator's decision back to the
// pending Prompter flow, invokes the host's AlwaysAllow callback
// when the decision is DecisionAllowAlways, echoes a system
// message naming the decision, and clears the modal state.
//
// The system-message echo (parity with internal/tui:239) preserves
// what the operator chose in the transcript / scroll history —
// without it a fast-fingered approval leaves no trace and an
// audit reader can't reconstruct why a tool was allowed.
//
// The AlwaysAllow callback lets the host persist the entry (e.g.
// writing to permissions.allow or path_scope.allow in .agents/
// config.json). Errors are surfaced inline so the operator knows
// the allow-always didn't stick.
func (m *Model) dispatchPermission(d PermissionDecision) {
	req := m.pendingPermission
	if d == DecisionAllowAlways && m.opts.AlwaysAllow != nil && req != nil {
		if err := m.opts.AlwaysAllow(*req); err != nil {
			m.history.Append(Message{
				Role: RoleError,
				Text: "allow-always persistence failed: " + err.Error(),
			})
		}
	}
	if req != nil {
		m.history.Append(Message{
			Role: RoleSystem,
			Text: "Permission " + permissionDecisionLabel(d) + ": " + req.ToolName + " — " + truncate(req.Detail, 80),
		})
	}
	if p, ok := m.opts.Prompter.(*Prompter); ok {
		p.dispatchDecision(d)
	}
	m.pendingPermission = nil
	m.refreshViewport()
}

// permissionDecisionLabel maps a decision to the short label used in
// the transcript echo ("allow-once", "deny", "allow-session", etc.).
func permissionDecisionLabel(d PermissionDecision) string {
	switch d {
	case DecisionAllowOnce:
		return "allow-once"
	case DecisionAllowSession:
		return "allow-session"
	case DecisionAllowSessionVerb:
		return "allow-session-verb"
	case DecisionAllowSessionTool:
		return "allow-session-tool"
	case DecisionAllowAlways:
		return "allow-always"
	case DecisionDeny:
	}
	// Deny is also the label for a decision outside the declared set:
	// the echo should never claim an approval the gate did not get.
	return "deny"
}

// dispatchElicit writes the operator's elicit result back to the
// pending elicitor flow and clears the modal state. Every caller of
// this one is relaying a keystroke, so the error is nil: the operator
// answered, and an answer is not a failure.
func (m *Model) dispatchElicit(r ElicitResult) {
	m.dispatchElicitErr(r, nil)
}

// dispatchElicitErr is dispatchElicit for the case where the TUI is
// answering on its own account — err says why, and the Action beside
// it is a placeholder rather than a decision anyone made (issue
// #209). Kept separate rather than adding a second parameter to
// dispatchElicit, because four of the five call sites relay an
// operator and would all read `, nil`, which is exactly the sort of
// argument that stops being read.
func (m *Model) dispatchElicitErr(r ElicitResult, err error) {
	if e, ok := m.opts.Elicitor.(*elicitor); ok {
		e.dispatchResult(r, err)
	}
	m.refreshViewport()
}

// maybeDrainQueue auto-starts the next Queued prompt as a fresh turn
// (R-CHAT-10). Marks the popped entry InFlight so it stays visible
// in the queue panel during streaming, then finalizeTurn flips it to
// Done / Failed. Skips terminal-state entries (Done / Failed) that
// haven't culled yet. Returns the next-step Cmd batch.
func (m Model) maybeDrainQueue() (tea.Model, tea.Cmd) {
	next, idx := -1, -1
	for i := range m.queue {
		if m.queue[i].State == QueueQueued {
			next, idx = i, i
			break
		}
	}
	if next < 0 {
		return m, m.eventListener()
	}
	prompt := m.queue[idx].Text
	m.queue[idx].State = QueueInFlight
	out := m.submitTurn(prompt)
	return out, tea.Batch(out.armSpinner(), out.eventListener())
}

// enqueueDuringStream routes an operator-typed-during-streaming
// prompt per Options.MidTurnInjectionMode (R-CHAT-10 / R-CHAT-11):
//
//   - `QueueForNext` (default) — append as a Queued queue row;
//     `maybeDrainQueue` picks it up on the next turn-end.
//   - `InjectIntoCurrent` — call the agent's `Inject` so the entry
//     joins the running turn's context. The queue row renders
//     immediately as Done so the operator sees what was injected;
//     cullTTL drops it after ~2s. Falls back to `QueueForNext` when
//     the agent doesn't satisfy `InjectableAgent` (no runtime error).
func (m *Model) enqueueDuringStream(text string) {
	if m.opts.MidTurnInjectionMode == InjectIntoCurrent {
		if injector, ok := m.opts.Agent.(InjectableAgent); ok {
			if err := injector.Inject(text); err != nil {
				m.queue = append(m.queue, QueueEntry{
					Text:     text,
					State:    QueueFailed,
					Err:      err.Error(),
					Created:  time.Now(),
					Injected: true,
				})
				return
			}
			m.queue = append(m.queue, QueueEntry{
				Text:     text,
				State:    QueueDone,
				Created:  time.Now(),
				Injected: true,
			})
			return
		}
		// Agent doesn't support injection — fall back to QueueForNext.
	}
	if m.opts.MidTurnInjectionMode == AutoContinueFromInbox {
		// Issue #9: best of both worlds — Inject feeds the host's
		// inbox so DrainInbox at turn-end picks the entry up; the
		// queue row stays Queued so the operator sees the entry
		// is pending. maybeAutoContinue flips it to Done when the
		// inbox drain returns it. Falls through to QueueForNext
		// when either capability is missing.
		if injector, ok := m.opts.Agent.(InjectableAgent); ok {
			if _, isDrainer := m.opts.Agent.(InboxDrainer); isDrainer {
				if err := injector.Inject(text); err != nil {
					m.queue = append(m.queue, QueueEntry{
						Text:     text,
						State:    QueueFailed,
						Err:      err.Error(),
						Created:  time.Now(),
						Injected: true,
					})
					return
				}
				m.queue = append(m.queue, QueueEntry{
					Text:     text,
					State:    QueueQueued,
					Created:  time.Now(),
					Injected: true,
				})
				return
			}
		}
	}
	m.queue = append(m.queue, QueueEntry{
		Text:    text,
		State:   QueueQueued,
		Created: time.Now(),
	})
}

// markInFlightTerminal flips the InFlight queue entry (if any) to a
// terminal state. Called from finalizeTurn so the panel can show the
// result before the cullTTL drops it.
func (m *Model) markInFlightTerminal(success bool, reason string) {
	for i := range m.queue {
		if m.queue[i].State != QueueInFlight {
			continue
		}
		if success {
			m.queue[i].State = QueueDone
		} else {
			m.queue[i].State = QueueFailed
			m.queue[i].Err = reason
		}
		m.queue[i].Created = time.Now() // restart TTL from the transition
		return
	}
}

// cullQueue drops Done / Failed entries whose terminal-state TTL has
// elapsed. Called from the render path so the panel naturally fades
// completed entries as the operator keeps using the TUI.
func (m *Model) cullQueue() {
	if len(m.queue) == 0 {
		return
	}
	now := time.Now()
	kept := m.queue[:0]
	for _, e := range m.queue {
		if e.State.terminalState() && now.Sub(e.Created) > cullTTL {
			continue
		}
		kept = append(kept, e)
	}
	m.queue = kept
}

// trimToolArg sanitizes a tool-arg summary and truncates it to max
// runes, appending a truncation marker (style.md §2 GlyphTruncate).
//
// This is the second untrusted-content funnel in the package — the
// rune-based one, for single-row arg hints and queue rows where the
// CALLER supplies the width. Everything that renders a whole line of
// model- or host-derived text goes through sanitizeLine instead
// (sanitize.go). Both apply the same escape set; only the cap
// differs, because an arg hint is trimmed to the terminal's columns
// and a content line to perLineByteCap.
//
// Sanitizing BEFORE the rune trim is deliberate: escaping expands
// (an ESC becomes four runes, not one), so trimming first would let
// the row overflow the width the caller asked for.
func trimToolArg(s string, max int) string {
	s = sanitizeContent(s)
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + GlyphTruncate
}

// paletteInsert replaces the typed trigger token with the selected
// item's insert form and closes the palette (Enter while palette is
// open). Two exceptions:
//
//   - Directory entries in the file palette stay open after the
//     insert so the operator drills into the dir without re-typing
//     `@`. The palette re-filters to entries under the new prefix.
//   - Unavailable items are skipped — closing the palette silently
//     — until a real slice surfaces a system-message hint.
func (m Model) paletteInsert() tea.Model {
	if m.palette == nil {
		return m
	}
	item, ok := m.palette.selected()
	if !ok || !item.Available {
		m.palette = nil
		m.resize()
		return m
	}
	value := m.input.Value()
	tokenEnd := m.palette.triggerPos + 1 + len(m.palette.filter)
	if tokenEnd > len(value) {
		tokenEnd = len(value)
	}
	insertText := item.insertText(m.palette.kind)
	// Directories don't get a trailing space — the prefix is meant
	// to be extended by further palette selection or typing.
	isDir := m.palette.kind == paletteFile && item.IsDir
	if m.palette.kind == paletteFile && !isDir {
		insertText += " "
	}
	newValue := value[:m.palette.triggerPos] + insertText + value[tokenEnd:]
	m.input.SetValue(newValue)
	if isDir {
		// Keep the palette open and re-sync the filter to the new
		// prefix so children of the picked directory surface. Still
		// the same snapshot, so no fetch Cmd comes back.
		_ = m.refreshPalette()
	} else {
		m.palette = nil
		m.resize()
	}
	return m
}
