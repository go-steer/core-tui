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
	"strings"
	"testing"
)

func TestComputeUnifiedDiff_NoChange(t *testing.T) {
	got := computeUnifiedDiff("foo.go", "hello\n", "hello\n")
	if got != "" {
		t.Fatalf("expected empty diff for unchanged content, got %q", got)
	}
}

func TestComputeUnifiedDiff_ProducesUnifiedFormat(t *testing.T) {
	old := "line one\nline two\nline three\n"
	new := "line one\nLINE TWO\nline three\n"
	got := computeUnifiedDiff("foo.go", old, new)
	if got == "" {
		t.Fatal("expected non-empty diff")
	}
	if !strings.Contains(got, "@@") {
		t.Errorf("expected hunk header, got: %q", got)
	}
	if !strings.Contains(got, "-line two") {
		t.Errorf("expected deletion line, got: %q", got)
	}
	if !strings.Contains(got, "+LINE TWO") {
		t.Errorf("expected addition line, got: %q", got)
	}
}

func TestRenderDiffInline_StripsFileHeaders(t *testing.T) {
	diff := "--- foo.go\n+++ foo.go\n@@ -1 +1 @@\n-old\n+new\n"
	styles := NewStyles(true, Branding{})
	got := renderDiffInline(diff, styles, 0, "")
	if strings.Contains(got, "--- foo.go") || strings.Contains(got, "+++ foo.go") {
		t.Errorf("expected file headers stripped, got: %q", got)
	}
	if !strings.Contains(got, "@@ -1 +1 @@") {
		t.Errorf("expected hunk header preserved, got: %q", got)
	}
	if !strings.Contains(got, "old") || !strings.Contains(got, "new") {
		t.Errorf("expected +/- lines preserved, got: %q", got)
	}
}

func TestRenderDiffInline_TruncatesAtMaxLines(t *testing.T) {
	// Build a 20-line diff body to overflow the cap.
	var b strings.Builder
	b.WriteString("@@ -1,20 +1,20 @@\n")
	for i := 0; i < 20; i++ {
		b.WriteString("-old line\n")
		b.WriteString("+new line\n")
	}
	styles := NewStyles(true, Branding{})
	got := renderDiffInline(b.String(), styles, 5, "")
	lines := strings.Split(got, "\n")
	if len(lines) > 6 { // 5 rendered + 1 truncation marker
		t.Errorf("expected truncation at maxLines, got %d lines:\n%s", len(lines), got)
	}
	if !strings.Contains(got, "more lines") {
		t.Errorf("expected truncation marker, got: %q", got)
	}
}

func TestRenderToolPreview_ApplyPatch(t *testing.T) {
	args := map[string]any{
		"patch": "--- a\n+++ a\n@@ -1 +1 @@\n-foo\n+bar\n",
	}
	styles := NewStyles(true, Branding{})
	got := renderToolPreview("apply_patch", args, styles)
	if got == "" {
		t.Fatal("expected non-empty preview")
	}
	if !strings.Contains(got, "foo") || !strings.Contains(got, "bar") {
		t.Errorf("expected diff body in preview, got: %q", got)
	}
}

func TestRenderToolPreview_EditFile(t *testing.T) {
	args := map[string]any{
		"path":     "src/foo.go",
		"old_text": "hello\nworld\n",
		"new_text": "hello\nWORLD\n",
	}
	styles := NewStyles(true, Branding{})
	got := renderToolPreview("edit_file", args, styles)
	if got == "" {
		t.Fatal("expected non-empty preview")
	}
	// Phase 2: +/- prefix is styled separately from the body, so
	// `-world` / `+WORLD` aren't contiguous strings — assert on
	// the bodies and on at least one styled prefix glyph instead.
	if !strings.Contains(got, "world") || !strings.Contains(got, "WORLD") {
		t.Errorf("expected diff bodies in preview, got: %q", got)
	}
}

func TestRenderToolPreview_UnknownTool(t *testing.T) {
	got := renderToolPreview("unknown_tool", map[string]any{"key": "value"}, NewStyles(true, Branding{}))
	if got != "" {
		t.Errorf("expected empty preview for unknown tool, got: %q", got)
	}
}

func TestRenderToolPreview_NilArgs(t *testing.T) {
	got := renderToolPreview("apply_patch", nil, NewStyles(true, Branding{}))
	if got != "" {
		t.Errorf("expected empty preview for nil args, got: %q", got)
	}
}

func TestDetectLang_GoFile(t *testing.T) {
	if got := detectLang("foo.go"); got != "Go" {
		t.Errorf("expected lang=Go for .go file, got %q", got)
	}
}

func TestDetectLang_NoMatch(t *testing.T) {
	if got := detectLang("README"); got != "" {
		t.Errorf("expected empty lang for extensionless name, got %q", got)
	}
	if got := detectLang(""); got != "" {
		t.Errorf("expected empty lang for empty label, got %q", got)
	}
}

func TestHighlightLine_EmptyLang(t *testing.T) {
	got := highlightLine("func main() {}", "")
	if got != "func main() {}" {
		t.Errorf("expected unchanged line for empty lang, got %q", got)
	}
}

func TestHighlightLine_KnownLang_DiffersFromInput(t *testing.T) {
	// Smoke test: highlighted output must contain the original
	// tokens but should differ in length (ANSI codes added).
	in := "func main() {}"
	got := highlightLine(in, "Go")
	if got == in {
		t.Errorf("expected highlighted output to differ from input for Go, got identical %q", got)
	}
	if !strings.Contains(got, "main") {
		t.Errorf("expected highlighted output to contain 'main' token, got %q", got)
	}
}

func TestHighlightLine_CacheReturnsSameValue(t *testing.T) {
	in := "x := 1"
	first := highlightLine(in, "Go")
	second := highlightLine(in, "Go")
	if first != second {
		t.Errorf("expected cache to return identical output for same (lang, line)")
	}
}

func TestRenderReadPreview_ReadFile_LineRange(t *testing.T) {
	args := map[string]any{
		"path":       "src/foo.go",
		"start_line": float64(10),
		"end_line":   float64(42),
	}
	styles := NewStyles(true, Branding{})
	got := renderReadPreview("read_file", args, styles)
	if !strings.Contains(got, "L10-L42") {
		t.Errorf("expected line range L10-L42 in preview, got %q", got)
	}
	if !strings.Contains(got, "go") {
		t.Errorf("expected detected lang 'go' in preview, got %q", got)
	}
}

func TestRenderReadPreview_ReadFile_Full(t *testing.T) {
	args := map[string]any{"path": "README.md"}
	styles := NewStyles(true, Branding{})
	got := renderReadPreview("read_file", args, styles)
	if !strings.Contains(got, "full") {
		t.Errorf("expected 'full' in preview when no range given, got %q", got)
	}
}

func TestRenderReadPreview_ReadFile_OffsetLimit(t *testing.T) {
	args := map[string]any{
		"path":   "main.go",
		"offset": float64(5),
		"limit":  float64(10),
	}
	styles := NewStyles(true, Branding{})
	got := renderReadPreview("read_file", args, styles)
	if !strings.Contains(got, "L5-L14") {
		t.Errorf("expected L5-L14 (offset=5, limit=10), got %q", got)
	}
}

func TestRenderReadPreview_ReadFile_NoPath(t *testing.T) {
	got := renderReadPreview("read_file", map[string]any{}, NewStyles(true, Branding{}))
	if got != "" {
		t.Errorf("expected empty preview when path missing, got %q", got)
	}
}

func TestRenderReadPreview_ReadManyFiles_Paths(t *testing.T) {
	args := map[string]any{
		"paths": []any{"a.go", "b.go", "c.go", "d.go", "e.go"},
	}
	styles := NewStyles(true, Branding{})
	got := renderReadPreview("read_many_files", args, styles)
	if !strings.Contains(got, "5 files") {
		t.Errorf("expected '5 files' count, got %q", got)
	}
	if !strings.Contains(got, "a.go") || !strings.Contains(got, "c.go") {
		t.Errorf("expected first 3 paths in preview, got %q", got)
	}
	if !strings.Contains(got, "+2 more") {
		t.Errorf("expected '+2 more' marker, got %q", got)
	}
}

func TestRenderReadPreview_ReadManyFiles_Pattern(t *testing.T) {
	args := map[string]any{"pattern": "*.go"}
	styles := NewStyles(true, Branding{})
	got := renderReadPreview("read_many_files", args, styles)
	if !strings.Contains(got, "*.go") {
		t.Errorf("expected pattern in preview, got %q", got)
	}
}

func TestRenderReadPreview_Grep(t *testing.T) {
	args := map[string]any{
		"pattern": "TODO",
		"path":    "lib/",
	}
	styles := NewStyles(true, Branding{})
	got := renderReadPreview("grep", args, styles)
	if !strings.Contains(got, "TODO") {
		t.Errorf("expected pattern 'TODO' in preview, got %q", got)
	}
	if !strings.Contains(got, "lib/") {
		t.Errorf("expected path 'lib/' in preview, got %q", got)
	}
}

func TestRenderReadPreview_Glob_PatternOnly(t *testing.T) {
	args := map[string]any{"pattern": "**/*.ts"}
	styles := NewStyles(true, Branding{})
	got := renderReadPreview("glob", args, styles)
	if !strings.Contains(got, "**/*.ts") {
		t.Errorf("expected pattern in preview, got %q", got)
	}
}

func TestRenderReadPreview_Grep_EmptyArgs(t *testing.T) {
	got := renderReadPreview("grep", map[string]any{}, NewStyles(true, Branding{}))
	if got != "" {
		t.Errorf("expected empty preview when no pattern/path, got %q", got)
	}
}

func TestRenderToolPreview_ReadFile_Routes(t *testing.T) {
	args := map[string]any{
		"path":       "main.go",
		"start_line": float64(1),
		"end_line":   float64(20),
	}
	got := renderToolPreview("read_file", args, NewStyles(true, Branding{}))
	if !strings.Contains(got, "L1-L20") {
		t.Errorf("expected dispatcher to route read_file → renderReadPreview, got %q", got)
	}
}

func TestRenderToolPreview_Grep_Routes(t *testing.T) {
	args := map[string]any{"pattern": "FIXME", "path": "src/"}
	got := renderToolPreview("grep", args, NewStyles(true, Branding{}))
	if !strings.Contains(got, "FIXME") {
		t.Errorf("expected dispatcher to route grep → renderReadPreview, got %q", got)
	}
}

func TestRenderDiffInline_WithLang_HighlightsBody(t *testing.T) {
	// When lang is set, the rendered output should differ from
	// the no-lang version — syntax highlighting injects ANSI codes
	// into the body of +/- lines.
	diff := "@@ -1 +1 @@\n-old := 1\n+new := 2\n"
	styles := NewStyles(true, Branding{})
	plain := renderDiffInline(diff, styles, 0, "")
	highlighted := renderDiffInline(diff, styles, 0, "Go")
	if plain == highlighted {
		t.Errorf("expected highlighted output to differ from plain, both:\n%q", plain)
	}
}
