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

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/styles"
)

// markdownRenderer wraps a Glamour TermRenderer with the parameters
// the TUI tracks — dark/light background and viewport width. Held by
// Model and lazily rebuilt when either changes.
//
// R-CHAT-4 / R-MD-3: assistant text is rendered through Glamour on
// every update (including mid-stream partials). When a render fails
// — typically because the accumulated stream ends mid-code-fence —
// renderMarkdown falls back to the raw text for that frame so the
// chunk isn't dropped.
type markdownRenderer struct {
	r     *glamour.TermRenderer
	dark  bool
	width int
}

// newMarkdownRenderer builds a Glamour renderer with the project's
// chosen style + a soft word-wrap at width. Returns a no-op renderer
// on construction error so callers don't need to handle nil — any
// markdown they pass to renderMarkdown will fall through to raw text.
func newMarkdownRenderer(dark bool, width int) *markdownRenderer {
	cfg := styles.DraculaStyleConfig
	if !dark {
		// Light-mode fallback. Glamour ships an ASCII style that
		// reads cleanly on light backgrounds; richer light themes
		// can land later via Options.MarkdownStyle (R-MD-4).
		cfg = styles.ASCIIStyleConfig
	}
	r, _ := glamour.NewTermRenderer(
		glamour.WithStyles(cfg),
		glamour.WithWordWrap(width),
	)
	return &markdownRenderer{r: r, dark: dark, width: width}
}

// renderMarkdown returns the Glamour-rendered form of text, or text
// itself when Glamour returns an error (R-MD-3 fallback). Trims one
// trailing newline because Glamour adds one consistently and we
// already manage spacing via the per-turn rule.
func (mr *markdownRenderer) renderMarkdown(text string) string {
	if mr == nil || mr.r == nil || text == "" {
		return text
	}
	out, err := mr.r.Render(text)
	if err != nil {
		return text
	}
	return strings.TrimRight(out, "\n")
}
