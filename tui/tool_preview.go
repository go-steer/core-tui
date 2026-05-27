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

// Per-tool preview computation (docs/inline-tool-display-design.md
// §3). Routes the tool name to a per-tool builder that returns a
// multi-line string to attach under the tool row.
//
// Phase 1 covered diff-producing tools (apply_patch / patch /
// edit_file / replace / str_replace). Phase 2 adds read_file /
// read_many_files / grep / glob scope previews via
// renderReadPreview, and routes the diff path through the
// syntax cache by passing detectLang(label) into
// renderDiffInline. A summary row "⎿  +N -M" is prepended to
// diff previews so the operator sees the magnitude of the change
// at a glance without scanning the body — inspired by Claude
// Code's `⎿  Added N lines, removed M lines` summary line.

package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// previewLineCap bounds inline previews — long diffs collapse
// with a "… +N more" marker. Matches the 8-line "default-render"
// threshold from the design doc.
const previewLineCap = 8

// renderToolPreview returns the multi-line preview to attach
// under the tool row for the given tool call. Returns "" when
// the tool isn't recognized as preview-worthy or args don't
// carry the data we'd render. Never returns an error — preview
// is a nice-to-have, NEVER blocks tool-row rendering.
func renderToolPreview(name string, args map[string]any, styles Styles) string {
	if args == nil {
		return ""
	}
	switch name {
	case "apply_patch", "patch":
		// Args carry a pre-formed unified diff; just render. The
		// path arg (when present) seeds syntax highlighting.
		label := stringArg(args, "path", "file", "filename")
		diff := stringArg(args, "patch", "diff", "content")
		return diffPreviewWithSummary(diff, styles, detectLang(label))
	case "edit_file", "replace", "str_replace":
		// Args carry old + new text; compute the diff.
		oldText := stringArg(args, "old_text", "old_string", "old", "search")
		newText := stringArg(args, "new_text", "new_string", "new", "replace")
		if oldText == "" && newText == "" {
			return ""
		}
		label := stringArg(args, "path", "file", "filename")
		if label == "" {
			label = "edit"
		}
		diff := computeUnifiedDiff(label, oldText, newText)
		return diffPreviewWithSummary(diff, styles, detectLang(label))
	case "read_file", "read_many_files", "grep", "glob":
		return renderReadPreview(name, args, styles)
	}
	return ""
}

// diffPreviewWithSummary renders a diff with a "⎿  +N -M" totals
// row prepended above the syntax-highlighted body. The summary is
// derived from the diff args at call time so the operator sees the
// magnitude immediately without waiting for the result event.
//
// Returns "" when the diff carries no add/remove lines (e.g. a
// no-op apply_patch); the body would also be empty, so no preview
// row makes sense.
func diffPreviewWithSummary(diff string, styles Styles, lang string) string {
	if diff == "" {
		return ""
	}
	added, removed := countDiffStats(diff)
	body := renderDiffInline(diff, styles, previewLineCap, lang)
	if added == 0 && removed == 0 {
		return body
	}
	summary := formatDiffSummary(added, removed, styles)
	if body == "" {
		return summary
	}
	return summary + "\n" + body
}

// formatDiffSummary builds the "⎿  +N -M" line for the diff
// totals. + count in Success / bold, - count in Error / bold,
// muted "⎿" tree-branch glyph as the visual anchor.
func formatDiffSummary(added, removed int, styles Styles) string {
	addStyle := lipgloss.NewStyle().Foreground(styles.Theme.Success).Bold(true)
	delStyle := lipgloss.NewStyle().Foreground(styles.Theme.Error).Bold(true)
	parts := []string{}
	if added > 0 {
		parts = append(parts, addStyle.Render("+"+itoa(added)))
	}
	if removed > 0 {
		parts = append(parts, delStyle.Render("-"+itoa(removed)))
	}
	return styles.Muted.Render(summaryIndent) + strings.Join(parts, " ")
}

// stringArg returns the first non-empty string value from args
// for any of the given keys. Lets per-tool builders match the
// 2-3 conventional arg names different agents use for the same
// concept (e.g. "old_text" vs "old_string" vs "search").
func stringArg(args map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := args[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}
