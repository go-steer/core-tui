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
	"fmt"
	"strings"
	"testing"
)

func TestRenderToolDetail_ArgsAndResponse(t *testing.T) {
	styles := NewStyles(true, Branding{})
	args := map[string]any{"path": "main.go", "range": []any{1, 20}}
	response := map[string]any{"content": "package main\nfunc main() {}\n"}
	got := renderToolDetail(args, response, "", styles)
	if !strings.Contains(got, "args:") {
		t.Errorf("expected 'args:' header, got:\n%q", got)
	}
	if !strings.Contains(got, "response:") {
		t.Errorf("expected 'response:' header, got:\n%q", got)
	}
	if !strings.Contains(got, "main.go") {
		t.Errorf("expected args body to include path, got:\n%q", got)
	}
	if !strings.Contains(got, "package main") {
		t.Errorf("expected response body to include content, got:\n%q", got)
	}
}

func TestRenderToolDetail_ErrorSuppressesResponse(t *testing.T) {
	styles := NewStyles(true, Branding{})
	args := map[string]any{"path": "missing.txt"}
	// Response would show if the error weren't there; the error
	// section takes precedence.
	response := map[string]any{"content": "SHOULD-NOT-APPEAR"}
	got := renderToolDetail(args, response, "no such file", styles)
	if !strings.Contains(got, "error:") {
		t.Errorf("expected 'error:' header, got:\n%q", got)
	}
	if !strings.Contains(got, "no such file") {
		t.Errorf("expected error message in body, got:\n%q", got)
	}
	if strings.Contains(got, "SHOULD-NOT-APPEAR") {
		t.Errorf("response body should be suppressed on error, got:\n%q", got)
	}
	// Args still surface — the operator wants to see WHAT was
	// asked when they debug WHY it failed.
	if !strings.Contains(got, "missing.txt") {
		t.Errorf("expected args body preserved alongside error, got:\n%q", got)
	}
}

func TestRenderToolDetail_ArgsOnly_CallPending(t *testing.T) {
	// Tool call in flight — args populated, response not yet
	// arrived. Detail block should render just the args section.
	styles := NewStyles(true, Branding{})
	args := map[string]any{"pattern": "*.go"}
	got := renderToolDetail(args, nil, "", styles)
	if !strings.Contains(got, "args:") {
		t.Errorf("expected 'args:' header, got:\n%q", got)
	}
	if strings.Contains(got, "response:") {
		t.Errorf("no response yet — 'response:' header should not appear, got:\n%q", got)
	}
	if strings.Contains(got, "error:") {
		t.Errorf("no error — 'error:' header should not appear, got:\n%q", got)
	}
}

func TestRenderToolDetail_Empty(t *testing.T) {
	styles := NewStyles(true, Branding{})
	got := renderToolDetail(nil, nil, "", styles)
	if got != "" {
		t.Errorf("expected empty string when there's nothing to render, got:\n%q", got)
	}
}

func TestRenderToolDetail_LongValueGetsPerLineCap(t *testing.T) {
	styles := NewStyles(true, Branding{})
	// One giant scalar — should truncate at detailValueByteCap with
	// an ellipsis, not blow the line width.
	huge := strings.Repeat("x", detailValueByteCap*2)
	response := map[string]any{"blob": huge}
	got := renderToolDetail(nil, response, "", styles)
	if strings.Contains(got, strings.Repeat("x", detailValueByteCap*2)) {
		t.Errorf("expected value truncation at detailValueByteCap, got long line intact")
	}
	if !strings.Contains(got, "…") {
		t.Errorf("expected truncation ellipsis in output, got:\n%q", got[:min(len(got), 400)])
	}
}

func TestRenderToolDetail_ManyLinesGetsPerSectionCap(t *testing.T) {
	styles := NewStyles(true, Branding{})
	// Many keys — force > detailMaxLines JSON lines in the pretty
	// output.
	response := make(map[string]any, detailMaxLines*2)
	for i := 0; i < detailMaxLines*2; i++ {
		response[keyForIndex(i)] = i
	}
	got := renderToolDetail(nil, response, "", styles)
	if !strings.Contains(got, "+") || !strings.Contains(got, "more line") {
		t.Errorf("expected '+N more line(s)' footer when body overflows detailMaxLines, got tail:\n%q",
			tail(got, 200))
	}
}

// keyForIndex synthesizes a stable string key so map ordering
// doesn't matter — we only care about the total line count.
func keyForIndex(i int) string { return fmt.Sprintf("k_%04d", i) }

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
