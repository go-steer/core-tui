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

// ForceTheme values for Options.ForceTheme. "" (the zero value)
// means "auto" — let core-tui's OSC-11 query decide. The strings
// match what core-agent's UIConfig writes to JSON so hosts can
// pass cfg.UI.Theme through unchanged.
const (
	ThemeAuto  = ""
	ThemeDark  = "dark"
	ThemeLight = "light"
)

// Options configures tui.Run.
type Options struct {
	// Agent is required.
	Agent Agent

	// Branding overrides the default house style on the axes listed in
	// R-BRAND-1. Zero value uses defaults.
	Branding Branding

	// AutoProviderTheme opts the host in to per-provider palette
	// tinting (Anthropic clay / Gemini blue / OpenAI green) based
	// on StatusReporter.Status().Provider. Defaults to false so
	// the brand palette stays consistent across model swaps; hosts
	// that prefer the per-provider identity flip it on. Branding
	// overrides still apply on top of whichever theme is picked.
	AutoProviderTheme bool

	// ForceTheme overrides core-tui's terminal-background auto-
	// detection (OSC-11 → BackgroundColorMsg → m.styles.Dark). One
	// of "" (auto, the default — query the terminal), "dark", or
	// "light". Useful on terminals where the OSC-11 query is
	// unreliable (some SSH stacks, tmux passthrough quirks). When
	// set, BackgroundColorMsg is ignored so a stale or wrong
	// response can't override the operator's explicit choice.
	ForceTheme string

	// InitialThemeName seeds the named theme at startup. Resolved
	// case-insensitively against tui.BuiltinThemes; unknown names
	// fall through to DefaultTheme. Set this when the host has
	// persisted a previous /theme pick (observed via
	// ThemeChangedMsg). Empty leaves the theme on the auto /
	// per-provider path (see AutoProviderTheme).
	InitialThemeName string

	// Mouse toggles terminal mouse capture. nil (the default) keeps
	// MouseModeCellMotion on so the wheel scrolls the viewport;
	// *false disables capture entirely (MouseModeNone), restoring
	// the terminal's native click-drag text-select. Operators on
	// terminals that handle wheel scrolling natively, or who prefer
	// text-select-without-Shift, flip this off. The /mouse slash
	// flips it at runtime; this option is the startup default.
	Mouse *bool

	// PermissionLayout picks how the permission prompt is rendered
	// when the gate asks for approval (R-PERM-1). Zero value =
	// PermissionInline: the prompt renders as a block inside the
	// chat viewport flow, right under the tool call that triggered
	// it, preserving the assistant context. PermissionOverlay
	// renders a centered modal that dims the chat — more
	// attention-grabbing, less context.
	PermissionLayout PermissionLayout

	// StatusLayout picks the status surface (R-USE-2). The initial
	// value is whatever the host sets here; the user can flip it at
	// runtime via Ctrl+B.
	StatusLayout StatusLayout

	// PersistStatusLayout is called when the user toggles the status
	// layout at runtime so the host can write the choice to a
	// settings file. Hosts that read it back into StatusLayout on
	// the next launch give users a layout preference that survives
	// restarts. Nil means the toggle stays session-local.
	PersistStatusLayout func(StatusLayout) error

	// PersistModelChoice is called when the operator picks a new
	// model in the /model picker (R-MOD-3). Hosts persist the
	// choice to their config + read it back on next launch so the
	// preference survives restarts. Nil means the choice stays
	// session-local.
	PersistModelChoice func(modelID string) error

	// PersistThemeChoice is called when the operator picks a new
	// theme via the /theme picker (or `/theme <name>` with a
	// known name). Mirrors PersistModelChoice: hosts persist the
	// name to their config + seed it back via InitialThemeName on
	// next launch. Nil means the theme stays session-local. Hosts
	// can ALSO observe ThemeChangedMsg in their Update loop for
	// the same notification — pick whichever pattern fits the
	// host's architecture (callback = less code; msg = no
	// Options field needed).
	PersistThemeChoice func(name string) error

	// PermissionMode wires the permission-mode chip (R-PERM-6 / R-PERM-7).
	// Zero value hides the chip and disables Shift+Tab cycling.
	PermissionMode PermissionModeWiring

	// ThinkingPhrases / WorkingPhrases override the spinner verb pools
	// (R-CHAT-3). Nil uses built-in defaults.
	ThinkingPhrases []string
	WorkingPhrases  []string

	// SeedHistory pre-populates the chat with example messages. Used by
	// the examples/local visual-preview binary; production hosts leave
	// this nil.
	SeedHistory []Message

	// Prompter is the TUI-provided PermissionPrompter that the host
	// wires into its permission gate before the first turn (R-PERM-1).
	// Hosts construct one via tui.NewPrompter() and pass it both
	// into the gate (`gate.SetPrompter(prompter)`) AND here. The TUI
	// drains the prompter's request channel and renders a modal
	// for each inbound request.
	Prompter PermissionPrompter

	// Elicitor is the TUI-provided Elicitor that the host wires
	// into each MCP server's elicit callback before MCP connect
	// (R-ELIC-1). Construct via tui.NewElicitor().
	Elicitor Elicitor

	// Notifier is the host-facing side channel for chat rows
	// that don't belong to the agent event stream (issue #30):
	// reconnect notices, host-shutdown warnings, multi-attach
	// signals, version-mismatch errors, etc. Construct via
	// tui.NewNotifier(); call Notifier.Notify(text) from any
	// goroutine. The TUI drains the channel and renders each
	// notice as a RoleNotice row (◇ glyph + muted color) —
	// visually distinct from RoleSystem so operators can tell
	// "framework speaking" from "agent system response". Nil
	// (the default) disables the side channel; existing
	// "yield-through-agent-stream" workarounds keep working.
	Notifier *Notifier

	// AlwaysAllow is invoked when the operator picks
	// DecisionAllowAlways in the permission modal (R-PERM-3). The
	// host persists the entry to its allowlist; on nil callback the
	// TUI falls back to allow-session and logs a system message.
	AlwaysAllow func(req PermissionRequest) error

	// UsageTracker provides per-turn + session totals for the status
	// surface (R-USE-2) and /stats (R-USE-1). Optional — when nil
	// the per-turn footer renders only the Usage / Model / Elapsed
	// fields the agent populates directly on the Message and the
	// session-total slot in the status surface stays empty.
	UsageTracker UsageTracker

	// AgentsDir is the path the TUI writes the on-exit transcript
	// to (R-TR-1) when non-empty.
	AgentsDir string

	// Memory / MCPServers / Skills feed the display-only slash
	// commands (/memory, /mcp, /skills). Optional — when nil the
	// corresponding slash renders an empty list with a hint about
	// configuring the host.
	Memory     []MemoryFile
	MCPServers []MCPServerInfo
	Skills     []SkillInfo

	// PathScope is the list of roots the @file palette filters
	// against (R-SCOPE-1). Empty means no scope filtering.
	PathScope PathScope

	// MidTurnInjectionMode picks what happens when the operator
	// submits a prompt while a turn is in flight (R-CHAT-11). Zero
	// value (`QueueForNext`) preserves the R-CHAT-10 default:
	// buffer the entry as Queued, auto-drain on turn-end.
	// `InjectIntoCurrent` routes the entry through
	// `InjectableAgent.Inject` instead so it lands in the running
	// turn's context — falls back to `QueueForNext` when the agent
	// doesn't satisfy `InjectableAgent`.
	MidTurnInjectionMode MidTurnInjectionMode

	// AutoContinueFormatter wraps the slice of drained inbox
	// messages into a single prompt string for the synthetic
	// auto-continue turn. Only consulted when MidTurnInjectionMode
	// == AutoContinueFromInbox AND the agent satisfies
	// InboxDrainer. Nil falls back to defaultAutoContinueFormatter
	// (a bulleted "[Operator notes added during the previous task]"
	// frame followed by a "Continue." instruction).
	//
	// Receives the same []string DrainInbox returned, in order,
	// after the TUI has already removed empty strings. Return
	// value becomes the prompt of a fresh turn.
	AutoContinueFormatter func([]string) string

	// AutoContinueCap is the soft limit on chained auto-continues
	// between operator-initiated turns. After this many consecutive
	// auto-continue turns without the operator typing a fresh
	// prompt, the loop pauses and a system note tells the operator
	// the remaining drained messages will land on their next
	// submission. 0 (zero value) uses DefaultAutoContinueCap.
	// Negative disables the cap entirely (use with care — a typo-
	// fast operator can pile messages faster than the model
	// answers).
	AutoContinueCap int
}

// MidTurnInjectionMode controls operator-typed-during-streaming
// routing (R-CHAT-11).
type MidTurnInjectionMode int

const (
	// QueueForNext (default) buffers the entry as a Queued queue
	// row; drains on the next turn-end (R-CHAT-10).
	QueueForNext MidTurnInjectionMode = iota
	// InjectIntoCurrent calls InjectableAgent.Inject so the entry
	// lands in the running turn's context. The queue entry renders
	// immediately as Done with an "injected" suffix.
	InjectIntoCurrent
	// AutoContinueFromInbox is the "opaque-runner" mode (issue #9):
	// operator-typed-during-streaming entries call Inject AND stay
	// Queued in the panel. On turn end, the TUI calls
	// InboxDrainer.DrainInbox to pull all queued operator messages,
	// formats them via Options.AutoContinueFormatter (or a default
	// framing), and submits as a synthetic auto-continue turn —
	// the resulting user-row renders with the ↻ glyph + muted
	// style so the operator can tell which turns they typed and
	// which came from the auto-continue. Matching queue entries
	// flip Queued → Done.
	//
	// Falls back to QueueForNext when the agent doesn't satisfy
	// InboxDrainer (no runtime error). Soft cap on consecutive
	// auto-continues (Options.AutoContinueCap, default
	// DefaultAutoContinueCap) prevents runaway loops.
	AutoContinueFromInbox
)

// DefaultAutoContinueCap is the fallback consecutive-auto-continue
// limit when Options.AutoContinueCap is unset. After this many
// chained auto-continues without an operator-initiated turn in
// between, the TUI logs a system note and stops — the next batch
// of inbox messages will land on the operator's next prompt.
const DefaultAutoContinueCap = 10

// Branding overrides the brand-line and chrome strings. Empty fields
// fall back to the house defaults (style.md §1.1 + §8).
type Branding struct {
	Wordmark string
	// AgentIdentity is the operator's per-deployment label for the
	// running agent — typically `cfg.Agent.DisplayName` from the
	// host's config. When set AND not equal to Wordmark, the
	// status-line banner renders "<wordmark> · <identity> · …" so
	// the operator can tell which agent they're talking to in
	// multi-window setups (parity with core-agent's internal/tui).
	// Empty falls back to the bare wordmark.
	AgentIdentity    string
	AccentColor      string
	SecondaryColor   string
	CursorColor      string
	EmptyStateHint   string
	FooterHint       string
	InputPlaceholder string
}

// StatusLayout picks the persistent status surface (R-USE-2).
type StatusLayout int

const (
	// StatusHeader places a single status line above the chat (default).
	StatusHeader StatusLayout = iota
	// StatusSidebar places a fixed-width right-hand panel.
	StatusSidebar
)

// PermissionLayout picks how permission prompts render (R-PERM-1).
type PermissionLayout int

const (
	// PermissionInline (default) renders the prompt as a block
	// inside the chat viewport flow — under the tool call that
	// triggered it. Preserves context; the decision is part of
	// the natural conversation scroll.
	PermissionInline PermissionLayout = iota
	// PermissionOverlay renders a centered modal that dims the
	// chat. Most attention-grabbing; covers the surrounding
	// context until the operator decides.
	PermissionOverlay
)

// PermissionModeWiring backs the permission-mode chip (R-PERM-6 /
// R-PERM-7). When Set is nil the chip is hidden.
type PermissionModeWiring struct {
	Initial PermissionMode
	Set     func(PermissionMode) error
	Persist func(PermissionMode) error
}

// PermissionMode is the agent-wide approval policy.
type PermissionMode int

const (
	PermissionModeDefault     PermissionMode = iota // every tool call asks
	PermissionModeAcceptEdits                       // file-edit tools auto-allow
	PermissionModePlan                              // no tool calls execute
	PermissionModeBypass                            // every tool call auto-allows
)

// String returns the canonical name of the mode.
func (m PermissionMode) String() string {
	switch m {
	case PermissionModeAcceptEdits:
		return "acceptEdits"
	case PermissionModePlan:
		return "plan"
	case PermissionModeBypass:
		return "bypassPermissions"
	default:
		return "default"
	}
}

// Next returns the next mode in the Shift+Tab cycle.
func (m PermissionMode) Next() PermissionMode {
	return (m + 1) % 4
}
