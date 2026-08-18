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
	"time"
)

// SlashProvider is an optional Agent capability: hosts that implement
// it on their Agent type can advertise additional slash commands the
// TUI merges into /help and the palette. Invocations dispatch back
// via InvokeSlash. Built-in command names always win on collision; a
// system warning is logged at startup when the agent's spec list
// shadows a built-in.
//
// See R-CMD-4 in requirements.md and design.md §3.3.
type SlashProvider interface {
	SlashCommands() []SlashCommandSpec
	InvokeSlash(ctx context.Context, name, args string) (SlashResult, error)
}

// SlashCommandSpec is one entry in the agent's command catalog.
// Name is the bare identifier (no leading "/"). Aliases are
// alternative invocations (e.g. {"by-the-way"} for /btw). Description
// renders in /help and as the dim subtitle in the palette.
type SlashCommandSpec struct {
	Name        string
	Aliases     []string
	Description string
}

// SlashResult is what InvokeSlash returns. Any subset of the fields
// may be populated:
//
//   - SystemMessage — a one-line confirmation that renders as a dim
//     italic system row in the chat history.
//   - ModalAnswer — a richer Q+A overlay rendered as a dismissable
//     Glamour-formatted modal. Used by /btw-style side questions
//     whose answer shouldn't pollute the persistent chat history.
//   - SwitchTo — instructs the TUI to detach from the current Agent
//     and reattach to the supplied one (issue #48). Non-nil triggers
//     a mid-run session swap through applySwitchTarget. Any
//     SystemMessage / ModalAnswer set on the same result is applied
//     against the OUTGOING session's chat (i.e. before the wipe)
//     unless the host prefers to surface post-switch context via
//     SwitchTarget.Note.
//
// When SwitchTo is nil and both SystemMessage / ModalAnswer are
// empty, the call ran but had nothing visible to say. When
// SystemMessage + ModalAnswer are both set, the modal renders first
// and the system message lands behind it.
type SlashResult struct {
	SystemMessage string
	ModalAnswer   *SideAnswer
	SwitchTo      *SwitchTarget
}

// SwitchTarget instructs the TUI to detach the current Agent's
// local subscriptions and attach to Agent (issue #48). Fields other
// than Agent are optional — non-nil / non-empty values REPLACE the
// corresponding Options field, nil / zero values leave the existing
// value in place (Memory / Skills / MCPServers: nil = keep, non-nil
// including empty = replace with the supplied slice).
//
// Lifecycle contract:
//
//   - Agent is required. SwitchTo with a nil Agent is rejected with a
//     RoleError row; the current session stays attached.
//   - Core-tui does NOT close, Detach, or otherwise touch the
//     OUTGOING Agent. The host owns its lifecycle — if teardown is
//     needed (closing a socket, releasing a bearer token) do it
//     inside SwitchToSession() before returning the new
//     SwitchTarget, or from the host's own slash handler before
//     returning the SlashResult.
//   - Core-tui cancels the LOCAL contexts it owns (streaming turn,
//     async slash, LiveAgent Events). Server-side sessions are
//     unaffected — a remote daemon observes a dropped reader and
//     keeps the session ticking per its own reattach policy. This
//     is a detach, NOT a kill.
//   - History wipes; the new session paints on a blank canvas.
//   - Chrome (theme, terminal size, permission mode, overlay stack
//     minus any open session picker) survives.
//
// See design.md §3.3 and issues #48 / #53.
type SwitchTarget struct {
	// Agent is the incoming Agent. Required.
	Agent Agent

	// UsageTracker replaces Options.UsageTracker when non-nil.
	// Typical: a fresh per-session tracker so /stats and the
	// status header reflect the new session's totals.
	UsageTracker UsageTracker

	// Prompter / Elicitor / Asker / Notifier replace the
	// corresponding Options fields when non-nil. Nil = keep the
	// existing subscriber so cross-session permission / elicit /
	// question / notice pipes keep working. Hosts that want to fully
	// sever those channels supply fresh instances here.
	Prompter PermissionPrompter
	Elicitor Elicitor
	Asker    Asker
	Notifier *Notifier

	// Memory / Skills / MCPServers replace the corresponding
	// Options fields when non-nil (nil-vs-empty matters: an empty
	// non-nil slice CLEARS the display).
	Memory     []MemoryFile
	Skills     []SkillInfo
	MCPServers []MCPServerInfo

	// Branding replaces Options.Branding wholesale when non-nil.
	// Nil = keep existing (common; the chrome is per-operator,
	// not per-session).
	Branding *Branding

	// Note is appended as a RoleSystem row after the switch
	// completes so the operator sees which session they landed
	// on (e.g. "Attached to session <sid>"). Empty = no row.
	Note string
}

// SideAnswer carries the operator's question + the agent's response
// for modal-style rendering. Used for /btw and similar side-channel
// Q&A flows that should display once and disappear (not lodge in
// chat history). When Err is non-nil the modal renders an error
// state instead of the Glamour-rendered answer body.
//
// See R-CMD-5 in requirements.md.
type SideAnswer struct {
	Question string
	Answer   string
	Err      error
}

// SlashResultOrErr bundles the SlashResult + error pair that
// InvokeSlashAsync's channel carries. Exactly one of Res / Err is
// meaningful per send.
type SlashResultOrErr struct {
	Res SlashResult
	Err error
}

// AsyncSlashProvider is the non-blocking variant of SlashProvider
// (issues #10 and #16). Hosts whose slash commands do network or file
// I/O implement this so the dispatch runs off the Update goroutine and
// the TUI stays responsive — every keystroke, render tick, and toast
// continues processing while the host's call is in flight.
//
// Implementation contract:
//   - InvokeSlashAsync returns (preamble, results). The preamble is
//     computed synchronously and appended to history as a RoleSystem
//     row BEFORE the goroutine that drains results is launched, so a
//     call that takes 5-15s (/done writing a checkpoint, /compact
//     writing a summary) says so in the chat flow, next to the prompt
//     the operator just typed, rather than only in the bottom-bar
//     toast. Return "" for no preamble.
//   - results is single-shot: core-tui reads exactly one
//     SlashResultOrErr and closes its tea.Cmd. Hosts send exactly one
//     value and then close (or just send and abandon — core-tui does
//     not re-read).
//   - The supplied ctx is cancellable; when the operator hits
//     Ctrl+C / Esc, core-tui cancels it and the host should bail as
//     fast as the underlying work allows. The eventual sent value is
//     discarded.
//   - A host satisfying BOTH SlashProvider and AsyncSlashProvider
//     prefers the async path. Built-in slash commands are not routed
//     here — they are synchronous-and-fast by design.
//
// Until v0.21.0 this was two interfaces: a bare variant returning the
// channel alone and AsyncSlashProviderWithPreamble returning the pair.
// They shared the method name InvokeSlashAsync and differed only in
// return signature, so no single Go type could satisfy both, and the
// preamble variant was a documented strict superset — an empty
// preamble behaves exactly like the bare one. Nothing anywhere
// implemented the bare variant. See docs/api-audit.md §5.2.
type AsyncSlashProvider interface {
	SlashCommands() []SlashCommandSpec
	InvokeSlashAsync(ctx context.Context, name, args string) (preamble string, results <-chan SlashResultOrErr)
}

// slashFlight tracks one pending AsyncSlashProvider call (issue #13).
// Name carries the slash identifier so the toast + status-line
// indicator can render "/<name> running…"; startedAt isn't read by
// any indicator today but lets future "running 8s…" / progress
// affordances ride the same struct without another model field.
//
// Lifecycle: created in dispatchSlash's async branch, cleared in
// the slashResultMsg handler (success, error, OR cancel — every
// path lands a slashResultMsg one way or another).
type slashFlight struct {
	name      string
	startedAt time.Time
}
