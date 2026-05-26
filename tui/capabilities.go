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

// SubagentLister backs /subagents (R-SUB-1 read-only v1).
type SubagentLister interface {
	Subagents() []SubagentInfo
}

// SubagentInfo is one entry in the /subagents display.
type SubagentInfo struct {
	Name       string
	Status     string // "running" / "done" / "failed" / "paused"
	LastReport string // most recent alert / completion text (truncated)
	StartedAt  time.Time
}

// StatusReporter backs the persistent status surface (R-USE-2)
// when the host needs to surface non-trivial state. Most hosts
// leave model name + state for the TUI to derive from Options;
// implement StatusReporter when the agent has richer state
// (deferred / waiting / etc.) to surface.
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
