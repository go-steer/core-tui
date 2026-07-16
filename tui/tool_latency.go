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

// Per-tool-call latency plumbing + rendering (core-tui #60 / SSE
// spec v1.2.0). core-agent PR #278 emits `latency_ms` inside the
// tool-result response map because ADK's `tool.Run` has no write
// access to the enclosing `session.Event.CustomMetadata` — the
// response payload is the only sidecar channel available from
// inside the tool call. This file bridges that wire shape to a
// typed field consumers can render without map-key-typing.

package tui

import (
	"fmt"
	"time"
)

// toolLatencyResponseKey is the well-known sidecar key core-agent
// stamps on every tool-result response map (helper `withLatency` in
// pkg/mcp/digest_wrap.go). Both remote and embedded adapters copy
// the map through to tui.ToolResult.Response verbatim, so consumers
// on either transport see the value at this key.
const toolLatencyResponseKey = "latency_ms"

// resolveToolLatencyMs returns the per-call wall-clock latency for
// this ToolResult, preferring the typed field when adapters populate
// it explicitly, falling back to the sidecar key in the response
// map (the shape core-agent PR #278 ships today). Returns 0 when
// neither source has a positive value — 0 suppresses the badge and
// dialog chip end-to-end.
//
// Handles the JSON-unmarshal reality that numbers arrive as float64
// via SSE — hosts that decode from JSON get float64; hosts that
// construct in-process get int / int64. Both accepted.
func resolveToolLatencyMs(tr ToolResult) int64 {
	if tr.LatencyMs > 0 {
		return tr.LatencyMs
	}
	if tr.Response == nil {
		return 0
	}
	switch v := tr.Response[toolLatencyResponseKey].(type) {
	case int64:
		if v > 0 {
			return v
		}
	case int:
		if v > 0 {
			return int64(v)
		}
	case float64:
		if v > 0 {
			return int64(v)
		}
	}
	return 0
}

// formatLatency renders a wall-clock duration for tool-row inline
// display. Scales the unit so the number stays under three digits
// wide: `450ms` under a second, `2.4s` under 100s, `1m 45s` above.
// Returns "" on non-positive input so callers can chain without
// nil-guarding.
func formatLatency(ms int64) string {
	if ms <= 0 {
		return ""
	}
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d < time.Second:
		// Sub-second: milliseconds are load-bearing. `450ms` is
		// more legible than `0.4s` at this scale.
		return fmt.Sprintf("%dms", ms)
	case d < 10*time.Second:
		// Under 10s: one decimal so 2.4s vs 2.9s isn't rounded away.
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()+0.5))
	default:
		mins := int(d / time.Minute)
		secs := int((d % time.Minute) / time.Second)
		return fmt.Sprintf("%dm %ds", mins, secs)
	}
}

// renderLatencyBadge returns a muted inline chip like `  [2.4s]` to
// append to a tool-result line. Two leading spaces so it doesn't
// crowd the tool name; brackets so it reads as metadata, not
// content. Empty string when ms is 0.
func renderLatencyBadge(ms int64, styles Styles) string {
	txt := formatLatency(ms)
	if txt == "" {
		return ""
	}
	return "  " + styles.Muted.Render("["+txt+"]")
}
