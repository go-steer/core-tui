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

// TestPalette_FilterRanksFourTier pins the 4-tier ranking
// (agentic-tui skill §8.B): exact basename → basename prefix →
// path-segment exact → fuzzy substring. Tiebreak by shorter
// path. All matches case-insensitive.
func TestPalette_FilterRanksFourTier(t *testing.T) {
	p := &palette{
		kind: paletteSlash,
		items: []paletteItem{
			{Name: "model", Available: true},     // substring "od"
			{Name: "odd", Available: true},       // basename prefix "od" (shorter than Odyssey)
			{Name: "code", Available: true},      // substring "od"
			{Name: "Odyssey", Available: true},   // basename prefix "od" (case-insensitive)
			{Name: "unrelated", Available: true}, // no match
		},
	}
	p.filter = "od"
	got := p.filtered()
	if len(got) != 4 {
		t.Fatalf("expected 4 matches for %q, got %d (%v)", p.filter, len(got), names(got))
	}
	// Tier 2 (basename prefix): odd (len 3) before Odyssey (len 7).
	// Tier 4 (substring): code (len 4) before model (len 5).
	want := []string{"odd", "Odyssey", "code", "model"}
	for i, n := range want {
		if got[i].Name != n {
			t.Errorf("rank %d: got %q, want %q (full order: %v)", i, got[i].Name, n, names(got))
		}
	}
}

// TestPalette_FilterFourTierFiles exercises all four tiers
// against file-like names so the per-tier ordering is visible.
func TestPalette_FilterFourTierFiles(t *testing.T) {
	p := &palette{
		kind: paletteFile,
		items: []paletteItem{
			{Name: "lib/domain.go"},   // tier 4: substring "main" inside "domain"
			{Name: "cmd/main/run.go"}, // tier 3: path-segment "main"
			{Name: "main_test.go"},    // tier 2: basename prefix "main"
			{Name: "main.go"},         // tier 1: exact basename "main" (well, "main.go" basename — not exact)
			{Name: "main"},            // tier 1: exact basename "main"
		},
	}
	p.filter = "main"
	got := p.filtered()
	want := []string{
		"main",            // tier 1 exact
		"main.go",         // tier 2 prefix (shorter than main_test.go)
		"main_test.go",    // tier 2 prefix
		"cmd/main/run.go", // tier 3 segment
		"lib/domain.go",   // tier 4 substring
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d matches, got %d (%v)", len(want), len(got), names(got))
	}
	for i, n := range want {
		if got[i].Name != n {
			t.Errorf("rank %d: got %q, want %q (full order: %v)", i, got[i].Name, n, names(got))
		}
	}
}

// TestPalette_FilterCaseInsensitive pins case-insensitive matching.
func TestPalette_FilterCaseInsensitive(t *testing.T) {
	p := &palette{
		kind:   paletteSlash,
		items:  []paletteItem{{Name: "Memory", Available: true}},
		filter: "mem",
	}
	got := p.filtered()
	if len(got) != 1 || got[0].Name != "Memory" {
		t.Errorf("case-insensitive filter failed: got %v", names(got))
	}
}

// TestPalette_FilterEmptyReturnsAll pins that an empty filter shows
// the full catalog in source order.
func TestPalette_FilterEmptyReturnsAll(t *testing.T) {
	p := &palette{items: builtinSlashItems()}
	if got := p.filtered(); len(got) != len(p.items) {
		t.Errorf("empty filter: got %d items, want %d", len(got), len(p.items))
	}
}

// TestPalette_RankingOrderIsPinned is the regression net around the
// ranker itself: the FULL ordered result for a spread of filters over
// the real built-in catalog and a file-shaped fixture, written out
// literally.
//
// The three tests above pin one tier boundary each, which is the
// right shape for explaining the algorithm but is not enough to move
// it. This one exists because the ranking was lifted out of
// palette.filtered into the shared rankNames helper (issue #117) so
// the pickers could use it too, and "the palette's behaviour must not
// change" needed to be a test rather than a claim. Every element of
// every want below was captured from the pre-lift implementation.
//
// A deliberate change to the ranking updates these tables; an
// accidental one fails them.
func TestPalette_RankingOrderIsPinned(t *testing.T) {
	slashCases := []struct {
		filter string
		want   []string
	}{
		{filter: "m", want: []string{"mcp", "model", "mouse", "memory", "theme", "resume", "permissions"}},
		{filter: "mo", want: []string{"model", "mouse", "memory"}},
		{filter: "model", want: []string{"model"}},
		{filter: "pricing", want: []string{"pricing", "pricing set", "pricing refresh"}},
		{filter: "e", want: []string{"deny", "help", "keys", "clear", "model", "mouse", "pause", "theme", "memory", "reload", "resume", "continue", "interrupt", "subagents", "permissions", "pricing set", "allow bundle:", "pricing refresh"}},
		{filter: "allow", want: []string{"allow", "allow bundle:"}},
		{filter: "sw", want: []string{"switch"}},
		{filter: "set", want: []string{"pricing set"}},
		{filter: "ricing", want: []string{"pricing", "pricing set", "pricing refresh"}},
		{filter: "help", want: []string{"help"}},
		// Case folding is on the whole name, not just the head.
		{filter: "S", want: []string{"stats", "skills", "switch", "subagents", "keys", "mouse", "pause", "tools", "resume", "permissions", "pricing set", "pricing refresh"}},
		// Non-matching filters drop every row rather than scoring
		// them to zero — the palette shows its "no matches" state.
		{filter: "z", want: nil},
		{filter: "?", want: nil},
		{filter: "/", want: nil},
	}
	for _, tc := range slashCases {
		t.Run("slash/"+tc.filter, func(t *testing.T) {
			p := &palette{kind: paletteSlash, items: builtinSlashItems(), filter: tc.filter}
			assertNameOrder(t, names(p.filtered()), tc.want)
		})
	}

	files := []paletteItem{
		{Name: "main"},
		{Name: "main.go"},
		{Name: "main_test.go"},
		{Name: "cmd/main/run.go"},
		{Name: "cmd/maintain/run.go"},
		{Name: "lib/domain.go"},
		{Name: "docs/", IsDir: true},
		{Name: "docs/design.md"},
		{Name: "internal/main/main.go"},
		{Name: "README.md"},
	}
	fileCases := []struct {
		filter string
		want   []string
	}{
		{filter: "main", want: []string{"main", "main.go", "main_test.go", "internal/main/main.go", "cmd/main/run.go", "lib/domain.go", "cmd/maintain/run.go"}},
		{filter: "MAIN", want: []string{"main", "main.go", "main_test.go", "internal/main/main.go", "cmd/main/run.go", "lib/domain.go", "cmd/maintain/run.go"}},
		{filter: "run", want: []string{"cmd/main/run.go", "cmd/maintain/run.go"}},
		{filter: "docs", want: []string{"docs/", "docs/design.md"}},
		{filter: "go", want: []string{"main.go", "main_test.go", "lib/domain.go", "cmd/main/run.go", "cmd/maintain/run.go", "internal/main/main.go"}},
		{filter: "md", want: []string{"README.md", "docs/design.md", "cmd/main/run.go", "cmd/maintain/run.go"}},
		{filter: "d", want: []string{"lib/domain.go", "docs/design.md", "docs/", "README.md", "cmd/main/run.go", "cmd/maintain/run.go"}},
		// Empty filter is identity, in source order — not a rank of
		// everything against "".
		{filter: "", want: []string{"main", "main.go", "main_test.go", "cmd/main/run.go", "cmd/maintain/run.go", "lib/domain.go", "docs/", "docs/design.md", "internal/main/main.go", "README.md"}},
	}
	for _, tc := range fileCases {
		t.Run("file/"+tc.filter, func(t *testing.T) {
			p := &palette{kind: paletteFile, items: files, filter: tc.filter}
			assertNameOrder(t, names(p.filtered()), tc.want)
		})
	}
}

// assertNameOrder compares two name slices element by element and
// reports the whole ordering on failure — a rank test whose message
// is "index 4 differs" makes you re-run it by hand to see what moved.
func assertNameOrder(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d rows %v, want %d rows %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rank %d is %q, want %q\n got: %v\nwant: %v", i, got[i], want[i], got, want)
		}
	}
}

// TestPalette_ContainsSwitch pins that /switch (issue #53) is
// discoverable when the operator types `/` to open the palette.
// v0.10.0 shipped the dispatcher + /help entry but forgot the
// palette row, so operators who didn't already know the name
// couldn't find it.
func TestPalette_ContainsSwitch(t *testing.T) {
	items := builtinSlashItems()
	for _, it := range items {
		if it.Name == "switch" {
			if !it.Available {
				t.Errorf("/switch palette row should be Available=true")
			}
			return
		}
	}
	t.Fatalf("builtinSlashItems() does not contain /switch — the slash palette won't advertise it")
}

// TestPalette_CursorWrapsAround pins ↑/↓ wrap behavior.
func TestPalette_CursorWrapsAround(t *testing.T) {
	p := &palette{
		items: []paletteItem{
			{Name: "a", Available: true},
			{Name: "b", Available: true},
			{Name: "c", Available: true},
		},
	}
	p.moveCursor(-1) // wrap to last
	if p.cursor != 2 {
		t.Errorf("backward wrap: cursor = %d, want 2", p.cursor)
	}
	p.moveCursor(1) // wrap to first
	if p.cursor != 0 {
		t.Errorf("forward wrap: cursor = %d, want 0", p.cursor)
	}
}

// TestPalette_CompletionExtendsToCommonPrefix pins Tab's behavior of
// auto-completing the filter to the longest shared prefix of matches.
func TestPalette_CompletionExtendsToCommonPrefix(t *testing.T) {
	p := &palette{
		items: []paletteItem{
			{Name: "memory", Available: true},
			{Name: "merge", Available: true},
		},
		filter: "m",
	}
	if got := p.completion(); got != "me" {
		t.Errorf("completion: got %q, want %q", got, "me")
	}
}

// TestPalette_CompletionEmptyWhenAlreadyFull pins that Tab doesn't
// loop or insert garbage when the filter already equals the common
// prefix.
func TestPalette_CompletionEmptyWhenAlreadyFull(t *testing.T) {
	p := &palette{
		items:  []paletteItem{{Name: "model", Available: true}},
		filter: "model",
	}
	if got := p.completion(); got != "" {
		t.Errorf("completion when full: got %q, want empty", got)
	}
}

// TestPaletteItem_InsertText pins the round-trip from selection to
// the literal that replaces the trigger token in the input.
func TestPaletteItem_InsertText(t *testing.T) {
	tests := []struct {
		item paletteItem
		kind paletteKind
		want string
	}{
		{paletteItem{Name: "help"}, paletteSlash, "/help"},
		{paletteItem{Name: "tui/foo.go"}, paletteFile, "@tui/foo.go"},
		{paletteItem{Name: "x", Insert: "/x --force"}, paletteSlash, "/x --force"},
	}
	for _, tc := range tests {
		if got := tc.item.insertText(tc.kind); got != tc.want {
			t.Errorf("insertText(%v): got %q, want %q", tc.item.Name, got, tc.want)
		}
	}
}

// TestLastAtTokenStart pins @-trigger detection at word boundaries
// (start-of-string or after whitespace), and rejects mid-word @s.
func TestLastAtTokenStart(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"", -1},
		{"@", 0},
		{"explain @file", 8},
		{"explain @file then @other", 19},
		{"user@host", -1}, // mid-word @ rejected
		{"\t@indented", 1},
		{"plain text", -1},
	}
	for _, tc := range tests {
		if got := lastAtTokenStart(tc.in); got != tc.want {
			t.Errorf("lastAtTokenStart(%q): got %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestAtFilterFrom pins that the filter text stops at the next
// whitespace, so the palette filter doesn't accidentally include
// subsequent words the user has already typed.
func TestAtFilterFrom(t *testing.T) {
	if got := atFilterFrom("explain @file go away", 8); got != "file" {
		t.Errorf("atFilterFrom: got %q, want %q", got, "file")
	}
	if got := atFilterFrom("@partial", 0); got != "partial" {
		t.Errorf("atFilterFrom no-trailing-space: got %q, want %q", got, "partial")
	}
}

// names returns the Name field of each item — convenience for test
// failure diagnostics.
func names(items []paletteItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Name
	}
	return out
}

// TestPalette_TriggerRuneMatchesKind is a sanity check that the
// trigger glyph round-trips with paletteKind.
func TestPalette_TriggerRuneMatchesKind(t *testing.T) {
	if (&palette{kind: paletteSlash}).triggerRune() != "/" {
		t.Errorf("slash palette triggerRune should be /")
	}
	if (&palette{kind: paletteFile}).triggerRune() != "@" {
		t.Errorf("file palette triggerRune should be @")
	}
}

// TestCommonPrefix is a quick sanity check on the helper used by
// completion(); covers the empty-match and full-match edge cases.
func TestCommonPrefix(t *testing.T) {
	tests := []struct{ a, b, want string }{
		{"abc", "abd", "ab"},
		{"abc", "xyz", ""},
		{"abc", "abc", "abc"},
		{"", "abc", ""},
		{"abc", "", ""},
		{"Memory", "memorial", "Memor"}, // case-insensitive compare, a's case preserved
	}
	for _, tc := range tests {
		if got := commonPrefix(tc.a, tc.b); !strings.EqualFold(got, tc.want) {
			t.Errorf("commonPrefix(%q, %q): got %q, want %q", tc.a, tc.b, got, tc.want)
		}
	}
}
