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
	return tea.Batch(cmds...)
}

// spawnLiveStreamCmd is the bridge that lets Init() (value
// receiver) hand the eventually-mutating cancelLiveStream onto
// the model: returns a Cmd that starts the drain goroutine and
// reports back via liveStreamStartedMsg so the Update handler
// can stash the cancel on the pointer it owns.
func (m Model) spawnLiveStreamCmd(agent LiveAgent) tea.Cmd {
	return func() tea.Msg {
		cancel := m.startLiveStream(agent)
		return liveStreamStartedMsg{cancel: cancel}
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
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		// Cached Glamour Rendered text is width-pinned at the
		// time of the render; when the viewport narrows the cached
		// output is wider than the visible area and the terminal
		// clips it (leading whitespace can vanish along with the
		// first few content chars). Re-render every assistant
		// message through the new-width Glamour so wrapping
		// matches the current viewport.
		m.rerenderHistoryMarkdown()
		m.refreshViewport()
		return m, nil

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

	case streamChunkMsg:
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
			return m, m.liveStreamRenderCmd(spinnerTick())
		}
		return m, m.liveStreamRenderCmd()
	case toolCallMsg:
		m.applyToolCall(msg)
		// Issue #26: render kick in LiveAgent mode — a solo
		// autonomous tool call could otherwise sit invisible
		// until the next operator keypress.
		return m, m.liveStreamRenderCmd()
	case toolResultMsg:
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
		// Issue #22: stash the cancel func; log the one-time
		// "Attached as observer" system row so the operator knows
		// they're in LiveAgent mode (and that typing without
		// InjectableAgent is read-only).
		m.cancelLiveStream = msg.cancel
		m.history.Append(Message{
			Role: RoleSystem,
			Text: "Attached as observer — agent runs autonomously; events stream below.",
		})
		m.refreshViewport()
		// Issue #28: route through liveStreamRenderCmd so the
		// eventListener stays armed. This Msg actually arrives
		// via the spawnLiveStreamCmd Cmd-result path (not
		// eventCh) so dropping the listener wouldn't kill the
		// drain in itself, but routing through the helper keeps
		// every LiveAgent handler consistent — and the render
		// kick (#24) comes along for free.
		return m, m.liveStreamRenderCmd()
	case liveStreamErrMsg:
		// Surface as an Error row and keep draining. The iterator
		// itself decides whether to keep yielding events.
		m.history.Append(Message{
			Role: RoleError,
			Text: "live stream error: " + msg.err.Error(),
		})
		m.refreshViewport()
		// Issue #28: this Msg ARRIVED via eventListener (it was
		// pushed onto m.eventCh by startLiveStream's drain
		// goroutine). Returning only the kick would leave nothing
		// reading m.eventCh — subsequent events (reconnect
		// notices, post-error frames) would sit buffered until
		// some other path happens to re-issue eventListener.
		// liveStreamRenderCmd batches eventListener + render kick.
		return m, m.liveStreamRenderCmd()
	case liveStreamEndedMsg:
		// Iterator returned. Flip the disconnected bit so the
		// banner can render; keep the program alive so the
		// operator can read scrollback and choose to quit.
		m.liveDisconnected = true
		m.history.Append(Message{
			Role: RoleSystem,
			Text: "Disconnected from live stream. Press Ctrl+C to quit.",
		})
		m.refreshViewport()
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
	case usageMsg:
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
	case turnDoneMsg:
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
		m.finalizeTurn(0, msg.err.Error())
		return m.maybeDrainQueue()
	case turnCancelledMsg:
		m.finalizeTurn(0, "(interrupted)")
		return m.maybeDrainQueue()
	case spinnerTickMsg:
		// Two gating paths:
		//   - per-turn (Run): m.state == stateStreaming
		//   - LiveAgent (#22): m.spinnerActive driven by
		//     applyStreamChunk's partial-vs-commit logic
		if m.state != stateStreaming && (!m.liveMode || !m.spinnerActive) {
			return m, nil
		}
		m.thinkingIdx++
		m.refreshViewport()
		return m, spinnerTick()
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
		m.pendingElicit = &r
		m.pendingElicitSrv = msg.serverName
		m.elicitFieldIdx = 0
		m.elicitValues = make(map[string]any, len(r.Fields))
		for _, f := range r.Fields {
			if f.Default != nil {
				m.elicitValues[f.Name] = f.Default
			}
		}
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

	// Forward unhandled messages to the input + viewport so navigation
	// keys work even when our switch above doesn't claim them.
	var (
		taCmd tea.Cmd
		vpCmd tea.Cmd
	)
	m.input, taCmd = m.input.Update(msg)
	m.viewport, vpCmd = m.viewport.Update(msg)
	return m, tea.Batch(taCmd, vpCmd)
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
		if m.pendingElicit != nil {
			m.dispatchElicit(ElicitResult{Action: ElicitActionCancel})
			return m, m.elicitListener()
		}
		if m.sideAnswer != nil {
			m.sideAnswer = nil
			m.resize()
			m.refreshViewport()
			return m, nil
		}
		// Esc closes the front-most Dialog overlay (model picker).
		if m.overlayStack.HasDialogs() {
			m.overlayStack.CloseFront()
			m.refreshViewport()
			return m, nil
		}
		if m.overlay != overlayNone {
			m.overlay = overlayNone
			return m, nil
		}
		if m.helpOpen {
			m.helpOpen = false
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

	// Permission modal — R-PERM-2's six decision keys. Highest
	// precedence after Esc so nothing else fires while pending.
	if m.pendingPermission != nil {
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
		// Swallow any other key — modal is exclusive while open.
		return m, nil
	}

	// Elicit modal — Tab/Shift+Tab nav, Enter submit, n decline,
	// Space toggle bool/enum, and printable chars feed the focused
	// string/number field.
	if m.pendingElicit != nil {
		if cmd := m.handleElicitKey(stroke); cmd != nil {
			return m, cmd
		}
		return m, nil
	}

	// Dialog overlay — front-most dialog gets every keystroke
	// before the legacy modal cascade. Returns Consumed=true when
	// the dialog handled it; we then return early so the key
	// doesn't double-fire on textarea / viewport. The optional
	// Cmd is dialogs' channel for emitting outbound msgs (e.g.
	// theme picker fires ThemeChangedMsg here on commit).
	if m.overlayStack.HasDialogs() {
		if consumed, cmd := m.overlayStack.HandleKey(stroke, &m); consumed {
			m.refreshViewport()
			return m, cmd
		}
	}

	// Vestigial legacy model picker (no longer reachable — Ctrl+G
	// + /model both go through the Dialog overlay now). Kept for
	// transition safety; remove after next stable release.
	if m.overlay == overlayModelPicker {
		swapper, ok := m.opts.Agent.(ModelSwapper)
		if !ok {
			m.overlay = overlayNone
			return m, nil
		}
		models := swapper.AvailableModels()
		if len(models) == 0 {
			m.overlay = overlayNone
			m.history.Append(Message{Role: RoleSystem, Text: "/model: no models available"})
			m.refreshViewport()
			return m, nil
		}
		switch stroke {
		case "up", "ctrl+p":
			m.modelPickerIdx = (m.modelPickerIdx - 1 + len(models)) % len(models)
			return m, nil
		case "down", "ctrl+n":
			m.modelPickerIdx = (m.modelPickerIdx + 1) % len(models)
			return m, nil
		case "enter":
			pick := models[m.modelPickerIdx]
			newAgent, err := swapper.SwitchModel(pick.ID)
			m.overlay = overlayNone
			if err != nil {
				m.history.Append(Message{Role: RoleError, Text: "/model: switch failed: " + err.Error()})
				m.refreshViewport()
				return m, nil
			}
			m.opts.Agent = newAgent
			m.history.Append(Message{Role: RoleSystem, Text: "/model: switched to " + pick.ID})
			if m.opts.PersistModelChoice != nil {
				if perr := m.opts.PersistModelChoice(pick.ID); perr != nil {
					m.history.Append(Message{Role: RoleError, Text: "/model: persist failed: " + perr.Error()})
				}
			}
			m.refreshTheme()
			m.refreshViewport()
			return m, nil
		}
		return m, nil
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
		return m, tea.Quit
	case "ctrl+d":
		// Ctrl+D quits unconditionally — "EOF closes input" is the
		// muscle memory and most TUIs honor it without a warning.
		m.quitting = true
		return m, tea.Quit

	case "ctrl+l":
		// Reset viewport scroll to the top. Mirrors the shell-style
		// "redraw / clear screen" muscle memory without actually
		// clearing history (use /clear for that).
		m.viewport.GotoTop()
		return m, nil

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
		if m.opts.PersistStatusLayout != nil {
			_ = m.opts.PersistStatusLayout(m.statusLayout)
		}
		m.resize()
		m.refreshViewport()
		return m, nil

	case "shift+tab":
		if m.permissionModeWired() {
			m.permMode = m.permMode.Next()
			_ = m.opts.PermissionMode.Set(m.permMode)
			if m.opts.PermissionMode.Persist != nil {
				_ = m.opts.PermissionMode.Persist(m.permMode)
			}
		}
		return m, nil

	case "ctrl+g":
		// Open the model picker dialog. Singleton — re-press
		// while open is a no-op (HasID check).
		if _, ok := m.opts.Agent.(ModelSwapper); ok && !m.overlayStack.HasID(modelPickerDialogID) {
			m.overlayStack.Open(newModelPickerDialog())
			m.refreshViewport()
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
				} else {
					// Render the typed prompt as a normal user row
					// so the operator sees what they sent — the
					// host's event stream may not echo it back.
					m.history.Append(Message{Role: RoleUser, Text: text})
				}
				m.refreshViewport()
				return m, nil
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
		return m.submitTurn(text), spinnerTick()

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
		return m, cmd

	case "?":
		// Toggle the bottom-anchored stacked help panel. Only fires
		// when input is empty so users can still type `?` mid-sentence
		// without hijacking the key.
		if strings.TrimSpace(m.input.Value()) == "" {
			m.helpOpen = !m.helpOpen
			m.resize()
			m.refreshViewport()
			return m, nil
		}
	}

	// Forward unmatched keys to the input field for typing. Viewport
	// gets the message too so PgUp/PgDn/Home/End scroll the chat
	// even while the input is focused.
	var (
		taCmd tea.Cmd
		vpCmd tea.Cmd
	)
	m.input, taCmd = m.input.Update(msg)
	m.viewport, vpCmd = m.viewport.Update(msg)
	// Auto-grow textarea: if typing (or pasting) bumped the line
	// count, re-clamp the textarea height between min/max and
	// re-resolve the layout so the viewport shrinks to make room.
	// Re-snap the viewport to the bottom when the operator was
	// already pinned there so they don't lose the in-flight tail.
	if m.syncInputHeight() {
		wasAtBottom := m.viewport.AtBottom()
		m.resize()
		m.refreshViewport()
		if wasAtBottom {
			m.viewport.GotoBottom()
		}
	}
	// Refresh palette state from the updated input — opens a new
	// palette on a fresh `/` or `@` trigger, closes the active one
	// when the trigger is deleted, or updates the filter on any
	// other keystroke.
	m.refreshPalette()
	return m, tea.Batch(taCmd, vpCmd)
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
func (m *Model) refreshPalette() {
	value := m.input.Value()

	if m.palette == nil {
		if strings.HasPrefix(value, "/") {
			// Merge agent-provided SlashProvider commands so /btw,
			// /subagent, etc. are discoverable in the palette in
			// addition to working when typed manually.
			var provider SlashProvider
			if p, ok := m.opts.Agent.(SlashProvider); ok {
				provider = p
			}
			m.palette = newSlashPalette(0, provider)
			m.palette.filter = value[1:]
			m.resize()
			return
		}
		if idx := lastAtTokenStart(value); idx >= 0 {
			m.palette = newFilePalette(idx, m.opts.PathScope)
			m.palette.filter = atFilterFrom(value, idx)
			m.resize()
			return
		}
		return
	}

	// Palette is open — verify the trigger char still sits at
	// triggerPos, otherwise close it.
	if m.palette.triggerPos >= len(value) {
		m.palette = nil
		m.resize()
		return
	}
	if string(value[m.palette.triggerPos]) != m.palette.triggerRune() {
		m.palette = nil
		m.resize()
		return
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
	m.refreshPalette()
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
	m.turnStarted = time.Now()
	m.inProgressText = ""
	m.currentUsage = nil
	m.currentCost = 0
	m.currentModel = ""
	m.inProgressStablePrefix = ""
	m.inProgressStableRender = ""
	m.toolActive = false
	m.thinkingIdx = 0
	m.spinnerActive = true
	for k := range m.seenToolIDs {
		delete(m.seenToolIDs, k)
	}
	m.cancelTurn = m.startAgentTurn(m.opts.Agent, text)
	m.refreshViewport()
	// Operator-initiated submit always scrolls to bottom — they want
	// to see their own message land and the response start, even if
	// they'd been scrolled up reading backlog.
	m.viewport.GotoBottom()
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
			// Spinner active while tokens flow.
			if !m.spinnerActive {
				m.spinnerActive = true
				m.thinkingIdx = 0
			}
		} else {
			m.liveLastCommitAt = time.Now()
			// Commit: freeze the Glamour render on the just-
			// finalized assistant row (mirrors finalizeTurn's
			// commit path) and stop the spinner.
			if strings.TrimSpace(m.inProgressText) != "" {
				mr := m.ensureMarkdown()
				m.history.Append(Message{
					Role:     RoleAssistant,
					Text:     m.inProgressText,
					Rendered: mr.renderMarkdown(m.inProgressText),
				})
				m.inProgressText = ""
				m.inProgressStablePrefix = ""
				m.inProgressStableRender = ""
			}
			m.spinnerActive = false
		}
	}
	m.refreshViewport()
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
			Role:     RoleAssistant,
			Text:     m.inProgressText,
			Rendered: mr.renderMarkdown(m.inProgressText),
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
	m.refreshViewport()
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
	m.history.SetToolPreview(idx, preview)
	m.refreshViewport()
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

	// Commit the streamed text as a Message. Skip when empty (the
	// agent emitted only tool calls, no assistant prose).
	if strings.TrimSpace(m.inProgressText) != "" {
		mr := m.ensureMarkdown()
		msg := Message{
			Role:     RoleAssistant,
			Text:     m.inProgressText,
			Rendered: mr.renderMarkdown(m.inProgressText),
			Model:    m.currentModel,
			Usage:    m.currentUsage,
			CostUSD:  m.currentCost,
			Elapsed:  elapsed,
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

	_ = m.input.Focus()
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

	matched := false
	for _, spec := range provider.SlashCommands() {
		if spec.Name == name || sliceContains(spec.Aliases, name) {
			matched = true
			break
		}
	}
	if !matched {
		m.history.Append(Message{
			Role: RoleSystem,
			Text: "unknown command /" + name + " — type / to see what's available",
		})
		m.input.Reset()
		m.refreshViewport()
		return m, nil
	}

	m.input.Reset()
	// Issue #10: hosts that implement AsyncSlashProvider (or the
	// preamble variant from #16) get the non-blocking path so any
	// network / file I/O the call needs runs off the Update
	// goroutine. The TUI stays responsive (cursor blinks, spinner
	// ticks, Ctrl+C lands) while the host works; the eventual
	// result arrives via slashResultMsg and goes through the same
	// modal/system-message/error machinery below.
	//
	// Issue #16 preamble variant takes precedence over the bare
	// variant: hosts that want a chat-visible "running…" row at
	// dispatch time implement AsyncSlashProviderWithPreamble; the
	// preamble lands as a RoleSystem row BEFORE the goroutine is
	// launched so the operator's eye picks it up next to the prompt.
	if preProv, ok := provider.(AsyncSlashProviderWithPreamble); ok {
		if refusal, refused := m.refuseConcurrentSlash(name); refused {
			return refusal, nil
		}
		ctx, cancel := context.WithCancel(context.Background())
		m.cancelSlash = cancel
		m.inFlightSlash = &slashFlight{name: name, startedAt: time.Now()}
		m.toast = "▸ /" + name + " running…"
		m.toastSetAt = time.Now()
		preamble, ch := preProv.InvokeSlashAsync(ctx, name, args)
		if preamble != "" {
			m.history.Append(Message{Role: RoleSystem, Text: preamble})
		}
		m.refreshViewport()
		return m, awaitSlashChannel(name, ch)
	}
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
		m.refreshViewport()
		return m, m.invokeSlashAsync(asyncProv, ctx, name, args)
	}
	res, err := provider.InvokeSlash(context.Background(), name, args)
	return m.applySlashResult(name, res, err)
}

// refuseConcurrentSlash applies issue #13's concurrent-slash policy:
// when an async slash is already in flight, log a RoleSystem refusal
// for the new dispatch and return refused=true. Shared by both the
// preamble (#16) and bare async (#10) paths so the policy stays in
// one place.
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
// Shared by both async dispatch paths so the channel-draining shape
// stays in one place.
func awaitSlashChannel(name string, ch <-chan SlashResultOrErr) tea.Cmd {
	return func() tea.Msg {
		out, ok := <-ch
		if !ok {
			return slashResultMsg{name: name}
		}
		return slashResultMsg{name: name, res: out.Res, err: out.Err}
	}
}

// invokeSlashAsync launches the host's AsyncSlashProvider call in
// a goroutine and returns a tea.Cmd that forwards the eventual
// SlashResultOrErr as a slashResultMsg into the Update loop. ctx is
// the cancellable context owned by dispatchSlash so the Esc handler
// can fire cancelSlash and the host can bail mid-call.
//
// Single-shot semantics + channel-draining are delegated to
// awaitSlashChannel so the preamble variant (#16) and the bare
// variant (#10) share one implementation.
func (m Model) invokeSlashAsync(prov AsyncSlashProvider, ctx context.Context, name, args string) tea.Cmd {
	return awaitSlashChannel(name, prov.InvokeSlashAsync(ctx, name, args))
}

// applySlashResult is the shared post-processing for both the
// synchronous and async slash paths. Returns the new model + a
// nil Cmd so callers can return directly.
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
	}
	if res.SystemMessage != "" {
		m.history.Append(Message{Role: RoleSystem, Text: res.SystemMessage})
	}
	m.resize()
	m.refreshViewport()
	return m, nil
}

// rerenderHistoryMarkdown re-runs Glamour at the current viewport
// width over every assistant message with a cached Rendered. Called
// from the WindowSizeMsg path so a resize doesn't leave the
// transcript displaying width-pinned output that the terminal then
// clips. Cheap-enough — typical transcripts hold dozens of
// messages, not thousands.
func (m *Model) rerenderHistoryMarkdown() {
	mr := m.ensureMarkdown()
	if mr == nil {
		return
	}
	snap := m.history.Snapshot()
	for i, msg := range snap {
		if msg.Role != RoleAssistant || msg.Text == "" {
			continue
		}
		m.history.SetRendered(i, mr.renderMarkdown(msg.Text))
	}
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
	default:
		return "deny"
	}
}

// dispatchElicit writes the operator's elicit result back to the
// pending elicitor flow and clears the modal state.
func (m *Model) dispatchElicit(r ElicitResult) {
	if e, ok := m.opts.Elicitor.(*elicitor); ok {
		e.dispatchResult(r)
	}
	m.pendingElicit = nil
	m.pendingElicitSrv = ""
	m.elicitFieldIdx = 0
	m.elicitValues = nil
	m.refreshViewport()
}

// handleElicitKey routes a keystroke to the elicit form's nav /
// per-field-edit handlers (R-ELIC-2). Returns the Cmd to issue
// after the keystroke (the re-armed elicit listener when a result
// is dispatched, otherwise nil for in-form moves).
func (m *Model) handleElicitKey(stroke string) tea.Cmd {
	req := m.pendingElicit
	if req.Mode == ElicitURLMode {
		switch stroke {
		case "a", "enter":
			m.dispatchElicit(ElicitResult{Action: ElicitActionSubmit})
			return m.elicitListener()
		case "n":
			m.dispatchElicit(ElicitResult{Action: ElicitActionDecline})
			return m.elicitListener()
		}
		// 'o' would open the URL in a browser — deferred to a
		// later slice (we don't depend on os/exec yet).
		return func() tea.Msg { return nil } // swallow other keys
	}

	// Form mode.
	switch stroke {
	case "enter":
		// Validate required fields; on success submit.
		for _, f := range req.Fields {
			if !f.Required {
				continue
			}
			v, ok := m.elicitValues[f.Name]
			if !ok || isElicitEmpty(v) {
				// Move cursor to the missing field; don't submit.
				for i, ff := range req.Fields {
					if ff.Name == f.Name {
						m.elicitFieldIdx = i
						break
					}
				}
				m.refreshViewport()
				return func() tea.Msg { return nil }
			}
		}
		m.dispatchElicit(ElicitResult{Action: ElicitActionSubmit, Values: m.elicitValues})
		return m.elicitListener()
	case "tab":
		m.elicitFieldIdx = (m.elicitFieldIdx + 1) % len(req.Fields)
		m.refreshViewport()
		return func() tea.Msg { return nil }
	case "shift+tab":
		m.elicitFieldIdx = (m.elicitFieldIdx - 1 + len(req.Fields)) % len(req.Fields)
		m.refreshViewport()
		return func() tea.Msg { return nil }
	case "space":
		f := req.Fields[m.elicitFieldIdx]
		if f.Type == ElicitFieldBoolean {
			cur, _ := m.elicitValues[f.Name].(bool)
			m.elicitValues[f.Name] = !cur
			m.refreshViewport()
			return func() tea.Msg { return nil }
		}
	case "left", "right":
		f := req.Fields[m.elicitFieldIdx]
		if f.Type == ElicitFieldEnum && len(f.EnumChoices) > 0 {
			idx := indexOfEnum(f.EnumChoices, m.elicitValues[f.Name])
			delta := 1
			if stroke == "left" {
				delta = -1
			}
			idx = (idx + delta + len(f.EnumChoices)) % len(f.EnumChoices)
			m.elicitValues[f.Name] = f.EnumChoices[idx]
			m.refreshViewport()
			return func() tea.Msg { return nil }
		}
	case "backspace":
		f := req.Fields[m.elicitFieldIdx]
		if f.Type == ElicitFieldString {
			cur, _ := m.elicitValues[f.Name].(string)
			if cur != "" {
				m.elicitValues[f.Name] = cur[:len(cur)-1]
				m.refreshViewport()
				return func() tea.Msg { return nil }
			}
		}
	}
	// Printable single-rune keystrokes — append to string fields.
	if len(stroke) == 1 {
		f := req.Fields[m.elicitFieldIdx]
		if f.Type == ElicitFieldString || f.Type == ElicitFieldNumber || f.Type == ElicitFieldInteger {
			cur, _ := m.elicitValues[f.Name].(string)
			m.elicitValues[f.Name] = cur + stroke
			m.refreshViewport()
			return func() tea.Msg { return nil }
		}
	}
	return func() tea.Msg { return nil }
}

// isElicitEmpty reports whether v is the zero value for its type
// — used by Enter's submit-time validation against required fields.
func isElicitEmpty(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case bool:
		return false // every bool is a valid choice
	default:
		return false
	}
}

// indexOfEnum returns the index of v in choices, defaulting to 0
// when v is nil or not found. Used by enum left/right cycling.
func indexOfEnum(choices []string, v any) int {
	s, _ := v.(string)
	for i, c := range choices {
		if c == s {
			return i
		}
	}
	return 0
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
	return out, tea.Batch(spinnerTick(), out.eventListener())
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

// trimToolArg truncates a tool-arg summary to max runes, appending a
// truncation marker (style.md §2 GlyphTruncate).
func trimToolArg(s string, max int) string {
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
		// prefix so children of the picked directory surface.
		m.refreshPalette()
	} else {
		m.palette = nil
		m.resize()
	}
	return m
}
