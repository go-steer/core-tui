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
	"os"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// textareaMinHeight / textareaMaxHeight bound the auto-growing
// input box. The textarea starts at MinHeight and grows one row
// per visual line until it hits MaxHeight, after which the
// textarea's own internal scroll takes over. Layout reconciles
// on every height change so the viewport shrinks to make room
// (and re-scrolls if it was pinned to bottom).
const (
	textareaMinHeight = 3
	textareaMaxHeight = 15
)

// turnState is the high-level activity bit the spinner and input
// gating key off of.
type turnState int

const (
	stateIdle      turnState = iota // input enabled, no spinner
	stateStreaming                  // turn in flight: input disabled, spinner active
)

// overlay identifies which enum-driven modal, if any, is currently
// visible. Permission + elicit modals don't ride this enum — they're
// keyed off pendingPermission / pendingElicit directly so the real
// flows can open them without the TUI having to also flip an
// overlay value.
type overlay int

const (
	overlayNone overlay = iota
	overlayModelPicker
)

// Model is the Bubble Tea model that drives the TUI. Field set is the
// minimum needed for the v0 visual-preview slice; later slices add
// streaming state, modal forms, transcript persistence, etc.
type Model struct {
	opts    Options
	styles  Styles
	history History

	viewport viewport.Model
	input    textarea.Model

	width  int
	height int

	statusLayout StatusLayout
	permMode     PermissionMode
	overlay      overlay

	// helpOpen toggles the bottom-anchored stacked help panel
	// (`?` to open / close). When open, the chat viewport shrinks
	// to make room above the input.
	helpOpen bool

	// palette is the active slash / file palette overlay (R-PAL-1 /
	// R-PAL-2). Nil = no palette open. Triggered by typing `/` at
	// the start of the input or `@` anywhere.
	palette *palette

	// sideAnswer is the active /btw-style modal overlay (R-CMD-5).
	// Nil = no side-answer open. Carries the question, the agent's
	// answer (or err), and the Glamour render width. Dismissed with
	// Esc / Enter / Space.
	sideAnswer *SideAnswer

	// pendingPermission is the active PermissionRequest awaiting an
	// operator decision (R-PERM-1). Nil = no modal open. Key
	// handler dispatches back via opts.Prompter.dispatchDecision.
	pendingPermission *PermissionRequest

	// pendingElicit is the active ElicitRequest awaiting form
	// submission / decline / cancel (R-ELIC-1). Nil = no modal
	// open. Per-field cursor + values tracked in elicitFieldIdx +
	// elicitValues. Key handler dispatches back via
	// opts.Elicitor.dispatchResult.
	pendingElicit    *ElicitRequest
	pendingElicitSrv string         // server name for the title bar
	elicitFieldIdx   int            // currently-focused field (Tab/Shift+Tab nav)
	elicitValues     map[string]any // in-progress form values

	// toast is a transient banner that renders between the input
	// box and the footer (R-WAKE-1). Cleared after toastTTL via
	// cullToast on the next render. Set by wakeMsg handling.
	toast      string
	toastSetAt time.Time

	// Streaming-turn state (R-CHAT-3 / R-CHAT-4 / R-CHAT-6).
	state          turnState
	cancelTurn     context.CancelFunc // non-nil while state == stateStreaming
	turnStarted    time.Time
	inProgressText string // accumulator for streamed tokens
	currentUsage   *Usage  // most recent usage snapshot for this turn
	currentCost    float64 // most recent positive cost for this turn (USD)
	currentModel   string  // model name for the in-progress message

	// Incremental Glamour cache for the in-progress assistant
	// stream. inProgressStablePrefix holds the portion of
	// inProgressText up to the latest safe boundary (\n\n outside
	// an open code fence); inProgressStableRender holds its
	// Glamour render. On each chunk, only the trailing partial
	// is re-rendered + concatenated, avoiding a full re-parse of
	// the accumulated text per token. Both reset when:
	//   - turn finalizes (finalizeTurn)
	//   - tool call segments the stream (applyToolCall)
	//   - viewport width changes (ensureMarkdown rebuilds)
	inProgressStablePrefix string
	inProgressStableRender string
	toolActive     bool   // true after a ToolCall; flips back on next Text
	seenToolIDs    map[string]bool
	thinkingIdx    int  // rotation index into ThinkingPhrases / WorkingPhrases
	spinnerActive  bool // gates spinner tick scheduling

	// queue is the per-session prompt queue (R-CHAT-10). Each entry
	// transitions through Queued → InFlight → Done / Failed and
	// lingers in terminal state for cullTTL so the operator can see
	// the result. Drained one-at-a-time when finalizeTurn fires.
	queue []QueueEntry

	// eventCh is the bridge between the agent dispatch goroutine and
	// the Bubble Tea loop. eventListener drains it one message at a
	// time. Buffered so a fast agent can't stall on a slow Update.
	eventCh chan tea.Msg

	// markdown is the lazily-built Glamour renderer; rebuilt when
	// dark/light or width changes. nil until first use.
	markdown *markdownRenderer

	// quitting flips when Ctrl+C / Ctrl+D land, so the next Update
	// returns tea.Quit.
	quitting bool

	// pendingExit holds the warn-then-quit state for Ctrl+C while
	// idle: first press sets it (showing a system message), second
	// press within ctrlCExitTTL actually quits. Mirrors internal/tui
	// + Claude Code: prevents accidental drops on a single fat-finger.
	// Reset by any keystroke that isn't Ctrl+C and by the
	// pendingExitClearMsg fired after the TTL.
	pendingExit bool

	// confirmingClear is true between a /clear submission and the
	// operator's y/yes confirmation. While true the footer hint
	// changes and the next Enter is interpreted as the confirmation
	// answer (y/yes wipes history, anything else cancels).
	confirmingClear bool

	// promptHistory is the shell-style recall buffer: every
	// non-slash submitted user prompt is appended (deduped if it
	// matches the immediate previous entry). historyCursor walks
	// the buffer when the operator presses ↑/↓ on an empty input.
	// -1 = not navigating.
	promptHistory  []string
	historyCursor  int
	historyDraft   string // user's in-flight input saved before navigation

	// startedAt is the wall-clock time the TUI launched. Used by
	// the transcript-on-exit hook so saved files name themselves
	// with the session-start instant.
	startedAt time.Time

	// modelPickerIdx is the currently-focused row in the
	// overlayModelPicker overlay. Reset to 0 every time the picker
	// opens; ↑/↓ adjust; Enter dispatches SwitchModel for the row.
	modelPickerIdx int
}

// NewModel constructs a Model from Options. SeedHistory entries are
// appended in order before the first render.
func NewModel(opts Options) Model {
	ta := textarea.New()
	ta.Placeholder = "Type a message and hit Enter. /help for commands."
	if opts.Branding.InputPlaceholder != "" {
		ta.Placeholder = opts.Branding.InputPlaceholder
	}
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.SetHeight(textareaMinHeight)
	// Focus the textarea so KeyPressMsg events route to it. Focus()
	// returns a blink Cmd we deliberately drop here — Init below
	// returns textarea.Blink directly to start the cursor animation.
	_ = ta.Focus()

	vp := viewport.New()
	// The viewport's default KeyMap binds h/j/k/l + arrow keys for
	// horizontal/vertical scrolling. Update forwards every keystroke
	// to the viewport, so typing those letters into the input (or
	// pressing Right inside the palette) would scroll the viewport
	// — Right in particular sets xOffset, which then cuts chars off
	// the LEFT of every rendered line (`ansi.Cut` at viewport.go:362).
	// Strip the conflicting bindings so only PageUp/PageDown survive
	// (mouse wheel handled separately).
	vp.KeyMap = viewport.KeyMap{
		PageDown:     vp.KeyMap.PageDown,
		PageUp:       vp.KeyMap.PageUp,
		HalfPageUp:   vp.KeyMap.HalfPageUp,
		HalfPageDown: vp.KeyMap.HalfPageDown,
	}

	m := Model{
		opts:          opts,
		styles:        NewStyles(true, opts.Branding), // overwritten on BackgroundColorMsg
		viewport:      vp,
		input:         ta,
		statusLayout:  opts.StatusLayout,
		permMode:      opts.PermissionMode.Initial,
		eventCh:       make(chan tea.Msg, 32),
		seenToolIDs:   make(map[string]bool),
		historyCursor: -1,
		startedAt:     time.Now(),
	}
	for _, msg := range opts.SeedHistory {
		m.history.Append(msg)
	}
	return m
}

// thinkingPhrases / workingPhrases return the rotated verb pools
// (R-CHAT-3). Falls back to internal/tui's pool when Options are
// not set — "Thinking..." anchors the first tick so the affordance
// is unambiguous before the rotator wanders into the AI / sci-fi /
// CS jokes.
func (m Model) thinkingPhrases() []string {
	if len(m.opts.ThinkingPhrases) > 0 {
		return m.opts.ThinkingPhrases
	}
	return []string{
		"Thinking...",
		"Consulting the latent space...",
		"Sampling from the distribution...",
		"Reticulating splines...",
		"Computing the answer to the ultimate question...",
		"Spinning up the attention heads...",
		"Asking Stack Overflow nicely...",
		"Untangling pointer chains...",
		"Bargaining with the loss function...",
		"Compiling a thoughtful response...",
		"Defragmenting cache lines...",
		"Negotiating with the Vogons...",
		"Brewing a fresh stack frame...",
		"Plotting a hyperspace course...",
		"Resolving promises...",
		"Eval'ing your prompt...",
	}
}

func (m Model) workingPhrases() []string {
	if len(m.opts.WorkingPhrases) > 0 {
		return m.opts.WorkingPhrases
	}
	return []string{
		"Working...",
		"Running tools...",
		"Reading the code...",
		"Searching the haystack...",
		"Editing in place...",
		"Tracing call sites...",
		"Cross-referencing...",
	}
}

// ensureMarkdown returns the cached markdown renderer, rebuilding it
// when dark/light or width has changed since the last call. A rebuild
// invalidates the incremental stream cache too, since cached prefix
// renders are width-pinned (re-rendering them with the new width is
// what makes resize keep the in-progress text readable).
func (m *Model) ensureMarkdown() *markdownRenderer {
	width := m.viewport.Width()
	if width <= 0 {
		width = 80
	}
	if m.markdown == nil || m.markdown.dark != m.styles.Dark || m.markdown.width != width {
		m.markdown = newMarkdownRenderer(m.styles.Dark, width)
		m.inProgressStablePrefix = ""
		m.inProgressStableRender = ""
	}
	return m.markdown
}

// permissionModeWired reports whether the host configured the chip.
func (m Model) permissionModeWired() bool {
	return m.opts.PermissionMode.Set != nil
}

// wordmark returns the brand identity string for the status surface.
func (m Model) wordmark() string {
	if m.opts.Branding.Wordmark != "" {
		return m.opts.Branding.Wordmark
	}
	return "core-tui"
}

// displayCwd returns the operator's cwd, shortened with `~/` when
// it sits under the home directory, for the status surface. Empty
// when os.Getwd fails (no point displaying a stale or wrong path).
func (m Model) displayCwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	if home, herr := os.UserHomeDir(); herr == nil && strings.HasPrefix(cwd, home) {
		return "~" + cwd[len(home):]
	}
	return cwd
}

// displayProvider extracts the provider tag from the host's
// StatusReporter when wired. Empty when the host doesn't surface
// it (no capability or empty Provider field).
func (m Model) displayProvider() string {
	reporter, ok := m.opts.Agent.(StatusReporter)
	if !ok {
		return ""
	}
	return reporter.Status().Provider
}

// displayModelName picks the best model identifier to surface on the
// status header/sidebar. Order:
//
//  1. StatusReporter.Status().ModelName  — the host's authoritative
//     read of the live model (preferred; updates on /model swap).
//  2. m.currentModel                     — set per-turn from streamed
//     Event.Model when the host populates it; empty before any turn.
//  3. "(model not set)"                  — placeholder so the chip
//     isn't blank when neither source has fired yet.
func (m Model) displayModelName() string {
	if reporter, ok := m.opts.Agent.(StatusReporter); ok {
		if s := reporter.Status(); s.ModelName != "" {
			return s.ModelName
		}
	}
	if m.currentModel != "" {
		return m.currentModel
	}
	return "(model not set)"
}

// usageSummaryOneLine returns the compact "Nk in · Nk out · $X · used/size"
// spend block for the status header. Empty when no UsageTracker is
// wired (the header just drops the trailing segment rather than
// rendering placeholder zeros that look like real data).
func (m Model) usageSummaryOneLine() string {
	if m.opts.UsageTracker == nil {
		return ""
	}
	t := m.opts.UsageTracker.SessionTotals()
	cost := m.opts.UsageTracker.SessionCostUSD()
	used := m.opts.UsageTracker.ContextWindowUsed()
	size := m.opts.UsageTracker.ContextWindowSize()
	sep := " " + GlyphSeparator + " "
	out := formatKTokens(t.InputTokens) + " in" + sep + formatKTokens(t.OutputTokens) + " out" + sep + fmt.Sprintf("$%.4f", cost)
	if size > 0 {
		out += sep + m.contextFillStyle(used, size).Render(
			formatKTokens(used)+" / "+formatKTokens(size),
		)
	}
	return out
}

// usageSummaryStacked returns the sidebar's two-line spend block.
// First line: "Nk in · Nk out"; second line: "$X · used / size" (or
// just "$X" when context window is unknown). Empty pair when no
// UsageTracker is wired.
func (m Model) usageSummaryStacked() (string, string) {
	if m.opts.UsageTracker == nil {
		return "", ""
	}
	t := m.opts.UsageTracker.SessionTotals()
	cost := m.opts.UsageTracker.SessionCostUSD()
	used := m.opts.UsageTracker.ContextWindowUsed()
	size := m.opts.UsageTracker.ContextWindowSize()
	sep := " " + GlyphSeparator + " "
	line1 := formatKTokens(t.InputTokens) + " in" + sep + formatKTokens(t.OutputTokens) + " out"
	line2 := fmt.Sprintf("$%.4f", cost)
	if size > 0 {
		line2 += sep + m.contextFillStyle(used, size).Render(
			formatKTokens(used)+" / "+formatKTokens(size),
		)
	}
	return line1, line2
}

// contextFillStyle picks a fg style for the "<used> / <size>"
// segment based on a 3-tier color ramp: green when below 60%,
// yellow 60-85%, red above 85% (per agentic-tui skill §17.C).
// Lets the operator see overflow risk before it bites.
func (m Model) contextFillStyle(used, size int) lipgloss.Style {
	if size <= 0 {
		return m.styles.Muted
	}
	pct := (used * 100) / size
	switch {
	case pct >= 85:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F5F")).Bold(true)
	case pct >= 60:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD75F"))
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#5FD787"))
	}
}

// formatKTokens renders an integer token count in compact human form
// — "1.5K" for 1500, "23K" for 23000, plain "850" for sub-1K. Mirrors
// the format the per-turn footer uses (R-USE-1).
func formatKTokens(n int) string {
	if n < 1000 {
		return strconv.Itoa(n)
	}
	if n < 10000 {
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	}
	return fmt.Sprintf("%dK", n/1000)
}

// subagentSummary renders the sidebar's subagent rows from a wired
// SubagentLister. Returns ("none") when the capability is unwired or
// the list is empty so the section reads consistently.
func (m Model) subagentSummary() []string {
	lister, ok := m.opts.Agent.(SubagentLister)
	if !ok {
		return []string{"none (no SubagentLister)"}
	}
	subs := lister.Subagents()
	if len(subs) == 0 {
		return []string{"none"}
	}
	out := make([]string, 0, len(subs))
	for _, s := range subs {
		row := s.Name + " [" + s.Status + "]"
		out = append(out, row)
	}
	return out
}
