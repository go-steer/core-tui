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
	"context"
	"encoding/json"
	"iter"
	"strings"
	"testing"
)

// TestRemoteEvents_JSONShapeMatchesSpec asserts the exported
// payload types unmarshal cleanly from the JSON shapes in the
// spec's examples. If a host writes a parser by reading the spec
// and these structs round-trip the same JSON, integration is
// safe-by-construction.
func TestRemoteEvents_JSONShapeMatchesSpec(t *testing.T) {
	// Sample from spec §2.2 status-update.
	const statusJSON = `{
		"model": "gemini-2.5-pro",
		"provider": "vertex",
		"perm_mode": "default",
		"turn_state": "streaming",
		"context_pct": 42
	}`
	var su StatusUpdate
	if err := json.Unmarshal([]byte(statusJSON), &su); err != nil {
		t.Fatalf("unmarshal status-update: %v", err)
	}
	if su.Model != "gemini-2.5-pro" || su.Provider != "vertex" || su.PermMode != "default" || su.TurnState != "streaming" {
		t.Errorf("status-update field decode mismatch: %+v", su)
	}
	if su.ContextPct == nil || *su.ContextPct != 42 {
		t.Errorf("status-update context_pct = %v, want pointer to 42", su.ContextPct)
	}

	// Sample from spec §2.3 usage-update with by_model.
	const usageJSON = `{
		"tokens_in_total": 5557,
		"tokens_out_total": 123,
		"cost_usd_total": 0.0126,
		"turns_total": 2,
		"by_model": {
			"gemini-2.5-pro": {"tokens_in": 4521, "tokens_out": 87, "cost_usd": 0.0102, "turns": 2}
		}
	}`
	var uu UsageUpdate
	if err := json.Unmarshal([]byte(usageJSON), &uu); err != nil {
		t.Fatalf("unmarshal usage-update: %v", err)
	}
	if uu.TokensInTotal != 5557 || uu.TokensOutTotal != 123 || uu.CostUSDTotal != 0.0126 || uu.TurnsTotal != 2 {
		t.Errorf("usage-update field decode mismatch: %+v", uu)
	}
	pro, ok := uu.ByModel["gemini-2.5-pro"]
	if !ok || pro.TokensIn != 4521 || pro.Turns != 2 {
		t.Errorf("usage-update by_model decode mismatch: %+v", uu.ByModel)
	}

	// Sample from spec §2.4 inbox.
	const inboxJSON = `{"state": "queued", "prompt_id": "p-9c4a", "queued_at": "2026-06-07T19:42:11Z"}`
	var ib InboxEvent
	if err := json.Unmarshal([]byte(inboxJSON), &ib); err != nil {
		t.Fatalf("unmarshal inbox: %v", err)
	}
	if ib.State != "queued" || ib.PromptID != "p-9c4a" {
		t.Errorf("inbox field decode mismatch: %+v", ib)
	}

	// Sample from spec §2.5 turn-complete (deferred-cost shape — no cost_usd).
	const turnJSON = `{
		"prompt_id": "p-9c4a",
		"model": "gemini-2.5-pro",
		"tokens_in": 2806,
		"tokens_out": 87,
		"latency_ms": 4521
	}`
	var ts TurnSummary
	if err := json.Unmarshal([]byte(turnJSON), &ts); err != nil {
		t.Fatalf("unmarshal turn-complete: %v", err)
	}
	if ts.PromptID != "p-9c4a" || ts.TokensIn != 2806 || ts.LatencyMs != 4521 || ts.CostUSD != 0 {
		t.Errorf("turn-complete decode mismatch: %+v", ts)
	}

	// Sample from spec §2.6 turn-error.
	const errJSON = `{
		"kind": "model_not_found",
		"code": "NOT_FOUND",
		"message": "Publisher Model not found.",
		"retryable": false,
		"hint": "Check vertex.location and model name."
	}`
	var te TurnError
	if err := json.Unmarshal([]byte(errJSON), &te); err != nil {
		t.Fatalf("unmarshal turn-error: %v", err)
	}
	if te.Kind != "model_not_found" || te.Code != "NOT_FOUND" || te.Retryable || te.Hint == "" {
		t.Errorf("turn-error decode mismatch: %+v", te)
	}
}

// TestRemoteEvents_StatusUpdateMerge asserts the statusUpdateMsg
// handler merges fields (non-empty overrides; empty leaves alone)
// per spec §2.2 semantics, and triggers a theme refresh on
// provider change.
func TestRemoteEvents_StatusUpdateMerge(t *testing.T) {
	m := NewModel(Options{ForceTheme: ThemeDark, Agent: &noopAgent{}})
	m.currentModel = "preexisting-model"

	// First update — sets provider and model; non-empty fields override.
	got, _ := m.Update(statusUpdateMsg{status: StatusUpdate{
		Model:     "new-model",
		Provider:  "anthropic",
		TurnState: TurnStateStreaming,
	}})
	m2 := got.(Model)
	if m2.currentModel != "new-model" {
		t.Errorf("currentModel after status with Model = %q, want new-model", m2.currentModel)
	}
	if m2.pushedProvider != "anthropic" {
		t.Errorf("pushedProvider after status with Provider = %q, want anthropic", m2.pushedProvider)
	}

	// Second update — empty Model, non-empty Provider. Model must
	// stay; provider must update.
	got2, _ := m2.Update(statusUpdateMsg{status: StatusUpdate{
		Provider:  "gemini",
		TurnState: TurnStateIdle,
	}})
	m3 := got2.(Model)
	if m3.currentModel != "new-model" {
		t.Errorf("currentModel after empty-Model update = %q, want unchanged (new-model)", m3.currentModel)
	}
	if m3.pushedProvider != "gemini" {
		t.Errorf("pushedProvider after non-empty Provider update = %q, want gemini", m3.pushedProvider)
	}

	// displayProvider should prefer the pushed value.
	if got := m3.displayProvider(); got != "gemini" {
		t.Errorf("displayProvider after push = %q, want gemini (push wins over StatusReporter fallback)", got)
	}
}

// TestRemoteEvents_StatusUpdateContextPct asserts that ContextPct
// uses pointer semantics so 0 is distinguishable from absent.
func TestRemoteEvents_StatusUpdateContextPct(t *testing.T) {
	m := NewModel(Options{ForceTheme: ThemeDark, Agent: &noopAgent{}})
	if m.pushedContextPct != nil {
		t.Fatalf("initial pushedContextPct should be nil, got %v", m.pushedContextPct)
	}
	zero := 0
	got, _ := m.Update(statusUpdateMsg{status: StatusUpdate{
		TurnState:  TurnStateIdle,
		ContextPct: &zero,
	}})
	m2 := got.(Model)
	if m2.pushedContextPct == nil {
		t.Fatal("pushedContextPct should be non-nil after status carried ContextPct=0")
	}
	if *m2.pushedContextPct != 0 {
		t.Errorf("pushedContextPct value = %d, want 0", *m2.pushedContextPct)
	}
}

// TestRemoteEvents_UsageUpdateSnapshot asserts the session-level
// payload from usage-update is snapshot onto m.sessionUsage.
func TestRemoteEvents_UsageUpdateSnapshot(t *testing.T) {
	m := NewModel(Options{ForceTheme: ThemeDark, Agent: &noopAgent{}})
	payload := UsageUpdate{
		TokensInTotal:  1000,
		TokensOutTotal: 200,
		CostUSDTotal:   0.05,
		TurnsTotal:     3,
		ByModel: map[string]UsageByModel{
			"gemini-2.5-pro":   {TokensIn: 800, TokensOut: 150, CostUSD: 0.04, Turns: 2},
			"gemini-2.5-flash": {TokensIn: 200, TokensOut: 50, CostUSD: 0.01, Turns: 1},
		},
	}
	got, _ := m.Update(usageUpdateMsg{update: payload})
	m2 := got.(Model)
	if m2.sessionUsage == nil {
		t.Fatal("sessionUsage should be non-nil after usage-update")
	}
	if m2.sessionUsage.TokensInTotal != 1000 || m2.sessionUsage.TurnsTotal != 3 {
		t.Errorf("sessionUsage totals mismatch: %+v", m2.sessionUsage)
	}
	if len(m2.sessionUsage.ByModel) != 2 {
		t.Errorf("sessionUsage.ByModel len = %d, want 2", len(m2.sessionUsage.ByModel))
	}
}

// TestRemoteEvents_InboxToastOnQueued asserts the queued state
// surfaces a transient toast; dequeued clears it.
func TestRemoteEvents_InboxToastOnQueued(t *testing.T) {
	m := NewModel(Options{ForceTheme: ThemeDark, Agent: &noopAgent{}})
	got, _ := m.Update(inboxStateMsg{event: InboxEvent{State: InboxStateQueued, PromptID: "p-1"}})
	m2 := got.(Model)
	if !strings.Contains(m2.toast, "queued") {
		t.Errorf("toast after queued = %q, want a 'queued' substring", m2.toast)
	}

	got2, _ := m2.Update(inboxStateMsg{event: InboxEvent{State: InboxStateDequeued, PromptID: "p-1"}})
	m3 := got2.(Model)
	if m3.toast != "" {
		t.Errorf("toast after dequeued = %q, want cleared", m3.toast)
	}

	// Unknown state — must not panic + must not set a toast.
	got3, _ := m3.Update(inboxStateMsg{event: InboxEvent{State: "injected", PromptID: "p-2"}})
	m4 := got3.(Model)
	if m4.toast != "" {
		t.Errorf("toast after unknown state = %q, want empty (tolerated as no-op)", m4.toast)
	}
}

// TestRemoteEvents_TurnSummaryPopulatesFooterState asserts the
// turn-summary payload feeds the same currentUsage / currentCost /
// currentModel fields the per-turn footer reads, so push-mode and
// legacy paths render identical footers.
func TestRemoteEvents_TurnSummaryPopulatesFooterState(t *testing.T) {
	m := NewModel(Options{ForceTheme: ThemeDark, Agent: &noopAgent{}})

	got, _ := m.Update(turnSummaryMsg{summary: TurnSummary{
		PromptID:  "p-1",
		Model:     "gemini-2.5-pro",
		TokensIn:  100,
		TokensOut: 50,
		CostUSD:   0.01,
		LatencyMs: 1234,
	}})
	m2 := got.(Model)
	if m2.currentModel != "gemini-2.5-pro" {
		t.Errorf("currentModel after turn-summary = %q", m2.currentModel)
	}
	if m2.currentUsage == nil || m2.currentUsage.InputTokens != 100 || m2.currentUsage.OutputTokens != 50 {
		t.Errorf("currentUsage after turn-summary = %+v", m2.currentUsage)
	}
	if m2.currentCost != 0.01 {
		t.Errorf("currentCost after turn-summary = %v, want 0.01", m2.currentCost)
	}

	// Deferred-cost shape per spec v1.1.0 — CostUSD=0 must NOT
	// clobber a previously-set positive cost (handler guards with
	// > 0 check).
	got2, _ := m2.Update(turnSummaryMsg{summary: TurnSummary{
		PromptID:  "p-2",
		Model:     "gemini-2.5-pro",
		TokensIn:  200,
		TokensOut: 80,
		CostUSD:   0, // deferred — authoritative cost arrives on next usage-update
		LatencyMs: 2000,
	}})
	m3 := got2.(Model)
	if m3.currentCost != 0.01 {
		t.Errorf("currentCost after deferred-cost turn-summary = %v, want 0.01 (preserved)", m3.currentCost)
	}
}

// TestRemoteEvents_TurnErrorAppendsStyledRow asserts the
// turn-error handler appends a RoleError Message carrying the
// structured payload, and the renderer paints the richer block.
func TestRemoteEvents_TurnErrorAppendsStyledRow(t *testing.T) {
	m := NewModel(Options{ForceTheme: ThemeDark, Agent: &noopAgent{}})
	m.viewport.SetWidth(80)

	got, _ := m.Update(turnErrorMsg{turnError: TurnError{
		Kind:      TurnErrorModelNotFound,
		Code:      "NOT_FOUND",
		Message:   "Publisher Model not found.",
		Retryable: false,
		Hint:      "Check vertex.location and model name.",
	}})
	m2 := got.(Model)
	entries := m2.history.entries
	if len(entries) == 0 {
		t.Fatal("turn-error handler should append a Message")
	}
	last := entries[len(entries)-1]
	if last.Role != RoleError {
		t.Errorf("appended Message.Role = %v, want RoleError", last.Role)
	}
	if last.TurnError == nil {
		t.Fatal("appended Message.TurnError should be non-nil")
	}
	if last.TurnError.Kind != TurnErrorModelNotFound {
		t.Errorf("Message.TurnError.Kind = %q, want %q", last.TurnError.Kind, TurnErrorModelNotFound)
	}

	// Render the message and verify it includes the structured
	// block elements (kind, code, message, hint).
	rendered := m2.renderMessage(last)
	for _, expected := range []string{"model_not_found", "NOT_FOUND", "Publisher Model not found.", "hint:", "Check vertex.location"} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("rendered turn-error missing %q\n  output: %s", expected, rendered)
		}
	}
}

// TestRemoteEvents_TurnErrorRetryableShowsAffordance asserts the
// retryable variant adds the ↻ affordance line.
func TestRemoteEvents_TurnErrorRetryableShowsAffordance(t *testing.T) {
	m := NewModel(Options{ForceTheme: ThemeDark, Agent: &noopAgent{}})
	m.viewport.SetWidth(80)
	got, _ := m.Update(turnErrorMsg{turnError: TurnError{
		Kind:      TurnErrorRateLimited,
		Message:   "Vertex quota exceeded.",
		Retryable: true,
	}})
	m2 := got.(Model)
	rendered := m2.renderMessage(m2.history.entries[len(m2.history.entries)-1])
	if !strings.Contains(rendered, "retryable") {
		t.Errorf("retryable turn-error should contain 'retryable' affordance\n  output: %s", rendered)
	}
}

// noopAgent is a stub Agent for tests that don't need real turn
// dispatch — Update doesn't call Agent.Run for any of the push-
// mode msgs, so a stub is fine. (NewModel requires Options.Agent
// to be non-nil for some paths.)
type noopAgent struct{}

func (*noopAgent) Run(_ context.Context, _ string) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {}
}
