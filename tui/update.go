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
	return tea.Batch(
		tea.RequestBackgroundColor,
		textarea.Blink,
		m.eventListener(),
		m.wakeListener(),
		m.promptListener(),
		m.elicitListener(),
	)
}

// Update is the Bubble Tea dispatcher. The visual-preview slice
// handles window-resize, background-color, and a small keymap; later
// slices add agent-event dispatch, modal forms, etc.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		m.refreshViewport()
		return m, nil

	case tea.BackgroundColorMsg:
		m.styles = NewStyles(msg.IsDark(), m.opts.Branding)
		m.markdown = nil // force rebuild on next render
		m.refreshViewport()
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case streamChunkMsg:
		m.applyStreamChunk(msg)
		return m, m.eventListener()
	case toolCallMsg:
		m.applyToolCall(msg)
		return m, m.eventListener()
	case usageMsg:
		m.currentUsage = &msg.usage
		return m, m.eventListener()
	case turnDoneMsg:
		m.finalizeTurn(msg.elapsed, "")
		return m.maybeDrainQueue()
	case turnErrMsg:
		m.finalizeTurn(0, msg.err.Error())
		return m.maybeDrainQueue()
	case turnCancelledMsg:
		m.finalizeTurn(0, "(interrupted)")
		return m.maybeDrainQueue()
	case spinnerTickMsg:
		if m.state != stateStreaming {
			return m, nil
		}
		m.thinkingIdx++
		m.refreshViewport()
		return m, spinnerTick()
	case wakeMsg:
		m.toast = "agent needs your attention"
		m.toastSetAt = time.Now()
		m.refreshViewport()
		// Re-issue both the wake subscription (drain the next one)
		// and a tick that auto-clears the toast after toastTTL.
		return m, tea.Batch(m.wakeListener(), toastTick())
	case permissionRequestMsg:
		req := msg.req
		m.pendingPermission = &req
		m.refreshViewport()
		return m, nil
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
		return m, nil
	case toastClearMsg:
		// Only clear if the same toast is still up (a fresh wake
		// during the TTL window restarts the timer).
		if time.Since(m.toastSetAt) >= toastTTL {
			m.toast = ""
			m.refreshViewport()
		}
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
			return m.paletteComplete(), nil
		case "enter":
			return m.paletteInsert(), nil
		}
	}

	switch stroke {
	case "ctrl+c":
		// Always quits (R-CHAT-6). Ctrl+D also quits as a familiar
		// "EOF closes input" convention.
		m.quitting = true
		return m, tea.Quit
	case "ctrl+d":
		m.quitting = true
		return m, tea.Quit

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
		m.overlay = overlayModelPicker
		return m, nil
	case "ctrl+y":
		m.overlay = overlayPermission
		return m, nil
	case "ctrl+e":
		m.overlay = overlayElicit
		return m, nil

	case "enter":
		// Submit (R-CHAT-1). When idle: dispatch as a slash command
		// if the input begins with `/`, otherwise append the typed
		// text as a RoleUser message and start an agent turn. When
		// streaming (R-CHAT-10): append to the prompt queue and clear
		// the input; the queue drains one entry per turn-end.
		text := strings.TrimSpace(m.input.Value())
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
		return m.submitTurn(text), spinnerTick()

	case "shift+enter", "ctrl+j":
		// Insert a newline (R-CHAT-1: Shift-Enter / Ctrl-J inserts
		// newline). Synthesize an Enter KeyPressMsg with no modifier
		// and forward to the textarea — that hits its InsertNewline
		// binding.
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
			m.palette = newSlashPalette(0)
			m.palette.filter = value[1:]
			m.resize()
			return
		}
		if idx := lastAtTokenStart(value); idx >= 0 {
			m.palette = newFilePalette(idx)
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
func (m Model) submitTurn(text string) Model {
	m.history.Append(Message{Role: RoleUser, Text: text})
	m.input.Reset()
	m.state = stateStreaming
	m.turnStarted = time.Now()
	m.inProgressText = ""
	m.currentUsage = nil
	m.currentModel = ""
	m.toolActive = false
	m.thinkingIdx = 0
	m.spinnerActive = true
	for k := range m.seenToolIDs {
		delete(m.seenToolIDs, k)
	}
	m.cancelTurn = m.startAgentTurn(m.opts.Agent, text)
	m.refreshViewport()
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
	if msg.partial {
		m.inProgressText += msg.text
	} else {
		// Committed full text — overwrite (some agents echo the
		// full message at turn-end).
		m.inProgressText = msg.text
	}
	m.refreshViewport()
}

// applyToolCall handles a toolCallMsg. Dedup by ID (R-CHAT-5), append
// a one-line tool row to history (the in-progress assistant message
// stays above it visually), and flip the spinner to tool-active.
func (m *Model) applyToolCall(msg toolCallMsg) {
	if msg.id != "" {
		if m.seenToolIDs[msg.id] {
			return
		}
		m.seenToolIDs[msg.id] = true
	}
	args := ""
	if len(msg.args) > 0 {
		// One-line summary: just the first arg's value, truncated.
		// A real slice would pick a host-supplied summarizer.
		for _, v := range msg.args {
			args = trimToolArg(fmt.Sprint(v), 80)
			break
		}
	}
	m.history.Append(Message{
		Role:     RoleTool,
		ToolName: msg.name,
		ToolArgs: args,
	})
	m.toolActive = true
	m.refreshViewport()
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
// Built-in slash commands aren't wired yet (a later slice owns the
// /help, /clear, /quit dispatch); for now we only route to the
// agent's SlashProvider when present. Unknown commands surface as
// a system row pointing at /help.
func (m Model) dispatchSlash(text string) (tea.Model, tea.Cmd) {
	rest := strings.TrimPrefix(text, "/")
	name, args, _ := strings.Cut(rest, " ")
	name = strings.ToLower(name)
	args = strings.TrimSpace(args)

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
	res, err := provider.InvokeSlash(context.Background(), name, args)
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
// pending Prompter flow and clears the modal state so the next
// inbound request can render.
func (m *Model) dispatchPermission(d PermissionDecision) {
	if p, ok := m.opts.Prompter.(*Prompter); ok {
		p.dispatchDecision(d)
	}
	m.pendingPermission = nil
	m.refreshViewport()
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
// open). Unavailable items are skipped — closing the palette
// silently — until a real slice surfaces a system-message hint.
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
	if m.palette.kind == paletteFile {
		insertText += " "
	}
	newValue := value[:m.palette.triggerPos] + insertText + value[tokenEnd:]
	m.input.SetValue(newValue)
	m.palette = nil
	m.resize()
	return m
}
