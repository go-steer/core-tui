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

// Full tool-call detail rendering (core-tui #52 tiers 1 + 2). The
// compact per-tool renderers in tool_preview.go / tool_preview_result.go
// truncate args at 80 chars and cap result bodies at 8 lines — the
// right default for the transcript, but useless when the operator
// is debugging a specific call ("did the model actually pass
// outputFormat=YAML? what did the MCP tool return?").
//
// renderToolDetail is the shared "give me EVERYTHING" formatter:
// pretty-printed JSON for args + response, with an error banner
// when the call failed. Consumed by two surfaces:
//
//   - Tier 2 (verbose flag): appended below the compact preview
//     inline in the transcript when Options.ToolDetailVerbose is on.
//     Threaded via update.go at the applyToolCall / applyToolResult
//     callsites — the existing per-tool render funcs stay unchanged.
//   - Tier 1 (expand overlay): the body of dialog_toolcall.go, where
//     the same content renders in a modal that the operator opens
//     on-demand with Ctrl+X for any past tool call in the session.

package tui

import (
	"encoding/json"
	"fmt"
	"strings"
)

// detailIndent prefixes every line of the rendered detail block so
// it visually anchors under the tool row (matches summaryIndent's
// ⎿ family of leading whitespace elsewhere in the tool renderers).
const detailIndent = "    "

// detailValueByteCap bounds a single JSON scalar's rendered length
// so a 20k-line YAML string doesn't blow the transcript width.
// Larger than the compact perLineByteCap (80) because verbose /
// expand-overlay callers explicitly asked for detail; still capped
// because glamour + lipgloss don't cope with megabytes cheerfully.
const detailValueByteCap = 4000

// detailMaxLines is a per-section (args, response) safety valve
// against runaway output. The overlay is scrollable so this is a
// generous ceiling, not a design constraint.
const detailMaxLines = 400

// renderToolDetail returns the raw args + response block for a
// single tool call. Layout:
//
//	args:
//	  {
//	    "path": "…",
//	    "range": [1, 20]
//	  }
//	response:
//	  {"content": "…"}
//
// Missing sections are omitted (call-time with no result yet =
// args only; error case = args + red error line, response
// skipped). Returns "" when there's literally nothing to render.
//
// Both call sites — the verbose-inline path and the expand-single
// overlay — already show the tool name above this block, so the
// name isn't included here.
//
// Never returns an error — detail is a nice-to-have, NEVER blocks
// tool-row rendering or dialog display. Malformed args/response
// get a "(unrenderable: …)" muted line instead of panicking.
func renderToolDetail(args, response map[string]any, errStr string, styles Styles) string {
	sections := make([]string, 0, 3)

	if len(args) > 0 {
		sections = append(sections, renderDetailSection("args", args, styles))
	}

	if strings.TrimSpace(errStr) != "" {
		sections = append(sections, renderDetailError(errStr, styles))
	} else if len(response) > 0 {
		sections = append(sections, renderDetailSection("response", response, styles))
	}

	if len(sections) == 0 {
		return ""
	}
	// Blank line between sections so the args block and the
	// response block don't visually run together.
	return strings.Join(sections, "\n\n")
}

// renderDetailSection formats one labeled JSON block. Marshals via
// json.MarshalIndent so callers see a stable, human-readable shape.
// Falls back to fmt.Sprintf when the value isn't JSON-marshalable
// (unlikely for map[string]any but cheap insurance).
func renderDetailSection(label string, payload map[string]any, styles Styles) string {
	head := styles.Muted.Render(detailIndent + label + ":")
	body := marshalPretty(payload)
	body = capBytesPerLine(body, detailValueByteCap)
	body = capLines(body, detailMaxLines)
	// Indent one level deeper than the label so the JSON braces
	// line up visually under the label text.
	body = indentBlock(body, detailIndent+"  ")
	return head + "\n" + styles.Muted.Render(body)
}

// renderDetailError paints the tool's error string as a red
// "error: <message>" banner. Distinct from renderResultError
// (tool_preview_result.go) because that one truncates at 200 chars
// for the compact preview; the detail view shows the full text
// under the same styling since operators opened the overlay
// specifically to see it.
func renderDetailError(errStr string, styles Styles) string {
	text := strings.TrimSpace(errStr)
	if text == "" {
		text = "(failed)"
	}
	head := styles.Muted.Render(detailIndent + "error:")
	body := indentBlock(text, detailIndent+"  ")
	return head + "\n" + styles.ErrorText.Render(body)
}

// marshalPretty JSON-encodes v with 2-space indent. Uses a fallback
// representation when the value doesn't round-trip cleanly (rare
// for map[string]any where every leaf is a JSON-compatible primitive
// coming off the SSE stream, but non-JSON-marshalable types
// occasionally sneak in via untyped interface{} paths).
func marshalPretty(v any) string {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("(unrenderable: %v)", err)
	}
	return string(raw)
}

// indentBlock prefixes every line of s with prefix. Preserves the
// terminal newline / no-newline shape of the input.
func indentBlock(s, prefix string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

// capBytesPerLine truncates each line of s at maxBytes with a
// trailing ellipsis so a single monster scalar (e.g. a base64 blob)
// doesn't wrap into a screenful. Multi-line preservation is
// intentional — the detail body isn't a single logical line.
func capBytesPerLine(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if len(line) > maxBytes {
			lines[i] = line[:maxBytes] + "…"
		}
	}
	return strings.Join(lines, "\n")
}

// capLines truncates s to the first maxLines lines with a footer
// showing how many were dropped. maxLines <= 0 disables the cap.
func capLines(s string, maxLines int) string {
	if maxLines <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= maxLines {
		return s
	}
	dropped := len(lines) - maxLines
	head := lines[:maxLines]
	head = append(head, fmt.Sprintf("… +%d more line%s", dropped, plural(dropped)))
	return strings.Join(head, "\n")
}
