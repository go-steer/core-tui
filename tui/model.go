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

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
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
	inProgressText string  // accumulator for streamed tokens
	currentUsage   *Usage  // most recent usage snapshot for this turn
	currentCost    float64 // most recent positive cost for this turn (USD)
	currentModel   string  // model name for the in-progress message

	// listCache memoizes the styled-string render of each history
	// Message keyed by (Message.ID, viewport width, Message.Version).
	// Without it every refreshViewport re-Glamour-renders every
	// assistant message — visible as stutter on long sessions. See
	// listcache.go for the cache contract.
	listCache *listCache

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
	toolActive             bool // true after a ToolCall; flips back on next Text
	seenToolIDs            map[string]bool
	thinkingIdx            int  // rotation index into ThinkingPhrases / WorkingPhrases
	spinnerActive          bool // gates spinner tick scheduling

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
	promptHistory []string
	historyCursor int
	historyDraft  string // user's in-flight input saved before navigation

	// startedAt is the wall-clock time the TUI launched. Used by
	// the transcript-on-exit hook so saved files name themselves
	// with the session-start instant.
	startedAt time.Time

	// modelPickerIdx is preserved only for the inline (non-Dialog)
	// legacy overlay render path that's still in renderOverlay's
	// vestigial body. Real picker state now lives inside
	// modelPickerDialog (see dialog_modelpicker.go).
	modelPickerIdx int

	// overlay is the dialog stack (agentic-tui skill §9). Model
	// picker rides this stack; permission / elicit / sideAnswer
	// still use their inline pendingX fields because the channel
	// lifecycle hasn't been decoupled yet.
	overlayStack Overlay

	// caps holds the env-sniffed terminal capability bag
	// (agentic-tui skill §18). Renderers consult this to gate
	// hyperlinks, clipboard sequences, etc. Detected once at
	// NewModel; hosts can override post-construction.
	caps TerminalCapabilities

	// spinnerCache holds the pre-rendered Braille frame strings
	// for the thinking spinner (agentic-tui skill §7). Rebuilt
	// on theme change (primary / secondary color update).
	spinnerCache *spinnerFrameCache

	// pendingForm is an embedded huh.Form (agentic-tui skill §12).
	// When non-nil, Update routes every tea.Msg into the form
	// first, intercepting all keystrokes; render shows it as a
	// centered modal. Today only /pricing set populates it; a
	// future PR migrates elicit forms here too once the
	// channel-based dispatch is wrapped.
	//
	// Typed as *huh.Form (not tea.Model) because huh's Update
	// returns its own compat.Model interface — not Bubble Tea
	// v2's tea.Model — and View returns a string, not tea.View.
	pendingForm *huh.Form

	// newlineHint is the keystroke we display in the footer for
	// "insert newline." Seeded from the detected terminal program
	// (VS Code → alt+enter, kitty/wezterm/iTerm → shift+enter,
	// everything else → ctrl+j) and overwritten the first time
	// the operator uses one of the three accepted combos. Stops
	// hints from lying when the user's terminal can't deliver the
	// suggested key.
	newlineHint string

	// activeToolID is the Message.ID of the in-flight tool call:
	// the most recent RoleTool message that hasn't been followed
	// by any assistant text or another tool. 0 = no active tool.
	// Renderer uses it to swap the tool glyph (▶ active vs › done)
	// and brighten the row so the operator's eye lands on "what
	// the model is doing RIGHT NOW" instead of scanning back.
	activeToolID uint64
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

	// Start the textarea with a transparent CursorLine style —
	// bubbles v2 textarea.New() applies DefaultDarkStyles which
	// paints the cursor line solid black; that's invisible on
	// dark terminals and a screaming black block on light ones.
	// We don't yet know dark/light (BackgroundColorMsg comes
	// post-Init), so pick the "safer" no-tint default and let
	// the BackgroundColorMsg handler swap in the right variant.
	ta.SetStyles(textareaStyles(true))

	vp := viewport.New()
	// The viewport's default KeyMap is full of vim conventions
	// (h/j/k/l + arrows for scroll; b/f for page; space for page
	// down; u/d for half-page; ctrl+u for half-page) — every one
	// of those collides with normal text input. handleKey forwards
	// every keystroke to the viewport at the end so typing "b"
	// into the prompt would PgUp, " " into the prompt would PgDn,
	// etc. We also can't rely on Right being safe: it sets
	// xOffset which then cuts chars off the LEFT of every line
	// (`ansi.Cut` at viewport.go:362).
	//
	// Override every binding with the non-letter form ONLY so the
	// page keys still work for power users but text-input letters
	// pass through cleanly. Mouse wheel is handled separately by
	// MouseMode.
	vp.KeyMap = viewport.KeyMap{
		PageDown:     newKeyBinding("pgdown"),
		PageUp:       newKeyBinding("pgup"),
		HalfPageDown: newKeyBinding("ctrl+d"),
		HalfPageUp:   newKeyBinding("ctrl+u"),
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
		listCache:     newListCache(),
		caps:          DetectCapabilities(),
		newlineHint:   defaultNewlineHint(DetectCapabilities().TermProgram),
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

// newKeyBinding is a tiny helper that builds a key.Binding from
// a single literal key name. Used in NewModel to replace the
// viewport's default KeyMap entries with non-letter-only forms
// so vim conventions (h/j/k/l/b/f/space/u/d) don't fire on
// every forwarded keystroke.
func newKeyBinding(k string) key.Binding {
	return key.NewBinding(key.WithKeys(k))
}

// defaultNewlineHint picks the most-likely-to-work newline
// keystroke for the given terminal program, used to seed the
// footer hint before the operator has actually pressed one.
//
//   - VS Code integrated terminal  → alt+enter (terminal-setup
//     binds Shift+Enter → \x1b\r, which bubbletea normalizes
//     to alt+enter)
//   - kitty / wezterm / iTerm2     → shift+enter (likely have the
//     keyboard-enhancement protocol enabled, so true shift+enter
//     reaches the app)
//   - everything else              → ctrl+j (ASCII LF, the most
//     portable; works unless something steals the binding)
func defaultNewlineHint(termProgram string) string {
	switch termProgram {
	case "vscode":
		return "alt+enter"
	case "kitty", "wezterm", "iterm.app", "iterm2", "ghostty":
		return "shift+enter"
	default:
		return "ctrl+j"
	}
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

// refreshTheme re-resolves Styles (picking up the active provider
// when AutoProviderTheme is on), invalidates the Glamour renderer
// + list cache so the next render uses the new palette, and
// rebuilds the textarea styles for the current dark/light mode.
// Called after any event that could change which theme applies:
// /model swap, dark/light flip, explicit theme reset.
func (m *Model) refreshTheme() {
	m.styles = m.resolveStyles(m.styles.Dark)
	m.markdown = nil
	if m.listCache != nil {
		m.listCache.reset(m.viewport.Width())
	}
	m.input.SetStyles(textareaStyles(m.styles.Dark))
}

// resolveStyles builds the Styles bundle for the current dark/
// light mode. When Options.AutoProviderTheme is true the
// StatusReporter's Provider tag picks the per-provider theme
// (Anthropic clay / Gemini blue / OpenAI green); otherwise the
// brand stays on DefaultTheme regardless of which model is
// active. Branding overrides still apply on top of whichever
// theme was picked. Called from BackgroundColorMsg (first-paint
// dark/light detect) and any time the active provider could
// have changed (post-/model swap).
func (m Model) resolveStyles(dark bool) Styles {
	var theme Theme
	if m.opts.AutoProviderTheme {
		theme = ThemeForProvider(m.displayProvider(), dark)
	} else {
		theme = DefaultTheme(dark)
	}
	if m.opts.Branding.AccentColor != "" {
		c := lipgloss.Color(m.opts.Branding.AccentColor)
		theme.Primary = c
		theme.Accent = c
		theme.BorderActive = c
	}
	if m.opts.Branding.SecondaryColor != "" {
		theme.Secondary = lipgloss.Color(m.opts.Branding.SecondaryColor)
	}
	return NewStylesWithTheme(dark, theme)
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
