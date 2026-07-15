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

// Tests for the observer-mode per-turn footer fix (issue #57):
// stamp the tail assistant Message with Model/Usage/CostUSD/Elapsed
// from turnSummaryMsg + usageUpdateMsg so renderTurnFooter has
// something to show. Chat-mode uses finalizeTurn; observer mode
// (LiveAgent) doesn't get a turnDoneMsg, so those handlers must
// back-annotate directly.

package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestObserverFooter_WireOrder — the realistic sequence a core-agent
// v2.7.0-dev.3+ session emits: chat-content chunks (partial), a
// non-partial commit, then turn-complete (turnSummaryMsg with
// tokens+model+latency, cost=0 per spec), then usage-update
// (usageUpdateMsg with LastTurn carrying authoritative cost). After
// all three land, the tail assistant Message must carry Model,
// Usage, CostUSD, and Elapsed, and renderTurnFooter must return a
// non-empty footer string.
func TestObserverFooter_WireOrder_StampsAllFields(t *testing.T) {
	m := NewModel(Options{Agent: newLiveAgentStub()})
	m.viewport.SetWidth(80)

	// Stream + commit the assistant text (this is the SSE
	// chat-content chunks landing).
	m.applyStreamChunk(streamChunkMsg{text: "answer ", partial: true})
	m.applyStreamChunk(streamChunkMsg{text: "text", partial: true})
	m.applyStreamChunk(streamChunkMsg{text: "answer text", partial: false})
	if m.history.Len() == 0 {
		t.Fatal("setup: expected committed assistant row after non-partial")
	}

	// turn-complete lands next (tokens+model+latency; cost=0
	// per spec v1.1.0 for servers that compute cost out-of-band).
	out, _ := m.Update(turnSummaryMsg{summary: TurnSummary{
		PromptID:  "prompt-1",
		Model:     "gemini-3.5-flash",
		TokensIn:  26556,
		TokensOut: 622,
		LatencyMs: 27676,
		// CostUSD deliberately zero — the whole point of #57 is
		// that observer mode used to show $0 forever without the
		// LastTurn back-annotation from usage-update.
	}})
	m = out.(Model)

	// Post-turnSummary the footer already renders tokens+model+
	// latency (cost still $0). Confirm.
	tail := m.history.Snapshot()[m.history.Len()-1]
	if tail.Model != "gemini-3.5-flash" {
		t.Errorf("after turnSummary: tail.Model = %q, want gemini-3.5-flash", tail.Model)
	}
	if tail.Usage == nil || tail.Usage.InputTokens != 26556 || tail.Usage.OutputTokens != 622 {
		t.Errorf("after turnSummary: tail.Usage = %+v, want {26556, 622}", tail.Usage)
	}
	if tail.Elapsed != 27676*time.Millisecond {
		t.Errorf("after turnSummary: tail.Elapsed = %v, want 27.676s", tail.Elapsed)
	}
	if tail.CostUSD != 0 {
		t.Errorf("after turnSummary alone: tail.CostUSD = %f, want 0 (cost arrives on usage-update)", tail.CostUSD)
	}

	// usage-update lands with LastTurn carrying authoritative cost.
	out, _ = m.Update(usageUpdateMsg{update: UsageUpdate{
		TokensInTotal:  74450,
		TokensOutTotal: 1349,
		CostUSDTotal:   0.12381600000000001,
		TurnsTotal:     3,
		LastTurn: &UsageLastTurn{
			TokensIn:  26556,
			TokensOut: 622,
			CostUSD:   0.045432,
			Model:     "gemini-3.5-flash",
		},
	}})
	m = out.(Model)

	tail = m.history.Snapshot()[m.history.Len()-1]
	if tail.CostUSD != 0.045432 {
		t.Errorf("after usage-update: tail.CostUSD = %f, want 0.045432", tail.CostUSD)
	}
	// Model + Usage should remain unchanged (already stamped).
	if tail.Model != "gemini-3.5-flash" {
		t.Errorf("Model lost after usage-update: got %q", tail.Model)
	}
	if tail.Usage == nil || tail.Usage.InputTokens != 26556 {
		t.Errorf("Usage lost after usage-update: %+v", tail.Usage)
	}

	// The whole point: renderTurnFooter now returns non-empty.
	footer := m.renderTurnFooter(tail)
	if footer == "" {
		t.Fatal("renderTurnFooter returned empty — footer would not render in the observer-mode view")
	}
	if !strings.Contains(footer, "gemini-3.5-flash") {
		t.Errorf("footer missing model: %q", footer)
	}
	if !strings.Contains(footer, "$0.0454") {
		t.Errorf("footer missing cost: %q", footer)
	}
	if !strings.Contains(footer, "27") {
		// 27676ms rounds to 27.7s; loose match on "27" avoids
		// coupling to the exact humanization.
		t.Errorf("footer missing latency: %q", footer)
	}
}

// TestObserverFooter_ReverseOrder — defensive: if turn-complete
// arrives BEFORE the final non-partial chat-content commit (out-of-
// order delivery / adapter quirk), the stamp path on applyStreamChunk
// picks up the current* fields at commit time. Both orderings
// converge on a stamped footer.
func TestObserverFooter_TurnCompleteBeforeCommit(t *testing.T) {
	m := NewModel(Options{Agent: newLiveAgentStub()})
	m.viewport.SetWidth(80)

	// turn-complete lands first (populates current*).
	out, _ := m.Update(turnSummaryMsg{summary: TurnSummary{
		Model:     "gemini-3.5-flash",
		TokensIn:  100,
		TokensOut: 20,
		LatencyMs: 500,
	}})
	m = out.(Model)
	if m.currentModel != "gemini-3.5-flash" {
		t.Fatal("setup: turnSummary should have populated m.currentModel")
	}

	// Then the assistant text streams + commits.
	m.applyStreamChunk(streamChunkMsg{text: "hi", partial: true})
	m.applyStreamChunk(streamChunkMsg{text: "hi", partial: false})

	if m.history.Len() == 0 {
		t.Fatal("expected committed assistant row")
	}
	tail := m.history.Snapshot()[m.history.Len()-1]
	// applyStreamChunk's commit branch should have stamped current*
	// onto the fresh Message.
	if tail.Model != "gemini-3.5-flash" {
		t.Errorf("commit-time stamp missing Model: %q", tail.Model)
	}
	if tail.Usage == nil || tail.Usage.InputTokens != 100 {
		t.Errorf("commit-time stamp missing Usage: %+v", tail.Usage)
	}
}

// TestObserverFooter_UsageUpdateWithoutLastTurn — pre-#249 servers
// omit LastTurn. Behavior must degrade to "cost stays whatever
// turnSummary provided" (typically $0) without crashing or clobbering
// the stamp turnSummary set.
func TestObserverFooter_UsageUpdateWithoutLastTurn(t *testing.T) {
	m := NewModel(Options{Agent: newLiveAgentStub()})
	m.viewport.SetWidth(80)

	m.applyStreamChunk(streamChunkMsg{text: "answer", partial: false})
	out, _ := m.Update(turnSummaryMsg{summary: TurnSummary{
		Model:     "gpt-x",
		TokensIn:  10,
		TokensOut: 5,
		LatencyMs: 200,
	}})
	m = out.(Model)

	// usage-update WITHOUT LastTurn (pre-#249 server).
	out, _ = m.Update(usageUpdateMsg{update: UsageUpdate{
		TokensInTotal: 10,
		TurnsTotal:    1,
	}})
	m = out.(Model)

	tail := m.history.Snapshot()[m.history.Len()-1]
	if tail.Model != "gpt-x" {
		t.Errorf("Model should survive nil-LastTurn: got %q", tail.Model)
	}
	if tail.Usage == nil {
		t.Errorf("Usage should survive nil-LastTurn: got nil")
	}
	// Cost stays 0 — no source populated it. Footer still renders
	// (Model+Usage+Elapsed are enough for a useful footer).
	if tail.CostUSD != 0 {
		t.Errorf("expected CostUSD 0 when LastTurn omitted, got %f", tail.CostUSD)
	}
	if footer := m.renderTurnFooter(tail); footer == "" {
		t.Error("footer should render on Model+Usage even without cost")
	}
}

// TestObserverFooter_DoesNotClobberExistingStamp — safety guard.
// StampLatestAssistantFooter only fills currently-zero fields; a
// Message that already has Model/Usage/CostUSD (e.g. from a future
// mode where per-event usage was pre-stamped) shouldn't get
// overwritten with different values.
func TestObserverFooter_DoesNotClobberExistingStamp(t *testing.T) {
	var h History
	h.Append(Message{
		Role:    RoleAssistant,
		Text:    "hi",
		Model:   "original-model",
		Usage:   &Usage{InputTokens: 999, OutputTokens: 111},
		CostUSD: 0.99,
		Elapsed: 5 * time.Second,
	})

	changed := h.StampLatestAssistantFooter(
		"other-model",
		&Usage{InputTokens: 1, OutputTokens: 1},
		0.01,
		1*time.Second,
	)
	if changed {
		t.Error("stamp on fully-populated row should be a no-op")
	}
	tail := h.Snapshot()[h.Len()-1]
	if tail.Model != "original-model" {
		t.Errorf("Model clobbered: got %q, want original-model", tail.Model)
	}
	if tail.Usage.InputTokens != 999 {
		t.Errorf("Usage clobbered: got %+v", tail.Usage)
	}
	if tail.CostUSD != 0.99 {
		t.Errorf("CostUSD clobbered: got %f", tail.CostUSD)
	}
	if tail.Elapsed != 5*time.Second {
		t.Errorf("Elapsed clobbered: got %v", tail.Elapsed)
	}
}

// TestObserverFooter_NoAssistantRow_NoOp — StampLatestAssistantFooter
// on an empty history or a tail that isn't RoleAssistant (e.g. a
// tool row landed after the last assistant text) must not stamp a
// wrong row. Returns false; caller ignores.
func TestObserverFooter_NoAssistantRow_NoOp(t *testing.T) {
	var h History
	if h.StampLatestAssistantFooter("m", &Usage{InputTokens: 1}, 0.1, time.Second) {
		t.Error("empty history should not stamp anything")
	}

	// Tool row at the tail (e.g. autonomous tool call after assistant).
	h.Append(Message{Role: RoleAssistant, Text: "hi"})
	h.Append(Message{Role: RoleTool, ToolName: "bash"})
	if h.StampLatestAssistantFooter("m", &Usage{InputTokens: 1}, 0.1, time.Second) {
		t.Error("tail is a tool row, not assistant — must not stamp")
	}
	// Assistant row at index 0 must stay clean (we should NOT walk
	// past a non-matching tail).
	if h.Snapshot()[0].Model != "" {
		t.Errorf("stamped past non-assistant tail: row 0.Model = %q", h.Snapshot()[0].Model)
	}
}

// TestUsageUpdate_LastTurn_JSON — round-trip the wire evidence from
// issue #57 through JSON to confirm the new field shape parses.
func TestUsageUpdate_LastTurn_JSON(t *testing.T) {
	// Payload adapted from the wire capture in the issue.
	raw := `{"tokens_in_total":74450,"tokens_out_total":1349,"cost_usd_total":0.123816,"turns_total":3,"last_turn":{"tokens_in":26556,"tokens_in_cached":26556,"tokens_out":622,"cost_usd":0.045432,"model":"gemini-3.5-flash"}}`
	var u UsageUpdate
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if u.LastTurn == nil {
		t.Fatal("LastTurn should have parsed from the payload")
	}
	if u.LastTurn.TokensIn != 26556 || u.LastTurn.TokensInCached != 26556 {
		t.Errorf("cached-token fields wrong: %+v", u.LastTurn)
	}
	if u.LastTurn.CostUSD != 0.045432 {
		t.Errorf("cost wrong: %f", u.LastTurn.CostUSD)
	}
	if u.LastTurn.Model != "gemini-3.5-flash" {
		t.Errorf("model wrong: %q", u.LastTurn.Model)
	}
}
