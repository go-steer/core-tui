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

// Benchmarks for the label -> lexer-name lookup (issue #106).
// detectLang runs on every tool event that carries a filename, and
// the uncached form is a lexers.Match glob sweep — hundreds of
// filepath.Match calls per lookup, by Chroma's own doc comment.
//
// The Hit / Miss split is the point of the measurement. A hit
// ("main.go") short-circuits partway through Chroma's matcher; a
// miss ("README") is the worst case, because Match only knows there
// is no lexer after it has tried every glob — and "" is the common
// answer for the plain-text paths a session touches. Both are
// benchmarked against the uncached function so the cache's effect on
// each is visible separately.
//
//	go test ./tui -run '^$' -bench DetectLang -benchmem

package tui

import "testing"

// benchDetectLangLabels are realistic tool-event paths: a hit, a
// miss, and a repeat of each, so the mixed benchmark measures the
// per-event cost of a small working set rather than one label.
var benchDetectLangLabels = []string{
	"tui/syntax_cache.go",
	"README",
	"docs/design.md",
	"LICENSE",
	"tui/model.go",
	"Makefile",
}

// BenchmarkDetectLangHit measures a label that resolves to a lexer.
func BenchmarkDetectLangHit(b *testing.B) {
	langCache.Clear()
	b.ReportAllocs()
	for b.Loop() {
		if got := detectLang("tui/syntax_cache.go"); got != "Go" {
			b.Fatalf("lang = %q, want Go", got)
		}
	}
}

// BenchmarkDetectLangMiss measures a label with no lexer — the case
// the cache exists for, since Match must exhaust every glob before
// it can return nil.
func BenchmarkDetectLangMiss(b *testing.B) {
	langCache.Clear()
	b.ReportAllocs()
	for b.Loop() {
		if got := detectLang("README"); got != "" {
			b.Fatalf("lang = %q, want empty", got)
		}
	}
}

// BenchmarkDetectLangMixed sweeps a small working set of hits and
// misses, which is what a real session's tool stream looks like.
func BenchmarkDetectLangMixed(b *testing.B) {
	langCache.Clear()
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		detectLang(benchDetectLangLabels[i%len(benchDetectLangLabels)])
		i++
	}
}

// BenchmarkDetectLangUncachedHit / Miss are the controls: the same
// two labels through the raw Chroma matcher, so the speedup the
// cache buys is a ratio between two numbers in one benchmark run
// rather than a comparison against a deleted revision.
func BenchmarkDetectLangUncachedHit(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		detectLangUncached("tui/syntax_cache.go")
	}
}

func BenchmarkDetectLangUncachedMiss(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		detectLangUncached("README")
	}
}
