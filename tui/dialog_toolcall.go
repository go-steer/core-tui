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

// Expand-single tool-call detail overlay (core-tui #52 tier 1).
// Ctrl+X opens the dialog on the most recent tool call in the
// session; ←/→ (and PgUp/PgDn) walk to older / newer calls;
// ↑/↓ scroll long detail bodies; Esc closes.
//
// This overlay was built when there was no transcript cursor, so it
// presents its own navigable list rather than requiring the operator
// to highlight a row first. Issue #152 later added a cursor, and
// issue #233 connected the two: opened from focus mode with a tool
// row selected, the overlay starts on THAT call rather than the
// newest one. The list is still the navigation — the cursor only
// chooses where it starts, and every case that has no usable cursor
// still starts at the most recent call. All the raw data it renders
// (args, response, error) is already client-side — see
// Message.ToolArgsMap / ToolResponseMap / ToolError stashed by
// applyToolResult — so no additional wire-protocol changes are
// needed for this tier.
//
// Composes with tier 3 (core-agent#277 + core-tui#60): once per-call
// latency / token-delta arrive on the SSE stream, they surface here
// as an additional row in the header banner without changing the
// overlay's navigation shape.

package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

const toolCallDialogID = "tool-call-detail"

// toolCallDialogChromeRows is the row budget this modal spends on
// chrome: RenderContext's modalChromeRows (title line, blank, blank,
// footer rule, footer text) plus the two rows this dialog puts
// INSIDE Body — the header banner and the blank line under it.
//
// Counted rather than guessed, and derived from modalChromeRows
// rather than restated, so a change to the shared chrome reaches
// here too. The predecessor constant was a hardcoded 8 against a
// six-item list, which is how it stayed wrong through #142.
const toolCallDialogChromeRows = modalChromeRows + 2

// Default preferred width when there's terminal room. The dialog
// clamps down to whatever fits when the terminal is narrower.
const toolCallDialogPreferredWidth = 96

// toolCallDialog owns two pieces of state: which tool call in the
// session is currently focused (idx into the filtered snapshot at
// render time) and the vertical scroll offset into that call's
// detail body.
type toolCallDialog struct {
	idx    int
	scroll int
	// lastBody / lastView cache the geometry of the most recent
	// render so wheel ticks can clamp without re-rendering the
	// detail body. Keys don't need them (Render re-clamps), but the
	// wheel arrives between renders in bursts.
	lastBody int
	lastView int
}

// newToolCallDialog constructs a fresh overlay focused on the
// most-recent tool call. Callers OPEN via overlay.Open; the Overlay
// container owns lifecycle.
func newToolCallDialog(toolCount int) *toolCallDialog {
	last := toolCount - 1
	if last < 0 {
		last = 0
	}
	return &toolCallDialog{idx: last, scroll: 0}
}

func (d *toolCallDialog) ID() string { return toolCallDialogID }

func (d *toolCallDialog) HandleKey(stroke string, m *Model) DialogAction {
	tools := collectToolCalls(m.history.Snapshot())
	n := len(tools)
	if n == 0 {
		// Nothing to show — close cleanly. The keybinding shouldn't
		// have opened us with an empty session, but the guard is
		// cheap insurance against a /clear racing an open.
		return DialogAction{Consumed: true, Close: true}
	}
	// Keep idx in range even when tools shrank between renders
	// (unlikely in practice — history only grows within a session —
	// but /clear can wipe it).
	if d.idx >= n {
		d.idx = n - 1
	}
	if d.idx < 0 {
		d.idx = 0
	}
	switch stroke {
	case "esc":
		return DialogAction{Consumed: true, Close: true}
	case "left", "ctrl+p", "pgup":
		if d.idx > 0 {
			d.idx--
			d.scroll = 0
		}
		return DialogAction{Consumed: true}
	case "right", "ctrl+n", "pgdown", "pgdn":
		if d.idx < n-1 {
			d.idx++
			d.scroll = 0
		}
		return DialogAction{Consumed: true}
	case "home", "g":
		d.idx = 0
		d.scroll = 0
		return DialogAction{Consumed: true}
	case "end", "G":
		d.idx = n - 1
		d.scroll = 0
		return DialogAction{Consumed: true}
	case "up", "k":
		if d.scroll > 0 {
			d.scroll--
		}
		return DialogAction{Consumed: true}
	case "down", "j":
		d.scroll++
		return DialogAction{Consumed: true}
	}
	// Unhandled key — consume so it doesn't leak to the textarea
	// behind the modal, but don't close.
	return DialogAction{Consumed: true}
}

// ScrollBy implements ScrollDialog: mouse-wheel ticks move the
// detail body, clamped against the last render's geometry.
func (d *toolCallDialog) ScrollBy(delta int, _ *Model) {
	d.scroll = min(nonNeg(d.scroll+delta), nonNeg(d.lastBody-d.lastView))
}

func (d *toolCallDialog) Render(totalWidth int, m *Model) string {
	width := toolCallDialogPreferredWidth
	if totalWidth > 0 && width > totalWidth-4 {
		width = totalWidth - 4
	}
	if width < 40 {
		width = 40
	}

	tools := collectToolCalls(m.history.Snapshot())
	if len(tools) == 0 {
		return RenderContext{
			Title:  "Tool call detail",
			Body:   m.styles.Muted.Render("(no tool calls in this session yet)"),
			Footer: "esc close",
			Width:  width,
			Height: m.height,
			Styles: m.styles,
		}.Render()
	}

	if d.idx >= len(tools) {
		d.idx = len(tools) - 1
	}
	if d.idx < 0 {
		d.idx = 0
	}
	tool := tools[d.idx]

	// Header banner: "3/8  ·  read_file  ·  id abc123"
	header := renderToolCallHeader(d.idx, len(tools), tool, m.styles)

	// Body: full detail block, clamped to available viewport
	// height and scrolled by d.scroll.
	detail := renderToolDetail(tool.ToolArgsMap, tool.ToolResponseMap, tool.ToolError, m.styles)
	if strings.TrimSpace(detail) == "" {
		detail = m.styles.Muted.Render(detailIndent + "(no args / response captured for this call)")
	}
	bodyLines := strings.Split(detail, "\n")

	viewport := toolCallBodyHeight(m.height)
	d.lastBody, d.lastView = len(bodyLines), viewport
	d.scroll = min(nonNeg(d.scroll), nonNeg(len(bodyLines)-viewport))
	// modalBodyWidth reserves the scrollbar column and its gutter so
	// the body doesn't reflow the moment the detail starts
	// overflowing; the further two columns are slack this overlay has
	// always had.
	visible := scrollView(m.styles, bodyLines, nonNeg(modalBodyWidth(width)-2), viewport, d.scroll)
	body := header + "\n\n" + strings.Join(visible, "\n")

	footer := renderToolCallFooter(len(tools), len(bodyLines), viewport)
	return RenderContext{
		Title:  "Tool call detail",
		Body:   body,
		Footer: footer,
		Width:  width,
		Height: m.height,
		Styles: m.styles,
	}.Render()
}

// renderToolCallHeader builds the top banner: which-of-how-many,
// tool name, wire-level call ID, and a state hint (pending / failed).
// Kept compact — the interesting content is the JSON body below.
func renderToolCallHeader(idx, total int, tool Message, styles Styles) string {
	name := tool.ToolName
	if name == "" {
		name = "(unknown tool)"
	}
	bold := lipgloss.NewStyle().Bold(true)
	parts := []string{
		styles.Accent.Render(fmt.Sprintf("%d/%d", idx+1, total)),
		bold.Render(name),
	}
	if tool.ToolCallID != "" {
		parts = append(parts, styles.Muted.Render("id "+tool.ToolCallID))
	}
	if lat := formatLatency(tool.ToolLatencyMs); lat != "" {
		parts = append(parts, styles.Muted.Render(lat))
	}
	if sv := formatSavingsCompact(tool.ToolSavings); sv != "" {
		parts = append(parts, styles.Muted.Render(sv))
	}
	if strings.TrimSpace(tool.ToolError) != "" {
		parts = append(parts, styles.ErrorText.Render("✘ failed"))
	} else if tool.ToolResponseMap == nil {
		parts = append(parts, styles.Muted.Render("(pending)"))
	}
	sep := "  " + styles.Muted.Render(GlyphSeparator) + "  "
	return detailIndent + strings.Join(parts, sep)
}

// renderToolCallFooter shows the key affordances plus a scroll hint
// only when the body actually overflows. Kept out of the dialog
// method receiver so it's easy to test standalone.
func renderToolCallFooter(total, bodyLines, viewport int) string {
	parts := []string{}
	if total > 1 {
		parts = append(parts, "← → walk")
	}
	if bodyLines > viewport {
		parts = append(parts, "↑↓ scroll")
	}
	parts = append(parts, "esc close")
	return strings.Join(parts, "  "+GlyphSeparator+"  ")
}

// collectToolCalls returns every RoleTool message from the snapshot
// in transcript order. Kept small and pure so the same helper backs
// the "should we even open the dialog" guard in Update and the
// per-render enumeration here.
func collectToolCalls(snap []Message) []Message {
	out := make([]Message, 0, len(snap))
	for _, m := range snap {
		if m.Role == RoleTool {
			out = append(out, m)
		}
	}
	return out
}

// indexOfToolCall maps a transcript row onto the tool-call list this
// dialog indexes into, and returns -1 when that row is not in the
// list at all.
//
// Matching is by Message.ID rather than by counting RoleTool rows up
// to the cursor. Both are correct against the same snapshot, but the
// ID is the identity History already issues and the fold map already
// keys by, so it stays right if the two ever read different
// snapshots — which a counted position would not.
func indexOfToolCall(tools []Message, id uint64) int {
	for i, t := range tools {
		if t.ID == id {
			return i
		}
	}
	return -1
}

// toolCallBodyHeight is how many detail rows fit at the current
// terminal height.
//
// It is modalBodyHeight like every other modal (issue #149). The
// predecessor helper predated #142's height regime and did its own
// arithmetic: terminal height minus a chrome constant of 8, minus an
// unexplained further 6, floored at 6 rather than at
// minModalBodyRows. On a 12-row terminal that floor handed back a
// 6-row body against 7 rows of chrome — a 13-row modal in 12 rows,
// with clipFrame taking the difference off the bottom, which is
// where the footer is. It also never went fullscreen, so it kept
// reserving a margin at heights where the margin was already fiction.
func toolCallBodyHeight(termHeight int) int {
	return modalBodyHeight(termHeight, toolCallDialogChromeRows)
}

// Bold is a helper style commonly used across renderers; if the
// underlying Styles struct doesn't expose it as a distinct field,
// the tests below will surface the missing surface immediately.
var _ = lipgloss.Left
