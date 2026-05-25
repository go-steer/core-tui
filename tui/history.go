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
}

// Append adds an entry to the end.
func (h *History) Append(m Message) {
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

// Len returns the entry count.
func (h *History) Len() int {
	return len(h.entries)
}
