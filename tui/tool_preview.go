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
// Phase 1 covers diff-producing tools (apply_patch / patch /
// edit_file / replace). read_* / grep / glob metadata previews
// land in Phase 2.

package tui

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
		// Args carry a pre-formed unified diff; just render.
		return renderDiffInline(stringArg(args, "patch", "diff", "content"), styles, previewLineCap)
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
		return renderDiffInline(computeUnifiedDiff(label, oldText, newText), styles, previewLineCap)
	}
	return ""
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
