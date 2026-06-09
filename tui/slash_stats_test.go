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
	"time"
)

// bareTracker satisfies UsageTracker only — used to verify the
// /stats fallback path (no Models: row) when the host hasn't
// opted into the SessionByModelTracker capability (issue #18).
type bareTracker struct {
	totals Usage
	cost   float64
}

func (t *bareTracker) SessionTotals() Usage           { return t.totals }
func (t *bareTracker) SessionCostUSD() float64        { return t.cost }
func (t *bareTracker) LastTurn() (Usage, float64)     { return t.totals, t.cost }
func (t *bareTracker) ContextWindowSize() int         { return 0 }
func (t *bareTracker) ContextWindowUsed() int         { return 0 }
func (t *bareTracker) SessionTurns() int              { return 1 }
func (t *bareTracker) SessionDuration() time.Duration { return time.Second }

// modelAwareTracker also satisfies SessionByModelTracker.
type modelAwareTracker struct {
	bareTracker
	byModel map[string]ModelTotals
}

func (t *modelAwareTracker) SessionByModel() map[string]ModelTotals { return t.byModel }

func TestRenderStats_BareTracker_NoModelsRow(t *testing.T) {
	m := NewModel(Options{UsageTracker: &bareTracker{
		totals: Usage{InputTokens: 100, OutputTokens: 50},
		cost:   0.01,
	}})
	got := m.renderStats()
	if strings.Contains(got, "Models:") {
		t.Errorf("bare tracker: no Models: row expected, got:\n%s", got)
	}
}

func TestRenderStats_EmptyByModelMap_NoRow(t *testing.T) {
	m := NewModel(Options{UsageTracker: &modelAwareTracker{
		bareTracker: bareTracker{totals: Usage{InputTokens: 100, OutputTokens: 50}, cost: 0.01},
		byModel:     map[string]ModelTotals{},
	}})
	got := m.renderStats()
	if strings.Contains(got, "Models:") {
		t.Errorf("empty by-model map: no row expected, got:\n%s", got)
	}
}

func TestRenderStats_SingleEntryByModelMap_NoRow(t *testing.T) {
	// One model means the breakdown duplicates SessionTotals —
	// skip rendering per the issue contract.
	m := NewModel(Options{UsageTracker: &modelAwareTracker{
		bareTracker: bareTracker{totals: Usage{InputTokens: 100, OutputTokens: 50}, cost: 0.01},
		byModel: map[string]ModelTotals{
			"only-model": {Turns: 1, InputTokens: 100, OutputTokens: 50, CostUSD: 0.01},
		},
	}})
	got := m.renderStats()
	if strings.Contains(got, "Models:") {
		t.Errorf("single-entry by-model map: no row expected, got:\n%s", got)
	}
}

func TestRenderStats_MultiEntryByModelMap_RendersBreakdown(t *testing.T) {
	m := NewModel(Options{UsageTracker: &modelAwareTracker{
		bareTracker: bareTracker{totals: Usage{InputTokens: 1200, OutputTokens: 200}, cost: 0.012},
		byModel: map[string]ModelTotals{
			"gemini-3.1-pro":   {Turns: 5, InputTokens: 1000, OutputTokens: 150, CostUSD: 0.010},
			"gemini-2.5-flash": {Turns: 2, InputTokens: 200, OutputTokens: 50, CostUSD: 0.002},
		},
	}})
	got := m.renderStats()
	if !strings.Contains(got, "Models:") {
		t.Fatalf("expected Models: row, got:\n%s", got)
	}
	if !strings.Contains(got, "gemini-3.1-pro") || !strings.Contains(got, "gemini-2.5-flash") {
		t.Errorf("expected both model names in breakdown, got:\n%s", got)
	}
	// Priciest leads — pro should appear before flash.
	if idxPro, idxFlash := strings.Index(got, "gemini-3.1-pro"), strings.Index(got, "gemini-2.5-flash"); idxPro > idxFlash {
		t.Errorf("expected priciest model first (pro before flash), got pro at %d flash at %d:\n%s", idxPro, idxFlash, got)
	}
}

func TestRenderStats_SortByCostDescending(t *testing.T) {
	m := NewModel(Options{UsageTracker: &modelAwareTracker{
		bareTracker: bareTracker{totals: Usage{InputTokens: 0, OutputTokens: 0}, cost: 0.06},
		byModel: map[string]ModelTotals{
			"cheap":  {Turns: 1, CostUSD: 0.01},
			"mid":    {Turns: 1, CostUSD: 0.02},
			"costly": {Turns: 1, CostUSD: 0.03},
		},
	}})
	got := m.renderStats()
	iCostly := strings.Index(got, "costly")
	iMid := strings.Index(got, "mid")
	iCheap := strings.Index(got, "cheap")
	if iCostly >= iMid || iMid >= iCheap {
		t.Errorf("expected costly < mid < cheap by appearance, got positions costly=%d mid=%d cheap=%d in:\n%s",
			iCostly, iMid, iCheap, got)
	}
}

func TestRenderStats_TurnsPluralization(t *testing.T) {
	m := NewModel(Options{UsageTracker: &modelAwareTracker{
		bareTracker: bareTracker{totals: Usage{}, cost: 0.05},
		byModel: map[string]ModelTotals{
			"single": {Turns: 1, CostUSD: 0.04},
			"multi":  {Turns: 3, CostUSD: 0.01},
		},
	}})
	got := m.renderStats()
	if !strings.Contains(got, "1 turn,") {
		t.Errorf("expected '1 turn,' (singular) for single, got:\n%s", got)
	}
	if !strings.Contains(got, "3 turns,") {
		t.Errorf("expected '3 turns,' (plural) for multi, got:\n%s", got)
	}
}

func TestFormatModelBreakdown_FirstPrefixDiffersFromContinuation(t *testing.T) {
	got := formatModelBreakdown(map[string]ModelTotals{
		"a": {Turns: 1, CostUSD: 0.02},
		"b": {Turns: 1, CostUSD: 0.01},
	})
	// First row starts with "  Models:     ", continuation rows
	// start with "              + " so the operator's eye anchors
	// on the label only once.
	if !strings.HasPrefix(got, "  Models:") {
		t.Errorf("first row should start with 'Models:' prefix, got:\n%s", got)
	}
	if !strings.Contains(got, "\n              + ") {
		t.Errorf("continuation rows should use the '+' indent prefix, got:\n%s", got)
	}
}

func TestRenderStats_NoTracker_GracefulMessage(t *testing.T) {
	// Sanity — no panic when UsageTracker is unwired.
	m := NewModel(Options{})
	got := m.renderStats()
	if !strings.Contains(got, "no UsageTracker") {
		t.Errorf("expected helpful 'no UsageTracker' message, got: %q", got)
	}
}

// Push-path coverage (issue #38): m.sessionUsage.ByModel — populated
// by the usageUpdateMsg handler from SSE usage-update events —
// must produce the same Models: breakdown the pull-path tracker
// produces, and follow the same skip rules for single-entry / nil.

func TestRenderStats_PushSessionUsage_MultiEntry_RendersBreakdown(t *testing.T) {
	m := NewModel(Options{UsageTracker: &bareTracker{
		totals: Usage{InputTokens: 1200, OutputTokens: 200},
		cost:   0.012,
	}})
	// Simulate a usage-update event having landed.
	m.sessionUsage = &UsageUpdate{
		TokensInTotal:  1200,
		TokensOutTotal: 200,
		CostUSDTotal:   0.012,
		TurnsTotal:     7,
		ByModel: map[string]UsageByModel{
			"gemini-3.1-pro":   {Turns: 5, TokensIn: 1000, TokensOut: 150, CostUSD: 0.010},
			"gemini-2.5-flash": {Turns: 2, TokensIn: 200, TokensOut: 50, CostUSD: 0.002},
		},
	}
	got := m.renderStats()
	if !strings.Contains(got, "Models:") {
		t.Fatalf("expected Models: row from push path, got:\n%s", got)
	}
	if !strings.Contains(got, "gemini-3.1-pro") || !strings.Contains(got, "gemini-2.5-flash") {
		t.Errorf("expected both model names in push breakdown, got:\n%s", got)
	}
	// Same sort — priciest first.
	if idxPro, idxFlash := strings.Index(got, "gemini-3.1-pro"), strings.Index(got, "gemini-2.5-flash"); idxPro > idxFlash {
		t.Errorf("expected priciest model first (pro before flash), got pro at %d flash at %d:\n%s", idxPro, idxFlash, got)
	}
	// Per-row turn counts should match what UsageByModel.Turns carried.
	if !strings.Contains(got, "5 turns") || !strings.Contains(got, "2 turns") {
		t.Errorf("expected per-row turn counts in breakdown, got:\n%s", got)
	}
}

func TestRenderStats_PushSessionUsage_SingleEntry_NoRow(t *testing.T) {
	// Single-entry push breakdown duplicates the aggregate — skip
	// (same rule as the pull path).
	m := NewModel(Options{UsageTracker: &bareTracker{
		totals: Usage{InputTokens: 100, OutputTokens: 50}, cost: 0.01,
	}})
	m.sessionUsage = &UsageUpdate{
		TokensInTotal:  100,
		TokensOutTotal: 50,
		CostUSDTotal:   0.01,
		TurnsTotal:     1,
		ByModel: map[string]UsageByModel{
			"only-model": {Turns: 1, TokensIn: 100, TokensOut: 50, CostUSD: 0.01},
		},
	}
	got := m.renderStats()
	if strings.Contains(got, "Models:") {
		t.Errorf("single-entry push breakdown: no row expected, got:\n%s", got)
	}
}

func TestRenderStats_PushWinsOverPull_WhenBothPopulated(t *testing.T) {
	// In remote/attach mode both sources may have data (the local
	// pull tracker may keep its own per-model totals from observed
	// usageMsg events). Push must win because it reflects the
	// daemon's own tracker — the authoritative source.
	pullTracker := &modelAwareTracker{
		bareTracker: bareTracker{totals: Usage{InputTokens: 100, OutputTokens: 50}, cost: 0.01},
		byModel: map[string]ModelTotals{
			"pull-only-model-A": {Turns: 1, InputTokens: 50, OutputTokens: 25, CostUSD: 0.005},
			"pull-only-model-B": {Turns: 1, InputTokens: 50, OutputTokens: 25, CostUSD: 0.005},
		},
	}
	m := NewModel(Options{UsageTracker: pullTracker})
	m.sessionUsage = &UsageUpdate{
		TokensInTotal:  100,
		TokensOutTotal: 50,
		CostUSDTotal:   0.01,
		TurnsTotal:     2,
		ByModel: map[string]UsageByModel{
			"push-model-X": {Turns: 1, TokensIn: 60, TokensOut: 30, CostUSD: 0.006},
			"push-model-Y": {Turns: 1, TokensIn: 40, TokensOut: 20, CostUSD: 0.004},
		},
	}
	got := m.renderStats()
	if !strings.Contains(got, "push-model-X") || !strings.Contains(got, "push-model-Y") {
		t.Errorf("expected push-source models in breakdown, got:\n%s", got)
	}
	if strings.Contains(got, "pull-only-model-A") || strings.Contains(got, "pull-only-model-B") {
		t.Errorf("push must win over pull when both populated — pull names should NOT appear, got:\n%s", got)
	}
}

func TestRenderStats_PullPathStillWorks_WhenPushAbsent(t *testing.T) {
	// Regression: the pre-#38 pull path must keep working when
	// sessionUsage is nil (embedded mode with no push events).
	pullTracker := &modelAwareTracker{
		bareTracker: bareTracker{totals: Usage{InputTokens: 1200, OutputTokens: 200}, cost: 0.012},
		byModel: map[string]ModelTotals{
			"gemini-3.1-pro":   {Turns: 5, InputTokens: 1000, OutputTokens: 150, CostUSD: 0.010},
			"gemini-2.5-flash": {Turns: 2, InputTokens: 200, OutputTokens: 50, CostUSD: 0.002},
		},
	}
	m := NewModel(Options{UsageTracker: pullTracker})
	// sessionUsage stays nil — embedded-mode shape.
	got := m.renderStats()
	if !strings.Contains(got, "Models:") {
		t.Fatalf("expected pull-path Models: row when sessionUsage nil, got:\n%s", got)
	}
	if !strings.Contains(got, "gemini-3.1-pro") || !strings.Contains(got, "gemini-2.5-flash") {
		t.Errorf("expected both pull-source model names, got:\n%s", got)
	}
}

func TestUsageByModelToTotals_FieldMapping(t *testing.T) {
	in := map[string]UsageByModel{
		"m1": {Turns: 3, TokensIn: 100, TokensOut: 40, CostUSD: 0.005},
	}
	out := usageByModelToTotals(in)
	if len(out) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(out))
	}
	got := out["m1"]
	if got.Turns != 3 || got.InputTokens != 100 || got.OutputTokens != 40 || got.CostUSD != 0.005 {
		t.Errorf("field mapping mismatch: got %+v", got)
	}
}
