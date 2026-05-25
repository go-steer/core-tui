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

	// StatusLayout picks the status surface (R-USE-2).
	StatusLayout StatusLayout

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
}

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
