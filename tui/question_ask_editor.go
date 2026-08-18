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

// AskLongText — the answer that does not fit on a line (issue #255).
//
// The TUI does not grow a multi-line editor for this. It hands the
// buffer to the operator's own $VISUAL / $EDITOR, suspends itself while
// they work, and takes the file back when they exit. Two reasons, and
// the second is the one that decided it:
//
//   - An in-modal textarea would be a third text surface in this package
//     — after the composer and the elicit form's fields — and the worst
//     of the three, because it is the one an operator would be asked to
//     write paragraphs in with no keybindings of their own.
//   - The operator already has an editor, and every answer long enough
//     to want this kind is an answer they would rather write there.
//
// The suspend is tea.ExecProcess, which is new to this package. It is
// the only correct primitive for it: a child process on the same
// terminal has to have the terminal, and anything short of handing it
// over leaves two programs writing to one screen. The Program restores
// itself on exit; the callback runs off the Update loop, which is where
// the file read belongs.
//
// The editor round trip is also the second async answer in the package
// — an answer that arrives as a msg rather than as a keystroke — so it
// goes back through overlay.resolve, which is the seam stage 3 built
// for exactly this and which keeps the exactly-once latch honest.

package tui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// askEditorPreviewRows caps how much of the buffer the modal shows.
// The modal is a staging area, not a viewer: the operator is about to
// open the whole thing in something that scrolls properly.
const askEditorPreviewRows = 8

// askEditorQuestion stages a long answer: it shows what will be sent,
// opens the editor on enter, and resolves when the editor exits.
type askEditorQuestion struct {
	req AskRequest

	// buf is what the editor will open on and, after a round trip, what
	// came back. Seeded from Initial, so an agent can propose a draft.
	buf string

	// note is a one-line status under the preview: an editor that failed
	// to run, or one that came back with nothing. It is set from Update
	// (setNote) because that is where the round trip lands, and it is
	// cleared on the next launch so a stale complaint cannot outlive the
	// attempt it describes.
	note string
}

func newAskEditorQuestion(req AskRequest) *askEditorQuestion {
	return &askEditorQuestion{req: req, buf: req.Initial}
}

func (q *askEditorQuestion) ID() string { return askDialogID }

func (q *askEditorQuestion) Title() string { return askTitle(q.req) }

func (q *askEditorQuestion) legend() string {
	return keyLegend("enter edit & send", "ctrl+d decline", "esc cancel")
}

func (q *askEditorQuestion) Footer() string { return q.legend() }

func (q *askEditorQuestion) Width(avail int) int { return askWidth(avail) }

// Commits implements gracedQuestion. enter is held by the grace window
// like every other committing key — and here it is the one that hands
// the terminal to another program, which is the last thing a stray
// keystroke arriving with the modal should be able to do.
func (q *askEditorQuestion) Commits(msg tea.KeyPressMsg) bool {
	switch msg.String() {
	case "enter", "ctrl+d":
		return true
	}
	return false
}

func (q *askEditorQuestion) Key(msg tea.KeyPressMsg) (answer, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return dismissed{Reason: dismissEscape}, nil
	case "ctrl+d":
		return declined{}, nil
	case "enter":
		q.note = ""
		// No answer yet: the Cmd suspends the Program, and what the
		// operator writes comes back as askEditorDoneMsg. The question
		// stays open and latched-unresolved throughout, which is what
		// makes an editor that crashes a recoverable state rather than a
		// question nobody can answer.
		return nil, askEditorCmd(q.buf)
	}
	return nil, nil
}

func (q *askEditorQuestion) Body(width, _ int, st styleSet) string {
	lines := promptLines(q.req.Prompt, modalInnerWidth(width), st)

	if strings.TrimSpace(q.buf) == "" {
		lines = append(lines, st.Muted.Render("(empty — enter opens "+askEditorName()+")"))
	} else {
		lines = append(lines, st.Muted.Render("Draft, editable in "+askEditorName()+":"))
		lines = append(lines, askEditorPreview(q.buf, st)...)
	}
	if q.note != "" {
		lines = append(lines, "", st.ErrorText.Render(GlyphWarn+" "+q.note))
	}
	return strings.Join(lines, "\n")
}

// setNote records the outcome of a round trip that produced no answer.
// Called from Update, which is the only place that learns of one.
func (q *askEditorQuestion) setNote(note string) { q.note = note }

// askEditorPreview renders the head of the buffer, dimmed, with a count
// of what is not shown. Trailing rows are elided rather than the middle
// because the operator is reading to confirm they are about to edit the
// right draft, and the opening lines are what tells them that.
func askEditorPreview(buf string, st styleSet) []string {
	rows := strings.Split(buf, "\n")
	shown := rows
	hidden := 0
	if len(rows) > askEditorPreviewRows {
		shown, hidden = rows[:askEditorPreviewRows], len(rows)-askEditorPreviewRows
	}
	out := make([]string, 0, len(shown)+1)
	for _, row := range shown {
		out = append(out, st.Muted.Render("  "+row))
	}
	if hidden > 0 {
		out = append(out, st.Muted.Render(
			fmt.Sprintf("  … %d more line%s", hidden, plural(hidden))))
	}
	return out
}

// askEditorName is what the modal calls the editor: the operator's own
// command, so the prompt names the program that is about to take the
// screen rather than an environment variable they then have to go and
// read. Falls back to the variable names when neither is set, which
// only shows up in a preview built for a request supportedAsk would
// have refused.
func askEditorName() string {
	if argv := editorArgv(); len(argv) > 0 {
		return argv[0]
	}
	return "$EDITOR"
}

// editorArgv resolves the operator's editor command, $VISUAL first —
// the long-standing convention that VISUAL is the full-screen editor
// and EDITOR the line-oriented fallback, and this surface is the
// full-screen case.
//
// Fields, not a shell: a value like "code -w" has to reach exec as two
// arguments, and running it through a shell instead would make the
// contents of an environment variable a command line. Quoting is not
// supported, and the cost of that is an editor path containing a space,
// which is worth less than the shell is.
//
// Read at every call rather than cached, because supportedAsk's screen
// and this launch have to agree, and a host may set the variable after
// the TUI starts.
func editorArgv() []string {
	for _, name := range []string{"VISUAL", "EDITOR"} {
		if argv := strings.Fields(os.Getenv(name)); len(argv) > 0 {
			return argv
		}
	}
	return nil
}

// askEditorCmd writes the buffer to a temp file, hands the terminal to
// the editor, and reports back what came out.
//
// Everything that can fail is reported as askEditorDoneMsg{err} rather
// than as a silent no-op: the question is open and the operator is
// waiting on it, so an editor that never launched has to say so on
// screen or the modal looks hung.
func askEditorCmd(buf string) tea.Cmd {
	fail := func(err error) tea.Cmd {
		return func() tea.Msg { return askEditorDoneMsg{err: err} }
	}

	argv := editorArgv()
	if len(argv) == 0 {
		return fail(errors.New("no editor is configured ($VISUAL / $EDITOR are unset)"))
	}
	// .md because the answer is prose and every editor's markdown mode
	// is a better default for it than its plain-text one.
	f, err := os.CreateTemp("", "core-tui-ask-*.md")
	if err != nil {
		return fail(err)
	}
	path := f.Name()
	if _, err := f.WriteString(buf); err != nil {
		f.Close()
		os.Remove(path)
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return fail(err)
	}

	args := append(append([]string{}, argv[1:]...), path)
	// #nosec G204 -- the argv is the operator's own $VISUAL / $EDITOR,
	// which is theirs to set and already runs on their behalf every time
	// they type `git commit`. It is exec'd directly, never via a shell.
	c := exec.Command(argv[0], args...)
	return tea.ExecProcess(c, func(runErr error) tea.Msg {
		// The read happens here, in the callback, rather than back in
		// Update: this runs off the loop, and a slow filesystem should
		// not be a stalled frame.
		defer os.Remove(path)
		if runErr != nil {
			return askEditorDoneMsg{err: runErr}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return askEditorDoneMsg{err: err}
		}
		return askEditorDoneMsg{text: strings.TrimSpace(string(data))}
	})
}

var (
	_ question       = (*askEditorQuestion)(nil)
	_ askSurface     = (*askEditorQuestion)(nil)
	_ gracedQuestion = (*askEditorQuestion)(nil)
)
