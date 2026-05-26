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

// Textarea style overrides. bubbles v2's textarea.New() hard-codes
// DefaultDarkStyles, which paints the focused CursorLine with a
// solid background color tuned for dark terminals. On light
// terminals that color is "0" (black), which reads as a screaming
// horizontal black block under the cursor row — broken UX.
//
// textareaStyles returns a Styles bundle derived from the
// bubbles default for the correct dark/light variant AND zeroes
// out the CursorLine + CursorLineNumber backgrounds + line-number
// styling that don't fit a chat prompter. Operators want a clean
// prompt box, not a code-editor cursor highlight.

package tui

import (
	"charm.land/bubbles/v2/textarea"
	"charm.land/lipgloss/v2"
)

// textareaStyles returns the styles bundle for the chat prompter.
// Starts from textarea.DefaultStyles(isDark) so the per-mode
// foreground / placeholder palette is correct, then clears the
// CursorLine background so the focused row doesn't paint a tinted
// block under the prompt.
func textareaStyles(isDark bool) textarea.Styles {
	s := textarea.DefaultStyles(isDark)
	// Both Focused and Blurred states: drop the cursor-line tint
	// inherited from the code-editor defaults. The chat prompter
	// is a single conceptual line; highlighting the cursor row
	// reads as visual noise (and is broken on light terminals).
	s.Focused.CursorLine = lipgloss.NewStyle()
	s.Focused.CursorLineNumber = lipgloss.NewStyle()
	s.Blurred.CursorLine = lipgloss.NewStyle()
	s.Blurred.CursorLineNumber = lipgloss.NewStyle()
	return s
}
