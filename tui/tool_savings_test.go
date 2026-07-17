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

// TestResolveToolSavings covers the resolver's precedence + type
// tolerance. Typed field wins over map fallback; map is walked with
// float64 (SSE/JSON path) and int (in-process path); nil / empty /
// malformed shapes suppress the badge.
func TestResolveToolSavings(t *testing.T) {
	explicit := &ToolSavings{Path: SavingsPathStructural, OriginalTokensEst: 100, DigestTokensEst: 20}
	cases := []struct {
		name  string
		in    ToolResult
		want  *ToolSavings // compared for nil-ness + Path + token counts
		nilOK bool
	}{
		{
			name: "typed field wins when set",
			in:   ToolResult{Savings: explicit, Response: map[string]any{"savings": map[string]any{"path": "ignored"}}},
			want: explicit,
		},
		{
			name: "falls back to response map (JSON path — float64)",
			in: ToolResult{Response: map[string]any{"savings": map[string]any{
				"path":                "structural_json",
				"original_bytes":      float64(12345),
				"digest_bytes":        float64(2100),
				"original_tokens_est": float64(3086),
				"digest_tokens_est":   float64(525),
			}}},
			want: &ToolSavings{
				Path:              SavingsPathStructural,
				OriginalBytes:     12345,
				DigestBytes:       2100,
				OriginalTokensEst: 3086,
				DigestTokensEst:   525,
			},
		},
		{
			name: "falls back to response map (in-process — int)",
			in: ToolResult{Response: map[string]any{"savings": map[string]any{
				"path":                "structural_json",
				"original_tokens_est": 3086,
				"digest_tokens_est":   525,
			}}},
			want: &ToolSavings{
				Path:              SavingsPathStructural,
				OriginalTokensEst: 3086,
				DigestTokensEst:   525,
			},
		},
		{
			name: "agentic path plucks subagent fields",
			in: ToolResult{Response: map[string]any{"savings": map[string]any{
				"path":                   "llm_fallback",
				"original_tokens_est":    float64(8000),
				"digest_tokens_est":      float64(500),
				"subagent_model":         "gemini-2.5-flash",
				"subagent_input_tokens":  float64(400),
				"subagent_output_tokens": float64(150),
			}}},
			want: &ToolSavings{
				Path:                 SavingsPathAgentic,
				OriginalTokensEst:    8000,
				DigestTokensEst:      500,
				SubagentModel:        "gemini-2.5-flash",
				SubagentInputTokens:  400,
				SubagentOutputTokens: 150,
			},
		},
		{
			name:  "nil when response is nil",
			in:    ToolResult{},
			nilOK: true,
		},
		{
			name:  "nil when sidecar key absent",
			in:    ToolResult{Response: map[string]any{"content": "x"}},
			nilOK: true,
		},
		{
			name:  "nil when sidecar value is wrong type",
			in:    ToolResult{Response: map[string]any{"savings": "not a map"}},
			nilOK: true,
		},
		{
			name:  "nil when sidecar map has no recognized keys",
			in:    ToolResult{Response: map[string]any{"savings": map[string]any{"unknown": 1}}},
			nilOK: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveToolSavings(tc.in)
			if tc.nilOK {
				if got != nil {
					t.Errorf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected non-nil, got nil")
			}
			if got.Path != tc.want.Path {
				t.Errorf("Path = %q, want %q", got.Path, tc.want.Path)
			}
			if got.OriginalBytes != tc.want.OriginalBytes {
				t.Errorf("OriginalBytes = %d, want %d", got.OriginalBytes, tc.want.OriginalBytes)
			}
			if got.DigestBytes != tc.want.DigestBytes {
				t.Errorf("DigestBytes = %d, want %d", got.DigestBytes, tc.want.DigestBytes)
			}
			if got.OriginalTokensEst != tc.want.OriginalTokensEst {
				t.Errorf("OriginalTokensEst = %d, want %d", got.OriginalTokensEst, tc.want.OriginalTokensEst)
			}
			if got.DigestTokensEst != tc.want.DigestTokensEst {
				t.Errorf("DigestTokensEst = %d, want %d", got.DigestTokensEst, tc.want.DigestTokensEst)
			}
			if got.SubagentModel != tc.want.SubagentModel {
				t.Errorf("SubagentModel = %q, want %q", got.SubagentModel, tc.want.SubagentModel)
			}
			if got.SubagentInputTokens != tc.want.SubagentInputTokens {
				t.Errorf("SubagentInputTokens = %d, want %d", got.SubagentInputTokens, tc.want.SubagentInputTokens)
			}
			if got.SubagentOutputTokens != tc.want.SubagentOutputTokens {
				t.Errorf("SubagentOutputTokens = %d, want %d", got.SubagentOutputTokens, tc.want.SubagentOutputTokens)
			}
		})
	}
}

func TestFormatTokenCount(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{-5, "0"},
		{1, "1"},
		{999, "999"},
		{1000, "1.0k"},
		{1234, "1.2k"},
		{9999, "10.0k"},
		{10_000, "10k"},
		{12_400, "12k"},
		{999_999, "999k"},
		{1_000_000, "1.0M"},
	}
	for _, tc := range cases {
		if got := formatTokenCount(tc.in); got != tc.want {
			t.Errorf("formatTokenCount(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormatSavingsCompact(t *testing.T) {
	cases := []struct {
		name string
		in   *ToolSavings
		want string
	}{
		{"nil", nil, ""},
		{"passthrough suppresses",
			&ToolSavings{Path: SavingsPathPassthrough, OriginalTokensEst: 1, DigestTokensEst: 1},
			""},
		{"structural",
			&ToolSavings{Path: SavingsPathStructural, OriginalTokensEst: 12_400, DigestTokensEst: 2_100},
			"12k→2.1k tok · struct"},
		{"agentic",
			&ToolSavings{Path: SavingsPathAgentic, OriginalTokensEst: 8_000, DigestTokensEst: 500},
			"8.0k→500 tok · agentic"},
		{"missing token counts suppress",
			&ToolSavings{Path: SavingsPathStructural, OriginalBytes: 1000},
			""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatSavingsCompact(tc.in); got != tc.want {
				t.Errorf("formatSavingsCompact = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRenderSavingsBadge(t *testing.T) {
	styles := NewStyles(true, Branding{})
	// Non-passthrough with token counts renders.
	got := renderSavingsBadge(&ToolSavings{
		Path:              SavingsPathStructural,
		OriginalTokensEst: 12_400,
		DigestTokensEst:   2_100,
	}, styles)
	if !strings.Contains(got, "12k→2.1k tok · struct") {
		t.Errorf("expected compact chip in badge, got %q", got)
	}
	if !strings.HasPrefix(got, "  ") {
		t.Errorf("expected two leading spaces (gap from latency badge), got %q", got)
	}
	// Nil suppresses entirely.
	if renderSavingsBadge(nil, styles) != "" {
		t.Errorf("expected empty badge for nil savings")
	}
	// Passthrough suppresses.
	if renderSavingsBadge(&ToolSavings{Path: SavingsPathPassthrough}, styles) != "" {
		t.Errorf("expected empty badge for passthrough path")
	}
}

func TestSavedTokens_ClampsNegative(t *testing.T) {
	s := &ToolSavings{OriginalTokensEst: 10, DigestTokensEst: 15}
	if got := s.SavedTokens(); got != 0 {
		t.Errorf("SavedTokens on inflating digest should clamp to 0, got %d", got)
	}
	if got := (*ToolSavings)(nil).SavedTokens(); got != 0 {
		t.Errorf("SavedTokens on nil receiver should return 0, got %d", got)
	}
}

// TestApplyToolResult_SavingsBadgeAppendedToPreview mirrors the
// latency badge test — drive the History helpers directly to
// verify the badge lands in the preview alongside any latency badge.
func TestApplyToolResult_SavingsBadgeAppendedToPreview(t *testing.T) {
	styles := NewStyles(true, Branding{})
	h := &History{}
	args := map[string]any{"cluster": "prod-a"}
	h.Append(Message{
		Role:        RoleTool,
		ToolName:    "gke_get_k8s_resource",
		ToolCallID:  "call-99",
		ToolArgsMap: args,
	})
	idx := h.FindByToolCallID("call-99")
	if idx < 0 {
		t.Fatal("expected to find seeded tool row")
	}
	snap := h.Snapshot()

	// Response arrives with both latency_ms and a savings sidecar (the
	// shipping shape core-agent PR #290 emits).
	response := map[string]any{
		"digest":     "compressed",
		"latency_ms": float64(720),
		"savings": map[string]any{
			"path":                "structural_json",
			"original_tokens_est": float64(12_400),
			"digest_tokens_est":   float64(2_100),
		},
	}
	tr := ToolResult{Response: response}
	latencyMs := resolveToolLatencyMs(tr)
	savings := resolveToolSavings(tr)

	preview := renderToolPreviewWithResult(tr.Name, snap[idx].ToolArgsMap, response, "", styles)
	if badge := renderLatencyBadge(latencyMs, styles); badge != "" {
		preview += badge
	}
	if badge := renderSavingsBadge(savings, styles); badge != "" {
		preview += badge
	}
	h.SetToolPreview(idx, preview)
	h.SetToolResult(idx, response, "", latencyMs, savings)

	final := h.Snapshot()[idx]
	if !strings.Contains(final.ToolPreview, "720ms") {
		t.Errorf("expected latency badge appended to preview, got:\n%s", final.ToolPreview)
	}
	if !strings.Contains(final.ToolPreview, "12k→2.1k tok · struct") {
		t.Errorf("expected savings badge appended to preview, got:\n%s", final.ToolPreview)
	}
	if final.ToolSavings == nil || final.ToolSavings.Path != SavingsPathStructural {
		t.Errorf("expected ToolSavings stashed for dialog chip, got %+v", final.ToolSavings)
	}
}

// TestToolCallDialog_HeaderShowsSavingsChip pins the tier-2 surface:
// the tool-call detail overlay's header banner shows the compact
// savings chip alongside the latency chip. Regression signal if a
// future refactor drops the chip from the header rendering path.
func TestToolCallDialog_HeaderShowsSavingsChip(t *testing.T) {
	styles := NewStyles(true, Branding{})
	m := Model{}
	m.styles = styles
	m.width = 120
	m.height = 40
	m.history.Append(Message{
		Role:            RoleTool,
		ToolName:        "gke_get_k8s_resource",
		ToolCallID:      "call-1",
		ToolArgsMap:     map[string]any{"cluster": "prod"},
		ToolResponseMap: map[string]any{"digest": "compressed"},
		ToolLatencyMs:   720,
		ToolSavings: &ToolSavings{
			Path:              SavingsPathStructural,
			OriginalTokensEst: 12_400,
			DigestTokensEst:   2_100,
		},
	})
	d := newToolCallDialog(1)
	out := d.Render(m.width, &m)
	if !strings.Contains(out, "12k→2.1k tok · struct") {
		t.Errorf("expected savings chip in dialog header, got:\n%s", out)
	}
	// The existing latency chip must not have regressed by our addition.
	if !strings.Contains(out, "720ms") {
		t.Errorf("expected latency chip retained, got:\n%s", out)
	}
}
