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
	got := renderDiffInline(diff, styles, 0)
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
	got := renderDiffInline(b.String(), styles, 5)
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
	if !strings.Contains(got, "-world") || !strings.Contains(got, "+WORLD") {
		t.Errorf("expected diff lines, got: %q", got)
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
