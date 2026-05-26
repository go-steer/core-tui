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

// Per-tool rendering strategy (agentic-tui skill §14). One
// ToolRenderer interface, one concrete renderer per well-known
// tool, plus a generic fallback; a factory dispatches by tool
// name. renderMessage's RoleTool case routes through the factory
// so per-tool layout / framing can diverge without growing the
// switch in renderMessage.
//
// Today the interface only renders the CALL (tool name + arg
// hint). Once tool RESULTS land on Message (currently absent —
// agent events deliver results as further Text chunks), each
// renderer will grow a RenderResult method for cap-and-expand
// styling per skill §14.

package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// ToolRenderer is the contract for one tool's call/result
// rendering. renderMessage feeds it the message, the styled head
// (already glyph + bold name), and the available width; the
// renderer returns the full styled string for the row.
//
// Implementations should be stateless / value receivers so the
// factory can hand out a single shared instance per tool.
type ToolRenderer interface {
	RenderCall(msg Message, head string, width int, styles Styles) string
}

// genericToolRenderer is the fallback used for any tool name not
// recognized by the factory. Mirrors the pre-strategy behavior:
// `⚙ name · arg-hint` on a single wrapped line.
type genericToolRenderer struct{}

func (genericToolRenderer) RenderCall(msg Message, head string, width int, styles Styles) string {
	if msg.ToolArgs == "" {
		return head
	}
	body := wordWrap(msg.ToolArgs, width-lipgloss.Width(head)-1)
	return head + " " + styles.ToolBody.Render(body)
}

// bashToolRenderer styles bash invocations as `⚙ bash · $ <cmd>`.
// The dollar sign is already prepended by toolArgHint, so this
// renderer's only distinguishing job today is to use Accent for
// the command body (operators scan for shell calls; coloring them
// brighter than generic tool calls helps).
type bashToolRenderer struct{}

func (bashToolRenderer) RenderCall(msg Message, head string, width int, styles Styles) string {
	if msg.ToolArgs == "" {
		return head
	}
	body := wordWrap(msg.ToolArgs, width-lipgloss.Width(head)-1)
	return head + " " + styles.Accent.Render(body)
}

// fileToolRenderer styles file-touching tools (read_file,
// write_file, edit_file) with a path-colored body so the file
// stands out from prose. toolArgHint already returns just the
// path for these tools.
type fileToolRenderer struct{}

func (fileToolRenderer) RenderCall(msg Message, head string, width int, styles Styles) string {
	if msg.ToolArgs == "" {
		return head
	}
	body := wordWrap(msg.ToolArgs, width-lipgloss.Width(head)-1)
	// Underlined path → reads as a "location" hint, parity with
	// how IDEs highlight file references in tool output.
	pathStyle := lipgloss.NewStyle().Foreground(styles.Theme.Info).Underline(true)
	return head + " " + pathStyle.Render(body)
}

var (
	rendererGeneric ToolRenderer = genericToolRenderer{}
	rendererBash    ToolRenderer = bashToolRenderer{}
	rendererFile    ToolRenderer = fileToolRenderer{}
)

// toolRendererFor returns the per-tool renderer for name, or the
// generic fallback. Name matching is case-insensitive on the
// well-known builtins; MCP tools (any name without a builtin
// match) get the generic renderer.
func toolRendererFor(name string) ToolRenderer {
	switch strings.ToLower(name) {
	case "bash":
		return rendererBash
	case "read_file", "write_file", "edit_file", "read_many_files":
		return rendererFile
	default:
		return rendererGeneric
	}
}
