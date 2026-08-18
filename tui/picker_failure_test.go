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

// The failure row's own tests (issue #245). What the pickers assert is
// that the row reaches the frame and is paid for; what is here is that
// an arbitrary host err.Error() cannot turn one budgeted row into
// something else.

package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestPickerFailure_IsAlwaysExactlyOneRow. The row's cost is declared
// to the height budget before it is rendered, so a reason that paints
// two rows overflows a modal that was composed for one — and the row
// clipFrame then takes off the bottom is the footer.
//
// A newline in an error string is not hypothetical: wrapped errors and
// anything carrying a stack, a YAML fragment or a multi-line HTTP body
// all produce one.
func TestPickerFailure_IsAlwaysExactlyOneRow(t *testing.T) {
	cases := []struct {
		name   string
		reason string
	}{
		{"a wrapped error with a newline", "dial failed:\n  connection refused"},
		{"a run of them", "one\n\n\ntwo"},
		{"a carriage return pair", "one\r\ntwo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var f pickerFailure
			f.set(tc.reason)
			if f.rows() != 1 {
				t.Fatalf("rows() = %d, want 1", f.rows())
			}
			row := f.appendTo(nil, 64, styleSet{})
			if n := strings.Count(row, "\n"); n != 0 {
				t.Errorf("the reason rendered %d rows:\n%s", n+1, row)
			}
		})
	}
}

// TestPickerFailure_StripsTerminalEscapes. The reason comes from a host
// the TUI does not control, and it is drawn straight into the frame
// rather than into the transcript, which has its own funnel. An error
// carrying CSI would otherwise clear the operator's screen or retitle
// their window from inside a modal.
func TestPickerFailure_StripsTerminalEscapes(t *testing.T) {
	var f pickerFailure
	f.set("boom \x1b[2J\x1b]0;pwned\x07 and \x07done")

	row := f.appendTo(nil, 64, styleSet{})
	if strings.ContainsRune(row, 0x1b) {
		t.Errorf("an escape reached the frame: %q", row)
	}
	if !strings.Contains(row, "boom") || !strings.Contains(row, "done") {
		t.Errorf("the readable part of the reason was lost: %q", row)
	}
}

// TestPickerFailure_FitsTheModalWidth. Nothing downstream bounds this
// row — it is appended after scrollView, so fitRow never sees it — and
// a host error is easily longer than the picker is wide.
func TestPickerFailure_FitsTheModalWidth(t *testing.T) {
	const width = 64
	var f pickerFailure
	f.set(strings.Repeat("unreachable ", 30))

	row := f.appendTo(nil, width, styleSet{})
	if got, want := ansi.StringWidth(row), modalInnerWidth(width); got > want {
		t.Errorf("the reason row is %d cells wide, over the modal's %d:\n%s", got, want, row)
	}
	if !strings.HasSuffix(row, GlyphTruncate) {
		t.Errorf("a truncated row does not say it was truncated: %q", row)
	}
}

// TestPickerFailure_ZeroValueAddsNothing — the state a picker is in for
// almost all of its life. appendTo is on both of the pickers' exits
// from Body, so "no failure" has to be free of both a row and a cost.
func TestPickerFailure_ZeroValueAddsNothing(t *testing.T) {
	var f pickerFailure
	if f.rows() != 0 {
		t.Errorf("rows() = %d on the zero value, want 0", f.rows())
	}
	if got := f.appendTo([]string{"a", "b"}, 64, styleSet{}); got != "a\nb" {
		t.Errorf("appendTo = %q, want the lines it was handed", got)
	}

	f.set("boom")
	f.clear()
	if f.rows() != 0 {
		t.Error("clear did not put it back")
	}
}
