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

// The shared name ranker (issue #117).
//
// This is the slash palette's 4-tier classifier, lifted verbatim out
// of palette.filtered so the model / session / theme pickers can use
// the same one. It was already the only ranking taste in the repo and
// the alternative was a ninth direct dependency (sahilm/fuzzy) for a
// list of forty models — see docs/requirements.md N-DEPS.
//
// It is NOT fuzzy matching. Every tier is a contiguous, case-folded
// SUBSTRING test; a subsequence like "mgo" does not match "main.go".
// That has one visible consequence: a match highlight is a single
// span (matchSpan below), never a scatter of individual runes.
// Evaluating real fuzzy matching for the palette and the pickers
// together is a follow-up, not something to half-implement here.
//
// The shape is "indices in rank order" rather than "the matching
// items" because every caller maps back to a different source slice
// — []paletteItem, []ModelInfo, []SessionInfo, []BuiltinTheme — and
// a generic over four element types buys nothing a []int does not.

package tui

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// rankNames returns the indices of names that match filter, ordered
// best-first across four tiers:
//
//  1. exact basename match       ("main" → "main")
//  2. basename prefix match      ("main" → "main_test.go")
//  3. path-segment exact match   ("main" → "cmd/main/run.go")
//  4. substring anywhere         ("main" → "lib/domain.go")
//
// The basename is the text after the final '/'; names with no '/'
// (slash commands, model IDs, theme names) are their own basename.
// Ties break on shorter name first, then lexicographically, so the
// order is total and stable across runs.
//
// An empty filter is identity: every index in source order, NOT a
// rank of everything against "". Names that match no tier are
// dropped rather than scored to zero — a picker showing forty
// non-matching rows below the two that match is the thing the filter
// exists to prevent.
//
// Matching is case-insensitive.
func rankNames(names []string, filter string) []int {
	out := make([]int, 0, len(names))
	if filter == "" {
		for i := range names {
			out = append(out, i)
		}
		return out
	}
	q := strings.ToLower(filter)

	type ranked struct {
		idx  int
		tier int
		path string // lowercased name, for the tiebreak
	}
	rs := make([]ranked, 0, len(names))
	for i, raw := range names {
		name := strings.ToLower(raw)
		// Treat the last path segment (after the final '/') as the
		// basename. Slash commands have no '/' so the basename is
		// the whole name.
		base := name
		if j := strings.LastIndex(name, "/"); j >= 0 {
			base = name[j+1:]
		}
		switch {
		case base == q:
			rs = append(rs, ranked{i, 1, name})
		case strings.HasPrefix(base, q):
			rs = append(rs, ranked{i, 2, name})
		case segmentEquals(name, q):
			rs = append(rs, ranked{i, 3, name})
		case strings.Contains(name, q):
			rs = append(rs, ranked{i, 4, name})
		}
	}
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].tier != rs[j].tier {
			return rs[i].tier < rs[j].tier
		}
		// Tiebreak: shorter path wins (closer to repo root /
		// fewer typed chars to confirm).
		if len(rs[i].path) != len(rs[j].path) {
			return len(rs[i].path) < len(rs[j].path)
		}
		return rs[i].path < rs[j].path
	})
	for _, r := range rs {
		out = append(out, r.idx)
	}
	return out
}

// segmentEquals reports whether q appears as a full
// slash-delimited segment anywhere in path. "main" matches
// "cmd/main/run.go" (the middle segment) but NOT
// "cmd/maintain/run.go" (segment "maintain" contains but doesn't
// equal q).
func segmentEquals(path, q string) bool {
	for _, seg := range strings.Split(path, "/") {
		if seg == q {
			return true
		}
	}
	return false
}

// matchSpan returns the byte range of s that filter matched, for
// highlighting. ok is false when filter is empty or does not occur in
// s at all.
//
// Every tier rankNames assigns implies a contiguous substring match:
// an exact basename, a basename prefix and a whole path segment are
// each a substring of the full name, so the first case-folded
// occurrence is the span to paint. One span, not a scatter — see the
// file comment.
//
// Case folding is done rune by rune, exactly as strings.ToLower does
// it, while carrying a map from folded byte offsets back to original
// ones. That is what makes the returned range safe to slice s with:
// unicode.ToLower can change a rune's encoded LENGTH (U+0130 'İ' is
// three bytes and folds to a one-byte 'i'), so an index into
// strings.ToLower(s) is not in general an index into s.
func matchSpan(s, filter string) (start, end int, ok bool) {
	if filter == "" || s == "" {
		return 0, 0, false
	}
	var folded strings.Builder
	folded.Grow(len(s))
	// origAt[j] is the byte offset in s that folded byte offset j came
	// from. Every byte of a folded rune maps to that rune's start, and
	// the final entry maps one past the end, so both ends of a match
	// resolve whether or not they land on a rune boundary.
	origAt := make([]int, 0, len(s)+1)
	for i, r := range s {
		lower := unicode.ToLower(r)
		n := utf8.RuneLen(lower)
		if n < 0 {
			n = 1
		}
		for k := 0; k < n; k++ {
			origAt = append(origAt, i)
		}
		folded.WriteRune(lower)
	}
	origAt = append(origAt, len(s))

	f := folded.String()
	q := strings.ToLower(filter)
	j := strings.Index(f, q)
	if j < 0 {
		return 0, 0, false
	}
	// Defensive: the two lengths agree by construction, but a mismatch
	// would index out of range rather than merely mis-highlight.
	if j >= len(origAt) || j+len(q) >= len(origAt) {
		return 0, 0, false
	}
	return origAt[j], origAt[j+len(q)], true
}
