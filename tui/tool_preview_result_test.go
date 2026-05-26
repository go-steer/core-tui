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

func TestRenderToolPreviewWithResult_CallAndResultJoin(t *testing.T) {
	args := map[string]any{"path": "main.go"}
	response := map[string]any{"content": "package main\n\nfunc main() {}\n"}
	got := renderToolPreviewWithResult("read_file", args, response, "", NewStyles(true, Branding{}))
	if !strings.Contains(got, "full") || !strings.Contains(got, "go") {
		t.Errorf("expected call-scope summary in joined output, got:\n%q", got)
	}
	if !strings.Contains(got, "main") {
		t.Errorf("expected result content (function name) in joined output, got:\n%q", got)
	}
	// Call and result are separated by a newline.
	if strings.Count(got, "\n") == 0 {
		t.Errorf("expected newline-joined call + result, got single line:\n%q", got)
	}
}

func TestRenderToolPreviewWithResult_ErrorOverridesResult(t *testing.T) {
	args := map[string]any{"path": "main.go"}
	got := renderToolPreviewWithResult(
		"read_file", args, nil, "permission denied",
		NewStyles(true, Branding{}),
	)
	if !strings.Contains(got, "error:") || !strings.Contains(got, "permission denied") {
		t.Errorf("expected error row in joined output, got:\n%q", got)
	}
	// Call-scope ("full · go") should still show alongside the error.
	if !strings.Contains(got, "full") {
		t.Errorf("expected call scope preserved alongside error, got:\n%q", got)
	}
}

func TestRenderToolResult_ReadFileShowsContent(t *testing.T) {
	args := map[string]any{"path": "foo.go"}
	response := map[string]any{"content": "package foo\n\nfunc Bar() {}\n"}
	got := renderToolResult("read_file", args, response, "", NewStyles(true, Branding{}))
	if !strings.Contains(got, "package") || !strings.Contains(got, "Bar") {
		t.Errorf("expected file content tokens in result, got:\n%q", got)
	}
}

func TestRenderToolResult_ReadFileTruncates(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 20; i++ {
		b.WriteString("line ")
		b.WriteString(itoa(i))
		b.WriteString("\n")
	}
	args := map[string]any{"path": "long.go"}
	response := map[string]any{"content": b.String()}
	got := renderToolResult("read_file", args, response, "", NewStyles(true, Branding{}))
	if !strings.Contains(got, "more lines") {
		t.Errorf("expected truncation marker, got:\n%q", got)
	}
}

func TestRenderToolResult_BashStdoutShown(t *testing.T) {
	response := map[string]any{"stdout": "hello world\nsecond line\n"}
	got := renderToolResult("bash", nil, response, "", NewStyles(true, Branding{}))
	if !strings.Contains(got, "hello world") {
		t.Errorf("expected stdout in bash result, got:\n%q", got)
	}
}

func TestRenderToolResult_BashStderrSummarized(t *testing.T) {
	response := map[string]any{
		"stdout": "ok\n",
		"stderr": "warning: deprecated\nmore detail\n",
	}
	got := renderToolResult("bash", nil, response, "", NewStyles(true, Branding{}))
	if !strings.Contains(got, "stderr:") || !strings.Contains(got, "warning: deprecated") {
		t.Errorf("expected stderr label + first line, got:\n%q", got)
	}
	// Second line of stderr should NOT be inlined — only the first.
	if strings.Contains(got, "more detail") {
		t.Errorf("expected stderr second line elided, got:\n%q", got)
	}
}

func TestRenderToolResult_BashNonZeroExit(t *testing.T) {
	response := map[string]any{
		"stdout":    "",
		"exit_code": float64(2),
	}
	got := renderToolResult("bash", nil, response, "", NewStyles(true, Branding{}))
	if !strings.Contains(got, "exit 2") {
		t.Errorf("expected 'exit 2' for non-zero exit, got:\n%q", got)
	}
}

func TestRenderToolResult_GrepMatchesShown(t *testing.T) {
	response := map[string]any{
		"matches": []any{"file.go:12: TODO", "file.go:34: TODO again"},
	}
	got := renderToolResult("grep", nil, response, "", NewStyles(true, Branding{}))
	if !strings.Contains(got, "TODO") || !strings.Contains(got, "TODO again") {
		t.Errorf("expected matches in grep result, got:\n%q", got)
	}
}

func TestRenderToolResult_GlobPathsShown(t *testing.T) {
	response := map[string]any{
		"paths": []any{"src/a.go", "src/b.go", "src/c.go"},
	}
	got := renderToolResult("glob", nil, response, "", NewStyles(true, Branding{}))
	if !strings.Contains(got, "src/a.go") {
		t.Errorf("expected paths in glob result, got:\n%q", got)
	}
}

func TestRenderToolResult_WriteFileShowsBytes(t *testing.T) {
	response := map[string]any{"bytes_written": float64(1536)}
	got := renderToolResult("write_file", nil, response, "", NewStyles(true, Branding{}))
	if !strings.Contains(got, "wrote") {
		t.Errorf("expected 'wrote' in write_file result, got:\n%q", got)
	}
	// 1536 bytes formats as 1.5K.
	if !strings.Contains(got, "1.5K") {
		t.Errorf("expected '1.5K' byte formatting, got:\n%q", got)
	}
}

func TestRenderToolResult_EditAddedRemoved(t *testing.T) {
	response := map[string]any{
		"lines_added":   float64(5),
		"lines_removed": float64(2),
	}
	got := renderToolResult("edit_file", nil, response, "", NewStyles(true, Branding{}))
	if !strings.Contains(got, "+5") || !strings.Contains(got, "-2") {
		t.Errorf("expected '+5' and '-2' in edit summary, got:\n%q", got)
	}
	if !strings.Contains(got, "applied:") {
		t.Errorf("expected 'applied:' label, got:\n%q", got)
	}
}

func TestRenderToolResult_ErrorShortCircuits(t *testing.T) {
	// Even a populated response is ignored when err is non-empty.
	response := map[string]any{"content": "shouldn't show"}
	got := renderToolResult("read_file", nil, response, "boom", NewStyles(true, Branding{}))
	if strings.Contains(got, "shouldn't show") {
		t.Errorf("expected content suppressed on error, got:\n%q", got)
	}
	if !strings.Contains(got, "boom") {
		t.Errorf("expected error message in output, got:\n%q", got)
	}
}

func TestRenderToolResult_UnknownToolEmptyResult(t *testing.T) {
	got := renderToolResult("custom_mcp_tool", nil, map[string]any{"x": "y"}, "", NewStyles(true, Branding{}))
	if got != "" {
		t.Errorf("expected empty result for unknown tool, got:\n%q", got)
	}
}

func TestRenderToolResult_NilResponseEmpty(t *testing.T) {
	got := renderToolResult("read_file", nil, nil, "", NewStyles(true, Branding{}))
	if got != "" {
		t.Errorf("expected empty result for nil response, got:\n%q", got)
	}
}

func TestFormatBytes(t *testing.T) {
	cases := map[int]string{
		0:           "0B",
		1:           "1B",
		1023:        "1023B",
		1024:        "1.0K",
		1536:        "1.5K",
		1024 * 1024: "1.0M",
	}
	for in, want := range cases {
		if got := formatBytes(in); got != want {
			t.Errorf("formatBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestApplyToolResult_UpdatesPreview(t *testing.T) {
	h := &History{}
	h.Append(Message{
		Role:        RoleTool,
		ToolName:    "read_file",
		ToolCallID:  "call-1",
		ToolArgsMap: map[string]any{"path": "main.go"},
		ToolPreview: "    full · go",
	})

	// Mimic applyToolResult's side effect via the History helpers
	// directly (Model construction is heavier than these tests need).
	idx := h.FindByToolCallID("call-1")
	if idx < 0 {
		t.Fatal("expected to find tool by call ID")
	}
	snap := h.Snapshot()
	response := map[string]any{"content": "package main\n"}
	preview := renderToolPreviewWithResult("read_file", snap[idx].ToolArgsMap, response, "", NewStyles(true, Branding{}))
	h.SetToolPreview(idx, preview)

	final := h.Snapshot()[idx]
	if final.Version == 0 {
		t.Errorf("expected Version bump after SetToolPreview")
	}
	if !strings.Contains(final.ToolPreview, "package") {
		t.Errorf("expected updated preview to contain result content, got:\n%q", final.ToolPreview)
	}
}

func TestFindByToolCallID_NoMatch(t *testing.T) {
	h := &History{}
	h.Append(Message{Role: RoleTool, ToolCallID: "call-a"})
	if got := h.FindByToolCallID("call-b"); got != -1 {
		t.Errorf("expected -1 for missing ID, got %d", got)
	}
	if got := h.FindByToolCallID(""); got != -1 {
		t.Errorf("expected -1 for empty ID, got %d", got)
	}
}
