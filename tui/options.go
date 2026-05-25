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

// Options configures tui.Run.
type Options struct {
	// Agent is required.
	Agent Agent

	// Branding overrides the default house style on the axes listed in
	// R-BRAND-1. Zero value uses defaults.
	Branding Branding

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
)

// Branding overrides the brand-line and chrome strings. Empty fields
// fall back to the house defaults (style.md §1.1 + §8).
type Branding struct {
	Wordmark         string
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
