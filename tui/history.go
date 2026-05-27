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

import "time"

// Role tags each entry in the chat log so the renderer can pick the
// right style and glyph.
type Role int

const (
	RoleUser Role = iota
	RoleAssistant
	RoleSystem
	RoleError
	RoleTool
)

// Message is one entry in the rolling chat log.
type Message struct {
	Role Role
	Text string

	// Rendered caches the Glamour-rendered form of Text for assistant
	// messages after a turn completes (R-CHAT-4). Empty during stream.
	Rendered string

	// ToolName, ToolArgs populated when Role == RoleTool.
	ToolName string
	ToolArgs string
	// ToolPreview is the multi-line block that renders under the
	// tool row when the tool call has previewable content (unified
	// diff for apply_patch / edit_file, read scope summary,
	// result content). Pre-computed at applyToolCall time so the
	// lazy-list cache caches it as part of the row; re-computed at
	// applyToolResult time so the same field carries both call-only
	// and call+result variants. Empty = no preview.
	ToolPreview string
	// ToolCallID is the wire-level tool-call ID from the agent
	// event (e.g. genai.FunctionCall.ID). Stored on RoleTool
	// messages so applyToolResult can locate the matching row when
	// a tool-result event arrives. Empty when the host doesn't
	// emit per-call IDs.
	ToolCallID string
	// ToolArgsMap stashes the structured call-time args so
	// applyToolResult can re-render ToolPreview with both original
	// call info and the freshly-arrived result — renderToolPreview
	// needs path / range to format result content sensibly
	// (e.g. lang detection from the read_file path).
	ToolArgsMap map[string]any

	// Per-turn metadata populated by the TUI on the final assistant
	// Message of each turn so the renderer can append a one-line
	// `◇ Model · 8.4K in · 2.1K out · $0.012 · 4s` footer (R-USE-1).
	// Nil / zero values suppress the footer.
	Usage   *Usage
	Model   string
	Elapsed time.Duration
	CostUSD float64

	// ID is the stable identity History.Append assigns so the lazy-
	// render cache (listcache.go) can key entries across refreshes.
	// 0 until Append; preserved across SetRendered mutations.
	ID uint64

	// Version increments on every mutation that changes rendered
	// output (currently SetRendered on resize). The lazy-render
	// cache treats version mismatch as an invalidation signal.
	Version uint64

	// AutoContinue marks a RoleUser message that was synthesized by
	// the AutoContinueFromInbox loop (issue #9) rather than typed
	// by the operator. The renderer swaps the usual ❯ prefix +
	// brand-bg card for a muted ↻ prefix so operators can tell at
	// a glance which turns they initiated. False (zero) on every
	// other Message; the field is meaningless for non-RoleUser
	// rows.
	AutoContinue bool
}

// Display returns the renderable string for this message, preferring
// the cached Glamour render when available.
func (m Message) Display() string {
	if m.Rendered != "" {
		return m.Rendered
	}
	return m.Text
}

// History is the in-memory transcript backing the viewport.
type History struct {
	entries []Message
	nextID  uint64 // monotonic Message.ID assigner
}

// Append adds an entry to the end. Assigns a fresh Message.ID
// (preserved across SetRendered mutations) so the lazy-render
// cache can key entries stably.
func (h *History) Append(m Message) {
	h.nextID++
	m.ID = h.nextID
	m.Version = 0
	h.entries = append(h.entries, m)
}

// Snapshot returns a copy of every entry, in order.
func (h *History) Snapshot() []Message {
	out := make([]Message, len(h.entries))
	copy(out, h.entries)
	return out
}

// Reset empties the history. Used by /clear.
func (h *History) Reset() {
	h.entries = nil
}

// SetRendered overwrites the cached Glamour render on entry i and
// bumps the entry's Version so the lazy-render cache invalidates.
// Used by the resize path to refresh wrapping at the new width.
// Out-of-range i is a silent no-op so callers can pass the
// snapshot index without bounds-checking.
func (h *History) SetRendered(i int, rendered string) {
	if i < 0 || i >= len(h.entries) {
		return
	}
	h.entries[i].Rendered = rendered
	h.entries[i].Version++
}

// LastID returns the Message.ID of the most-recent entry, or 0
// when the history is empty. Used by the tool-call lifecycle to
// stash the active tool's identity right after Append.
func (h *History) LastID() uint64 {
	if len(h.entries) == 0 {
		return 0
	}
	return h.entries[len(h.entries)-1].ID
}

// BumpVersion finds the entry with the given ID and bumps its
// Version so the lazy-render cache invalidates the row. Used to
// signal "active tool" transitions (active → done) without
// touching the Message's content. No-op when id == 0 or no
// matching entry.
func (h *History) BumpVersion(id uint64) {
	if id == 0 {
		return
	}
	for i := range h.entries {
		if h.entries[i].ID == id {
			h.entries[i].Version++
			return
		}
	}
}

// FindByToolCallID locates the RoleTool entry whose wire-level
// ToolCallID matches the given id and returns its slice index, or
// -1 when no match exists. Used by applyToolResult to attach a
// freshly-arrived result to the correct row.
func (h *History) FindByToolCallID(callID string) int {
	if callID == "" {
		return -1
	}
	for i := range h.entries {
		if h.entries[i].ToolCallID == callID {
			return i
		}
	}
	return -1
}

// SetToolPreview overwrites the cached tool preview on entry i and
// bumps the entry's Version so the lazy-render cache invalidates.
// Used by applyToolResult to swap the call-only preview for the
// call+result preview. Out-of-range i is a silent no-op.
func (h *History) SetToolPreview(i int, preview string) {
	if i < 0 || i >= len(h.entries) {
		return
	}
	h.entries[i].ToolPreview = preview
	h.entries[i].Version++
}

// MarkLastUserAutoContinue flips Message.AutoContinue=true on the
// most-recently-appended RoleUser entry (and bumps its Version so
// the lazy-render cache invalidates). Used by the AutoContinueFromInbox
// loop (issue #9): submitTurn appends the RoleUser as an operator-
// typed prompt; this helper retro-fits the synthesized marker so
// the renderer picks the ↻ glyph + muted style on the next paint.
// No-op when there's no RoleUser entry in history.
func (h *History) MarkLastUserAutoContinue() {
	for i := len(h.entries) - 1; i >= 0; i-- {
		if h.entries[i].Role == RoleUser {
			h.entries[i].AutoContinue = true
			h.entries[i].Version++
			return
		}
	}
}

// Len returns the entry count.
func (h *History) Len() int {
	return len(h.entries)
}
