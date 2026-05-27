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

// Read-tool previews (docs/inline-tool-display-design.md §3.4-3.6).
// We don't get tool RESULTS as events today, so the preview shows
// the SCOPE of the read (range, count, pattern) — enough for the
// operator to know what context the model was just given without
// switching panes.

package tui

import "strings"

// renderReadPreview returns a one-line muted summary for read-
// shaped tools. Returns "" when args don't carry enough signal —
// preview is never required, the tool row stands on its own.
func renderReadPreview(name string, args map[string]any, styles Styles) string {
	if args == nil {
		return ""
	}
	const indent = summaryIndent
	switch name {
	case "read_file":
		return renderReadFilePreview(args, styles, indent)
	case "read_many_files":
		return renderReadManyFilesPreview(args, styles, indent)
	case "grep", "glob":
		return renderSearchPreview(args, styles, indent)
	}
	return ""
}

// renderReadFilePreview formats `L<a>-L<b> · <lang>` (or
// `full · <lang>`) for a single-file read. Bytes are intentionally
// omitted in v1 — getting them would mean a filesystem stat per
// tool call, which is gated to Phase 3 (Options.DiskReadOnPreview).
func renderReadFilePreview(args map[string]any, styles Styles, indent string) string {
	path := stringArg(args, "path", "file", "filename")
	if path == "" {
		return ""
	}
	startLine, hasStart := intArg(args, "start_line", "offset", "start")
	endLine, hasEnd := intArg(args, "end_line", "end")
	limit, hasLimit := intArg(args, "limit", "count")
	var rangeStr string
	switch {
	case hasStart && hasEnd:
		rangeStr = "L" + itoa(startLine) + "-L" + itoa(endLine)
	case hasStart && hasLimit:
		rangeStr = "L" + itoa(startLine) + "-L" + itoa(startLine+limit-1)
	case hasStart:
		rangeStr = "L" + itoa(startLine) + "+"
	case hasLimit:
		rangeStr = "L1-L" + itoa(limit)
	default:
		rangeStr = "full"
	}
	parts := []string{rangeStr}
	if lang := detectLang(path); lang != "" {
		parts = append(parts, strings.ToLower(lang))
	}
	return styles.Muted.Render(indent + strings.Join(parts, " · "))
}

// renderReadManyFilesPreview formats `N files · a, b, c, +K more`
// for a batch read. Falls back to `pattern: "..."` when args only
// carry a glob pattern instead of an explicit path list.
func renderReadManyFilesPreview(args map[string]any, styles Styles, indent string) string {
	if paths := stringSliceArg(args, "paths", "files"); len(paths) > 0 {
		head := paths
		if len(head) > 3 {
			head = head[:3]
		}
		body := itoa(len(paths)) + " files · " + strings.Join(head, ", ")
		if len(paths) > 3 {
			body += ", +" + itoa(len(paths)-3) + " more"
		}
		return styles.Muted.Render(indent + body)
	}
	if pattern := stringArg(args, "pattern", "glob"); pattern != "" {
		return styles.Muted.Render(indent + "pattern: \"" + pattern + "\"")
	}
	return ""
}

// renderSearchPreview formats `pattern: "X" · path: Y` for grep /
// glob. We don't have result counts yet (no tool-result event
// stream), so the preview shows search scope only.
func renderSearchPreview(args map[string]any, styles Styles, indent string) string {
	parts := []string{}
	if pattern := stringArg(args, "pattern", "query", "regex"); pattern != "" {
		parts = append(parts, "pattern: \""+pattern+"\"")
	}
	if path := stringArg(args, "path", "dir", "directory"); path != "" {
		parts = append(parts, "path: "+path)
	}
	if len(parts) == 0 {
		return ""
	}
	return styles.Muted.Render(indent + strings.Join(parts, " · "))
}

// intArg returns the first int-coercible value from args for any
// of the given keys. Handles int / int64 / float64 (JSON-decoded
// numbers arrive as float64) so per-tool builders don't have to
// re-type-switch the same shapes.
func intArg(args map[string]any, keys ...string) (int, bool) {
	for _, k := range keys {
		v, ok := args[k]
		if !ok {
			continue
		}
		switch n := v.(type) {
		case int:
			return n, true
		case int64:
			return int(n), true
		case float64:
			return int(n), true
		}
	}
	return 0, false
}

// stringSliceArg returns the first string-list-coercible value
// from args for any of the given keys. Handles []string / []any
// (JSON-decoded arrays arrive as []any); skips non-string elements
// in []any rather than erroring so partial data still surfaces.
func stringSliceArg(args map[string]any, keys ...string) []string {
	for _, k := range keys {
		v, ok := args[k]
		if !ok {
			continue
		}
		switch s := v.(type) {
		case []string:
			if len(s) > 0 {
				return s
			}
		case []any:
			out := make([]string, 0, len(s))
			for _, it := range s {
				if str, ok := it.(string); ok && str != "" {
					out = append(out, str)
				}
			}
			if len(out) > 0 {
				return out
			}
		}
	}
	return nil
}
