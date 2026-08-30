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
)

// mixedCatalog is the shape that broke the flat list (issue #289):
// the operator's own built-ins interleaved alphabetically into a much
// larger MCP server's rows, with skills and a subagent mixed in.
func mixedCatalog() []ToolInfo {
	return []ToolInfo{
		{Name: "bash", Source: "builtin", Description: "run a shell command", GateState: "ask"},
		{Name: "edit", Source: "builtin", Description: "edit a file"},
		{Name: "read", Source: "builtin", Description: "read a file"},
		{Name: "gke_cluster_get", Source: "gke", Description: "describe a cluster"},
		{Name: "gke_cluster_list", Source: "gke", Description: "list clusters"},
		{Name: "gke_node_drain", Source: "gke", Description: "drain a node", GateState: "denied"},
		{Name: "review_diff", Source: "skill:review", Description: "review a diff"},
		{Name: "write_adr", Source: "skill:adr", Description: "write a decision record"},
		{Name: "auditor", Source: "subagent", Description: "the auditor subagent"},
	}
}

// TestRenderToolList_GroupsBySourceWithCounts is the fix: a summary
// line the operator can read in one glance, then one heading per
// source, instead of nine rows in one alphabetical run.
func TestRenderToolList_GroupsBySourceWithCounts(t *testing.T) {
	m := newModel(Options{Agent: &noopAgent{}, ForceTheme: ThemeDark})
	got := m.renderToolList(mixedCatalog(), "")

	// Summary line, in group order: builtin leads, "other" would
	// trail, the rest alphabetical.
	wantSummary := "Tools (9): builtin 3 " + GlyphSeparator + " gke 3 " + GlyphSeparator + " skill 2 " + GlyphSeparator + " subagent 1"
	if !strings.Contains(got, wantSummary) {
		t.Errorf("missing summary line %q\n  output:\n%s", wantSummary, got)
	}
	for _, heading := range []string{"builtin", "gke", "skill", "subagent"} {
		if !strings.Contains(got, heading) {
			t.Errorf("missing group heading %q\n  output:\n%s", heading, got)
		}
	}
	// builtin leads because it is the set the operator already knows.
	if i, j := strings.Index(got, "builtin"), strings.Index(got, "gke"); i > j {
		t.Errorf("builtin should head the listing, got it after gke\n  output:\n%s", got)
	}
}

// TestRenderToolList_GroupedModeDropsDescriptions. The descriptions
// are most of the vertical space and they are what buries the one
// tool the operator was looking for. They come back under a filter,
// where the set is already small enough to read.
func TestRenderToolList_GroupedModeDropsDescriptions(t *testing.T) {
	m := newModel(Options{Agent: &noopAgent{}, ForceTheme: ThemeDark})
	got := m.renderToolList(mixedCatalog(), "")

	if strings.Contains(got, "describe a cluster") {
		t.Errorf("grouped mode should not print descriptions\n  output:\n%s", got)
	}
	// The gate survives: "this one will stop and ask" changes what
	// the operator does next in a way a description does not.
	if !strings.Contains(got, "[ask]") || !strings.Contains(got, "[denied]") {
		t.Errorf("grouped mode dropped the gate annotations\n  output:\n%s", got)
	}
	// A nine-tool catalog must still be shorter than it was flat: 9
	// names + 4 headings + summary + hint + blanks, versus 9 names +
	// 9 descriptions + 8 blank separators.
	if lines := strings.Count(got, "\n") + 1; lines > 20 {
		t.Errorf("grouped listing is %d lines, want it tighter than the flat layout\n  output:\n%s", lines, got)
	}
}

// TestRenderToolList_FiltersBySource covers both halves of the
// argument: a group family ("skill", which folds skill:review and
// skill:adr together) and a full source ("gke").
func TestRenderToolList_FiltersBySource(t *testing.T) {
	m := newModel(Options{Agent: &noopAgent{}, ForceTheme: ThemeDark})

	gke := m.renderToolList(mixedCatalog(), "gke")
	if !strings.Contains(gke, "Tools from gke (3):") {
		t.Errorf("filtered header wrong\n  output:\n%s", gke)
	}
	if strings.Contains(gke, "bash") {
		t.Errorf("/tools gke leaked a builtin\n  output:\n%s", gke)
	}
	// Descriptions are the point of narrowing.
	if !strings.Contains(gke, "describe a cluster") {
		t.Errorf("filtered view dropped descriptions\n  output:\n%s", gke)
	}

	// The family key gathers every skill:<name> under one filter.
	skills := m.renderToolList(mixedCatalog(), "skill")
	if !strings.Contains(skills, "Tools from skill (2):") {
		t.Errorf("family filter did not fold skill:*\n  output:\n%s", skills)
	}
	for _, want := range []string{"review_diff", "write_adr"} {
		if !strings.Contains(skills, want) {
			t.Errorf("/tools skill missing %q\n  output:\n%s", want, skills)
		}
	}
	// A full source still works, and narrows further than the family.
	one := m.renderToolList(mixedCatalog(), "skill:adr")
	if !strings.Contains(one, "write_adr") || strings.Contains(one, "review_diff") {
		t.Errorf("/tools skill:adr should match only its own tool\n  output:\n%s", one)
	}
}

// TestRenderToolList_FilterIsCaseInsensitive — the operator is typing
// a server name from memory, not copying it.
func TestRenderToolList_FilterIsCaseInsensitive(t *testing.T) {
	m := newModel(Options{Agent: &noopAgent{}, ForceTheme: ThemeDark})
	if got := m.renderToolList(mixedCatalog(), "GKE"); !strings.Contains(got, "gke_cluster_list") {
		t.Errorf("/tools GKE found nothing\n  output:\n%s", got)
	}
}

// TestRenderToolList_MissNamesTheAvailableSources. Filtering by source
// is the only reason to know the source names, so a miss that just
// says "nothing" is a dead end.
func TestRenderToolList_MissNamesTheAvailableSources(t *testing.T) {
	m := newModel(Options{Agent: &noopAgent{}, ForceTheme: ThemeDark})
	got := m.renderToolList(mixedCatalog(), "gek")
	if !strings.Contains(got, "no tools from") {
		t.Errorf("miss should say so\n  output:\n%s", got)
	}
	for _, want := range []string{"builtin", "gke", "skill", "subagent"} {
		if !strings.Contains(got, want) {
			t.Errorf("miss should name source %q so the operator can retry\n  output:\n%s", want, got)
		}
	}
}

// TestRenderToolList_SingleSourceKeepsTheOldLayout. Grouping a catalog
// that has one source adds a heading that says nothing. A host
// reporting only its built-ins — the case the flat layout was written
// for — sees exactly what it saw before #289.
func TestRenderToolList_SingleSourceKeepsTheOldLayout(t *testing.T) {
	m := newModel(Options{Agent: &noopAgent{}, ForceTheme: ThemeDark})
	only := []ToolInfo{
		{Name: "bash", Source: "builtin", Description: "run a shell command"},
		{Name: "edit", Source: "builtin", Description: "edit a file"},
	}
	got := m.renderToolList(only, "")
	if !strings.Contains(got, "Tools (2):") {
		t.Errorf("single-source header changed\n  output:\n%s", got)
	}
	if !strings.Contains(got, "run a shell command") {
		t.Errorf("single-source view should keep descriptions\n  output:\n%s", got)
	}
	if strings.Contains(got, "builtin (2)") {
		t.Errorf("single-source view should not add a group heading\n  output:\n%s", got)
	}
}

// TestRenderToolList_EmptySourceGroupsUnderOther guards the heading
// that would otherwise render with no name at all.
func TestRenderToolList_EmptySourceGroupsUnderOther(t *testing.T) {
	m := newModel(Options{Agent: &noopAgent{}, ForceTheme: ThemeDark})
	got := m.renderToolList([]ToolInfo{
		{Name: "bash", Source: "builtin"},
		{Name: "mystery"},
	}, "")
	if !strings.Contains(got, "other 1") {
		t.Errorf("unsourced tool should group under \"other\"\n  output:\n%s", got)
	}
	// And "other" trails, being the leftovers.
	if i, j := strings.Index(got, "builtin 1"), strings.Index(got, "other 1"); i > j {
		t.Errorf("\"other\" should come last in the summary\n  output:\n%s", got)
	}
}

// TestRenderToolList_EmptyCatalogUnchanged — the no-tools row predates
// this and is still the right answer.
func TestRenderToolList_EmptyCatalogUnchanged(t *testing.T) {
	m := newModel(Options{Agent: &noopAgent{}, ForceTheme: ThemeDark})
	if got := m.renderToolList(nil, ""); got != "Agent has no tools registered." {
		t.Errorf("empty catalog = %q", got)
	}
}

// TestToolsSlash_CarriesTheFilterThroughTheRoundTrip. Tools() is a
// round trip on a remote host and the input line is reset the moment
// the command dispatches, so the argument has to ride along on the
// msg — reading it back off the input when the catalog lands would
// find nothing.
func TestToolsSlash_CarriesTheFilterThroughTheRoundTrip(t *testing.T) {
	m := newModel(Options{Agent: &toolListerAgent{tools: mixedCatalog()}})
	m.width, m.height = 100, 40
	m.resize()
	m.input.SetValue("/tools gke")

	next, cmd := pressKey(m, tea.Key{Code: tea.KeyEnter})
	if next.input.Value() != "" {
		t.Fatalf("input not cleared: %q", next.input.Value())
	}
	msg, ok := runCmd(t, cmd).(toolsListedMsg)
	if !ok {
		t.Fatalf("/tools produced %T, want a toolsListedMsg", msg)
	}
	if msg.filter != "gke" {
		t.Fatalf("filter = %q, want %q", msg.filter, "gke")
	}

	out, _ := next.Update(msg)
	if got := lastText(out.(model)); !strings.Contains(got, "Tools from gke (3):") {
		t.Errorf("filtered listing did not render\n  output:\n%s", got)
	}
}

// toolListerAgent is a bare Agent that also reports a tool catalog.
type toolListerAgent struct {
	noopAgent
	tools []ToolInfo
}

func (a *toolListerAgent) Tools() []ToolInfo { return a.tools }
