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

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

var seedModels = []ModelInfo{
	{ID: "alpha-1", Display: "Alpha One"},
	{ID: "beta-2", Display: "Beta Two"},
	{ID: "gamma-3"}, // no Display: falls back to ID
	{ID: "delta-4", Display: "Delta Four"},
}

// indexOfModel resolves the cursor row for the active model. It has to
// match on the same predicate Render paints "(current)" with — ID or,
// failing that, the display name — because a host may advertise either
// as the string the operator recognises.
func TestIndexOfModel(t *testing.T) {
	cases := []struct {
		name    string
		current string
		want    int
	}{
		{"matches on ID", "beta-2", 1},
		{"matches on Display", "Delta Four", 3},
		{"Display falling back to ID", "gamma-3", 2},
		{"first row is not special-cased", "alpha-1", 0},
		{"unset model lands on row 0", "", 0},
		{"model absent from the host's own list lands on row 0", "not-advertised", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := indexOfModel(seedModels, tc.current); got != tc.want {
				t.Errorf("indexOfModel(%q) = %d, want %d", tc.current, got, tc.want)
			}
		})
	}
}

// The snapshot arrives asynchronously (#114), so the seed happens on
// arrival rather than in the constructor — the constructor has no list
// to seed against.
func TestApplyModels_SeedsCursorOnCurrent(t *testing.T) {
	d := newModelPickerDialog()
	if d.idx != 0 {
		t.Fatalf("setup: fresh dialog idx = %d, want 0", d.idx)
	}

	d.applyModels(seedModels, "gamma-3")

	if !d.loaded {
		t.Error("applyModels did not mark the snapshot loaded")
	}
	if d.idx != 2 {
		t.Errorf("cursor seeded at %d, want 2 (gamma-3)", d.idx)
	}
	if d.off != 0 {
		t.Errorf("scroll offset = %d, want 0", d.off)
	}
}

// End to end through Update: the reply lands as modelsLoadedMsg, and
// the cursor must be on the row Render tags "(current)" rather than on
// whatever the host happened to list first.
func TestModelsLoadedMsg_CursorLandsOnTheCurrentRow(t *testing.T) {
	m := NewModel(Options{})
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = out.(Model)
	m.currentModel = "beta-2"

	d := newModelPickerDialog()
	m.overlayStack.Open(d)

	out, _ = m.Update(modelsLoadedMsg{gen: m.sessionGen, models: seedModels})
	m = out.(Model)

	if d.idx != 1 {
		t.Fatalf("cursor at %d, want 1 (beta-2)", d.idx)
	}

	// The marker and the "(current)" tag must be on the same row —
	// that is the whole point, and asserting the index alone would
	// not catch the two drifting apart.
	for _, line := range strings.Split(ansi.Strip(d.Render(100, &m)), "\n") {
		if strings.Contains(line, "(current)") && !strings.HasPrefix(strings.TrimSpace(line), ">") {
			t.Errorf("the current model is not the selected row: %q", strings.TrimSpace(line))
		}
	}
}
