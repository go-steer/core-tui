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

// Custom Chroma formatter that delegates token coloring to Lipgloss
// (agentic-tui skill §11.B). Chroma's built-in terminal formatters
// emit raw ANSI true-color sequences regardless of the active
// Lipgloss palette + color profile — code fences end up clashing
// with the brand theme. The LipglossFormatter renders each token
// through `lipgloss.NewStyle().Foreground(...)` so syntax
// highlighting respects color-profile downgrade + future
// per-provider themes.

package tui

import (
	"image/color"
	"io"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"

	"charm.land/lipgloss/v2"
)

// chromaFormatterName is the key glamour reads from when
// constructing a TermRenderer via WithChromaFormatter. Set at
// package init so the renderer can find it.
const chromaFormatterName = "tui-lipgloss"

// LipglossFormatter returns a chroma.Formatter that emits tokens
// through Lipgloss. bg is applied as the background of each
// styled token so the code-fence background reads as a single
// uniform surface even when individual tokens override fg/style.
// Pass `nil` (or `color.Color(nil)`) when no background tint is
// wanted — Lipgloss simply skips the Background call.
func LipglossFormatter(bg color.Color) chroma.Formatter {
	return chroma.FormatterFunc(func(w io.Writer, style *chroma.Style, it chroma.Iterator) error {
		for token := it(); token != chroma.EOF; token = it() {
			entry := style.Get(token.Type)
			if entry.IsZero() {
				if _, err := io.WriteString(w, token.Value); err != nil {
					return err
				}
				continue
			}
			s := lipgloss.NewStyle()
			if bg != nil {
				s = s.Background(bg)
			}
			if entry.Bold == chroma.Yes {
				s = s.Bold(true)
			}
			if entry.Italic == chroma.Yes {
				s = s.Italic(true)
			}
			if entry.Underline == chroma.Yes {
				s = s.Underline(true)
			}
			if entry.Colour.IsSet() {
				s = s.Foreground(lipgloss.Color(entry.Colour.String()))
			}
			if _, err := io.WriteString(w, s.Render(token.Value)); err != nil {
				return err
			}
		}
		return nil
	})
}

// Register the formatter under chromaFormatterName at package init
// so glamour.WithChromaFormatter(chromaFormatterName) resolves it.
// nil background is fine here — the registered instance is the
// "default" formatter used everywhere; per-render background tints
// would require a separate construction path (currently unused).
func init() {
	formatters.Register(chromaFormatterName, LipglossFormatter(nil))
}
