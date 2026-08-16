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

// Getting text back out of the transcript (issue #153).
//
// The only way out used to be the terminal's own mouse selection,
// which selects what is on the SCREEN: the two-column selection
// gutter (#152), the tool glyphs, the box rules, the soft line breaks
// word wrap inserted, and every ANSI escape in between. Paste that
// into an editor and it is not the text the operator pointed at.
//
// # Copy the source, not the frame
//
// So this path never reads the frame. It reads the history entry
// under the cursor and reconstructs the text the renderer was GIVEN:
// the markdown source of an assistant turn, the prompt as it was
// typed, the structured args and response of a tool call. No ANSI, no
// gutter, no wrap-inserted newlines — which is also why a copy is
// unaffected by how wide the terminal happened to be, and why the
// same keystroke gives the same bytes at 60 columns and at 200.
//
// The one rendered fallback is a tool row whose host reported neither
// a structured response nor an error: all that exists for it is the
// pre-rendered preview, so that gets stripped of ANSI and copied. It
// is the only place in this file where wrap could reach the
// clipboard, and renderToolPreview does not wrap.
//
// # OSC 52, and its one caveat
//
// The write goes out as OSC 52 (tea.SetClipboard), which is the only
// mechanism that reaches the clipboard of the machine the operator is
// SITTING at rather than the one the process runs on. That matters
// here more than it does in most TUIs: a LiveAgent host is routinely
// on the far side of an SSH connection, and a native clipboard call
// would have put the text on the wrong computer. The caveat is that
// terminals cap the escape they will accept — 8KB in some, 100KB in
// others, and a few refuse it entirely for security — so a very long
// item can be reported as copied by us and dropped by the terminal.
// There is no acknowledgement in the protocol to detect that with,
// and truncating to the smallest known cap would silently corrupt the
// common case to protect the rare one, so this copies the whole thing
// and says how much it sent.
//
// # The notice clears itself on the next key
//
// A copy makes no visible change — that is the nature of it — so it
// has to say so, and the footer legend says it (footerHint). No timer
// is involved: the notice is cleared by the next transcript keystroke
// and by leaving focus mode. A timed toast would need a tick, a
// generation stamp to keep two of them from fighting, and a repaint
// the operator did not ask for; "it says copied until you do the next
// thing" needs none of that and is never wrong about which copy it is
// describing.

package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// chatCopyItem copies the whole selected item.
func (m *Model) chatCopyItem() tea.Cmd {
	msg, ok := m.chatSelectedMessage()
	if !ok {
		return m.copyNothing("nothing to copy")
	}
	return m.copyToClipboard(rawMessageText(msg), "")
}

// chatCopyCode copies the fenced code blocks of the selected item and
// nothing else — the case where the operator wants to RUN what the
// assistant wrote, and the prose around it is in the way.
//
// Every block in the item, joined by a blank line, rather than the
// first one or the biggest one. A turn that answers "how do I do X"
// with a config file and then the command that reads it means both,
// and picking one for the operator would be guessing; a second cursor
// for choosing between them is a whole navigation model for a case
// that is mostly "there is exactly one".
func (m *Model) chatCopyCode() tea.Cmd {
	msg, ok := m.chatSelectedMessage()
	if !ok {
		return m.copyNothing("nothing to copy")
	}
	blocks := fencedCodeBlocks(rawMessageText(msg))
	if len(blocks) == 0 {
		// Deliberately not "copy the item instead". A key that copies
		// something else when it can't do what it says is worse than a
		// key that declines: the clipboard is not inspectable from
		// here, so the operator finds out on paste.
		return m.copyNothing("no code block in this item")
	}
	what := fmt.Sprintf("%d code blocks, ", len(blocks))
	if len(blocks) == 1 {
		what = "code block, "
	}
	return m.copyToClipboard(strings.Join(blocks, "\n\n"), what)
}

// copyToClipboard hands text to the terminal and leaves a notice
// saying what went. what is a prefix naming the kind of thing copied
// ("" for a whole item); the line count comes from the text itself,
// because that is the number that tells the operator whether they got
// what they were pointing at.
func (m *Model) copyToClipboard(text, what string) tea.Cmd {
	text = strings.TrimRight(text, "\n")
	if strings.TrimSpace(text) == "" {
		return m.copyNothing("nothing to copy")
	}
	lines := strings.Count(text, "\n") + 1
	unit := "lines"
	if lines == 1 {
		unit = "line"
	}
	m.copyNotice = fmt.Sprintf("copied %s%d %s", what, lines, unit)
	return tea.SetClipboard(text)
}

// copyNothing reports why a copy did not happen. Returns a nil Cmd so
// the caller can `return m.copyNothing(…)` on every declining path.
func (m *Model) copyNothing(why string) tea.Cmd {
	m.copyNotice = why
	return nil
}

// rawMessageText reconstructs the source text of a history entry.
//
// Text is the source for every role but RoleTool — it is what the
// host sent and what Glamour was handed, so an assistant turn comes
// back as markdown rather than as the boxed, wrapped, highlighted
// thing on screen. Rendered is the fallback only for the rows that
// have no Text at all (a host or a resumed transcript that populated
// one and not the other); it is stripped, since ANSI in a paste
// buffer is the defect this whole path exists to avoid.
func rawMessageText(msg Message) string {
	if msg.Role == RoleTool {
		return rawToolText(msg)
	}
	if msg.Text != "" {
		return msg.Text
	}
	return ansi.Strip(msg.Rendered)
}

// rawToolText assembles a tool row from its structured fields, which
// is the only place a tool call's source survives: Text is empty on
// these rows, and everything the operator can see was computed by
// renderToolPreview from the maps below.
//
// Shape is name, then args, then outcome — the order the row itself
// reads in, so a paste into an issue reads the same way the screen
// did. marshalPretty is the same encoder the detail overlay
// (tool_detail.go) uses, which also means the escaping note in #108
// applies here: an ESC inside a payload string comes back as the
// literal characters of its JSON escape rather than as an ESC.
func rawToolText(msg Message) string {
	parts := make([]string, 0, 3)
	if msg.ToolName != "" {
		parts = append(parts, msg.ToolName)
	}
	switch {
	case len(msg.ToolArgsMap) > 0:
		parts = append(parts, marshalPretty(msg.ToolArgsMap))
	case msg.ToolArgs != "":
		// The compact hint. Lossy — it is what the row shows when the
		// structured args never arrived — but it is source, not frame.
		parts = append(parts, msg.ToolArgs)
	}
	switch {
	case msg.ToolError != "":
		parts = append(parts, "error: "+msg.ToolError)
	case len(msg.ToolResponseMap) > 0:
		parts = append(parts, marshalPretty(msg.ToolResponseMap))
	case msg.ToolPreview != "":
		parts = append(parts, ansi.Strip(msg.ToolPreview))
	}
	return strings.Join(parts, "\n")
}

// fencedCodeBlocks returns the bodies of the fenced code blocks in a
// markdown source, fences and info strings excluded.
//
// Not markdown.go's insideOpenCodeFence, which answers a different
// question for the streaming path — "does this prefix end inside a
// fence" — by counting backticks, and is documented as approximate
// because the cost of being wrong there is one extra re-render. The
// cost of being wrong here is the operator pasting the wrong text.
//
// A deliberately small scanner rather than a markdown parse. The
// whole question is "which lines are inside a fence", the renderer
// already agreed with this answer when it highlighted them, and
// pulling in a parser to answer it would make the clipboard depend on
// a second opinion about the document.
//
// An unterminated fence still yields a block: it means the turn is
// still streaming, or the model stopped mid-answer, and the lines the
// operator can see are the lines they meant to copy.
func fencedCodeBlocks(src string) []string {
	var (
		blocks []string
		body   []string
		fence  string
	)
	for _, line := range strings.Split(src, "\n") {
		marker := fenceMarker(strings.TrimLeft(line, " "))
		if fence == "" {
			if marker != "" {
				fence, body = marker, nil
			}
			continue
		}
		if closesFence(strings.TrimLeft(line, " "), marker, fence) {
			blocks = append(blocks, strings.Join(body, "\n"))
			fence = ""
			continue
		}
		body = append(body, line)
	}
	if fence != "" && len(body) > 0 {
		blocks = append(blocks, strings.Join(body, "\n"))
	}
	return blocks
}

// fenceMarker returns the run of backticks or tildes that opens or
// closes a fence, or "" when the line is not a fence. Three is the
// minimum, per CommonMark.
func fenceMarker(line string) string {
	for _, c := range []byte{'`', '~'} {
		n := 0
		for n < len(line) && line[n] == c {
			n++
		}
		if n >= 3 {
			return line[:n]
		}
	}
	return ""
}

// closesFence reports whether a line ends the open fence: same
// character, at least as long, and nothing after it. The last clause
// is what keeps a ```go line INSIDE a block — an info string can only
// appear on an opening fence, so a line carrying one is content.
func closesFence(line, marker, fence string) bool {
	if marker == "" || marker[0] != fence[0] || len(marker) < len(fence) {
		return false
	}
	return strings.TrimSpace(line[len(marker):]) == ""
}
