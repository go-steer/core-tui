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

// Per-line Chroma highlight cache (agentic-tui skill §13). The
// inline tool-display surface re-renders the same diff line on
// every scroll / resize; without a cache, Chroma's tokenize +
// format would burn 10-50ms per redraw on a long preview block.
//
// Phase 2 keeps the cache simple: a sync.Map keyed by
// `style \x00 lang \x00 bg \x00 line`. Diff content is short-lived
// and bounded by previewLineCap (8 lines per preview today), so we
// don't worry about eviction yet — the working set is small.
//
// The style component of that key is load-bearing and is why the
// cache survives /theme. The map is never evicted, so every input
// that can change the bytes has to be in the key; the Chroma style
// used to be a package-level var that could not change at runtime,
// and the moment it became theme-derived (Theme.ChromaStyleName) a
// theme swap would otherwise have replayed the previous palette's
// bytes out of the cache forever.

package tui

import (
	"bytes"
	"fmt"
	"image/color"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// syntaxCache memoizes highlighted output per
// (style, lang, bg, line), so the same source line does not
// collide across Chroma styles, across languages (e.g. "if x:"
// reads differently as Python vs Cucumber), or across diff
// backgrounds.
var syntaxCache sync.Map

// lexerCache memoizes the resolved + Coalesced chroma.Lexer per
// language name so highlightLineUncached skips lexers.Get +
// chroma.Coalesce on every call. Misses (unknown lang) are not
// cached — they're pathological and re-resolution is cheap.
var lexerCache sync.Map

// langCache memoizes detectLang's resolved language name per label
// so repeat tool events for the same path skip lexers.Match, whose
// own doc comment warns it runs hundreds of filepath.Match calls per
// lookup. Unlike lexerCache below, misses ARE cached: "" is the
// ordinary answer for the paths a session touches most (README,
// LICENSE, Makefile, plain text, extensionless binaries), so
// skipping the negative result would leave the hot half of the path
// uncached. detectLang is a pure function of its input, so an entry
// never needs invalidating.
var langCache sync.Map

// chromaStyleByName resolves a Theme.ChromaStyleName to a Chroma
// style. Empty answers defaultChromaStyleName; an unknown name
// falls through styles.Get, which returns Chroma's own fallback
// rather than nil — a bad name in a host theme degrades to plain
// highlighting, it never panics the render.
func chromaStyleByName(name string) *chroma.Style {
	if name == "" {
		name = defaultChromaStyleName
	}
	return styles.Get(name)
}

// detectLang maps a file path / label to a Chroma lexer name,
// returning "" when no lexer matches. Lipgloss-friendly output
// stays stable as long as the same name maps to the same lexer,
// so we return the canonical Lexer.Config().Name (e.g. "Go",
// "Python") rather than the raw extension.
//
// Every result — including the empty-string miss — rides langCache,
// so the lexers.Match glob sweep is paid once per distinct label
// per process rather than once per tool event.
func detectLang(label string) string {
	if label == "" {
		return ""
	}
	if v, ok := langCache.Load(label); ok {
		return v.(string)
	}
	lang := detectLangUncached(label)
	langCache.Store(label, lang)
	return lang
}

// detectLangUncached does the actual Chroma lexer match.
func detectLangUncached(label string) string {
	l := lexers.Match(label)
	if l == nil {
		return ""
	}
	return l.Config().Name
}

// highlightLine returns the syntax-highlighted form of line for
// the given language, or line unchanged when lang is empty / the
// lexer isn't found / highlighting errors. When bg is non-nil,
// every token carries it as a background through the Lipgloss
// formatter so adjacent tokens render as one continuous tinted
// strip (used for + / - lines in inline diffs). Caches every
// successful render so subsequent calls with the same
// (styleName, lang, bg, line) are a single map lookup.
//
// styleName is the active Theme.ChromaStyleName, threaded down from
// the Styles the caller already holds. Both call sites
// (highlightOrFlat in diff.go, renderCodeInline in
// tool_preview_result.go) have a Styles in scope, so this stays
// parameter passing rather than a package-level atomic that the
// cache would then have to chase.
func highlightLine(line, lang, styleName string, bg color.Color) string {
	if lang == "" || line == "" {
		return line
	}
	key := styleName + "\x00" + lang + "\x00" + bgKey(bg) + "\x00" + line
	if v, ok := syntaxCache.Load(key); ok {
		return v.(string)
	}
	out := highlightLineUncached(line, lang, styleName, bg)
	syntaxCache.Store(key, out)
	return out
}

// bgKey produces a stable cache-key fragment for a background
// color. Lipgloss colors stringify to their hex form, so different
// theme bgs (dark vs light, add vs del) bucket separately without
// extra type assertions.
func bgKey(bg color.Color) string {
	if bg == nil {
		return ""
	}
	return fmt.Sprintf("%v", bg)
}

// highlightLineUncached does the actual Chroma tokenize + format.
// The lexer resolution + Coalesce wrap rides the lexerCache so
// only the FIRST line for a given language pays for them; later
// lines reuse the cached lexer. lipglossFormatter routes coloring
// through the lipgloss color profile so 256-color / truecolor /
// no-color terminals all get appropriate output.
func highlightLineUncached(line, lang, styleName string, bg color.Color) string {
	lexer := getLexer(lang)
	if lexer == nil {
		return line
	}
	it, err := lexer.Tokenise(nil, line)
	if err != nil {
		return line
	}
	var buf bytes.Buffer
	if err := lipglossFormatter(bg).Format(&buf, chromaStyleByName(styleName), it); err != nil {
		return line
	}
	// Chroma's tokenizer sometimes appends a trailing newline from
	// the input line itself. Strip so callers get exactly one line.
	return strings.TrimRight(buf.String(), "\n")
}

// getLexer returns the Coalesced Chroma lexer for lang, memoizing
// the result so subsequent calls skip lexers.Get + chroma.Coalesce
// entirely. Returns nil for unknown languages; nil results are
// NOT cached (re-resolution is cheap and lets future Chroma
// updates pick up newly added lexers without process restart).
func getLexer(lang string) chroma.Lexer {
	if v, ok := lexerCache.Load(lang); ok {
		return v.(chroma.Lexer)
	}
	l := lexers.Get(lang)
	if l == nil {
		return nil
	}
	l = chroma.Coalesce(l)
	lexerCache.Store(lang, l)
	return l
}
