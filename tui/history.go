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

// Len returns the entry count.
func (h *History) Len() int {
	return len(h.entries)
}
