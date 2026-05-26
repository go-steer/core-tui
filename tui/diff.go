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

// Unified-diff rendering for the inline tool-display surface
// (docs/inline-tool-display-design.md §3). Phase 1: parse +
// render with plain Success/Error/Muted colors; syntax
// highlighting + per-line cache land in Phase 2.

package tui

import (
	"strings"

	udiff "github.com/aymanbagabas/go-udiff"

	"charm.land/lipgloss/v2"
)

// computeUnifiedDiff returns a unified-diff string between old and
// new content with the given label as both from/to filename. Used
// by edit_file / replace tools whose args carry old_text + new_text
// but not the pre-computed diff.
//
// `contextLines` controls how many surrounding lines of context
// each hunk includes; 3 is the conventional default that balances
// readability against verbosity.
func computeUnifiedDiff(label, oldText, newText string) string {
	if oldText == newText {
		return ""
	}
	return udiff.Unified(label, label, oldText, newText)
}

// renderDiffInline styles a unified-diff string for inline display
// under a tool row. Adds Success-colored `+` lines, Error-colored
// `-` lines, Muted hunk headers, and a 4-space left indent on
// every line so the block visually attaches to the tool name
// above it.
//
// `maxLines` caps the rendered output; lines beyond the cap are
// dropped and replaced with a "… +N more" marker. Pass 0 for
// no cap.
func renderDiffInline(diff string, styles Styles, maxLines int) string {
	if diff == "" {
		return ""
	}
	addStyle := lipgloss.NewStyle().Foreground(styles.Theme.Success)
	delStyle := lipgloss.NewStyle().Foreground(styles.Theme.Error)
	hunkStyle := lipgloss.NewStyle().Foreground(styles.Theme.FgMuted).Italic(true)
	ctxStyle := lipgloss.NewStyle().Foreground(styles.Theme.FgMuted)

	const indent = "    "
	lines := strings.Split(strings.TrimRight(diff, "\n"), "\n")
	out := make([]string, 0, len(lines)+1)
	truncatedAt := -1

	for i, line := range lines {
		// Skip the "--- old\n+++ new" file headers — they
		// duplicate the tool's path arg and read as visual
		// noise inline.
		if strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
			continue
		}
		if maxLines > 0 && len(out) >= maxLines {
			truncatedAt = i
			break
		}
		switch {
		case strings.HasPrefix(line, "@@"):
			out = append(out, indent+hunkStyle.Render(line))
		case strings.HasPrefix(line, "+"):
			out = append(out, indent+addStyle.Render(line))
		case strings.HasPrefix(line, "-"):
			out = append(out, indent+delStyle.Render(line))
		default:
			out = append(out, indent+ctxStyle.Render(line))
		}
	}
	if truncatedAt > 0 {
		remaining := len(lines) - truncatedAt
		out = append(out, indent+styles.Muted.Render("… +"+itoa(remaining)+" more lines · ctrl+o to expand (todo)"))
	}
	return strings.Join(out, "\n")
}

// itoa is a tiny non-strconv helper so this file doesn't pull
// strconv just for one int format. ASCII-only, non-negative
// integers (line counts).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		return "0" // line counts are never negative in practice
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
