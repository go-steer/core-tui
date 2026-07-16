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

func TestResolveToolLatencyMs(t *testing.T) {
	cases := []struct {
		name string
		in   ToolResult
		want int64
	}{
		{
			name: "typed field wins when set",
			in: ToolResult{
				LatencyMs: 1234,
				Response:  map[string]any{"latency_ms": float64(9999)},
			},
			want: 1234,
		},
		{
			name: "falls back to response map float64 (JSON path)",
			in:   ToolResult{Response: map[string]any{"latency_ms": float64(2400)}},
			want: 2400,
		},
		{
			name: "falls back to response map int (in-process path)",
			in:   ToolResult{Response: map[string]any{"latency_ms": 2400}},
			want: 2400,
		},
		{
			name: "falls back to response map int64",
			in:   ToolResult{Response: map[string]any{"latency_ms": int64(2400)}},
			want: 2400,
		},
		{
			name: "zero when both absent",
			in:   ToolResult{Response: map[string]any{}},
			want: 0,
		},
		{
			name: "zero when response is nil",
			in:   ToolResult{},
			want: 0,
		},
		{
			name: "zero when response value is negative (garbage)",
			in:   ToolResult{Response: map[string]any{"latency_ms": float64(-1)}},
			want: 0,
		},
		{
			name: "zero when response value is wrong type",
			in:   ToolResult{Response: map[string]any{"latency_ms": "2.4s"}},
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveToolLatencyMs(tc.in); got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestFormatLatency(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, ""},
		{-5, ""},
		{1, "1ms"},
		{450, "450ms"},
		{999, "999ms"},
		{1000, "1.0s"},
		{2400, "2.4s"},
		{9999, "10.0s"},
		{10000, "10s"},
		{45000, "45s"},
		{59500, "60s"},
		{60000, "1m 0s"},
		{75000, "1m 15s"},
		{125000, "2m 5s"},
	}
	for _, tc := range cases {
		if got := formatLatency(tc.in); got != tc.want {
			t.Errorf("formatLatency(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRenderLatencyBadge(t *testing.T) {
	styles := NewStyles(true, Branding{})
	// Non-zero renders as a muted "  [2.4s]" chip.
	got := renderLatencyBadge(2400, styles)
	if !strings.Contains(got, "[2.4s]") {
		t.Errorf("expected '[2.4s]' in badge, got %q", got)
	}
	if !strings.HasPrefix(got, "  ") {
		t.Errorf("expected two leading spaces to gap from tool name, got %q", got)
	}
	// Zero suppresses entirely.
	if renderLatencyBadge(0, styles) != "" {
		t.Errorf("expected empty badge for zero latency")
	}
}

func TestApplyToolResult_LatencyBadgeAppendedToPreview(t *testing.T) {
	// Mirrors the applyToolResult composition (see tool_verbose_test.go
	// for the same convention): drive History helpers directly so the
	// test doesn't need a full Model + viewport.
	styles := NewStyles(true, Branding{})
	h := &History{}
	args := map[string]any{"path": "main.go"}
	h.Append(Message{
		Role:        RoleTool,
		ToolName:    "read_file",
		ToolCallID:  "call-42",
		ToolArgsMap: args,
	})

	// Replay the applyToolResult logic with latency > 0.
	idx := h.FindByToolCallID("call-42")
	if idx < 0 {
		t.Fatal("expected to find seeded tool row")
	}
	snap := h.Snapshot()
	response := map[string]any{"content": "package main\n", "latency_ms": float64(2400)}
	tr := ToolResult{Response: response}
	latencyMs := resolveToolLatencyMs(tr)

	preview := renderToolPreviewWithResult("read_file", snap[idx].ToolArgsMap, response, "", styles)
	if badge := renderLatencyBadge(latencyMs, styles); badge != "" {
		preview += badge
	}
	h.SetToolPreview(idx, preview)
	h.SetToolResult(idx, response, "", latencyMs)

	final := h.Snapshot()[idx]
	if !strings.Contains(final.ToolPreview, "[2.4s]") {
		t.Errorf("expected latency badge appended to preview, got:\n%s", final.ToolPreview)
	}
	if final.ToolLatencyMs != 2400 {
		t.Errorf("expected ToolLatencyMs=2400 stashed for dialog chip, got %d", final.ToolLatencyMs)
	}
}

func TestToolCallDialog_HeaderShowsLatencyChip(t *testing.T) {
	styles := NewStyles(true, Branding{})
	m := Model{}
	m.styles = styles
	m.width = 120
	m.height = 40
	m.history.Append(Message{
		Role:            RoleTool,
		ToolName:        "read_file",
		ToolCallID:      "call-1",
		ToolArgsMap:     map[string]any{"path": "a.go"},
		ToolResponseMap: map[string]any{"content": "hi"},
		ToolLatencyMs:   2400,
	})
	d := newToolCallDialog(1)
	out := d.Render(m.width, &m)
	if !strings.Contains(out, "2.4s") {
		t.Errorf("expected '2.4s' chip in dialog header, got:\n%s", out)
	}
}
