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

// End-to-end coverage for the Options.ToolDetailVerbose knob
// (core-tui #52 tier 2): flipping it on should append the raw
// args + response detail block under every tool row's compact
// preview via applyToolResult's Model logic. Off (default) leaves
// the compact preview intact. Follows the existing convention in
// TestApplyToolResult_UpdatesPreview: drive History helpers
// directly and mirror the applyToolResult composition — cheaper
// than spinning up a full Model with a viewport / listcache.

package tui

import (
	"strings"
	"testing"
)

// applyToolResultLogic replays the composition inside
// (*Model).applyToolResult so the tests can exercise the verbose
// gating without booting a Model + viewport. Keep the shape in sync
// with update.go's applyToolResult body — that's the only place
// verbose is actually threaded.
func applyToolResultLogic(h *History, verbose bool, name string, args, response map[string]any, errStr string, styles Styles) {
	idx := h.FindByToolCallID("call")
	if idx < 0 {
		return
	}
	snap := h.Snapshot()
	preview := renderToolPreviewWithResult(name, snap[idx].ToolArgsMap, response, errStr, styles)
	if verbose {
		if detail := renderToolDetail(snap[idx].ToolArgsMap, response, errStr, styles); detail != "" {
			preview = preview + "\n" + detail
		}
	}
	h.SetToolPreview(idx, preview)
	h.SetToolResult(idx, response, errStr)
}

func TestToolDetailVerbose_AppendsDetailBlock(t *testing.T) {
	styles := NewStyles(true, Branding{})
	h := &History{}
	args := map[string]any{"path": "main.go"}
	h.Append(Message{
		Role:        RoleTool,
		ToolName:    "read_file",
		ToolCallID:  "call",
		ToolArgsMap: args,
	})
	response := map[string]any{"content": "package main\nfunc main() {}\n"}

	applyToolResultLogic(h, true, "read_file", args, response, "", styles)

	snap := h.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(snap))
	}
	preview := snap[0].ToolPreview
	if !strings.Contains(preview, "main") {
		t.Errorf("compact preview should still contain content, got:\n%s", preview)
	}
	if !strings.Contains(preview, "args:") || !strings.Contains(preview, "response:") {
		t.Errorf("verbose mode should append 'args:' + 'response:' sections, got:\n%s", preview)
	}
	if snap[0].ToolResponseMap == nil {
		t.Errorf("applyToolResult should stash raw response for the overlay")
	}
}

func TestToolDetailVerbose_CompactDefaultOmitsDetailBlock(t *testing.T) {
	styles := NewStyles(true, Branding{})
	h := &History{}
	args := map[string]any{"path": "main.go"}
	h.Append(Message{
		Role:        RoleTool,
		ToolName:    "read_file",
		ToolCallID:  "call",
		ToolArgsMap: args,
	})
	response := map[string]any{"content": "hi\n"}

	applyToolResultLogic(h, false, "read_file", args, response, "", styles)

	snap := h.Snapshot()
	preview := snap[0].ToolPreview
	if strings.Contains(preview, "args:\n") || strings.Contains(preview, "response:\n") {
		t.Errorf("compact mode should not append detail sections, got:\n%s", preview)
	}
	if snap[0].ToolResponseMap == nil {
		t.Errorf("compact mode should still stash raw response for the overlay")
	}
}

func TestToolDetailVerbose_ErrorRendersErrorSection(t *testing.T) {
	styles := NewStyles(true, Branding{})
	h := &History{}
	args := map[string]any{"path": "missing.txt"}
	h.Append(Message{
		Role:        RoleTool,
		ToolName:    "read_file",
		ToolCallID:  "call",
		ToolArgsMap: args,
	})

	applyToolResultLogic(h, true, "read_file", args, nil, "no such file", styles)

	snap := h.Snapshot()
	preview := snap[0].ToolPreview
	if !strings.Contains(preview, "error:") || !strings.Contains(preview, "no such file") {
		t.Errorf("verbose+error should include 'error:' section with message, got:\n%s", preview)
	}
	if snap[0].ToolError != "no such file" {
		t.Errorf("applyToolResult should stash error for overlay, got %q", snap[0].ToolError)
	}
}
