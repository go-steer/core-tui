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
	"errors"
	"fmt"
	"strings"
	"time"
)

// This file collects the optional Agent capability interfaces (and
// their supporting data types) that hosts MAY implement to light up
// the corresponding slash command / UI affordance. The TUI feature-
// detects each via type assertion; missing capabilities degrade
// gracefully to a "not available" system message rather than failing.
//
// Spec source of truth: design.md §3.3 and requirements.md §3.
// Capabilities split across this file + agent.go are organized
// here-by-feature, there-by-streaming-essentials.

// ModelSwapper backs /model (R-MOD-1 / R-MOD-2).
type ModelSwapper interface {
	AvailableModels() []ModelInfo
	SwitchModel(modelID string) (Agent, error)
}

// ModelInfo is one entry in the /model picker.
type ModelInfo struct {
	ID          string
	Display     string // optional; defaults to ID when empty
	Description string // optional dim subtitle
}

// SessionSwitcher backs the /switch built-in (issue #53) and lets
// hosts that manage multiple sessions (e.g. a remote daemon with
// per-caller bearer auth) offer a first-class in-run session picker.
// Hosts that don't implement this capability can still ship a
// /switch by registering an AsyncSlashProvider command that returns
// a SlashResult with SwitchTo populated — /switch falls through to
// the SlashProvider path when the capability is absent.
//
// SwitchToSession returns a SwitchTarget the TUI applies via
// applySwitchTarget: local detach from the outgoing Agent + attach
// to the incoming one. See SwitchTarget's godoc for the lifecycle
// contract (host owns old-Agent teardown; core-tui only cancels
// LOCAL contexts, does not touch server-side sessions).
type SessionSwitcher interface {
	Sessions() []SessionInfo
	SwitchToSession(id string) (SwitchTarget, error)
}

// SessionInfo is one row in the /switch picker.
type SessionInfo struct {
	ID          string
	Display     string // optional; defaults to ID when empty
	Description string // optional dim subtitle
	Current     bool   // marks the currently-attached row

	// Input, when non-nil, turns this row into an ACTION row
	// rather than a session row (issue #56): Enter opens a
	// single-line text-input dialog on top of the picker instead
	// of calling SwitchToSession. The canonical use is a
	// "+ Attach to endpoint…" row on a multi-daemon host where
	// the target isn't enumerable — the operator types it.
	//
	// Action rows are never marked "(current)" and their ID is
	// never passed to SwitchToSession; the row's own Submit
	// closure produces the SwitchTarget. `/switch <id>` naming an
	// action row opens the same dialog.
	Input *SessionInput
}

// SessionInput describes the text-input dialog a SessionInfo action
// row opens on Enter, plus the closure that turns the typed value
// into a SwitchTarget. Submit is the only required field.
//
// Keeping Submit on the row (rather than routing back through
// SwitchToSession with a magic ID) means the host writes the
// "what does this row do" logic in exactly one place, and core-tui
// never has to guess which IDs are real sessions.
type SessionInput struct {
	// Title is the dialog title bar. Empty defaults to the row's
	// display name.
	Title string

	// Prompt is the question line above the input box, e.g.
	// "Daemon URL:".
	Prompt string

	// Placeholder is the dim hint shown while the box is empty,
	// e.g. "http://host:7778".
	Placeholder string

	// Initial pre-fills the box.
	Initial string

	// Validate is called with the trimmed value on Enter. A
	// non-empty return renders inline under the box and keeps the
	// dialog open. Nil accepts anything (including "").
	Validate func(value string) string

	// Submit turns the trimmed value into a SwitchTarget. A
	// non-nil error surfaces as a RoleError row and closes both
	// dialogs; the current session stays attached. Same contract
	// as SessionSwitcher.SwitchToSession — see SwitchTarget.
	//
	// It is called off the event loop, so it may block: dialling
	// the endpoint the operator just typed is the motivating case,
	// and the dialog stays up showing progress meanwhile. Two
	// consequences for implementations. It runs on its own
	// goroutine, so it must be safe to call while the TUI keeps
	// painting. And it can be cancelled from the operator's side —
	// esc closes the dialog while the call is still out — in which
	// case the returned SwitchTarget is DISCARDED rather than
	// attached. Anything Submit opened on the way to building that
	// target is the host's to release, exactly as it would be for a
	// target the TUI declined for any other reason.
	Submit func(value string) (SwitchTarget, error)
}

// Reloader backs /reload (R-RELOAD-1 / R-RELOAD-2).
type Reloader interface {
	Reload(ctx context.Context) (ReloadResult, error)
}

// ReloadResult is what Reload returns on success — the host
// constructs fresh views of every reload-able piece of state and
// the TUI atomically swaps to them.
type ReloadResult struct {
	Agent      Agent           // replaces the live agent
	Memory     []MemoryFile    // for /memory
	MCPServers []MCPServerInfo // for /mcp
	Skills     []SkillInfo     // for /skills
	Note       string          // optional one-line system-message confirmation
}

// MemoryFile is one entry in the /memory display.
type MemoryFile struct {
	Path      string
	Excerpt   string // optional first few lines for the display
	Bytes     int64  // optional file size; 0 = not tracked
	Truncated bool   // host reads only first N bytes when true
}

// MCPServerInfo is one entry in the /mcp display. Tools carries the
// per-server tool catalog (name + description) so /mcp can render a
// nested view; an empty slice falls back to the ToolCount summary.
type MCPServerInfo struct {
	Name      string
	Transport string // "stdio" / "http" / "sse" / "websocket"
	URL       string // empty for stdio
	Connected bool
	ToolCount int
	Tools     []MCPToolInfo
}

// MCPToolInfo is one tool exposed by an MCP server, for the /mcp
// nested rendering. Name is required; Description is optional and
// rendered indented under the name when present.
type MCPToolInfo struct {
	Name        string
	Description string
}

// SkillInfo is one entry in the /skills display.
type SkillInfo struct {
	Name        string
	Description string
	Source      string // "local" / "<mcp-server>" / etc.
	ToolCount   int
}

// PermissionController backs /permissions, /allow, /deny, and the
// persistence side of the permission-modal's allow-always decision
// (R-PERM-3 / R-PERM-4 / R-PERM-5).
type PermissionController interface {
	SessionApprovals() []ApprovalLog
	AddAllowPatterns(patterns []string) error
	AddDenyPatterns(patterns []string) error
	AddBuiltinAllowExtra(bundleName string) error
}

// ApprovalLog is one row in the /permissions review picker — the
// gate's recollection of every approval-shaped decision the
// operator made this session.
type ApprovalLog struct {
	Tool     string
	Key      string
	Decision string // "allow-once" / "allow-session" / "deny" / etc.
}

// PricingController backs /pricing refresh + /pricing set
// (R-PRICE-1).
type PricingController interface {
	Refresh(ctx context.Context) (summary string, err error)
	Set(modelID string, inputPerMTok, outputPerMTok float64) (summary string, err error)
}

// ToolLister backs /tools (R-CMD-1 table).
type ToolLister interface {
	Tools() []ToolInfo
}

// ToolInfo is one entry in the /tools modal.
type ToolInfo struct {
	Name        string
	Description string
	Source      string // "builtin" / "<mcp-server>" / "skill:<name>"
	GateState   string // "allowed" / "denied" / "ask" — current gate disposition
}

// SubagentReporter backs the /subagents surfaces: the roster
// (R-SUB-1) and the per-subagent turn drill-down (issue #71) — what a
// subagent actually DID, not just its name, status, and final report.
// The canonical implementation is core-agent's
// GET /sessions/{id}/agents and GET /sessions/{id}/agents/{name}/events.
//
// Until v0.21.0 the two methods were two interfaces, SubagentLister
// and SubagentEventReader, and every consumer took both. Nothing ever
// implemented one without the other, and a host that listed its
// subagents but could not say what they did left the roster's only
// affordance — drill in — pointing at nothing. See
// docs/api-audit.md §5.3.
//
// Three surfaces consume it, all off the render path:
//
//   - `/subagents` renders the roster from Subagents().
//   - `/subagents <name>` opens a detail overlay — the untruncated
//     report (issue #70) above a scrollable turn log.
//   - A running SYNC subagent's tool row grows a live preview block
//     underneath it, tailed while the call is in flight, which
//     collapses to a one-line summary when the result lands.
//
// SubagentEvents contract:
//
//   - Paged and cursored. since is a seq cursor; 0 means "from the
//     start". Return the page plus the cursor to resume from. The
//     tail poller calls this once a second with the previous
//     NextSince, so an implementation that ignores since will make
//     the TUI re-render the whole log every tick.
//   - Honor ctx. The TUI always passes a bounded one and discards
//     late results; a blocking implementation parks a background
//     goroutine, never the event loop.
//   - Report an unresolvable name as a *SubagentNotFoundError rather
//     than an empty page, so the UI can say "no such subagent, here
//     are the ones there are" instead of showing a plausible-looking
//     empty log. An empty page means "this subagent has recorded no
//     turns yet", which is a different and legitimate answer.
type SubagentReporter interface {
	Subagents() []SubagentInfo
	SubagentEvents(ctx context.Context, name string, since int64) (SubagentEventPage, error)
}

// SubagentInfo is one entry in the /subagents display.
type SubagentInfo struct {
	Name       string
	Status     string // "running" / "done" / "failed" / "paused"
	LastReport string // most recent alert / completion text (truncated)
	StartedAt  time.Time
}

// SubagentEventPage is one page of a subagent's inner turns.
type SubagentEventPage struct {
	// Events are the turns in chronological order.
	Events []SubagentEvent

	// NextSince is the cursor to pass back to fetch what comes
	// after this page. Hosts that can't produce a cursor may leave
	// it 0; the TUI then de-duplicates by Seq and re-reads from the
	// start each poll, which works but costs more.
	NextSince int64

	// Truncated reports that a page limit cut this response short
	// and there is more to fetch immediately from NextSince.
	Truncated bool
}

// SubagentEvent is one turn inside a subagent: what it said, what it
// called, and what came back. Every field is optional — a turn that
// only calls a tool has no Text, and a turn that only speaks has no
// calls.
type SubagentEvent struct {
	// Seq is the host's monotonic ordering key, also used to
	// de-duplicate across overlapping pages. 0 when the host has no
	// sequence number, in which case the TUI falls back to
	// append-in-arrival-order.
	Seq int64

	// Timestamp is when the turn was recorded; zero suppresses the
	// time column.
	Timestamp time.Time

	// Author is who produced the turn — the model, the user proxy,
	// a tool name, whatever vocabulary the host uses.
	Author string

	// Text is the turn's prose (model output, a report body).
	Text string

	// ToolCalls / ToolResults are the structured tool traffic. Args
	// and Response are the raw maps, same shape as ToolCall /
	// ToolResult on the streaming surface — the TUI does its own
	// summarizing so subagent rows read like the parent's.
	ToolCalls   []SubagentToolCall
	ToolResults []SubagentToolResult
}

// SubagentToolCall is one tool invocation inside a subagent turn.
type SubagentToolCall struct {
	ID   string
	Name string
	Args map[string]any
}

// SubagentToolResult is one tool result inside a subagent turn.
type SubagentToolResult struct {
	ID       string
	Name     string
	Response map[string]any
	Error    string
}

// SubagentNotFoundError is what SubagentReporter.SubagentEvents
// returns for a
// name it cannot resolve. Available carries the names that WOULD
// resolve, so the UI can name them instead of leaving the operator to
// guess at a spelling.
//
// The distinction this type exists to preserve: "no such subagent" is
// not "this subagent did nothing". A reader that flattens the two
// into an empty page makes the TUI render a convincing empty turn log
// for a typo — the exact failure go-steer/core-agent#694 fixed on the
// server side.
type SubagentNotFoundError struct {
	Name      string
	Available []string
}

func (e *SubagentNotFoundError) Error() string {
	if len(e.Available) == 0 {
		return fmt.Sprintf("no subagent named %q in this session", e.Name)
	}
	return fmt.Sprintf("no subagent named %q in this session (available: %s)",
		e.Name, strings.Join(e.Available, ", "))
}

// asSubagentNotFound unwraps err to a *SubagentNotFoundError.
// Wrapped is fine: a remote adapter that annotates the error with the
// request it failed still means "no such subagent".
func asSubagentNotFound(err error) (*SubagentNotFoundError, bool) {
	var nf *SubagentNotFoundError
	if errors.As(err, &nf) {
		return nf, true
	}
	return nil, false
}

// StatusReporter backs the persistent status surface (R-USE-2)
// when the host needs to surface non-trivial state. Most hosts
// leave model name + state for the TUI to derive from Options;
// implement StatusReporter when the agent has richer state
// (deferred / waiting / etc.) to surface.
//
// Status() must be safe for concurrent calls and must not block the
// caller for long: the TUI refreshes the status header off its event
// loop (see host_snapshot.go), so a slow implementation stalls only a
// background goroutine — but a panicking or data-racy one still breaks
// the TUI. Return cached/last-known state rather than doing unbounded
// I/O inline.
type StatusReporter interface {
	Status() Status
}

// Status is the bundle StatusReporter returns.
type Status struct {
	ModelName string
	State     string // "idle" / "running" / "deferred" / etc.
	Provider  string // "gemini" / "anthropic" / "vertex" / etc. — optional
}

// UsageTracker is the read-only side of the host's per-turn /
// session usage accounting (R-USE-1 / R-USE-3). The TUI snapshots
// values on each turn end to render the per-turn footer and the
// /stats output.
//
// Like StatusReporter, these accessors must be safe for concurrent
// calls and should return cached values without blocking — the TUI
// pulls them off its event loop to keep View() non-blocking.
type UsageTracker interface {
	SessionTotals() Usage           // input + output tokens, cumulative
	SessionCostUSD() float64        // accumulated dollar spend
	LastTurn() (Usage, float64)     // most-recent turn's usage + cost
	ContextWindowSize() int         // 0 when unknown
	ContextWindowUsed() int         // 0 when unknown
	SessionTurns() int              // 0 when unknown
	SessionDuration() time.Duration // 0 when unknown
}

// PathScope is the list of roots that bound `@file` palette lookups
// (R-SCOPE-1 / R-SCOPE-2). Empty list = no scope filtering.
type PathScope struct {
	Roots []string
}

// Allows reports whether path is inside any of the roots.
func (p PathScope) Allows(path string) bool {
	if len(p.Roots) == 0 {
		return true // no restriction
	}
	for _, r := range p.Roots {
		if r == "" {
			continue
		}
		if path == r {
			return true
		}
		if len(path) > len(r) && path[:len(r)] == r && (path[len(r)] == '/' || r[len(r)-1] == '/') {
			return true
		}
	}
	return false
}
