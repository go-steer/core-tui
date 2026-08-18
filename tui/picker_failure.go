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

// The async pickers' inline failure row (issue #245).
//
// The model and session pickers both start a host call on Enter, and
// both keep the list up when that call comes back with an error. Keeping
// it up is deliberate and right — the operator's next move after
// "provider unreachable" is almost always the next row down, and closing
// would make them re-run the command to get back to a list they were
// already looking at. What they did not have is anywhere to say WHY. The
// reason went to the transcript, which is BEHIND the modal, so from the
// operator's seat a failed switch was Enter, a pause, and the same list:
// indistinguishable from a keystroke that got dropped.
//
// The transcript row stays. It is the durable record, and it is the only
// channel `/model <id>` and `/switch <id>` have — those reach the same
// reply handler with no modal in the way, and there the row is perfectly
// visible. This is a second copy on the surface the operator is actually
// looking at, not a replacement for the first.
//
// Modelled on the text-input dialog's inline validation error
// (dialog_textinput.go), which is the same shape one layer down: the
// reason renders under the thing that produced it, in ErrorText with the
// warn glyph, and clears as soon as the operator moves.

package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// pickerFailure is the reason row an async picker shows under its list.
//
// Shared by the two pickers rather than written twice. The three rules
// that make it correct — sanitize on the way in, cost the body a row
// while it is up, clear it when the operator moves — are each one line,
// and two independent one-line copies of a rule is how the two pickers
// drift apart on the rule nobody re-read.
//
// The zero value is "no failure", which is the state a picker spends
// nearly all of its life in.
type pickerFailure struct{ msg string }

// set records why the host call failed.
//
// reason is host-derived text — an err.Error() this package has no
// control over — so it goes through the sanitizeLine funnel, which is
// where every single row of untrusted content in the package is
// bounded and escaped. Newlines are flattened FIRST because LF is the
// one control sanitizeLine deliberately passes through: it is the line
// separator for multi-line bodies, and this row is budgeted as exactly
// one. An error containing a newline would otherwise paint two rows
// into a one-row hole and push the footer off the bottom edge.
func (f *pickerFailure) set(reason string) {
	f.msg = sanitizeLine(strings.ReplaceAll(reason, "\n", " "))
}

// clear drops the row. Called whenever the operator moves the cursor,
// edits the filter, or commits a fresh Enter, so the reason on screen
// always belongs to the list the operator is looking at now.
func (f *pickerFailure) clear() { f.msg = "" }

// rows is what this costs the body's height budget: one row while
// there is a reason to show, none otherwise. Picker call sites add it
// to their chrome so the list window gives the row up, rather than the
// modal quietly growing past the height it was composed against — the
// same accounting the filter row's extra row gets (issue #117).
func (f *pickerFailure) rows() int {
	if f.msg == "" {
		return 0
	}
	return 1
}

// appendTo puts the reason on as the LAST body row and joins the
// result.
//
// It is the pickers' single exit from Body, which is the point: Body
// returns from several places once a filter has matched nothing, and a
// row appended at only some of them is a row that vanishes exactly when
// the operator has narrowed the list looking for something that works.
//
// width is the DIALOG width, and the row is measured against
// modalInnerWidth rather than modalBodyWidth: it sits outside the
// windowed region, so no scrollbar column is glued to its right and
// reserving one would leave it two columns short of the filter row
// above it.
//
// Truncation happens on the plain string BEFORE styling. Cutting the
// styled one could slice a colour on and never off, and unlike a
// windowed row nothing downstream closes it — scrollView's closeSGR
// never sees this row.
func (f *pickerFailure) appendTo(lines []string, width int, st styleSet) string {
	if f.msg != "" {
		row := ansi.Truncate(GlyphWarn+" "+f.msg, modalInnerWidth(width), GlyphTruncate)
		lines = append(lines, st.ErrorText.Render(row))
	}
	return strings.Join(lines, "\n")
}
