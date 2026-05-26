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
// `lang \x00 line`. Diff content is short-lived and bounded by
// previewLineCap (8 lines per preview today), so we don't worry
// about eviction yet — the working set is small.

package tui

import (
	"bytes"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// syntaxCache memoizes highlighted output per (lang, line). Keyed
// on lang+"\x00"+line so the same source line in different files
// of different languages doesn't collide (e.g. "if x:" reads
// differently as Python vs Cucumber).
var syntaxCache sync.Map

// chromaSyntaxStyle is the Chroma color theme used for inline
// highlighting. github-light reads well on both light and dark
// terminals — the foreground-only colors don't depend on the
// terminal background to be legible.
var chromaSyntaxStyle = styles.Get("github")

// detectLang maps a file path / label to a Chroma lexer name,
// returning "" when no lexer matches. Lipgloss-friendly output
// stays stable as long as the same name maps to the same lexer,
// so we return the canonical Lexer.Config().Name (e.g. "Go",
// "Python") rather than the raw extension.
func detectLang(label string) string {
	if label == "" {
		return ""
	}
	l := lexers.Match(label)
	if l == nil {
		return ""
	}
	return l.Config().Name
}

// highlightLine returns the syntax-highlighted form of line for
// the given language, or line unchanged when lang is empty / the
// lexer isn't found / highlighting errors. Caches every successful
// render so subsequent calls with the same (lang, line) are a
// single map lookup.
func highlightLine(line, lang string) string {
	if lang == "" || line == "" {
		return line
	}
	key := lang + "\x00" + line
	if v, ok := syntaxCache.Load(key); ok {
		return v.(string)
	}
	out := highlightLineUncached(line, lang)
	syntaxCache.Store(key, out)
	return out
}

// highlightLineUncached does the actual Chroma tokenize + format.
// Coalesce merges adjacent same-type tokens so the output is
// shorter (fewer ANSI runs); LipglossFormatter routes coloring
// through the lipgloss color profile so 256-color / truecolor /
// no-color terminals all get appropriate output.
func highlightLineUncached(line, lang string) string {
	lexer := lexers.Get(lang)
	if lexer == nil {
		return line
	}
	lexer = chroma.Coalesce(lexer)
	it, err := lexer.Tokenise(nil, line)
	if err != nil {
		return line
	}
	var buf bytes.Buffer
	if err := LipglossFormatter(nil).Format(&buf, chromaSyntaxStyle, it); err != nil {
		return line
	}
	// Chroma's tokenizer sometimes appends a trailing newline from
	// the input line itself. Strip so callers get exactly one line.
	return strings.TrimRight(buf.String(), "\n")
}
