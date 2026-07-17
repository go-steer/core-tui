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

// Per-tool digest-savings plumbing + rendering (SSE spec v1.3.0 /
// core-agent PR #290). core-agent's MCP wrap layer emits a `savings`
// sidecar inside the tool-result response map — same channel as
// `latency_ms` (see tool_latency.go for why). This file bridges the
// wire shape (map[string]any at Response["savings"]) to a typed
// value renderers can consume without map-key-typing.

package tui

import (
	"fmt"
)

// toolSavingsResponseKey is the well-known sidecar key core-agent
// stamps on every wrapped MCP tool response (`digestingTool.Run` in
// pkg/mcp/digest_wrap.go stamps it alongside `latency_ms`). Both
// remote and embedded adapters copy the map through to
// tui.ToolResult.Response verbatim, so consumers on either transport
// see the value at this key.
const toolSavingsResponseKey = "savings"

// Path values the digest router populates on ToolSavings.Path.
// Passthrough means the wrap layer decided the payload was small
// enough to skip; structural means the JSON pruner reduced it; agentic
// means the LLM subagent digested it after structural couldn't.
const (
	SavingsPathPassthrough = "passthrough"
	SavingsPathStructural  = "structural_json"
	SavingsPathAgentic     = "llm_fallback"
)

// ToolSavings surfaces the digest wrap's per-call reduction. Fields
// mirror the wire shape core-agent's pkg/digest.Savings emits — see
// docs/sse-event-stream-protocol.md §2.7 for the authoritative shape.
//
// The four *Bytes / *Tokens* fields are always populated on wrapped
// calls. Token counts use a 4-char-per-token heuristic (accurate to
// ±15%), suitable for savings display but NOT billing.
//
// Subagent* fields are populated only on the agentic path (Path ==
// SavingsPathAgentic) — the small-tier LLM digester's own usage.
// Zero on structural / passthrough.
type ToolSavings struct {
	Path                 string
	OriginalBytes        int
	DigestBytes          int
	OriginalTokensEst    int
	DigestTokensEst      int
	SubagentModel        string
	SubagentInputTokens  int
	SubagentOutputTokens int
}

// SavedTokens returns the parent-side token reduction (before any
// subagent offset). Clamps to zero to avoid negative "savings" on the
// passthrough path where a truncation marker can nominally inflate
// the digest above the original.
func (s *ToolSavings) SavedTokens() int {
	if s == nil {
		return 0
	}
	saved := s.OriginalTokensEst - s.DigestTokensEst
	if saved < 0 {
		return 0
	}
	return saved
}

// resolveToolSavings returns the per-call savings for this ToolResult,
// preferring the typed field when adapters populate it explicitly,
// falling back to Response["savings"] when the sidecar shape from
// core-agent's PR #290 landed. Returns nil when neither source has
// data — nil suppresses the badge, chip, and dialog block end-to-end.
//
// Handles the JSON-unmarshal reality that numeric values arrive as
// float64 via SSE; in-process adapters hand over int / int64. Both
// accepted; unrecognized shapes are silently dropped so a malformed
// sidecar can't crash the renderer.
func resolveToolSavings(tr ToolResult) *ToolSavings {
	if tr.Savings != nil {
		return tr.Savings
	}
	if tr.Response == nil {
		return nil
	}
	raw, ok := tr.Response[toolSavingsResponseKey].(map[string]any)
	if !ok {
		return nil
	}
	s := &ToolSavings{
		Path:                 stringField(raw, "path"),
		OriginalBytes:        intField(raw, "original_bytes"),
		DigestBytes:          intField(raw, "digest_bytes"),
		OriginalTokensEst:    intField(raw, "original_tokens_est"),
		DigestTokensEst:      intField(raw, "digest_tokens_est"),
		SubagentModel:        stringField(raw, "subagent_model"),
		SubagentInputTokens:  intField(raw, "subagent_input_tokens"),
		SubagentOutputTokens: intField(raw, "subagent_output_tokens"),
	}
	// Discard entirely-empty sidecars — a malformed map without any
	// recognized keys shouldn't render as a zero-savings badge.
	if s.Path == "" && s.OriginalBytes == 0 && s.DigestBytes == 0 {
		return nil
	}
	return s
}

func stringField(m map[string]any, key string) string {
	v, ok := m[key].(string)
	if !ok {
		return ""
	}
	return v
}

func intField(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}

// formatTokenCount renders a token count in the compact `12k` /
// `1.2k` / `456` shape used in tool-row previews. Under 1000 stays
// literal; 1000-9999 shows one decimal (`1.2k`); ≥10000 rounds to
// whole units (`12k`). Consistent with formatLatency's "keep the
// number under three digits wide" philosophy.
func formatTokenCount(n int) string {
	switch {
	case n < 0:
		return "0"
	case n < 1_000:
		return fmt.Sprintf("%d", n)
	case n < 10_000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	case n < 1_000_000:
		return fmt.Sprintf("%dk", n/1000)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
}

// shortPath returns the tag core-agent's docs use in operator-facing
// telemetry — one word so the inline badge stays compact.
func shortPath(path string) string {
	switch path {
	case SavingsPathStructural:
		return "struct"
	case SavingsPathAgentic:
		return "agentic"
	case SavingsPathPassthrough:
		return "passthrough"
	default:
		return path
	}
}

// formatSavingsCompact renders a per-tool digest-savings summary for
// the inline preview: `12k→2k tok · struct`. Passthrough paths return
// "" because there's nothing meaningful to render (no reduction —
// the wrap decided the payload was small enough to skip). Nil
// receiver also returns "" so callers can chain without nil-guarding.
func formatSavingsCompact(s *ToolSavings) string {
	if s == nil || s.Path == SavingsPathPassthrough {
		return ""
	}
	if s.OriginalTokensEst == 0 || s.DigestTokensEst == 0 {
		return ""
	}
	return fmt.Sprintf("%s→%s tok · %s",
		formatTokenCount(s.OriginalTokensEst),
		formatTokenCount(s.DigestTokensEst),
		shortPath(s.Path),
	)
}

// renderSavingsBadge returns a muted inline chip like
// `  [12k→2k tok · struct]` appended to a tool-row line. Empty on
// nil / passthrough / missing token counts — same suppression contract
// as renderLatencyBadge.
//
// Two leading spaces so it doesn't crowd the latency badge (both
// badges may fire on the same row; the latency badge lands first
// so the row reads `read_file  [2.4s]  [12k→2k tok · struct]`).
func renderSavingsBadge(s *ToolSavings, styles Styles) string {
	txt := formatSavingsCompact(s)
	if txt == "" {
		return ""
	}
	return "  " + styles.Muted.Render("["+txt+"]")
}
