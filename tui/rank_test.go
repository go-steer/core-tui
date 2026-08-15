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

// ranked is the test's view of rankNames: names instead of indices,
// because an assertion about []int is unreadable in a failure message.
func ranked(names []string, filter string) []string {
	idx := rankNames(names, filter)
	out := make([]string, len(idx))
	for i, at := range idx {
		out[i] = names[at]
	}
	return out
}

// TestRankNames_Tiers walks the four tiers in order over one fixture,
// so the boundary between each pair is visible in a single table.
func TestRankNames_Tiers(t *testing.T) {
	names := []string{
		"lib/domain.go",       // 4: substring "main" inside "domain"
		"cmd/main/run.go",     // 3: whole path segment "main"
		"cmd/maintain/run.go", // 4: segment CONTAINS but does not EQUAL
		"main_test.go",        // 2: basename prefix
		"main",                // 1: exact basename
		"unrelated.go",        // no match at all
	}
	want := []string{
		"main",
		"main_test.go",
		"cmd/main/run.go",
		"lib/domain.go",
		"cmd/maintain/run.go",
	}
	assertNameOrder(t, ranked(names, "main"), want)
}

// TestRankNames_EmptyFilterIsIdentity pins that "" is not a rank of
// everything against the empty string but a pass-through — the
// pickers depend on it to render their unfiltered list in the order
// the host supplied.
func TestRankNames_EmptyFilterIsIdentity(t *testing.T) {
	names := []string{"zeta", "alpha", "Mu"}
	got := rankNames(names, "")
	if len(got) != len(names) {
		t.Fatalf("empty filter returned %d indices, want %d", len(got), len(names))
	}
	for i, at := range got {
		if at != i {
			t.Errorf("empty filter reordered: index %d is %d, want %d", i, at, i)
		}
	}
}

// TestRankNames_NoMatchesDropRows: a non-matching name is removed,
// not kept with a low score. The pickers' empty state depends on this
// returning length zero rather than the whole list.
func TestRankNames_NoMatchesDropRows(t *testing.T) {
	if got := ranked([]string{"alpha", "beta"}, "zzz"); len(got) != 0 {
		t.Errorf("rankNames with no matches returned %v, want none", got)
	}
	if got := rankNames(nil, "x"); len(got) != 0 {
		t.Errorf("rankNames over a nil slice returned %v, want none", got)
	}
}

// TestRankNames_CaseInsensitive covers folding on both sides.
func TestRankNames_CaseInsensitive(t *testing.T) {
	names := []string{"Claude-Opus", "gpt-4O", "Gemini"}
	for _, filter := range []string{"OPUS", "opus", "OpUs"} {
		got := ranked(names, filter)
		if len(got) != 1 || got[0] != "Claude-Opus" {
			t.Errorf("filter %q matched %v, want [Claude-Opus]", filter, got)
		}
	}
	if got := ranked(names, "4o"); len(got) != 1 || got[0] != "gpt-4O" {
		t.Errorf("filter %q matched %v, want [gpt-4O]", "4o", got)
	}
}

// TestRankNames_TiebreakIsTotal pins that names sharing a tier and a
// length order lexicographically, so the result is deterministic
// rather than dependent on the input order.
func TestRankNames_TiebreakIsTotal(t *testing.T) {
	forward := ranked([]string{"xa", "xb", "xc"}, "x")
	reverse := ranked([]string{"xc", "xb", "xa"}, "x")
	assertNameOrder(t, forward, []string{"xa", "xb", "xc"})
	assertNameOrder(t, reverse, []string{"xa", "xb", "xc"})
}

// TestRankNames_IsNotFuzzy is the documented negative: the ranker
// matches contiguous substrings only. If someone swaps in a
// subsequence matcher this fails, which is the point — the
// single-span highlight in the pickers assumes contiguity.
func TestRankNames_IsNotFuzzy(t *testing.T) {
	if got := ranked([]string{"main.go"}, "mgo"); len(got) != 0 {
		t.Errorf("subsequence filter %q matched %v; the ranker is substring-only", "mgo", got)
	}
}

// TestMatchSpan_HighlightsTheMatch checks the span the pickers paint,
// including the multi-byte case where a byte index into
// strings.ToLower(s) would not be a valid index into s.
func TestMatchSpan_HighlightsTheMatch(t *testing.T) {
	cases := []struct {
		name   string
		s      string
		filter string
		want   string // s[start:end]
		ok     bool
	}{
		{name: "head", s: "main.go", filter: "main", want: "main", ok: true},
		{name: "tail", s: "main.go", filter: ".go", want: ".go", ok: true},
		{name: "middle", s: "lib/domain.go", filter: "main", want: "main", ok: true},
		{name: "case-folded-keeps-original-case", s: "Claude-Opus", filter: "opus", want: "Opus", ok: true},
		{name: "first-occurrence-wins", s: "aXaXa", filter: "a", want: "a", ok: true},
		{name: "wide-runes-before", s: "日本語-model", filter: "model", want: "model", ok: true},
		{name: "wide-runes-matched", s: "モデル一覧", filter: "デル", want: "デル", ok: true},
		{name: "length-changing-fold", s: "İstanbul", filter: "stan", want: "stan", ok: true},
		{name: "no-match", s: "main.go", filter: "zzz", ok: false},
		{name: "empty-filter", s: "main.go", filter: "", ok: false},
		{name: "empty-name", s: "", filter: "a", ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, end, ok := matchSpan(tc.s, tc.filter)
			if ok != tc.ok {
				t.Fatalf("matchSpan(%q, %q) ok = %v, want %v", tc.s, tc.filter, ok, tc.ok)
			}
			if !ok {
				return
			}
			if start < 0 || end > len(tc.s) || start > end {
				t.Fatalf("matchSpan(%q, %q) = [%d,%d), which is not a valid range of a %d-byte string",
					tc.s, tc.filter, start, end, len(tc.s))
			}
			if got := tc.s[start:end]; got != tc.want {
				t.Errorf("matchSpan(%q, %q) spans %q, want %q", tc.s, tc.filter, got, tc.want)
			}
		})
	}
}

// TestMatchSpan_AgreesWithRankNames is the invariant the highlight
// depends on: every name rankNames keeps has a paintable span. A tier
// that matched on something other than a substring would break the
// renderer, not just the highlight.
func TestMatchSpan_AgreesWithRankNames(t *testing.T) {
	names := []string{
		"main", "main.go", "main_test.go", "cmd/main/run.go",
		"lib/domain.go", "docs/", "README.md", "Claude-Opus",
		"日本語-model", "gpt-4O",
	}
	for _, filter := range []string{"main", "go", "d", "o", "MODEL", "opus", "/"} {
		for _, at := range rankNames(names, filter) {
			name := names[at]
			start, end, ok := matchSpan(name, filter)
			if !ok {
				t.Errorf("rankNames kept %q for filter %q but matchSpan found nothing to highlight",
					name, filter)
				continue
			}
			if !strings.EqualFold(name[start:end], filter) {
				t.Errorf("matchSpan(%q, %q) spans %q, which is not the filter",
					name, filter, name[start:end])
			}
		}
	}
}
