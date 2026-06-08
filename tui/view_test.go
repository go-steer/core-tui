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

	"github.com/charmbracelet/x/ansi"
)

// TestWordWrapIndent_FitsWithinWidth covers the /tools description
// overflow bug: wordWrapIndent must produce lines whose visible width
// is <= width, even when the source line has leading whitespace and
// later source lines get a role indent re-applied. The pre-fix
// implementation wrapped the prefixed line to width, then re-prepended
// the prefix on continuations, overflowing by len(prefix) cols.
func TestWordWrapIndent_FitsWithinWidth(t *testing.T) {
	long := "Spawn an in-process background subagent that runs in parallel with you. You provide its name, system prompt, goal, and the tools it may use."
	cases := []struct {
		name   string
		input  string
		width  int
		indent string
	}{
		{
			name:   "tools-description-with-6-space-leading-and-3-space-role-indent",
			input:  "header\n      " + long,
			width:  60,
			indent: "   ",
		},
		{
			name:   "system-message-no-leading",
			input:  "ℹ  " + long,
			width:  40,
			indent: "   ",
		},
		{
			name:   "narrow-terminal",
			input:  "      " + long,
			width:  30,
			indent: "   ",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := wordWrapIndent(tc.input, tc.width, tc.indent)
			for i, line := range strings.Split(got, "\n") {
				if w := ansi.StringWidth(line); w > tc.width {
					t.Errorf("line %d width %d > width %d: %q", i, w, tc.width, line)
				}
			}
		})
	}
}

// TestWordWrapIndent_PreservesContinuationIndent verifies that lines
// produced by wrapping a single source line keep the same leading
// indent as that source line, so /tools descriptions stay visually
// aligned under their tool name.
func TestWordWrapIndent_PreservesContinuationIndent(t *testing.T) {
	input := "      " + strings.Repeat("word ", 30)
	got := wordWrapIndent(input, 40, "   ")
	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected wrapping to produce multiple lines, got %d: %q", len(lines), got)
	}
	for i, line := range lines {
		if !strings.HasPrefix(line, "      ") {
			t.Errorf("line %d missing 6-space continuation indent: %q", i, line)
		}
	}
}

// TestWordWrapIndent_RoleIndentOnContinuationSourceLines verifies that
// source lines after the first get the role indent prepended (so
// multi-paragraph system messages keep their hanging indent).
func TestWordWrapIndent_RoleIndentOnContinuationSourceLines(t *testing.T) {
	got := wordWrapIndent("first\nsecond", 40, "   ")
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), got)
	}
	if lines[0] != "first" {
		t.Errorf("source line 0 should not get role indent, got %q", lines[0])
	}
	if lines[1] != "   second" {
		t.Errorf("source line 1 should get role indent, got %q", lines[1])
	}
}
